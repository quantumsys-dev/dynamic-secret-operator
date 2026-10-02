package aws

import (
	"context"
	"errors"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
)

type mockSecretsManagerClient struct {
	GetSecretValueFunc func(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

func (m *mockSecretsManagerClient) GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	if m.GetSecretValueFunc != nil {
		return m.GetSecretValueFunc(ctx, params, optFns...)
	}
	return nil, errors.New("unexpected call")
}

func TestAWSSecretsManagerProvider_FetchSecret(t *testing.T) {
	tests := []struct {
		name          string
		policy        *secretv1alpha1.DynamicSecretPolicy
		mockResp      *secretsmanager.GetSecretValueOutput
		mockErr       error
		expectedErr   bool
		expectedData  map[string][]byte
		errorContains string
	}{
		{
			name: "Success plaintext SecretString",
			policy: &secretv1alpha1.DynamicSecretPolicy{
				Spec: secretv1alpha1.DynamicSecretPolicySpec{
					Source: &secretv1alpha1.SecretSource{
						Type: secretv1alpha1.SourceTypeAWSSecretsManager,
						AWSSecretsManager: &secretv1alpha1.AWSSecretsManagerSource{
							SecretID: "my-secret",
						},
						ParseJSON: false,
					},
				},
			},
			mockResp: &secretsmanager.GetSecretValueOutput{
				SecretString: aws.String("super-secret-value"),
			},
			expectedErr: false,
			expectedData: map[string][]byte{
				"secret": []byte("super-secret-value"),
			},
		},
		{
			name: "Success ParseJSON SecretString",
			policy: &secretv1alpha1.DynamicSecretPolicy{
				Spec: secretv1alpha1.DynamicSecretPolicySpec{
					Source: &secretv1alpha1.SecretSource{
						Type: secretv1alpha1.SourceTypeAWSSecretsManager,
						AWSSecretsManager: &secretv1alpha1.AWSSecretsManagerSource{
							SecretID: "my-secret",
						},
						ParseJSON: true,
					},
				},
			},
			mockResp: &secretsmanager.GetSecretValueOutput{
				SecretString: aws.String(`{"username":"admin","password":"foo"}`),
			},
			expectedErr: false,
			expectedData: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("foo"),
			},
		},
		{
			name: "API Error AccessDenied",
			policy: &secretv1alpha1.DynamicSecretPolicy{
				Spec: secretv1alpha1.DynamicSecretPolicySpec{
					Source: &secretv1alpha1.SecretSource{
						Type: secretv1alpha1.SourceTypeAWSSecretsManager,
						AWSSecretsManager: &secretv1alpha1.AWSSecretsManagerSource{
							SecretID: "my-secret",
						},
					},
				},
			},
			mockErr:       errors.New("AccessDeniedException"),
			expectedErr:   true,
			errorContains: "AccessDeniedException",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockClient := &mockSecretsManagerClient{
				GetSecretValueFunc: func(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
					return tt.mockResp, tt.mockErr
				},
			}

			provider := &AWSSecretsManagerProvider{
				client: mockClient,
			}

			payload, err := provider.FetchSecret(context.Background(), tt.policy)
			if tt.expectedErr {
				assert.Error(t, err)
				if tt.errorContains != "" {
					assert.Contains(t, err.Error(), tt.errorContains)
				}
			} else {
				require.NoError(t, err)
				assert.NotNil(t, payload)
				assert.Equal(t, tt.expectedData, payload.Data)
			}
		})
	}
}
