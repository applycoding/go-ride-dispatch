package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

func TestMemoryAssignIsExclusive(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	driver := domain.Driver{ID: "d1", Name: "ada", Status: domain.DriverAvailable, Geohash: "9q8yyxx", UpdatedAt: now}
	if err := st.UpsertDriver(ctx, driver); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"t1", "t2"} {
		trip := domain.Trip{ID: id, RiderID: "r", Status: domain.TripRequested, CreatedAt: now, UpdatedAt: now}
		if err := st.CreateTrip(ctx, trip); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, id := range []string{"t1", "t2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_, err := st.Assign(ctx, id, "d1", now)
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)

	var ok, denied int
	for err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, domain.ErrDriverUnavailable):
			denied++
		default:
			t.Fatal(err)
		}
	}
	if ok != 1 || denied != 1 {
		t.Fatalf("ok=%d denied=%d", ok, denied)
	}
}

func TestMemoryListUsesGeohashPrefix(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	drivers := []domain.Driver{
		{ID: "near", Name: "near", Status: domain.DriverAvailable, Geohash: "9q8yyab", UpdatedAt: now},
		{ID: "other", Name: "other", Status: domain.DriverAvailable, Geohash: "9q9yyyy", UpdatedAt: now},
		{ID: "busy", Name: "busy", Status: domain.DriverBusy, Geohash: "9q8yyac", UpdatedAt: now},
	}
	for _, driver := range drivers {
		if err := st.UpsertDriver(ctx, driver); err != nil {
			t.Fatal(err)
		}
	}
	got, err := st.ListAvailableInCells(ctx, "9q8yy")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "near" {
		t.Fatalf("got %+v", got)
	}
}

func TestMemoryTransitionMismatch(t *testing.T) {
	t.Parallel()
	st := store.NewMemory()
	ctx := context.Background()
	now := time.Now().UTC()
	trip := domain.Trip{ID: "t1", RiderID: "r", Status: domain.TripRequested, CreatedAt: now, UpdatedAt: now}
	if err := st.CreateTrip(ctx, trip); err != nil {
		t.Fatal(err)
	}
	_, err := st.Transition(ctx, "t1", domain.TripAssigned, domain.TripEnRoute, now)
	if !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("err = %v", err)
	}
	_, err = st.GetTrip(ctx, "missing")
	if !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
