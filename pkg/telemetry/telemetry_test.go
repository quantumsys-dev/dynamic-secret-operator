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
	"context"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func TestTelemetry_MetricsAndTracing(t *testing.T) {
	t.Run("records metrics safely without panic", func(t *testing.T) {
		ns := "default"

		RotationsTotal.WithLabelValues(ns).Inc()
		RotationsFailed.WithLabelValues(ns).Inc()
		CircuitBreakersTripped.WithLabelValues(ns).Inc()
		ProbeDurationSeconds.WithLabelValues(ns, "HTTP").Observe(0.125)
		ServiceBusMessagesTotal.WithLabelValues("ack").Inc()
		ServiceBusMessagesTotal.WithLabelValues("nack").Inc()
		ServiceBusMessagesTotal.WithLabelValues("dlq").Inc()
		KeyVaultFetchLatency.WithLabelValues("https://myvault.vault.azure.net", "success").Observe(0.045)
		KeyVaultFetchLatency.WithLabelValues("https://myvault.vault.azure.net", "error").Observe(0.010)
	})

	t.Run("starts and ends tracer spans cleanly", func(t *testing.T) {
		ctx, span := Tracer.Start(context.Background(), "TestSpan",
			trace.WithAttributes(attribute.String("test.key", "test.val")),
		)
		defer span.End()

		if span == nil {
			t.Errorf("expected valid span instance")
		}
		_ = ctx
	})
}
