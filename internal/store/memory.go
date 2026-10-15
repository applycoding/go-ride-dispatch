package store

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

// Memory is a mutex-guarded store with a geohash prefix index.
type Memory struct {
	mu      sync.Mutex
	drivers map[string]domain.Driver
	trips   map[string]domain.Trip
	// cells maps a geohash prefix to driver ids stored under that prefix.
	cells map[string]map[string]struct{}
}

// NewMemory returns an empty store.
func NewMemory() *Memory {
	return &Memory{
		drivers: map[string]domain.Driver{},
		trips:   map[string]domain.Trip{},
		cells:   map[string]map[string]struct{}{},
	}
}

// Ping implements Store.
func (m *Memory) Ping(context.Context) error { return nil }

// UpsertDriver implements Store.
func (m *Memory) UpsertDriver(ctx context.Context, driver domain.Driver) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.drivers[driver.ID]; exists {
		return domain.ErrConflict
	}
	m.drivers[driver.ID] = driver
	m.indexLocked(driver.ID, "", driver.Geohash)
	return nil
}

// GetDriver implements Store.
func (m *Memory) GetDriver(ctx context.Context, id string) (domain.Driver, error) {
	if err := ctx.Err(); err != nil {
		return domain.Driver{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	driver, ok := m.drivers[id]
	if !ok {
		return domain.Driver{}, domain.ErrNotFound
	}
	return driver, nil
}

// UpdateDriverLocation implements Store.
func (m *Memory) UpdateDriverLocation(ctx context.Context, id string, point domain.Point, geohash string, at time.Time) (domain.Driver, error) {
	if err := ctx.Err(); err != nil {
		return domain.Driver{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	driver, ok := m.drivers[id]
	if !ok {
		return domain.Driver{}, domain.ErrNotFound
	}
	old := driver.Geohash
	driver.Location = point
	driver.Geohash = geohash
	driver.UpdatedAt = at
	m.drivers[id] = driver
	m.indexLocked(id, old, geohash)
	return driver, nil
}

// SetDriverStatus implements Store.
func (m *Memory) SetDriverStatus(ctx context.Context, id string, status domain.DriverStatus, at time.Time) (domain.Driver, error) {
	if err := ctx.Err(); err != nil {
		return domain.Driver{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	driver, ok := m.drivers[id]
	if !ok {
		return domain.Driver{}, domain.ErrNotFound
	}
	if driver.Status == domain.DriverBusy && status != domain.DriverBusy {
		return domain.Driver{}, domain.ErrDriverBusy
	}
	driver.Status = status
	driver.UpdatedAt = at
	m.drivers[id] = driver
	return driver, nil
}

// ListAvailableInCells implements Store.
func (m *Memory) ListAvailableInCells(ctx context.Context, prefix string) ([]domain.Driver, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if prefix == "" {
		out := make([]domain.Driver, 0, len(m.drivers))
		for _, driver := range m.drivers {
			if driver.Status == domain.DriverAvailable {
				out = append(out, driver)
			}
		}
		return out, nil
	}
	ids := m.cells[prefix]
	out := make([]domain.Driver, 0, len(ids))
	for id := range ids {
		driver, ok := m.drivers[id]
		if !ok || driver.Status != domain.DriverAvailable {
			continue
		}
		if strings.HasPrefix(driver.Geohash, prefix) {
			out = append(out, driver)
		}
	}
	return out, nil
}

// CreateTrip implements Store.
func (m *Memory) CreateTrip(ctx context.Context, trip domain.Trip) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.trips[trip.ID]; exists {
		return domain.ErrConflict
	}
	m.trips[trip.ID] = trip
	return nil
}

// GetTrip implements Store.
func (m *Memory) GetTrip(ctx context.Context, id string) (domain.Trip, error) {
	if err := ctx.Err(); err != nil {
		return domain.Trip{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	trip, ok := m.trips[id]
	if !ok {
		return domain.Trip{}, domain.ErrNotFound
	}
	return trip, nil
}

// Assign implements Store.
func (m *Memory) Assign(ctx context.Context, tripID, driverID string, at time.Time) (domain.Trip, error) {
	if err := ctx.Err(); err != nil {
		return domain.Trip{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	trip, ok := m.trips[tripID]
	if !ok {
		return domain.Trip{}, domain.ErrNotFound
	}
	if trip.Status != domain.TripRequested {
		return domain.Trip{}, domain.ErrInvalidTransition
	}
	driver, ok := m.drivers[driverID]
	if !ok {
		return domain.Trip{}, domain.ErrNotFound
	}
	if driver.Status != domain.DriverAvailable {
		return domain.Trip{}, domain.ErrDriverUnavailable
	}
	driver.Status = domain.DriverBusy
	driver.UpdatedAt = at
	m.drivers[driverID] = driver
	trip.Status = domain.TripAssigned
	trip.DriverID = driverID
	trip.UpdatedAt = at
	m.trips[tripID] = trip
	return trip, nil
}

// Transition implements Store.
func (m *Memory) Transition(ctx context.Context, tripID string, from, to domain.TripStatus, at time.Time) (domain.Trip, error) {
	if err := ctx.Err(); err != nil {
		return domain.Trip{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	trip, ok := m.trips[tripID]
	if !ok {
		return domain.Trip{}, domain.ErrNotFound
	}
	if trip.Status != from {
		return domain.Trip{}, domain.ErrInvalidTransition
	}
	m.releaseDriverLocked(trip, to, at)
	trip.Status = to
	trip.UpdatedAt = at
	m.trips[tripID] = trip
	return trip, nil
}

func (m *Memory) releaseDriverLocked(trip domain.Trip, to domain.TripStatus, at time.Time) {
	if trip.DriverID == "" || !to.Terminal() {
		return
	}
	driver, ok := m.drivers[trip.DriverID]
	if !ok {
		return
	}
	if driver.Status == domain.DriverBusy {
		driver.Status = domain.DriverAvailable
		driver.UpdatedAt = at
		m.drivers[trip.DriverID] = driver
	}
}

func (m *Memory) indexLocked(id, oldHash, newHash string) {
	if oldHash == newHash {
		return
	}
	m.unindexLocked(id, oldHash)
	for n := 1; n <= len(newHash); n++ {
		key := newHash[:n]
		bucket := m.cells[key]
		if bucket == nil {
			bucket = map[string]struct{}{}
			m.cells[key] = bucket
		}
		bucket[id] = struct{}{}
	}
}

func (m *Memory) unindexLocked(id, hash string) {
	for n := 1; n <= len(hash); n++ {
		key := hash[:n]
		bucket := m.cells[key]
		if bucket == nil {
			continue
		}
		delete(bucket, id)
		if len(bucket) == 0 {
			delete(m.cells, key)
		}
	}
}
