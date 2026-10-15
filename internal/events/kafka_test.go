package events_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/events"
)

type fakeProducer struct {
	topic string
	key   []byte
	value []byte
	err   error
}

func (f *fakeProducer) Produce(_ context.Context, topic string, key, value []byte) error {
	f.topic = topic
	f.key = append([]byte(nil), key...)
	f.value = append([]byte(nil), value...)
	return f.err
}

func TestKafkaAdapterWritesJSON(t *testing.T) {
	t.Parallel()
	producer := &fakeProducer{}
	adapter := events.NewKafkaAdapter(producer, "dispatch.events")
	event := events.Event{
		ID:       "e1",
		Type:     events.TypeTripAssigned,
		TripID:   "trip-1",
		DriverID: "driver-9",
		Status:   "assigned",
	}
	if err := adapter.Publish(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if producer.topic != "dispatch.events" {
		t.Fatalf("topic = %s", producer.topic)
	}
	if string(producer.key) != "trip-1" {
		t.Fatalf("key = %s", producer.key)
	}
	var decoded events.Event
	if err := json.Unmarshal(producer.value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Type != events.TypeTripAssigned || decoded.DriverID != "driver-9" {
		t.Fatalf("payload = %#v", decoded)
	}
}

func TestKafkaAdapterReturnsProducerError(t *testing.T) {
	t.Parallel()
	want := errors.New("broker down")
	adapter := events.NewKafkaAdapter(&fakeProducer{err: want}, "dispatch.events")
	err := adapter.Publish(context.Background(), events.Event{ID: "e", Type: events.TypeTripRequested, DriverID: "d"})
	if !errors.Is(err, want) {
		t.Fatalf("err = %v", err)
	}
}

func TestMultiJoinsErrors(t *testing.T) {
	t.Parallel()
	bus := events.NewMemoryBus()
	fail := &fakeProducer{err: errors.New("nope")}
	pub := events.Multi{bus, events.NewKafkaAdapter(fail, "t")}
	err := pub.Publish(context.Background(), events.Event{ID: "1", Type: events.TypeTripCancelled, TripID: "t"})
	if err == nil {
		t.Fatal("expected joined error")
	}
}
