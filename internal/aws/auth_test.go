package aws

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewAWSConfig_ZeroTrustPolicy(t *testing.T) {
	// Helper to clean and restore env vars for test isolation
	cleanEnv := func() {
		os.Unsetenv("AWS_ACCESS_KEY_ID")
		os.Unsetenv("AWS_SECRET_ACCESS_KEY")
		os.Unsetenv("AWS_REGION")
	}

	tests := []struct {
		name          string
		setupEnv      func()
		region        string
		expectErr     bool
		expectedError error
		expectRegion  string
	}{
		{
			name: "Fails when AWS_ACCESS_KEY_ID is set",
			setupEnv: func() {
				cleanEnv()
				os.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")
			},
			region:        "us-east-1",
			expectErr:     true,
			expectedError: ErrStaticCredentialsDetected,
		},
		{
			name: "Fails when AWS_SECRET_ACCESS_KEY is set",
			setupEnv: func() {
				cleanEnv()
				os.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")
			},
			region:        "us-west-2",
			expectErr:     true,
			expectedError: ErrStaticCredentialsDetected,
		},
		{
			name: "Succeeds with no static credentials",
			setupEnv: func() {
				cleanEnv()
				// Simulate IRSA or Pod Identity by not having static keys
			},
			region:       "eu-central-1",
			expectErr:    false,
			expectRegion: "eu-central-1",
		},
		{
			name: "Succeeds and applies default region if not passed but in env",
			setupEnv: func() {
				cleanEnv()
				os.Setenv("AWS_REGION", "ap-southeast-2")
			},
			region:       "",
			expectErr:    false,
			expectRegion: "ap-southeast-2",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setupEnv()
			// Ensure cleanup after test
			t.Cleanup(cleanEnv)

			cfg, err := NewAWSConfig(context.Background(), tt.region)

			if tt.expectErr {
				assert.Error(t, err)
				if tt.expectedError != nil {
					assert.Equal(t, tt.expectedError, err)
				}
			} else {
				require.NoError(t, err)
				if tt.expectRegion != "" {
					assert.Equal(t, tt.expectRegion, cfg.Region)
				}
			}
		})
	}
}
