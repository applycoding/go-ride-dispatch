package store_test

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/id"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

var (
	pgOnce  sync.Once
	pgStore *store.Postgres
	pgErr   error
)

func postgresStore(t *testing.T) *store.Postgres {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Skip("DATABASE_URL not set")
	}
	pgOnce.Do(func() {
		pgStore, pgErr = store.NewPostgres(context.Background(), url)
	})
	if pgErr != nil {
		t.Fatal(pgErr)
	}
	return pgStore
}

func TestPostgresLifecycle(t *testing.T) {
	st := postgresStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	driverID := id.New()
	tripID := id.New()

	driver := domain.Driver{
		ID:        driverID,
		Name:      "ada",
		Status:    domain.DriverAvailable,
		Location:  domain.Point{Lat: 37.77, Lng: -122.42},
		Geohash:   "9q8yyab",
		UpdatedAt: now,
	}
	if err := st.UpsertDriver(ctx, driver); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertDriver(ctx, driver); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("duplicate err = %v", err)
	}

	moved, err := st.UpdateDriverLocation(ctx, driverID, domain.Point{Lat: 37.78, Lng: -122.41}, "9q8yyac", now)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Geohash != "9q8yyac" {
		t.Fatalf("geohash = %s", moved.Geohash)
	}

	trip := domain.Trip{
		ID:        tripID,
		RiderID:   "rider-pg",
		Status:    domain.TripRequested,
		Pickup:    domain.Point{Lat: 37.77, Lng: -122.42},
		Dropoff:   domain.Point{Lat: 37.80, Lng: -122.40},
		DistanceM: 1000,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := st.CreateTrip(ctx, trip); err != nil {
		t.Fatal(err)
	}
	found, err := st.ListAvailableInCells(ctx, "9q8yy")
	if err != nil {
		t.Fatal(err)
	}
	if !containsDriver(found, driverID) {
		t.Fatalf("driver missing from %+v", found)
	}

	assigned, err := st.Assign(ctx, tripID, driverID, now)
	if err != nil {
		t.Fatal(err)
	}
	if assigned.Status != domain.TripAssigned || assigned.DriverID != driverID {
		t.Fatalf("assigned = %+v", assigned)
	}
	busy, err := st.GetDriver(ctx, driverID)
	if err != nil {
		t.Fatal(err)
	}
	if busy.Status != domain.DriverBusy {
		t.Fatalf("status = %s", busy.Status)
	}
	if _, err := st.SetDriverStatus(ctx, driverID, domain.DriverOffline, now); !errors.Is(err, domain.ErrDriverBusy) {
		t.Fatalf("offline while busy err = %v", err)
	}

	enRoute, err := st.Transition(ctx, tripID, domain.TripAssigned, domain.TripEnRoute, now)
	if err != nil {
		t.Fatal(err)
	}
	if enRoute.Status != domain.TripEnRoute {
		t.Fatalf("status = %s", enRoute.Status)
	}
	done, err := st.Transition(ctx, tripID, domain.TripEnRoute, domain.TripCompleted, now)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.TripCompleted {
		t.Fatalf("status = %s", done.Status)
	}
	freed, err := st.GetDriver(ctx, driverID)
	if err != nil {
		t.Fatal(err)
	}
	if freed.Status != domain.DriverAvailable {
		t.Fatalf("status after complete = %s", freed.Status)
	}
	if err := st.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}

func containsDriver(drivers []domain.Driver, id string) bool {
	for _, driver := range drivers {
		if driver.ID == id {
			return true
		}
	}
	return false
}
