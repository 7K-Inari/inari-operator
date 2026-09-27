package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
		},
	}
}

func render(t *testing.T, cr *platformv1alpha1.ClusterDex) map[string]any {
	t.Helper()
	out, err := renderClusterDexValues(cr)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	var values map[string]any
	if err := yaml.Unmarshal([]byte(out), &values); err != nil {
		t.Fatalf("rendered values are not valid YAML: %v\n%s", err, out)
	}
	return values
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

func TestValuesDexConnector(t *testing.T) {
	v := render(t, testClusterDex())

	if got := dig(t, v, "dex", "config", "issuer"); got != defaultDexIssuerURL("c1") {
		t.Fatalf("dex issuer = %v", got)
	}
	if got := dig(t, v, "dex", "config", "enablePasswordDB"); got != true {
		t.Fatalf("enablePasswordDB = %v (break-glass must be preserved)", got)
	}
	connectors, ok := dig(t, v, "dex", "config", "connectors").([]any)
	if !ok || len(connectors) != 1 {
		t.Fatalf("connectors = %v", connectors)
	}
	conn := connectors[0].(map[string]any)
	if conn["type"] != "oidc" || conn["id"] != "keycloak" {
		t.Fatalf("connector = %+v", conn)
	}
	cfg := conn["config"].(map[string]any)
	if cfg["issuer"] != "https://keycloak.platform.example/realms/inari" {
		t.Fatalf("connector issuer = %v", cfg["issuer"])
	}
	if cfg["clientID"] != "cluster-c1-dex" {
		t.Fatalf("connector clientID = %v", cfg["clientID"])
	}
	secret, _ := cfg["clientSecret"].(string)
	if !strings.HasPrefix(secret, "$") {
		t.Fatalf("clientSecret must be a reference marker, got %q", secret)
	}
	if got := dig(t, v, "secrets", "keycloakClient", "name"); got != "cluster-c1-dex" {
		t.Fatalf("secrets.keycloakClient.name = %v", got)
	}
	if got := dig(t, v, "secrets", "keycloakClient", "namespace"); got != "tenant-acme" {
		t.Fatalf("secrets.keycloakClient.namespace = %v", got)
	}
}

func TestValuesArgoCDTargetsDexOnly(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD = &platformv1alpha1.ClusterDexArgoCD{
		AdminGroups:  []string{"/tenant-acme/platform-team"},
		ViewerGroups: []string{"/tenant-acme/viewers"},
	}
	v := render(t, cr)

	issuer := defaultDexIssuerURL("c1")
	if got := dig(t, v, "argocd", "oidc", "url"); got != issuer {
		t.Fatalf("argocd oidc url = %v, want %v", got, issuer)
	}
	if got := dig(t, v, "argocd", "oidc", "clientID"); got != "argocd" {
		t.Fatalf("argocd oidc clientID = %v", got)
	}

	rbac, _ := dig(t, v, "argocd", "rbac").(map[string]any)
	if def, ok := rbac["policyDefault"].(string); !ok || def != "" {
		t.Fatalf("policyDefault = %v (must be empty/fail-closed)", rbac["policyDefault"])
	}
	csv, _ := rbac["policyCSV"].(string)
	for _, want := range []string{
		"g, /tenant-acme/platform-team, role:admin",
		"g, /tenant-acme/viewers, role:readonly",
	} {
		if !strings.Contains(csv, want) {
			t.Fatalf("policyCSV missing %q:\n%s", want, csv)
		}
	}

	// Keycloak must never be referenced from the ArgoCD section.
	raw, _ := yaml.Marshal(v["argocd"])
	if strings.Contains(string(raw), "keycloak.platform.example") {
		t.Fatalf("argocd section references Keycloak directly:\n%s", raw)
	}
}

func TestValuesIssuerOverrideAndImage(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex = &platformv1alpha1.ClusterDexDex{
		IssuerURL: "https://dex.c1.example.com/dex",
		Image:     "ghcr.io/dexidp/dex:v2.42.0",
	}
	v := render(t, cr)
	if got := dig(t, v, "dex", "config", "issuer"); got != "https://dex.c1.example.com/dex" {
		t.Fatalf("dex issuer = %v", got)
	}
	if got := dig(t, v, "dex", "image"); got != "ghcr.io/dexidp/dex:v2.42.0" {
		t.Fatalf("dex image = %v", got)
	}
	if got := dig(t, v, "argocd", "oidc", "url"); got != "https://dex.c1.example.com/dex" {
		t.Fatalf("argocd oidc url = %v", got)
	}
}

func TestValuesStaticTokensRefPassThrough(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Dex = &platformv1alpha1.ClusterDexDex{
		StaticTokensSecretRef: &platformv1alpha1.SecretReference{Name: "dex-break-glass"},
	}
	v := render(t, cr)
	if got := dig(t, v, "secrets", "staticTokens", "name"); got != "dex-break-glass" {
		t.Fatalf("secrets.staticTokens.name = %v", got)
	}
	if got := dig(t, v, "secrets", "staticTokens", "namespace"); got != "tenant-acme" {
		t.Fatalf("secrets.staticTokens.namespace = %v", got)
	}
	if got := dig(t, v, "dex", "config", "enablePasswordDB"); got != true {
		t.Fatalf("enablePasswordDB = %v", got)
	}
}

func TestValuesNeverEmbedSecretMaterial(t *testing.T) {
	cr := testClusterDex()
	out, err := renderClusterDexValues(cr)
	if err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"client-secret-value", "clientSecretValue"} {
		if strings.Contains(out, banned) {
			t.Fatalf("rendered values contain secret material marker %q", banned)
		}
	}
}

func TestValuesRejectsNewlineInGroups(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.ArgoCD = &platformv1alpha1.ClusterDexArgoCD{
		AdminGroups: []string{"/tenant-acme/team\np, *, *, *, *, allow"},
	}
	if _, err := renderClusterDexValues(cr); err == nil {
		t.Fatal("expected error: newline in group would inject RBAC policy lines")
	}
}

func TestValuesDisabledRendersNothing(t *testing.T) {
	cr := testClusterDex()
	cr.Spec.Enabled = boolPtr(false)
	out, err := renderClusterDexValues(cr)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "" {
		t.Fatalf("disabled CR must render nothing, got:\n%s", out)
	}
}

func TestValuesArgoCDStaticClient(t *testing.T) {
	v := render(t, testClusterDex())

	clients, ok := dig(t, v, "dex", "config", "staticClients").([]any)
	if !ok || len(clients) != 1 {
		t.Fatalf("staticClients = %v (ArgoCD needs a registered Dex client)", clients)
	}
	c := clients[0].(map[string]any)
	if c["id"] != "argocd" {
		t.Fatalf("static client id = %v", c["id"])
	}
	secret, _ := c["secret"].(string)
	if !strings.HasPrefix(secret, "$") {
		t.Fatalf("static client secret must be a reference marker, got %q", secret)
	}
	// ArgoCD's OIDC config must reference the same marker so the chart wires
	// one shared generated secret on both sides.
	if got := dig(t, v, "argocd", "oidc", "clientSecret"); got != secret {
		t.Fatalf("argocd oidc clientSecret = %v, want shared marker %q", got, secret)
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
