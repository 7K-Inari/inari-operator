package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// dnsEndpointGVK is the ExternalDNS DNSEndpoint CRD (external-dns must be
// installed on the platform cluster; we render endpoints, it syncs them).
var dnsEndpointGVK = schema.GroupVersionKind{
	Group:   "externaldns.k8s.io",
	Version: "v1alpha1",
	Kind:    "DNSEndpoint",
}

// DNSZoneReconciler reconciles DNSZone Catalog Items (§5.6).
type DNSZoneReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=dnszones,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=dnszones/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=dnszones/finalizers,verbs=update

func (r *DNSZoneReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var zone platformv1alpha1.DNSZone
	if err := r.Get(ctx, req.NamespacedName, &zone); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !zone.DeletionTimestamp.IsZero() {
		if _, err := finalize(ctx, r.Client, &zone, nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &zone)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	if statusConditionsReady(zone.Status.Conditions, zone.Generation) &&
		zone.Status.ZoneName == zone.Spec.ZoneName {
		return ctrl.Result{}, nil
	}

	zone.Status.ZoneName = zone.Spec.ZoneName
	zone.Status.ObservedGeneration = zone.Generation
	platformv1alpha1.SetReady(&zone.Status.Conditions, zone.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("zone %q delegated to tenant %q", zone.Spec.ZoneName, zone.Spec.TenantID))
	if err := r.Status().Update(ctx, &zone); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	event(r.Recorder, &zone, corev1.EventTypeNormal, "Ready",
		fmt.Sprintf("DNS zone %q delegated to tenant %q", zone.Spec.ZoneName, zone.Spec.TenantID))
	return ctrl.Result{}, nil
}

func (r *DNSZoneReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.DNSZone{}).
		Complete(r)
}

// DNSRecordReconciler renders ExternalDNS DNSEndpoints from DNSRecord Catalog
// Items (§5.6). Records must fall inside the referenced tenant zone.
type DNSRecordReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=dnsrecords,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=dnsrecords/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=dnsrecords/finalizers,verbs=update
// +kubebuilder:rbac:groups=externaldns.k8s.io,resources=dnsendpoints,verbs=get;list;watch;create;update;patch;delete

func (r *DNSRecordReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var rec platformv1alpha1.DNSRecord
	if err := r.Get(ctx, req.NamespacedName, &rec); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// DNSEndpoint is created in the CR's namespace with an owner reference, so
	// garbage collection tears it down; no finalizer cleanup needed beyond the
	// standard one.
	if !rec.DeletionTimestamp.IsZero() {
		if _, err := finalize(ctx, r.Client, &rec, nil); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &rec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	var zone platformv1alpha1.DNSZone
	if err := r.Get(ctx, types.NamespacedName{Namespace: rec.Namespace, Name: rec.Spec.ZoneRef}, &zone); err != nil {
		platformv1alpha1.SetFailed(&rec.Status.Conditions, rec.Generation, "referenced DNSZone not found")
		_ = r.Status().Update(ctx, &rec)
		if apierrors.IsNotFound(err) {
			// Zone may appear later; poll until it does.
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	for _, ep := range rec.Spec.Endpoints {
		if !dnsNamesInZone(ep.DNSName, zone.Spec.ZoneName) {
			err := fmt.Errorf("dnsName %q outside zone %q", ep.DNSName, zone.Spec.ZoneName)
			platformv1alpha1.SetFailed(&rec.Status.Conditions, rec.Generation, err.Error())
			_ = r.Status().Update(ctx, &rec)
			event(r.Recorder, &rec, corev1.EventTypeWarning, "InvalidEndpoint", err.Error())
			return ctrl.Result{}, nil // spec error; wait for user fix
		}
	}

	endpoint := &unstructured.Unstructured{}
	endpoint.SetGroupVersionKind(dnsEndpointGVK)
	endpoint.SetName(rec.Name)
	endpoint.SetNamespace(rec.Namespace)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, endpoint, func() error {
		if err := setOwner(r.Scheme, &rec, endpoint); err != nil {
			return err
		}
		labels := endpoint.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[tenantLabel] = rec.Spec.TenantID
		endpoint.SetLabels(labels)

		eps := make([]any, 0, len(rec.Spec.Endpoints))
		for _, e := range rec.Spec.Endpoints {
			targets := make([]any, 0, len(e.Targets))
			for _, t := range e.Targets {
				targets = append(targets, t)
			}
			eps = append(eps, map[string]any{
				"dnsName":    e.DNSName,
				"recordType": e.RecordType,
				"targets":    targets,
				"recordTTL":  e.TTL,
			})
		}
		return unstructured.SetNestedSlice(endpoint.Object, eps, "spec", "endpoints")
	}); err != nil {
		platformv1alpha1.SetFailed(&rec.Status.Conditions, rec.Generation, err.Error())
		_ = r.Status().Update(ctx, &rec)
		return ctrl.Result{}, fmt.Errorf("render DNSEndpoint: %w", err)
	}

	rec.Status.ObservedGeneration = rec.Generation
	platformv1alpha1.SetReady(&rec.Status.Conditions, rec.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("%d endpoint(s) rendered for zone %q", len(rec.Spec.Endpoints), zone.Spec.ZoneName))
	if err := r.Status().Update(ctx, &rec); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	event(r.Recorder, &rec, corev1.EventTypeNormal, "Rendered",
		fmt.Sprintf("DNSEndpoint %q rendered for tenant %q", rec.Name, rec.Spec.TenantID))
	logger.Info("reconciled DNSRecord", "tenant", rec.Spec.TenantID, "name", rec.Name)
	return ctrl.Result{}, nil
}

func (r *DNSRecordReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.DNSRecord{}).
		Complete(r)
}

// dnsNamesInZone reports whether name equals zone or is a subdomain of it.
func dnsNamesInZone(name, zone string) bool {
	name = strings.TrimSuffix(name, ".")
	zone = strings.TrimSuffix(zone, ".")
	return name == zone || strings.HasSuffix(name, "."+zone)
}
