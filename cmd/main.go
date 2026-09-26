package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"

	batchv1 "k8s.io/api/batch/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	automationv1alpha1 "github.com/lannonbr/node-schedule/api/v1alpha1"
	"github.com/lannonbr/node-schedule/internal/controller"
)

const defaultNodeImage = "node:24-alpine@sha256:50c8e8ca1d27439048670df5883f32d57cf81cff6233222c893fd0d9884cbd81"

var (
	scheme      = runtime.NewScheme()
	setupLog    = ctrl.Log.WithName("setup")
	digestImage = regexp.MustCompile(`@sha256:[a-f0-9]{64}$`)
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(batchv1.AddToScheme(scheme))
	utilruntime.Must(automationv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr, probeAddr, watchNamespace, nodeImage string
	var leaderElection bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "address for the metrics endpoint")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "address for health probes")
	flag.StringVar(&watchNamespace, "watch-namespace", envOr("WATCH_NAMESPACE", "automation"), "the single namespace to watch")
	flag.StringVar(&nodeImage, "node-image", envOr("NODE_IMAGE", defaultNodeImage), "digest-pinned public Node runtime image")
	flag.BoolVar(&leaderElection, "leader-elect", false, "enable leader election")
	zapOptions := zap.Options{Development: true}
	zapOptions.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zapOptions)))

	if watchNamespace == "" {
		setupLog.Error(fmt.Errorf("watch namespace is empty"), "configuration error")
		os.Exit(1)
	}
	if !digestImage.MatchString(nodeImage) {
		setupLog.Error(fmt.Errorf("image must end in @sha256:<64 lowercase hex characters>"), "node image is not digest-pinned", "image", nodeImage)
		os.Exit(1)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{watchNamespace: {}}},
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         leaderElection,
		LeaderElectionID:       "nodeschedule.automation.lannonbr.com",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	reconciler := &controller.NodeScheduleReconciler{
		Client: mgr.GetClient(), Scheme: mgr.GetScheme(),
		Recorder: mgr.GetEventRecorderFor("nodeschedule-controller"), NodeImage: nodeImage,
	}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller")
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

	setupLog.Info("starting manager", "namespace", watchNamespace, "nodeImage", nodeImage)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "manager stopped with an error")
		os.Exit(1)
	}
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
