/*
Copyright 2024 KubeBao Authors.

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
	"flag"
	"os"

	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"

	kubebaoiov1alpha1 "github.com/kubebao/kubebao/api/v1alpha1"
	"github.com/kubebao/kubebao/internal/controller"
	"github.com/kubebao/kubebao/internal/openbao"

	"github.com/hashicorp/go-hclog"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")

	// Version is set at build time
	Version = "dev"
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(kubebaoiov1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		enableLeaderElection bool
		probeAddr            string
		logLevel             string
		configFile           string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	flag.StringVar(&configFile, "config", "", "Path to OpenBao configuration file")
	flag.Parse()

	// Setup logger
	zapConfig := zap.NewProductionConfig()
	switch logLevel {
	case "debug":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.DebugLevel)
	case "info":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.InfoLevel)
	case "warn":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.WarnLevel)
	case "error":
		zapConfig.Level = zap.NewAtomicLevelAt(zapcore.ErrorLevel)
	}

	zapLog, err := zapConfig.Build()
	if err != nil {
		os.Exit(1)
	}
	ctrl.SetLogger(zapr.NewLogger(zapLog))

	setupLog.Info("starting kubebao-operator", "version", Version)

	// Load OpenBao configuration
	var baoConfig *openbao.Config
	if configFile != "" {
		baoConfig, err = openbao.LoadConfig(configFile)
		if err != nil {
			setupLog.Error(err, "failed to load OpenBao config from file")
			os.Exit(1)
		}
	} else {
		baoConfig = openbao.LoadConfigFromEnv()
	}

	// Create OpenBao client
	hcLogger := hclog.New(&hclog.LoggerOptions{
		Name:  "openbao",
		Level: hclog.LevelFromString(logLevel),
	})

	var baoClient *openbao.Client
	if baoConfig.Address != "" {
		baoClient, err = openbao.NewClient(baoConfig, hcLogger)
		if err != nil {
			setupLog.Error(err, "failed to create OpenBao client")
			// Continue without client - controllers will fail gracefully
		} else {
			setupLog.Info("connected to OpenBao", "address", baoConfig.Address)
		}
	} else {
		setupLog.Info("OpenBao address not configured, skipping client initialization")
	}

	// Create manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "kubebao-operator.kubebao.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	// Setup BaoSecret controller
	if err := (&controller.BaoSecretReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Log:           ctrl.Log.WithName("controllers").WithName("BaoSecret"),
		OpenBaoClient: baoClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BaoSecret")
		os.Exit(1)
	}

	// Setup BaoPolicy controller
	if err := (&controller.BaoPolicyReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		Log:           ctrl.Log.WithName("controllers").WithName("BaoPolicy"),
		OpenBaoClient: baoClient,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "BaoPolicy")
		os.Exit(1)
	}

	// Add health check
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
