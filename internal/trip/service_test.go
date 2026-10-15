package trip_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/geo"
	"github.com/applycoding/go-ride-dispatch/internal/store"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

type recordBus struct {
	mu     sync.Mutex
	events []events.Event
}

func (r *recordBus) Publish(_ context.Context, event events.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
	return nil
}

func (r *recordBus) types() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.events))
	for i := range r.events {
		out[i] = r.events[i].Type
	}
	return out
}

func TestRequestTripMatching(t *testing.T) {
	t.Parallel()
	pickup := domain.Point{Lat: 37.7749, Lng: -122.4194}
	tests := []struct {
		name       string
		drivers    []seedDriver
		radius     float64
		wantStatus domain.TripStatus
		wantName   string
	}{
		{
			name: "nearest of two available drivers",
			drivers: []seedDriver{
				{name: "far", lat: 37.80, lng: -122.42, status: domain.DriverAvailable},
				{name: "near", lat: 37.776, lng: -122.419, status: domain.DriverAvailable},
			},
			radius:     8000,
			wantStatus: domain.TripAssigned,
			wantName:   "near",
		},
		{
			name: "busy driver is skipped",
			drivers: []seedDriver{
				{name: "busy-near", lat: 37.775, lng: -122.419, status: domain.DriverBusy},
				{name: "free", lat: 37.78, lng: -122.42, status: domain.DriverAvailable},
			},
			radius:     8000,
			wantStatus: domain.TripAssigned,
			wantName:   "free",
		},
		{
			name: "offline driver is skipped",
			drivers: []seedDriver{
				{name: "offline", lat: 37.775, lng: -122.419, status: domain.DriverOffline},
			},
			radius:     8000,
			wantStatus: domain.TripRequested,
		},
		{
			name: "outside radius stays requested",
			drivers: []seedDriver{
				{name: "far", lat: 37.90, lng: -122.50, status: domain.DriverAvailable},
			},
			radius:     1000,
			wantStatus: domain.TripRequested,
		},
		{
			name:       "no drivers",
			radius:     5000,
			wantStatus: domain.TripRequested,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, st, bus := newService(t)
			ctx := context.Background()
			ids := map[string]string{}
			for _, driver := range tt.drivers {
				ids[driver.name] = seed(t, svc, st, driver)
			}
			got, err := svc.RequestTrip(ctx, "rider-1", pickup, domain.Point{Lat: 37.79, Lng: -122.40}, tt.radius)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %s, want %s", got.Status, tt.wantStatus)
			}
			if tt.wantName != "" && got.DriverID != ids[tt.wantName] {
				t.Fatalf("driver = %s, want %s", got.DriverID, ids[tt.wantName])
			}
			if tt.wantStatus == domain.TripAssigned {
				driver, err := svc.GetDriver(ctx, got.DriverID)
				if err != nil {
					t.Fatal(err)
				}
				if driver.Status != domain.DriverBusy {
					t.Fatalf("assigned driver status = %s", driver.Status)
				}
			}
			tripEvents := filterPrefix(bus.types(), "trip.")
			if len(tripEvents) == 0 || tripEvents[0] != events.TypeTripRequested {
				t.Fatalf("trip events = %v", tripEvents)
			}
			if tt.wantStatus == domain.TripAssigned && tripEvents[len(tripEvents)-1] != events.TypeTripAssigned {
				t.Fatalf("trip events = %v", tripEvents)
			}
		})
	}
}

func TestMatchAcrossGeohashBoundary(t *testing.T) {
	t.Parallel()
	pickup := domain.Point{Lat: 37.7749, Lng: -122.4194}
	base := geo.Encode(pickup.Lat, pickup.Lng, 6)
	var driverPoint domain.Point
	found := false
	for i := 1; i <= 80; i++ {
		candidate := domain.Point{Lat: pickup.Lat + float64(i)*0.0004, Lng: pickup.Lng}
		hash := geo.Encode(candidate.Lat, candidate.Lng, 6)
		dist := geo.HaversineMeters(pickup.Lat, pickup.Lng, candidate.Lat, candidate.Lng)
		if hash != base && dist < 1500 {
			driverPoint = candidate
			found = true
			break
		}
	}
	if !found {
		t.Fatal("could not find a nearby point in a different precision-6 cell")
	}

	svc, _, _ := newService(t)
	ctx := context.Background()
	driver, err := svc.RegisterDriver(ctx, "", "boundary")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateLocation(ctx, driver.ID, driverPoint); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetStatus(ctx, driver.ID, domain.DriverAvailable); err != nil {
		t.Fatal(err)
	}
	trip, err := svc.RequestTrip(ctx, "rider-b", pickup, domain.Point{Lat: pickup.Lat, Lng: pickup.Lng + 0.01}, 2000)
	if err != nil {
		t.Fatal(err)
	}
	if trip.Status != domain.TripAssigned || trip.DriverID != driver.ID {
		t.Fatalf("trip = %+v", trip)
	}
}

func TestTripLifecycleFreesDriver(t *testing.T) {
	t.Parallel()
	svc, _, bus := newService(t)
	ctx := context.Background()
	driver := mustDriver(t, svc, "ada", domain.Point{Lat: 37.77, Lng: -122.42})
	pickup := domain.Point{Lat: 37.771, Lng: -122.421}
	dropoff := domain.Point{Lat: 37.79, Lng: -122.40}

	first, err := svc.RequestTrip(ctx, "rider-1", pickup, dropoff, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != domain.TripAssigned {
		t.Fatalf("first status = %s", first.Status)
	}
	if _, err := svc.CompleteTrip(ctx, first.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("complete from assigned err = %v", err)
	}
	enRoute, err := svc.MarkEnRoute(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if enRoute.Status != domain.TripEnRoute {
		t.Fatalf("status = %s", enRoute.Status)
	}
	done, err := svc.CompleteTrip(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.Status != domain.TripCompleted {
		t.Fatalf("status = %s", done.Status)
	}
	freed, err := svc.GetDriver(ctx, driver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if freed.Status != domain.DriverAvailable {
		t.Fatalf("driver status = %s", freed.Status)
	}

	second, err := svc.RequestTrip(ctx, "rider-2", pickup, dropoff, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if second.DriverID != driver.ID {
		t.Fatalf("second driver = %s", second.DriverID)
	}
	if _, err := svc.CancelTrip(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	afterCancel, err := svc.GetDriver(ctx, driver.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterCancel.Status != domain.DriverAvailable {
		t.Fatalf("driver status after cancel = %s", afterCancel.Status)
	}
	if _, err := svc.CancelTrip(ctx, second.ID); !errors.Is(err, domain.ErrInvalidTransition) {
		t.Fatalf("second cancel err = %v", err)
	}

	want := []string{
		events.TypeDriverStatus,
		events.TypeDriverLocation,
		events.TypeDriverStatus,
		events.TypeTripRequested,
		events.TypeTripAssigned,
		events.TypeTripEnRoute,
		events.TypeTripCompleted,
		events.TypeDriverStatus,
		events.TypeTripRequested,
		events.TypeTripAssigned,
		events.TypeTripCancelled,
		events.TypeDriverStatus,
	}
	if got := bus.types(); !equalStrings(got, want) {
		t.Fatalf("events =\n%v\nwant\n%v", got, want)
	}
}

func TestSecondTripDoesNotTakeBusyDriver(t *testing.T) {
	t.Parallel()
	svc, _, _ := newService(t)
	ctx := context.Background()
	driver := mustDriver(t, svc, "ada", domain.Point{Lat: 37.77, Lng: -122.42})
	pickup := domain.Point{Lat: 37.771, Lng: -122.421}
	dropoff := domain.Point{Lat: 37.78, Lng: -122.41}
	if _, err := svc.RequestTrip(ctx, "rider-1", pickup, dropoff, 3000); err != nil {
		t.Fatal(err)
	}
	second, err := svc.RequestTrip(ctx, "rider-2", pickup, dropoff, 3000)
	if err != nil {
		t.Fatal(err)
	}
	if second.Status != domain.TripRequested {
		t.Fatalf("second status = %s driver %s (only driver %s)", second.Status, second.DriverID, driver.ID)
	}
}

func TestNearbyDriversOrdersByDistance(t *testing.T) {
	t.Parallel()
	svc, _, _ := newService(t)
	ctx := context.Background()
	far := mustDriver(t, svc, "far", domain.Point{Lat: 37.80, Lng: -122.42})
	near := mustDriver(t, svc, "near", domain.Point{Lat: 37.775, Lng: -122.419})
	got, err := svc.NearbyDrivers(ctx, domain.Point{Lat: 37.7749, Lng: -122.4194}, 10000, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != near.ID || got[1].ID != far.ID {
		t.Fatalf("order = %+v", got)
	}
}

func TestRegisterRejectsBadInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		id   string
		who  string
	}{
		{name: "empty name", id: "ok", who: "  "},
		{name: "bad id", id: "has space", who: "ada"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc, _, _ := newService(t)
			_, err := svc.RegisterDriver(context.Background(), tt.id, tt.who)
			if !errors.Is(err, domain.ErrInvalid) {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

type seedDriver struct {
	name   string
	lat    float64
	lng    float64
	status domain.DriverStatus
}

func newService(t *testing.T) (*trip.Service, *store.Memory, *recordBus) {
	t.Helper()
	st := store.NewMemory()
	bus := &recordBus{}
	return trip.NewService(st, bus, nil, 5000, nil), st, bus
}

func seed(t *testing.T, svc *trip.Service, st *store.Memory, driver seedDriver) string {
	t.Helper()
	ctx := context.Background()
	created, err := svc.RegisterDriver(ctx, "", driver.name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateLocation(ctx, created.ID, domain.Point{Lat: driver.lat, Lng: driver.lng}); err != nil {
		t.Fatal(err)
	}
	if driver.status == domain.DriverOffline {
		return created.ID
	}
	if _, err := svc.SetStatus(ctx, created.ID, domain.DriverAvailable); err != nil {
		t.Fatal(err)
	}
	if driver.status == domain.DriverBusy {
		if _, err := st.SetDriverStatus(ctx, created.ID, domain.DriverBusy, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return created.ID
}

func mustDriver(t *testing.T, svc *trip.Service, name string, point domain.Point) domain.Driver {
	t.Helper()
	ctx := context.Background()
	driver, err := svc.RegisterDriver(ctx, "", name)
	if err != nil {
		t.Fatal(err)
	}
	driver, err = svc.UpdateLocation(ctx, driver.ID, point)
	if err != nil {
		t.Fatal(err)
	}
	driver, err = svc.SetStatus(ctx, driver.ID, domain.DriverAvailable)
	if err != nil {
		t.Fatal(err)
	}
	return driver
}

func filterPrefix(values []string, prefix string) []string {
	var out []string
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			out = append(out, value)
		}
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
