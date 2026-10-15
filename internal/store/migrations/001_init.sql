CREATE TABLE IF NOT EXISTS riders (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS drivers (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('offline', 'available', 'busy')),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS trips (
    id TEXT PRIMARY KEY,
    rider_id TEXT NOT NULL REFERENCES riders (id),
    driver_id TEXT REFERENCES drivers (id),
    status TEXT NOT NULL CHECK (status IN (
        'requested', 'matched', 'accepted', 'arrived', 'in_progress', 'completed', 'cancelled'
    )),
    pickup_lat DOUBLE PRECISION NOT NULL,
    pickup_lng DOUBLE PRECISION NOT NULL,
    dropoff_lat DOUBLE PRECISION NOT NULL,
    dropoff_lng DOUBLE PRECISION NOT NULL,
    fare_cents BIGINT NOT NULL,
    currency TEXT NOT NULL,
    distance_meters DOUBLE PRECISION NOT NULL,
    duration_seconds INTEGER NOT NULL,
    surge DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS trips_rider_id_idx ON trips (rider_id);

CREATE INDEX IF NOT EXISTS trips_driver_id_idx ON trips (driver_id);

CREATE INDEX IF NOT EXISTS trips_status_idx ON trips (status);
