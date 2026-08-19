package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TenantNamespaceSpec defines a tenant namespace on the platform cluster with
// RBAC bootstrap, for tenants allowed to run workloads centrally (§5.6).
type TenantNamespaceSpec struct {
	TenantReference `json:",inline"`

	// AdminGroup is the identity group granted admin inside the tenant
	// namespace (subjects of the tenant-admin RoleBinding).
	// +optional
	AdminGroup string `json:"adminGroup,omitempty"`

	// ResourceQuota optionally constrains the namespace.
	// +optional
	ResourceQuota *TenantResourceQuota `json:"resourceQuota,omitempty"`
}

// TenantResourceQuota constrains the tenant namespace.
type TenantResourceQuota struct {
	// +optional
	CPU string `json:"cpu,omitempty"`
	// +optional
	Memory string `json:"memory,omitempty"`
	// +optional
	Pods int32 `json:"pods,omitempty"`
}

// TenantNamespaceStatus exposes observed state for the Resources Inventory.
type TenantNamespaceStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Namespace is the materialized namespace name.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Namespace",type=string,JSONPath=`.status.namespace`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// TenantNamespace is a platform-scoped Catalog Item materializing a tenant
// namespace on the platform cluster with RBAC bootstrap (§5.6).
type TenantNamespace struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   TenantNamespaceSpec   `json:"spec,omitempty"`
	Status TenantNamespaceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// TenantNamespaceList contains a list of TenantNamespace.
type TenantNamespaceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []TenantNamespace `json:"items"`
}
