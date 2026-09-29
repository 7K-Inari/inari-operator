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

// SecretKeyReference points at a single key inside a Secret. Referenced
// only — the value is never read by the operator.
type SecretKeyReference struct {
	// Name of the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the Secret. Defaults to the ArgoCD namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Key inside the Secret. Defaults to "client-secret".
	// +kubebuilder:default="client-secret"
	Key string `json:"key"`
}

// ClusterDexArgoCD configures how the tenant-local ArgoCD is pointed at the
// cluster-local Dex issuer (ADR-0012 oidc-sso-session: never Keycloak
// directly).
type ClusterDexArgoCD struct {
	// Namespace is the ArgoCD namespace in the tenant cluster.
	// +kubebuilder:default=argocd
	Namespace string `json:"namespace"`

	// Project is the ArgoCD AppProject the rendered Dex Application belongs
	// to. Defaults to "default".
	// +kubebuilder:default=default
	Project string `json:"project"`

	// OIDCClientSecretRef references the Secret holding the shared Dex static
	// client secret ArgoCD authenticates with (referenced in argocd-cm as
	// $<name>:<key>). Referenced only; never read.
	// +optional
	OIDCClientSecretRef *SecretKeyReference `json:"oidcClientSecretRef,omitempty"`

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

// ClusterDexGatewayRef points at an ALREADY-EXISTING Gateway the Dex
// HTTPRoute attaches to. The operator never creates a Gateway.
type ClusterDexGatewayRef struct {
	// Name of the Gateway.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Namespace of the Gateway. Defaults to the Dex namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// SectionName selects a specific listener on the Gateway.
	// +optional
	SectionName string `json:"sectionName,omitempty"`
}

// ClusterDexRoute configures the Gateway API HTTPRoute exposing Dex.
type ClusterDexRoute struct {
	// GatewayRef points at the existing Gateway. Required.
	// +kubebuilder:validation:Required
	GatewayRef ClusterDexGatewayRef `json:"gatewayRef"`

	// Hostnames the HTTPRoute matches. Empty = match all hostnames.
	// +optional
	Hostnames []string `json:"hostnames,omitempty"`

	// Annotations added to the HTTPRoute.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// ClusterDexDex configures the cluster-local Dex instance.
type ClusterDexDex struct {
	// IssuerURL overrides the cluster-local Dex issuer URL. Defaults to the
	// deterministic in-cluster service URL for this cluster.
	// +optional
	IssuerURL string `json:"issuerURL,omitempty"`

	// Image overrides the Dex container image (repository:tag).
	// +optional
	Image string `json:"image,omitempty"`

	// ChartVersion pins the official dexidp dex Helm chart version rendered
	// as the Application targetRevision. Bumped deliberately.
	// +kubebuilder:default="0.25.2"
	ChartVersion string `json:"chartVersion"`

	// ConfigSecretRef references the ESO/Vault-synced Secret holding the full
	// Dex config.yaml (key "config.yaml"). Rendered as the upstream chart's
	// configSecret.name with configSecret.create=false. Required when the
	// baseline is enabled; referenced only, never read.
	// +optional
	ConfigSecretRef *SecretReference `json:"configSecretRef,omitempty"`

	// Route exposes Dex via a Gateway API HTTPRoute on an existing Gateway.
	// +optional
	Route *ClusterDexRoute `json:"route,omitempty"`

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

	// Enabled gates rendering. When false the rendered baseline (Dex
	// Application + ArgoCD OIDC/RBAC ConfigMaps) is removed and the cluster
	// keeps its pre-existing (break-glass/static-token) setup.
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

	// DexApplication is the rendered ArgoCD Application name for this
	// cluster's Dex (official dexidp chart, single-source).
	// +optional
	DexApplication string `json:"dexApplication,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cdx
// +kubebuilder:printcolumn:name="Tenant",type=string,JSONPath=`.spec.tenantID`
// +kubebuilder:printcolumn:name="Cluster",type=string,JSONPath=`.spec.clusterID`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`

// ClusterDex is a platform-scoped Catalog Item rendering the cluster-local
// Dex SSO baseline (Dex + ArgoCD OIDC/RBAC configuration) for one managed
// tenant cluster (ADR-0012). The operator renders a single-source ArgoCD
// Application consuming the official dexidp chart plus the ArgoCD OIDC/RBAC
// ConfigMaps into the tenant cluster's ArgoCD namespace; secret material is
// referenced only, never read.
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
