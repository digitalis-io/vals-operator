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
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	secretv1 "digitalis.io/vals-operator/api/v1"
	"digitalis.io/vals-operator/controllers"
	"digitalis.io/vals-operator/vault"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(secretv1.AddToScheme(scheme))
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
	var secretTTL time.Duration

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.DurationVar(&reconcilePeriod, "reconcile-period", 5*time.Second, "How often the controller will re-queue vals-operator events.")
	flag.DurationVar(&secretTTL, "ttl", 300*time.Second, "How often to check backend for updates.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "", "Comma separated list of namespaces that vals-operator will watch.")
	flag.StringVar(&excludeNamespaces, "exclude-namespaces", "", "Comma separated list of namespaces to ignore.")
	flag.BoolVar(&recordChanges, "record-changes", true, "Records every time a secret has been updated. You can view them with kubectl describe. "+
		"It may also be disabled globally and enabled per secret via the annotation 'vals-operator.digitalis.io/record: \"true\"'")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

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

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "6d6f94cf.digitalis.io",
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
		SecretTTL:            secretTTL,
		Log:                  ctrl.Log.WithName("controllers").WithName("vals-operator"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ValsSecret")
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

	if os.Getenv("VAULT_TOKEN") != "" || os.Getenv("VAULT_AUTH_METHOD") != "" {
		if err := vault.Start(); err != nil {
			setupLog.Error(err, "unable authenticate with Vault")
			os.Exit(1)
		}
	}

	setupLog.Info("starting vals-operator")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running vals-operator")
		os.Exit(1)
	}
}
