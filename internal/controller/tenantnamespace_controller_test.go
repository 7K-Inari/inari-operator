package controller

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

func TestTenantNamespaceMaterializes(t *testing.T) {
	r := &TenantNamespaceReconciler{Client: testClient, Scheme: testScheme, Recorder: newRecorder()}

	tn := &platformv1alpha1.TenantNamespace{
		ObjectMeta: metav1.ObjectMeta{Name: "team-g", Namespace: ns(t)},
		Spec: platformv1alpha1.TenantNamespaceSpec{
			TenantReference: platformv1alpha1.TenantReference{
				TenantID:                  "team-g",
				Namespace:                 "team-g",
				ImpersonateServiceAccount: "tenant-admin",
			},
			AdminGroup: "idp:team-g-admins",
			ResourceQuota: &platformv1alpha1.TenantResourceQuota{
				CPU: "4", Memory: "8Gi", Pods: 20,
			},
		},
	}
	if err := testClient.Create(context.Background(), tn); err != nil {
		t.Fatal(err)
	}
	key := types.NamespacedName{Name: tn.Name, Namespace: tn.Namespace}
	reconcileTwice(t, r, key)

	var namespace corev1.Namespace
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "team-g"}, &namespace); err != nil {
		t.Fatalf("namespace not materialized: %v", err)
	}
	if namespace.Labels[tenantLabel] != "team-g" {
		t.Fatalf("tenant label missing: %v", namespace.Labels)
	}

	var rb rbacv1.RoleBinding
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "tenant-admin", Namespace: "team-g"}, &rb); err != nil {
		t.Fatalf("tenant-admin RoleBinding missing: %v", err)
	}
	if rb.RoleRef.Name != "admin" {
		t.Fatalf("roleRef = %+v", rb.RoleRef)
	}
	var hasGroup, hasSA bool
	for _, s := range rb.Subjects {
		if s.Kind == rbacv1.GroupKind && s.Name == "idp:team-g-admins" {
			hasGroup = true
		}
		if s.Kind == rbacv1.ServiceAccountKind && s.Name == "tenant-admin" {
			hasSA = true
		}
	}
	if !hasGroup || !hasSA {
		t.Fatalf("expected group + SA subjects, got %+v", rb.Subjects)
	}

	var np networkingv1.NetworkPolicy
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "tenant-default-deny", Namespace: "team-g"}, &np); err != nil {
		t.Fatalf("default-deny NetworkPolicy missing: %v", err)
	}
	if len(np.Spec.PolicyTypes) != 2 {
		t.Fatalf("expected ingress+egress policy types, got %v", np.Spec.PolicyTypes)
	}

	var rq corev1.ResourceQuota
	if err := testClient.Get(context.Background(), types.NamespacedName{Name: "tenant-quota", Namespace: "team-g"}, &rq); err != nil {
		t.Fatalf("ResourceQuota missing: %v", err)
	}
	if q, ok := rq.Spec.Hard[corev1.ResourcePods]; !ok || q.Value() != 20 {
		t.Fatalf("pods quota = %v", rq.Spec.Hard)
	}

	var got platformv1alpha1.TenantNamespace
	if err := testClient.Get(context.Background(), key, &got); err != nil {
		t.Fatal(err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, platformv1alpha1.ConditionReady)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("Ready not true: %+v", got.Status.Conditions)
	}

	// Teardown: finalizer deletes the namespace and everything in it.
	if err := testClient.Delete(context.Background(), &got); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("finalizer reconcile: %v", err)
	}
	var gone corev1.Namespace
	err := testClient.Get(context.Background(), types.NamespacedName{Name: "team-g"}, &gone)
	if err == nil && gone.DeletionTimestamp.IsZero() {
		t.Fatal("namespace not deleted")
	}
}
