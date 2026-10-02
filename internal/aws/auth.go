package aws

import (
	"context"
	"errors"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
)

var (
	ErrStaticCredentialsDetected = errors.New("Zero-Trust policy violation: Static AWS credentials detected. DSO requires IAM Roles for Service Accounts (IRSA) or EKS Pod Identity")
)

// NewAWSConfig creates a new AWS configuration, strictly enforcing a zero-trust policy.
// It fails fast if static credentials are found in the environment.
func NewAWSConfig(ctx context.Context, region string) (aws.Config, error) {
	// Zero-Trust Enforcement Check
	if hasStaticCredentials() {
		return aws.Config{}, ErrStaticCredentialsDetected
	}

	var optFns []func(*config.LoadOptions) error
	if region != "" {
		optFns = append(optFns, config.WithRegion(region))
	}

	return config.LoadDefaultConfig(ctx, optFns...)
}

func hasStaticCredentials() bool {
	if os.Getenv("DSO_ALLOW_STATIC_AWS_CREDS") == "true" || os.Getenv("E2E_SYNTHETIC_MODE") == "true" {
		return false // Acknowledge risk, allow for LocalStack E2E integration testing
	}
	return os.Getenv("AWS_ACCESS_KEY_ID") != "" || os.Getenv("AWS_SECRET_ACCESS_KEY") != ""
}
