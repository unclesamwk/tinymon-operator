package main

import (
	"context"
	"flag"
	"os"
	"time"

	"github.com/unclesamwk/tinymon-operator/internal/controller"
	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	// metricsv1beta1 removed from scheme — metrics are fetched via REST client in node controller
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(k8upv1.AddToScheme(scheme))
	// metricsv1beta1 intentionally not added to scheme — causes watch errors on clusters
	// where metrics-server doesn't support watch. Metrics are fetched via direct REST calls.
}

func main() {
	var metricsAddr string
	var probeAddr string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	log := ctrl.Log.WithName("setup")

	tinymonURL := os.Getenv("TINYMON_URL")
	if tinymonURL == "" {
		log.Error(nil, "TINYMON_URL environment variable is required")
		os.Exit(1)
	}

	apiKey := os.Getenv("TINYMON_API_KEY")
	if apiKey == "" {
		log.Error(nil, "TINYMON_API_KEY environment variable is required")
		os.Exit(1)
	}

	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		log.Error(nil, "CLUSTER_NAME environment variable is required")
		os.Exit(1)
	}

	client := tinymon.NewClient(tinymonURL, apiKey)

	restConfig := ctrl.GetConfigOrDie()
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
	})
	if err != nil {
		log.Error(err, "unable to start manager")
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Error(err, "unable to create kubernetes clientset")
		os.Exit(1)
	}

	// Core controllers — always available
	if err := controller.SetupNodeReconciler(mgr, client, clusterName, clientset); err != nil {
		log.Error(err, "unable to setup node controller")
		os.Exit(1)
	}
	if err := controller.SetupDeploymentReconciler(mgr, client, clusterName); err != nil {
		log.Error(err, "unable to setup deployment controller")
		os.Exit(1)
	}
	if err := controller.SetupIngressReconciler(mgr, client, clusterName); err != nil {
		log.Error(err, "unable to setup ingress controller")
		os.Exit(1)
	}
	if err := controller.SetupPVCReconciler(mgr, client, clusterName); err != nil {
		log.Error(err, "unable to setup pvc controller")
		os.Exit(1)
	}

	// Optional controllers — registered if CRDs are available, or watched for in the background
	k8upGV := schema.GroupVersion{Group: "k8up.io", Version: "v1"}
	if apiAvailable(restConfig, k8upGV) {
		if err := controller.SetupBackupReconciler(mgr, client, clusterName); err != nil {
			log.Error(err, "unable to setup backup controller")
			os.Exit(1)
		}
		log.Info("backup controller enabled (k8up.io/v1 available)")
	} else {
		log.Info("backup controller skipped (k8up.io/v1 CRDs not installed), watching for availability...")
		go watchForAPI(mgr, restConfig, k8upGV, func() error {
			return controller.SetupBackupReconciler(mgr, client, clusterName)
		})
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	log.Info("starting manager", "tinymonURL", tinymonURL, "cluster", clusterName)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// apiAvailable checks if a GroupVersion is registered in the cluster's API server.
func apiAvailable(cfg *rest.Config, gv schema.GroupVersion) bool {
	disc, err := discovery.NewDiscoveryClientForConfig(cfg)
	if err != nil {
		return false
	}
	resources, err := disc.ServerResourcesForGroupVersion(gv.String())
	return err == nil && resources != nil
}

// watchForAPI polls the API server until the given GroupVersion becomes available,
// then registers the controller via setupFn. This allows optional controllers
// (e.g. K8up backup) to start dynamically when their CRDs are installed later.
func watchForAPI(mgr ctrl.Manager, cfg *rest.Config, gv schema.GroupVersion, setupFn func() error) {
	log := ctrl.Log.WithName("api-watch").WithValues("groupVersion", gv.String())
	ctx := context.Background()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !apiAvailable(cfg, gv) {
				continue
			}
			log.Info("API became available, registering controller")
			if err := setupFn(); err != nil {
				log.Error(err, "failed to setup controller after API became available")
				return
			}
			log.Info("controller registered successfully")
			return
		}
	}
}
