// Package service implements rider registration, location ingest, matching, and the trip lifecycle.
package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/fare"
	"github.com/applycoding/go-ride-dispatch/internal/geo"
	"github.com/applycoding/go-ride-dispatch/internal/location"
	"github.com/applycoding/go-ride-dispatch/internal/matching"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/store"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

const (
	maxTripMeters     = 200_000
	matchCandidateCap = 16
	futureSkew        = 5 * time.Second
)

// TripListener receives trip snapshots after they are committed.
type TripListener interface {
	OnTrip(ctx context.Context, trip domain.Trip, eventType string)
}

// Deps wires the dispatcher. Repo and Locations are required.
type Deps struct {
	Repo              store.Repository
	Locations         location.Store
	Index             *matching.Index
	Publisher         events.Publisher
	Listener          TripListener
	Fares             fare.Params
	MatchRadiusMeters float64
	LocationMaxAge    time.Duration
	SpeedKmh          float64
	RoadFactor        float64
	GeohashPrecision  int
	Log               *slog.Logger
	Metrics           *metrics.Metrics
	Now               func() time.Time
	NewID             func() string
}

// Service is the ride-dispatch use-case layer.
type Service struct {
	repo       store.Repository
	locations  location.Store
	index      *matching.Index
	publisher  events.Publisher
	listener   TripListener
	fares      fare.Params
	radius     float64
	maxAge     time.Duration
	speedKmh   float64
	roadFactor float64
	log        *slog.Logger
	metrics    *metrics.Metrics
	now        func() time.Time
	newID      func() string
}

// New validates dependencies and fills demo defaults.
func New(d Deps) (*Service, error) {
	if d.Repo == nil || d.Locations == nil {
		return nil, fmt.Errorf("dispatch service: repository and location store are required")
	}
	if d.GeohashPrecision == 0 {
		d.GeohashPrecision = 6
	}
	if d.Index == nil {
		idx, err := matching.New(d.GeohashPrecision)
		if err != nil {
			return nil, err
		}
		d.Index = idx
	}
	if d.Publisher == nil {
		d.Publisher = events.NewMemory()
	}
	if d.MatchRadiusMeters <= 0 {
		d.MatchRadiusMeters = 5000
	}
	if d.LocationMaxAge <= 0 {
		d.LocationMaxAge = 2 * time.Minute
	}
	if d.SpeedKmh <= 0 {
		d.SpeedKmh = fare.DefaultSpeedKmh
	}
	if d.RoadFactor <= 0 {
		d.RoadFactor = fare.DefaultRoadFactor
	}
	if d.Log == nil {
		d.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if d.Now == nil {
		d.Now = func() time.Time { return time.Now().UTC() }
	}
	if d.NewID == nil {
		d.NewID = uuid.NewString
	}
	return &Service{
		repo:       d.Repo,
		locations:  d.Locations,
		index:      d.Index,
		publisher:  d.Publisher,
		listener:   d.Listener,
		fares:      d.Fares,
		radius:     d.MatchRadiusMeters,
		maxAge:     d.LocationMaxAge,
		speedKmh:   d.SpeedKmh,
		roadFactor: d.RoadFactor,
		log:        d.Log,
		metrics:    d.Metrics,
		now:        d.Now,
		newID:      d.NewID,
	}, nil
}

// RegisterRider creates a rider.
func (s *Service) RegisterRider(ctx context.Context, name string) (domain.Rider, error) {
	name, err := domain.NormalizeName(name)
	if err != nil {
		return domain.Rider{}, err
	}
	rider := domain.Rider{ID: s.newID(), Name: name, CreatedAt: s.now()}
	if err := s.repo.CreateRider(ctx, rider); err != nil {
		return domain.Rider{}, err
	}
	return rider, nil
}

// RegisterDriver creates a driver in the offline state.
func (s *Service) RegisterDriver(ctx context.Context, name string) (domain.Driver, error) {
	name, err := domain.NormalizeName(name)
	if err != nil {
		return domain.Driver{}, err
	}
	driver := domain.Driver{ID: s.newID(), Name: name, Status: domain.DriverOffline, CreatedAt: s.now()}
	if err := s.repo.CreateDriver(ctx, driver); err != nil {
		return domain.Driver{}, err
	}
	return driver, nil
}

// GetRider fetches a rider.
func (s *Service) GetRider(ctx context.Context, id string) (domain.Rider, error) {
	return s.repo.GetRider(ctx, id)
}

// GetDriver fetches a driver.
func (s *Service) GetDriver(ctx context.Context, id string) (domain.Driver, error) {
	return s.repo.GetDriver(ctx, id)
}

// SetDriverStatus moves a driver between offline and available.
// Busy is reserved for an assigned trip.
func (s *Service) SetDriverStatus(ctx context.Context, driverID string, status domain.DriverStatus) (domain.Driver, error) {
	if status != domain.DriverAvailable && status != domain.DriverOffline {
		return domain.Driver{}, fmt.Errorf("%w: status must be available or offline", domain.ErrInvalid)
	}
	driver, err := s.repo.GetDriver(ctx, driverID)
	if err != nil {
		return domain.Driver{}, err
	}
	if driver.Status == domain.DriverBusy {
		return domain.Driver{}, fmt.Errorf("%w: driver is on a trip", domain.ErrConflict)
	}
	if driver.Status == status {
		return driver, nil
	}
	if err := s.repo.SetDriverStatus(ctx, driverID, driver.Status, status); err != nil {
		return domain.Driver{}, err
	}
	driver.Status = status
	if status == domain.DriverOffline {
		s.index.Remove(driverID)
		return driver, nil
	}
	s.reindex(ctx, driverID)
	return driver, nil
}

// IngestLocation records a driver position. The first fix takes an offline driver online.
// Busy drivers keep their position but stay out of the available index.
func (s *Service) IngestLocation(ctx context.Context, driverID string, p domain.Point) (domain.DriverLocation, domain.DriverStatus, error) {
	if err := p.Validate(); err != nil {
		return domain.DriverLocation{}, "", err
	}
	driver, err := s.repo.GetDriver(ctx, driverID)
	if err != nil {
		return domain.DriverLocation{}, "", err
	}
	loc := domain.DriverLocation{DriverID: driverID, Point: p, UpdatedAt: s.now()}
	if err := s.locations.Put(ctx, loc); err != nil {
		return domain.DriverLocation{}, "", err
	}
	status := driver.Status
	if status == domain.DriverOffline {
		err := s.repo.SetDriverStatus(ctx, driverID, domain.DriverOffline, domain.DriverAvailable)
		switch {
		case err == nil:
			status = domain.DriverAvailable
		case errors.Is(err, domain.ErrConflict):
			driver, err = s.repo.GetDriver(ctx, driverID)
			if err != nil {
				return domain.DriverLocation{}, "", err
			}
			status = driver.Status
		default:
			return domain.DriverLocation{}, "", err
		}
	}
	if status == domain.DriverAvailable && s.fresh(loc.UpdatedAt) {
		if err := s.index.Upsert(driverID, p, loc.UpdatedAt); err != nil {
			return domain.DriverLocation{}, "", err
		}
	} else {
		s.index.Remove(driverID)
	}
	if s.metrics != nil {
		s.metrics.LocationIngest.Inc()
	}
	return loc, status, nil
}

// Estimate prices a pickup and dropoff. Omitted policy: surge must be in range.
func (s *Service) Estimate(_ context.Context, pickup, dropoff domain.Point, surge float64) (fare.Quote, error) {
	return s.quote(pickup, dropoff, surge)
}

// RequestTrip stores a requested trip, then offers it to the nearest fresh driver.
func (s *Service) RequestTrip(ctx context.Context, riderID string, pickup, dropoff domain.Point, surge float64) (domain.Trip, error) {
	if _, err := s.repo.GetRider(ctx, riderID); err != nil {
		return domain.Trip{}, err
	}
	quote, err := s.quote(pickup, dropoff, surge)
	if err != nil {
		return domain.Trip{}, err
	}
	now := s.now()
	created := domain.Trip{
		ID:              s.newID(),
		RiderID:         riderID,
		Status:          domain.TripRequested,
		Pickup:          pickup,
		Dropoff:         dropoff,
		FareCents:       quote.AmountCents,
		Currency:        quote.Currency,
		DistanceMeters:  quote.DistanceMeters,
		DurationSeconds: quote.DurationSeconds,
		Surge:           quote.Surge,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.repo.CreateTrip(ctx, created); err != nil {
		return domain.Trip{}, err
	}
	s.emit(ctx, "", created)
	return s.tryMatch(ctx, created)
}

// MatchTrip tries again for a trip that is still requested.
func (s *Service) MatchTrip(ctx context.Context, tripID string) (domain.Trip, error) {
	current, err := s.repo.GetTrip(ctx, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	return s.tryMatch(ctx, current)
}

// GetTrip fetches a trip.
func (s *Service) GetTrip(ctx context.Context, tripID string) (domain.Trip, error) {
	return s.repo.GetTrip(ctx, tripID)
}

// AcceptTrip is the driver's acceptance of a match.
func (s *Service) AcceptTrip(ctx context.Context, tripID, driverID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, driverID, domain.TripAccepted, false)
}

// Arrive marks the driver at the pickup.
func (s *Service) Arrive(ctx context.Context, tripID, driverID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, driverID, domain.TripArrived, false)
}

// StartTrip begins the metered portion of the trip. The fare itself stays the request-time quote.
func (s *Service) StartTrip(ctx context.Context, tripID, driverID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, driverID, domain.TripInProgress, false)
}

// CompleteTrip finishes the trip and returns the driver to the available index.
func (s *Service) CompleteTrip(ctx context.Context, tripID, driverID string) (domain.Trip, error) {
	return s.advance(ctx, tripID, driverID, domain.TripCompleted, true)
}

// CancelTrip lets the rider or the assigned driver cancel.
func (s *Service) CancelTrip(ctx context.Context, tripID, actorID string) (domain.Trip, error) {
	if actorID == "" {
		return domain.Trip{}, fmt.Errorf("%w: actor_id is required", domain.ErrInvalid)
	}
	current, err := s.repo.GetTrip(ctx, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	if actorID != current.RiderID && actorID != current.DriverID {
		return domain.Trip{}, fmt.Errorf("%w: actor cannot cancel this trip", domain.ErrConflict)
	}
	if err := trip.Transition(current.Status, domain.TripCancelled); err != nil {
		return domain.Trip{}, err
	}
	update := store.TripUpdate{
		TripID: current.ID,
		From:   current.Status,
		To:     domain.TripCancelled,
		At:     s.now(),
	}
	if current.DriverID != "" {
		update.ReleaseDriver = current.DriverID
	}
	if err := s.repo.UpdateTrip(ctx, update); err != nil {
		return domain.Trip{}, err
	}
	if current.DriverID != "" {
		s.reindex(ctx, current.DriverID)
	}
	updated, err := s.repo.GetTrip(ctx, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	s.emit(ctx, current.Status, updated)
	return updated, nil
}

// Recover rebuilds the available-driver index from stored locations.
func (s *Service) Recover(ctx context.Context) error {
	locs, err := s.locations.List(ctx)
	if err != nil {
		return err
	}
	for _, loc := range locs {
		driver, err := s.repo.GetDriver(ctx, loc.DriverID)
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		if driver.Status == domain.DriverAvailable && s.fresh(loc.UpdatedAt) {
			if err := s.index.Upsert(loc.DriverID, loc.Point, loc.UpdatedAt); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) advance(ctx context.Context, tripID, driverID string, to domain.TripStatus, release bool) (domain.Trip, error) {
	if driverID == "" {
		return domain.Trip{}, fmt.Errorf("%w: driver_id is required", domain.ErrInvalid)
	}
	current, err := s.repo.GetTrip(ctx, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	if current.DriverID != driverID {
		return domain.Trip{}, fmt.Errorf("%w: driver is not assigned to this trip", domain.ErrConflict)
	}
	if err := trip.Transition(current.Status, to); err != nil {
		return domain.Trip{}, err
	}
	update := store.TripUpdate{
		TripID:         tripID,
		From:           current.Status,
		To:             to,
		ExpectedDriver: driverID,
		At:             s.now(),
	}
	if release {
		update.ReleaseDriver = driverID
	}
	if err := s.repo.UpdateTrip(ctx, update); err != nil {
		return domain.Trip{}, err
	}
	if release {
		s.reindex(ctx, driverID)
	}
	updated, err := s.repo.GetTrip(ctx, tripID)
	if err != nil {
		return domain.Trip{}, err
	}
	s.emit(ctx, current.Status, updated)
	return updated, nil
}

func (s *Service) tryMatch(ctx context.Context, current domain.Trip) (domain.Trip, error) {
	if current.Status != domain.TripRequested {
		return current, nil
	}
	started := time.Now()
	cands := s.index.Nearest(current.Pickup, matchCandidateCap, s.radius)
	if s.metrics != nil {
		s.metrics.MatchDuration.Observe(time.Since(started).Seconds())
		s.metrics.MatchCandidates.Observe(float64(len(cands)))
	}
	for _, cand := range cands {
		if !s.fresh(cand.UpdatedAt) {
			continue
		}
		err := s.repo.AssignDriver(ctx, current.ID, cand.DriverID, s.now())
		switch {
		case err == nil:
			s.index.Remove(cand.DriverID)
			updated, getErr := s.repo.GetTrip(ctx, current.ID)
			if getErr != nil {
				return domain.Trip{}, getErr
			}
			s.emit(ctx, domain.TripRequested, updated)
			return updated, nil
		case errors.Is(err, domain.ErrDriverUnavailable), errors.Is(err, domain.ErrNotFound):
			s.index.Remove(cand.DriverID)
		case errors.Is(err, domain.ErrConflict):
			return s.repo.GetTrip(ctx, current.ID)
		default:
			return domain.Trip{}, err
		}
	}
	return current, nil
}

func (s *Service) quote(pickup, dropoff domain.Point, surge float64) (fare.Quote, error) {
	if err := pickup.Validate(); err != nil {
		return fare.Quote{}, err
	}
	if err := dropoff.Validate(); err != nil {
		return fare.Quote{}, err
	}
	straight := geo.HaversineMeters(pickup.Lat, pickup.Lng, dropoff.Lat, dropoff.Lng)
	if straight > maxTripMeters {
		return fare.Quote{}, fmt.Errorf("%w: distance exceeds %.0f km", domain.ErrInvalid, float64(maxTripMeters)/1000)
	}
	billed := straight * s.roadFactor
	seconds := fare.DurationSeconds(billed, s.speedKmh)
	return s.fares.Estimate(billed, straight, seconds, surge)
}

func (s *Service) reindex(ctx context.Context, driverID string) {
	loc, err := s.locations.Get(ctx, driverID)
	if err != nil || !s.fresh(loc.UpdatedAt) {
		s.index.Remove(driverID)
		return
	}
	if err := s.index.Upsert(driverID, loc.Point, loc.UpdatedAt); err != nil {
		s.log.Error("reindex driver", "driver_id", driverID, "err", err)
	}
}

func (s *Service) fresh(at time.Time) bool {
	age := s.now().Sub(at)
	return age <= s.maxAge && age >= -futureSkew
}

func (s *Service) emit(ctx context.Context, from domain.TripStatus, current domain.Trip) {
	if s.metrics != nil {
		s.metrics.TripTransitions.WithLabelValues(string(from), string(current.Status)).Inc()
	}
	event := events.Event{
		ID:         s.newID(),
		Type:       eventType(current.Status),
		OccurredAt: current.UpdatedAt,
		Trip:       current,
	}
	if err := s.publisher.Publish(ctx, event); err != nil {
		s.log.Error("publish event", "type", event.Type, "trip_id", current.ID, "err", err)
		if s.metrics != nil {
			s.metrics.EventsFailed.Inc()
		}
	} else if s.metrics != nil {
		s.metrics.EventsPublished.Inc()
	}
	if s.listener != nil {
		s.listener.OnTrip(ctx, current, event.Type)
	}
	origin := string(from)
	if origin == "" {
		origin = "none"
	}
	s.log.Info("trip transition", "trip_id", current.ID, "from", origin, "to", current.Status, "driver_id", current.DriverID)
}

func eventType(status domain.TripStatus) string {
	switch status {
	case domain.TripRequested:
		return "trip.requested"
	case domain.TripMatched:
		return "trip.matched"
	case domain.TripAccepted:
		return "trip.accepted"
	case domain.TripArrived:
		return "trip.arrived"
	case domain.TripInProgress:
		return "trip.started"
	case domain.TripCompleted:
		return "trip.completed"
	case domain.TripCancelled:
		return "trip.cancelled"
	default:
		return "trip.updated"
	}
}
