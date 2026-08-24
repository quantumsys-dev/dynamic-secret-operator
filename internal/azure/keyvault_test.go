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
	"context"
	"testing"
)

func TestZeroBytes(t *testing.T) {
	data := []byte("super-sensitive-secret-payload-12345")
	ZeroBytes(data)

	for i, b := range data {
		if b != 0 {
			t.Errorf("byte at index %d was not zeroed, got: %d", i, b)
		}
	}
}

func TestSecretPayload_Wipe(t *testing.T) {
	payload := &SecretPayload{
		Value:   []byte("password123"),
		Version: "v1",
		ID:      "https://my-vault.vault.azure.net/secrets/db/v1",
	}

	payload.Wipe()

	if payload.Value != nil {
		t.Errorf("expected payload.Value to be nil after Wipe, got %v", payload.Value)
	}
}

func TestNewKeyVaultFetcher(t *testing.T) {
	t.Run("fails when cred is nil", func(t *testing.T) {
		_, err := NewKeyVaultFetcher(nil)
		if err == nil {
			t.Fatalf("expected error for nil credential, got nil")
		}
	})

	t.Run("succeeds with valid credential", func(t *testing.T) {
		cred := &mockTokenCredential{}
		fetcher, err := NewKeyVaultFetcher(cred)
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if fetcher == nil {
			t.Fatalf("expected non-nil fetcher")
		}
	})

	t.Run("GetSecret validates input arguments", func(t *testing.T) {
		cred := &mockTokenCredential{}
		fetcher, _ := NewKeyVaultFetcher(cred)

		_, err := fetcher.GetSecret(context.Background(), "", "secretName", "")
		if err == nil {
			t.Errorf("expected error for empty vaultURI")
		}

		_, err = fetcher.GetSecret(context.Background(), "https://vault.vault.azure.net", "", "")
		if err == nil {
			t.Errorf("expected error for empty secretName")
		}
	})
}
