package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KeycloakRealmSpec defines a tenant Keycloak realm (plan §5.4). Tenant
// realms serve workload federation only — never platform user identity.
type KeycloakRealmSpec struct {
	TenantReference `json:",inline"`

	// Realm is the Keycloak realm name. Defaults to the tenant ID.
	// +optional
	Realm string `json:"realm,omitempty"`

	// DisplayName is the human-friendly realm name.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Enabled controls whether the realm is active.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`
}

// KeycloakRealmStatus exposes observed state for the Resources Inventory.
type KeycloakRealmStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Realm is the effective realm name created in Keycloak.
	// +optional
	Realm string `json:"realm,omitempty"`

	// IssuerURL is the OIDC issuer URL of the realm.
	// +optional
	IssuerURL string `json:"issuerURL,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=kcr
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Realm",type=string,JSONPath=`.status.realm`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// KeycloakRealm is a platform-scoped Catalog Item provisioning a tenant
// Keycloak realm for workload federation (§5.4, §5.6).
type KeycloakRealm struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KeycloakRealmSpec   `json:"spec,omitempty"`
	Status KeycloakRealmStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KeycloakRealmList contains a list of KeycloakRealm.
type KeycloakRealmList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KeycloakRealm `json:"items"`
}
