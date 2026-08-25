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
	"testing"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

func TestNewProbeExecutor(t *testing.T) {
	tests := []struct {
		name         string
		probeType    string
		expectErr    bool
		expectedType string
	}{
		{
			name:         "creates HTTP probe executor",
			probeType:    string(secretv1alpha1.ProbeTypeHTTP),
			expectErr:    false,
			expectedType: "*probes.HTTPProbe",
		},
		{
			name:         "creates TLS probe executor",
			probeType:    string(secretv1alpha1.ProbeTypeTLS),
			expectErr:    false,
			expectedType: "*probes.TLSProbe",
		},
		{
			name:         "creates PostgreSQL probe executor",
			probeType:    string(secretv1alpha1.ProbeTypePostgreSQL),
			expectErr:    false,
			expectedType: "*probes.PostgresProbe",
		},
		{
			name:         "creates MySQL probe executor",
			probeType:    string(secretv1alpha1.ProbeTypeMySQL),
			expectErr:    false,
			expectedType: "*probes.MySQLProbe",
		},
		{
			name:         "returns error for unsupported probe type",
			probeType:    "UnknownType",
			expectErr:    true,
			expectedType: "",
		},
		{
			name:         "returns error for empty probe type",
			probeType:    "",
			expectErr:    true,
			expectedType: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			executor, err := NewProbeExecutor(tt.probeType)
			if tt.expectErr {
				if err == nil {
					t.Fatalf("expected error for probe type %q, got nil", tt.probeType)
				}
				if executor != nil {
					t.Fatalf("expected nil executor when error occurs, got %v", executor)
				}
				return
			}

			if err != nil {
				t.Fatalf("expected no error for probe type %q, got: %v", tt.probeType, err)
			}
			if executor == nil {
				t.Fatalf("expected non-nil executor for probe type %q", tt.probeType)
			}
		})
	}
}
