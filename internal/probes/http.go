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

package probes

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

// HTTPProbe validates canary health by issuing synthetic HTTP GET requests.
type HTTPProbe struct {
	// Client allows injecting custom HTTP client for testing.
	Client *http.Client
}

// Execute performs an HTTP GET against config.Endpoint and asserts a 2xx or 3xx status code.
func (p *HTTPProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	ctx, span := telemetry.Tracer.Start(ctx, "ExecuteHTTPProbe",
		trace.WithAttributes(
			attribute.String("probe.type", string(secretv1alpha1.ProbeTypeHTTP)),
			attribute.String("probe.endpoint", config.Endpoint),
		),
	)
	defer span.End()

	if config.Endpoint == "" {
		err := fmt.Errorf("http probe endpoint must not be empty")
		span.RecordError(err)
		return err
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	client := p.Client
	if client == nil {
		hasPinnedSecret := false
		if secretData != nil {
			if tBytes, ok := secretData["thumbprint"]; ok && len(tBytes) > 0 {
				hasPinnedSecret = true
			}
			if cBytes, ok := secretData["tls.crt"]; ok && len(cBytes) > 0 {
				hasPinnedSecret = true
			}
			if cBytes, ok := secretData["cert"]; ok && len(cBytes) > 0 {
				hasPinnedSecret = true
			}
		}

		baseTransport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: hasPinnedSecret,
			},
			ResponseHeaderTimeout: 10 * time.Second,
		}
		client = &http.Client{
			Transport: otelhttp.NewTransport(baseTransport),
			Timeout:   0, // Rely on ctx for timeout enforcement
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.Endpoint, nil)
	if err != nil {
		reqErr := fmt.Errorf("failed to construct http probe request for %q: %w", config.Endpoint, err)
		span.RecordError(reqErr)
		return reqErr
	}

	resp, err := client.Do(req)
	if err != nil {
		doErr := fmt.Errorf("http probe request failed for %q: %w", config.Endpoint, err)
		span.RecordError(doErr)
		return doErr
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		statusErr := fmt.Errorf("http probe received unsuccessful status code %d from %q", resp.StatusCode, config.Endpoint)
		span.RecordError(statusErr)
		return statusErr
	}

	return nil
}
