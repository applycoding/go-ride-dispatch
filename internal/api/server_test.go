package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/applycoding/go-ride-dispatch/internal/api"
	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/fare"
	"github.com/applycoding/go-ride-dispatch/internal/location"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/service"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

func TestHealthAndValidation(t *testing.T) {
	srv := newAPI(t, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	mustStatus(t, ts, http.MethodGet, "/healthz", nil, http.StatusOK)
	mustStatus(t, ts, http.MethodGet, "/readyz", nil, http.StatusOK)
	mustStatus(t, ts, http.MethodPost, "/v1/riders", map[string]any{"name": " "}, http.StatusBadRequest)
	mustStatus(t, ts, http.MethodPost, "/v1/riders", map[string]any{"name": "Ava", "extra": true}, http.StatusBadRequest)
	mustStatus(t, ts, http.MethodPost, "/v1/trips", map[string]any{
		"rider_id": "missing",
		"pickup":   map[string]float64{"lat": 37.77, "lng": -122.42},
		"dropoff":  map[string]float64{"lat": 37.78, "lng": -122.41},
	}, http.StatusNotFound)
}

func TestTripLifecycleHTTP(t *testing.T) {
	srv := newAPI(t, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	rider := postJSON[domain.Rider](t, ts, "/v1/riders", map[string]any{"name": "Ava"}, http.StatusCreated)
	driver := postJSON[domain.Driver](t, ts, "/v1/drivers", map[string]any{"name": "Sam"}, http.StatusCreated)
	if driver.Status != domain.DriverOffline {
		t.Fatalf("status = %s", driver.Status)
	}
	loc := postJSON[map[string]any](t, ts, "/v1/drivers/"+driver.ID+"/location", map[string]any{
		"lat": 37.7749, "lng": -122.4194,
	}, http.StatusOK)
	if loc["dispatchable"] != true || loc["status"] != string(domain.DriverAvailable) {
		t.Fatalf("location = %#v", loc)
	}
	quote := postJSON[fare.Quote](t, ts, "/v1/estimates", map[string]any{
		"pickup":  map[string]float64{"lat": 37.7749, "lng": -122.4194},
		"dropoff": map[string]float64{"lat": 37.7849, "lng": -122.4094},
	}, http.StatusOK)
	if quote.AmountCents <= 0 || quote.Currency != "USD" || quote.Surge != 1 {
		t.Fatalf("quote = %+v", quote)
	}
	trip := postJSON[domain.Trip](t, ts, "/v1/trips", map[string]any{
		"rider_id": rider.ID,
		"pickup":   map[string]float64{"lat": 37.7749, "lng": -122.4194},
		"dropoff":  map[string]float64{"lat": 37.7849, "lng": -122.4094},
	}, http.StatusCreated)
	if trip.Status != domain.TripMatched || trip.DriverID != driver.ID || trip.FareCents != quote.AmountCents {
		t.Fatalf("trip = %+v", trip)
	}
	for _, path := range []string{"accept", "arrive", "start", "complete"} {
		trip = postJSON[domain.Trip](t, ts, "/v1/trips/"+trip.ID+"/"+path, map[string]any{
			"driver_id": driver.ID,
		}, http.StatusOK)
	}
	if trip.Status != domain.TripCompleted {
		t.Fatalf("status = %s", trip.Status)
	}
	got := getJSON[domain.Trip](t, ts, "/v1/trips/"+trip.ID)
	if got.Status != domain.TripCompleted {
		t.Fatalf("get = %+v", got)
	}
	body := do(t, ts, http.MethodGet, "/metrics", nil)
	defer func() {
		if err := body.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if body.StatusCode != http.StatusOK {
		t.Fatalf("metrics = %d", body.StatusCode)
	}
	payload, err := io.ReadAll(body.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), "dispatch_http_requests_total") {
		t.Fatal("metrics missing request counter")
	}
	if !strings.Contains(string(payload), "dispatch_trip_transitions_total") {
		t.Fatal("metrics missing transition counter")
	}
}

func TestWebSocketReceivesMatch(t *testing.T) {
	srv := newAPI(t, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	rider := postJSON[domain.Rider](t, ts, "/v1/riders", map[string]any{"name": "Ava"}, http.StatusCreated)
	driver := postJSON[domain.Driver](t, ts, "/v1/drivers", map[string]any{"name": "Sam"}, http.StatusCreated)
	postJSON[map[string]any](t, ts, "/v1/drivers/"+driver.ID+"/location", map[string]any{
		"lat": 37.77, "lng": -122.42,
	}, http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/ws?role=rider&actor_id=" + rider.ID
	conn, resp, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(websocket.StatusNormalClosure, "") })
	if resp != nil && resp.Body != nil {
		if err := resp.Body.Close(); err != nil {
			t.Fatal(err)
		}
	}
	hello := readWS(t, ctx, conn)
	if hello.Type != "hello" || hello.ID != rider.ID {
		t.Fatalf("hello = %+v", hello)
	}

	trip := postJSON[domain.Trip](t, ts, "/v1/trips", map[string]any{
		"rider_id": rider.ID,
		"pickup":   map[string]float64{"lat": 37.77, "lng": -122.42},
		"dropoff":  map[string]float64{"lat": 37.78, "lng": -122.41},
	}, http.StatusCreated)
	if trip.Status != domain.TripMatched {
		t.Fatalf("trip = %+v", trip)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		msg := readWS(t, ctx, conn)
		if msg.Type == "trip.matched" && msg.Trip.ID == trip.ID && msg.Trip.DriverID == driver.ID {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("last message = %+v", msg)
		}
	}
}

func TestReadyFailure(t *testing.T) {
	srv := newAPI(t, func(context.Context) error { return context.DeadlineExceeded })
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	mustStatus(t, ts, http.MethodGet, "/readyz", nil, http.StatusServiceUnavailable)
}

func TestCancelConflict(t *testing.T) {
	srv := newAPI(t, nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	rider := postJSON[domain.Rider](t, ts, "/v1/riders", map[string]any{"name": "Ava"}, http.StatusCreated)
	trip := postJSON[domain.Trip](t, ts, "/v1/trips", map[string]any{
		"rider_id": rider.ID,
		"pickup":   map[string]float64{"lat": 37.77, "lng": -122.42},
		"dropoff":  map[string]float64{"lat": 37.78, "lng": -122.41},
	}, http.StatusCreated)
	mustStatus(t, ts, http.MethodPost, "/v1/trips/"+trip.ID+"/cancel", map[string]any{"actor_id": "nope"}, http.StatusConflict)
	cancelled := postJSON[domain.Trip](t, ts, "/v1/trips/"+trip.ID+"/cancel", map[string]any{"actor_id": rider.ID}, http.StatusOK)
	if cancelled.Status != domain.TripCancelled {
		t.Fatalf("status = %s", cancelled.Status)
	}
}

type wsMessage struct {
	Type string      `json:"type"`
	Trip domain.Trip `json:"trip"`
	ID   string      `json:"actor_id"`
}

func readWS(t *testing.T, ctx context.Context, conn *websocket.Conn) wsMessage {
	t.Helper()
	_, payload, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var msg wsMessage
	if err := json.Unmarshal(payload, &msg); err != nil {
		t.Fatal(err)
	}
	return msg
}

func newAPI(t *testing.T, ready func(context.Context) error) *api.Server {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := metrics.New(reg)
	if err != nil {
		t.Fatal(err)
	}
	hub := api.NewHub(nil)
	svc, err := service.New(service.Deps{
		Repo:             store.NewMemory(),
		Locations:        location.NewMemory(),
		Publisher:        events.NewMemory(),
		Listener:         hub,
		Fares:            fare.Params{BaseCents: 250, PerKmCents: 150, PerMinCents: 30, Currency: "USD"},
		GeohashPrecision: 6,
		Metrics:          m,
	})
	if err != nil {
		t.Fatal(err)
	}
	return api.New(svc, hub, nil, m, ready)
}

func mustStatus(t *testing.T, ts *httptest.Server, method, path string, body any, want int) {
	t.Helper()
	res := do(t, ts, method, path, body)
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := io.Copy(io.Discard, res.Body); err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s = %d, want %d", method, path, res.StatusCode, want)
	}
}

func do(t *testing.T, ts *httptest.Server, method, path string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func postJSON[T any](t *testing.T, ts *httptest.Server, path string, body any, want int) T {
	t.Helper()
	res := do(t, ts, http.MethodPost, path, body)
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	return decodeStatus[T](t, res, want)
}

func getJSON[T any](t *testing.T, ts *httptest.Server, path string) T {
	t.Helper()
	res := do(t, ts, http.MethodGet, path, nil)
	defer func() {
		if err := res.Body.Close(); err != nil {
			t.Error(err)
		}
	}()
	return decodeStatus[T](t, res, http.StatusOK)
}

func decodeStatus[T any](t *testing.T, res *http.Response, want int) T {
	t.Helper()
	var out T
	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("%s %s = %d %s", res.Request.Method, res.Request.URL.Path, res.StatusCode, raw)
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
