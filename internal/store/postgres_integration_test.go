//go:build integration

package store_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

func TestPostgresRoundTrip(t *testing.T) {
	repo := openPostgres(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	rider := domain.Rider{ID: uuid.NewString(), Name: "Ava", CreatedAt: now}
	driver := domain.Driver{ID: uuid.NewString(), Name: "Sam", Status: domain.DriverAvailable, CreatedAt: now}
	if err := repo.CreateRider(ctx, rider); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateDriver(ctx, driver); err != nil {
		t.Fatal(err)
	}
	trip := domain.Trip{
		ID: rider.ID + "-trip", RiderID: rider.ID, Status: domain.TripRequested,
		Pickup: domain.Point{Lat: 37.77, Lng: -122.42}, Dropoff: domain.Point{Lat: 37.78, Lng: -122.41},
		FareCents: 500, Currency: "USD", DistanceMeters: 1200, DurationSeconds: 180, Surge: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := repo.CreateTrip(ctx, trip); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.GetTrip(ctx, "missing"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("missing trip error = %v", err)
	}
	if err := repo.AssignDriver(ctx, trip.ID, driver.ID, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignDriver(ctx, trip.ID, driver.ID, now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second assign = %v", err)
	}
	got, err := repo.GetTrip(ctx, trip.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TripMatched || got.DriverID != driver.ID || got.FareCents != 500 {
		t.Fatalf("trip = %+v", got)
	}
	if err := repo.UpdateTrip(ctx, store.TripUpdate{
		TripID: trip.ID, From: domain.TripMatched, To: domain.TripCancelled,
		ReleaseDriver: driver.ID, At: now.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	driverGot, err := repo.GetDriver(ctx, driver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if driverGot.Status != domain.DriverAvailable {
		t.Fatalf("driver status = %s", driverGot.Status)
	}
}

func openPostgres(t *testing.T) *store.Postgres {
	t.Helper()
	url := os.Getenv("DATABASE_URL")
	if url == "" {
		t.Fatal("DATABASE_URL is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	repo := store.NewPostgres(pool)
	if err := repo.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	return repo
}
