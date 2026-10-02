package events

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseAWSRotationEvent(t *testing.T) {
	tests := []struct {
		name          string
		body          []byte
		expectedID    string
		expectErr     bool
		errorContains string
	}{
		{
			name: "Valid rotation event",
			body: []byte(`{
				"source": "aws.secretsmanager",
				"detail-type": "AWS Secrets Manager Secret Rotation Succeeded",
				"detail": {
					"SecretId": "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-db-secret"
				}
			}`),
			expectedID: "arn:aws:secretsmanager:us-east-1:123456789012:secret:my-db-secret",
			expectErr:  false,
		},
		{
			name: "Irrelevant source",
			body: []byte(`{
				"source": "aws.ec2",
				"detail-type": "AWS Secrets Manager Secret Rotation Succeeded",
				"detail": {
					"SecretId": "my-db-secret"
				}
			}`),
			expectErr:     true,
			errorContains: "irrelevant event source",
		},
		{
			name: "Irrelevant detail-type",
			body: []byte(`{
				"source": "aws.secretsmanager",
				"detail-type": "Unsupported Event Type",
				"detail": {
					"SecretId": "my-db-secret"
				}
			}`),
			expectErr:     true,
			errorContains: "irrelevant detail-type",
		},
		{
			name: "CloudTrail PutSecretValue",
			body: []byte(`{
				"source": "aws.secretsmanager",
				"detail-type": "AWS API Call via CloudTrail",
				"detail": {
					"requestParameters": {
						"secretId": "arn:aws:secretsmanager:us-east-1:123456789012:secret:manual-db-secret"
					}
				}
			}`),
			expectedID: "arn:aws:secretsmanager:us-east-1:123456789012:secret:manual-db-secret",
			expectErr:  false,
		},
		{
			name: "Missing SecretId",
			body: []byte(`{
				"source": "aws.secretsmanager",
				"detail-type": "AWS Secrets Manager Secret Rotation Succeeded",
				"detail": {}
			}`),
			expectErr:     true,
			errorContains: "missing SecretId",
		},
		{
			name:          "Malformed JSON",
			body:          []byte(`{bad-json`),
			expectErr:     true,
			errorContains: "failed to unmarshal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := ParseAWSRotationEvent(tt.body)
			if tt.expectErr {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorContains)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedID, id)
			}
		})
	}
}

func FuzzParseAWSRotationEvent(f *testing.F) {
	// Seed with valid and invalid JSONs
	f.Add([]byte(`{"source": "aws.secretsmanager", "detail-type": "AWS Secrets Manager Secret Rotation Succeeded", "detail": {"SecretId": "test"}}`))
	f.Add([]byte(`{"Type": "Notification", "Message": "{\"source\": \"aws.secretsmanager\"}"}`))
	f.Add([]byte(`{"bad-json"`))
	f.Add([]byte(`{}`))
	
	f.Fuzz(func(t *testing.T, data []byte) {
		// Fuzzing should not cause any panic.
		ParseAWSRotationEvent(data)
	})
}
