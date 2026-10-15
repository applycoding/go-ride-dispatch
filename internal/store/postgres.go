package store

import (
	"context"
	_ "embed"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

//go:embed migrations/001_init.sql
var migrationSQL string

// Postgres persists drivers and trips in PostgreSQL.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres opens a pool and applies the embedded schema.
func NewPostgres(ctx context.Context, databaseURL string) (*Postgres, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 10
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	p := &Postgres{pool: pool}
	if err := p.migrate(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return p, nil
}

// Close releases the pool.
func (p *Postgres) Close() {
	p.pool.Close()
}

func (p *Postgres) migrate(ctx context.Context) error {
	_, err := p.pool.Exec(ctx, migrationSQL)
	return err
}

// Ping implements Store.
func (p *Postgres) Ping(ctx context.Context) error {
	return p.pool.Ping(ctx)
}

// UpsertDriver implements Store. A duplicate id is a conflict; this method does not update.
func (p *Postgres) UpsertDriver(ctx context.Context, driver domain.Driver) error {
	_, err := p.pool.Exec(ctx, `
		INSERT INTO drivers (id, name, status, lat, lng, geohash, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`, driver.ID, driver.Name, string(driver.Status), driver.Location.Lat, driver.Location.Lng, driver.Geohash, driver.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

// GetDriver implements Store.
func (p *Postgres) GetDriver(ctx context.Context, id string) (domain.Driver, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, name, status, lat, lng, geohash, updated_at
		FROM drivers WHERE id = $1
	`, id)
	return scanDriver(row)
}

// UpdateDriverLocation implements Store.
func (p *Postgres) UpdateDriverLocation(ctx context.Context, id string, point domain.Point, geohash string, at time.Time) (domain.Driver, error) {
	row := p.pool.QueryRow(ctx, `
		UPDATE drivers
		SET lat = $2, lng = $3, geohash = $4, updated_at = $5
		WHERE id = $1
		RETURNING id, name, status, lat, lng, geohash, updated_at
	`, id, point.Lat, point.Lng, geohash, at)
	return scanDriver(row)
}

// SetDriverStatus implements Store.
func (p *Postgres) SetDriverStatus(ctx context.Context, id string, status domain.DriverStatus, at time.Time) (domain.Driver, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Driver{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current string
	err = tx.QueryRow(ctx, `SELECT status FROM drivers WHERE id = $1 FOR UPDATE`, id).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Driver{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Driver{}, err
	}
	if domain.DriverStatus(current) == domain.DriverBusy && status != domain.DriverBusy {
		return domain.Driver{}, domain.ErrDriverBusy
	}
	row := tx.QueryRow(ctx, `
		UPDATE drivers SET status = $2, updated_at = $3 WHERE id = $1
		RETURNING id, name, status, lat, lng, geohash, updated_at
	`, id, string(status), at)
	driver, err := scanDriver(row)
	if err != nil {
		return domain.Driver{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Driver{}, err
	}
	return driver, nil
}

// ListAvailableInCells implements Store.
func (p *Postgres) ListAvailableInCells(ctx context.Context, prefix string) ([]domain.Driver, error) {
	pattern := prefix + "%"
	rows, err := p.pool.Query(ctx, `
		SELECT id, name, status, lat, lng, geohash, updated_at
		FROM drivers
		WHERE status = $1 AND geohash LIKE $2
	`, string(domain.DriverAvailable), pattern)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []domain.Driver
	for rows.Next() {
		driver, err := scanDriver(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, driver)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateTrip implements Store.
func (p *Postgres) CreateTrip(ctx context.Context, trip domain.Trip) error {
	var driverID *string
	if trip.DriverID != "" {
		driverID = &trip.DriverID
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO trips (
			id, rider_id, driver_id, status,
			pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
			distance_m, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, trip.ID, trip.RiderID, driverID, string(trip.Status),
		trip.Pickup.Lat, trip.Pickup.Lng, trip.Dropoff.Lat, trip.Dropoff.Lng,
		trip.DistanceM, trip.CreatedAt, trip.UpdatedAt)
	if isUniqueViolation(err) {
		return domain.ErrConflict
	}
	return err
}

// GetTrip implements Store.
func (p *Postgres) GetTrip(ctx context.Context, id string) (domain.Trip, error) {
	row := p.pool.QueryRow(ctx, `
		SELECT id, rider_id, driver_id, status,
		       pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
		       distance_m, created_at, updated_at
		FROM trips WHERE id = $1
	`, id)
	return scanTrip(row)
}

// Assign implements Store.
func (p *Postgres) Assign(ctx context.Context, tripID, driverID string, at time.Time) (domain.Trip, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Trip{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM trips WHERE id = $1 FOR UPDATE`, tripID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Trip{}, err
	}
	if domain.TripStatus(status) != domain.TripRequested {
		return domain.Trip{}, domain.ErrInvalidTransition
	}

	var driverStatus string
	err = tx.QueryRow(ctx, `SELECT status FROM drivers WHERE id = $1 FOR UPDATE`, driverID).Scan(&driverStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Trip{}, err
	}
	if domain.DriverStatus(driverStatus) != domain.DriverAvailable {
		return domain.Trip{}, domain.ErrDriverUnavailable
	}

	if _, err := tx.Exec(ctx, `UPDATE drivers SET status = $2, updated_at = $3 WHERE id = $1`, driverID, string(domain.DriverBusy), at); err != nil {
		return domain.Trip{}, err
	}
	row := tx.QueryRow(ctx, `
		UPDATE trips SET status = $2, driver_id = $3, updated_at = $4 WHERE id = $1
		RETURNING id, rider_id, driver_id, status,
		          pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
		          distance_m, created_at, updated_at
	`, tripID, string(domain.TripAssigned), driverID, at)
	trip, err := scanTrip(row)
	if err != nil {
		return domain.Trip{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Trip{}, err
	}
	return trip, nil
}

// Transition implements Store.
func (p *Postgres) Transition(ctx context.Context, tripID string, from, to domain.TripStatus, at time.Time) (domain.Trip, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return domain.Trip{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var status string
	var driverID *string
	err = tx.QueryRow(ctx, `SELECT status, driver_id FROM trips WHERE id = $1 FOR UPDATE`, tripID).Scan(&status, &driverID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Trip{}, err
	}
	if domain.TripStatus(status) != from {
		return domain.Trip{}, domain.ErrInvalidTransition
	}
	if driverID != nil && *driverID != "" && to.Terminal() {
		if _, err := tx.Exec(ctx, `
			UPDATE drivers SET status = $2, updated_at = $3
			WHERE id = $1 AND status = $4
		`, *driverID, string(domain.DriverAvailable), at, string(domain.DriverBusy)); err != nil {
			return domain.Trip{}, err
		}
	}
	row := tx.QueryRow(ctx, `
		UPDATE trips SET status = $2, updated_at = $3 WHERE id = $1
		RETURNING id, rider_id, driver_id, status,
		          pickup_lat, pickup_lng, dropoff_lat, dropoff_lng,
		          distance_m, created_at, updated_at
	`, tripID, string(to), at)
	trip, err := scanTrip(row)
	if err != nil {
		return domain.Trip{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Trip{}, err
	}
	return trip, nil
}

type scannable interface {
	Scan(dest ...any) error
}

func scanDriver(row scannable) (domain.Driver, error) {
	var driver domain.Driver
	var status string
	err := row.Scan(&driver.ID, &driver.Name, &status, &driver.Location.Lat, &driver.Location.Lng, &driver.Geohash, &driver.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Driver{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Driver{}, err
	}
	driver.Status = domain.DriverStatus(status)
	return driver, nil
}

func scanTrip(row scannable) (domain.Trip, error) {
	var trip domain.Trip
	var driverID *string
	var status string
	err := row.Scan(
		&trip.ID, &trip.RiderID, &driverID, &status,
		&trip.Pickup.Lat, &trip.Pickup.Lng, &trip.Dropoff.Lat, &trip.Dropoff.Lng,
		&trip.DistanceM, &trip.CreatedAt, &trip.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Trip{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Trip{}, err
	}
	if driverID != nil {
		trip.DriverID = *driverID
	}
	trip.Status = domain.TripStatus(status)
	return trip, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
