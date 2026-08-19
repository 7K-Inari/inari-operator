package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

func reconcileTwice(t *testing.T, rec interface {
	Reconcile(context.Context, ctrl.Request) (ctrl.Result, error)
}, key types.NamespacedName) {
	t.Helper()
	for i := 0; i < 2; i++ {
		if _, err := rec.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}
}

func TestKeycloakRealmLifecycle(t *testing.T) {
	fk, kc := newFakeKC(t)
	r := &KeycloakRealmReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder(), Keycloak: kc}

	realm := &platformv1alpha1.KeycloakRealm{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-a-realm", Namespace: ns(t)},
		Spec: platformv1alpha1.KeycloakRealmSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "tenant-a", Namespace: "tenant-a"},
			Enabled:         true,
		},
	}
	if err := testClient.Create(context.Background(), realm); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: realm.Name, Namespace: realm.Namespace}
	reconcileTwice(t, r, key)

	if !fk.hasRealm("tenant-a") {
		t.Fatal("expected realm tenant-a in keycloak")
	}

	var got platformv1alpha1.KeycloakRealm
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if got.Status.Realm != "tenant-a" {
		t.Fatalf("status.realm = %q", got.Status.Realm)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Ready condition not true: %+v", got.Status.Conditions)
	}
	if got.Status.ObservedGeneration != got.Generation {
		t.Fatalf("observedGeneration %d != generation %d", got.Status.ObservedGeneration, got.Generation)
	}

	// Teardown: finalizer must delete the keycloak realm before the CR goes away.
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	if fk.hasRealm("tenant-a") {
		t.Fatal("realm not deleted from keycloak")
	}
	err := testClient.Get(context.Background(), key, &got)
	if err == nil {
		t.Fatal("CR still present after finalizer")
	}
}

func TestKeycloakClientLifecycleAndSecret(t *testing.T) {
	fk, kc := newFakeKC(t)
	r := &KeycloakClientReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder(), RESTConfig: testCfg, Keycloak: kc}

	// tenant namespace must exist for the secret write
	if err := testClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-b"}}); err != nil {
		t.Fatal(err)
	}

	// The referenced realm CR must exist and be Ready before the client is
	// provisioned.
	realm := &platformv1alpha1.KeycloakRealm{
		ObjectMeta: metav1.ObjectMeta{Name: "tenant-b-realm", Namespace: ns(t)},
		Spec: platformv1alpha1.KeycloakRealmSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "tenant-b", Namespace: "tenant-b"},
			Enabled:         true,
		},
	}
	if err := testClient.Create(context.Background(), realm); err != nil {
		t.Fatal(err)
	}
	rr := &KeycloakRealmReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder(), Keycloak: kc}
	reconcileTwice(t, rr, types.NamespacedName{Name: realm.Name, Namespace: realm.Namespace})

	kcr := &platformv1alpha1.KeycloakClient{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: ns(t)},
		Spec: platformv1alpha1.KeycloakClientSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "tenant-b", Namespace: "tenant-b"},
			RealmRef:        "tenant-b-realm",
			RedirectURIs:    []string{"https://app.example.com/cb"},
		},
	}
	if err := testClient.Create(context.Background(), kcr); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: kcr.Name, Namespace: kcr.Namespace}
	reconcileTwice(t, r, key)

	if !fk.hasClient("tenant-b", "app") {
		t.Fatal("expected client app in realm tenant-b")
	}

	var secret corev1.Secret
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "app-keycloak-client", Namespace: "tenant-b"}, &secret); err != nil {
		t.Fatalf("client secret not written to tenant namespace: %v", err)
	}
	if string(secret.Data["client-id"]) != "app" {
		t.Fatalf("client-id = %q", secret.Data["client-id"])
	}
	if string(secret.Data["client-secret"]) != "secret-id-app" {
		t.Fatalf("client-secret = %q", secret.Data["client-secret"])
	}

	var got platformv1alpha1.KeycloakClient
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	if fk.hasClient("tenant-b", "app") {
		t.Fatal("client not deleted from keycloak")
	}
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "app-keycloak-client", Namespace: "tenant-b"}, &secret); err == nil {
		t.Fatal("client secret not cleaned up")
	}
}
