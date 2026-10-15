package trip_test

import (
	"errors"
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

func TestTransition(t *testing.T) {
	legal := map[domain.TripStatus][]domain.TripStatus{
		domain.TripRequested:  {domain.TripMatched, domain.TripCancelled},
		domain.TripMatched:    {domain.TripAccepted, domain.TripCancelled},
		domain.TripAccepted:   {domain.TripArrived, domain.TripCancelled},
		domain.TripArrived:    {domain.TripInProgress, domain.TripCancelled},
		domain.TripInProgress: {domain.TripCompleted, domain.TripCancelled},
		domain.TripCompleted:  {},
		domain.TripCancelled:  {},
	}
	all := []domain.TripStatus{
		domain.TripRequested,
		domain.TripMatched,
		domain.TripAccepted,
		domain.TripArrived,
		domain.TripInProgress,
		domain.TripCompleted,
		domain.TripCancelled,
	}
	allowed := map[[2]domain.TripStatus]bool{}
	for from, tos := range legal {
		for _, to := range tos {
			allowed[[2]domain.TripStatus{from, to}] = true
		}
	}
	for _, from := range all {
		for _, to := range all {
			err := trip.Transition(from, to)
			if allowed[[2]domain.TripStatus{from, to}] {
				if err != nil {
					t.Fatalf("%s -> %s: %v", from, to, err)
				}
				continue
			}
			if !errors.Is(err, domain.ErrInvalidTransition) {
				t.Fatalf("%s -> %s error = %v, want invalid transition", from, to, err)
			}
		}
	}
	if err := trip.Transition("teleporting", domain.TripCompleted); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("unknown status error = %v", err)
	}
}
