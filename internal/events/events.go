// Package events publishes trip lifecycle facts.
// The in-memory publisher is the default for tests and the local demo.
// Kafka is used when brokers are configured.
package events

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/segmentio/kafka-go"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

const memoryLimit = 1024

// Event is one trip fact written to the bus.
type Event struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	OccurredAt time.Time   `json:"occurred_at"`
	Trip       domain.Trip `json:"trip"`
}

// Publisher accepts trip events. Implementations must be safe for concurrent use.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// Memory stores events in-process. It is the demo and test double for Kafka.
type Memory struct {
	mu     sync.Mutex
	events []Event
}

// NewMemory returns an empty in-memory publisher.
func NewMemory() *Memory {
	return &Memory{}
}

// Publish appends the event. The buffer keeps the most recent memoryLimit events.
func (m *Memory) Publish(ctx context.Context, event Event) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == memoryLimit {
		copy(m.events, m.events[1:])
		m.events[len(m.events)-1] = event
		return nil
	}
	m.events = append(m.events, event)
	return nil
}

// Events returns a copy of the recorded events in publish order.
func (m *Memory) Events() []Event {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Event, len(m.events))
	copy(out, m.events)
	return out
}

// Kafka publishes JSON events to a single topic. Delivery is synchronous and
// at-most-once: a failed write is logged by the caller and does not roll back the trip.
type Kafka struct {
	w *kafka.Writer
}

// NewKafka builds a writer for the given brokers and topic.
func NewKafka(brokers []string, topic string, log *slog.Logger) (*Kafka, error) {
	if len(brokers) == 0 {
		return nil, errNoBrokers
	}
	if topic == "" {
		return nil, errEmptyTopic
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Kafka{
		w: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Topic:                  topic,
			Balancer:               &kafka.LeastBytes{},
			RequiredAcks:           kafka.RequireOne,
			AllowAutoTopicCreation: true,
			Logger: kafka.LoggerFunc(func(string, ...interface{}) {
			}),
			ErrorLogger: kafka.LoggerFunc(func(msg string, args ...interface{}) {
				log.Error("kafka", "msg", msg, "args", args)
			}),
		},
	}, nil
}

// Publish writes one JSON message keyed by trip id.
func (k *Kafka) Publish(ctx context.Context, event Event) error {
	body, err := marshalEvent(event)
	if err != nil {
		return err
	}
	return k.w.WriteMessages(ctx, kafka.Message{
		Key:   []byte(event.Trip.ID),
		Value: body,
		Time:  event.OccurredAt,
	})
}

// Close flushes and closes the writer.
func (k *Kafka) Close() error {
	return k.w.Close()
}

type publishError string

func (e publishError) Error() string { return string(e) }

const (
	errNoBrokers  publishError = "kafka: no brokers"
	errEmptyTopic publishError = "kafka: empty topic"
)
