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
		[]string{"policy_name", "namespace"},
	)

	// RotationsFailed tracks failed validation probe cycles.
	RotationsFailed = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dso_rotations_failed",
			Help: "Total number of failed canary validation probe runs",
		},
		[]string{"policy_name", "namespace"},
	)

	// CircuitBreakersTripped tracks when a policy trips its consecutive failure threshold.
	CircuitBreakersTripped = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "dso_circuit_breakers_tripped",
			Help: "Total number of times a circuit breaker has tripped and halted reconciliations",
		},
		[]string{"policy_name", "namespace"},
	)

	// ProbeDurationSeconds records latency distribution for validation probes.
	ProbeDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "dso_probe_duration_seconds",
			Help:    "Execution duration of synthetic validation probes in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"policy_name", "namespace", "probe_type"},
	)
)

func init() {
	// Register all custom Prometheus metrics into controller-runtime's global registry
	crmetrics.Registry.MustRegister(
		RotationsTotal,
		RotationsFailed,
		CircuitBreakersTripped,
		ProbeDurationSeconds,
	)
}
