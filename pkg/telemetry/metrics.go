/*
Copyright 2026 QuantumSys.

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

package telemetry

import (
	"github.com/prometheus/client_golang/prometheus"
	crmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	// RotationsTotal tracks total secret rotation attempts.
	RotationsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dso_rotations_total",
			Help: "Total number of dynamic secret rotation progression cycles started",
		},
		[]string{"namespace"},
	)

	// RotationsFailed tracks failed validation probe cycles.
	RotationsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dso_rotations_failed_total",
			Help: "Total number of failed canary validation probe runs",
		},
		[]string{"namespace"},
	)

	// CircuitBreakersTripped tracks when a policy trips its consecutive failure threshold.
	CircuitBreakersTripped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dso_circuit_breakers_tripped_total",
			Help: "Total number of times a circuit breaker has tripped and halted reconciliations",
		},
		[]string{"namespace"},
	)

	// ProbeDurationSeconds records latency distribution for validation probes.
	ProbeDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dso_probe_duration_seconds",
			Help:    "Execution duration of synthetic validation probes in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"namespace", "probe_type"},
	)

	// ServiceBusMessagesTotal tracks total Azure Service Bus messages processed, partitioned by status.
	ServiceBusMessagesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dso_servicebus_messages_total",
			Help: "Total number of Azure Service Bus messages processed, partitioned by status.",
		},
		[]string{"status"}, // ack, nack, dlq
	)

	// KeyVaultFetchLatency records latency in seconds for fetching secrets/certificates from Azure Key Vault.
	// Deliberately excludes secret_name: with dynamically generated secrets or many policies, a per-secret
	// label would cause unbounded cardinality growth and risk OOMing Prometheus scrapers.
	KeyVaultFetchLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dso_keyvault_fetch_latency_seconds",
			Help:    "Latency in seconds for fetching secrets/certificates from Azure Key Vault.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"vault_name", "status"},
	)
)

func init() {
	// Register all custom Prometheus metrics into controller-runtime's global registry
	crmetrics.Registry.MustRegister(
		RotationsTotal,
		RotationsFailed,
		CircuitBreakersTripped,
		ProbeDurationSeconds,
		ServiceBusMessagesTotal,
		KeyVaultFetchLatency,
	)
}
