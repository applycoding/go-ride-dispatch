package location

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

func TestRedisRoundTrip(t *testing.T) {
	mr, err := miniredis.Run()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(mr.Close)
	client := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedis(client)
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, 1_700_000_000_123_000_000).UTC()
	loc := domain.DriverLocation{
		DriverID:  "driver-1",
		Point:     domain.Point{Lat: 37.77, Lng: -122.42},
		UpdatedAt: at,
	}
	if err := store.Put(ctx, loc); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, loc.DriverID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Point != loc.Point || !got.UpdatedAt.Equal(at) {
		t.Fatalf("got = %+v", got)
	}
	all, err := store.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].DriverID != loc.DriverID {
		t.Fatalf("list = %+v", all)
	}
	if _, err := store.Get(ctx, "missing"); err == nil {
		t.Fatal("expected not found")
	}
}

func TestMemoryRoundTrip(t *testing.T) {
	store := NewMemory()
	ctx := context.Background()
	loc := domain.DriverLocation{DriverID: "d", Point: domain.Point{Lat: 1, Lng: 2}, UpdatedAt: time.Unix(3, 0).UTC()}
	if err := store.Put(ctx, loc); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, "d")
	if err != nil {
		t.Fatal(err)
	}
	if got != loc {
		t.Fatalf("got = %+v", got)
	}
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
}
