/*
Copyright 2021 Digitalis.IO.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package main

import (
	"context"
	"flag"
	"os"
	"strconv"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
	digitalisiov1beta1 "digitalis.io/vals-operator/apis/digitalis.io/v1beta1"
	"digitalis.io/vals-operator/controllers"
	dmetrics "digitalis.io/vals-operator/metrics"
	"digitalis.io/vals-operator/vault"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	//+kubebuilder:scaffold:imports
)

var (
	scheme                 = runtime.NewScheme()
	setupLog               = ctrl.Log.WithName("setup")
	developmentMode string = "false"
	gitVersion      string = "main"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(secretv1.AddToScheme(scheme))
	utilruntime.Must(digitalisiov1beta1.AddToScheme(scheme))

	metrics.Registry.MustRegister(
		dmetrics.SecretFailures,
		dmetrics.DbSecretFailures,
		dmetrics.SecretError,
		dmetrics.DbSecretError,
		dmetrics.DbSecretExpireTime,
		dmetrics.DbSecretInfo,
		dmetrics.SecretInfo,
		dmetrics.VaultError,
		dmetrics.VaultTokenError,
		dmetrics.SecretRetrieveTime,
		dmetrics.SecretCreationTime,
		dmetrics.DbSecretRevokationError,
		dmetrics.DbSecretDeletionError,
	)
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var reconcilePeriod time.Duration
	var watchNamespaces string
	var excludeNamespaces string
	var recordChanges bool
	var defaultTTL time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.DurationVar(&reconcilePeriod, "reconcile-period", 5*time.Second, "How often the controller will re-queue vals-operator events.")
	flag.DurationVar(&defaultTTL, "ttl", 300*time.Second, "How often to check backend for updates.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "", "Comma separated list of namespaces that vals-operator will watch.")
	flag.StringVar(&excludeNamespaces, "exclude-namespaces", "", "Comma separated list of namespaces to ignore.")
	flag.BoolVar(&recordChanges, "record-changes", true, "Records every time a secret has been updated. You can view them with kubectl describe. "+
		"It may also be disabled globally and enabled per secret via the annotation 'vals-operator.digitalis.io/record: \"true\"'")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")

	d, err := strconv.ParseBool(developmentMode)
	if err != nil {
		d = true
	}
	opts := zap.Options{
		Development: d,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	if developmentMode == "true" {
		setupLog.Info("Starting controller manager in development mode")
	}
	if gitVersion != "" {
		setupLog.Info("Version: ", "version", gitVersion)
	}

	nsSlice := func(ns string) []string {
		trimmed := strings.Trim(strings.TrimSpace(ns), "\"")
		return strings.Split(trimmed, ",")
	}
	excludeNs := make(map[string]bool)
	if len(excludeNamespaces) > 0 {
		for _, ns := range nsSlice(excludeNamespaces) {
			excludeNs[ns] = true
		}
	}

	setupLog.Info("The backends will be checked every " + defaultTTL.String())
	var cacheOptions cache.Options
	if watchNamespaces != "" {
		setupLog.Info("watching namespaces", "namespaces", watchNamespaces)

		// Split the watchNamespaces string into a slice of namespaces
		namespaces := strings.Split(watchNamespaces, ",")

		// Create a map to hold namespace configurations
		namespaceConfigs := make(map[string]cache.Config)

		// Add each namespace to the map
		for _, ns := range namespaces {
			// Trim any whitespace from the namespace
			ns = strings.TrimSpace(ns)
			if ns != "" {
				namespaceConfigs[ns] = cache.Config{}
			}
		}

		// Set the cache options with the namespace configurations
		cacheOptions = cache.Options{
			DefaultNamespaces: namespaceConfigs,
		}

		setupLog.Info("configured cache for namespaces", "count", len(namespaceConfigs))
	}
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: false,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "6d6f94cf.digitalis.io",
		Cache:                  cacheOptions,
	})
	if err != nil {
		setupLog.Error(err, "unable to start vals-operator")
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err = (&controllers.ValsSecretReconciler{
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Ctx:                  ctx,
		ReconciliationPeriod: reconcilePeriod,
		ExcludeNamespaces:    excludeNs,
		RecordChanges:        recordChanges,
		DefaultTTL:           defaultTTL,
		Log:                  ctrl.Log.WithName("controllers").WithName("vals-operator"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ValsSecret")
		os.Exit(1)
	}
	if err = (&controllers.DbSecretReconciler{
		Scheme:               scheme,
		Client:               mgr.GetClient(),
		APIReader:            mgr.GetAPIReader(),
		Ctx:                  ctx,
		ReconciliationPeriod: reconcilePeriod,
		ExcludeNamespaces:    excludeNs,
		RecordChanges:        recordChanges,
		DefaultTTL:           defaultTTL,
		Log:                  ctrl.Log.WithName("controllers").WithName("vals-operator"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DbSecret")
		os.Exit(1)
	}
	//+kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	if os.Getenv("VAULT_AUTH_METHOD") != "" {
		panic("Please remove the VAULT_AUTH_METHOD environment variable as it conflicts with `vals` backend engine")
	}

	// Check if either Vault or OpenBao is configured
	if os.Getenv("VAULT_ADDR") != "" || os.Getenv("BAO_ADDR") != "" {
		if err := vault.Start(); err != nil {
			setupLog.Error(err, "unable to authenticate with secrets backend")
			os.Exit(1)
		}
	}

	setupLog.Info("starting vals-operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running vals-operator")
		os.Exit(1)
	}
}
