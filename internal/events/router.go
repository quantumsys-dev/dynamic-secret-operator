// Copyright 2026 QuantumSys. Licensed under the Apache License, Version 2.0.
// See the full license text at http://www.apache.org/licenses/LICENSE-2.0

package events

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EventPayload contains the extracted metadata from an inbound rotation notification event.
type EventPayload struct {
	// ObjectName is the upstream secret or key vault object name (e.g., "db-password").
	ObjectName string
	// PolicyName is an optional specific policy targeted by the notification.
	PolicyName string
	// Namespace is an optional Kubernetes namespace scope specified in the event.
	Namespace string
}

// ParseRotationEventPayload unmarshals and extracts rotation metadata from an inbound
// event payload (such as Azure EventGrid over Service Bus, AWS EventBridge, etc.).
// It extracts the secret name from event data, falling back to parsing the subject path.
func ParseRotationEventPayload(body []byte) (EventPayload, error) {
	var eventData struct {
		Subject   string `json:"subject"`
		EventType string `json:"eventType"`
		Data      struct {
			ObjectName string `json:"ObjectName"`
			ObjectType string `json:"ObjectType"`
			Version    string `json:"Version"`
		} `json:"data"`
		PolicyName string `json:"policyName"`
		Namespace  string `json:"namespace"`
	}

	if err := json.Unmarshal(body, &eventData); err != nil {
		return EventPayload{}, fmt.Errorf("failed to unmarshal rotation event payload: %w", err)
	}

	targetObjectName := eventData.Data.ObjectName
	if targetObjectName == "" && eventData.Subject != "" {
		parts := strings.Split(eventData.Subject, "/")
		for i, part := range parts {
			if part == "secrets" && i+1 < len(parts) {
				targetObjectName = parts[i+1]
				break
			}
		}
	}

	return EventPayload{
		ObjectName: targetObjectName,
		PolicyName: eventData.PolicyName,
		Namespace:  eventData.Namespace,
	}, nil
}

// ParseRotationEvent unmarshals and extracts the target object name and policy name
// from an inbound rotation event payload. Returns (objectName, policyName, error).
func ParseRotationEvent(body []byte) (string, string, error) {
	payload, err := ParseRotationEventPayload(body)
	if err != nil {
		return "", "", err
	}
	return payload.ObjectName, payload.PolicyName, nil
}
