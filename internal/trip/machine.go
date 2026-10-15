// Package trip is the allowed lifecycle for a ride.
//
//	requested -> matched -> accepted -> arrived -> in_progress -> completed
//	any non-terminal state -> cancelled
package trip

import (
	"fmt"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

// Transition returns nil when from -> to is a legal trip edge.
func Transition(from, to domain.TripStatus) error {
	for _, next := range exits(from) {
		if next == to {
			return nil
		}
	}
	return fmt.Errorf("%w: %s -> %s", domain.ErrInvalidTransition, from, to)
}

func exits(from domain.TripStatus) []domain.TripStatus {
	switch from {
	case domain.TripRequested:
		return []domain.TripStatus{domain.TripMatched, domain.TripCancelled}
	case domain.TripMatched:
		return []domain.TripStatus{domain.TripAccepted, domain.TripCancelled}
	case domain.TripAccepted:
		return []domain.TripStatus{domain.TripArrived, domain.TripCancelled}
	case domain.TripArrived:
		return []domain.TripStatus{domain.TripInProgress, domain.TripCancelled}
	case domain.TripInProgress:
		return []domain.TripStatus{domain.TripCompleted, domain.TripCancelled}
	case domain.TripCompleted, domain.TripCancelled:
		return nil
	default:
		return nil
	}
}
