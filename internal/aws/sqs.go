package aws

import (
	"context"
	"fmt"
	"time"

	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/quantumsys-dev/dynamic-secret-operator/internal/events"
	"github.com/quantumsys-dev/dynamic-secret-operator/pkg/telemetry"
)

type SQSAPI interface {
	ReceiveMessage(ctx context.Context, params *sqs.ReceiveMessageInput, optFns ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, params *sqs.DeleteMessageInput, optFns ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(ctx context.Context, params *sqs.ChangeMessageVisibilityInput, optFns ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
}

type SQSListener struct {
	client   SQSAPI
	queueURL string
	handler  events.EventHandler
}

func NewSQSListener(cfg aws.Config, queueURL string) *SQSListener {
	return &SQSListener{
		client:   sqs.NewFromConfig(cfg),
		queueURL: queueURL,
	}
}

func (s *SQSListener) SetEventHandler(handler events.EventHandler) {
	s.handler = handler
}

func (s *SQSListener) NeedLeaderElection() bool {
	return true
}

// Start begins long-polling SQS for secret rotation events.
func (s *SQSListener) Start(ctx context.Context) error {
	if s.handler == nil {
		return fmt.Errorf("SQSListener cannot start without an EventHandler")
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		out, err := s.client.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{
			QueueUrl:              aws.String(s.queueURL),
			MaxNumberOfMessages:   10,
			WaitTimeSeconds:       20, // Long Polling to minimize costs
			MessageAttributeNames: []string{"All"},
		})

		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			// In production, we should log the error here. Pausing before retry.
			timer := time.NewTimer(5 * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil
			case <-timer.C:
				timer.Stop()
			}
			continue
		}

		for _, msg := range out.Messages {
			msg := msg // capture for closure

			go func(m types.Message) {
				startTime := time.Now()

				// Extract W3C Trace Context from MessageAttributes
				carrier := propagation.MapCarrier{}
				for k, v := range m.MessageAttributes {
					if v.StringValue != nil {
						carrier[k] = aws.ToString(v.StringValue)
					}
				}
				msgCtx := otel.GetTextMapPropagator().Extract(ctx, carrier)

				// Detached Context for ACK to succeed even if parent ctx gets cancelled
				ackFunc := func() error {
					if time.Since(startTime) > 30*time.Second {
						logger := log.FromContext(ctx).WithName("sqs-listener")
						logger.Info("WARNING: SQS message materialization latency > 30s. Ensure SQS Visibility Timeout is high enough to prevent redelivery.", "receiptHandle", m.ReceiptHandle)
					}
					ackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()

					_, err := s.client.DeleteMessage(ackCtx, &sqs.DeleteMessageInput{
						QueueUrl:      aws.String(s.queueURL),
						ReceiptHandle: m.ReceiptHandle,
					})
					if err != nil {
						telemetry.QueueMessagesTotal.WithLabelValues("nack").Inc()
						return fmt.Errorf("failed to ack SQS message: %w", err)
					}
					telemetry.QueueMessagesTotal.WithLabelValues("ack").Inc()
					return nil
				}

				var body []byte
				if m.Body != nil {
					body = []byte(*m.Body)
				}

				// Dispatch event
				err := s.handler(msgCtx, body, ackFunc)
				if err != nil {
					telemetry.QueueMessagesTotal.WithLabelValues("nack").Inc()
					// NACK: Return the message to the queue for retry (Redrive Policy will handle backoff)
					nackCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					_, _ = s.client.ChangeMessageVisibility(nackCtx, &sqs.ChangeMessageVisibilityInput{
						QueueUrl:          aws.String(s.queueURL),
						ReceiptHandle:     m.ReceiptHandle,
						VisibilityTimeout: 0,
					})
					cancel()
				}
			}(msg)
		}
	}
}
