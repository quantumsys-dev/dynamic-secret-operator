package events

import (
	"encoding/json"
	"fmt"
)

type EventBridgePayload struct {
	Source     string `json:"source"`
	DetailType string `json:"detail-type"`
	Detail     struct {
		SecretId          string `json:"SecretId"` // From native rotation event
		RequestParameters struct {
			SecretId string `json:"secretId"` // From CloudTrail PutSecretValue
		} `json:"requestParameters"`
	} `json:"detail"`
}

type SNSEnvelope struct {
	Type    string `json:"Type"`
	Message string `json:"Message"`
}

// ParseAWSRotationEvent extracts the SecretId from an EventBridge rotation event.
func ParseAWSRotationEvent(body []byte) (string, error) {
	// Try to unwrap SNS envelope
	var snsPayload SNSEnvelope
	if err := json.Unmarshal(body, &snsPayload); err == nil && snsPayload.Type == "Notification" && snsPayload.Message != "" {
		body = []byte(snsPayload.Message)
	}

	var payload EventBridgePayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", fmt.Errorf("failed to unmarshal EventBridge payload: %w", err)
	}

	if payload.Source != "aws.secretsmanager" {
		return "", fmt.Errorf("irrelevant event source: %s", payload.Source)
	}

	var secretId string
	switch payload.DetailType {
	case "AWS Secrets Manager Secret Rotation Succeeded":
		secretId = payload.Detail.SecretId
	case "AWS API Call via CloudTrail":
		secretId = payload.Detail.RequestParameters.SecretId
	default:
		return "", fmt.Errorf("irrelevant detail-type: %s", payload.DetailType)
	}

	if secretId == "" {
		return "", fmt.Errorf("missing SecretId in event detail")
	}

	return secretId, nil
}
