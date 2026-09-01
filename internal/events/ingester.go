// Copyright 2026 QuantumSys. Licensed under the Apache License, Version 2.0.
// See the full license text at http://www.apache.org/licenses/LICENSE-2.0

// Package events defines the provider-agnostic event ingestion interface used by the
// operator to decouple cmd/main.go from any specific cloud messaging transport
// (Azure Service Bus, AWS EventBridge/SQS, GCP Eventarc/Pub-Sub, etc.).
//
// Design intent (v0.3.0 extensibility):
// Every cloud provider rotation notification system MUST be adapted to this
// interface before being wired into the manager. The core binary therefore
// carries zero cloud-messaging SDK imports at the cmd layer.
package events

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// AckFunc settles (completes/acks) a received event after the secret revision has
// been successfully materialized in the cluster. It is provider-agnostic: Azure
// Service Bus wraps CompleteMessage, AWS SQS wraps DeleteMessage, GCP Pub/Sub
// wraps Acknowledge, etc.
type AckFunc func(ctx context.Context) error

// EventHandler is the callback signature delivered to an EventIngester.
// Implementations receive the raw event body (JSON bytes) and a settlement function.
// The handler is responsible for parsing the body, enqueuing reconcile requests,
// and registering the AckFunc for deferred settlement after materialization.
type EventHandler func(ctx context.Context, body []byte, ack AckFunc) error

// EventIngester is the provider-agnostic interface that every cloud messaging
// adapter must implement to participate in the operator's event-driven rotation
// pipeline. It composes manager.Runnable so the ingester can be registered
// directly with controller-runtime's manager lifecycle.
//
// Implementing providers:
//   - Azure: internal/azure.ServiceBusListener
//   - AWS    (v0.3.0): internal/aws.SQSIngester     (planned)
//   - GCP    (v0.3.0): internal/gcp.PubSubIngester  (planned)
//   - Vault  (v0.3.0): internal/vault.AgentIngester (planned)
type EventIngester interface {
	manager.Runnable
	manager.LeaderElectionRunnable

	// SetEventHandler registers the callback that processes each inbound
	// rotation event. Must be called before Start.
	SetEventHandler(h EventHandler)
}
