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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/quantumsys-dev/dynamic-secret-operator/internal/events"
)

func TestApplicationPropertiesCarrier(t *testing.T) {
	t.Run("Get returns string values and empty string for missing/non-string keys", func(t *testing.T) {
		c := applicationPropertiesCarrier{
			"traceparent": "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
			"non-string":  42,
		}
		if got := c.Get("traceparent"); got != "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01" {
			t.Errorf("expected traceparent value, got %q", got)
		}
		if got := c.Get("non-string"); got != "" {
			t.Errorf("expected empty string for non-string value, got %q", got)
		}
		if got := c.Get("missing"); got != "" {
			t.Errorf("expected empty string for missing key, got %q", got)
		}
	})

	t.Run("Set stores a value retrievable via Get", func(t *testing.T) {
		c := applicationPropertiesCarrier{}
		c.Set("baggage", "userId=alice")
		if got := c.Get("baggage"); got != "userId=alice" {
			t.Errorf("expected 'userId=alice', got %q", got)
		}
	})

	t.Run("Keys returns all map keys", func(t *testing.T) {
		c := applicationPropertiesCarrier{"a": "1", "b": "2"}
		if got := len(c.Keys()); got != 2 {
			t.Fatalf("expected 2 keys, got %d", got)
		}
	})

	t.Run("integrates with the W3C TraceContext propagator to extract a remote span context", func(t *testing.T) {
		traceID := "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID := "00f067aa0ba902b7"
		props := applicationPropertiesCarrier{
			"traceparent": fmt.Sprintf("00-%s-%s-01", traceID, spanID),
		}

		ctx := propagation.TraceContext{}.Extract(context.Background(), props)
		sc := trace.SpanContextFromContext(ctx)
		if !sc.IsValid() {
			t.Fatalf("expected a valid extracted span context")
		}
		if sc.TraceID().String() != traceID {
			t.Errorf("expected traceID %q, got %q", traceID, sc.TraceID().String())
		}
		if sc.SpanID().String() != spanID {
			t.Errorf("expected spanID %q, got %q", spanID, sc.SpanID().String())
		}
	})
}

type mockTokenCredential struct{}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "mock-access-token",
		ExpiresOn: time.Now().Add(1 * time.Hour),
	}, nil
}

type mockReceiver struct {
	receiveFunc  func(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	completeFunc func(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error
	abandonFunc  func(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.AbandonMessageOptions) error
	closeFunc    func(ctx context.Context) error
}

func (m *mockReceiver) ReceiveMessages(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	if m.receiveFunc != nil {
		return m.receiveFunc(ctx, maxMessages, options)
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (m *mockReceiver) CompleteMessage(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error {
	if m.completeFunc != nil {
		return m.completeFunc(ctx, message, options)
	}
	return nil
}

func (m *mockReceiver) AbandonMessage(ctx context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.AbandonMessageOptions) error {
	if m.abandonFunc != nil {
		return m.abandonFunc(ctx, message, options)
	}
	return nil
}

func (m *mockReceiver) Close(ctx context.Context) error {
	if m.closeFunc != nil {
		return m.closeFunc(ctx)
	}
	return nil
}

func TestNewServiceBusListener(t *testing.T) {
	cred := &mockTokenCredential{}

	t.Run("fails when namespace is empty", func(t *testing.T) {
		_, err := NewServiceBusListener("", "my-queue", cred)
		if err == nil {
			t.Fatalf("expected error for empty namespace, got nil")
		}
	})

	t.Run("fails when queueName is empty", func(t *testing.T) {
		_, err := NewServiceBusListener("sb.servicebus.windows.net", "", cred)
		if err == nil {
			t.Fatalf("expected error for empty queueName, got nil")
		}
	})

	t.Run("fails when cred is nil", func(t *testing.T) {
		_, err := NewServiceBusListener("sb.servicebus.windows.net", "my-queue", nil)
		if err == nil {
			t.Fatalf("expected error for nil credential, got nil")
		}
	})

	t.Run("succeeds with valid parameters", func(t *testing.T) {
		listener, err := NewServiceBusListener("sb.servicebus.windows.net", "my-queue", cred)
		if err != nil {
			t.Fatalf("expected valid listener, got error: %v", err)
		}
		if listener == nil {
			t.Fatalf("expected non-nil listener")
		}
		if !listener.NeedLeaderElection() {
			t.Errorf("expected NeedLeaderElection to return true")
		}
	})
}

func TestServiceBusListener_Start_GracefulShutdown(t *testing.T) {
	cred := &mockTokenCredential{}
	listener, err := NewServiceBusListener("sb.servicebus.windows.net", "my-queue", cred)
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	closedCalled := false
	mock := &mockReceiver{
		receiveFunc: func(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
		closeFunc: func(ctx context.Context) error {
			closedCalled = true
			return nil
		},
	}
	listener.customReceiver = mock

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		done <- listener.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("expected clean exit with nil error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for listener to shut down gracefully")
	}

	if !closedCalled {
		t.Errorf("expected receiver Close() to be called on shutdown")
	}
}

func TestServiceBusListener_Start_ProcessesMessages(t *testing.T) {
	cred := &mockTokenCredential{}
	listener, err := NewServiceBusListener("sb.servicebus.windows.net", "my-queue", cred)
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	receivedCount := 0
	ackCalled := false
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	seq := int64(42)
	subj := "Microsoft.KeyVault.SecretNewVersionCreated"
	ct := "application/json"

	mock := &mockReceiver{
		receiveFunc: func(c context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
			if receivedCount == 0 {
				receivedCount++
				return []*azservicebus.ReceivedMessage{
					{
						MessageID:      "msg-001",
						SequenceNumber: &seq,
						Subject:        &subj,
						ContentType:    &ct,
						Body:           []byte(`{"eventType": "SecretNewVersionCreated"}`),
					},
				}, nil
			}
			cancel()
			return nil, errors.New("context canceled")
		},
		completeFunc: func(c context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error {
			ackCalled = true
			return nil
		},
	}
	listener.customReceiver = mock

	listener.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
		return ack(ctx)
	})

	err = listener.Start(ctx)
	if err != nil {
		t.Fatalf("expected clean exit from Start, got: %v", err)
	}
	if receivedCount != 1 {
		t.Errorf("expected 1 receive iteration, got %d", receivedCount)
	}
	if !ackCalled {
		t.Errorf("expected AckFunc to trigger CompleteMessage")
	}

	t.Run("abandons the message lock when handler returns error", func(t *testing.T) {
		listener, err := NewServiceBusListener("sb.servicebus.windows.net", "my-queue", cred)
		if err != nil {
			t.Fatalf("failed to create listener: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		abandonCalled := false
		var abandonedMessageID string
		mock := &mockReceiver{
			receiveFunc: func(c context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
				return []*azservicebus.ReceivedMessage{
					{
						MessageID: "msg-err",
						Body:      []byte(`{"eventType": "Test"}`),
					},
				}, nil
			},
			abandonFunc: func(c context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.AbandonMessageOptions) error {
				abandonCalled = true
				abandonedMessageID = message.MessageID
				return nil
			},
		}
		listener.customReceiver = mock
		listener.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
			cancel()
			return errors.New("handler failure")
		})

		_ = listener.Start(ctx)

		if !abandonCalled {
			t.Errorf("expected AbandonMessage to be called when the handler returns an error")
		}
		if abandonedMessageID != "msg-err" {
			t.Errorf("expected abandoned message ID %q, got %q", "msg-err", abandonedMessageID)
		}
	})

	t.Run("completes message using detached context even if caller context is already canceled", func(t *testing.T) {
		listener, err := NewServiceBusListener("sb.servicebus.windows.net", "my-queue", cred)
		if err != nil {
			t.Fatalf("failed to create listener: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		var completedWithContextErr error
		mock := &mockReceiver{
			receiveFunc: func(c context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
				return []*azservicebus.ReceivedMessage{
					{
						MessageID: "msg-canceled-ctx",
						Body:      []byte(`{"eventType": "Test"}`),
					},
				}, nil
			},
			completeFunc: func(c context.Context, message *azservicebus.ReceivedMessage, options *azservicebus.CompleteMessageOptions) error {
				completedWithContextErr = c.Err()
				cancel()
				return nil
			},
		}
		listener.customReceiver = mock
		listener.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
			canceledCtx, cancelNow := context.WithCancel(context.Background())
			cancelNow() // cancel immediately
			return ack(canceledCtx)
		})

		_ = listener.Start(ctx)

		if completedWithContextErr != nil {
			t.Errorf("expected CompleteMessage to receive a non-canceled detached context, but got ctx error: %v", completedWithContextErr)
		}
	})
}
