// Package location stores the latest position reported by each driver.
package location

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

const (
	redisLiveSet = "dispatch:drivers:live"
)

func redisLocKey(id string) string { return "dispatch:drivers:loc:" + id }

// Store is the live location record. The spatial index is rebuilt from it.
type Store interface {
	Put(ctx context.Context, loc domain.DriverLocation) error
	Get(ctx context.Context, driverID string) (domain.DriverLocation, error)
	List(ctx context.Context) ([]domain.DriverLocation, error)
	Ping(ctx context.Context) error
}

// Memory is a process-local location store.
type Memory struct {
	mu   sync.Mutex
	locs map[string]domain.DriverLocation
}

// NewMemory returns an empty location store.
func NewMemory() *Memory {
	return &Memory{locs: make(map[string]domain.DriverLocation)}
}

// Put stores the latest location for a driver.
func (m *Memory) Put(_ context.Context, loc domain.DriverLocation) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.locs[loc.DriverID] = loc
	return nil
}

// Get returns the latest location.
func (m *Memory) Get(_ context.Context, driverID string) (domain.DriverLocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	loc, ok := m.locs[driverID]
	if !ok {
		return domain.DriverLocation{}, domain.ErrNotFound
	}
	return loc, nil
}

// List returns every stored location.
func (m *Memory) List(context.Context) ([]domain.DriverLocation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.DriverLocation, 0, len(m.locs))
	for _, loc := range m.locs {
		out = append(out, loc)
	}
	return out, nil
}

// Ping always succeeds.
func (m *Memory) Ping(context.Context) error { return nil }

// Redis stores live locations in Redis hashes plus a set of driver ids.
type Redis struct {
	rdb *redis.Client
}

// NewRedis wraps a Redis client. The caller owns the client lifetime.
func NewRedis(rdb *redis.Client) *Redis {
	return &Redis{rdb: rdb}
}

// Put writes the location hash and adds the driver to the live set.
func (s *Redis) Put(ctx context.Context, loc domain.DriverLocation) error {
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, redisLocKey(loc.DriverID),
		"lat", strconv.FormatFloat(loc.Point.Lat, 'f', -1, 64),
		"lng", strconv.FormatFloat(loc.Point.Lng, 'f', -1, 64),
		"updated_unix_nano", strconv.FormatInt(loc.UpdatedAt.UnixNano(), 10),
	)
	pipe.SAdd(ctx, redisLiveSet, loc.DriverID)
	if _, err := pipe.Exec(ctx); err != nil {
		return err
	}
	return nil
}

// Get reads one driver location.
func (s *Redis) Get(ctx context.Context, driverID string) (domain.DriverLocation, error) {
	fields, err := s.rdb.HGetAll(ctx, redisLocKey(driverID)).Result()
	if err != nil {
		return domain.DriverLocation{}, err
	}
	return parseLoc(driverID, fields)
}

// List reads every live driver. Missing hashes are skipped.
func (s *Redis) List(ctx context.Context) ([]domain.DriverLocation, error) {
	ids, err := s.rdb.SMembers(ctx, redisLiveSet).Result()
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}
	pipe := s.rdb.Pipeline()
	cmds := make([]*redis.MapStringStringCmd, len(ids))
	for i, id := range ids {
		cmds[i] = pipe.HGetAll(ctx, redisLocKey(id))
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return nil, err
	}
	out := make([]domain.DriverLocation, 0, len(ids))
	for i, cmd := range cmds {
		loc, err := parseLoc(ids[i], cmd.Val())
		if errors.Is(err, domain.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, loc)
	}
	return out, nil
}

// Ping checks the Redis connection.
func (s *Redis) Ping(ctx context.Context) error {
	return s.rdb.Ping(ctx).Err()
}

func parseLoc(id string, fields map[string]string) (domain.DriverLocation, error) {
	if len(fields) == 0 {
		return domain.DriverLocation{}, domain.ErrNotFound
	}
	lat, err := strconv.ParseFloat(fields["lat"], 64)
	if err != nil {
		return domain.DriverLocation{}, err
	}
	lng, err := strconv.ParseFloat(fields["lng"], 64)
	if err != nil {
		return domain.DriverLocation{}, err
	}
	nanos, err := strconv.ParseInt(fields["updated_unix_nano"], 10, 64)
	if err != nil {
		return domain.DriverLocation{}, err
	}
	return domain.DriverLocation{
		DriverID:  id,
		Point:     domain.Point{Lat: lat, Lng: lng},
		UpdatedAt: time.Unix(0, nanos).UTC(),
	}, nil
}
