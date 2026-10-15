package events_test

import (
	"context"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/events"
)

func TestMemoryBusDeliversSubscribedType(t *testing.T) {
	t.Parallel()
	bus := events.NewMemoryBus()
	ch, cancel := bus.Subscribe(events.TypeTripAssigned)
	defer cancel()

	ctx := context.Background()
	if err := bus.Publish(ctx, events.Event{ID: "1", Type: events.TypeTripRequested}); err != nil {
		t.Fatal(err)
	}
	if err := bus.Publish(ctx, events.Event{ID: "2", Type: events.TypeTripAssigned, TripID: "t1"}); err != nil {
		t.Fatal(err)
	}

	select {
	case got := <-ch:
		if got.ID != "2" || got.TripID != "t1" {
			t.Fatalf("got %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestMemoryBusDropsWhenFull(t *testing.T) {
	t.Parallel()
	bus := events.NewMemoryBus()
	_, cancel := bus.Subscribe()
	defer cancel()

	ctx := context.Background()
	for i := 0; i < 200; i++ {
		if err := bus.Publish(ctx, events.Event{ID: "x", Type: events.TypeDriverLocation}); err != nil {
			t.Fatal(err)
		}
	}
	if bus.Dropped() == 0 {
		t.Fatal("expected dropped events once the buffer filled")
	}
}

func TestMemoryBusCancelStopsDelivery(t *testing.T) {
	t.Parallel()
	bus := events.NewMemoryBus()
	ch, cancel := bus.Subscribe()
	cancel()
	if err := bus.Publish(context.Background(), events.Event{ID: "1", Type: events.TypeTripRequested}); err != nil {
		t.Fatal(err)
	}
	select {
	case ev := <-ch:
		t.Fatalf("received %#v after cancel", ev)
	case <-time.After(50 * time.Millisecond):
	}
}
