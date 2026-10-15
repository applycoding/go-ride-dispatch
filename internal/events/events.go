// Package events publishes dispatch facts.
// The default runtime bus is in-memory. KafkaAdapter is the same Publisher port
// backed by a Producer, so a broker client can be swapped in without touching
// the matcher.
package events

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	// TypeTripRequested is emitted when a rider creates a trip.
	TypeTripRequested = "trip.requested"
	// TypeTripAssigned is emitted when a driver is matched.
	TypeTripAssigned = "trip.assigned"
	// TypeTripEnRoute is emitted when the trip enters en-route.
	TypeTripEnRoute = "trip.en-route"
	// TypeTripCompleted is emitted when the trip finishes.
	TypeTripCompleted = "trip.completed"
	// TypeTripCancelled is emitted when the trip is cancelled.
	TypeTripCancelled = "trip.cancelled"
	// TypeDriverLocation is emitted when a driver reports a position.
	TypeDriverLocation = "driver.location"
	// TypeDriverStatus is emitted when a driver's availability changes.
	TypeDriverStatus = "driver.status"
)

// Location is an optional coordinate on an event.
type Location struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Event is the JSON document written to the bus and to Kafka.
type Event struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	OccurredAt time.Time `json:"occurred_at"`
	TripID     string    `json:"trip_id,omitempty"`
	RiderID    string    `json:"rider_id,omitempty"`
	DriverID   string    `json:"driver_id,omitempty"`
	Status     string    `json:"status,omitempty"`
	Location   *Location `json:"location,omitempty"`
}

// Publisher accepts events. Implementations must be safe for concurrent use.
type Publisher interface {
	Publish(ctx context.Context, event Event) error
}

// Subscriber is the read side of the in-memory bus.
// The cancel function detaches the subscription. It does not close the channel
// while a publisher might still send.
type Subscriber interface {
	Subscribe(types ...string) (<-chan Event, func())
}

// Producer is the Kafka client port used by KafkaAdapter.
type Producer interface {
	Produce(ctx context.Context, topic string, key, value []byte) error
}

// Multi fans an event out to every publisher.
type Multi []Publisher

// Publish implements Publisher.
func (m Multi) Publish(ctx context.Context, event Event) error {
	var err error
	for _, pub := range m {
		err = errors.Join(err, pub.Publish(ctx, event))
	}
	return err
}

// KafkaAdapter serializes events as JSON and writes them through a Producer.
type KafkaAdapter struct {
	producer Producer
	topic    string
}

// NewKafkaAdapter returns a Publisher that writes to topic.
func NewKafkaAdapter(producer Producer, topic string) *KafkaAdapter {
	return &KafkaAdapter{producer: producer, topic: topic}
}

// Publish implements Publisher.
func (k *KafkaAdapter) Publish(ctx context.Context, event Event) error {
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	key := event.TripID
	if key == "" {
		key = event.DriverID
	}
	if key == "" {
		key = event.ID
	}
	return k.producer.Produce(ctx, k.topic, []byte(key), body)
}
