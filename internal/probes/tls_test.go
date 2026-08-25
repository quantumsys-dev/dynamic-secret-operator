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
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func generateTestCert(notBefore, notAfter time.Time) (tls.Certificate, []byte, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"QuantumSys Test"},
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})

	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, err
	}

	return tlsCert, derBytes, nil
}

func TestTLSProbe_Execute(t *testing.T) {
	t.Run("succeeds on valid certificate and matching thumbprint", func(t *testing.T) {
		tlsCert, derBytes, err := generateTestCert(time.Now().Add(-1*time.Hour), time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("failed to generate test cert: %v", err)
		}

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		ts.StartTLS()
		defer ts.Close()

		endpoint := strings.TrimPrefix(ts.URL, "https://")
		thumbprint := fmt.Sprintf("%x", sha256.Sum256(derBytes))

		probe := &TLSProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:         secretv1alpha1.ProbeTypeTLS,
			Endpoint:     endpoint,
			QueryTimeout: 5,
		}

		secretData := map[string][]byte{
			"thumbprint": []byte(thumbprint),
			"tls.crt":    pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes}),
		}

		if err := probe.Execute(context.Background(), cfg, secretData); err != nil {
			t.Fatalf("expected TLS probe success, got: %v", err)
		}
	})

	t.Run("fails on expired certificate", func(t *testing.T) {
		tlsCert, _, err := generateTestCert(time.Now().Add(-48*time.Hour), time.Now().Add(-24*time.Hour))
		if err != nil {
			t.Fatalf("failed to generate expired cert: %v", err)
		}

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		ts.StartTLS()
		defer ts.Close()

		endpoint := strings.TrimPrefix(ts.URL, "https://")

		probe := &TLSProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeTLS,
			Endpoint: endpoint,
		}

		err = probe.Execute(context.Background(), cfg, nil)
		if err == nil {
			t.Fatalf("expected error for expired certificate, got nil")
		}
		if !strings.Contains(err.Error(), "expired") {
			t.Errorf("expected error message mentioning 'expired', got: %v", err)
		}
	})

	t.Run("fails on certificate not yet valid", func(t *testing.T) {
		tlsCert, _, err := generateTestCert(time.Now().Add(24*time.Hour), time.Now().Add(48*time.Hour))
		if err != nil {
			t.Fatalf("failed to generate future cert: %v", err)
		}

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		ts.StartTLS()
		defer ts.Close()

		endpoint := strings.TrimPrefix(ts.URL, "https://")

		probe := &TLSProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeTLS,
			Endpoint: endpoint,
		}

		err = probe.Execute(context.Background(), cfg, nil)
		if err == nil {
			t.Fatalf("expected error for future certificate, got nil")
		}
	})

	t.Run("fails on thumbprint mismatch", func(t *testing.T) {
		tlsCert, _, err := generateTestCert(time.Now().Add(-1*time.Hour), time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("failed to generate cert: %v", err)
		}

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		ts.StartTLS()
		defer ts.Close()

		endpoint := strings.TrimPrefix(ts.URL, "https://")

		probe := &TLSProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeTLS,
			Endpoint: endpoint,
		}

		secretData := map[string][]byte{
			"thumbprint": []byte("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"),
		}

		err = probe.Execute(context.Background(), cfg, secretData)
		if err == nil {
			t.Fatalf("expected error on thumbprint mismatch, got nil")
		}
		if !strings.Contains(err.Error(), "mismatch") {
			t.Errorf("expected error mentioning mismatch, got: %v", err)
		}
	})

	t.Run("fails on tls.crt hash mismatch", func(t *testing.T) {
		tlsCert, _, err := generateTestCert(time.Now().Add(-1*time.Hour), time.Now().Add(24*time.Hour))
		if err != nil {
			t.Fatalf("failed to generate cert: %v", err)
		}

		ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		ts.TLS = &tls.Config{
			Certificates: []tls.Certificate{tlsCert},
		}
		ts.StartTLS()
		defer ts.Close()

		endpoint := strings.TrimPrefix(ts.URL, "https://")

		probe := &TLSProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeTLS,
			Endpoint: endpoint,
		}

		mismatchedPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("different-bytes")})
		secretData := map[string][]byte{
			"tls.crt": mismatchedPEM,
		}

		err = probe.Execute(context.Background(), cfg, secretData)
		if err == nil {
			t.Fatalf("expected error on tls.crt mismatch, got nil")
		}
	})

	t.Run("fails on empty endpoint", func(t *testing.T) {
		probe := &TLSProbe{}
		cfg := secretv1alpha1.ValidationProbe{
			Type:     secretv1alpha1.ProbeTypeTLS,
			Endpoint: "",
		}

		if err := probe.Execute(context.Background(), cfg, nil); err == nil {
			t.Fatalf("expected error on empty endpoint, got nil")
		}
	})
}
