package main

import (
	"flag"
	"os"

	"github.com/unclesamwk/tinymon-operator/internal/controller"
	"github.com/unclesamwk/tinymon-operator/internal/tinymon"

	k8upv1 "github.com/k8up-io/k8up/v2/api/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(k8upv1.AddToScheme(scheme))
	utilruntime.Must(metricsv1beta1.AddToScheme(scheme))
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
	if err := controller.SetupBackupReconciler(mgr, client, clusterName); err != nil {
		log.Error(err, "unable to setup backup controller")
		os.Exit(1)
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
