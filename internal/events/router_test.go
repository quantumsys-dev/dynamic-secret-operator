// Copyright 2026 QuantumSys. Licensed under the Apache License, Version 2.0.
// See the full license text at http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRotationEvent(t *testing.T) {
	tests := []struct {
		name           string
		payload        string
		expectedObject string
		expectedPolicy string
		expectError    bool
	}{
		{
			name:           "Standard EventGrid Payload",
			payload:        `{"subject": "/vaults/my-vault/secrets/db-password/versions/abc12345", "data": {"ObjectName": "db-password", "ObjectType": "Secret"}}`,
			expectedObject: "db-password",
		},
		{
			name:           "Fallback to Subject parsing when Data is empty",
			payload:        `{"subject": "/vaults/my-vault/secrets/fallback-secret/versions/xyz"}`,
			expectedObject: "fallback-secret",
		},
		{
			name:        "Malformed JSON",
			payload:     `{invalid-json`,
			expectError: true,
		},
		{
			name:           "Missing Subject and Data",
			payload:        `{"id": "12345"}`,
			expectedObject: "",
		},
		{
			name:           "Explicit PolicyName and Namespace",
			payload:        `{"policyName": "redis-rotation-policy", "namespace": "prod", "data": {"ObjectName": "redis-auth"}}`,
			expectedObject: "redis-auth",
			expectedPolicy: "redis-rotation-policy",
		},
		{
			name:           "Trailing secrets in subject without subsequent segment",
			payload:        `{"subject": "/vaults/my-vault/secrets"}`,
			expectedObject: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj, policy, err := ParseRotationEvent([]byte(tt.payload))
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedObject, obj)
				if tt.expectedPolicy != "" {
					assert.Equal(t, tt.expectedPolicy, policy)
				}
			}
		})
	}
}

func TestParseRotationEventPayload(t *testing.T) {
	t.Run("extracts all fields including namespace", func(t *testing.T) {
		payloadJSON := []byte(`{
			"subject": "/vaults/corp-kv/secrets/tls-cert/versions/v1",
			"data": {"ObjectName": "tls-cert"},
			"policyName": "tls-policy",
			"namespace": "ingress"
		}`)

		payload, err := ParseRotationEventPayload(payloadJSON)
		require.NoError(t, err)
		assert.Equal(t, "tls-cert", payload.ObjectName)
		assert.Equal(t, "tls-policy", payload.PolicyName)
		assert.Equal(t, "ingress", payload.Namespace)
	})

	t.Run("returns error on invalid json", func(t *testing.T) {
		_, err := ParseRotationEventPayload([]byte(`{not-json`))
		require.Error(t, err)
	})
}
