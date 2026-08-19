package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ArgoProjectSpec defines a shared ArgoCD AppProject on the platform cluster
// restricted to a tenant's sources and destinations (§5.6).
type ArgoProjectSpec struct {
	TenantReference `json:",inline"`

	// SourceRepos restricts which Git repositories Applications may use.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	SourceRepos []string `json:"sourceRepos"`

	// DestinationNamespaces restricts which namespaces Applications may
	// deploy into. Defaults to the tenant namespace.
	// +optional
	DestinationNamespaces []string `json:"destinationNamespaces,omitempty"`

	// Description is shown in ArgoCD.
	// +optional
	Description string `json:"description,omitempty"`
}

// ArgoProjectStatus exposes observed state for the Resources Inventory.
type ArgoProjectStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ProjectName is the rendered ArgoCD AppProject name.
	// +optional
	ProjectName string `json:"projectName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Project",type=string,JSONPath=`.status.projectName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// ArgoProject is a platform-scoped Catalog Item rendering a shared ArgoCD
// AppProject scoped to a tenant (§5.6).
type ArgoProject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ArgoProjectSpec   `json:"spec,omitempty"`
	Status ArgoProjectStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ArgoProjectList contains a list of ArgoProject.
type ArgoProjectList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ArgoProject `json:"items"`
}
