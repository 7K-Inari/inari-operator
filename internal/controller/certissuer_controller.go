package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// clusterIssuerGVK is the cert-manager ClusterIssuer CRD (cert-manager must
// be installed on the platform cluster).
var clusterIssuerGVK = schema.GroupVersionKind{
	Group:   "cert-manager.io",
	Version: "v1",
	Kind:    "ClusterIssuer",
}

// CertIssuerReconciler renders cert-manager ClusterIssuers from CertIssuer
// Catalog Items (§5.6). The child is cluster-scoped, so cleanup is driven by
// the finalizer rather than an owner reference.
type CertIssuerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=certissuers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=certissuers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=certissuers/finalizers,verbs=update
// +kubebuilder:rbac:groups=cert-manager.io,resources=clusterissuers,verbs=get;list;watch;create;update;patch;delete

// clusterIssuerName derives a stable, tenant-scoped ClusterIssuer name.
func clusterIssuerName(cr *platformv1alpha1.CertIssuer) string {
	return tenantChildName("tenant", cr.Spec.TenantID, cr.Name)
}

func buildClusterIssuerSpec(cr *platformv1alpha1.CertIssuer) (map[string]any, error) {
	switch {
	case cr.Spec.ACME != nil && cr.Spec.CA != nil:
		return nil, fmt.Errorf("spec.acme and spec.ca are mutually exclusive")
	case cr.Spec.ACME != nil:
		return map[string]any{
			"acme": map[string]any{
				"server": cr.Spec.ACME.Server,
				"email":  cr.Spec.ACME.Email,
				"privateKeySecretRef": map[string]any{
					"name": clusterIssuerName(cr) + "-account-key",
				},
				"solvers": []any{
					map[string]any{
						"http01": map[string]any{
							"ingress": map[string]any{"ingressClassName": cr.Spec.ACME.IngressClass},
						},
					},
				},
			},
		}, nil
	case cr.Spec.CA != nil:
		return map[string]any{
			"ca": map[string]any{"secretName": cr.Spec.CA.SecretName},
		}, nil
	default:
		return nil, fmt.Errorf("one of spec.acme or spec.ca is required")
	}
}

func (r *CertIssuerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ci platformv1alpha1.CertIssuer
	if err := r.Get(ctx, req.NamespacedName, &ci); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	name := clusterIssuerName(&ci)

	if !ci.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &ci, func(ctx context.Context) error {
			child := &unstructured.Unstructured{}
			child.SetGroupVersionKind(clusterIssuerGVK)
			child.SetName(name)
			return client.IgnoreNotFound(r.Delete(ctx, child))
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &ci, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("ClusterIssuer %q deleted for tenant %q", name, ci.Spec.TenantID))
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &ci)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	child := &unstructured.Unstructured{}
	child.SetGroupVersionKind(clusterIssuerGVK)
	child.SetName(name)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, child, func() error {
		spec, err := buildClusterIssuerSpec(&ci)
		if err != nil {
			return err
		}
		child.Object["spec"] = spec
		labels := child.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[tenantLabel] = ci.Spec.TenantID
		child.SetLabels(labels)
		return nil
	}); err != nil {
		platformv1alpha1.SetFailed(&ci.Status.Conditions, ci.Generation, err.Error())
		_ = r.Status().Update(ctx, &ci)
		event(r.Recorder, &ci, corev1.EventTypeWarning, "ReconcileError", err.Error())
		return ctrl.Result{}, nil // spec errors wait for user fix
	}

	ci.Status.ClusterIssuerName = name
	ci.Status.ObservedGeneration = ci.Generation
	platformv1alpha1.SetReady(&ci.Status.Conditions, ci.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("ClusterIssuer %q rendered", name))
	if err := r.Status().Update(ctx, &ci); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	event(r.Recorder, &ci, corev1.EventTypeNormal, "Rendered",
		fmt.Sprintf("ClusterIssuer %q rendered for tenant %q", name, ci.Spec.TenantID))
	logger.Info("reconciled CertIssuer", "tenant", ci.Spec.TenantID, "clusterIssuer", name)
	return ctrl.Result{}, nil
}

func (r *CertIssuerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.CertIssuer{}).
		Complete(r)
}
