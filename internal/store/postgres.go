package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

//go:embed migrations/001_init.sql
var migrationFS embed.FS

// Postgres stores riders, drivers, and trips in PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres wraps a connection pool. The caller owns the pool lifetime.
func NewPostgres(pool *pgxpool.Pool) *Postgres {
	return &Postgres{pool: pool}
}

// Migrate applies the embedded idempotent schema.
func (p *Postgres) Migrate(ctx context.Context) error {
	raw, err := migrationFS.ReadFile("migrations/001_init.sql")
	if err != nil {
		return fmt.Errorf("read migration: %w", err)
	}
	for _, stmt := range splitSQL(string(raw)) {
		if _, err := p.pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Ping checks the pool.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// CreateRider inserts a rider.
func (p *Postgres) CreateRider(ctx context.Context, rider domain.Rider) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO riders (id, name, created_at) VALUES ($1, $2, $3)`,
		rider.ID, rider.Name, rider.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert rider: %w", err)
	}
	return nil
}

// GetRider fetches a rider by id.
func (p *Postgres) GetRider(ctx context.Context, id string) (domain.Rider, error) {
	var rider domain.Rider
	err := p.pool.QueryRow(ctx,
		`SELECT id, name, created_at FROM riders WHERE id = $1`, id,
	).Scan(&rider.ID, &rider.Name, &rider.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Rider{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Rider{}, fmt.Errorf("select rider: %w", err)
	}
	return rider, nil
}

// CreateDriver inserts a driver.
func (p *Postgres) CreateDriver(ctx context.Context, driver domain.Driver) error {
	_, err := p.pool.Exec(ctx,
		`INSERT INTO drivers (id, name, status, created_at) VALUES ($1, $2, $3, $4)`,
		driver.ID, driver.Name, driver.Status, driver.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert driver: %w", err)
	}
	return nil
}

// GetDriver fetches a driver by id.
func (p *Postgres) GetDriver(ctx context.Context, id string) (domain.Driver, error) {
	var driver domain.Driver
	err := p.pool.QueryRow(ctx,
		`SELECT id, name, status, created_at FROM drivers WHERE id = $1`, id,
	).Scan(&driver.ID, &driver.Name, &driver.Status, &driver.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Driver{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Driver{}, fmt.Errorf("select driver: %w", err)
	}
	return driver, nil
}

// SetDriverStatus updates status when the current value is from.
func (p *Postgres) SetDriverStatus(ctx context.Context, id string, from, to domain.DriverStatus) error {
	tag, err := p.pool.Exec(ctx,
		`UPDATE drivers SET status = $2 WHERE id = $1 AND status = $3`,
		id, string(to), string(from),
	)
	if err != nil {
		return fmt.Errorf("update driver status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := p.GetDriver(ctx, id); err != nil {
			return err
		}
		return domain.ErrConflict
	}
	return nil
}

// CreateTrip inserts a trip. An empty driver id is stored as NULL.
func (p *Postgres) CreateTrip(ctx context.Context, trip domain.Trip) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO trips (
			id, rider_id, driver_id, status,
			pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
			fare_cents, currency, distance_meters, duration_seconds, surge,
			created_at, updated_at
		) VALUES (
			$1, $2, NULLIF($3, ''), $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12, $13,
			$14, $15
		)`,
		trip.ID, trip.RiderID, trip.DriverID, string(trip.Status),
		trip.Pickup.Lat, trip.Pickup.Lng, trip.Dropoff.Lat, trip.Dropoff.Lng,
		trip.FareCents, trip.Currency, trip.DistanceMeters, trip.DurationSeconds, trip.Surge,
		trip.CreatedAt, trip.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insert trip: %w", err)
	}
	return nil
}

// GetTrip fetches a trip by id.
func (p *Postgres) GetTrip(ctx context.Context, id string) (domain.Trip, error) {
	return scanTrip(p.pool.QueryRow(ctx, tripSelect+` WHERE id = $1`, id))
}

// AssignDriver moves a requested trip to matched and marks the driver busy.
func (p *Postgres) AssignDriver(ctx context.Context, tripID, driverID string, at time.Time) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin assign: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE trips
		SET driver_id = $2, status = $3, updated_at = $4
		WHERE id = $1 AND status = $5 AND driver_id IS NULL`,
		tripID, driverID, string(domain.TripMatched), at, string(domain.TripRequested),
	)
	if err != nil {
		return fmt.Errorf("assign trip: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := scanTrip(tx.QueryRow(ctx, tripSelect+` WHERE id = $1`, tripID)); err != nil {
			return err
		}
		return domain.ErrConflict
	}
	tag, err = tx.Exec(ctx,
		`UPDATE drivers SET status = $2 WHERE id = $1 AND status = $3`,
		driverID, string(domain.DriverBusy), string(domain.DriverAvailable),
	)
	if err != nil {
		return fmt.Errorf("reserve driver: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var name string
		err := tx.QueryRow(ctx, `SELECT name FROM drivers WHERE id = $1`, driverID).Scan(&name)
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("select driver: %w", err)
		}
		return domain.ErrDriverUnavailable
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit assign: %w", err)
	}
	return nil
}

// UpdateTrip applies a compare-and-swap transition and optionally releases a driver.
func (p *Postgres) UpdateTrip(ctx context.Context, update TripUpdate) error {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin trip update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `
		UPDATE trips
		SET status = $2, updated_at = $3
		WHERE id = $1 AND status = $4 AND ($5 = '' OR driver_id = $5)`,
		update.TripID, string(update.To), update.At, string(update.From), update.ExpectedDriver,
	)
	if err != nil {
		return fmt.Errorf("update trip: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := scanTrip(tx.QueryRow(ctx, tripSelect+` WHERE id = $1`, update.TripID)); err != nil {
			return err
		}
		return domain.ErrConflict
	}
	if update.ReleaseDriver != "" {
		tag, err = tx.Exec(ctx,
			`UPDATE drivers SET status = $2 WHERE id = $1 AND status = $3`,
			update.ReleaseDriver, string(domain.DriverAvailable), string(domain.DriverBusy),
		)
		if err != nil {
			return fmt.Errorf("release driver: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return domain.ErrConflict
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit trip update: %w", err)
	}
	return nil
}

const tripSelect = `
	SELECT id, rider_id, driver_id, status,
		pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
		fare_cents, currency, distance_meters, duration_seconds, surge,
		created_at, updated_at
	FROM trips`

func scanTrip(row pgx.Row) (domain.Trip, error) {
	var trip domain.Trip
	var driverID *string
	var status string
	err := row.Scan(
		&trip.ID, &trip.RiderID, &driverID, &status,
		&trip.Pickup.Lat, &trip.Pickup.Lng, &trip.Dropoff.Lat, &trip.Dropoff.Lng,
		&trip.FareCents, &trip.Currency, &trip.DistanceMeters, &trip.DurationSeconds, &trip.Surge,
		&trip.CreatedAt, &trip.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Trip{}, fmt.Errorf("select trip: %w", err)
	}
	trip.Status = domain.TripStatus(status)
	if driverID != nil {
		trip.DriverID = *driverID
	}
	return trip, nil
}

func splitSQL(sql string) []string {
	parts := strings.Split(sql, ";")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		stmt := strings.TrimSpace(part)
		if stmt != "" {
			out = append(out, stmt)
		}
	}
	return out
}
