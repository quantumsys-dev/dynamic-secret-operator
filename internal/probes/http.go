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

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

// HTTPProbe validates canary health by issuing synthetic HTTP GET requests.
type HTTPProbe struct {
	// Client allows injecting custom HTTP client for testing.
	Client *http.Client
}

// Execute performs an HTTP GET against config.Endpoint and asserts a 2xx or 3xx status code.
func (p *HTTPProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	if config.Endpoint == "" {
		return fmt.Errorf("http probe endpoint must not be empty")
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	client := p.Client
	if client == nil {
		transport := &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true, // Allow canary internal probes with self-signed cluster certificates
			},
			ResponseHeaderTimeout: 10 * time.Second,
		}
		client = &http.Client{
			Transport: transport,
			Timeout:   0, // Rely on ctx for timeout enforcement
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, config.Endpoint, nil)
	if err != nil {
		return fmt.Errorf("failed to construct http probe request for %q: %w", config.Endpoint, err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("http probe request failed for %q: %w", config.Endpoint, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 400 {
		return fmt.Errorf("http probe received unsuccessful status code %d from %q", resp.StatusCode, config.Endpoint)
	}

	return nil
}
