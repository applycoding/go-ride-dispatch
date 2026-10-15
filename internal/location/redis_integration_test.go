//go:build integration

package location

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

func TestRedisServerRoundTrip(t *testing.T) {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Fatal("REDIS_ADDR is required")
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })
	store := NewRedis(client)
	ctx := context.Background()
	if err := store.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	t.Cleanup(func() {
		_ = client.Del(context.Background(), redisLocKey(id)).Err()
		_ = client.SRem(context.Background(), redisLiveSet, id).Err()
	})
	at := time.Now().UTC().Truncate(time.Microsecond)
	loc := domain.DriverLocation{DriverID: id, Point: domain.Point{Lat: 40.7, Lng: -74.0}, UpdatedAt: at}
	if err := store.Put(ctx, loc); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Point != loc.Point || !got.UpdatedAt.Equal(at) {
		t.Fatalf("got = %+v", got)
	}
}
