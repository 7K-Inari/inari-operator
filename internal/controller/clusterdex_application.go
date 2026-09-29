package controller

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/yaml"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// applicationGVK is the ArgoCD Application CRD.
var applicationGVK = schema.GroupVersionKind{
	Group:   "argoproj.io",
	Version: "v1alpha1",
	Kind:    "Application",
}

const (
	// dexChartRepoURL is the official dexidp Helm chart repository. The
	// Application is single-source: no wrapper or companion chart.
	dexChartRepoURL = "https://charts.dexidp.io"
	// dexChartName is the upstream chart name.
	dexChartName = "dex"
	// defaultDexChartVersion pins the upstream chart when the CR does not
	// override spec.dex.chartVersion. Keep in sync with the CRD default;
	// bump deliberately.
	defaultDexChartVersion = "0.25.2"

	// dexNamespace is the destination namespace for the Dex release.
	dexNamespace = "dex"
	// dexServicePort is the stable Dex API port siblings (ArgoCD,
	// extensions) rely on; also the upstream chart default.
	dexServicePort = 5556

	// argoCDConfigMapName / argoCDRBACConfigMapName are the fixed names
	// ArgoCD reads its OIDC and RBAC configuration from.
	argoCDConfigMapName     = "argocd-cm"
	argoCDRBACConfigMapName = "argocd-rbac-cm"

	// oidcScopes are requested on both the Dex upstream connector and the
	// ArgoCD OIDC client.
	oidcScopes = "openid profile email groups organization"
)

// defaultDexIssuerURL derives the deterministic cluster-local Dex issuer for
// a managed cluster. ArgoCD is configured against this issuer only — never
// against platform Keycloak directly (ADR-0012). The service name is capped
// and hashed like other tenant child names so long cluster IDs stay valid
// DNS-1123 labels.
func defaultDexIssuerURL(clusterID string) string {
	return fmt.Sprintf("http://%s.dex.svc.cluster.local:5556/dex", tenantChildName("dex", clusterID))
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

// dexApplicationName is the deterministic Application name; it doubles as the
// Helm fullnameOverride so the Service DNS name matches the default issuer.
func dexApplicationName(cr *platformv1alpha1.ClusterDex) string {
	return tenantChildName("dex", cr.Spec.ClusterID)
}

// dexChartVersion resolves the pinned upstream chart version.
func dexChartVersion(cr *platformv1alpha1.ClusterDex) string {
	if cr.Spec.Dex != nil && cr.Spec.Dex.ChartVersion != "" {
		return cr.Spec.Dex.ChartVersion
	}
	return defaultDexChartVersion
}

// argoCDNamespace resolves the ArgoCD namespace (default "argocd").
func argoCDNamespace(cr *platformv1alpha1.ClusterDex) string {
	if cr.Spec.ArgoCD != nil && cr.Spec.ArgoCD.Namespace != "" {
		return cr.Spec.ArgoCD.Namespace
	}
	return "argocd"
}

// argoCDProject resolves the AppProject the Application belongs to.
func argoCDProject(cr *platformv1alpha1.ClusterDex) string {
	if cr.Spec.ArgoCD != nil && cr.Spec.ArgoCD.Project != "" {
		return cr.Spec.ArgoCD.Project
	}
	return "default"
}

// splitImageRef splits a full image reference into repository and tag. The
// tag separator must follow the last path separator so registry ports
// (host:5000/img:tag) are handled.
func splitImageRef(image string) (repository, tag string, err error) {
	idx := strings.LastIndex(image, ":")
	if idx < 0 || idx < strings.LastIndex(image, "/") {
		return "", "", fmt.Errorf("image %q must include an explicit tag", image)
	}
	return image[:idx], image[idx+1:], nil
}

// dexHelmValues builds the Helm values for the upstream dex chart. Secret
// material is referenced by name/key only — never embedded.
func dexHelmValues(cr *platformv1alpha1.ClusterDex) (map[string]any, error) {
	if cr.Spec.Dex == nil || cr.Spec.Dex.ConfigSecretRef == nil {
		return nil, fmt.Errorf("spec.dex.configSecretRef is required")
	}

	values := map[string]any{
		// Pins the Service name so the deterministic issuer URL stays valid.
		"fullnameOverride": dexApplicationName(cr),
		"commonLabels": map[string]any{
			"app.kubernetes.io/part-of": "inari-platform",
		},
		// Full config.yaml (Keycloak OIDC connector cluster-<id>-dex with
		// scopes openid profile email groups organization + getUserInfo,
		// static client "argocd", enablePasswordDB break-glass) lives in an
		// ESO/Vault-synced Secret — unchanged production posture.
		"configSecret": map[string]any{
			"create": false,
			"name":   cr.Spec.Dex.ConfigSecretRef.Name,
		},
		"service": map[string]any{
			"ports": map[string]any{
				"http": map[string]any{"port": dexServicePort},
			},
		},
		// Default-deny egress except DNS and 443 (Keycloak issuer). The
		// upstream chart's ingress rule is port-scoped.
		"networkPolicy": map[string]any{
			"enabled": true,
			"egressRules": []any{
				map[string]any{
					"to": []any{
						map[string]any{
							"namespaceSelector": map[string]any{
								"matchLabels": map[string]any{"kubernetes.io/metadata.name": "kube-system"},
							},
						},
					},
					"ports": []any{
						map[string]any{"port": 53, "protocol": "UDP"},
						map[string]any{"port": 53, "protocol": "TCP"},
					},
				},
				map[string]any{
					"to": []any{
						map[string]any{"ipBlock": map[string]any{"cidr": "0.0.0.0/0"}},
					},
					"ports": []any{
						map[string]any{"port": 443, "protocol": "TCP"},
					},
				},
			},
		},
		"podDisruptionBudget": map[string]any{
			"enabled":      true,
			"minAvailable": 1,
		},
		"podSecurityContext": map[string]any{
			"runAsNonRoot":   true,
			"runAsUser":      1001,
			"runAsGroup":     1001,
			"fsGroup":        1001,
			"seccompProfile": map[string]any{"type": "RuntimeDefault"},
		},
		"securityContext": map[string]any{
			"allowPrivilegeEscalation": false,
			"readOnlyRootFilesystem":   true,
			"capabilities":             map[string]any{"drop": []any{"ALL"}},
		},
		"resources": map[string]any{
			"requests": map[string]any{"cpu": "50m", "memory": "64Mi"},
			"limits":   map[string]any{"memory": "128Mi"},
		},
	}

	if cr.Spec.Dex.Image != "" {
		repo, tag, err := splitImageRef(cr.Spec.Dex.Image)
		if err != nil {
			return nil, err
		}
		values["image"] = map[string]any{"repository": repo, "tag": tag}
	}

	if route := cr.Spec.Dex.Route; route != nil {
		parentRef := map[string]any{"name": route.GatewayRef.Name}
		if route.GatewayRef.Namespace != "" {
			parentRef["namespace"] = route.GatewayRef.Namespace
		}
		if route.GatewayRef.SectionName != "" {
			parentRef["sectionName"] = route.GatewayRef.SectionName
		}
		httpRoute := map[string]any{
			"enabled":    true,
			"parentRefs": []any{parentRef},
		}
		if len(route.Hostnames) > 0 {
			hostnames := make([]any, 0, len(route.Hostnames))
			for _, h := range route.Hostnames {
				hostnames = append(hostnames, h)
			}
			httpRoute["hostnames"] = hostnames
		}
		if len(route.Annotations) > 0 {
			annotations := make(map[string]any, len(route.Annotations))
			for k, v := range route.Annotations {
				annotations[k] = v
			}
			httpRoute["annotations"] = annotations
		}
		values["httpRoute"] = httpRoute
	}

	return values, nil
}

// renderDexApplication renders the single-source ArgoCD Application that
// deploys the cluster-local Dex from the official dexidp chart.
func renderDexApplication(cr *platformv1alpha1.ClusterDex) (*unstructured.Unstructured, error) {
	values, err := dexHelmValues(cr)
	if err != nil {
		return nil, err
	}
	valuesYAML, err := yaml.Marshal(values)
	if err != nil {
		return nil, fmt.Errorf("marshal dex helm values: %w", err)
	}

	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	app.SetName(dexApplicationName(cr))
	app.SetNamespace(argoCDNamespace(cr))
	app.SetLabels(map[string]string{tenantLabel: cr.Spec.TenantID})
	app.Object["spec"] = map[string]any{
		"project": argoCDProject(cr),
		"source": map[string]any{
			"repoURL":        dexChartRepoURL,
			"chart":          dexChartName,
			"targetRevision": dexChartVersion(cr),
			"helm":           map[string]any{"values": string(valuesYAML)},
		},
		"destination": map[string]any{
			"server":    "https://kubernetes.default.svc",
			"namespace": dexNamespace,
		},
		"syncPolicy": map[string]any{
			"automated":   map[string]any{"prune": true, "selfHeal": true},
			"syncOptions": []any{"CreateNamespace=true"},
		},
	}
	return app, nil
}

// renderArgoCDConfigMaps renders the argocd-cm (OIDC against the
// cluster-local Dex issuer + break-glass accounts) and argocd-rbac-cm
// (fail-closed policy.csv) baseline ConfigMaps.
func renderArgoCDConfigMaps(cr *platformv1alpha1.ClusterDex) (*corev1.ConfigMap, *corev1.ConfigMap, error) {
	if cr.Spec.ArgoCD == nil || cr.Spec.ArgoCD.OIDCClientSecretRef == nil {
		return nil, nil, fmt.Errorf("spec.argocd.oidcClientSecretRef is required")
	}
	ref := cr.Spec.ArgoCD.OIDCClientSecretRef
	key := ref.Key
	if key == "" {
		key = "client-secret"
	}

	ns := argoCDNamespace(cr)
	scopes := strings.Fields(oidcScopes)

	var adminGroups, viewerGroups, policyExtra []string
	adminGroups = cr.Spec.ArgoCD.AdminGroups
	viewerGroups = cr.Spec.ArgoCD.ViewerGroups
	policyExtra = cr.Spec.ArgoCD.RBACPolicyExtra
	for _, g := range append(append([]string{}, adminGroups...), viewerGroups...) {
		if strings.ContainsAny(g, "\r\n") {
			return nil, nil, fmt.Errorf("argocd group %q must be a single line", g)
		}
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

	oidcConfig, err := yaml.Marshal(map[string]any{
		"name":            "Inari SSO (Dex)",
		"issuer":          dexIssuerURL(cr),
		"clientID":        "argocd",
		"clientSecret":    fmt.Sprintf("$%s:%s", ref.Name, key),
		"requestedScopes": scopes,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("marshal argocd oidc config: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      argoCDConfigMapName,
			Namespace: ns,
			Labels:    map[string]string{tenantLabel: cr.Spec.TenantID},
		},
		Data: map[string]string{
			// ArgoCD local admin stays enabled: it is the break-glass path
			// while per-user sessions are absent (ADR-0012).
			"admin.enabled": "true",
			// Legacy/break-glass fallback account. Password is set
			// out-of-band via the argocd CLI — never stored here.
			"accounts.inari-breakglass": "apiKey, login",
			"oidc.config":               string(oidcConfig),
		},
	}

	rbacCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      argoCDRBACConfigMapName,
			Namespace: ns,
			Labels:    map[string]string{tenantLabel: cr.Spec.TenantID},
		},
		Data: map[string]string{
			// Fail closed: no implicit access beyond break-glass local admin
			// and explicitly mapped Keycloak groups.
			"policy.default": "",
			"policy.csv":     csv.String(),
		},
	}
	return cm, rbacCM, nil
}
