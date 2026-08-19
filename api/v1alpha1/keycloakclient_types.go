package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KeycloakClientSpec defines a tenant OIDC client inside a tenant realm
// (§5.4). Confidential clients get their secret written into the tenant
// namespace.
type KeycloakClientSpec struct {
	TenantReference `json:",inline"`

	// RealmRef references the KeycloakRealm this client belongs to.
	// +kubebuilder:validation:Required
	RealmRef string `json:"realmRef"`

	// ClientID is the OIDC client identifier. Defaults to the CR name.
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// Public marks the client as public (no secret). Default is confidential.
	// +kubebuilder:default=false
	Public bool `json:"public"`

	// RedirectURIs allowed for authorization code flows.
	// +optional
	RedirectURIs []string `json:"redirectURIs,omitempty"`

	// ServiceAccountsEnabled enables the client credentials grant for
	// workload-to-workload federation.
	// +kubebuilder:default=true
	ServiceAccountsEnabled bool `json:"serviceAccountsEnabled"`

	// SecretName is the name of the Secret written into the tenant namespace
	// holding the client credentials. Defaults to "<name>-keycloak-client".
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// KeycloakClientStatus exposes observed state for the Resources Inventory.
type KeycloakClientStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ClientID is the effective client identifier.
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// Realm is the Keycloak realm the client was provisioned into. Recorded
	// so teardown targets the right realm even if the realm CR is gone.
	// +optional
	Realm string `json:"realm,omitempty"`

	// SecretName is the tenant-namespace Secret holding credentials.
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=kcc
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="ClientID",type=string,JSONPath=`.status.clientID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// KeycloakClient is a platform-scoped Catalog Item provisioning an OIDC
// client in a tenant Keycloak realm for workload federation (§5.4, §5.6).
type KeycloakClient struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   KeycloakClientSpec   `json:"spec,omitempty"`
	Status KeycloakClientStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// KeycloakClientList contains a list of KeycloakClient.
type KeycloakClientList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []KeycloakClient `json:"items"`
}
