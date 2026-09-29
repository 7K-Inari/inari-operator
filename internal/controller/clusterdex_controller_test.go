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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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

func ensureNamespace(t *testing.T, name string) {
	t.Helper()
	nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}
	if err := testClient.Create(context.Background(), nsObj); err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
}

func createDexClientSecret(t *testing.T, name, namespace string) {
	t.Helper()
	ensureNamespace(t, namespace)
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

func createDexConfigSecret(t *testing.T, name, namespace string) {
	t.Helper()
	ensureNamespace(t, namespace)
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"config.yaml": "issuer: http://dex-c1.dex.svc.cluster.local:5556/dex\n",
		},
	}
	if err := testClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
}

// createClusterDex creates an enabled CR with all required references set
// (client secret + config secret + argocd oidc client ref). The referenced
// Secrets must be created separately.
func createClusterDex(t *testing.T, name string, mutate func(*platformv1alpha1.ClusterDex)) *platformv1alpha1.ClusterDex {
	t.Helper()
	ensureNamespace(t, "argocd")
	cr := &platformv1alpha1.ClusterDex{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns(t)},
		Spec: platformv1alpha1.ClusterDexSpec{
			TenantReference:   platformv1alpha1.TenantReference{TenantID: "tenant-a", Namespace: "tenant-a"},
			ClusterID:         name,
			KeycloakIssuerURL: "https://keycloak.platform.example/realms/inari",
			ClientSecretRef:   platformv1alpha1.SecretReference{Name: name + "-kc", Namespace: "tenant-a"},
			Dex: &platformv1alpha1.ClusterDexDex{
				ConfigSecretRef: &platformv1alpha1.SecretReference{Name: name + "-config", Namespace: "tenant-a"},
			},
			ArgoCD: &platformv1alpha1.ClusterDexArgoCD{
				OIDCClientSecretRef: &platformv1alpha1.SecretKeyReference{Name: name + "-argocd-oidc"},
			},
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

func dexApplicationKey(cr *platformv1alpha1.ClusterDex) types.NamespacedName {
	return types.NamespacedName{Name: tenantChildName("dex", cr.Spec.ClusterID), Namespace: "argocd"}
}

func argoCDCMKey() types.NamespacedName {
	return types.NamespacedName{Name: "argocd-cm", Namespace: "argocd"}
}

func argoCDRBACCMKey() types.NamespacedName {
	return types.NamespacedName{Name: "argocd-rbac-cm", Namespace: "argocd"}
}

func getDexApplication(t *testing.T, key types.NamespacedName) unstructured.Unstructured {
	t.Helper()
	app := &unstructured.Unstructured{}
	app.SetGroupVersionKind(applicationGVK)
	if err := testClient.Get(context.Background(), key, app); err != nil {
		t.Fatalf("Dex Application missing: %v", err)
	}
	return *app
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
	createDexConfigSecret(t, "happy-config", "tenant-a")
	cr := createClusterDex(t, "happy", func(c *platformv1alpha1.ClusterDex) {
		c.Spec.ArgoCD.AdminGroups = []string{"/tenant-a/platform-team"}
	})
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	app := getDexApplication(t, dexApplicationKey(cr))
	if app.GetLabels()[tenantLabel] != "tenant-a" {
		t.Fatalf("tenant label missing: %v", app.GetLabels())
	}
	spec, _ := app.Object["spec"].(map[string]any)
	src, _ := spec["source"].(map[string]any)
	if src["repoURL"] != "https://charts.dexidp.io" || src["chart"] != "dex" {
		t.Fatalf("source = %+v", src)
	}
	if src["targetRevision"] != defaultDexChartVersion {
		t.Fatalf("targetRevision = %v, want pinned %v", src["targetRevision"], defaultDexChartVersion)
	}
	helmValues, _ := src["helm"].(map[string]any)["values"].(string)
	for _, want := range []string{"create: false", "name: happy-config", "inari-platform"} {
		if !strings.Contains(helmValues, want) {
			t.Fatalf("helm values missing %q:\n%s", want, helmValues)
		}
	}
	if strings.Contains(helmValues, "client-secret-value") {
		t.Fatal("helm values embed the client secret")
	}

	var cm, rbacCM corev1.ConfigMap
	if err := testClient.Get(context.Background(), argoCDCMKey(), &cm); err != nil {
		t.Fatalf("argocd-cm missing: %v", err)
	}
	if !strings.Contains(cm.Data["oidc.config"], "issuer: "+defaultDexIssuerURL(cr.Spec.ClusterID)) {
		t.Fatalf("oidc.config issuer wrong:\n%s", cm.Data["oidc.config"])
	}
	if !strings.Contains(cm.Data["oidc.config"], "$happy-argocd-oidc:client-secret") {
		t.Fatalf("oidc.config clientSecret reference wrong:\n%s", cm.Data["oidc.config"])
	}
	if cm.Data["accounts.inari-breakglass"] == "" {
		t.Fatal("inari-breakglass account missing")
	}
	if err := testClient.Get(context.Background(), argoCDRBACCMKey(), &rbacCM); err != nil {
		t.Fatalf("argocd-rbac-cm missing: %v", err)
	}
	if !strings.Contains(rbacCM.Data["policy.csv"], "g, /tenant-a/platform-team, role:admin") {
		t.Fatalf("policy.csv wrong:\n%s", rbacCM.Data["policy.csv"])
	}

	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Ready not true: %+v", got.Status.Conditions)
	}
	if got.Status.DexApplication != app.GetName() {
		t.Fatalf("status.dexApplication = %q, want %q", got.Status.DexApplication, app.GetName())
	}
	if got.Status.DexIssuerURL != defaultDexIssuerURL(cr.Spec.ClusterID) {
		t.Fatalf("status.dexIssuerURL = %q", got.Status.DexIssuerURL)
	}
	if got.Status.ClientID != cr.Spec.DexClientID() {
		t.Fatalf("status.clientID = %q", got.Status.ClientID)
	}

	// Teardown: finalizer removes all children.
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	gone := &unstructured.Unstructured{}
	gone.SetGroupVersionKind(applicationGVK)
	if err := testClient.Get(context.Background(), dexApplicationKey(cr), gone); !apierrors.IsNotFound(err) {
		t.Fatalf("Application not deleted: %v", err)
	}
	for _, k := range []types.NamespacedName{argoCDCMKey(), argoCDRBACCMKey()} {
		var cm corev1.ConfigMap
		if err := testClient.Get(context.Background(), k, &cm); !apierrors.IsNotFound(err) {
			t.Fatalf("%s not deleted: %v", k.Name, err)
		}
	}
}

func TestClusterDexIdempotent(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "idem-kc", "tenant-a")
	createDexConfigSecret(t, "idem-config", "tenant-a")
	cr := createClusterDex(t, "idem", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	first := getReady(t, key)
	app := getDexApplication(t, dexApplicationKey(cr))
	rv := app.GetResourceVersion()

	// Additional reconciles must not churn the Application or status.
	reconcileTwice(t, r, key)
	second := getReady(t, key)
	if second.Status.ObservedGeneration != first.Status.ObservedGeneration {
		t.Fatalf("observedGeneration drifted: %d -> %d", first.Status.ObservedGeneration, second.Status.ObservedGeneration)
	}
	app2 := getDexApplication(t, dexApplicationKey(cr))
	if app2.GetResourceVersion() != rv {
		t.Fatalf("Application churned across reconciles: %s -> %s", rv, app2.GetResourceVersion())
	}
}

func TestClusterDexMissingClientSecret(t *testing.T) {
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

	gone := &unstructured.Unstructured{}
	gone.SetGroupVersionKind(applicationGVK)
	if err := testClient.Get(context.Background(), dexApplicationKey(cr), gone); !apierrors.IsNotFound(err) {
		t.Fatal("no Application must be rendered without the client secret")
	}

	// Secrets appear (W2 provisioning + ESO sync catch up) -> becomes ready.
	createDexClientSecret(t, "nosecret-kc", "tenant-a")
	createDexConfigSecret(t, "nosecret-config", "tenant-a")
	reconcileTwice(t, r, key)
	got = getReady(t, key)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, platformv1alpha1.ConditionReady) {
		t.Fatalf("not ready after secrets appeared: %+v", got.Status.Conditions)
	}
}

func TestClusterDexMissingConfigSecret(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "noconfig-kc", "tenant-a")
	cr := createClusterDex(t, "noconfig", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	var res ctrl.Result
	for i := 0; i < 2; i++ {
		var err error
		res, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatalf("missing config secret should not error (graceful rollout): %v", err)
		}
	}
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while waiting for the ESO/Vault config secret")
	}
	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionFailed)
	if cond == nil || cond.Status != metav1.ConditionTrue || !strings.Contains(cond.Message, "noconfig-config") {
		t.Fatalf("expected Failed condition naming the config secret: %+v", got.Status.Conditions)
	}
	gone := &unstructured.Unstructured{}
	gone.SetGroupVersionKind(applicationGVK)
	if err := testClient.Get(context.Background(), dexApplicationKey(cr), gone); !apierrors.IsNotFound(err) {
		t.Fatal("no Application must be rendered without the config secret")
	}
}

func TestClusterDexConfigSecretMissingKey(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "badconfig-kc", "tenant-a")
	ensureNamespace(t, "tenant-a")
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "badconfig-config", Namespace: "tenant-a"},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"wrong-key": "x"},
	}
	if err := testClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	cr := createClusterDex(t, "badconfig", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	var res ctrl.Result
	for i := 0; i < 2; i++ {
		var err error
		res, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatalf("incomplete config secret should not error: %v", err)
		}
	}
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while config secret is incomplete")
	}
	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionFailed)
	if cond == nil || cond.Status != metav1.ConditionTrue || !strings.Contains(cond.Message, "config.yaml") {
		t.Fatalf("expected Failed condition naming the config.yaml key: %+v", got.Status.Conditions)
	}
}

func TestClusterDexMissingOIDCClientSecretRef(t *testing.T) {
	r := newClusterDexReconciler()
	createDexClientSecret(t, "nooidc-kc", "tenant-a")
	createDexConfigSecret(t, "nooidc-config", "tenant-a")
	cr := createClusterDex(t, "nooidc", func(c *platformv1alpha1.ClusterDex) {
		c.Spec.ArgoCD.OIDCClientSecretRef = nil
	})
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	var res ctrl.Result
	for i := 0; i < 2; i++ {
		var err error
		res, err = r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
		if err != nil {
			t.Fatalf("missing oidcClientSecretRef should not error: %v", err)
		}
	}
	if res.RequeueAfter == 0 {
		t.Fatal("expected requeue while oidcClientSecretRef is absent")
	}
	got := getReady(t, key)
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionFailed)
	if cond == nil || cond.Status != metav1.ConditionTrue || !strings.Contains(cond.Message, "oidcClientSecretRef") {
		t.Fatalf("expected Failed condition naming oidcClientSecretRef: %+v", got.Status.Conditions)
	}
	gone := &unstructured.Unstructured{}
	gone.SetGroupVersionKind(applicationGVK)
	if err := testClient.Get(context.Background(), dexApplicationKey(cr), gone); !apierrors.IsNotFound(err) {
		t.Fatal("no Application must be rendered without oidcClientSecretRef")
	}
}

func TestClusterDexSecretMissingKeys(t *testing.T) {
	r := newClusterDexReconciler()
	ensureNamespace(t, "tenant-a")
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "badkeys-kc", Namespace: "tenant-a"},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"client-id": "cluster-c1-dex"}, // client-secret missing
	}
	if err := testClient.Create(context.Background(), s); err != nil {
		t.Fatal(err)
	}
	createDexConfigSecret(t, "badkeys-config", "tenant-a")
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
	gone := &unstructured.Unstructured{}
	gone.SetGroupVersionKind(applicationGVK)
	if err := testClient.Get(context.Background(), dexApplicationKey(cr), gone); !apierrors.IsNotFound(err) {
		t.Fatal("no Application must be rendered with an incomplete client secret")
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
	createDexConfigSecret(t, "off-config", "tenant-a")
	cr := createClusterDex(t, "off", func(c *platformv1alpha1.ClusterDex) {
		c.Spec.Enabled = boolPtr(false)
	})
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	assertNoChildren := func() {
		t.Helper()
		app := &unstructured.Unstructured{}
		app.SetGroupVersionKind(applicationGVK)
		if err := testClient.Get(context.Background(), dexApplicationKey(cr), app); !apierrors.IsNotFound(err) {
			t.Fatal("disabled CR must render no Application")
		}
	}
	assertNoChildren()
	got := getReady(t, key)
	if !meta.IsStatusConditionTrue(got.Status.Conditions, platformv1alpha1.ConditionReady) {
		t.Fatalf("disabled CR should still report Ready (graceful no-op): %+v", got.Status.Conditions)
	}

	// Toggling on renders the baseline; toggling off removes it.
	enabled := getReady(t, key)
	enabled.Spec.Enabled = nil
	if err := testClient.Update(context.Background(), &enabled); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, r, key)
	getDexApplication(t, dexApplicationKey(cr))

	disabled := getReady(t, key)
	disabled.Spec.Enabled = boolPtr(false)
	if err := testClient.Update(context.Background(), &disabled); err != nil {
		t.Fatal(err)
	}
	reconcileTwice(t, r, key)
	assertNoChildren()
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
	// rendered children, not into status, not into events.
	r := newClusterDexReconciler()
	rec := newRecorder()
	r.Recorder = rec
	createDexClientSecret(t, "leak-kc", "tenant-a")
	createDexConfigSecret(t, "leak-config", "tenant-a")
	cr := createClusterDex(t, "leak", nil)
	key := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}
	reconcileTwice(t, r, key)

	app := getDexApplication(t, dexApplicationKey(cr))
	raw, _ := json.Marshal(app.Object)
	if strings.Contains(string(raw), "client-secret-value") {
		t.Fatal("client secret leaked into the Application")
	}
	var cm corev1.ConfigMap
	if err := testClient.Get(context.Background(), argoCDCMKey(), &cm); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(cm.Data["oidc.config"], "client-secret-value") {
		t.Fatal("client secret leaked into argocd-cm")
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
