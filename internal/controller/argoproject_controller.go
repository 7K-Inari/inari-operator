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

// appProjectGVK is the ArgoCD AppProject CRD (ArgoCD must be installed on the
// platform cluster).
var appProjectGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "AppProject",
}

// ArgoProjectReconciler renders shared ArgoCD AppProjects from ArgoProject
// Catalog Items (§5.6). AppProjects live in the ArgoCD namespace, so cleanup
// is driven by the finalizer.
type ArgoProjectReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// ArgoCDNamespace is where ArgoCD AppProjects live (default "argocd").
	ArgoCDNamespace string
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=argoprojects,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=argoprojects/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=argoprojects/finalizers,verbs=update
// +kubebuilder:rbac:groups=argoproj.io,resources=appprojects,verbs=get;list;watch;create;update;patch;delete

func (r *ArgoProjectReconciler) argoNS() string {
	if r.ArgoCDNamespace == "" {
		return "argocd"
	}
	return r.ArgoCDNamespace
}

func argoProjectName(cr *platformv1alpha1.ArgoProject) string {
	return fmt.Sprintf("tenant-%s-%s", cr.Spec.TenantID, cr.Name)
}

func (r *ArgoProjectReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var ap platformv1alpha1.ArgoProject
	if err := r.Get(ctx, req.NamespacedName, &ap); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	name := argoProjectName(&ap)

	if !ap.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &ap, func(ctx context.Context) error {
			child := &unstructured.Unstructured{}
			child.SetGroupVersionKind(appProjectGVK)
			child.SetName(name)
			child.SetNamespace(r.argoNS())
			return client.IgnoreNotFound(r.Delete(ctx, child))
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &ap, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("AppProject %q deleted for tenant %q", name, ap.Spec.TenantID))
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &ap)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	destNS := ap.Spec.DestinationNamespaces
	if len(destNS) == 0 {
		destNS = []string{ap.Spec.Namespace}
	}

	repos := make([]any, 0, len(ap.Spec.SourceRepos))
	for _, s := range ap.Spec.SourceRepos {
		repos = append(repos, s)
	}
	dests := make([]any, 0, len(destNS))
	for _, ns := range destNS {
		dests = append(dests, map[string]any{"server": "*", "namespace": ns})
	}

	child := &unstructured.Unstructured{}
	child.SetGroupVersionKind(appProjectGVK)
	child.SetName(name)
	child.SetNamespace(r.argoNS())
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, child, func() error {
		desc := ap.Spec.Description
		if desc == "" {
			desc = fmt.Sprintf("Tenant %s project (managed by inari-operator)", ap.Spec.TenantID)
		}
		child.Object["spec"] = map[string]any{
			"description":  desc,
			"sourceRepos":  repos,
			"destinations": dests,
			"clusterResourceWhitelist": []any{
				map[string]any{"group": "*", "kind": "*"},
			},
			"namespaceResourceWhitelist": []any{
				map[string]any{"group": "*", "kind": "*"},
			},
		}
		labels := child.GetLabels()
		if labels == nil {
			labels = map[string]string{}
		}
		labels[tenantLabel] = ap.Spec.TenantID
		child.SetLabels(labels)
		return nil
	}); err != nil {
		platformv1alpha1.SetFailed(&ap.Status.Conditions, ap.Generation, err.Error())
		_ = r.Status().Update(ctx, &ap)
		return ctrl.Result{}, fmt.Errorf("render AppProject: %w", err)
	}

	ap.Status.ProjectName = name
	ap.Status.ObservedGeneration = ap.Generation
	platformv1alpha1.SetReady(&ap.Status.Conditions, ap.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("AppProject %q rendered", name))
	if err := r.Status().Update(ctx, &ap); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	event(r.Recorder, &ap, corev1.EventTypeNormal, "Rendered",
		fmt.Sprintf("AppProject %q rendered for tenant %q", name, ap.Spec.TenantID))
	logger.Info("reconciled ArgoProject", "tenant", ap.Spec.TenantID, "project", name)
	return ctrl.Result{}, nil
}

func (r *ArgoProjectReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.ArgoProject{}).
		Complete(r)
}
