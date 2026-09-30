package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
// renders a single-source ArgoCD Application deploying the cluster-local Dex
// from the official dexidp chart (config mounted from an ESO/Vault-synced
// Secret, configSecret.create=false) plus the ArgoCD OIDC/RBAC baseline
// (argocd-cm, argocd-rbac-cm) into the ArgoCD namespace. Dex federates to
// platform Keycloak via the cluster-<id>-dex client; ArgoCD is pointed at the
// cluster-local Dex issuer only — never Keycloak directly. The operator never
// reads secret values.
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
// +kubebuilder:rbac:groups=argoproj.io,resources=applications,verbs=get;list;watch;create;update;patch;delete

func (r *ClusterDexReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var cr platformv1alpha1.ClusterDex
	if err := r.Get(ctx, req.NamespacedName, &cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !cr.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &cr, func(ctx context.Context) error {
			return r.deleteChildren(ctx, &cr)
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &cr, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("Dex baseline deleted for tenant %q cluster %q", cr.Spec.TenantID, cr.Spec.ClusterID))
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

	// Disabled baseline: remove any rendered children and report Ready. The
	// cluster keeps its pre-existing (break-glass/static-token) setup.
	if !cr.Spec.IsEnabled() {
		if err := r.deleteChildren(ctx, &cr); err != nil {
			return ctrl.Result{}, err
		}
		return r.ready(ctx, &cr, "", fmt.Sprintf("Dex baseline disabled for cluster %q", cr.Spec.ClusterID))
	}

	// Verify the referenced secrets exist — reference only: metadata and key
	// presence, never the values (they must not reach logs, events, status,
	// or rendered output).
	if res, ok, err := r.checkSecret(ctx, &cr, cr.Spec.ClientSecretRef, secretRefNamespace(&cr, cr.Spec.ClientSecretRef),
		[]string{"client-id", "client-secret"}, "W2 cluster-<id>-dex provisioning"); !ok || err != nil {
		return res, err
	}
	if cr.Spec.Dex == nil || cr.Spec.Dex.ConfigSecretRef == nil {
		return r.waiting(ctx, &cr, "spec.dex.configSecretRef is required: Dex config comes from an ESO/Vault-synced Secret (configSecret.create=false)")
	}
	configRef := *cr.Spec.Dex.ConfigSecretRef
	if res, ok, err := r.checkSecret(ctx, &cr, configRef, secretRefNamespace(&cr, configRef),
		[]string{"config.yaml"}, "ESO/Vault dex config sync"); !ok || err != nil {
		return res, err
	}
	if cr.Spec.ArgoCD == nil || cr.Spec.ArgoCD.OIDCClientSecretRef == nil {
		return r.waiting(ctx, &cr, "spec.argocd.oidcClientSecretRef is required: argocd-cm references the Dex static client secret by name/key")
	}

	app, err := renderDexApplication(&cr)
	if err != nil {
		platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, err.Error())
		_ = r.Status().Update(ctx, &cr)
		return ctrl.Result{}, err
	}
	cm, rbacCM, err := renderArgoCDConfigMaps(&cr)
	if err != nil {
		platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, err.Error())
		_ = r.Status().Update(ctx, &cr)
		return ctrl.Result{}, err
	}

	wc, err := impersonatingClient(r.Client, r.RESTConfig, cr.Spec.TenantReference)
	if err != nil {
		return ctrl.Result{}, err
	}
	for _, child := range []client.Object{app, cm, rbacCM} {
		if err := r.applyChild(ctx, wc, &cr, child); err != nil {
			platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, err.Error())
			_ = r.Status().Update(ctx, &cr)
			return ctrl.Result{}, err
		}
	}

	res, err := r.ready(ctx, &cr, app.GetName(),
		fmt.Sprintf("Dex baseline %q rendered for cluster %q", app.GetName(), cr.Spec.ClusterID))
	if err != nil {
		return res, err
	}
	logger.Info("reconciled ClusterDex", "tenant", cr.Spec.TenantID, "cluster", cr.Spec.ClusterID, "application", app.GetName())
	return res, nil
}

// checkSecret verifies a referenced Secret exists and carries the required
// keys. Values are never read beyond key presence.
func (r *ClusterDexReconciler) checkSecret(ctx context.Context, cr *platformv1alpha1.ClusterDex, ref platformv1alpha1.SecretReference, namespace string, keys []string, hint string) (ctrl.Result, bool, error) {
	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Name: ref.Name, Namespace: namespace}, &secret); err != nil {
		if apierrors.IsNotFound(err) {
			res, err := r.waiting(ctx, cr, fmt.Sprintf("waiting for secret %q in namespace %q (%s)", ref.Name, namespace, hint))
			return res, false, err
		}
		return ctrl.Result{}, false, err
	}
	for _, k := range keys {
		if _, ok := secret.Data[k]; !ok {
			res, err := r.waiting(ctx, cr, fmt.Sprintf("secret %q in namespace %q is missing key %q", ref.Name, namespace, k))
			return res, false, err
		}
	}
	return ctrl.Result{}, true, nil
}

// waiting records a Failed condition and requeues without erroring, so
// rollout ordering (secrets synced after the CR) stays graceful.
func (r *ClusterDexReconciler) waiting(ctx context.Context, cr *platformv1alpha1.ClusterDex, msg string) (ctrl.Result, error) {
	platformv1alpha1.SetFailed(&cr.Status.Conditions, cr.Generation, msg)
	_ = r.Status().Update(ctx, cr)
	return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
}

// applyChild upserts one tenant child with the tenant label. The desired
// content is re-applied inside the mutate closure because CreateOrUpdate
// replaces child with the cluster state on the update path.
func (r *ClusterDexReconciler) applyChild(ctx context.Context, wc client.Client, cr *platformv1alpha1.ClusterDex, child client.Object) error {
	desired, ok := child.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("deepcopy %s %q: unexpected type", child.GetObjectKind().GroupVersionKind().Kind, child.GetName())
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, wc, child, func() error {
		switch c := child.(type) {
		case *unstructured.Unstructured:
			c.Object["spec"] = desired.(*unstructured.Unstructured).Object["spec"]
		case *corev1.ConfigMap:
			c.Data = desired.(*corev1.ConfigMap).Data
		}
		labels := child.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[tenantLabel] = cr.Spec.TenantID
		child.SetLabels(labels)
		return nil
	}); err != nil {
		return fmt.Errorf("write %s %q in namespace %q: %w", child.GetObjectKind().GroupVersionKind().Kind, child.GetName(), child.GetNamespace(), err)
	}
	return nil
}

// ready records status idempotently: when nothing changed and Ready is
// already recorded for this generation, no status write happens.
func (r *ClusterDexReconciler) ready(ctx context.Context, cr *platformv1alpha1.ClusterDex, appName, msg string) (ctrl.Result, error) {
	unchanged := cr.Status.DexApplication == appName &&
		cr.Status.DexIssuerURL == dexIssuerURL(cr) &&
		cr.Status.ClientID == cr.Spec.DexClientID() &&
		statusConditionsReady(cr.Status.Conditions, cr.Generation)
	if unchanged {
		return ctrl.Result{}, nil
	}
	cr.Status.ClientID = cr.Spec.DexClientID()
	cr.Status.DexApplication = appName
	cr.Status.DexIssuerURL = dexIssuerURL(cr)
	cr.Status.ObservedGeneration = cr.Generation
	platformv1alpha1.SetReady(&cr.Status.Conditions, cr.Generation, platformv1alpha1.ReasonReady, msg)
	if err := r.Status().Update(ctx, cr); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

// deleteChildren removes the rendered Application and ArgoCD baseline
// ConfigMaps, impersonating the tenant identity when one is configured
// (§5.6).
func (r *ClusterDexReconciler) deleteChildren(ctx context.Context, cr *platformv1alpha1.ClusterDex) error {
	wc, err := impersonatingClient(r.Client, r.RESTConfig, cr.Spec.TenantReference)
	if err != nil {
		return err
	}
	ns := argoCDNamespace(cr)
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName(dexApplicationName(cr))
	app.SetNamespace(ns)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: argoCDConfigMapName, Namespace: ns}}
	rbacCM := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: argoCDRBACConfigMapName, Namespace: ns}}
	for _, child := range []client.Object{app, cm, rbacCM} {
		if err := wc.Delete(ctx, child); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (r *ClusterDexReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ClusterDex{}).
		Complete(r)
}
