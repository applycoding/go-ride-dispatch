package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/fare"
	"github.com/applycoding/go-ride-dispatch/internal/geo"
	"github.com/applycoding/go-ride-dispatch/internal/location"
	"github.com/applycoding/go-ride-dispatch/internal/service"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newService(t *testing.T, c *clock) (*service.Service, *events.Memory) {
	t.Helper()
	pub := events.NewMemory()
	svc, err := service.New(service.Deps{
		Repo:              store.NewMemory(),
		Locations:         location.NewMemory(),
		Publisher:         pub,
		Fares:             fare.Params{BaseCents: 250, PerKmCents: 150, PerMinCents: 30, Currency: "USD"},
		MatchRadiusMeters: 5000,
		LocationMaxAge:    time.Minute,
		GeohashPrecision:  6,
		Now:               c.now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return svc, pub
}

func TestRequestMatchesNearestFreshDriver(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)}
	svc, pub := newService(t, c)
	ctx := context.Background()
	rider := mustRider(t, svc, "Ava")
	near := mustDriver(t, svc, "Near")
	far := mustDriver(t, svc, "Far")
	pickup := domain.Point{Lat: 37.7749, Lng: -122.4194}
	mustLoc(t, svc, near.ID, pickup)
	mustLoc(t, svc, far.ID, domain.Point{Lat: 37.7900, Lng: -122.4194})

	trip := mustTrip(t, svc, rider.ID, pickup, domain.Point{Lat: 37.7849, Lng: -122.4094}, 1)
	if trip.Status != domain.TripMatched || trip.DriverID != near.ID {
		t.Fatalf("trip = %+v", trip)
	}
	if trip.FareCents <= 0 || trip.Currency != "USD" {
		t.Fatalf("fare = %d %s", trip.FareCents, trip.Currency)
	}
	straight := geo.HaversineMeters(pickup.Lat, pickup.Lng, 37.7849, -122.4094)
	quote, err := fare.Params{BaseCents: 250, PerKmCents: 150, PerMinCents: 30, Currency: "USD"}.
		Estimate(straight*fare.DefaultRoadFactor, straight, fare.DurationSeconds(straight*fare.DefaultRoadFactor, 30), 1)
	if err != nil {
		t.Fatal(err)
	}
	if trip.FareCents != quote.AmountCents {
		t.Fatalf("fare = %d, want %d", trip.FareCents, quote.AmountCents)
	}
	driver, err := svc.GetDriver(ctx, near.ID)
	if err != nil {
		t.Fatal(err)
	}
	if driver.Status != domain.DriverBusy {
		t.Fatalf("near status = %s", driver.Status)
	}
	types := eventTypes(pub)
	if len(types) != 2 || types[0] != "trip.requested" || types[1] != "trip.matched" {
		t.Fatalf("events = %v", types)
	}
}

func TestStaleDriverIsSkipped(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)}
	svc, _ := newService(t, c)
	rider := mustRider(t, svc, "Ava")
	stale := mustDriver(t, svc, "Stale")
	fresh := mustDriver(t, svc, "Fresh")
	pickup := domain.Point{Lat: 37.7749, Lng: -122.4194}
	mustLoc(t, svc, stale.ID, pickup)
	c.t = c.t.Add(3 * time.Minute)
	mustLoc(t, svc, fresh.ID, domain.Point{Lat: 37.7800, Lng: -122.4194})

	trip := mustTrip(t, svc, rider.ID, pickup, domain.Point{Lat: 37.79, Lng: -122.41}, 1)
	if trip.DriverID != fresh.ID {
		t.Fatalf("driver = %s, want fresh", trip.DriverID)
	}
}

func TestNoDriverLeavesTripRequested(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)}
	svc, pub := newService(t, c)
	rider := mustRider(t, svc, "Ava")
	trip := mustTrip(t, svc, rider.ID, domain.Point{Lat: 37.77, Lng: -122.42}, domain.Point{Lat: 37.78, Lng: -122.41}, 1.5)
	if trip.Status != domain.TripRequested || trip.DriverID != "" {
		t.Fatalf("trip = %+v", trip)
	}
	if trip.Surge != 1.5 {
		t.Fatalf("surge = %f", trip.Surge)
	}
	if got := eventTypes(pub); len(got) != 1 || got[0] != "trip.requested" {
		t.Fatalf("events = %v", got)
	}
}

func TestLifecycleAndRematchAfterCancel(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)}
	svc, _ := newService(t, c)
	ctx := context.Background()
	rider := mustRider(t, svc, "Ava")
	driver := mustDriver(t, svc, "Sam")
	pickup := domain.Point{Lat: 37.7749, Lng: -122.4194}
	dropoff := domain.Point{Lat: 37.7849, Lng: -122.4094}
	mustLoc(t, svc, driver.ID, pickup)

	first := mustTrip(t, svc, rider.ID, pickup, dropoff, 1)
	if _, err := svc.AcceptTrip(ctx, first.ID, "someone-else"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("wrong driver = %v", err)
	}
	for _, step := range []func(string, string) (domain.Trip, error){
		func(id, driverID string) (domain.Trip, error) { return svc.AcceptTrip(ctx, id, driverID) },
		func(id, driverID string) (domain.Trip, error) { return svc.Arrive(ctx, id, driverID) },
		func(id, driverID string) (domain.Trip, error) { return svc.StartTrip(ctx, id, driverID) },
		func(id, driverID string) (domain.Trip, error) { return svc.CompleteTrip(ctx, id, driverID) },
	} {
		next, err := step(first.ID, driver.ID)
		if err != nil {
			t.Fatal(err)
		}
		first = next
	}
	if first.Status != domain.TripCompleted {
		t.Fatalf("status = %s", first.Status)
	}
	if _, err := svc.CancelTrip(ctx, first.ID, rider.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("cancel completed = %v", err)
	}

	second := mustTrip(t, svc, rider.ID, pickup, dropoff, 1)
	if second.DriverID != driver.ID || second.Status != domain.TripMatched {
		t.Fatalf("rematch = %+v", second)
	}
	cancelled, err := svc.CancelTrip(ctx, second.ID, rider.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != domain.TripCancelled {
		t.Fatalf("status = %s", cancelled.Status)
	}
	if _, err := svc.CancelTrip(ctx, second.ID, "stranger"); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("stranger cancel = %v", err)
	}
	third := mustTrip(t, svc, rider.ID, pickup, dropoff, 1)
	if third.DriverID != driver.ID {
		t.Fatalf("driver not released, trip = %+v", third)
	}
}

func TestOfflineDriverIsNotMatched(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)}
	svc, _ := newService(t, c)
	ctx := context.Background()
	rider := mustRider(t, svc, "Ava")
	driver := mustDriver(t, svc, "Sam")
	pickup := domain.Point{Lat: 37.77, Lng: -122.42}
	mustLoc(t, svc, driver.ID, pickup)
	if _, err := svc.SetDriverStatus(ctx, driver.ID, domain.DriverOffline); err != nil {
		t.Fatal(err)
	}
	trip := mustTrip(t, svc, rider.ID, pickup, domain.Point{Lat: 37.78, Lng: -122.41}, 1)
	if trip.Status != domain.TripRequested {
		t.Fatalf("status = %s", trip.Status)
	}
	if _, _, err := svc.IngestLocation(ctx, driver.ID, pickup); err != nil {
		t.Fatal(err)
	}
	matched, err := svc.MatchTrip(ctx, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if matched.Status != domain.TripMatched || matched.DriverID != driver.ID {
		t.Fatalf("rematch = %+v", matched)
	}
}

func TestEstimateAndValidation(t *testing.T) {
	c := &clock{t: time.Date(2026, 10, 15, 15, 0, 0, 0, time.UTC)}
	svc, _ := newService(t, c)
	ctx := context.Background()
	q, err := svc.Estimate(ctx, domain.Point{Lat: 37.77, Lng: -122.42}, domain.Point{Lat: 37.78, Lng: -122.41}, 1)
	if err != nil || q.AmountCents <= q.BaseCents {
		t.Fatalf("quote = %+v err %v", q, err)
	}
	if _, err := svc.Estimate(ctx, domain.Point{Lat: 91, Lng: 0}, domain.Point{Lat: 0, Lng: 0}, 1); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("bad point = %v", err)
	}
	if _, err := svc.RegisterRider(ctx, "  "); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("blank name = %v", err)
	}
	if _, err := svc.RequestTrip(ctx, "missing", domain.Point{Lat: 1, Lng: 2}, domain.Point{Lat: 1.1, Lng: 2.1}, 1); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing rider = %v", err)
	}
}

func mustRider(t *testing.T, svc *service.Service, name string) domain.Rider {
	t.Helper()
	rider, err := svc.RegisterRider(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return rider
}

func mustDriver(t *testing.T, svc *service.Service, name string) domain.Driver {
	t.Helper()
	driver, err := svc.RegisterDriver(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func mustLoc(t *testing.T, svc *service.Service, driverID string, p domain.Point) {
	t.Helper()
	if _, _, err := svc.IngestLocation(context.Background(), driverID, p); err != nil {
		t.Fatal(err)
	}
}

func mustTrip(t *testing.T, svc *service.Service, riderID string, pickup, dropoff domain.Point, surge float64) domain.Trip {
	t.Helper()
	trip, err := svc.RequestTrip(context.Background(), riderID, pickup, dropoff, surge)
	if err != nil {
		t.Fatal(err)
	}
	return trip
}

func eventTypes(pub *events.Memory) []string {
	evs := pub.Events()
	out := make([]string, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type
	}
	return out
}
