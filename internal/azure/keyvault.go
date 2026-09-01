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
	"errors"
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azsecrets"
)

// SecretPayload represents the retrieved secret and its cryptographic version metadata.
type SecretPayload struct {
	Value   []byte
	Version string
	ID      string
}

// SecretFetcher defines the contract for retrieving secrets from external vault backends.
type SecretFetcher interface {
	GetSecret(ctx context.Context, vaultURI, secretName, version string) (*SecretPayload, error)
}

// AzureKeyVaultFetcher retrieves secrets from Azure Key Vault using Azure Workload Identity.
type AzureKeyVaultFetcher struct {
	cred azcore.TokenCredential
}

// NewKeyVaultFetcher creates a new SecretFetcher backed by Azure Key Vault.
func NewKeyVaultFetcher(cred azcore.TokenCredential) (*AzureKeyVaultFetcher, error) {
	if cred == nil {
		return nil, errors.New("token credential must not be nil")
	}
	return &AzureKeyVaultFetcher{
		cred: cred,
	}, nil
}

// GetSecret fetches a secret by name and optional version from the specified Azure Key Vault URI.
func (f *AzureKeyVaultFetcher) GetSecret(ctx context.Context, vaultURI, secretName, version string) (*SecretPayload, error) {
	if vaultURI == "" {
		return nil, errors.New("vaultURI must not be empty")
	}
	if secretName == "" {
		return nil, errors.New("secretName must not be empty")
	}

	client, err := azsecrets.NewClient(vaultURI, f.cred, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create azsecrets client for %q: %w", vaultURI, err)
	}

	resp, err := client.GetSecret(ctx, secretName, version, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve secret %q from vault %q: %w", secretName, vaultURI, err)
	}

	if resp.Value == nil {
		return nil, fmt.Errorf("retrieved secret %q has nil value", secretName)
	}

	var secretVersion string
	var secretID string
	if resp.ID != nil {
		secretID = string(*resp.ID)
		// Extract version from ID URL if present (https://<vault>.vault.azure.net/secrets/<name>/<version>)
		parts := strings.Split(secretID, "/")
		if len(parts) > 0 {
			secretVersion = parts[len(parts)-1]
		}
	}

	return &SecretPayload{
		Value:   []byte(*resp.Value),
		Version: secretVersion,
		ID:      secretID,
	}, nil
}
