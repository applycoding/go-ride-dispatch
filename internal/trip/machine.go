// Package trip implements the trip state machine and nearest-driver matching.
package trip

import (
	"fmt"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
)

// CanTransition reports whether a trip may move from one status to another.
// requested to assigned is included; the store performs that edge in Assign
// so the driver and the trip change together.
func CanTransition(from, to domain.TripStatus) bool {
	switch from {
	case domain.TripRequested:
		return to == domain.TripAssigned || to == domain.TripCancelled
	case domain.TripAssigned:
		return to == domain.TripEnRoute || to == domain.TripCancelled
	case domain.TripEnRoute:
		return to == domain.TripCompleted || to == domain.TripCancelled
	case domain.TripCompleted, domain.TripCancelled:
		return false
	default:
		var never domain.TripStatus = from
		_ = never
		return false
	}
}

// EventType maps a trip status to the event name emitted for that state.
func EventType(status domain.TripStatus) string {
	switch status {
	case domain.TripRequested:
		return events.TypeTripRequested
	case domain.TripAssigned:
		return events.TypeTripAssigned
	case domain.TripEnRoute:
		return events.TypeTripEnRoute
	case domain.TripCompleted:
		return events.TypeTripCompleted
	case domain.TripCancelled:
		return events.TypeTripCancelled
	default:
		var never domain.TripStatus = status
		panic(fmt.Sprintf("unhandled trip status %q", never))
	}
}
