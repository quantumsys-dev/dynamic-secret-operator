/*
Copyright 2026.

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
	"crypto/tls"
	"flag"
	"fmt"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys/dynamic-secret-operator/internal/azure"
	"github.com/quantumsys/dynamic-secret-operator/internal/controller"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(secretv1alpha1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

// syntheticFetcher is used exclusively in local E2E container tests
type syntheticFetcher struct{}

func (s *syntheticFetcher) GetSecret(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
	if version == "" {
		version = "v1"
	}
	return &azure.SecretPayload{
		Value:   []byte("synthetic-e2e-secret-content"),
		Version: version,
		ID:      fmt.Sprintf("%s/secrets/%s/%s", vaultURI, secretName, version),
	}, nil
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string
	var secureMetrics bool
	var enableHTTP2 bool
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.BoolVar(&enableHTTP2, "enable-http2", false,
		"If set, HTTP/2 will be enabled for the metrics and webhook servers")
	opts := zap.Options{
		Development: false,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	// Enforce structured JSON logging with standard operator context keys
	logger := zap.New(zap.UseFlagOptions(&opts), zap.JSONEncoder()).WithValues("operator", "dynamic-secret-operator")
	ctrl.SetLogger(logger)
	setupLog = ctrl.Log.WithName("setup")

	var secretFetcher azure.SecretFetcher
	var sbListener *azure.ServiceBusListener

	// In E2E synthetic testing mode, bypass Azure cloud dependencies
	if os.Getenv("E2E_SYNTHETIC_MODE") == "true" {
		setupLog.Info("operating in E2E_SYNTHETIC_MODE; using synthetic secret fetcher")
		secretFetcher = &syntheticFetcher{}
	} else {
		// Initialize zero-trust Azure Workload Identity authentication (fail-fast)
		azureCred, err := azure.NewAzureCredential()
		if err != nil {
			setupLog.Error(err, "unable to initialize Azure Workload Identity credential; refusing to start")
			os.Exit(1)
		}
		setupLog.Info("successfully initialized Azure Workload Identity token credential")

		kvFetcher, err := azure.NewKeyVaultFetcher(azureCred)
		if err != nil {
			setupLog.Error(err, "unable to create Key Vault fetcher")
			os.Exit(1)
		}
		secretFetcher = kvFetcher

		// Register Azure Service Bus Peek-Lock Listener if configured
		sbNamespace := os.Getenv("SERVICEBUS_NAMESPACE")
		sbQueue := os.Getenv("SERVICEBUS_QUEUE_NAME")
		if sbNamespace != "" && sbQueue != "" {
			var err error
			sbListener, err = azure.NewServiceBusListener(sbNamespace, sbQueue, azureCred)
			if err != nil {
				setupLog.Error(err, "unable to create Azure Service Bus listener")
				os.Exit(1)
			}
			setupLog.Info("configured Azure Service Bus peek-lock listener", "namespace", sbNamespace, "queue", sbQueue)
		}
	}

	disableHTTP2 := func(c *tls.Config) {
		setupLog.Info("disabling http/2")
		c.NextProtos = []string{"http/1.1"}
	}

	tlsOpts := []func(*tls.Config){}
	if !enableHTTP2 {
		tlsOpts = append(tlsOpts, disableHTTP2)
	}

	webhookServer := webhook.NewServer(webhook.Options{
		TLSOpts: tlsOpts,
	})

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
			TLSOpts:       tlsOpts,
		},
		WebhookServer:          webhookServer,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "81b37f41.quantumsys.io",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controller.DynamicSecretPolicyReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		SecretFetcher: secretFetcher,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DynamicSecretPolicy")
		os.Exit(1)
	}

	if sbListener != nil {
		if err := mgr.Add(sbListener); err != nil {
			setupLog.Error(err, "unable to register ServiceBusListener with manager")
			os.Exit(1)
		}
		setupLog.Info("registered Azure Service Bus peek-lock listener with manager")
	}

	if os.Getenv("ENABLE_WEBHOOKS") == "true" {
		if err = (&secretv1alpha1.DynamicSecretPolicy{}).SetupWebhookWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to create webhook", "webhook", "DynamicSecretPolicy")
			os.Exit(1)
		}
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

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
