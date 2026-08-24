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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

var _ manager.Runnable = &ServiceBusListener{}
var _ manager.LeaderElectionRunnable = &ServiceBusListener{}

// Receiver defines the interface for receiving messages, allowing unit test mocking.
type Receiver interface {
	ReceiveMessages(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	Close(ctx context.Context) error
}

// ServiceBusListener implements manager.Runnable to listen to Azure Service Bus queue events
// via Peek-Lock mode.
type ServiceBusListener struct {
	Namespace   string
	QueueName   string
	Cred        azcore.TokenCredential
	MaxMessages int

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
			// NOTE: Do not call CompleteMessage() here.
			// Message completion and dead-letter handling is delegated to the progressive rollout reconciler.
		}
	}
}
