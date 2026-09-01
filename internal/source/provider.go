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

package source

import (
	"context"
	"fmt"
	"sync"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/azure"
)

// SecretPayload holds the raw data bytes and optional version/metadata fetched from a provider.
type SecretPayload struct {
	Data    map[string][]byte
	Version string
}

// Provider defines the extensible abstraction interface for secret ingestion sources.
type Provider interface {
	// FetchSecret extracts the secret payload for a given policy from the source backend.
	FetchSecret(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (*SecretPayload, error)
}

// Registry manages registered secret source providers dynamically.
type Registry struct {
	mu        sync.RWMutex
	providers map[secretv1alpha1.SourceType]Provider
}

// NewRegistry creates a new Provider Registry.
func NewRegistry() *Registry {
	return &Registry{
		providers: make(map[secretv1alpha1.SourceType]Provider),
	}
}

// Register registers a provider implementation for a source type.
func (r *Registry) Register(sourceType secretv1alpha1.SourceType, p Provider) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.providers[sourceType] = p
}

// Get retrieves the provider implementation for the given source type.
func (r *Registry) Get(sourceType secretv1alpha1.SourceType) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[sourceType]
	if !ok {
		return nil, fmt.Errorf("no provider registered for source type %q", sourceType)
	}
	return p, nil
}

// AzureKeyVaultProvider implements Provider for Azure Key Vault.
type AzureKeyVaultProvider struct {
	Fetcher azure.SecretFetcher
}

// FetchSecret fetches secrets from Azure Key Vault via the configured SecretFetcher.
func (p *AzureKeyVaultProvider) FetchSecret(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (*SecretPayload, error) {
	if p.Fetcher == nil {
		return nil, fmt.Errorf("azure secret fetcher is not initialized")
	}

	src := policy.Spec.GetResolvedSource()
	if src.AzureKeyVault == nil {
		return nil, fmt.Errorf("azureKeyVault configuration is missing on policy %s", policy.Name)
	}

	vaultURI := src.AzureKeyVault.KeyVaultURI
	objName := src.AzureKeyVault.ObjectName

	payload, err := p.Fetcher.GetSecret(ctx, vaultURI, objName, "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch secret from Azure Key Vault %q (object %q): %w", vaultURI, objName, err)
	}

	return &SecretPayload{
		Data: map[string][]byte{
			objName: payload.Value,
		},
		Version: payload.Version,
	}, nil
}

// K8sSecretProvider implements Provider for intermediate Kubernetes secrets (e.g. ESO sync target).
type K8sSecretProvider struct {
	Reader client.Reader
}

// FetchSecret retrieves the payload directly from an intermediate Kubernetes Secret in the policy namespace.
func (p *K8sSecretProvider) FetchSecret(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (*SecretPayload, error) {
	if p.Reader == nil {
		return nil, fmt.Errorf("k8s reader is not initialized on K8sSecretProvider")
	}

	src := policy.Spec.GetResolvedSource()
	if src.K8sSecret == nil {
		return nil, fmt.Errorf("k8sSecret configuration is missing on policy %s", policy.Name)
	}

	secretName := src.K8sSecret.Name
	sec := &corev1.Secret{}
	if err := p.Reader.Get(ctx, types.NamespacedName{Name: secretName, Namespace: policy.Namespace}, sec); err != nil {
		return nil, fmt.Errorf("failed to fetch intermediate Kubernetes source secret %q in namespace %q: %w", secretName, policy.Namespace, err)
	}

	// Copy secret data
	dataCopy := make(map[string][]byte, len(sec.Data))
	for k, v := range sec.Data {
		vCopy := make([]byte, len(v))
		copy(vCopy, v)
		dataCopy[k] = vCopy
	}

	return &SecretPayload{
		Data:    dataCopy,
		Version: sec.ResourceVersion,
	}, nil
}

// StubProvider provides descriptive placeholder errors for upcoming native cloud providers.
type StubProvider struct {
	Name      string
	Milestone string
}

// FetchSecret returns a roadmap stub error.
func (p *StubProvider) FetchSecret(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (*SecretPayload, error) {
	return nil, fmt.Errorf("native provider %q is currently in roadmap (%s); please use type %q with External Secrets Operator (ESO) for multi-cloud vault ingestion",
		p.Name, p.Milestone, secretv1alpha1.SourceTypeK8sSecret)
}

// SetupDefaultRegistry constructs a fully wired registry with standard and stub providers.
func SetupDefaultRegistry(k8sReader client.Reader, fetcher azure.SecretFetcher) *Registry {
	reg := NewRegistry()
	if fetcher != nil {
		reg.Register(secretv1alpha1.SourceTypeAzureKeyVault, &AzureKeyVaultProvider{Fetcher: fetcher})
	}
	if k8sReader != nil {
		reg.Register(secretv1alpha1.SourceTypeK8sSecret, &K8sSecretProvider{Reader: k8sReader})
	}
	reg.Register(secretv1alpha1.SourceTypeAWSSecretsManager, &StubProvider{Name: "AWSSecretsManager", Milestone: "v0.3.0"})
	reg.Register(secretv1alpha1.SourceTypeGCPSecretManager, &StubProvider{Name: "GCPSecretManager", Milestone: "v0.3.0"})
	reg.Register(secretv1alpha1.SourceTypeVault, &StubProvider{Name: "Vault", Milestone: "v0.3.0"})
	return reg
}
