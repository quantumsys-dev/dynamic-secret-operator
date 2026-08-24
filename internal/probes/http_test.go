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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	secretv1alpha1 "github.com/quantumsys/dynamic-secret-operator/api/v1alpha1"
)

func TestHTTPProbe_Execute(t *testing.T) {
	t.Run("succeeds on 200 OK", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
		}))
		defer ts.Close()

		probe := &HTTPProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:         secretv1alpha1.ProbeTypeHTTP,
			Endpoint:     ts.URL,
			QueryTimeout: 5,
		}

		if err := probe.Execute(context.Background(), cfg, nil); err != nil {
			t.Fatalf("expected HTTP probe success, got: %v", err)
		}
	})

	t.Run("succeeds on 302 Redirect", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/target" {
				w.WriteHeader(http.StatusOK)
				return
			}
			http.Redirect(w, r, "/target", http.StatusFound)
		}))
		defer ts.Close()

		probe := &HTTPProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeHTTP,
			Endpoint: ts.URL,
		}

		if err := probe.Execute(context.Background(), cfg, nil); err != nil {
			t.Fatalf("expected redirect to be accepted, got: %v", err)
		}
	})

	t.Run("fails on 500 Internal Server Error", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer ts.Close()

		probe := &HTTPProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeHTTP,
			Endpoint: ts.URL,
		}

		if err := probe.Execute(context.Background(), cfg, nil); err == nil {
			t.Fatalf("expected error for 500 status code, got nil")
		}
	})

	t.Run("fails on empty endpoint", func(t *testing.T) {
		probe := &HTTPProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeHTTP,
			Endpoint: "",
		}

		if err := probe.Execute(context.Background(), cfg, nil); err == nil {
			t.Fatalf("expected error for empty endpoint, got nil")
		}
	})

	t.Run("fails on context timeout", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer ts.Close()

		probe := &HTTPProbe{}
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeHTTP,
			Endpoint: ts.URL,
		}

		if err := probe.Execute(ctx, cfg, nil); err == nil {
			t.Fatalf("expected timeout error, got nil")
		}
	})
}
