// Package domain holds the dispatch types shared by the HTTP API, storage, and matcher.
package domain

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
	"unicode/utf8"
)

const maxNameRunes = 80

var (
	ErrNotFound          = errors.New("not found")
	ErrConflict          = errors.New("conflict")
	ErrInvalid           = errors.New("invalid")
	ErrInvalidTransition = errors.New("invalid transition")
	ErrDriverUnavailable = errors.New("driver unavailable")
)

// Point is a WGS84 coordinate.
type Point struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// Validate reports whether the point is a finite WGS84 coordinate.
func (p Point) Validate() error {
	if math.IsNaN(p.Lat) || math.IsNaN(p.Lng) || math.IsInf(p.Lat, 0) || math.IsInf(p.Lng, 0) {
		return fmt.Errorf("%w: coordinates must be finite", ErrInvalid)
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lng < -180 || p.Lng > 180 {
		return fmt.Errorf("%w: coordinates out of range", ErrInvalid)
	}
	return nil
}

// NormalizeName trims a display name and rejects empty or oversized values.
func NormalizeName(name string) (string, error) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	if n < 1 || n > maxNameRunes {
		return "", fmt.Errorf("%w: name must be 1-%d characters", ErrInvalid, maxNameRunes)
	}
	return name, nil
}

// Rider is a person who requests trips.
type Rider struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

// DriverStatus is the dispatch availability of a driver.
type DriverStatus string

const (
	DriverOffline   DriverStatus = "offline"
	DriverAvailable DriverStatus = "available"
	DriverBusy      DriverStatus = "busy"
)

// Driver is a supply-side participant.
type Driver struct {
	ID        string       `json:"id"`
	Name      string       `json:"name"`
	Status    DriverStatus `json:"status"`
	CreatedAt time.Time    `json:"created_at"`
}

// TripStatus is a state in the trip lifecycle.
type TripStatus string

const (
	TripRequested  TripStatus = "requested"
	TripMatched    TripStatus = "matched"
	TripAccepted   TripStatus = "accepted"
	TripArrived    TripStatus = "arrived"
	TripInProgress TripStatus = "in_progress"
	TripCompleted  TripStatus = "completed"
	TripCancelled  TripStatus = "cancelled"
)

// Trip is one rider journey from request through a terminal state.
type Trip struct {
	ID              string     `json:"id"`
	RiderID         string     `json:"rider_id"`
	DriverID        string     `json:"driver_id,omitempty"`
	Status          TripStatus `json:"status"`
	Pickup          Point      `json:"pickup"`
	Dropoff         Point      `json:"dropoff"`
	FareCents       int64      `json:"fare_cents"`
	Currency        string     `json:"currency"`
	DistanceMeters  float64    `json:"distance_meters"`
	DurationSeconds int        `json:"duration_seconds"`
	Surge           float64    `json:"surge"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// DriverLocation is the last reported position of a driver.
type DriverLocation struct {
	DriverID  string
	Point     Point
	UpdatedAt time.Time
}
