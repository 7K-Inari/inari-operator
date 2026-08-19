// inari-operator is the platform-cluster operator for the Inari IDP
// (plan §5.6): it reconciles platform-scoped Catalog Items — tenant Keycloak
// realms/clients, DNS via ExternalDNS, cert-manager ClusterIssuers, shared
// ArgoCD projects, and tenant namespaces.
package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	platformv1alpha1 "github.com/7k-inari/inari-operator/api/v1alpha1"
	"github.com/7k-inari/inari-operator/internal/controller"
	"github.com/7k-inari/inari-operator/internal/keycloak"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(platformv1alpha1.AddToScheme(scheme))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		leaderElect          bool
		keycloakURL          string
		keycloakTokenRealm   string
		keycloakClientID     string
		keycloakClientSecret string
		argocdNamespace      string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&leaderElect, "leader-elect", false, "Enable leader election for controller manager.")
	flag.StringVar(&keycloakURL, "keycloak-url", envOr("KEYCLOAK_URL", ""), "Base URL of the platform Keycloak (empty disables Keycloak reconcilers).")
	flag.StringVar(&keycloakTokenRealm, "keycloak-token-realm", envOr("KEYCLOAK_TOKEN_REALM", "master"), "Keycloak realm used to obtain admin tokens.")
	flag.StringVar(&keycloakClientID, "keycloak-client-id", envOr("KEYCLOAK_CLIENT_ID", "inari-operator"), "Keycloak admin client ID.")
	flag.StringVar(&keycloakClientSecret, "keycloak-client-secret", envOr("KEYCLOAK_CLIENT_SECRET", ""), "Keycloak admin client secret.")
	flag.StringVar(&argocdNamespace, "argocd-namespace", envOr("ARGOCD_NAMESPACE", "argocd"), "Namespace where ArgoCD AppProjects live.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElect,
		LeaderElectionID:       "inari-operator.platform.inari.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	var kcClient *keycloak.Client
	if keycloakURL != "" {
		if keycloakClientSecret == "" {
			setupLog.Error(fmt.Errorf("missing keycloak client secret"), "KEYCLOAK_CLIENT_SECRET or --keycloak-client-secret is required when --keycloak-url is set")
			os.Exit(1)
		}
		kcClient = keycloak.NewClient(keycloakURL, keycloakTokenRealm, keycloakClientID, keycloakClientSecret, nil)
	} else {
		setupLog.Info("keycloak-url not set; KeycloakRealm/KeycloakClient reconcilers disabled")
	}

	recorder := mgr.GetEventRecorderFor("inari-operator")

	if kcClient != nil {
		if err := (&controller.KeycloakRealmReconciler{
			Client:   mgr.GetClient(),
			Scheme:   mgr.GetScheme(),
			Recorder: recorder,
			Keycloak: kcClient,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "KeycloakRealm")
			os.Exit(1)
		}
		if err := (&controller.KeycloakClientReconciler{
			Client:     mgr.GetClient(),
			Scheme:     mgr.GetScheme(),
			Recorder:   recorder,
			RESTConfig: mgr.GetConfig(),
			Keycloak:   kcClient,
		}).SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create controller", "controller", "KeycloakClient")
			os.Exit(1)
		}
	}
	if err := (&controller.DNSZoneReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DNSZone")
		os.Exit(1)
	}
	if err := (&controller.DNSRecordReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DNSRecord")
		os.Exit(1)
	}
	if err := (&controller.CertIssuerReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CertIssuer")
		os.Exit(1)
	}
	if err := (&controller.ArgoProjectReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Recorder:        recorder,
		ArgoCDNamespace: argocdNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ArgoProject")
		os.Exit(1)
	}
	if err := (&controller.TenantNamespaceReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: recorder,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "TenantNamespace")
		os.Exit(1)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
