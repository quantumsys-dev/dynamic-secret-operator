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

package azure

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/quantumsys-dev/dynamic-secret-operator/internal/events"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

// Compile-time assertion: ServiceBusListener must satisfy events.EventIngester.
var _ events.EventIngester = &ServiceBusListener{}

// applicationPropertiesCarrier adapts azservicebus's ApplicationProperties map to the
// OpenTelemetry propagation.TextMapCarrier interface, so a W3C trace context set by the
// producer (e.g. the system that published the Key Vault rotation event) can be extracted
// and continued, instead of every ingested message starting a disconnected trace.
type applicationPropertiesCarrier map[string]any

func (c applicationPropertiesCarrier) Get(key string) string {
	if v, ok := c[key].(string); ok {
		return v
	}
	return ""
}

func (c applicationPropertiesCarrier) Set(key, value string) {
	c[key] = value
}

func (c applicationPropertiesCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// Receiver defines the interface for receiving messages, allowing unit test mocking.
type Receiver interface {
	ReceiveMessages(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	CompleteMessage(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error
	AbandonMessage(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.AbandonMessageOptions) error
	Close(ctx context.Context) error
}

// ServiceBusListener implements events.EventIngester for Azure Service Bus.
// It operates in Peek-Lock mode and provides transactional message completion.
type ServiceBusListener struct {
	Namespace   string
	QueueName   string
	Cred        azcore.TokenCredential
	MaxMessages int
	handler     events.EventHandler

	// customReceiver allows injecting a mock receiver for testing.
	customReceiver Receiver
}

// NewServiceBusListener initializes a new pull-based Peek-Lock listener for Azure Service Bus.
func NewServiceBusListener(namespace, queueName string, cred azcore.TokenCredential) (*ServiceBusListener, error) {
	if namespace == "" {
		return nil, errors.New("service bus namespace must be specified")
	}
	if queueName == "" {
		return nil, errors.New("service bus queue name must be specified")
	}
	if cred == nil {
		return nil, errors.New("token credential must not be nil")
	}

	return &ServiceBusListener{
		Namespace:   namespace,
		QueueName:   queueName,
		Cred:        cred,
		MaxMessages: 10,
	}, nil
}

// SetEventHandler registers the provider-agnostic handler for received events.
// Satisfies events.EventIngester. Must be called before Start.
func (l *ServiceBusListener) SetEventHandler(h events.EventHandler) {
	l.handler = h
}

// NeedLeaderElection ensures this listener only runs on the active leader manager.
func (l *ServiceBusListener) NeedLeaderElection() bool {
	return true
}

// Start runs the message receiver loop until the provided context is canceled.
func (l *ServiceBusListener) Start(ctx context.Context) error {
	log := ctrl.LoggerFrom(ctx).WithName("servicebus-listener").WithValues(
		"namespace", l.Namespace,
		"queue", l.QueueName,
	)
	log.Info("starting Azure Service Bus peek-lock listener")

	var receiver Receiver
	var client *azservicebus.Client

	if l.customReceiver != nil {
		receiver = l.customReceiver
	} else {
		var err error
		client, err = azservicebus.NewClient(l.Namespace, l.Cred, nil)
		if err != nil {
			return fmt.Errorf("failed to create azservicebus client: %w", err)
		}
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = client.Close(closeCtx)
		}()

		realReceiver, err := client.NewReceiverForQueue(l.QueueName, &azservicebus.ReceiverOptions{
			ReceiveMode: azservicebus.ReceiveModePeekLock,
		})
		if err != nil {
			return fmt.Errorf("failed to create queue receiver: %w", err)
		}
		receiver = realReceiver
	}

	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = receiver.Close(closeCtx)
		log.Info("Azure Service Bus receiver stopped cleanly")
	}()

	maxMessages := l.MaxMessages
	if maxMessages <= 0 {
		maxMessages = 10
	}

	for {
		select {
		case <-ctx.Done():
			log.Info("shutdown signal received; exiting service bus receive loop")
			return nil
		default:
		}

		messages, err := receiver.ReceiveMessages(ctx, maxMessages, nil)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				log.Info("context canceled during receive; exiting gracefully")
				return nil
			}
			log.Error(err, "error receiving messages from service bus queue; retrying after backoff")
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(2 * time.Second):
				continue
			}
		}

		for _, msg := range messages {
			var seqNum int64
			if msg.SequenceNumber != nil {
				seqNum = *msg.SequenceNumber
			}
			var subject string
			if msg.Subject != nil {
				subject = *msg.Subject
			}
			var contentType string
			if msg.ContentType != nil {
				contentType = *msg.ContentType
			}

			log.Info("received Service Bus event via Peek-Lock",
				"messageID", msg.MessageID,
				"sequenceNumber", seqNum,
				"subject", subject,
				"contentType", contentType,
				"bodySize", len(msg.Body),
			)

			// Continue the producer's trace, if it propagated W3C trace context via
			// ApplicationProperties, rather than starting a disconnected trace per message.
			msgCtx := otel.GetTextMapPropagator().Extract(ctx, applicationPropertiesCarrier(msg.ApplicationProperties))
			msgCtx, span := telemetry.Tracer.Start(msgCtx, "ServiceBusReceiveMessage",
				trace.WithAttributes(
					attribute.String("messaging.system", "servicebus"),
					attribute.String("messaging.destination.name", l.QueueName),
					attribute.String("messaging.message.id", msg.MessageID),
				),
			)

			// Construct a provider-agnostic AckFunc that wraps CompleteMessage.
			// The ACK network call uses a detached, short-lived context with timeout so that
			// message completion succeeds reliably even if the caller's context timed out or was canceled.
			ackFunc := events.AckFunc(func(ackCtx context.Context) error {
				log.Info("completing Service Bus message after successful reconciliation", "messageID", msg.MessageID)
				completeCtx, completeCancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer completeCancel()

				if err := receiver.CompleteMessage(completeCtx, msg, nil); err != nil {
					telemetry.ServiceBusMessagesTotal.WithLabelValues("nack").Inc()
					return err
				}
				telemetry.ServiceBusMessagesTotal.WithLabelValues("ack").Inc()
				return nil
			})

			// Deliver the raw message body to the provider-agnostic handler.
			if l.handler != nil {
				if err := l.handler(msgCtx, msg.Body, ackFunc); err != nil {
					log.Error(err, "handler failed to process message, abandoning lock", "messageID", msg.MessageID)
					telemetry.ServiceBusMessagesTotal.WithLabelValues("nack").Inc()
					span.RecordError(err)

					// Release the peek-lock immediately so the message becomes available for
					// redelivery right away, instead of sitting locked until it naturally expires.
					// ctx may already be canceled (shutdown, or the handler's own timeout), so use
					// a short-lived context independent of it.
					abandonCtx, abandonCancel := context.WithTimeout(context.Background(), 5*time.Second)
					if abandonErr := receiver.AbandonMessage(abandonCtx, msg, nil); abandonErr != nil {
						log.Error(abandonErr, "failed to abandon message lock", "messageID", msg.MessageID)
					}
					abandonCancel()
				}
			}
			span.End()
		}
	}
}
