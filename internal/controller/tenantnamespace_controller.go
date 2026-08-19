package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// TenantNamespaceReconciler materializes tenant namespaces on the platform
// cluster with RBAC bootstrap (§5.6): tenant-admin RoleBinding, default-deny
// NetworkPolicy, optional ResourceQuota. The namespace is cluster-scoped, so
// teardown is driven by the finalizer.
type TenantNamespaceReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=tenantnamespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=tenantnamespaces/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=tenantnamespaces/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=resourcequotas,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=rolebindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch;create;update;patch;delete

func (r *TenantNamespaceReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var tn platformv1alpha1.TenantNamespace
	if err := r.Get(ctx, req.NamespacedName, &tn); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	nsName := tn.Spec.Namespace

	if !tn.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &tn, func(ctx context.Context) error {
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
			return client.IgnoreNotFound(r.Delete(ctx, ns))
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &tn, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("namespace %q deleted for tenant %q", nsName, tn.Spec.TenantID))
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &tn)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: nsName}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, ns, func() error {
		if ns.Labels == nil {
			ns.Labels = map[string]string{}
		}
		ns.Labels[tenantLabel] = tn.Spec.TenantID
		return nil
	}); err != nil {
		return r.fail(ctx, &tn, err)
	}

	if err := r.reconcileRBAC(ctx, &tn); err != nil {
		return r.fail(ctx, &tn, err)
	}
	if err := r.reconcileNetworkPolicy(ctx, &tn); err != nil {
		return r.fail(ctx, &tn, err)
	}
	if err := r.reconcileQuota(ctx, &tn); err != nil {
		return r.fail(ctx, &tn, err)
	}

	tn.Status.Namespace = nsName
	tn.Status.ObservedGeneration = tn.Generation
	platformv1alpha1.SetReady(&tn.Status.Conditions, tn.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("namespace %q materialized", nsName))
	if err := r.Status().Update(ctx, &tn); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	event(r.Recorder, &tn, corev1.EventTypeNormal, "Materialized",
		fmt.Sprintf("namespace %q with RBAC bootstrap materialized for tenant %q", nsName, tn.Spec.TenantID))
	logger.Info("reconciled TenantNamespace", "tenant", tn.Spec.TenantID, "namespace", nsName)
	return ctrl.Result{}, nil
}

func (r *TenantNamespaceReconciler) fail(ctx context.Context, tn *platformv1alpha1.TenantNamespace, err error) (ctrl.Result, error) {
	platformv1alpha1.SetFailed(&tn.Status.Conditions, tn.Generation, err.Error())
	_ = r.Status().Update(ctx, tn)
	event(r.Recorder, tn, corev1.EventTypeWarning, "ReconcileError", err.Error())
	return ctrl.Result{}, err
}

func (r *TenantNamespaceReconciler) reconcileRBAC(ctx context.Context, tn *platformv1alpha1.TenantNamespace) error {
	subjects := []rbacv1.Subject{}
	if tn.Spec.AdminGroup != "" {
		subjects = append(subjects, rbacv1.Subject{
			Kind:     rbacv1.GroupKind,
			APIGroup: rbacv1.GroupName,
			Name:     tn.Spec.AdminGroup,
		})
	}
	if tn.Spec.ImpersonateServiceAccount != "" {
		subjects = append(subjects, rbacv1.Subject{
			Kind:      rbacv1.ServiceAccountKind,
			Name:      tn.Spec.ImpersonateServiceAccount,
			Namespace: tn.Spec.Namespace,
		})
	}

	rb := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: "tenant-admin", Namespace: tn.Spec.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		if rb.Labels == nil {
			rb.Labels = map[string]string{}
		}
		rb.Labels[tenantLabel] = tn.Spec.TenantID
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: rbacv1.GroupName,
			Kind:     "ClusterRole",
			Name:     "admin",
		}
		rb.Subjects = subjects
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile tenant-admin RoleBinding: %w", err)
	}
	return nil
}

func (r *TenantNamespaceReconciler) reconcileNetworkPolicy(ctx context.Context, tn *platformv1alpha1.TenantNamespace) error {
	np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: "tenant-default-deny", Namespace: tn.Spec.Namespace}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		if np.Labels == nil {
			np.Labels = map[string]string{}
		}
		np.Labels[tenantLabel] = tn.Spec.TenantID
		np.Spec.PodSelector = metav1.LabelSelector{}
		np.Spec.PolicyTypes = []networkingv1.PolicyType{
			networkingv1.PolicyTypeIngress,
			networkingv1.PolicyTypeEgress,
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile default-deny NetworkPolicy: %w", err)
	}
	return nil
}

func (r *TenantNamespaceReconciler) reconcileQuota(ctx context.Context, tn *platformv1alpha1.TenantNamespace) error {
	rq := &corev1.ResourceQuota{ObjectMeta: metav1.ObjectMeta{Name: "tenant-quota", Namespace: tn.Spec.Namespace}}
	if tn.Spec.ResourceQuota == nil {
		return client.IgnoreNotFound(r.Delete(ctx, rq))
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, rq, func() error {
		if rq.Labels == nil {
			rq.Labels = map[string]string{}
		}
		rq.Labels[tenantLabel] = tn.Spec.TenantID
		hard := corev1.ResourceList{}
		if tn.Spec.ResourceQuota.CPU != "" {
			q, err := resource.ParseQuantity(tn.Spec.ResourceQuota.CPU)
			if err != nil {
				return fmt.Errorf("invalid resourceQuota.cpu %q: %w", tn.Spec.ResourceQuota.CPU, err)
			}
			hard[corev1.ResourceRequestsCPU] = q
			hard[corev1.ResourceLimitsCPU] = q
		}
		if tn.Spec.ResourceQuota.Memory != "" {
			q, err := resource.ParseQuantity(tn.Spec.ResourceQuota.Memory)
			if err != nil {
				return fmt.Errorf("invalid resourceQuota.memory %q: %w", tn.Spec.ResourceQuota.Memory, err)
			}
			hard[corev1.ResourceRequestsMemory] = q
			hard[corev1.ResourceLimitsMemory] = q
		}
		if tn.Spec.ResourceQuota.Pods > 0 {
			hard[corev1.ResourcePods] = *resource.NewQuantity(int64(tn.Spec.ResourceQuota.Pods), resource.DecimalSI)
		}
		rq.Spec.Hard = hard
		return nil
	})
	if err != nil {
		return fmt.Errorf("reconcile ResourceQuota: %w", err)
	}
	return nil
}

func (r *TenantNamespaceReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.TenantNamespace{}).
		Complete(r)
}
