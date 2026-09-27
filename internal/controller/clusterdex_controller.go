package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// ClusterDexReconciler reconciles ClusterDex Catalog Items (ADR-0012): it
// renders the per-cluster Helm values for the cluster-local Dex SSO baseline
// (Dex federating to platform Keycloak via the cluster-<id>-dex client, plus
// ArgoCD OIDC/RBAC against the cluster-local Dex issuer) into a ConfigMap in
// the tenant namespace. The tenant-zone baseline chart (W3) consumes the
// values and owns all manifests; the operator never pushes into tenant
// clusters and never reads secret values.
type ClusterDexReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	RESTConfig *rest.Config
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=clusterdexes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=clusterdexes/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=clusterdexes/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=impersonate

func (r *ClusterDexReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr platformv1alpha1.ClusterDex
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	cmName := tenantChildName(cr.Name, "dex-values")

	if !cr.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &cr, func(ctx context.Context) error {
			cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: cr.Spec.Namespace}}
			return client.IgnoreNotFound(r.deleteChild(ctx, &cr, cm))
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &cr, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("Dex baseline values %q deleted for tenant %q cluster %q", cmName, cr.Spec.TenantID, cr.Spec.ClusterID))
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &cr)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	// Disabled baseline: remove any rendered values and report Ready. The
	// cluster keeps its pre-existing (break-glass/static-token) setup.
	if !cr.Spec.IsEnabled() {
		cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: cr.Spec.Namespace}}
		if err := r.deleteChild(ctx, &cr, cm); err != nil && !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		return r.ready(ctx, &cr, "", "", fmt.Sprintf("Dex baseline disabled for cluster %q", cr.Spec.ClusterID))
	}

	// Verify the referenced client secret exists — reference only: metadata
	// and key presence, never the values (they must not reach logs, events,
	// status, or rendered output).
	secretNS := cr.Spec.ClientSecretRef.Namespace
	if secretNS == "" {
		secretNS = cr.Namespace
	}
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: cr.Spec.ClientSecretRef.Name, Namespace: secretNS}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			msg := fmt.Sprintf("waiting for Dex client secret %q in namespace %q (W2 cluster-<id>-dex provisioning)",
				cr.Spec.ClientSecretRef.Name, secretNS)
			platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, msg)
			_ = r.Status().Update(ctx, &cr)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}
	for _, k := range []string{"client-id", "client-secret"} {
		if _, ok := secret.Data[k]; !ok {
			msg := fmt.Sprintf("Dex client secret %q in namespace %q is missing key %q", cr.Spec.ClientSecretRef.Name, secretNS, k)
			platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, msg)
			_ = r.Status().Update(ctx, &cr)
			return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
		}
	}

	values, err := renderClusterDexValues(&cr)
	if err != nil {
		platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, err.Error())
		_ = r.Status().Update(ctx, &cr)
		return ctrl.Result{}, err
	}

	wc, err := impersonatingClient(r.Client, r.RESTConfig, cr.Spec.TenantReference)
	if err != nil {
		return ctrl.Result{}, err
	}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: cmName, Namespace: cr.Spec.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, wc, cm, func() error {
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels[tenantLabel] = cr.Spec.TenantID
		cm.Data = map[string]string{"values.yaml": values}
		return nil
	}); err != nil {
		platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, err.Error())
		_ = r.Status().Update(ctx, &cr)
		return ctrl.Result{}, fmt.Errorf("write dex values ConfigMap: %w", err)
	}

	issuer := dexIssuerURL(&cr)
	res, err := r.ready(ctx, &cr, cmName, issuer,
		fmt.Sprintf("Dex baseline values %q rendered for cluster %q", cmName, cr.Spec.ClusterID))
	if err != nil {
		return res, err
	}
	logger.Info("reconciled ClusterDex", "tenant", cr.Spec.TenantID, "cluster", cr.Spec.ClusterID, "configMap", cmName)
	return res, nil
}

// ready records status idempotently: when nothing changed and Ready is
// already recorded for this generation, no status write happens.
func (r *ClusterDexReconciler) ready(ctx context.Context, cr *platformv1alpha1.ClusterDex, cmName, issuer, msg string) (ctrl.Result, error) {
	unchanged := cr.Status.ValuesConfigMap == cmName &&
		cr.Status.DexIssuerURL == issuer &&
		cr.Status.ClientID == cr.Spec.DexClientID() &&
		statusConditionsReady(cr.Status.Conditions, cr.Generation)
	if unchanged {
		return ctrl.Result{}, nil
	}
	cr.Status.ClientID = cr.Spec.DexClientID()
	cr.Status.ValuesConfigMap = cmName
	cr.Status.DexIssuerURL = issuer
	cr.Status.ObservedGeneration = cr.Generation
	platformv1alpha1.SetReady(&cr.Status.Conditions, cr.Generation, platformv1alpha1.ReasonReady, msg)
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// deleteChild removes a tenant-namespace child, impersonating the tenant
// identity when one is configured (§5.6).
func (r *ClusterDexReconciler) deleteChild(ctx context.Context, cr *platformv1alpha1.ClusterDex, obj client.Object) error {
	wc, err := impersonatingClient(r.Client, r.RESTConfig, cr.Spec.TenantReference)
	if err != nil {
		return err
	}
	return wc.Delete(ctx, obj)
}

func (r *ClusterDexReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ClusterDex{}).
		Complete(r)
}
