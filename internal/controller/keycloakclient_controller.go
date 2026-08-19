package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
	"github.com/7k-inari/inari-operator/internal/keycloak"
)

// KeycloakClientReconciler reconciles KeycloakClient Catalog Items (§5.4/§5.6).
type KeycloakClientReconciler struct {
	client.Client
	Scheme     *runtime.Scheme
	Recorder   record.EventRecorder
	RESTConfig *rest.Config

	Keycloak *keycloak.Client
}

// +kubebuilder:rbac:groups=platform.inari.io,resources=keycloakclients,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=platform.inari.io,resources=keycloakclients/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=platform.inari.io,resources=keycloakclients/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=impersonate

func (r *KeycloakClientReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var kc platformv1alpha1.KeycloakClient
	if err := r.Get(ctx, req.NamespacedName, &kc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	clientID := kc.Spec.ClientID
	if clientID == "" {
		clientID = kc.Name
	}

	// Resolve the target realm from the referenced KeycloakRealm's spec
	// (deterministic, independent of reconcile ordering). Never fall back to
	// the tenant ID here: provisioning into an ambient fallback realm would
	// put the client in the wrong realm and leak it on teardown.
	realmName := ""
	if kc.Status.Realm != "" {
		realmName = kc.Status.Realm
	}
	var realm platformv1alpha1.KeycloakRealm
	realmKey := types.NamespacedName{Namespace: kc.Namespace, Name: kc.Spec.RealmRef}
	realmReady := false
	if err := r.Get(ctx, realmKey, &realm); err == nil {
		if realmName == "" {
			realmName = realm.Spec.Realm
			if realmName == "" {
				realmName = realm.Spec.TenantID
			}
		}
		realmReady = meta.IsStatusConditionTrue(realm.Status.Conditions, platformv1alpha1.ConditionReady)
	} else if !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !kc.DeletionTimestamp.IsZero() {
		deleted, err := finalize(ctx, r.Client, &kc, func(ctx context.Context) error {
			if r.Keycloak != nil && realmName != "" {
				if err := r.Keycloak.DeleteClient(ctx, realmName, clientID); err != nil {
					return err
				}
			}
			secretName := kc.Spec.SecretName
			if secretName == "" {
				secretName = kc.Name + "-keycloak-client"
			}
			wc, err := impersonatingClient(r.Client, r.RESTConfig, kc.Spec.TenantReference)
			if err != nil {
				return err
			}
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: kc.Spec.Namespace}}
			return client.IgnoreNotFound(wc.Delete(ctx, secret))
		})
		if err != nil {
			return ctrl.Result{}, err
		}
		if deleted {
			event(r.Recorder, &kc, corev1.EventTypeNormal, "Deleted",
				fmt.Sprintf("Keycloak client %q deleted for tenant %q", clientID, kc.Spec.TenantID))
		}
		return ctrl.Result{}, nil
	}

	added, err := ensureFinalizer(ctx, r.Client, &kc)
	if err != nil {
		return ctrl.Result{}, err
	}
	if added {
		return ctrl.Result{Requeue: true}, nil
	}

	if r.Keycloak == nil {
		return ctrl.Result{}, fmt.Errorf("keycloak client not configured")
	}

	// Wait for the referenced realm to exist and be ready before provisioning.
	if realmName == "" {
		platformv1alpha1.SetFailed(&kc.Status.Conditions, kc.Generation,
			fmt.Sprintf("referenced KeycloakRealm %q not found", kc.Spec.RealmRef))
		_ = r.Status().Update(ctx, &kc)
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	if !realmReady && kc.Status.Realm == "" {
		platformv1alpha1.SetReconciling(&kc.Status.Conditions, kc.Generation,
			fmt.Sprintf("waiting for KeycloakRealm %q to become ready", kc.Spec.RealmRef))
		_ = r.Status().Update(ctx, &kc)
		return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
	}

	res, err := r.Keycloak.EnsureClient(ctx, realmName, keycloak.ClientRepresentation{
		ClientID:               clientID,
		PublicClient:           kc.Spec.Public,
		ServiceAccountsEnabled: kc.Spec.ServiceAccountsEnabled,
		RedirectURIs:           kc.Spec.RedirectURIs,
	})
	if err != nil {
		platformv1alpha1.SetFailed(&kc.Status.Conditions, kc.Generation, err.Error())
		_ = r.Status().Update(ctx, &kc)
		event(r.Recorder, &kc, corev1.EventTypeWarning, "ReconcileError", err.Error())
		return ctrl.Result{}, err
	}

	secretName := kc.Spec.SecretName
	if secretName == "" {
		secretName = kc.Name + "-keycloak-client"
	}

	data := map[string]string{
		"client-id": clientID,
		"issuer":    r.Keycloak.IssuerURL(realmName),
	}
	if !kc.Spec.Public {
		data["client-secret"] = res.Secret
	}

	// Tenant-visible secret: write through the tenant-scoped identity when
	// one is configured (§5.6 impersonation), else as the operator.
	wc, err := impersonatingClient(r.Client, r.RESTConfig, kc.Spec.TenantReference)
	if err != nil {
		return ctrl.Result{}, err
	}
	secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: secretName, Namespace: kc.Spec.Namespace}}
	if _, err := controllerutil.CreateOrUpdate(ctx, wc, secret, func() error {
		if secret.Labels == nil {
			secret.Labels = map[string]string{}
		}
		secret.Labels[tenantLabel] = kc.Spec.TenantID
		secret.Type = corev1.SecretTypeOpaque
		secret.StringData = data
		return nil
	}); err != nil {
		platformv1alpha1.SetFailed(&kc.Status.Conditions, kc.Generation, err.Error())
		_ = r.Status().Update(ctx, &kc)
		return ctrl.Result{}, fmt.Errorf("write client secret: %w", err)
	}

	kc.Status.ClientID = clientID
	kc.Status.SecretName = secretName
	kc.Status.Realm = realmName
	kc.Status.ObservedGeneration = kc.Generation
	platformv1alpha1.SetReady(&kc.Status.Conditions, kc.Generation,
		platformv1alpha1.ReasonReady, fmt.Sprintf("client %q provisioned in realm %q", clientID, realmName))
	if err := r.Status().Update(ctx, &kc); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	if res.Created {
		event(r.Recorder, &kc, corev1.EventTypeNormal, "Created",
			fmt.Sprintf("Keycloak client %q created in realm %q for tenant %q", clientID, realmName, kc.Spec.TenantID))
	}
	logger.Info("reconciled KeycloakClient", "tenant", kc.Spec.TenantID, "client", clientID, "realm", realmName)
	return ctrl.Result{}, nil
}

func (r *KeycloakClientReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&platformv1alpha1.KeycloakClient{}).
		Complete(r)
}
