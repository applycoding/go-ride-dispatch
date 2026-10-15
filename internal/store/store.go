// Package store persists drivers and trips.
package store

import (
	"context"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

// Store is the persistence port. Memory is used in tests and when DATABASE_URL
// is empty. Postgres is used when a database is configured.
type Store interface {
	Ping(ctx context.Context) error
	UpsertDriver(ctx context.Context, driver domain.Driver) error
	GetDriver(ctx context.Context, id string) (domain.Driver, error)
	UpdateDriverLocation(ctx context.Context, id string, point domain.Point, geohash string, at time.Time) (domain.Driver, error)
	SetDriverStatus(ctx context.Context, id string, status domain.DriverStatus, at time.Time) (domain.Driver, error)
	// ListAvailableInCells returns available drivers whose geohash starts with
	// prefix. An empty prefix returns every available driver.
	ListAvailableInCells(ctx context.Context, prefix string) ([]domain.Driver, error)
	CreateTrip(ctx context.Context, trip domain.Trip) error
	GetTrip(ctx context.Context, id string) (domain.Trip, error)
	// Assign moves a requested trip to assigned and the driver to busy.
	Assign(ctx context.Context, tripID, driverID string, at time.Time) (domain.Trip, error)
	// Transition applies a legal status change and frees the driver on a terminal state.
	Transition(ctx context.Context, tripID string, from, to domain.TripStatus, at time.Time) (domain.Trip, error)
}
