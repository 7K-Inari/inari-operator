package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
	"github.com/7k-inari/inari-operator/internal/keycloak"
)

// KeycloakRealmReconciler reconciles KeycloakRealm Catalog Items (§5.4/§5.6).
type KeycloakRealmReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder

	// Keycloak is the Admin REST client. Nil disables provisioning (used when
	// the operator runs without Keycloak configured).
	Keycloak *keycloak.Client
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=keycloakrealms,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=keycloakrealms/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=keycloakrealms/finalizers,verbs=update

func (r *KeycloakRealmReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var realm platformv1alpha1.KeycloakRealm
	if err := r.Get(ctx, req.NamespacedName, &realm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	realmName := realm.Spec.Realm
	if realmName == "" {
		realmName = realm.Spec.TenantID
	}

	if !realm.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &realm, func(ctx context.Context) error {
			if r.Keycloak == nil {
				return nil
			}
			return r.Keycloak.DeleteRealm(ctx, realmName)
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &realm, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("Keycloak realm %q deleted for tenant %q", realmName, realm.Spec.TenantID))
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &realm)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	if r.Keycloak == nil {
		return ctrl.Result{}, fmt.Errorf("keycloak client not configured")
	}

	created, err := r.Keycloak.EnsureRealm(ctx, keycloak.RealmRepresentation{
		Realm:       realmName,
		DisplayName: realm.Spec.DisplayName,
		Enabled:     realm.Spec.Enabled,
	})
	if err != nil {
		platformv1alpha1.SetFailed(&realm.Status.Conditions, realm.Generation, err.Error())
		_ = r.Status().Update(ctx, &realm)
		event(r.Recorder, &realm, corev1.EventTypeWarning, "ReconcileError", err.Error())
		return ctrl.Result{}, err
	}

	realm.Status.Realm = realmName
	realm.Status.IssuerURL = r.Keycloak.IssuerURL(realmName)
	realm.Status.ObservedGeneration = realm.Generation
	platformv1alpha1.SetReady(&realm.Status.Conditions, realm.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("realm %q provisioned", realmName))
	if err := r.Status().Update(ctx, &realm); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if created {
		event(r.Recorder, &realm, corev1.EventTypeNormal, "Created",
			fmt.Sprintf("Keycloak realm %q created for tenant %q", realmName, realm.Spec.TenantID))
	}
	logger.Info("reconciled KeycloakRealm", "tenant", realm.Spec.TenantID, "realm", realmName)
	return ctrl.Result{}, nil
}

func (r *KeycloakRealmReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.KeycloakRealm{}).
		Complete(r)
}
