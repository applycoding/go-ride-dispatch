package trip

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/geo"
	"github.com/applycoding/go-ride-dispatch/internal/id"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

const (
	defaultRadiusM = 5000
	maxMatchTries  = 5
	maxNameLen     = 80
)

// Recorder receives match and publish observations for metrics.
type Recorder interface {
	RecordMatch(elapsed time.Duration, matched bool)
	RecordTransition(status string)
	RecordPublishError()
}

// Service matches riders to the nearest available driver and moves trips
// through the status machine.
type Service struct {
	store   store.Store
	pub     events.Publisher
	metrics Recorder
	log     *slog.Logger
	radius  float64
	now     func() time.Time
}

// NewService builds a dispatch service. pub and metrics may be nil.
func NewService(st store.Store, pub events.Publisher, metrics Recorder, radius float64, logger *slog.Logger) *Service {
	if radius <= 0 {
		radius = defaultRadiusM
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:   st,
		pub:     pub,
		metrics: metrics,
		log:     logger,
		radius:  radius,
		now:     func() time.Time { return time.Now().UTC() },
	}
}

// RegisterDriver creates a driver in the offline state.
func (s *Service) RegisterDriver(ctx context.Context, driverID, name string) (domain.Driver, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return domain.Driver{}, fmt.Errorf("%w: name", domain.ErrInvalid)
	}
	if driverID == "" {
		driverID = id.New()
	}
	if err := domain.ValidateID(driverID); err != nil {
		return domain.Driver{}, err
	}
	now := s.now()
	driver := domain.Driver{
		ID:        driverID,
		Name:      name,
		Status:    domain.DriverOffline,
		UpdatedAt: now,
	}
	if err := s.store.UpsertDriver(ctx, driver); err != nil {
		return domain.Driver{}, err
	}
	s.publish(ctx, events.Event{
		Type:     events.TypeDriverStatus,
		DriverID: driver.ID,
		Status:   string(driver.Status),
	})
	return driver, nil
}

// UpdateLocation stores a driver position and indexes it by geohash.
func (s *Service) UpdateLocation(ctx context.Context, driverID string, point domain.Point) (domain.Driver, error) {
	if err := domain.ValidateID(driverID); err != nil {
		return domain.Driver{}, err
	}
	if !point.Valid() {
		return domain.Driver{}, fmt.Errorf("%w: location", domain.ErrInvalid)
	}
	hash := geo.Encode(point.Lat, point.Lng, geo.IndexPrecision)
	driver, err := s.store.UpdateDriverLocation(ctx, driverID, point, hash, s.now())
	if err != nil {
		return domain.Driver{}, err
	}
	s.publish(ctx, events.Event{
		Type:     events.TypeDriverLocation,
		DriverID: driver.ID,
		Status:   string(driver.Status),
		Location: &events.Location{Lat: point.Lat, Lng: point.Lng},
	})
	return driver, nil
}

// SetStatus changes availability. A busy driver stays busy until the trip ends.
func (s *Service) SetStatus(ctx context.Context, driverID string, status domain.DriverStatus) (domain.Driver, error) {
	if err := domain.ValidateID(driverID); err != nil {
		return domain.Driver{}, err
	}
	if !status.Valid() || status == domain.DriverBusy {
		return domain.Driver{}, fmt.Errorf("%w: status", domain.ErrInvalid)
	}
	driver, err := s.store.SetDriverStatus(ctx, driverID, status, s.now())
	if err != nil {
		return domain.Driver{}, err
	}
	s.publish(ctx, events.Event{
		Type:     events.TypeDriverStatus,
		DriverID: driver.ID,
		Status:   string(driver.Status),
	})
	return driver, nil
}

// Ping checks the store.
func (s *Service) Ping(ctx context.Context) error {
	return s.store.Ping(ctx)
}

// GetDriver loads a driver.
func (s *Service) GetDriver(ctx context.Context, driverID string) (domain.Driver, error) {
	if err := domain.ValidateID(driverID); err != nil {
		return domain.Driver{}, err
	}
	return s.store.GetDriver(ctx, driverID)
}

// NearbyDrivers returns available drivers within radiusM, nearest first.
func (s *Service) NearbyDrivers(ctx context.Context, point domain.Point, radiusM float64, limit int) ([]domain.Driver, error) {
	if !point.Valid() {
		return nil, fmt.Errorf("%w: location", domain.ErrInvalid)
	}
	if radiusM <= 0 {
		radiusM = s.radius
	}
	if radiusM > 50_000 {
		return nil, fmt.Errorf("%w: radius", domain.ErrInvalid)
	}
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	drivers, err := s.within(ctx, point, radiusM)
	if err != nil {
		return nil, err
	}
	if len(drivers) > limit {
		drivers = drivers[:limit]
	}
	return drivers, nil
}

// RequestTrip creates a trip and assigns the nearest available driver inside the radius.
// When nobody is close enough the trip stays requested.
func (s *Service) RequestTrip(ctx context.Context, riderID string, pickup, dropoff domain.Point, radiusM float64) (domain.Trip, error) {
	if err := domain.ValidateID(riderID); err != nil {
		return domain.Trip{}, err
	}
	if !pickup.Valid() || !dropoff.Valid() {
		return domain.Trip{}, fmt.Errorf("%w: location", domain.ErrInvalid)
	}
	if radiusM <= 0 {
		radiusM = s.radius
	}
	if radiusM > 50_000 {
		return domain.Trip{}, fmt.Errorf("%w: radius", domain.ErrInvalid)
	}
	now := s.now()
	trip := domain.Trip{
		ID:        id.New(),
		RiderID:   riderID,
		Status:    domain.TripRequested,
		Pickup:    pickup,
		Dropoff:   dropoff,
		DistanceM: geo.HaversineMeters(pickup.Lat, pickup.Lng, dropoff.Lat, dropoff.Lng),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateTrip(ctx, trip); err != nil {
		return domain.Trip{}, err
	}
	s.publishTrip(ctx, trip)
	s.recordTransition(trip.Status)

	started := time.Now()
	assigned, ok, err := s.tryMatch(ctx, trip, radiusM)
	s.recordMatch(time.Since(started), ok)
	if err != nil {
		return domain.Trip{}, err
	}
	if !ok {
		s.log.Info("trip unmatched", "trip_id", trip.ID, "rider_id", riderID)
		return trip, nil
	}
	return assigned, nil
}

// GetTrip loads a trip.
func (s *Service) GetTrip(ctx context.Context, tripID string) (domain.Trip, error) {
	if err := domain.ValidateID(tripID); err != nil {
		return domain.Trip{}, err
	}
	return s.store.GetTrip(ctx, tripID)
}

// MarkEnRoute moves an assigned trip to en-route.
func (s *Service) MarkEnRoute(ctx context.Context, tripID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, domain.TripEnRoute)
}

// CompleteTrip moves an en-route trip to completed and frees the driver.
func (s *Service) CompleteTrip(ctx context.Context, tripID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, domain.TripCompleted)
}

// CancelTrip cancels a trip that is not already terminal and frees an assigned driver.
func (s *Service) CancelTrip(ctx context.Context, tripID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, domain.TripCancelled)
}

func (s *Service) advance(ctx context.Context, tripID string, to domain.TripStatus) (domain.Trip, error) {
	if err := domain.ValidateID(tripID); err != nil {
		return domain.Trip{}, err
	}
	current, err := s.store.GetTrip(ctx, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	if !CanTransition(current.Status, to) {
		return domain.Trip{}, domain.ErrInvalidTransition
	}
	updated, err := s.store.Transition(ctx, tripID, current.Status, to, s.now())
	if err != nil {
		return domain.Trip{}, err
	}
	s.publishTrip(ctx, updated)
	s.recordTransition(updated.Status)
	if updated.DriverID != "" && to.Terminal() {
		s.publish(ctx, events.Event{
			Type:     events.TypeDriverStatus,
			DriverID: updated.DriverID,
			Status:   string(domain.DriverAvailable),
		})
	}
	return updated, nil
}

func (s *Service) tryMatch(ctx context.Context, trip domain.Trip, radiusM float64) (domain.Trip, bool, error) {
	candidates, err := s.within(ctx, trip.Pickup, radiusM)
	if err != nil {
		return domain.Trip{}, false, err
	}
	limit := maxMatchTries
	if len(candidates) < limit {
		limit = len(candidates)
	}
	for i := 0; i < limit; i++ {
		assigned, err := s.store.Assign(ctx, trip.ID, candidates[i].ID, s.now())
		if err == nil {
			s.publishTrip(ctx, assigned)
			s.recordTransition(assigned.Status)
			s.log.Info("trip assigned", "trip_id", assigned.ID, "driver_id", assigned.DriverID)
			return assigned, true, nil
		}
		if !isRetryableAssign(err) {
			return domain.Trip{}, false, err
		}
	}
	return domain.Trip{}, false, nil
}

func isRetryableAssign(err error) bool {
	return errors.Is(err, domain.ErrDriverUnavailable) || errors.Is(err, domain.ErrInvalidTransition)
}

func (s *Service) within(ctx context.Context, point domain.Point, radiusM float64) ([]domain.Driver, error) {
	prefix := geo.BoundingPrefix(point.Lat, point.Lng, radiusM)
	drivers, err := s.store.ListAvailableInCells(ctx, prefix)
	if err != nil {
		return nil, err
	}
	type candidate struct {
		driver domain.Driver
		dist   float64
	}
	ranked := make([]candidate, 0, len(drivers))
	for _, driver := range drivers {
		dist := geo.HaversineMeters(point.Lat, point.Lng, driver.Location.Lat, driver.Location.Lng)
		if dist <= radiusM {
			ranked = append(ranked, candidate{driver: driver, dist: dist})
		}
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].dist == ranked[j].dist {
			return ranked[i].driver.ID < ranked[j].driver.ID
		}
		return ranked[i].dist < ranked[j].dist
	})
	out := make([]domain.Driver, len(ranked))
	for i := range ranked {
		out[i] = ranked[i].driver
	}
	return out, nil
}

func (s *Service) publishTrip(ctx context.Context, trip domain.Trip) {
	s.publish(ctx, events.Event{
		Type:     EventType(trip.Status),
		TripID:   trip.ID,
		RiderID:  trip.RiderID,
		DriverID: trip.DriverID,
		Status:   string(trip.Status),
		Location: &events.Location{Lat: trip.Pickup.Lat, Lng: trip.Pickup.Lng},
	})
}

func (s *Service) publish(ctx context.Context, event events.Event) {
	if s.pub == nil {
		return
	}
	if event.ID == "" {
		event.ID = id.New()
	}
	if event.OccurredAt.IsZero() {
		event.OccurredAt = s.now()
	}
	if err := s.pub.Publish(ctx, event); err != nil {
		s.log.Error("publish event", "type", event.Type, "err", err)
		if s.metrics != nil {
			s.metrics.RecordPublishError()
		}
	}
}

func (s *Service) recordMatch(elapsed time.Duration, matched bool) {
	if s.metrics != nil {
		s.metrics.RecordMatch(elapsed, matched)
	}
}

func (s *Service) recordTransition(status domain.TripStatus) {
	if s.metrics != nil {
		s.metrics.RecordTransition(string(status))
	}
}
