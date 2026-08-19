package v1alpha1

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition types shared by all platform Catalog Item resources.
const (
	ConditionReady       = "Ready"
	ConditionReconciling = "Reconciling"
	ConditionFailed      = "Failed"
)

// Condition reasons.
const (
	ReasonReconciling = "Reconciling"
	ReasonReady       = "Ready"
	ReasonError       = "ReconcileError"
	ReasonDeleting    = "Deleting"
)

// TenantReference carries the tenant identity for a platform-scoped Catalog
// Item. Every object reconciled by inari-operator is tenant-aware to the core
// (plan §5.6): lifecycle is tied to the tenant and access is namespace-isolated.
type TenantReference struct {
	// TenantID is the platform-wide tenant identifier this resource belongs to.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	TenantID string `json:"tenantID"`

	// Namespace is the tenant's namespace on the platform cluster. Tenant
	// access is namespace-isolated (§5.6); child resources the tenant may
	// observe are written here.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Namespace string `json:"namespace"`

	// ImpersonateServiceAccount is the tenant-scoped ServiceAccount the
	// operator impersonates when writing tenant-visible child resources into
	// the tenant namespace (§5.6: access mediated by impersonation).
	// +optional
	ImpersonateServiceAccount string `json:"impersonateServiceAccount,omitempty"`
}

// SetReady sets the Ready condition and clears Reconciling/Failed.
func SetReady(conditions *[]metav1.Condition, generation int64, reason, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               ConditionReady,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		Reason:             reason,
		Message:            message,
	})
	meta.RemoveStatusCondition(conditions, ConditionFailed)
	meta.RemoveStatusCondition(conditions, ConditionReconciling)
}

// SetReconciling marks the resource as being reconciled.
func SetReconciling(conditions *[]metav1.Condition, generation int64, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               ConditionReconciling,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		Reason:             ReasonReconciling,
		Message:            message,
	})
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             ReasonReconciling,
		Message:            message,
	})
}

// SetFailed records a reconcile failure.
func SetFailed(conditions *[]metav1.Condition, generation int64, message string) {
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               ConditionFailed,
		Status:             metav1.ConditionTrue,
		ObservedGeneration: generation,
		Reason:             ReasonError,
		Message:            message,
	})
	meta.SetStatusCondition(conditions, metav1.Condition{
		Type:               ConditionReady,
		Status:             metav1.ConditionFalse,
		ObservedGeneration: generation,
		Reason:             ReasonError,
		Message:            message,
	})
}
