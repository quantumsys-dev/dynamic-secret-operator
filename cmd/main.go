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
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	argorolloutsv1alpha1 "github.com/argoproj/argo-rollouts/pkg/apis/rollouts/v1alpha1"
	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/azure"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/controller"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/events"
	sourceProvider "github.com/quantumsys-dev/dynamic-secret-operator/internal/source"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(secretv1alpha1.AddToScheme(scheme))
	utilruntime.Must(argorolloutsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(argov1alpha1.AddToScheme(scheme))
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
	var eventBufferSize int
	var maxConcurrentReconciles int
	var watchNamespaces string
	var syncPeriod time.Duration
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"If set the metrics endpoint is served securely")
	flag.IntVar(&eventBufferSize, "event-buffer-size", 1000,
		"The buffer capacity of the internal event channel bridging Azure Service Bus to the controller.")
	flag.IntVar(&maxConcurrentReconciles, "max-concurrent-reconciles", 5,
		"The maximum number of concurrent Reconciles which can be run.")
	flag.StringVar(&watchNamespaces, "watch-namespaces", "",
		"Comma-separated list of namespaces to restrict the manager's cache and watches to, "+
			"matching the RBAC grant when deployed with rbac.scope=Namespaced. Empty (default) "+
			"watches all namespaces cluster-wide and requires a ClusterRole.")
	flag.DurationVar(&syncPeriod, "sync-period", 5*time.Minute,
		"Full resync period for the manager's cache. This bounds how long a DynamicSecretPolicy "+
			"can go without a fresh drift-check against its source (e.g. Key Vault) if an "+
			"external rotation event (Azure Service Bus) is ever lost - the reconciler treats "+
			"a resync identically to a real event once DesiredRevision has been cleared by a "+
			"prior promotion. Without this, controller-runtime's 10h default would apply.")
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

	// eventIngester is the provider-agnostic handle; currently backed by Azure Service Bus.
	// Replace with a different events.EventIngester implementation for AWS/GCP/Vault.
	var eventIngester events.EventIngester
	if sbListener != nil {
		eventIngester = sbListener
	}

	// A single Kubernetes label selector can only AND requirements together, so secrets DSO
	// owns (ManagedValueTrue) and externally owned source secrets DSO merely watches
	// (ManagedValueWatch, e.g. an ESO sync target) must share the same label key with an
	// In-operator selector rather than being expressed as two separate label keys.
	secretCacheRequirement, err := labels.NewRequirement(
		controller.LabelManaged,
		selection.In,
		[]string{controller.ManagedValueTrue, controller.ManagedValueWatch},
	)
	if err != nil {
		setupLog.Error(err, "unable to build secret cache label selector")
		os.Exit(1)
	}

	// Restrict the manager's cache (and therefore its List/Watch calls) to the namespaces the
	// operator's ServiceAccount actually has RBAC permissions in when deployed with
	// rbac.scope=Namespaced. Without this, a namespaced Role/RoleBinding pairs with a manager
	// that still tries to watch cluster-wide, which fails outright with Forbidden errors.
	var defaultNamespaces map[string]cache.Config
	for _, ns := range strings.Split(watchNamespaces, ",") {
		ns = strings.TrimSpace(ns)
		if ns == "" {
			continue
		}
		if defaultNamespaces == nil {
			defaultNamespaces = make(map[string]cache.Config)
		}
		defaultNamespaces[ns] = cache.Config{}
	}
	if len(defaultNamespaces) > 0 {
		setupLog.Info("restricting manager cache to explicit watch namespaces", "namespaces", defaultNamespaces)
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Cache: cache.Options{
			SyncPeriod:        &syncPeriod,
			DefaultNamespaces: defaultNamespaces,
			ByObject: map[client.Object]cache.ByObject{
				&corev1.Secret{}: {
					Label: labels.NewSelector().Add(*secretCacheRequirement),
				},
			},
		},
		Metrics: metricsserver.Options{
			BindAddress:   metricsAddr,
			SecureServing: secureMetrics,
		},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "81b37f41.quantumsys.dev",
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Create buffered channel to bridge Service Bus rotation triggers to controller-runtime watch queue
	if eventBufferSize <= 0 {
		eventBufferSize = 1000
	}
	eventsChannel := make(chan event.GenericEvent, eventBufferSize)

	kubeClient, err := kubernetes.NewForConfig(mgr.GetConfig())
	if err != nil {
		setupLog.Error(err, "unable to create kubernetes clientset for log retrieval")
	}

	providerRegistry := sourceProvider.SetupDefaultRegistry(mgr.GetAPIReader(), secretFetcher)

	if err = (&controller.DynamicSecretPolicyReconciler{
		Client:                  mgr.GetClient(),
		Scheme:                  mgr.GetScheme(),
		SecretFetcher:           secretFetcher,
		ProviderRegistry:        providerRegistry,
		KubeClient:              kubeClient,
		MaxConcurrentReconciles: maxConcurrentReconciles,
		EventsChannel:           eventsChannel,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DynamicSecretPolicy")
		os.Exit(1)
	}

	if eventIngester != nil {
		// Wire provider-agnostic EventHandler to forward rotation events to controller-runtime.
		eventIngester.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
			handlerLog := ctrl.LoggerFrom(ctx).WithName("event-ingester-handler")
			handlerLog.Info("processing rotation event")

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

			_ = json.Unmarshal(body, &eventData)

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
				handlerLog.Error(err, "failed to list DynamicSecretPolicies for rotation event")
				return err
			}

			matchedCount := 0
			for i := range policyList.Items {
				p := &policyList.Items[i]
				if targetObjectName == "" || p.Spec.GetVaultObjectName() == targetObjectName || p.Name == eventData.PolicyName {
					timeoutCtx, timeoutCancel := context.WithTimeout(ctx, 2*time.Second)
					select {
					case eventsChannel <- event.GenericEvent{Object: p}:
						matchedCount++
					case <-timeoutCtx.Done():
						timeoutCancel()
						handlerLog.Error(nil, "event channel full; rate limiting ingestion, NACKing message")
						return fmt.Errorf("controller event channel at capacity")
					case <-ctx.Done():
						timeoutCancel()
						return ctx.Err()
					}
					timeoutCancel()
				}
			}

			handlerLog.Info("enqueued reconciliation events for matching policies; awaiting materialization for settlement",
				"matchedPolicies", matchedCount,
				"objectName", targetObjectName,
			)

			// The reconciler is level-triggered: once accepted into the controller-runtime work
			// queue it will retry until success or circuit-breaker trip, so the message can be
			// acked immediately rather than held pending eventual secret materialization.
			if err := ack(ctx); err != nil {
				handlerLog.Error(err, "failed to complete event ingester message after enqueueing")
				return err
			}

			return nil
		})

		if err := mgr.Add(eventIngester); err != nil {
			setupLog.Error(err, "unable to register EventIngester with manager")
			os.Exit(1)
		}
		setupLog.Info("registered EventIngester with manager and wired event bridge")
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
