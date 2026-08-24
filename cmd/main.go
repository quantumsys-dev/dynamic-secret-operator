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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
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
		LeaderElectionID:       "81b37f41.quantumsys.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create buffered channel to bridge Service Bus rotation triggers to controller-runtime watch queue
	eventsChannel := make(chan event.GenericEvent, 100)

	if err = (&controller.DynamicSecretPolicyReconciler{
		Client:        mgr.GetClient(),
		Scheme:        mgr.GetScheme(),
		SecretFetcher: secretFetcher,
		EventsChannel: eventsChannel,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DynamicSecretPolicy")
		os.Exit(1)
	}

	if sbListener != nil {
		// Wire Service Bus message callback handler to forward events to controller-runtime
		sbListener.SetHandler(func(ctx context.Context, msg *azservicebus.ReceivedMessage, ack azure.AckFunc) error {
			handlerLog := ctrl.LoggerFrom(ctx).WithName("servicebus-handler").WithValues("messageID", msg.MessageID)
			handlerLog.Info("processing Service Bus event for secret rotation")

			var eventData struct {
				Subject   string `json:"subject"`
				EventType string `json:"eventType"`
				Data      struct {
					ObjectName string `json:"ObjectName"`
					ObjectType string `json:"ObjectType"`
					Version    string `json:"Version"`
				} `json:"data"`
				PolicyName string `json:"policyName"`
				Namespace  string `json:"namespace"`
			}

			_ = json.Unmarshal(msg.Body, &eventData)

			targetObjectName := eventData.Data.ObjectName
			if targetObjectName == "" && eventData.Subject != "" {
				parts := strings.Split(eventData.Subject, "/")
				for i, part := range parts {
					if part == "secrets" && i+1 < len(parts) {
						targetObjectName = parts[i+1]
						break
					}
				}
			}

			policyList := &secretv1alpha1.DynamicSecretPolicyList{}
			listOpts := []client.ListOption{}
			if eventData.Namespace != "" {
				listOpts = append(listOpts, client.InNamespace(eventData.Namespace))
			}

			if err := mgr.GetClient().List(ctx, policyList, listOpts...); err != nil {
				handlerLog.Error(err, "failed to list DynamicSecretPolicies for Service Bus event")
				return err
			}

			matchedCount := 0
			for i := range policyList.Items {
				p := &policyList.Items[i]
				if targetObjectName == "" || p.Spec.VaultRef.ObjectName == targetObjectName || p.Name == eventData.PolicyName {
					select {
					case eventsChannel <- event.GenericEvent{Object: p}:
						matchedCount++
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}

			handlerLog.Info("enqueued reconciliation events for matching policies",
				"matchedPolicies", matchedCount,
				"objectName", targetObjectName,
			)

			// Acknowledge message only after successfully pushing to event channel
			if err := ack(ctx); err != nil {
				handlerLog.Error(err, "failed to complete Service Bus message")
				return err
			}

			return nil
		})

		if err := mgr.Add(sbListener); err != nil {
			setupLog.Error(err, "unable to register ServiceBusListener with manager")
			os.Exit(1)
		}
		setupLog.Info("registered Azure Service Bus peek-lock listener with manager and wired event bridge")
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
