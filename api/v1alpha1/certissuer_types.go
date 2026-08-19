package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CertIssuerSpec defines a tenant certificate issuer rendered as a
// cert-manager ClusterIssuer (§5.6).
type CertIssuerSpec struct {
	TenantReference `json:",inline"`

	// ACME configures an ACME (e.g. Let's Encrypt) issuer. Mutually exclusive
	// with CA.
	// +optional
	ACME *ACMEIssuerSpec `json:"acme,omitempty"`

	// CA configures a CA issuer backed by an existing Secret holding a
	// signing keypair. Mutually exclusive with ACME.
	// +optional
	CA *CAIssuerSpec `json:"ca,omitempty"`
}

// ACMEIssuerSpec configures an ACME ClusterIssuer.
type ACMEIssuerSpec struct {
	// Server is the ACME directory URL.
	// +kubebuilder:validation:Required
	Server string `json:"server"`

	// Email is the ACME account contact.
	// +kubebuilder:validation:Required
	Email string `json:"email"`

	// IngressClass solves HTTP-01 challenges via this ingress class.
	// +kubebuilder:validation:Required
	IngressClass string `json:"ingressClass"`
}

// CAIssuerSpec configures a CA ClusterIssuer.
type CAIssuerSpec struct {
	// SecretName references the Secret (in cert-manager's namespace) holding
	// tls.crt/tls.key for the signing CA.
	// +kubebuilder:validation:Required
	SecretName string `json:"secretName"`
}

// CertIssuerStatus exposes observed state for the Resources Inventory.
type CertIssuerStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ClusterIssuerName is the rendered cert-manager ClusterIssuer.
	// +optional
	ClusterIssuerName string `json:"clusterIssuerName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="ClusterIssuer",type=string,JSONPath=`.status.clusterIssuerName`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// CertIssuer is a platform-scoped Catalog Item rendering a cert-manager
// ClusterIssuer for a tenant (§5.6).
type CertIssuer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertIssuerSpec   `json:"spec,omitempty"`
	Status CertIssuerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CertIssuerList contains a list of CertIssuer.
type CertIssuerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CertIssuer `json:"items"`
}
