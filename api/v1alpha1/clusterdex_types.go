package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretReference points at a Secret holding sensitive material. The
// operator only ever propagates the reference — secret values are never
// read into memory, status, logs, or events.
type SecretReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the Secret. Defaults to the ClusterDex CR namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// ClusterDexArgoCD configures how the tenant-local ArgoCD is pointed at the
// cluster-local Dex issuer (ADR-0012 oidc-sso-session: never Keycloak
// directly).
type ClusterDexArgoCD struct {
	// Namespace is the ArgoCD namespace in the tenant cluster.
	// +kubebuilder:default=argocd
	Namespace string `json:"namespace"`

	// AdminGroups are Keycloak group paths (e.g. "/tenant-acme/platform-team")
	// mapped to ArgoCD role:admin.
	// +optional
	AdminGroups []string `json:"adminGroups,omitempty"`

	// ViewerGroups are Keycloak group paths mapped to ArgoCD role:readonly.
	// +optional
	ViewerGroups []string `json:"viewerGroups,omitempty"`

	// RBACPolicyExtra holds additional raw argocd-rbac-cm policy lines
	// (e.g. organization-claim scoped project roles).
	// +optional
	RBACPolicyExtra []string `json:"rbacPolicyExtra,omitempty"`
}

// ClusterDexDex configures the cluster-local Dex instance.
type ClusterDexDex struct {
	// IssuerURL overrides the cluster-local Dex issuer URL. Defaults to the
	// deterministic in-cluster service URL for this cluster.
	// +optional
	IssuerURL string `json:"issuerURL,omitempty"`

	// Image overrides the Dex container image.
	// +optional
	Image string `json:"image,omitempty"`

	// StaticTokensSecretRef references a Secret holding break-glass static
	// user/token configuration. Referenced only; never inlined.
	// +optional
	StaticTokensSecretRef *SecretReference `json:"staticTokensSecretRef,omitempty"`
}

// ClusterDexSpec defines the cluster-local Dex SSO baseline for one managed
// tenant cluster (ADR-0012): Dex federates to platform Keycloak via the
// per-cluster confidential client cluster-<clusterID>-dex provisioned by the
// server/Tenant Zone Factory path (W2).
type ClusterDexSpec struct {
	TenantReference `json:",inline"`

	// ClusterID identifies the managed tenant cluster.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	ClusterID string `json:"clusterID"`

	// KeycloakIssuerURL is the platform Keycloak issuer URL used by Dex's
	// upstream OIDC connector. Only Dex requires egress to Keycloak.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	KeycloakIssuerURL string `json:"keycloakIssuerURL"`

	// ClientSecretRef references the Secret provisioned by the W2 server
	// path holding the cluster-<clusterID>-dex client credentials
	// (keys "client-id" and "client-secret"). Referenced only — the value is
	// never copied into CRs, status, logs, or events.
	// +kubebuilder:validation:Required
	ClientSecretRef SecretReference `json:"clientSecretRef"`

	// Enabled gates rendering. When false the values bundle is removed and
	// the cluster keeps its pre-existing (break-glass/static-token) setup.
	// +optional
	Enabled *bool `json:"enabled,omitempty"`

	// Dex configures the cluster-local Dex instance.
	// +optional
	Dex *ClusterDexDex `json:"dex,omitempty"`

	// ArgoCD configures ArgoCD OIDC/RBAC against the cluster-local Dex.
	// +optional
	ArgoCD *ClusterDexArgoCD `json:"argocd,omitempty"`
}

// IsEnabled reports whether the baseline is enabled (default true).
func (s *ClusterDexSpec) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

// DexClientID derives the per-cluster Keycloak client identifier.
func (s *ClusterDexSpec) DexClientID() string {
	return "cluster-" + s.ClusterID + "-dex"
}

// ClusterDexStatus exposes observed state for the Resources Inventory.
type ClusterDexStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// ClientID is the effective Keycloak client identifier for Dex.
	// +optional
	ClientID string `json:"clientID,omitempty"`

	// DexIssuerURL is the cluster-local Dex issuer ArgoCD is configured
	// against.
	// +optional
	DexIssuerURL string `json:"dexIssuerURL,omitempty"`

	// ValuesConfigMap is the tenant-namespace ConfigMap holding the rendered
	// Helm values consumed by the tenant-zone baseline chart (W3).
	// +optional
	ValuesConfigMap string `json:"valuesConfigMap,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cdx
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// ClusterDex is a platform-scoped Catalog Item rendering the cluster-local
// Dex SSO baseline (Dex + ArgoCD OIDC/RBAC configuration) for one managed
// tenant cluster (ADR-0012). The operator renders Helm values referencing
// secrets only; the tenant-zone baseline chart owns the manifests.
type ClusterDex struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterDexSpec   `json:"spec,omitempty"`
	Status ClusterDexStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterDexList contains a list of ClusterDex.
type ClusterDexList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterDex `json:"items"`
}
