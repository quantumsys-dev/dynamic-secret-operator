package aws

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/stretchr/testify/assert"

	"github.com/quantumsys-dev/dynamic-secret-operator/internal/events"
)

type mockSQSClient struct {
	ReceiveMessageFunc          func(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessageFunc           func(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibilityFunc func(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
}

func (m *mockSQSClient) ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	if m.ReceiveMessageFunc != nil {
		return m.ReceiveMessageFunc(ctx, params, optFns...)
	}
	return nil, errors.New("unexpected ReceiveMessage call")
}

func (m *mockSQSClient) DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	if m.DeleteMessageFunc != nil {
		return m.DeleteMessageFunc(ctx, params, optFns...)
	}
	return nil, errors.New("unexpected DeleteMessage call")
}

func (m *mockSQSClient) ChangeMessageVisibility(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	if m.ChangeMessageVisibilityFunc != nil {
		return m.ChangeMessageVisibilityFunc(ctx, params, optFns...)
	}
	return nil, errors.New("unexpected ChangeMessageVisibility call")
}

func TestSQSListener_Start(t *testing.T) {
	t.Run("Successful message receipt and ACK", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		ackCalled := false

		mockClient := &mockSQSClient{
			ReceiveMessageFunc: func(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
				defer cancel() // stop loop after first fetch
				return &sqs.ReceiveMessageOutput{
					Messages: []types.Message{
						{
							Body:          aws.String(`{"foo":"bar"}`),
							ReceiptHandle: aws.String("receipt-1"),
						},
					},
				}, nil
			},
			DeleteMessageFunc: func(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
				assert.Equal(t, "receipt-1", *params.ReceiptHandle)
				ackCalled = true
				return &sqs.DeleteMessageOutput{}, nil
			},
		}

		listener := &SQSListener{
			client:   mockClient,
			queueURL: "https://sqs.test",
		}

		listener.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
			assert.Equal(t, `{"foo":"bar"}`, string(body))
			return ack()
		})

		err := listener.Start(ctx)
		assert.NoError(t, err)
		assert.True(t, ackCalled, "DeleteMessage should have been called")
	})

	t.Run("Handler failure resulting in NACK", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		nackCalled := false

		mockClient := &mockSQSClient{
			ReceiveMessageFunc: func(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
				defer cancel()
				return &sqs.ReceiveMessageOutput{
					Messages: []types.Message{
						{
							Body:          aws.String("bad-payload"),
							ReceiptHandle: aws.String("receipt-2"),
						},
					},
				}, nil
			},
			ChangeMessageVisibilityFunc: func(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
				assert.Equal(t, "receipt-2", *params.ReceiptHandle)
				assert.Equal(t, int32(0), params.VisibilityTimeout)
				nackCalled = true
				return &sqs.ChangeMessageVisibilityOutput{}, nil
			},
		}

		listener := &SQSListener{
			client:   mockClient,
			queueURL: "https://sqs.test",
		}

		listener.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
			return errors.New("simulated handler failure")
		})

		err := listener.Start(ctx)
		assert.NoError(t, err)
		assert.True(t, nackCalled, "ChangeMessageVisibility should have been called with 0")
	})

	t.Run("Empty queue timeout handling", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		receiveCalled := false

		mockClient := &mockSQSClient{
			ReceiveMessageFunc: func(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
				receiveCalled = true
				return &sqs.ReceiveMessageOutput{
					Messages: []types.Message{},
				}, nil
			},
		}

		listener := &SQSListener{
			client:   mockClient,
			queueURL: "https://sqs.test",
		}

		listener.SetEventHandler(func(ctx context.Context, body []byte, ack events.AckFunc) error {
			t.Fatal("handler should not be called")
			return nil
		})

		err := listener.Start(ctx)
		assert.NoError(t, err)
		assert.True(t, receiveCalled, "ReceiveMessage should have been called")
	})
}
