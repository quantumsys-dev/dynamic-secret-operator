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

package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newValidPolicy() *DynamicSecretPolicy {
	return &DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "default",
		},
		Spec: DynamicSecretPolicySpec{
			VaultRef: VaultReference{
				KeyVaultURI: "https://my-prod-vault.vault.azure.net",
				ObjectName:  "db-credentials",
				ObjectType:  VaultObjectTypeSecret,
			},
			WorkloadSelector: WorkloadSelector{
				Kind: "Deployment",
				Name: "payment-service",
			},
			CanaryStrategy: &CanaryStrategy{
				TimeoutSeconds: 45,
			},
			ValidationProbes: []ValidationProbe{
				{
					Type:         ProbeTypePostgreSQL,
					Endpoint:     "postgres.database.svc:5432",
					QueryTimeout: 5,
				},
			},
			RollbackConfig: &RollbackConfig{
				AutoRollback:            true,
				CircuitBreakerThreshold: 5,
			},
		},
	}
}

func TestDynamicSecretPolicy_Default(t *testing.T) {
	ctx := context.Background()

	t.Run("defaults omitted rollback and canary strategy", func(t *testing.T) {
		policy := &DynamicSecretPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-default",
				Namespace: "default",
			},
			Spec: DynamicSecretPolicySpec{
				VaultRef: VaultReference{
					KeyVaultURI: "https://my-vault.vault.azure.net",
					ObjectName:  "api-key",
					ObjectType:  VaultObjectTypeSecret,
				},
				WorkloadSelector: WorkloadSelector{
					Kind: "Deployment",
					Name: "order-service",
				},
			},
		}

		err := policy.Default(ctx, policy)
		if err != nil {
			t.Fatalf("unexpected error during defaulting: %v", err)
		}

		if policy.Spec.RollbackConfig == nil {
			t.Fatalf("expected RollbackConfig to be initialized")
		}
		if policy.Spec.RollbackConfig.CircuitBreakerThreshold != 3 {
			t.Errorf("expected default CircuitBreakerThreshold to be 3, got %d", policy.Spec.RollbackConfig.CircuitBreakerThreshold)
		}
		if !policy.Spec.RollbackConfig.AutoRollback {
			t.Errorf("expected default AutoRollback to be true, got %v", policy.Spec.RollbackConfig.AutoRollback)
		}

		if policy.Spec.CanaryStrategy == nil {
			t.Fatalf("expected CanaryStrategy to be initialized")
		}
		if policy.Spec.CanaryStrategy.TimeoutSeconds != 30 {
			t.Errorf("expected default TimeoutSeconds to be 30, got %d", policy.Spec.CanaryStrategy.TimeoutSeconds)
		}
	})

	t.Run("defaults zero values in existing configs", func(t *testing.T) {
		policy := &DynamicSecretPolicy{
			Spec: DynamicSecretPolicySpec{
				RollbackConfig: &RollbackConfig{
					CircuitBreakerThreshold: 0,
				},
				CanaryStrategy: &CanaryStrategy{
					TimeoutSeconds: 0,
				},
			},
		}

		err := policy.Default(ctx, policy)
		if err != nil {
			t.Fatalf("unexpected error during defaulting: %v", err)
		}

		if policy.Spec.RollbackConfig.CircuitBreakerThreshold != 3 {
			t.Errorf("expected CircuitBreakerThreshold to be defaulted to 3, got %d", policy.Spec.RollbackConfig.CircuitBreakerThreshold)
		}
		if policy.Spec.CanaryStrategy.TimeoutSeconds != 30 {
			t.Errorf("expected TimeoutSeconds to be defaulted to 30, got %d", policy.Spec.CanaryStrategy.TimeoutSeconds)
		}
	})
}

func TestDynamicSecretPolicy_ValidateCreate(t *testing.T) {
	ctx := context.Background()

	t.Run("accepts valid DynamicSecretPolicy payload", func(t *testing.T) {
		policy := newValidPolicy()
		warnings, err := policy.ValidateCreate(ctx, policy)
		if err != nil {
			t.Fatalf("expected valid policy to pass validation, got error: %v", err)
		}
		if warnings != nil {
			t.Errorf("expected no warnings, got: %v", warnings)
		}
	})

	t.Run("rejects invalid KeyVaultURI format", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.VaultRef.KeyVaultURI = "http://insecure-vault.net"

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected invalid KeyVaultURI to be rejected")
		}
	})

	t.Run("rejects non-Azure URI", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.VaultRef.KeyVaultURI = "https://vault.malicious.com/secret"

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected non-Azure KeyVault URI to be rejected")
		}
	})

	t.Run("security: rejects SSRF unanchored domain suffix bypasses", func(t *testing.T) {
		ssrfPayloads := []string{
			"https://my-vault.vault.azure.net.attacker.com",
			"https://my-vault.vault.azure.net.attacker.com/secrets/steal",
			"https://my-vault.vault.azure.net-malicious.org",
			"https://my-vault.vault.azure.net.evil.io",
			"https://evil.com/https://my-vault.vault.azure.net",
		}

		for _, payload := range ssrfPayloads {
			policy := newValidPolicy()
			policy.Spec.VaultRef.KeyVaultURI = payload

			_, err := policy.ValidateCreate(ctx, policy)
			if err == nil {
				t.Errorf("CRITICAL SECURITY VULNERABILITY (SSRF): webhook accepted malicious URI %q", payload)
			}
		}
	})

	t.Run("security: accepts valid anchored Azure Key Vault endpoints", func(t *testing.T) {
		validURIs := []string{
			"https://my-prod-vault.vault.azure.net",
			"https://my-prod-vault.vault.azure.net/",
			"https://my-prod-vault.vault.azure.net/secrets/db-pass",
			"https://vault-123.vault.azure.net",
			"https://my-china-vault.vault.azure.cn",
			"https://my-gov-vault.vault.usgovcloudapi.net",
			"https://my-gov-vault.vault.usgovcloudapi.net/secrets/test",
		}

		for _, uri := range validURIs {
			policy := newValidPolicy()
			policy.Spec.VaultRef.KeyVaultURI = uri

			_, err := policy.ValidateCreate(ctx, policy)
			if err != nil {
				t.Errorf("expected valid Key Vault URI %q to pass, got: %v", uri, err)
			}
		}
	})

	t.Run("rejects empty ObjectName", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.VaultRef.ObjectName = ""

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected empty ObjectName to be rejected")
		}
	})

	t.Run("rejects unsupported ObjectType", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.VaultRef.ObjectType = VaultObjectType("InvalidType")

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected invalid ObjectType to be rejected")
		}
	})

	t.Run("rejects missing workload selector kind or name", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.WorkloadSelector.Kind = ""

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected empty workload Kind to be rejected")
		}

		policy = newValidPolicy()
		policy.Spec.WorkloadSelector.Name = ""
		_, err = policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected empty workload Name to be rejected")
		}
	})

	t.Run("rejects invalid probe type", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.ValidationProbes = []ValidationProbe{
			{
				Type: ProbeType("UNKNOWN"),
			},
		}

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected unknown probe type to be rejected")
		}
	})

	t.Run("rejects invalid canary timeout", func(t *testing.T) {
		policy := newValidPolicy()
		policy.Spec.CanaryStrategy = &CanaryStrategy{
			TimeoutSeconds: -10,
		}

		_, err := policy.ValidateCreate(ctx, policy)
		if err == nil {
			t.Fatalf("expected negative CanaryStrategy.TimeoutSeconds to be rejected")
		}
	})
}

func TestDynamicSecretPolicy_ValidateUpdate(t *testing.T) {
	ctx := context.Background()

	t.Run("validates updates correctly", func(t *testing.T) {
		oldPolicy := newValidPolicy()
		newPolicy := newValidPolicy()
		newPolicy.Spec.VaultRef.KeyVaultURI = "https://new-vault.vault.azure.net"

		warnings, err := newPolicy.ValidateUpdate(ctx, oldPolicy, newPolicy)
		if err != nil {
			t.Fatalf("expected valid update to pass, got: %v", err)
		}
		if warnings != nil {
			t.Errorf("expected no warnings, got: %v", warnings)
		}
	})

	t.Run("rejects invalid update", func(t *testing.T) {
		oldPolicy := newValidPolicy()
		newPolicy := newValidPolicy()
		newPolicy.Spec.VaultRef.KeyVaultURI = "invalid-uri"

		_, err := newPolicy.ValidateUpdate(ctx, oldPolicy, newPolicy)
		if err == nil {
			t.Fatalf("expected invalid update to be rejected")
		}
	})
}
