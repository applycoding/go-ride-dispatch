package trip_test

import (
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

func TestCanTransition(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		from domain.TripStatus
		to   domain.TripStatus
		ok   bool
	}{
		{name: "requested to assigned", from: domain.TripRequested, to: domain.TripAssigned, ok: true},
		{name: "requested to cancelled", from: domain.TripRequested, to: domain.TripCancelled, ok: true},
		{name: "requested to en-route", from: domain.TripRequested, to: domain.TripEnRoute, ok: false},
		{name: "requested to completed", from: domain.TripRequested, to: domain.TripCompleted, ok: false},
		{name: "assigned to en-route", from: domain.TripAssigned, to: domain.TripEnRoute, ok: true},
		{name: "assigned to cancelled", from: domain.TripAssigned, to: domain.TripCancelled, ok: true},
		{name: "assigned to completed", from: domain.TripAssigned, to: domain.TripCompleted, ok: false},
		{name: "en-route to completed", from: domain.TripEnRoute, to: domain.TripCompleted, ok: true},
		{name: "en-route to cancelled", from: domain.TripEnRoute, to: domain.TripCancelled, ok: true},
		{name: "en-route to assigned", from: domain.TripEnRoute, to: domain.TripAssigned, ok: false},
		{name: "completed to cancelled", from: domain.TripCompleted, to: domain.TripCancelled, ok: false},
		{name: "cancelled to assigned", from: domain.TripCancelled, to: domain.TripAssigned, ok: false},
		{name: "unknown origin", from: domain.TripStatus("paused"), to: domain.TripCancelled, ok: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := trip.CanTransition(tt.from, tt.to); got != tt.ok {
				t.Fatalf("CanTransition(%s, %s) = %v, want %v", tt.from, tt.to, got, tt.ok)
			}
		})
	}
}

func TestEventTypeCoversEveryStatus(t *testing.T) {
	t.Parallel()
	seen := map[string]domain.TripStatus{}
	for _, status := range domain.AllTripStatuses() {
		got := trip.EventType(status)
		if got == "" {
			t.Fatalf("empty event type for %s", status)
		}
		if prev, ok := seen[got]; ok {
			t.Fatalf("event type %s used by %s and %s", got, prev, status)
		}
		seen[got] = status
	}
}
