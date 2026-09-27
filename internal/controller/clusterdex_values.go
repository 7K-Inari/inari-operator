package controller

import (
	"fmt"
	"strings"

	"sigs.k8s.io/yaml"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// keycloakClientSecretMarker is the placeholder the tenant-zone baseline
// chart (W3) resolves to a secretKeyRef/env reference at install time. The
// operator never renders the secret value itself.
const keycloakClientSecretMarker = "$KEYCLOAK_CLIENT_SECRET"

// defaultDexIssuerURL derives the deterministic cluster-local Dex issuer for
// a managed cluster. ArgoCD is configured against this issuer only — never
// against platform Keycloak directly (ADR-0012).
func defaultDexIssuerURL(clusterID string) string {
	return fmt.Sprintf("http://dex-%s.dex.svc.cluster.local:5556/dex", clusterID)
}

// dexIssuerURL resolves the effective Dex issuer for the CR.
func dexIssuerURL(cr *platformv1alpha1.ClusterDex) string {
	if cr.Spec.Dex != nil && cr.Spec.Dex.IssuerURL != "" {
		return cr.Spec.Dex.IssuerURL
	}
	return defaultDexIssuerURL(cr.Spec.ClusterID)
}

// secretRefNamespace defaults a SecretReference namespace to the CR
// namespace.
func secretRefNamespace(cr *platformv1alpha1.ClusterDex, ref platformv1alpha1.SecretReference) string {
	if ref.Namespace != "" {
		return ref.Namespace
	}
	return cr.Namespace
}

// renderClusterDexValues renders the Helm values consumed by the W3
// tenant-zone baseline chart for one managed cluster. Output is YAML; an
// empty string means "render nothing" (baseline disabled). Secret material
// is referenced by name/key only — never embedded.
func renderClusterDexValues(cr *platformv1alpha1.ClusterDex) (string, error) {
	if !cr.Spec.IsEnabled() {
		return "", nil
	}

	issuer := dexIssuerURL(cr)

	scopes := []string{"openid", "profile", "email", "groups", "organization"}

	dexConfig := map[string]any{
		"issuer":           issuer,
		"enablePasswordDB": true, // break-glass static login is always preserved
		"connectors": []any{
			map[string]any{
				"type": "oidc",
				"id":   "keycloak",
				"name": "Inari SSO",
				"config": map[string]any{
					"issuer":       cr.Spec.KeycloakIssuerURL,
					"clientID":     cr.Spec.DexClientID(),
					"clientSecret": keycloakClientSecretMarker,
					"redirectURI":  strings.TrimSuffix(issuer, "/") + "/callback",
					"scopes":       scopes,
					"getUserInfo":  true,
				},
			},
		},
	}

	dex := map[string]any{"config": dexConfig}
	if cr.Spec.Dex != nil && cr.Spec.Dex.Image != "" {
		dex["image"] = cr.Spec.Dex.Image
	}

	secrets := map[string]any{
		"keycloakClient": map[string]any{
			"name":            cr.Spec.ClientSecretRef.Name,
			"namespace":       secretRefNamespace(cr, cr.Spec.ClientSecretRef),
			"clientIDKey":     "client-id",
			"clientSecretKey": "client-secret",
		},
	}
	if cr.Spec.Dex != nil && cr.Spec.Dex.StaticTokensSecretRef != nil {
		ref := cr.Spec.Dex.StaticTokensSecretRef
		secrets["staticTokens"] = map[string]any{
			"name":      ref.Name,
			"namespace": secretRefNamespace(cr, *ref),
		}
	}

	argocdNS := "argocd"
	var adminGroups, viewerGroups, policyExtra []string
	if cr.Spec.ArgoCD != nil {
		if cr.Spec.ArgoCD.Namespace != "" {
			argocdNS = cr.Spec.ArgoCD.Namespace
		}
		adminGroups = cr.Spec.ArgoCD.AdminGroups
		viewerGroups = cr.Spec.ArgoCD.ViewerGroups
		policyExtra = cr.Spec.ArgoCD.RBACPolicyExtra
	}

	var csv strings.Builder
	for _, g := range adminGroups {
		fmt.Fprintf(&csv, "g, %s, role:admin\n", g)
	}
	for _, g := range viewerGroups {
		fmt.Fprintf(&csv, "g, %s, role:readonly\n", g)
	}
	for _, line := range policyExtra {
		csv.WriteString(strings.TrimRight(line, "\n") + "\n")
	}

	argocd := map[string]any{
		"namespace": argocdNS,
		// ArgoCD local admin stays enabled: it is the break-glass path while
		// per-user sessions are absent (ADR-0012).
		"localAdminEnabled": true,
		"oidc": map[string]any{
			"url":      issuer,
			"clientID": "argocd",
			"scopes":   scopes,
		},
		"rbac": map[string]any{
			// Fail closed: no implicit access beyond break-glass local admin
			// and explicitly mapped Keycloak groups.
			"policyDefault": "",
			"policyCSV":     csv.String(),
		},
	}

	values := map[string]any{
		"clusterID": cr.Spec.ClusterID,
		"dex":       dex,
		"argocd":    argocd,
		"secrets":   secrets,
	}

	out, err := yaml.Marshal(values)
	if err != nil {
		return "", fmt.Errorf("marshal dex values: %w", err)
	}
	return string(out), nil
}
