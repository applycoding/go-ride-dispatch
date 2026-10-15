package store

import (
	"context"
	"sync"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

// Memory is a process-local repository for tests and the no-database demo.
type Memory struct {
	mu      sync.Mutex
	riders  map[string]domain.Rider
	drivers map[string]domain.Driver
	trips   map[string]domain.Trip
}

// NewMemory returns an empty repository.
func NewMemory() *Memory {
	return &Memory{
		riders:  make(map[string]domain.Rider),
		drivers: make(map[string]domain.Driver),
		trips:   make(map[string]domain.Trip),
	}
}

// Ping always succeeds.
func (m *Memory) Ping(context.Context) error { return nil }

// CreateRider inserts a rider.
func (m *Memory) CreateRider(_ context.Context, rider domain.Rider) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.riders[rider.ID]; ok {
		return domain.ErrConflict
	}
	m.riders[rider.ID] = rider
	return nil
}

// GetRider fetches a rider by id.
func (m *Memory) GetRider(_ context.Context, id string) (domain.Rider, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rider, ok := m.riders[id]
	if !ok {
		return domain.Rider{}, domain.ErrNotFound
	}
	return rider, nil
}

// CreateDriver inserts a driver.
func (m *Memory) CreateDriver(_ context.Context, driver domain.Driver) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.drivers[driver.ID]; ok {
		return domain.ErrConflict
	}
	m.drivers[driver.ID] = driver
	return nil
}

// GetDriver fetches a driver by id.
func (m *Memory) GetDriver(_ context.Context, id string) (domain.Driver, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	driver, ok := m.drivers[id]
	if !ok {
		return domain.Driver{}, domain.ErrNotFound
	}
	return driver, nil
}

// SetDriverStatus updates status when the current value is from.
func (m *Memory) SetDriverStatus(_ context.Context, id string, from, to domain.DriverStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	driver, ok := m.drivers[id]
	if !ok {
		return domain.ErrNotFound
	}
	if driver.Status != from {
		return domain.ErrConflict
	}
	driver.Status = to
	m.drivers[id] = driver
	return nil
}

// CreateTrip inserts a trip.
func (m *Memory) CreateTrip(_ context.Context, trip domain.Trip) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.trips[trip.ID]; ok {
		return domain.ErrConflict
	}
	m.trips[trip.ID] = trip
	return nil
}

// GetTrip fetches a trip by id.
func (m *Memory) GetTrip(_ context.Context, id string) (domain.Trip, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	trip, ok := m.trips[id]
	if !ok {
		return domain.Trip{}, domain.ErrNotFound
	}
	return trip, nil
}

// AssignDriver claims an available driver onto a requested trip.
func (m *Memory) AssignDriver(_ context.Context, tripID, driverID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	trip, ok := m.trips[tripID]
	if !ok {
		return domain.ErrNotFound
	}
	if trip.Status != domain.TripRequested || trip.DriverID != "" {
		return domain.ErrConflict
	}
	driver, ok := m.drivers[driverID]
	if !ok {
		return domain.ErrNotFound
	}
	if driver.Status != domain.DriverAvailable {
		return domain.ErrDriverUnavailable
	}
	driver.Status = domain.DriverBusy
	m.drivers[driverID] = driver
	trip.DriverID = driverID
	trip.Status = domain.TripMatched
	trip.UpdatedAt = at
	m.trips[tripID] = trip
	return nil
}

// UpdateTrip applies a compare-and-swap transition.
func (m *Memory) UpdateTrip(_ context.Context, update TripUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	trip, ok := m.trips[update.TripID]
	if !ok {
		return domain.ErrNotFound
	}
	if trip.Status != update.From {
		return domain.ErrConflict
	}
	if update.ExpectedDriver != "" && trip.DriverID != update.ExpectedDriver {
		return domain.ErrConflict
	}
	if update.ReleaseDriver != "" {
		driver, ok := m.drivers[update.ReleaseDriver]
		if !ok || driver.Status != domain.DriverBusy {
			return domain.ErrConflict
		}
		driver.Status = domain.DriverAvailable
		m.drivers[update.ReleaseDriver] = driver
	}
	trip.Status = update.To
	trip.UpdatedAt = update.At
	m.trips[update.TripID] = trip
	return nil
}
