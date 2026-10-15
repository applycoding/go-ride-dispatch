// Package store persists riders, drivers, and trips.
package store

import (
	"context"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

// TripUpdate is a compare-and-swap status change.
// ExpectedDriver, when set, must match the trip's driver.
// ReleaseDriver, when set, is marked available in the same commit.
type TripUpdate struct {
	TripID         string
	From           domain.TripStatus
	To             domain.TripStatus
	ExpectedDriver string
	ReleaseDriver  string
	At             time.Time
}

// Repository is the system of record for riders, drivers, and trips.
type Repository interface {
	Ping(ctx context.Context) error
	CreateRider(ctx context.Context, rider domain.Rider) error
	GetRider(ctx context.Context, id string) (domain.Rider, error)
	CreateDriver(ctx context.Context, driver domain.Driver) error
	GetDriver(ctx context.Context, id string) (domain.Driver, error)
	SetDriverStatus(ctx context.Context, id string, from, to domain.DriverStatus) error
	CreateTrip(ctx context.Context, trip domain.Trip) error
	GetTrip(ctx context.Context, id string) (domain.Trip, error)
	AssignDriver(ctx context.Context, tripID, driverID string, at time.Time) error
	UpdateTrip(ctx context.Context, update TripUpdate) error
}
