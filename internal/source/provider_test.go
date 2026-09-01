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
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/azure"
)

type mockSecretFetcher struct {
	value   []byte
	version string
	err     error
}

func (m *mockSecretFetcher) GetSecret(ctx context.Context, vaultURI, secretName, version string) (*azure.SecretPayload, error) {
	if m.err != nil {
		return nil, m.err
	}
	return &azure.SecretPayload{
		Value:   m.value,
		Version: m.version,
		ID:      fmt.Sprintf("%s/secrets/%s/%s", vaultURI, secretName, m.version),
	}, nil
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	mockP := &StubProvider{Name: "Custom", Milestone: "v1.0"}
	reg.Register("Custom", mockP)

	p, err := reg.Get("Custom")
	if err != nil || p == nil {
		t.Fatalf("expected registered provider, got err: %v", err)
	}

	_, err = reg.Get("NonExistent")
	if err == nil {
		t.Fatalf("expected error for unregistered provider")
	}
}

func TestAzureKeyVaultProvider(t *testing.T) {
	fetcher := &mockSecretFetcher{
		value:   []byte("secret-payload-akv"),
		version: "v123",
	}
	provider := &AzureKeyVaultProvider{Fetcher: fetcher}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			Source: &secretv1alpha1.SecretSource{
				Type: secretv1alpha1.SourceTypeAzureKeyVault,
				AzureKeyVault: &secretv1alpha1.AzureKeyVaultSource{
					KeyVaultURI: "https://mykv.vault.azure.net",
					ObjectName:  "db-pass",
				},
			},
		},
	}

	payload, err := provider.FetchSecret(context.Background(), policy)
	if err != nil {
		t.Fatalf("unexpected error fetching secret: %v", err)
	}

	if string(payload.Data["db-pass"]) != "secret-payload-akv" {
		t.Errorf("expected secret-payload-akv, got %s", string(payload.Data["db-pass"]))
	}
	if payload.Version != "v123" {
		t.Errorf("expected version v123, got %s", payload.Version)
	}
}

func TestK8sSecretProvider(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = secretv1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "synced-eso-secret",
			Namespace:       "production",
			ResourceVersion: "45678",
		},
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("secure-password"),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := &K8sSecretProvider{Reader: c}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "eso-policy",
			Namespace: "production",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			Source: &secretv1alpha1.SecretSource{
				Type: secretv1alpha1.SourceTypeK8sSecret,
				K8sSecret: &secretv1alpha1.K8sSecretSource{
					Name: "synced-eso-secret",
				},
			},
		},
	}

	payload, err := provider.FetchSecret(context.Background(), policy)
	if err != nil {
		t.Fatalf("unexpected error fetching k8s secret: %v", err)
	}

	if string(payload.Data["password"]) != "secure-password" {
		t.Errorf("expected password 'secure-password', got %s", string(payload.Data["password"]))
	}
	if string(payload.Data["username"]) != "admin" {
		t.Errorf("expected username 'admin', got %s", string(payload.Data["username"]))
	}
}

func TestStubProviders(t *testing.T) {
	reg := SetupDefaultRegistry(nil, nil)

	for _, st := range []secretv1alpha1.SourceType{
		secretv1alpha1.SourceTypeAWSSecretsManager,
		secretv1alpha1.SourceTypeGCPSecretManager,
		secretv1alpha1.SourceTypeVault,
	} {
		p, err := reg.Get(st)
		if err != nil {
			t.Fatalf("expected registered stub provider for %s", st)
		}
		_, fetchErr := p.FetchSecret(context.Background(), &secretv1alpha1.DynamicSecretPolicy{})
		if fetchErr == nil {
			t.Errorf("expected roadmap error from stub provider %s", st)
		}
	}
}

func TestParseJSONPayload(t *testing.T) {
	t.Run("valid JSON map unmarshals to discrete byte slices", func(t *testing.T) {
		raw := []byte(`{"username":"foo","password":"bar","port":5432,"ssl":true}`)
		parsed, err := ParseJSONPayload(raw)
		if err != nil {
			t.Fatalf("unexpected error parsing JSON: %v", err)
		}
		if string(parsed["username"]) != "foo" {
			t.Errorf("expected username 'foo', got %s", string(parsed["username"]))
		}
		if string(parsed["password"]) != "bar" {
			t.Errorf("expected password 'bar', got %s", string(parsed["password"]))
		}
		if string(parsed["port"]) != "5432" {
			t.Errorf("expected port '5432', got %s", string(parsed["port"]))
		}
		if string(parsed["ssl"]) != "true" {
			t.Errorf("expected ssl 'true', got %s", string(parsed["ssl"]))
		}
	})

	t.Run("invalid JSON returns error", func(t *testing.T) {
		raw := []byte(`not-json-data`)
		_, err := ParseJSONPayload(raw)
		if err == nil {
			t.Errorf("expected error parsing invalid JSON, got nil")
		}
	})
}

func TestAzureKeyVaultProvider_ParseJSON(t *testing.T) {
	fetcher := &mockSecretFetcher{
		value:   []byte(`{"db_user":"appuser","db_pass":"secretpass"}`),
		version: "v999",
	}
	provider := &AzureKeyVaultProvider{Fetcher: fetcher}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "json-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			Source: &secretv1alpha1.SecretSource{
				Type:      secretv1alpha1.SourceTypeAzureKeyVault,
				ParseJSON: true,
				AzureKeyVault: &secretv1alpha1.AzureKeyVaultSource{
					KeyVaultURI: "https://mykv.vault.azure.net",
					ObjectName:  "db-credentials-json",
				},
			},
		},
	}

	payload, err := provider.FetchSecret(context.Background(), policy)
	if err != nil {
		t.Fatalf("unexpected error fetching JSON secret: %v", err)
	}

	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 parsed keys, got %d", len(payload.Data))
	}
	if string(payload.Data["db_user"]) != "appuser" {
		t.Errorf("expected db_user 'appuser', got %s", string(payload.Data["db_user"]))
	}
	if string(payload.Data["db_pass"]) != "secretpass" {
		t.Errorf("expected db_pass 'secretpass', got %s", string(payload.Data["db_pass"]))
	}
}

func TestK8sSecretProvider_ParseJSON(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = secretv1alpha1.AddToScheme(scheme)

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "json-secret",
			Namespace:       "default",
			ResourceVersion: "12345",
		},
		Data: map[string][]byte{
			"json-payload": []byte(`{"apiKey":"xyz-123","apiSecret":"supersecret"}`),
		},
	}

	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(sec).Build()
	provider := &K8sSecretProvider{Reader: c}

	policy := &secretv1alpha1.DynamicSecretPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "json-k8s-policy",
			Namespace: "default",
		},
		Spec: secretv1alpha1.DynamicSecretPolicySpec{
			Source: &secretv1alpha1.SecretSource{
				Type:      secretv1alpha1.SourceTypeK8sSecret,
				ParseJSON: true,
				K8sSecret: &secretv1alpha1.K8sSecretSource{
					Name: "json-secret",
				},
			},
		},
	}

	payload, err := provider.FetchSecret(context.Background(), policy)
	if err != nil {
		t.Fatalf("unexpected error fetching JSON k8s secret: %v", err)
	}

	if len(payload.Data) != 2 {
		t.Fatalf("expected 2 parsed keys, got %d", len(payload.Data))
	}
	if string(payload.Data["apiKey"]) != "xyz-123" {
		t.Errorf("expected apiKey 'xyz-123', got %s", string(payload.Data["apiKey"]))
	}
	if string(payload.Data["apiSecret"]) != "supersecret" {
		t.Errorf("expected apiSecret 'supersecret', got %s", string(payload.Data["apiSecret"]))
	}
}
