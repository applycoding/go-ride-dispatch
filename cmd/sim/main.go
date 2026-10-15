// Command sim is a small load generator for the dispatch demo.
// It registers drivers, streams location updates over WebSocket, and requests trips over REST.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

func main() {
	base := flag.String("url", "http://localhost:8080", "dispatch base URL")
	driversN := flag.Int("drivers", 25, "drivers to register")
	tripsN := flag.Int("trips", 40, "trips to request")
	concurrency := flag.Int("concurrency", 8, "concurrent trip workers")
	radius := flag.Float64("radius", 5000, "match radius in meters")
	centerLat := flag.Float64("center-lat", 37.7749, "city center latitude")
	centerLng := flag.Float64("center-lng", -122.4194, "city center longitude")
	spreadKm := flag.Float64("spread-km", 3, "driver and pickup spread in kilometers")
	flag.Parse()

	if *driversN < 1 || *tripsN < 1 || *concurrency < 1 {
		fmt.Fprintln(os.Stderr, "drivers, trips, and concurrency must be positive")
		os.Exit(2)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	drivers := make([]string, 0, *driversN)
	for i := 0; i < *driversN; i++ {
		point := jitter(*centerLat, *centerLng, *spreadKm)
		id, err := registerDriver(ctx, client, *base, fmt.Sprintf("sim-%d", i), point)
		if err != nil {
			fmt.Fprintf(os.Stderr, "register driver: %v\n", err)
			os.Exit(1)
		}
		drivers = append(drivers, id)
	}

	var wsErrors atomic.Int64
	var wsWG sync.WaitGroup
	for _, id := range drivers {
		wsWG.Add(1)
		go func(id string) {
			defer wsWG.Done()
			if err := streamLocations(ctx, *base, id, *centerLat, *centerLng, *spreadKm); err != nil && ctx.Err() == nil {
				wsErrors.Add(1)
			}
		}(id)
	}

	type sample struct {
		latency  time.Duration
		assigned bool
		err      error
	}
	samples := make([]sample, *tripsN)
	jobs := make(chan int)
	var wg sync.WaitGroup
	workers := *concurrency
	if workers > *tripsN {
		workers = *tripsN
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := range jobs {
				samples[n] = requestTrip(ctx, client, *base, *centerLat, *centerLng, *spreadKm, *radius)
			}
		}()
	}
	for n := 0; n < *tripsN; n++ {
		jobs <- n
	}
	close(jobs)
	wg.Wait()
	cancel()
	wsWG.Wait()

	var assigned, unmatched, failed int
	latencies := make([]time.Duration, 0, *tripsN)
	for _, sample := range samples {
		if sample.err != nil {
			failed++
			continue
		}
		latencies = append(latencies, sample.latency)
		if sample.assigned {
			assigned++
		} else {
			unmatched++
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })

	fmt.Printf("drivers: %d\n", len(drivers))
	fmt.Printf("trips: %d\n", *tripsN)
	fmt.Printf("assigned: %d\n", assigned)
	fmt.Printf("unmatched: %d\n", unmatched)
	fmt.Printf("errors: %d\n", failed)
	fmt.Printf("websocket errors: %d\n", wsErrors.Load())
	if len(latencies) > 0 {
		fmt.Printf("match latency p50: %s\n", percentile(latencies, 0.50))
		fmt.Printf("match latency p95: %s\n", percentile(latencies, 0.95))
	}
	if failed > 0 {
		os.Exit(1)
	}
}

func registerDriver(ctx context.Context, client *http.Client, base, name string, point latLng) (string, error) {
	var created struct {
		ID string `json:"id"`
	}
	if err := post(ctx, client, base+"/v1/drivers", map[string]string{"name": name}, &created); err != nil {
		return "", err
	}
	if err := post(ctx, client, base+"/v1/drivers/"+created.ID+"/location", point, nil); err != nil {
		return "", err
	}
	if err := post(ctx, client, base+"/v1/drivers/"+created.ID+"/status", map[string]string{"status": "available"}, nil); err != nil {
		return "", err
	}
	return created.ID, nil
}

func requestTrip(ctx context.Context, client *http.Client, base string, lat, lng, spreadKm, radius float64) struct {
	latency  time.Duration
	assigned bool
	err      error
} {
	pickup := jitter(lat, lng, spreadKm)
	dropoff := jitter(lat, lng, spreadKm)
	payload := map[string]any{
		"rider_id": "sim-rider",
		"pickup":   pickup,
		"dropoff":  dropoff,
		"radius_m": radius,
	}
	var trip struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	start := time.Now()
	err := post(ctx, client, base+"/v1/trips", payload, &trip)
	result := struct {
		latency  time.Duration
		assigned bool
		err      error
	}{latency: time.Since(start), err: err, assigned: err == nil && trip.Status == "assigned"}
	if err != nil || trip.Status != "assigned" {
		return result
	}
	if err := post(ctx, client, base+"/v1/trips/"+trip.ID+"/en-route", nil, nil); err != nil {
		result.err = err
		return result
	}
	if err := post(ctx, client, base+"/v1/trips/"+trip.ID+"/complete", nil, nil); err != nil {
		result.err = err
	}
	return result
}

func streamLocations(ctx context.Context, base, driverID string, lat, lng, spreadKm float64) error {
	url := "ws" + strings.TrimPrefix(base, "http") + "/v1/drivers/" + driverID + "/stream"
	conn, resp, err := websocket.Dial(ctx, url, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	go func() {
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			body, err := json.Marshal(jitter(lat, lng, spreadKm))
			if err != nil {
				return err
			}
			writeCtx, cancel := context.WithTimeout(ctx, time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, body)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

type latLng struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func jitter(lat, lng, spreadKm float64) latLng {
	// A degree of latitude is about 111km. This square is good enough for a demo.
	delta := spreadKm / 111.0
	return latLng{
		Lat: lat + (rand.Float64()-0.5)*delta,
		Lng: lng + (rand.Float64()-0.5)*delta,
	}
}

func post(ctx context.Context, client *http.Client, url string, payload any, dst any) error {
	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		return fmt.Errorf("%s: %s: %s", url, resp.Status, bytes.TrimSpace(data))
	}
	if dst != nil && len(data) > 0 {
		if err := json.Unmarshal(data, dst); err != nil {
			return err
		}
	}
	return nil
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(math.Ceil(p*float64(len(sorted)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}
