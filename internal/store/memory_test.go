package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

func TestMemoryAssignIsCompareAndSwap(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemory()
	now := time.Unix(100, 0).UTC()
	rider := domain.Rider{ID: "r", Name: "Ava", CreatedAt: now}
	driver := domain.Driver{ID: "d", Name: "Sam", Status: domain.DriverAvailable, CreatedAt: now}
	trip := domain.Trip{ID: "t", RiderID: "r", Status: domain.TripRequested, CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateRider(ctx, rider); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateDriver(ctx, driver); err != nil {
		t.Fatal(err)
	}
	if err := repo.CreateTrip(ctx, trip); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignDriver(ctx, "t", "d", now); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignDriver(ctx, "t", "d", now); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("second assign = %v", err)
	}
	got, err := repo.GetDriver(ctx, "d")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.DriverBusy {
		t.Fatalf("status = %s", got.Status)
	}
	busy := domain.Driver{ID: "d2", Name: "Kim", Status: domain.DriverBusy, CreatedAt: now}
	if err := repo.CreateDriver(ctx, busy); err != nil {
		t.Fatal(err)
	}
	other := trip
	other.ID = "t2"
	if err := repo.CreateTrip(ctx, other); err != nil {
		t.Fatal(err)
	}
	if err := repo.AssignDriver(ctx, "t2", "d2", now); !errors.Is(err, domain.ErrDriverUnavailable) {
		t.Fatalf("busy assign = %v", err)
	}
}

func TestMemoryUpdateReleasesDriver(t *testing.T) {
	ctx := context.Background()
	repo := store.NewMemory()
	now := time.Unix(100, 0).UTC()
	if err := repo.CreateDriver(ctx, domain.Driver{ID: "d", Name: "Sam", Status: domain.DriverBusy, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	trip := domain.Trip{ID: "t", RiderID: "r", DriverID: "d", Status: domain.TripMatched, CreatedAt: now, UpdatedAt: now}
	if err := repo.CreateTrip(ctx, trip); err != nil {
		t.Fatal(err)
	}
	err := repo.UpdateTrip(ctx, store.TripUpdate{
		TripID:        "t",
		From:          domain.TripCancelled,
		To:            domain.TripCancelled,
		ReleaseDriver: "d",
		At:            now,
	})
	if !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("bad from-status = %v", err)
	}
	if err := repo.UpdateTrip(ctx, store.TripUpdate{
		TripID:        "t",
		From:          domain.TripMatched,
		To:            domain.TripCancelled,
		ReleaseDriver: "d",
		At:            now.Add(time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	driver, err := repo.GetDriver(ctx, "d")
	if err != nil {
		t.Fatal(err)
	}
	if driver.Status != domain.DriverAvailable {
		t.Fatalf("status = %s", driver.Status)
	}
}
