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
	"crypto/sha1" // #nosec G505 -- Legacy thumbprint calculation
	"crypto/sha256"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

// TLSProbe validates endpoint TLS handshakes, certificate expiration, and cryptographic thumbprints.
type TLSProbe struct{}

// Execute connects via TLS to config.Endpoint, inspects peer certificates for expiration,
// and optionally verifies the certificate thumbprint against secretData.
func (p *TLSProbe) Execute(ctx context.Context, config secretv1alpha1.ValidationProbe, secretData map[string][]byte) error {
	ctx, span := telemetry.Tracer.Start(ctx, "ExecuteTLSProbe",
		trace.WithAttributes(
			attribute.String("probe.type", string(secretv1alpha1.ProbeTypeTLS)),
			attribute.String("probe.endpoint", config.Endpoint),
		),
	)
	defer span.End()

	if config.Endpoint == "" {
		err := errors.New("tls probe endpoint must not be empty")
		span.RecordError(err)
		return err
	}

	if config.QueryTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(config.QueryTimeout)*time.Second)
		defer cancel()
	}

	// If secretData provides a pinned thumbprint or certificate payload (tls.crt/cert),
	// we skip standard PKI verification and perform strict cryptographic pinning below.
	// Otherwise, we enforce strict PKI chain validation against root CAs to prevent MITM attacks.
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

	dialer := &tls.Dialer{
		Config: &tls.Config{
			InsecureSkipVerify: hasPinnedSecret, // #nosec G402 -- Custom pinned certificate validation
		},
	}

	rawConn, err := dialer.DialContext(ctx, "tcp", config.Endpoint)
	if err != nil {
		dialErr := fmt.Errorf("tls dial failed for %q: %w", config.Endpoint, err)
		span.RecordError(dialErr)
		return dialErr
	}
	defer func() {
		if rawConn != nil {
			_ = rawConn.Close()
		}
	}()

	tlsConn, ok := rawConn.(*tls.Conn)
	if !ok || tlsConn == nil {
		connErr := fmt.Errorf("failed to obtain tls connection state for %q", config.Endpoint)
		span.RecordError(connErr)
		return connErr
	}

	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) == 0 {
		certErr := fmt.Errorf("no peer certificates presented by endpoint %q", config.Endpoint)
		span.RecordError(certErr)
		return certErr
	}

	leafCert := state.PeerCertificates[0]
	now := time.Now()

	// 1. Expiration check
	if now.After(leafCert.NotAfter) {
		expErr := fmt.Errorf("certificate expired on %s", leafCert.NotAfter.Format(time.RFC3339))
		span.RecordError(expErr)
		return expErr
	}
	if now.Before(leafCert.NotBefore) {
		validErr := fmt.Errorf("certificate is not yet valid (valid from %s)", leafCert.NotBefore.Format(time.RFC3339))
		span.RecordError(validErr)
		return validErr
	}

	// 2. Cryptographic Thumbprint & Secret Content Verification
	if secretData != nil {
		cleanHex := func(s string) string {
			s = strings.ReplaceAll(s, ":", "")
			s = strings.ReplaceAll(s, " ", "")
			return strings.ToLower(strings.TrimSpace(s))
		}

		// Check explicitly provided thumbprint
		if expectedThumbprintBytes, exists := secretData["thumbprint"]; exists && len(expectedThumbprintBytes) > 0 {
			expected := cleanHex(string(expectedThumbprintBytes))
			actualSHA256 := fmt.Sprintf("%x", sha256.Sum256(leafCert.Raw))
			actualSHA1 := fmt.Sprintf("%x", sha1.Sum(leafCert.Raw)) // #nosec G401 -- Legacy thumbprint calculation

			if expected != actualSHA256 && expected != actualSHA1 {
				mismatchErr := fmt.Errorf("tls certificate thumbprint mismatch against expected secret value")
				span.RecordError(mismatchErr)
				return mismatchErr
			}
		}

		// Check PEM certificate payload (e.g. tls.crt)
		var certPEMBytes []byte
		if b, ok := secretData["tls.crt"]; ok {
			certPEMBytes = b
		} else if b, ok := secretData["cert"]; ok {
			certPEMBytes = b
		}

		if len(certPEMBytes) > 0 {
			block, _ := pem.Decode(certPEMBytes)
			if block != nil && len(block.Bytes) > 0 {
				expectedHash := sha256.Sum256(block.Bytes)
				actualHash := sha256.Sum256(leafCert.Raw)
				if expectedHash != actualHash {
					derErr := fmt.Errorf("tls certificate DER hash does not match tls.crt payload")
					span.RecordError(derErr)
					return derErr
				}
			}
		}
	}

	return nil
}
