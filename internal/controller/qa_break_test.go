package controller

import (
	"context"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
	"github.com/7k-inari/inari-operator/internal/keycloak"
)

// QA: a KeycloakClient whose RealmRef points at a realm CR that has not
// reconciled yet (or is missing) must NOT be provisioned into the TenantID
// fallback realm — that would create the client in the wrong realm and leak
// it (the finalizer resolves the realm the same wrong way).
func TestKeycloakClientWaitsForRealmReadiness(t *testing.T) {
	fk, kc := newFakeKC(t)
	r := &KeycloakClientReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder(), RESTConfig: testCfg, Keycloak: kc}

	// Pre-provision the TenantID fallback realm so a wrong-realm create would
	// succeed (in real Keycloak it would too if the realm exists).
	if _, err := kc.EnsureRealm(context.Background(), keycloak.RealmRepresentation{Realm: "tenant-qa", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := testClient.Create(context.Background(), &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-qa"}}); err != nil {
		t.Fatal(err)
	}

	// Referenced realm CR exists with a custom realm name but is NOT
	// reconciled (Status.Realm empty).
	realm := &platformv1alpha1.KeycloakRealm{
		ObjectMeta: metav1.ObjectMeta{Name: "qa-realm", Namespace: ns(t)},
		Spec: platformv1alpha1.KeycloakRealmSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "tenant-qa", Namespace: "tenant-qa"},
			Realm:           "qa-custom-realm",
			Enabled:         true,
		},
	}
	if err := testClient.Create(context.Background(), realm); err != nil {
		t.Fatal(err)
	}

	kcr := &platformv1alpha1.KeycloakClient{
		ObjectMeta: metav1.ObjectMeta{Name: "qa-app", Namespace: ns(t)},
		Spec: platformv1alpha1.KeycloakClientSpec{
			TenantReference: platformv1alpha1.TenantReference{TenantID: "tenant-qa", Namespace: "tenant-qa"},
			RealmRef:        "qa-realm",
		},
	}
	if err := testClient.Create(context.Background(), kcr); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: kcr.Name, Namespace: kcr.Namespace}
	// First reconcile adds the finalizer; subsequent ones must not provision.
	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatalf("reconcile %d: %v", i, err)
		}
	}

	if fk.hasClient("tenant-qa", "qa-app") {
		t.Fatal("BUG: client provisioned into TenantID fallback realm while RealmRef realm is unready")
	}
	var got platformv1alpha1.KeycloakClient
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionReady); cond != nil && cond.Status == metav1.ConditionTrue {
		t.Fatal("BUG: client reported Ready before its realm exists")
	}

	// Now reconcile the realm; the client must land in the custom realm.
	rr := &KeycloakRealmReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder(), Keycloak: kc}
	reconcileTwice(t, rr, types.NamespacedName{Name: realm.Name, Namespace: realm.Namespace})
	reconcileTwice(t, r, key)
	if !fk.hasClient("qa-custom-realm", "qa-app") {
		t.Fatal("client not provisioned into the referenced custom realm")
	}
	if fk.hasClient("tenant-qa", "qa-app") {
		t.Fatal("client leaked in TenantID fallback realm")
	}

	// Teardown must target the realm the client actually lives in, even
	// after the realm CR is gone.
	var realmGot platformv1alpha1.KeycloakRealm
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: realm.Name, Namespace: realm.Namespace}, &realmGot); err != nil {
		t.Fatal(err)
	}
	if err := testClient.Delete(context.Background(), &realmGot); err != nil {
		t.Fatal(err)
	}
	// Force-remove the realm finalizer so the CR disappears WITHOUT the
	// keycloak realm being deleted (simulates operator downtime or manual
	// finalizer removal): the client teardown must still find the realm.
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: realm.Name, Namespace: realm.Namespace}, &realmGot); err != nil {
		t.Fatal(err)
	}
	realmGot.Finalizers = nil
	if err := testClient.Update(context.Background(), &realmGot); err != nil {
		t.Fatal(err)
	}
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("client finalizer reconcile keeps failing: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if fk.hasClient("qa-custom-realm", "qa-app") {
		t.Fatal("BUG: client leaked in custom realm after realm CR disappeared")
	}
}
