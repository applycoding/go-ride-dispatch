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

	"github.com/applycoding/go-ride-dispatch/internal/api"
	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/store"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

func TestHTTPLifecycle(t *testing.T) {
	ts, _ := newTestServer(t)

	near := createDriver(t, ts.URL, "near")
	far := createDriver(t, ts.URL, "far")
	postJSON(t, ts.URL+"/v1/drivers/"+near+"/location", `{"lat":37.775,"lng":-122.419}`, http.StatusOK)
	postJSON(t, ts.URL+"/v1/drivers/"+far+"/location", `{"lat":37.80,"lng":-122.43}`, http.StatusOK)
	postJSON(t, ts.URL+"/v1/drivers/"+near+"/status", `{"status":"available"}`, http.StatusOK)
	postJSON(t, ts.URL+"/v1/drivers/"+far+"/status", `{"status":"available"}`, http.StatusOK)

	body := postJSON(t, ts.URL+"/v1/trips", `{
		"rider_id":"rider-1",
		"pickup":{"lat":37.7749,"lng":-122.4194},
		"dropoff":{"lat":37.79,"lng":-122.40},
		"radius_m":8000
	}`, http.StatusCreated)
	var created domain.Trip
	decode(t, body, &created)
	if created.Status != domain.TripAssigned || created.DriverID != near {
		t.Fatalf("trip = %+v", created)
	}

	var fetched domain.Trip
	decode(t, get(t, ts.URL+"/v1/trips/"+created.ID, http.StatusOK), &fetched)
	if fetched.ID != created.ID {
		t.Fatalf("fetched = %+v", fetched)
	}

	postJSON(t, ts.URL+"/v1/trips/"+created.ID+"/complete", ``, http.StatusConflict)
	postJSON(t, ts.URL+"/v1/trips/"+created.ID+"/en-route", ``, http.StatusOK)
	var done domain.Trip
	decode(t, postJSON(t, ts.URL+"/v1/trips/"+created.ID+"/complete", ``, http.StatusOK), &done)
	if done.Status != domain.TripCompleted {
		t.Fatalf("status = %s", done.Status)
	}

	var driver domain.Driver
	decode(t, get(t, ts.URL+"/v1/drivers/"+near, http.StatusOK), &driver)
	if driver.Status != domain.DriverAvailable {
		t.Fatalf("driver status = %s", driver.Status)
	}

	nearby := get(t, ts.URL+"/v1/drivers?lat=37.7749&lng=-122.4194&radius_m=8000", http.StatusOK)
	if !strings.Contains(string(nearby), near) {
		t.Fatalf("nearby = %s", nearby)
	}
	get(t, ts.URL+"/v1/drivers/missing-driver", http.StatusNotFound)
	postJSON(t, ts.URL+"/v1/drivers", `{"name":`, http.StatusBadRequest)

	metricsBody := get(t, ts.URL+"/metrics", http.StatusOK)
	if !strings.Contains(string(metricsBody), "dispatch_match_results_total") {
		t.Fatalf("metrics missing dispatch series:\n%s", metricsBody)
	}
	get(t, ts.URL+"/healthz", http.StatusOK)
	get(t, ts.URL+"/readyz", http.StatusOK)
}

func TestDriverWebSocketLocation(t *testing.T) {
	ts, _ := newTestServer(t)
	driverID := createDriver(t, ts.URL, "ada")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, resp, err := websocket.Dial(ctx, wsURL(ts.URL)+"/v1/drivers/"+driverID+"/stream", nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	payload := []byte(`{"lat":37.78,"lng":-122.41}`)
	if err := conn.Write(ctx, websocket.MessageText, payload); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		readCtx, readCancel := context.WithTimeout(ctx, time.Until(deadline))
		_, data, err := conn.Read(readCtx)
		readCancel()
		if err != nil {
			t.Fatal(err)
		}
		var event events.Event
		if err := json.Unmarshal(data, &event); err != nil {
			t.Fatal(err)
		}
		if event.Type == events.TypeDriverLocation && event.DriverID == driverID && event.Location != nil {
			if event.Location.Lat != 37.78 {
				t.Fatalf("lat = %v", event.Location.Lat)
			}
			var driver domain.Driver
			decode(t, get(t, ts.URL+"/v1/drivers/"+driverID, http.StatusOK), &driver)
			if driver.Location.Lat != 37.78 || driver.Geohash == "" {
				t.Fatalf("stored driver = %+v", driver)
			}
			return
		}
	}
	t.Fatal("did not observe location event")
}

func TestEventWebSocketReceivesAssignment(t *testing.T) {
	ts, _ := newTestServer(t)
	driverID := createDriver(t, ts.URL, "ada")
	postJSON(t, ts.URL+"/v1/drivers/"+driverID+"/location", `{"lat":37.77,"lng":-122.42}`, http.StatusOK)
	postJSON(t, ts.URL+"/v1/drivers/"+driverID+"/status", `{"status":"available"}`, http.StatusOK)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, wsURL(ts.URL)+"/v1/ws?types=trip.assigned", nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	postJSON(t, ts.URL+"/v1/trips", `{
		"rider_id":"rider-ws",
		"pickup":{"lat":37.771,"lng":-122.421},
		"dropoff":{"lat":37.78,"lng":-122.41}
	}`, http.StatusCreated)

	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var event events.Event
	if err := json.Unmarshal(data, &event); err != nil {
		t.Fatal(err)
	}
	if event.Type != events.TypeTripAssigned || event.DriverID != driverID {
		t.Fatalf("event = %+v", event)
	}
}

func TestCancelRequestedTrip(t *testing.T) {
	ts, _ := newTestServer(t)
	body := postJSON(t, ts.URL+"/v1/trips", `{
		"rider_id":"rider-2",
		"pickup":{"lat":37.77,"lng":-122.42},
		"dropoff":{"lat":37.80,"lng":-122.40}
	}`, http.StatusCreated)
	var created domain.Trip
	decode(t, body, &created)
	if created.Status != domain.TripRequested {
		t.Fatalf("status = %s", created.Status)
	}
	var cancelled domain.Trip
	decode(t, postJSON(t, ts.URL+"/v1/trips/"+created.ID+"/cancel", ``, http.StatusOK), &cancelled)
	if cancelled.Status != domain.TripCancelled {
		t.Fatalf("status = %s", cancelled.Status)
	}
}

func newTestServer(t *testing.T) (*httptest.Server, *events.MemoryBus) {
	t.Helper()
	bus := events.NewMemoryBus()
	recorder := metrics.New()
	svc := trip.NewService(store.NewMemory(), bus, recorder, 5000, nil)
	handler := api.NewServer(svc, bus, recorder, nil).Handler()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts, bus
}

func createDriver(t *testing.T, baseURL, name string) string {
	t.Helper()
	body := postJSON(t, baseURL+"/v1/drivers", `{"name":"`+name+`"}`, http.StatusCreated)
	var driver domain.Driver
	decode(t, body, &driver)
	if driver.ID == "" || driver.Status != domain.DriverOffline {
		t.Fatalf("driver = %+v", driver)
	}
	return driver.ID
}

func postJSON(t *testing.T, url, payload string, want int) []byte {
	t.Helper()
	var body io.Reader
	if payload != "" {
		body = strings.NewReader(payload)
	}
	req, err := http.NewRequest(http.MethodPost, url, body)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("POST %s status = %d, want %d, body %s", url, resp.StatusCode, want, data)
	}
	return data
}

func get(t *testing.T, url string, want int) []byte {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != want {
		t.Fatalf("GET %s status = %d, want %d, body %s", url, resp.StatusCode, want, data)
	}
	return data
}

func decode(t *testing.T, data []byte, dst any) {
	t.Helper()
	if err := json.NewDecoder(bytes.NewReader(data)).Decode(dst); err != nil {
		t.Fatalf("decode %s: %v", data, err)
	}
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}
