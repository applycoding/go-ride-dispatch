# go-ride-dispatch

A small **demo** of a ride-hailing dispatch service written in Go. Drivers report their position, riders request trips, and the service assigns the nearest available driver. It is a portfolio project meant to show API design, a geospatial index, a state machine, and a clean storage/event boundary. It is not a production dispatch platform.

## What this is

- One process. There is no multi-node lock, no driver app, and no rider app.
- Matching uses a geohash grid plus haversine distance. It is not H3, not PostGIS, and not a road router. `distance_m` is straight-line distance.
- The default event bus is in memory. `KafkaAdapter` implements the same publisher port and is covered by tests with a fake producer. A real `segmentio/kafka-go` writer is wired when `KAFKA_BROKERS` is set. The compose broker profile is optional and was not required to run the tests.
- PostgreSQL is used when `DATABASE_URL` is set. With no database URL the process uses an in-memory store, which is also what the unit tests use.
- There is no authentication, payments, surge pricing, or ETA model.

## Architecture

```mermaid
flowchart LR
  drivers[Driver clients] -->|REST and WebSocket locations| api[HTTP API]
  riders[Rider clients] -->|POST /v1/trips| api
  api --> svc[Dispatch service]
  svc --> grid[Geohash prefix index]
  svc --> store[(PostgreSQL or in-memory store)]
  svc --> bus[Publisher]
  bus --> mem[In-memory bus]
  bus --> kafka[Kafka adapter]
  mem --> ws[WebSocket subscribers]
  api --> metrics[/metrics]
```

Trip lifecycle:

```mermaid
stateDiagram-v2
  [*] --> requested
  requested --> assigned
  requested --> cancelled
  assigned --> en-route
  assigned --> cancelled
  en-route --> completed
  en-route --> cancelled
  completed --> [*]
  cancelled --> [*]
```

`requested → assigned` is done inside one store operation so the trip and the driver (available → busy) change together. Completing or cancelling an assigned trip marks the driver available again. A busy driver cannot go offline until that happens.

## Matching

Each location update stores a precision-7 geohash (about 150 m cells) and indexes every prefix of that hash. A trip request builds a bounding box around the pickup, takes the geohash prefix shared by the box corners, loads available drivers in that prefix, and keeps the nearest one whose haversine distance is inside the radius. Sharing a prefix across the corners means a driver just across a cell border is still visible. Candidates outside the circle are dropped. If the box crosses the antimeridian or a very coarse cell boundary, the search scans every available driver and still applies the haversine limit.

If nobody is in range, the trip stays `requested` and the API returns 201. That is an empty match, not a transport failure.

## Run it

Go 1.22 or newer.

```bash
go run ./cmd/dispatch
```

The server listens on `:8080` and stores data in memory.

```bash
curl -s localhost:8080/healthz
curl -s -X POST localhost:8080/v1/drivers -d '{"name":"Ada"}' -H 'content-type: application/json'
# use the returned id as $ID
curl -s -X POST localhost:8080/v1/drivers/$ID/location -d '{"lat":37.775,"lng":-122.419}' -H 'content-type: application/json'
curl -s -X POST localhost:8080/v1/drivers/$ID/status -d '{"status":"available"}' -H 'content-type: application/json'
curl -s -X POST localhost:8080/v1/trips -H 'content-type: application/json' -d '{
  "rider_id":"rider-1",
  "pickup":{"lat":37.7749,"lng":-122.4194},
  "dropoff":{"lat":37.79,"lng":-122.41}
}'
```

Then `POST /v1/trips/{id}/en-route` and `POST /v1/trips/{id}/complete`.

Postgres via Compose (the database password is a demo default):

```bash
docker compose up --build
```

That sets `DATABASE_URL` for the `dispatch` service. Metrics are at `http://localhost:8080/metrics`.

To start the optional broker and point the service at it on the Compose network:

```bash
KAFKA_BROKERS=kafka:9092 docker compose --profile broker up --build
```

WebSocket fanout still uses the in-memory bus. Kafka is an additional sink. The adapter is what the tests exercise, with a fake producer; bring-up of the broker container is left to Compose.

## HTTP API

| Method | Path | Purpose |
| --- | --- | --- |
| GET | `/healthz` | Process is up |
| GET | `/readyz` | Store ping |
| GET | `/metrics` | Prometheus |
| POST | `/v1/drivers` | Register. Body: `{"name","id?"}` |
| GET | `/v1/drivers/{id}` | Fetch one driver |
| POST | `/v1/drivers/{id}/location` | Body: `{"lat","lng"}` |
| POST | `/v1/drivers/{id}/status` | Body: `{"status":"available"\|"offline"}` |
| GET | `/v1/drivers?lat&lng&radius_m&limit` | Nearest available drivers |
| GET | `/v1/drivers/{id}/stream` | WebSocket. Send `{"lat","lng"}` text frames |
| POST | `/v1/trips` | Body: `rider_id`, `pickup`, `dropoff`, optional `radius_m` |
| GET | `/v1/trips/{id}` | Fetch one trip |
| POST | `/v1/trips/{id}/en-route` | assigned → en-route |
| POST | `/v1/trips/{id}/complete` | en-route → completed |
| POST | `/v1/trips/{id}/cancel` | cancel a non-terminal trip |
| GET | `/v1/ws?types=trip.assigned,trip.completed` | WebSocket event stream |

Illegal transitions return 409. Unknown drivers and trips return 404.

## Events

The publisher port is `events.Publisher`. `MemoryBus` delivers to in-process subscribers and drops events when a subscriber buffer is full. `KafkaAdapter` JSON-encodes the same event and calls `Producer.Produce`. The Kafka message key is the trip id, then the driver id.

Event types: `trip.requested`, `trip.assigned`, `trip.en-route`, `trip.completed`, `trip.cancelled`, `driver.location`, `driver.status`.

## Load simulation

With the server already running:

```bash
go run ./cmd/sim -url http://localhost:8080 -drivers 25 -trips 40 -concurrency 8
```

The script registers drivers around a center point, keeps a WebSocket location stream open per driver, and requests trips. Assigned trips are moved to en-route and then completed so drivers become free again. It prints assignment counts and match-latency percentiles. Unmatched trips are a normal outcome when every nearby driver is busy.

## Tests and CI

```bash
go test -race ./...
golangci-lint run
```

`DATABASE_URL` opts in the PostgreSQL test. Without it, that test skips. GitHub Actions starts Postgres 16 and runs `go test -race ./...` plus golangci-lint.

## Layout

```
cmd/dispatch          HTTP server
cmd/sim               load generator
internal/geo          geohash, neighbors, haversine, bounding prefix
internal/trip         state machine and matching
internal/store        memory and Postgres
internal/events       in-memory bus and Kafka adapter
internal/kafka        kafka-go writer behind the producer port
internal/api          REST and WebSocket
internal/metrics      Prometheus registry
```

## Limits worth knowing

- Assignment is safe inside one process and inside one Postgres transaction. Two service replicas would race without a shared lock.
- The spatial index is only as selective as the common geohash prefix. A search that crosses a major cell boundary degrades to a full scan of available drivers, then a distance filter.
- WebSocket clients do not authenticate, and the origin check is disabled so the demo is easy to dial.
- Publishing is best-effort after the database write. A Kafka outage is logged and counted; it does not roll back the trip.
