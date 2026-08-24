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
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// mockTokenCredential implements azcore.TokenCredential for tests.
type mockTokenCredential struct{}

func (m *mockTokenCredential) GetToken(ctx context.Context, options policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{
		Token:     "mock-access-token",
		ExpiresOn: time.Now().Add(1 * time.Hour),
	}, nil
}

// mockReceiver implements Receiver interface for tests.
type mockReceiver struct {
	receiveFunc func(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error)
	closeFunc   func(ctx context.Context) error
}

func (m *mockReceiver) ReceiveMessages(ctx context.Context, maxMessages int, options *azservicebus.ReceiveMessagesOptions) ([]*azservicebus.ReceivedMessage, error) {
	if m.receiveFunc != nil {
		return m.receiveFunc(ctx, maxMessages, options)
	}
	<-ctx.Done()
	return nil, ctx.Err()
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
			// Simulate waiting on context cancellation
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

	// Allow goroutine to start and enter receive loop
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
			// Cancel context after first batch
			cancel()
			return nil, errors.New("context canceled")
		},
	}
	listener.customReceiver = mock

	err = listener.Start(ctx)
	if err != nil {
		t.Fatalf("expected clean exit from Start, got: %v", err)
	}
	if receivedCount != 1 {
		t.Errorf("expected 1 receive iteration, got %d", receivedCount)
	}
}
