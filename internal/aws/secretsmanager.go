package aws

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"

	secretv1alpha1 "github.com/quantumsys-dev/dynamic-secret-operator/api/v1alpha1"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/source"
	"github.com/quantumsys-dev/dynamic-secret-operator/internal/telemetry"
)

type GetSecretValueAPI interface {
	GetSecretValue(ctx context.Context, params *secretsmanager.GetSecretValueInput, optFns ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error)
}

type AWSSecretsManagerProvider struct {
	baseConfig aws.Config
	client     GetSecretValueAPI
	clients    map[string]GetSecretValueAPI
	mu         sync.RWMutex
}

func NewAWSSecretsManagerProvider(cfg aws.Config) *AWSSecretsManagerProvider {
	return &AWSSecretsManagerProvider{
		baseConfig: cfg,
		clients:    make(map[string]GetSecretValueAPI),
	}
}

// FetchSecret implements the source.Provider interface for AWS Secrets Manager.
func (p *AWSSecretsManagerProvider) FetchSecret(ctx context.Context, policy *secretv1alpha1.DynamicSecretPolicy) (*source.SecretPayload, error) {
	tracer := otel.Tracer("aws-secretsmanager-provider")
	ctx, span := tracer.Start(ctx, "ExecuteAWSFetch")
	defer span.End()

	start := time.Now()
	status := "success"
	region := "default"

	defer func() {
		telemetry.AWSFetchLatency.WithLabelValues(region, status).Observe(time.Since(start).Seconds())
	}()

	if policy.Spec.Source == nil || policy.Spec.Source.AWSSecretsManager == nil {
		status = "error"
		err := fmt.Errorf("AWSSecretsManager source configuration is missing")
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	src := policy.Spec.Source.AWSSecretsManager
	if src.Region != "" {
		region = src.Region
	}

	cfg := p.baseConfig
	if region != "default" {
		cfg.Region = region
	}

	var client GetSecretValueAPI
	if p.client != nil {
		client = p.client
	} else {
		cacheKey := fmt.Sprintf("%s-%s", region, src.RoleARN)
		
		p.mu.RLock()
		cachedClient, exists := p.clients[cacheKey]
		p.mu.RUnlock()

		if exists {
			client = cachedClient
		} else {
			p.mu.Lock()
			// Double check
			cachedClient, exists = p.clients[cacheKey]
			if exists {
				client = cachedClient
			} else {
				// Prevent unbounded memory growth
				if len(p.clients) >= 1000 {
					// Pseudo-random eviction
					for k := range p.clients {
						delete(p.clients, k)
						break
					}
				}
				// Assume Role if configured
				if src.RoleARN != "" {
					stsClient := sts.NewFromConfig(cfg)
					provider := stscreds.NewAssumeRoleProvider(stsClient, src.RoleARN)
					cfg.Credentials = aws.NewCredentialsCache(provider)
				}
				client = secretsmanager.NewFromConfig(cfg)
				p.clients[cacheKey] = client
			}
			p.mu.Unlock()
		}
	}

	out, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(src.SecretID),
	})
	if err != nil {
		status = "error"
		err = fmt.Errorf("failed to get secret value for %q: %w", src.SecretID, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	var rawBytes []byte
	if out.SecretBinary != nil {
		rawBytes = out.SecretBinary
	} else if out.SecretString != nil {
		rawBytes = []byte(*out.SecretString)
	} else {
		status = "error"
		err = fmt.Errorf("secret %q is empty", src.SecretID)
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, err
	}

	// Parse JSON if requested
	if policy.Spec.Source.ParseJSON {
		parsedData, err := source.ParseJSONPayload(rawBytes)
		if err != nil {
			return nil, err
		}
		return &source.SecretPayload{
			Data:    parsedData,
			Version: aws.ToString(out.VersionId),
		}, nil
	}

	// Return raw mapped to the object's name
	return &source.SecretPayload{
		Data: map[string][]byte{
			policy.GetVaultObjectName(): rawBytes,
		},
		Version: aws.ToString(out.VersionId),
	}, nil
}
