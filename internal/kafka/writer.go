// Package kafka adapts segmentio/kafka-go to the events.Producer port.
package kafka

import (
	"context"

	"github.com/segmentio/kafka-go"

	"github.com/applycoding/go-ride-dispatch/internal/events"
)

// Writer publishes bytes to a single Kafka topic.
type Writer struct {
	w *kafka.Writer
}

// NewWriter builds a synchronous writer. Topic creation is left to the broker.
func NewWriter(brokers []string, topic string) *Writer {
	return &Writer{w: &kafka.Writer{
		Addr:                   kafka.TCP(brokers...),
		Topic:                  topic,
		Balancer:               &kafka.LeastBytes{},
		RequiredAcks:           kafka.RequireOne,
		AllowAutoTopicCreation: true,
	}}
}

// Produce implements events.Producer. The topic argument is ignored because
// the writer is constructed for one topic.
func (w *Writer) Produce(ctx context.Context, _ string, key, value []byte) error {
	return w.w.WriteMessages(ctx, kafka.Message{Key: key, Value: value})
}

// Close flushes and closes the writer.
func (w *Writer) Close() error {
	return w.w.Close()
}

var _ events.Producer = (*Writer)(nil)
