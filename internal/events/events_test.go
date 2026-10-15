package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
)

func TestMemoryPublishAndSnapshot(t *testing.T) {
	m := events.NewMemory()
	ctx := context.Background()
	ev := events.Event{
		ID:         "e1",
		Type:       "trip.requested",
		OccurredAt: time.Unix(10, 0).UTC(),
		Trip:       domain.Trip{ID: "t1", Status: domain.TripRequested},
	}
	if err := m.Publish(ctx, ev); err != nil {
		t.Fatal(err)
	}
	got := m.Events()
	if len(got) != 1 || got[0].ID != "e1" || got[0].Trip.ID != "t1" {
		t.Fatalf("events = %+v", got)
	}
	got[0].ID = "mutated"
	if m.Events()[0].ID != "e1" {
		t.Fatal("snapshot must be a copy")
	}
}

func TestMemoryPublishHonorsCanceledContext(t *testing.T) {
	m := events.NewMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Publish(ctx, events.Event{}); err == nil {
		t.Fatal("expected context error")
	}
}

func TestNewKafkaValidatesConfig(t *testing.T) {
	if _, err := events.NewKafka(nil, "dispatch.trips", nil); err == nil {
		t.Fatal("expected brokers error")
	}
	if _, err := events.NewKafka([]string{"localhost:9092"}, "", nil); err == nil {
		t.Fatal("expected topic error")
	}
	k, err := events.NewKafka([]string{"localhost:9092"}, "dispatch.trips", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Close(); err != nil {
		t.Fatal(err)
	}
}
