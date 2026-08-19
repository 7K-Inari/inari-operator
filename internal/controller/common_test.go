package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

func TestTenantChildName(t *testing.T) {
	long := strings.Repeat("a", 63)
	tests := []struct {
		name  string
		parts []string
	}{
		{"short", []string{"tenant", "acme", "iss"}},
		{"max tenant id", []string{"tenant", long, "iss"}},
		{"max tenant id + long cr name", []string{"tenant", long, strings.Repeat("b", 63)}},
		{"trailing dash after truncation", []string{"tenant", strings.Repeat("c", 50) + "-", strings.Repeat("d", 40)}},
	}
	seen := map[string]int{}
	for _, tt := range tests {
		got := tenantChildName(tt.parts...)
		if len(got) > 63 {
			t.Errorf("%s: name %q is %d chars (> 63)", tt.name, got, len(got))
		}
		if got != strings.ToLower(got) || strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") || strings.HasSuffix(got, ".") {
			t.Errorf("%s: name %q is not DNS-1123 safe", tt.name, got)
		}
		seen[got]++
	}
	if seen[tenantChildName("tenant", long, "iss")] != 1 {
		t.Fatal("duplicate short name?")
	}
	// Deterministic
	if tenantChildName("tenant", long, "x") != tenantChildName("tenant", long, "x") {
		t.Fatal("not deterministic")
	}
	// Distinct inputs must not collide after truncation
	if tenantChildName("tenant", long, "a") == tenantChildName("tenant", long, "b") {
		t.Fatal("hash collision: distinct inputs produced the same name")
	}
}

func TestClusterIssuerNameLongTenant(t *testing.T) {
	cr := &platformv1alpha1.CertIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("i", 60)},
		Spec: platformv1alpha1.CertIssuerSpec{
			TenantReference: platformv1alpha1.TenantReference{
				TenantID:  strings.Repeat("t", 63),
				Namespace: "ns",
			},
		},
	}
	if n := clusterIssuerName(cr); len(n) > 63 {
		t.Fatalf("clusterIssuerName = %q (%d chars)", n, len(n))
	}
	ap := &platformv1alpha1.ArgoProject{
		ObjectMeta: metav1.ObjectMeta{Name: strings.Repeat("p", 60)},
		Spec: platformv1alpha1.ArgoProjectSpec{
			TenantReference: platformv1alpha1.TenantReference{
				TenantID:  strings.Repeat("t", 63),
				Namespace: "ns",
			},
		},
	}
	if n := argoProjectName(ap); len(n) > 63 {
		t.Fatalf("argoProjectName = %q (%d chars)", n, len(n))
	}
}
