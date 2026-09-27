package controller

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

func newClusterDexReconciler() *ClusterDexReconciler {
	return &ClusterDexReconciler{
		Client:     testClient,
		Scheme:     testScheme,
		Recorder:   newRecorder(),
		RESTConfig: testCfg,
	}
}

func createDexClientSecret(t *testing.T, name, namespace string) {
	t.Helper()
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	if err := testClient.Create(context.Background(), nsObj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"client-id":     "cluster-c1-dex",
			"client-secret": "client-secret-value",
		},
	}
	if err := testClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

func createClusterDex(t *testing.T, name string, mutate func(*platformv1alpha1.ClusterDex)) *platformv1alpha1.ClusterDex {
	t.Helper()
	cr := &platformv1alpha1.ClusterDex{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns(t)},
		Spec: platformv1alpha1.ClusterDexSpec{
			TenantReference:  platformv1alpha1.TenantReference{TenantID: "tenant-a", Namespace: "tenant-a"},
			ClusterID:        "c1",
			KeycloakIssuerURL: "https://keycloak.platform.example/realms/inari",
			ClientSecretRef:   platformv1alpha1.SecretReference{Name: name + "-kc", Namespace: "tenant-a"},
		},
	}
	if mutate != nil {
		mutate(cr)
	}
	if err := testClient.Create(context.Background(), cr); err != nil {
		t.Fatal(err)
	}
	return cr
}

func valuesConfigMapKey(cr *platformv1alpha1.ClusterDex) types.NamespacedName {
	return types.NamespacedName{Name: tenantChildName(cr.Name, "dex-values"), Namespace: cr.Spec.Namespace}
}

func getReady(t *testing.T, key types.NamespacedName) platformv1alpha1.ClusterDex {
	t.Helper()
	var got platformv1alpha1.ClusterDex
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestClusterDexHappyPath(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "happy-kc", "tenant-a")
	cr := createClusterDex(t, "happy", func(c *platformv1alpha1.ClusterDex) {
		c.Spec.ArgoCD = &platformv1alpha1.ClusterDexArgoCD{
			AdminGroups: []string{"/tenant-a/platform-team"},
		}
	})
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); err != nil {
		t.Fatalf("values ConfigMap missing: %v", err)
	}
	if cm.Labels[tenantLabel] != "tenant-a" {
		t.Fatalf("tenant label missing: %v", cm.Labels)
	}
	vals := cm.Data["values.yaml"]
	for _, want := range []string{
		"cluster-c1-dex",
		"https://keycloak.platform.example/realms/inari",
		"enablePasswordDB: true",
		"g, /tenant-a/platform-team, role:admin",
		defaultDexIssuerURL("c1"),
	} {
		if !strings.Contains(vals, want) {
			t.Fatalf("values.yaml missing %q:\n%s", want, vals)
		}
	}
	if strings.Contains(vals, "client-secret-value") {
		t.Fatal("values.yaml embeds the client secret")
	}

	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Ready not true: %+v", got.Status.Conditions)
	}
	if got.Status.ValuesConfigMap != cm.Name {
		t.Fatalf("status.valuesConfigMap = %q, want %q", got.Status.ValuesConfigMap, cm.Name)
	}
	if got.Status.DexIssuerURL != defaultDexIssuerURL("c1") {
		t.Fatalf("status.dexIssuerURL = %q", got.Status.DexIssuerURL)
	}
	if got.Status.ClientID != "cluster-c1-dex" {
		t.Fatalf("status.clientID = %q", got.Status.ClientID)
	}

	// Teardown: finalizer removes the ConfigMap.
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	var gone corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &gone); !apierrors.IsNotFound(err) {
		t.Fatalf("values ConfigMap not deleted: %v", err)
	}
}

func TestClusterDexIdempotent(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "idem-kc", "tenant-a")
	cr := createClusterDex(t, "idem", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	first := getReady(t, key)
	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); err != nil {
		t.Fatal(err)
	}
	rv := cm.ResourceVersion

	// Additional reconciles must not churn the ConfigMap or status.
	reconcileTwice(t, r, key)
	second := getReady(t, key)
	if second.Status.ObservedGeneration != first.Status.ObservedGeneration {
		t.Fatalf("observedGeneration drifted: %d -> %d", first.Status.ObservedGeneration, second.Status.ObservedGeneration)
	}
	var cm2 corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm2); err != nil {
		t.Fatal(err)
	}
	if cm2.ResourceVersion != rv {
		t.Fatalf("ConfigMap churned across reconciles: %s -> %s", rv, cm2.ResourceVersion)
	}
}

func TestClusterDexMissingSecret(t *testing.T) {
	r := newClusterDexReconciler()
	cr := createClusterDex(t, "nosecret", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	var res ctrl.Result
	for i := 0; i < 2; i++ {
		var err error
		res, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatalf("missing secret should not error (graceful rollout): %v", err)
		}
	}
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while waiting for the W2 client secret")
	}

	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionFailed)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Failed not true: %+v", got.Status.Conditions)
	}
	if !strings.Contains(cond.Message, "nosecret-kc") {
		t.Fatalf("condition should name the missing secret (not its content): %q", cond.Message)
	}

	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); !apierrors.IsNotFound(err) {
		t.Fatal("no ConfigMap must be rendered without the client secret")
	}

	// Secret appears (W2 provisioning catches up) -> becomes ready.
	createDexClientSecret(t, "nosecret-kc", "tenant-a")
	reconcileTwice(t, r, key)
	got = getReady(t, key)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, platformv1alpha1.ConditionReady) {
		t.Fatalf("not ready after secret appeared: %+v", got.Status.Conditions)
	}
}

func TestClusterDexSecretMissingKeys(t *testing.T) {
	r := newClusterDexReconciler()
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "tenant-a"}}
	if err := testClient.Create(context.Background(), nsObj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "badkeys-kc", Namespace: "tenant-a"},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"client-id": "cluster-c1-dex"}, // client-secret missing
	}
	if err := testClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	cr := createClusterDex(t, "badkeys", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	var res ctrl.Result
	for i := 0; i < 2; i++ {
		var err error
		res, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatalf("missing secret keys should not error (graceful rollout): %v", err)
		}
	}
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while secret is incomplete")
	}
	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionFailed)
	if cond == nil || cond.Status != metav1.ConditionTrue || !strings.Contains(cond.Message, "client-secret") {
		t.Fatalf("expected Failed condition naming the missing key: %+v", got.Status.Conditions)
	}
	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); !apierrors.IsNotFound(err) {
		t.Fatal("no ConfigMap must be rendered with an incomplete client secret")
	}

	// Completing the secret recovers.
	var live corev1.Secret
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "badkeys-kc", Namespace: "tenant-a"}, &live); err != nil {
		t.Fatal(err)
	}
	live.Data["client-secret"] = []byte("client-secret-value")
	if err := testClient.Update(context.Background(), &live); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, r, key)
	got = getReady(t, key)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, platformv1alpha1.ConditionReady) {
		t.Fatalf("not ready after secret completed: %+v", got.Status.Conditions)
	}
}

func TestClusterDexDisabledNoOp(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "off-kc", "tenant-a")
	cr := createClusterDex(t, "off", func(c *platformv1alpha1.ClusterDex) {
		c.Spec.Enabled = boolPtr(false)
	})
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); !apierrors.IsNotFound(err) {
		t.Fatal("disabled CR must render no ConfigMap")
	}
	got := getReady(t, key)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, platformv1alpha1.ConditionReady) {
		t.Fatalf("disabled CR should still report Ready (graceful no-op): %+v", got.Status.Conditions)
	}

	// Toggling off removes a previously rendered ConfigMap.
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	enabled := getReady(t, key)
	enabled.Spec.Enabled = nil
	if err := testClient.Update(context.Background(), &enabled); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, r, key)
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); err != nil {
		t.Fatalf("ConfigMap missing after enabling: %v", err)
	}
	disabled := getReady(t, key)
	disabled.Spec.Enabled = boolPtr(false)
	if err := testClient.Update(context.Background(), &disabled); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, r, key)
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); !apierrors.IsNotFound(err) {
		t.Fatal("disabling must remove the rendered ConfigMap")
	}
}

func TestClusterDexMigrationNoOp(t *testing.T) {
	// Clusters without a ClusterDex CR (existing installs) must be
	// completely unaffected: nothing rendered, nothing touched.
	r := newClusterDexReconciler()
	res, err := r.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{Name: "never-existed", Namespace: ns(t)},
	})
	if err != nil || res.Requeue || res.RequeueAfter != 0 {
		t.Fatalf("reconcile of unknown CR: res=%+v err=%v", res, err)
	}
	var cms corev1.ConfigMapList
	if err := testClient.List(context.Background(), &cms); err != nil {
		t.Fatal(err)
	}
	for _, cm := range cms.Items {
		if strings.Contains(cm.Name, "never-existed") {
			t.Fatalf("unexpected ConfigMap rendered: %s", cm.Name)
		}
	}
}

func TestClusterDexSecretValueNeverRead(t *testing.T) {
	// The operator must not copy secret material anywhere: not into the
	// ConfigMap, not into status, not into events.
	r := newClusterDexReconciler()
	rec := newRecorder()
	r.Recorder = rec
	createDexClientSecret(t, "leak-kc", "tenant-a")
	cr := createClusterDex(t, "leak", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), valuesConfigMapKey(cr), &cm); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cm.Data["values.yaml"], "client-secret-value") {
		t.Fatal("client secret leaked into values ConfigMap")
	}
	got := getReady(t, key)
	statusRaw, _ := json.Marshal(got.Status)
	if strings.Contains(string(statusRaw), "client-secret-value") {
		t.Fatal("client secret leaked into status")
	}
	for {
		select {
		case ev := <-rec.Events:
			if strings.Contains(ev, "client-secret-value") {
				t.Fatalf("client secret leaked into event: %s", ev)
			}
		default:
			return
		}
	}
}
