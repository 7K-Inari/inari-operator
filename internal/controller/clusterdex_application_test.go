package controller

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

func boolPtr(b bool) *bool { return &b }

func testClusterDex() *platformv1alpha1.ClusterDex {
	return &platformv1alpha1.ClusterDex{
		ObjectMeta: metav1.ObjectMeta{Name: "c1", Namespace: "tenant-acme"},
		Spec: platformv1alpha1.ClusterDexSpec{
			TenantReference: platformv1alpha1.TenantReference{
				TenantID:  "acme",
				Namespace: "tenant-acme",
			},
			ClusterID:         "c1",
			KeycloakIssuerURL: "https://keycloak.platform.example/realms/inari",
			ClientSecretRef:   platformv1alpha1.SecretReference{Name: "cluster-c1-dex"},
			Dex: &platformv1alpha1.ClusterDexDex{
				ConfigSecretRef: &platformv1alpha1.SecretReference{Name: "dex-config"},
			},
			ArgoCD: &platformv1alpha1.ClusterDexArgoCD{
				OIDCClientSecretRef: &platformv1alpha1.SecretKeyReference{Name: "argocd-dex-client"},
			},
		},
	}
}

func renderApp(t *testing.T, cr *platformv1alpha1.ClusterDex) *unstructured.Unstructured {
	t.Helper()
	app, err := renderDexApplication(cr)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	return app
}

func dig(t *testing.T, m map[string]any, path ...string) any {
	t.Helper()
	var cur any = m
	for _, p := range path {
		mm, ok := cur.(map[string]any)
		if !ok {
			t.Fatalf("path %v: %v is not a map", path, cur)
		}
		cur, ok = mm[p]
		if !ok {
			t.Fatalf("path %v: key %q missing", path, p)
		}
	}
	return cur
}

func appValues(t *testing.T, app *unstructured.Unstructured) map[string]any {
	t.Helper()
	raw, ok := dig(t, app.Object, "spec", "source", "helm", "values").(string)
	if !ok {
		t.Fatal("spec.source.helm.values is not a string")
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatalf("helm values are not valid YAML: %v\n%s", err, raw)
	}
	return values
}

func TestApplicationSourcePinsUpstreamChart(t *testing.T) {
	app := renderApp(t, testClusterDex())

	if app.GetKind() != "Application" || app.GetAPIVersion() != "argoproj.io/v1alpha1" {
		t.Fatalf("gvk = %s %s", app.GetAPIVersion(), app.GetKind())
	}
	if got := dig(t, app.Object, "spec", "source", "repoURL"); got != "https://charts.dexidp.io" {
		t.Fatalf("repoURL = %v", got)
	}
	if got := dig(t, app.Object, "spec", "source", "chart"); got != "dex" {
		t.Fatalf("chart = %v", got)
	}
	if got := dig(t, app.Object, "spec", "source", "targetRevision"); got != defaultDexChartVersion {
		t.Fatalf("targetRevision = %v, want pinned default %v", got, defaultDexChartVersion)
	}
	if app.GetLabels()[tenantLabel] != "acme" {
		t.Fatalf("tenant label missing: %v", app.GetLabels())
	}
}

func TestApplicationChartVersionOverride(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex.ChartVersion = "0.24.0"
	app := renderApp(t, cr)
	if got := dig(t, app.Object, "spec", "source", "targetRevision"); got != "0.24.0" {
		t.Fatalf("targetRevision = %v", got)
	}
}

func TestApplicationNameNamespaceDestination(t *testing.T) {
	app := renderApp(t, testClusterDex())

	want := tenantChildName("dex", "c1")
	if app.GetName() != want {
		t.Fatalf("name = %q, want %q", app.GetName(), want)
	}
	if app.GetNamespace() != "argocd" {
		t.Fatalf("namespace = %q", app.GetNamespace())
	}
	if got := dig(t, app.Object, "spec", "destination", "namespace"); got != "dex" {
		t.Fatalf("destination namespace = %v", got)
	}
	if got := dig(t, app.Object, "spec", "project"); got != "default" {
		t.Fatalf("project = %v", got)
	}
	automated, ok := dig(t, app.Object, "spec", "syncPolicy", "automated").(map[string]any)
	if !ok || automated["prune"] != true || automated["selfHeal"] != true {
		t.Fatalf("syncPolicy.automated = %v", automated)
	}
}

func TestApplicationProjectOverride(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD.Project = "tenant-acme"
	app := renderApp(t, cr)
	if got := dig(t, app.Object, "spec", "project"); got != "tenant-acme" {
		t.Fatalf("project = %v", got)
	}
}

func TestApplicationArgoCDNamespaceOverride(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD.Namespace = "argo"
	app := renderApp(t, cr)
	if app.GetNamespace() != "argo" {
		t.Fatalf("namespace = %q", app.GetNamespace())
	}
}

func TestValuesConfigSecretExisting(t *testing.T) {
	v := appValues(t, renderApp(t, testClusterDex()))
	if got := dig(t, v, "configSecret", "create"); got != false {
		t.Fatalf("configSecret.create = %v (must be false: ESO/Vault-synced Secret)", got)
	}
	if got := dig(t, v, "configSecret", "name"); got != "dex-config" {
		t.Fatalf("configSecret.name = %v", got)
	}
}

func TestValuesIssuerStability(t *testing.T) {
	v := appValues(t, renderApp(t, testClusterDex()))
	// fullnameOverride pins the Service name so the deterministic issuer URL
	// (http://<name>.dex.svc.cluster.local:5556/dex) stays valid.
	if got := dig(t, v, "fullnameOverride"); got != tenantChildName("dex", "c1") {
		t.Fatalf("fullnameOverride = %v", got)
	}
	if got := dig(t, v, "service", "ports", "http", "port"); fmt.Sprint(got) != "5556" {
		t.Fatalf("service http port = %v (issuer assumes 5556)", got)
	}
}

func TestValuesHardeningBaseline(t *testing.T) {
	v := appValues(t, renderApp(t, testClusterDex()))

	if got := dig(t, v, "commonLabels", "app.kubernetes.io/part-of"); got != "inari-platform" {
		t.Fatalf("part-of label = %v", got)
	}
	if got := dig(t, v, "podDisruptionBudget", "enabled"); got != true {
		t.Fatalf("pdb.enabled = %v", got)
	}
	if got := dig(t, v, "podSecurityContext", "runAsNonRoot"); got != true {
		t.Fatalf("runAsNonRoot = %v", got)
	}
	sc := dig(t, v, "securityContext").(map[string]any)
	if sc["allowPrivilegeEscalation"] != false || sc["readOnlyRootFilesystem"] != true {
		t.Fatalf("securityContext = %+v", sc)
	}
	if got := dig(t, v, "networkPolicy", "enabled"); got != true {
		t.Fatalf("networkPolicy.enabled = %v", got)
	}
	rules, ok := dig(t, v, "networkPolicy", "egressRules").([]any)
	if !ok || len(rules) == 0 {
		t.Fatalf("networkPolicy.egressRules = %v", rules)
	}
	raw, _ := yaml.Marshal(rules)
	for _, want := range []string{"port: 53", "port: 443"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("egressRules missing %s:\n%s", want, raw)
		}
	}
	if got := dig(t, v, "resources", "requests", "cpu"); got != "50m" {
		t.Fatalf("resources.requests.cpu = %v", got)
	}
}

func TestValuesImageOverride(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex.Image = "ghcr.io/dexidp/dex:v2.42.0"
	v := appValues(t, renderApp(t, cr))
	if got := dig(t, v, "image", "repository"); got != "ghcr.io/dexidp/dex" {
		t.Fatalf("image.repository = %v", got)
	}
	if got := dig(t, v, "image", "tag"); got != "v2.42.0" {
		t.Fatalf("image.tag = %v", got)
	}
}

func TestValuesImageOverrideEdgeCases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		image   string
		wantErr bool
	}{
		{"registry port", "host:5000/dexidp/dex:v2.42.0", false},
		{"missing tag", "ghcr.io/dexidp/dex", true},
		{"digest ref", "ghcr.io/dexidp/dex@sha256:1a2b3c", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cr := testClusterDex()
			cr.Spec.Dex.Image = tc.image
			_, err := dexHelmValues(cr)
			if tc.wantErr && err == nil {
				t.Fatalf("image %q: expected error, got none", tc.image)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("image %q: unexpected error %v", tc.image, err)
			}
		})
	}
	if repo, tag, err := splitImageRef("host:5000/dexidp/dex:v2.42.0"); err != nil || repo != "host:5000/dexidp/dex" || tag != "v2.42.0" {
		t.Fatalf("registry port split = %q %q %v", repo, tag, err)
	}
}

func TestValuesHTTPRoute(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex.Route = &platformv1alpha1.ClusterDexRoute{
		GatewayRef: platformv1alpha1.ClusterDexGatewayRef{Name: "gw", Namespace: "gateway-system", SectionName: "https"},
		Hostnames:  []string{"dex.c1.example.com"},
	}
	v := appValues(t, renderApp(t, cr))
	if got := dig(t, v, "httpRoute", "enabled"); got != true {
		t.Fatalf("httpRoute.enabled = %v", got)
	}
	refs, ok := dig(t, v, "httpRoute", "parentRefs").([]any)
	if !ok || len(refs) != 1 {
		t.Fatalf("parentRefs = %v", refs)
	}
	ref := refs[0].(map[string]any)
	if ref["name"] != "gw" || ref["namespace"] != "gateway-system" || ref["sectionName"] != "https" {
		t.Fatalf("parentRef = %+v", ref)
	}
	hosts, ok := dig(t, v, "httpRoute", "hostnames").([]any)
	if !ok || len(hosts) != 1 || hosts[0] != "dex.c1.example.com" {
		t.Fatalf("hostnames = %v", hosts)
	}
}

func TestValuesHTTPRouteAbsentByDefault(t *testing.T) {
	v := appValues(t, renderApp(t, testClusterDex()))
	if _, found := v["httpRoute"]; found {
		t.Fatalf("httpRoute must be absent without spec.dex.route: %v", v["httpRoute"])
	}
}

func TestRenderRequiresConfigSecretRef(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex.ConfigSecretRef = nil
	if _, err := renderDexApplication(cr); err == nil {
		t.Fatal("expected error: configSecretRef is required")
	}
}

func TestRenderNeverEmbedsSecretMaterial(t *testing.T) {
	cr := testClusterDex()
	app, err := renderDexApplication(cr)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := yaml.Marshal(app.Object)
	for _, banned := range []string{"client-secret-value", "clientSecretValue", "$KEYCLOAK_CLIENT_SECRET", "$ARGOCD_CLIENT_SECRET"} {
		if strings.Contains(string(raw), banned) {
			t.Fatalf("rendered Application contains %q", banned)
		}
	}
}

func TestArgoCDConfigMaps(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD.AdminGroups = []string{"/tenant-acme/platform-team"}
	cr.Spec.ArgoCD.ViewerGroups = []string{"/tenant-acme/viewers"}
	cm, rbacCM, err := renderArgoCDConfigMaps(cr)
	if err != nil {
		t.Fatal(err)
	}

	if cm.GetName() != "argocd-cm" || cm.GetNamespace() != "argocd" {
		t.Fatalf("argocd-cm = %s/%s", cm.GetNamespace(), cm.GetName())
	}
	if cm.Data["admin.enabled"] != "true" {
		t.Fatalf("admin.enabled = %q (break-glass local admin must stay)", cm.Data["admin.enabled"])
	}
	if cm.Data["accounts.inari-breakglass"] == "" {
		t.Fatal("inari-breakglass account missing")
	}
	oidc := cm.Data["oidc.config"]
	for _, want := range []string{
		"issuer: " + defaultDexIssuerURL("c1"),
		"clientID: argocd",
		"$argocd-dex-client:client-secret",
		"openid", "groups", "organization",
	} {
		if !strings.Contains(oidc, want) {
			t.Fatalf("oidc.config missing %q:\n%s", want, oidc)
		}
	}
	if strings.Contains(oidc, "keycloak.platform.example") {
		t.Fatalf("oidc.config references Keycloak directly:\n%s", oidc)
	}

	if rbacCM.GetName() != "argocd-rbac-cm" || rbacCM.GetNamespace() != "argocd" {
		t.Fatalf("argocd-rbac-cm = %s/%s", rbacCM.GetNamespace(), rbacCM.GetName())
	}
	if def, ok := rbacCM.Data["policy.default"]; !ok || def != "" {
		t.Fatalf("policy.default = %q (must be empty/fail-closed)", rbacCM.Data["policy.default"])
	}
	csv := rbacCM.Data["policy.csv"]
	for _, want := range []string{
		"g, /tenant-acme/platform-team, role:admin",
		"g, /tenant-acme/viewers, role:readonly",
	} {
		if !strings.Contains(csv, want) {
			t.Fatalf("policy.csv missing %q:\n%s", want, csv)
		}
	}
	if cm.Labels[tenantLabel] != "acme" || rbacCM.Labels[tenantLabel] != "acme" {
		t.Fatalf("tenant labels missing: %v / %v", cm.Labels, rbacCM.Labels)
	}
}

func TestArgoCDConfigMapsIssuerOverride(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex.IssuerURL = "https://dex.c1.example.com/dex"
	cm, _, err := renderArgoCDConfigMaps(cr)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cm.Data["oidc.config"], "issuer: https://dex.c1.example.com/dex") {
		t.Fatalf("oidc.config issuer not overridden:\n%s", cm.Data["oidc.config"])
	}
}

func TestArgoCDConfigMapsRequiresOIDCClientSecretRef(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD.OIDCClientSecretRef = nil
	if _, _, err := renderArgoCDConfigMaps(cr); err == nil {
		t.Fatal("expected error: oidcClientSecretRef is required")
	}
}

func TestArgoCDConfigMapsRejectsNewlineInGroups(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD.AdminGroups = []string{"/tenant-acme/team\np, *, *, *, *, allow"}
	if _, _, err := renderArgoCDConfigMaps(cr); err == nil {
		t.Fatal("expected error: newline in group would inject RBAC policy lines")
	}
}

func TestDefaultDexIssuerURLValidForLongClusterIDs(t *testing.T) {
	long := strings.Repeat("a", 63)
	issuer := defaultDexIssuerURL(long)
	host := strings.TrimPrefix(issuer, "http://")
	label := strings.SplitN(host, ".", 2)[0]
	if len(label) > 63 {
		t.Fatalf("issuer host label %q exceeds 63 chars (invalid DNS)", label)
	}
}

func TestDefaultDexIssuerURLDeterministic(t *testing.T) {
	first, second := defaultDexIssuerURL("c1"), defaultDexIssuerURL("c1")
	if first != second {
		t.Fatal("issuer must be deterministic")
	}
	if defaultDexIssuerURL("c1") == defaultDexIssuerURL("c2") {
		t.Fatal("issuer must differ per cluster")
	}
	if !strings.Contains(defaultDexIssuerURL("c1"), "c1") {
		t.Fatalf("issuer should identify the cluster: %s", defaultDexIssuerURL("c1"))
	}
}
