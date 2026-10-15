// Package domain holds the ride-dispatch types shared by the API, store, and matcher.
package domain

import (
	"errors"
	"fmt"
	"time"
)

const (
	// DriverOffline means the driver is not eligible for new trips.
	DriverOffline DriverStatus = "offline"
	// DriverAvailable means the driver can be matched.
	DriverAvailable DriverStatus = "available"
	// DriverBusy means the driver is already on a trip.
	DriverBusy DriverStatus = "busy"

	// TripRequested is a new trip waiting for a driver.
	TripRequested TripStatus = "requested"
	// TripAssigned means a driver has been selected.
	TripAssigned TripStatus = "assigned"
	// TripEnRoute means the driver is carrying the rider.
	TripEnRoute TripStatus = "en-route"
	// TripCompleted is a terminal successful state.
	TripCompleted TripStatus = "completed"
	// TripCancelled is a terminal unsuccessful state.
	TripCancelled TripStatus = "cancelled"
)

var (
	// ErrNotFound is returned when a driver or trip does not exist.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when an identifier is already in use.
	ErrConflict = errors.New("conflict")
	// ErrInvalid is returned for rejected caller input.
	ErrInvalid = errors.New("invalid input")
	// ErrInvalidTransition is returned when a trip status change is not allowed.
	ErrInvalidTransition = errors.New("invalid trip transition")
	// ErrDriverUnavailable is returned when a driver cannot be assigned.
	ErrDriverUnavailable = errors.New("driver unavailable")
	// ErrDriverBusy is returned when a busy driver is asked to go offline.
	ErrDriverBusy = errors.New("driver busy")
)

// DriverStatus is the availability of a driver.
type DriverStatus string

// Valid reports whether s is a known driver status.
func (s DriverStatus) Valid() bool {
	switch s {
	case DriverOffline, DriverAvailable, DriverBusy:
		return true
	default:
		return false
	}
}

// TripStatus is a step in the trip lifecycle.
type TripStatus string

// AllTripStatuses is the closed set of trip states.
func AllTripStatuses() []TripStatus {
	return []TripStatus{
		TripRequested,
		TripAssigned,
		TripEnRoute,
		TripCompleted,
		TripCancelled,
	}
}

// Valid reports whether s is a known trip status.
func (s TripStatus) Valid() bool {
	switch s {
	case TripRequested, TripAssigned, TripEnRoute, TripCompleted, TripCancelled:
		return true
	default:
		return false
	}
}

// Terminal reports whether the trip can no longer change.
func (s TripStatus) Terminal() bool {
	switch s {
	case TripCompleted, TripCancelled:
		return true
	case TripRequested, TripAssigned, TripEnRoute:
		return false
	default:
		return false
	}
}

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Valid reports whether the coordinate is inside the WGS84 range.
func (p Point) Valid() bool {
	return p.Lat >= -90 && p.Lat <= 90 && p.Lng >= -180 && p.Lng <= 180
}

// Driver is a supply-side participant.
type Driver struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    DriverStatus `json:"status"`
	Location  Point        `json:"location"`
	Geohash   string       `json:"geohash"`
	UpdatedAt time.Time    `json:"updated_at"`
}

// Trip is one rider request and its lifecycle.
type Trip struct {
	ID        string     `json:"id"`
	RiderID   string     `json:"rider_id"`
	DriverID  string     `json:"driver_id,omitempty"`
	Status    TripStatus `json:"status"`
	Pickup    Point      `json:"pickup"`
	Dropoff   Point      `json:"dropoff"`
	DistanceM float64    `json:"distance_m"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
}

// ValidateID reports whether id is safe to use in a URL path.
func ValidateID(id string) error {
	if id == "" || len(id) > 64 {
		return fmt.Errorf("%w: id length", ErrInvalid)
	}
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_' || r == '-':
		default:
			return fmt.Errorf("%w: id characters", ErrInvalid)
		}
	}
	return nil
}
