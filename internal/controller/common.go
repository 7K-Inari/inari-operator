// Package controller holds the reconcilers for the platform.inari.io
// Catalog Item CRDs (plan §5.6).
package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
)

// Finalizer is attached to every platform Catalog Item so teardown (external
// Keycloak resources, cluster-scoped children) is verified before deletion.
const Finalizer = "platform.inari.io/finalizer"

// tenantLabel marks every child resource with its owning tenant.
const tenantLabel = "platform.inari.io/tenant"

// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

// ensureFinalizer adds the finalizer if missing. Returns true when added.
func ensureFinalizer(ctx context.Context, c client.Client, obj client.Object) (bool, error) {
	if controllerutil.ContainsFinalizer(obj, Finalizer) {
		return false, nil
	}
	controllerutil.AddFinalizer(obj, Finalizer)
	if err := c.Update(ctx, obj); err != nil {
		return false, fmt.Errorf("add finalizer: %w", err)
	}
	return true, nil
}

// finalize removes the finalizer after cleanupFn succeeds. A nil cleanupFn
// means no external cleanup is required.
func finalize(ctx context.Context, c client.Client, obj client.Object, cleanupFn func(context.Context) error) (bool, error) {
	if !controllerutil.ContainsFinalizer(obj, Finalizer) {
		return false, nil
	}
	if cleanupFn != nil {
		if err := cleanupFn(ctx); err != nil {
			return true, err
		}
	}
	controllerutil.RemoveFinalizer(obj, Finalizer)
	if err := c.Update(ctx, obj); err != nil {
		return true, fmt.Errorf("remove finalizer: %w", err)
	}
	return true, nil
}

// impersonatingClient returns a client acting as the tenant-scoped
// ServiceAccount named in the Catalog Item (§5.6: tenant access is mediated
// by impersonation). When no ServiceAccount is specified, the operator's own
// client is returned.
func impersonatingClient(base client.Client, cfg *rest.Config, tenant platformv1alpha1.TenantReference) (client.Client, error) {
	if tenant.ImpersonateServiceAccount == "" {
		return base, nil
	}
	ic := rest.CopyConfig(cfg)
	ic.Impersonate = rest.ImpersonationConfig{
		UserName: fmt.Sprintf("system:serviceaccount:%s:%s", tenant.Namespace, tenant.ImpersonateServiceAccount),
	}
	c, err := client.New(ic, client.Options{Scheme: base.Scheme()})
	if err != nil {
		return nil, fmt.Errorf("build impersonating client for tenant %q: %w", tenant.TenantID, err)
	}
	return c, nil
}

// tenantChildName derives a stable, tenant-scoped name for cluster-scoped
// child resources. The result is always a valid DNS-1123 subdomain (<= 63
// chars); over-long combinations are truncated with a short content hash to
// stay unique and deterministic.
func tenantChildName(parts ...string) string {
	s := strings.Join(parts, "-")
	const maxName = 63
	if len(s) <= maxName {
		return s
	}
	sum := sha256.Sum256([]byte(s))
	suffix := hex.EncodeToString(sum[:])[:8]
	truncated := strings.TrimRight(s[:maxName-len(suffix)-1], "-.")
	return truncated + "-" + suffix
}

// event emits an audit-friendly event on the object.
func event(rec record.EventRecorder, obj client.Object, eventtype, reason, message string) {
	if rec != nil {
		rec.Eventf(obj, eventtype, reason, "%s", message)
	}
}

// setOwner sets a controller reference; only valid when owner and child share
// a namespace scope.
func setOwner(scheme *runtime.Scheme, owner, child client.Object) error {
	return controllerutil.SetControllerReference(owner, child, scheme)
}

// statusConditionsReady reports whether Ready=True is already recorded for
// the given generation (used to keep reconciles idempotent and avoid
// redundant status writes).
func statusConditionsReady(conditions []metav1.Condition, generation int64) bool {
	cond := meta.FindStatusCondition(conditions, platformv1alpha1.ConditionReady)
	return cond != nil && cond.Status == metav1.ConditionTrue && cond.ObservedGeneration == generation
}
