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
	"errors"
	"fmt"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

const (
	// EnvAzureFederatedTokenFile is the projected ServiceAccount token file path injected by Azure Workload Identity.
	EnvAzureFederatedTokenFile = "AZURE_FEDERATED_TOKEN_FILE"
	// EnvAzureClientID is the Azure AD application / managed identity client ID.
	EnvAzureClientID = "AZURE_CLIENT_ID"
	// EnvAzureTenantID is the Azure AD directory tenant ID.
	EnvAzureTenantID = "AZURE_TENANT_ID"
)

// NewAzureCredential initializes a zero-trust, passwordless Azure TokenCredential strictly
// backed by Azure Workload Identity (federated tokens).
// Static credentials (client secret, passwords, long-lived API keys) are strictly prohibited.
func NewAzureCredential() (azcore.TokenCredential, error) {
	tokenFile := os.Getenv(EnvAzureFederatedTokenFile)
	if tokenFile == "" {
		return nil, fmt.Errorf(
			"required environment variable %s is not set: Azure Workload Identity is mandatory for zero-trust passwordless authentication",
			EnvAzureFederatedTokenFile,
		)
	}

	clientID := os.Getenv(EnvAzureClientID)
	if clientID == "" {
		return nil, fmt.Errorf(
			"required environment variable %s is not set: managed identity client ID is required",
			EnvAzureClientID,
		)
	}

	tenantID := os.Getenv(EnvAzureTenantID)
	if tenantID == "" {
		return nil, fmt.Errorf(
			"required environment variable %s is not set: directory tenant ID is required",
			EnvAzureTenantID,
		)
	}

	// Verify that the projected federated token file exists and is accessible
	if _, err := os.Stat(tokenFile); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("federated token file %q specified in %s does not exist", tokenFile, EnvAzureFederatedTokenFile)
		}
		return nil, fmt.Errorf("failed to access federated token file %q: %w", tokenFile, err)
	}

	cred, err := azidentity.NewWorkloadIdentityCredential(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create Azure Workload Identity credential: %w", err)
	}

	return cred, nil
}
