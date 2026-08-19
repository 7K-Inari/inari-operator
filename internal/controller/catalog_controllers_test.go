package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

func TestDNSRecordRendersEndpoint(t *testing.T) {
	zoneRec := &DNSZoneReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder()}
	recRec := &DNSRecordReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder()}

	zone := &platformv1alpha1.DNSZone{
		ObjectMeta: metav1.ObjectMeta{Name: "team-c-zone", Namespace: ns(t)},
		Spec: platformv1alpha1.DNSZoneSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "team-c", Namespace: "team-c"},
			ZoneName:        "team-c.apps.example.com",
		},
	}
	if err := testClient.Create(context.Background(), zone); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, zoneRec, types.NamespacedName{Name: zone.Name, Namespace: zone.Namespace})

	rec := &platformv1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "app-record", Namespace: ns(t)},
		Spec: platformv1alpha1.DNSRecordSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "team-c", Namespace: "team-c"},
			ZoneRef:         zone.Name,
			Endpoints: []platformv1alpha1.DNSRecordEndpoint{{
				DNSName:    "app.team-c.apps.example.com",
				RecordType: "A",
				Targets:    []string{"10.0.0.1"},
				TTL:        300,
			}},
		},
	}
	if err := testClient.Create(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: rec.Name, Namespace: rec.Namespace}
	reconcileTwice(t, recRec, key)

	var ep unstructured.Unstructured
	ep.SetGroupVersionKind(dnsEndpointGVK)
	if err := testClient.Get(context.Background(), key, &ep); err != nil {
		t.Fatalf("DNSEndpoint not rendered: %v", err)
	}
	if ep.GetLabels()[tenantLabel] != "team-c" {
		t.Fatalf("tenant label missing: %v", ep.GetLabels())
	}
	owners := ep.GetOwnerReferences()
	if len(owners) != 1 || owners[0].Name != rec.Name {
		t.Fatalf("expected ownerRef to DNSRecord, got %+v", owners)
	}
	eps, found, _ := unstructured.NestedSlice(ep.Object, "spec", "endpoints")
	if !found || len(eps) != 1 {
		t.Fatalf("spec.endpoints missing: %v", ep.Object)
	}

	var got platformv1alpha1.DNSRecord
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Ready not true: %+v", got.Status.Conditions)
	}
}

func TestDNSRecordRejectsOutOfZoneName(t *testing.T) {
	zoneRec := &DNSZoneReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder()}
	recRec := &DNSRecordReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder()}

	zone := &platformv1alpha1.DNSZone{
		ObjectMeta: metav1.ObjectMeta{Name: "team-d-zone", Namespace: ns(t)},
		Spec: platformv1alpha1.DNSZoneSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "team-d", Namespace: "team-d"},
			ZoneName:        "team-d.apps.example.com",
		},
	}
	if err := testClient.Create(context.Background(), zone); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, zoneRec, types.NamespacedName{Name: zone.Name, Namespace: zone.Namespace})

	rec := &platformv1alpha1.DNSRecord{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-record", Namespace: ns(t)},
		Spec: platformv1alpha1.DNSRecordSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "team-d", Namespace: "team-d"},
			ZoneRef:         zone.Name,
			Endpoints: []platformv1alpha1.DNSRecordEndpoint{{
				DNSName:    "evil.other-tenant.example.com",
				RecordType: "A",
				Targets:    []string{"10.0.0.2"},
				TTL:        300,
			}},
		},
	}
	if err := testClient.Create(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: rec.Name, Namespace: rec.Namespace}
	reconcileTwice(t, recRec, key)

	var got platformv1alpha1.DNSRecord
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionFailed)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected Failed condition: %+v", got.Status.Conditions)
	}
	var ep unstructured.Unstructured
	ep.SetGroupVersionKind(dnsEndpointGVK)
	if err := testClient.Get(context.Background(), key, &ep); err == nil {
		t.Fatal("DNSEndpoint must not be rendered for out-of-zone record")
	}
}

func TestCertIssuerRendersClusterIssuer(t *testing.T) {
	r := &CertIssuerReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder()}

	ci := &platformv1alpha1.CertIssuer{
		ObjectMeta: metav1.ObjectMeta{Name: "le", Namespace: ns(t)},
		Spec: platformv1alpha1.CertIssuerSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "team-e", Namespace: "team-e"},
			ACME: &platformv1alpha1.ACMEIssuerSpec{
				Server:       "https://acme.example.com/directory",
				Email:        "ops@example.com",
				IngressClass: "nginx",
			},
		},
	}
	if err := testClient.Create(context.Background(), ci); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: ci.Name, Namespace: ci.Namespace}
	reconcileTwice(t, r, key)

	var child unstructured.Unstructured
	child.SetGroupVersionKind(clusterIssuerGVK)
	childName := clusterIssuerName(ci)
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: childName}, &child); err != nil {
		t.Fatalf("ClusterIssuer not rendered: %v", err)
	}
	server, _, _ := unstructured.NestedString(child.Object, "spec", "acme", "server")
	if server != "https://acme.example.com/directory" {
		t.Fatalf("acme server = %q", server)
	}

	var got platformv1alpha1.CertIssuer
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.ClusterIssuerName != childName {
		t.Fatalf("status.clusterIssuerName = %q", got.Status.ClusterIssuerName)
	}

	// Teardown: finalizer must delete the cluster-scoped child.
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: childName}, &child); err == nil {
		t.Fatal("ClusterIssuer not cleaned up")
	}
}

func TestArgoProjectRendersAppProject(t *testing.T) {
	r := &ArgoProjectReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder(), ArgoCDNamespace: "argocd"}

	if err := testClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "argocd"}}); err != nil {
		t.Fatal(err)
	}

	ap := &platformv1alpha1.ArgoProject{
		ObjectMeta: metav1.ObjectMeta{Name: "team-f", Namespace: ns(t)},
		Spec: platformv1alpha1.ArgoProjectSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "team-f", Namespace: "team-f"},
			SourceRepos:     []string{"https://github.com/example/team-f-*"},
		},
	}
	if err := testClient.Create(context.Background(), ap); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: ap.Name, Namespace: ap.Namespace}
	reconcileTwice(t, r, key)

	name := argoProjectName(ap)
	var child unstructured.Unstructured
	child.SetGroupVersionKind(appProjectGVK)
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "argocd"}, &child); err != nil {
		t.Fatalf("AppProject not rendered: %v", err)
	}
	dests, _, _ := unstructured.NestedSlice(child.Object, "spec", "destinations")
	if len(dests) != 1 {
		t.Fatalf("expected 1 destination defaulting to tenant namespace, got %v", dests)
	}
	d0 := dests[0].(map[string]any)
	if d0["namespace"] != "team-f" {
		t.Fatalf("destination namespace = %v", d0["namespace"])
	}

	var got platformv1alpha1.ArgoProject
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: name, Namespace: "argocd"}, &child); err == nil {
		t.Fatal("AppProject not cleaned up")
	}
}
