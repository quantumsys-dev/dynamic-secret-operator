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

package azure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewAzureCredential(t *testing.T) {
	// Helper to clean environment before each test
	cleanEnv := func() {
		os.Unsetenv(EnvAzureFederatedTokenFile)
		os.Unsetenv(EnvAzureClientID)
		os.Unsetenv(EnvAzureTenantID)
	}

	t.Run("fails fast when AZURE_FEDERATED_TOKEN_FILE is missing", func(t *testing.T) {
		cleanEnv()
		cred, err := NewAzureCredential()
		if err == nil {
			t.Fatalf("expected error when %s is missing, got nil", EnvAzureFederatedTokenFile)
		}
		if cred != nil {
			t.Fatalf("expected nil credential, got %v", cred)
		}
		if !strings.Contains(err.Error(), EnvAzureFederatedTokenFile) {
			t.Errorf("expected error message to mention %s, got: %v", EnvAzureFederatedTokenFile, err)
		}
	})

	t.Run("fails when AZURE_CLIENT_ID is missing", func(t *testing.T) {
		cleanEnv()
		tmpDir := t.TempDir()
		tokenPath := filepath.Join(tmpDir, "token")
		if err := os.WriteFile(tokenPath, []byte("dummy-token"), 0600); err != nil {
			t.Fatalf("failed to create temp token: %v", err)
		}

		os.Setenv(EnvAzureFederatedTokenFile, tokenPath)
		defer cleanEnv()

		cred, err := NewAzureCredential()
		if err == nil {
			t.Fatalf("expected error when %s is missing, got nil", EnvAzureClientID)
		}
		if cred != nil {
			t.Fatalf("expected nil credential, got %v", cred)
		}
		if !strings.Contains(err.Error(), EnvAzureClientID) {
			t.Errorf("expected error message to mention %s, got: %v", EnvAzureClientID, err)
		}
	})

	t.Run("fails when AZURE_TENANT_ID is missing", func(t *testing.T) {
		cleanEnv()
		tmpDir := t.TempDir()
		tokenPath := filepath.Join(tmpDir, "token")
		if err := os.WriteFile(tokenPath, []byte("dummy-token"), 0600); err != nil {
			t.Fatalf("failed to create temp token: %v", err)
		}

		os.Setenv(EnvAzureFederatedTokenFile, tokenPath)
		os.Setenv(EnvAzureClientID, "00000000-0000-0000-0000-000000000001")
		defer cleanEnv()

		cred, err := NewAzureCredential()
		if err == nil {
			t.Fatalf("expected error when %s is missing, got nil", EnvAzureTenantID)
		}
		if cred != nil {
			t.Fatalf("expected nil credential, got %v", cred)
		}
		if !strings.Contains(err.Error(), EnvAzureTenantID) {
			t.Errorf("expected error message to mention %s, got: %v", EnvAzureTenantID, err)
		}
	})

	t.Run("fails when token file does not exist on disk", func(t *testing.T) {
		cleanEnv()
		os.Setenv(EnvAzureFederatedTokenFile, "/non/existent/path/token")
		os.Setenv(EnvAzureClientID, "00000000-0000-0000-0000-000000000001")
		os.Setenv(EnvAzureTenantID, "00000000-0000-0000-0000-000000000002")
		defer cleanEnv()

		cred, err := NewAzureCredential()
		if err == nil {
			t.Fatalf("expected error for non-existent token file, got nil")
		}
		if cred != nil {
			t.Fatalf("expected nil credential, got %v", cred)
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("expected error to state file does not exist, got: %v", err)
		}
	})

	t.Run("successfully creates WorkloadIdentityCredential with valid environment", func(t *testing.T) {
		cleanEnv()
		tmpDir := t.TempDir()
		tokenPath := filepath.Join(tmpDir, "token")
		if err := os.WriteFile(tokenPath, []byte("mock-jwt-federated-token"), 0600); err != nil {
			t.Fatalf("failed to create temp token file: %v", err)
		}

		os.Setenv(EnvAzureFederatedTokenFile, tokenPath)
		os.Setenv(EnvAzureClientID, "11111111-2222-3333-4444-555555555555")
		os.Setenv(EnvAzureTenantID, "66666666-7777-8888-9999-000000000000")
		defer cleanEnv()

		cred, err := NewAzureCredential()
		if err != nil {
			t.Fatalf("expected successful credential initialization, got error: %v", err)
		}
		if cred == nil {
			t.Fatalf("expected non-nil TokenCredential")
		}
	})
}
