package matching_test

import (
	"math"
	"math/rand"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/geo"
	"github.com/applycoding/go-ride-dispatch/internal/matching"
)

func TestNearestTable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	origin := domain.Point{Lat: 37.7749, Lng: -122.4194}
	north := domain.Point{Lat: 37.7849, Lng: -122.4194}
	far := domain.Point{Lat: 37.90, Lng: -122.4194}

	tests := []struct {
		name    string
		drivers map[string]domain.Point
		limit   int
		radius  float64
		want    []string
	}{
		{name: "empty", limit: 3, radius: 5000, want: nil},
		{
			name:    "closest first",
			drivers: map[string]domain.Point{"far": north, "near": origin},
			limit:   2,
			radius:  5000,
			want:    []string{"near", "far"},
		},
		{
			name:    "radius excludes",
			drivers: map[string]domain.Point{"near": origin, "away": far},
			limit:   5,
			radius:  2000,
			want:    []string{"near"},
		},
		{
			name:    "limit",
			drivers: map[string]domain.Point{"a": origin, "b": north},
			limit:   1,
			radius:  5000,
			want:    []string{"a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx := newIndex(t, 6)
			for id, p := range tt.drivers {
				if err := idx.Upsert(id, p, now); err != nil {
					t.Fatal(err)
				}
			}
			got := ids(idx.Nearest(origin, tt.limit, tt.radius))
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}
			if stringsEqual(got, tt.want) {
				return
			}
			t.Fatalf("nearest = %v, want %v", got, tt.want)
		})
	}
}

func TestUpsertMovesCellAndRemove(t *testing.T) {
	idx := newIndex(t, 6)
	now := time.Unix(10, 0).UTC()
	a := domain.Point{Lat: 37.77, Lng: -122.42}
	b := domain.Point{Lat: 37.85, Lng: -122.30}
	if err := idx.Upsert("d1", a, now); err != nil {
		t.Fatal(err)
	}
	if err := idx.Upsert("d1", b, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if idx.Len() != 1 {
		t.Fatalf("len = %d", idx.Len())
	}
	got := idx.Nearest(a, 5, 1000)
	if len(got) != 0 {
		t.Fatalf("old cell still returned %+v", got)
	}
	got = idx.Nearest(b, 5, 1000)
	if len(got) != 1 || got[0].DriverID != "d1" || !got[0].UpdatedAt.Equal(now.Add(time.Second)) {
		t.Fatalf("moved driver = %+v", got)
	}
	idx.Remove("d1")
	idx.Remove("missing")
	if idx.Len() != 0 || len(idx.Nearest(b, 5, 1000)) != 0 {
		t.Fatal("driver still indexed")
	}
}

func TestNearestTieBreaksByID(t *testing.T) {
	idx := newIndex(t, 6)
	p := domain.Point{Lat: 40.0, Lng: -73.0}
	now := time.Unix(1, 0).UTC()
	for _, id := range []string{"b", "a", "c"} {
		if err := idx.Upsert(id, p, now); err != nil {
			t.Fatal(err)
		}
	}
	got := ids(idx.Nearest(p, 3, 100))
	want := []string{"a", "b", "c"}
	if !stringsEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestNearestFindsAdjacentCell(t *testing.T) {
	idx := newIndex(t, 6)
	pickup := domain.Point{Lat: 37.7749, Lng: -122.4194}
	hash := geo.Encode(pickup.Lat, pickup.Lng, 6)
	neighbors := geo.Neighbors(hash)
	if len(neighbors) == 0 {
		t.Fatal("no neighbors")
	}
	lat, lng, err := geo.DecodeCenter(neighbors[0])
	if err != nil {
		t.Fatal(err)
	}
	driver := domain.Point{Lat: lat, Lng: lng}
	if err := idx.Upsert("adj", driver, time.Unix(2, 0).UTC()); err != nil {
		t.Fatal(err)
	}
	dist := geo.HaversineMeters(pickup.Lat, pickup.Lng, lat, lng)
	got := idx.Nearest(pickup, 1, dist+50)
	if len(got) != 1 || got[0].DriverID != "adj" {
		t.Fatalf("adjacent driver = %+v, distance %f", got, dist)
	}
}

func TestNearestMatchesBruteForce(t *testing.T) {
	idx := newIndex(t, 6)
	rng := rand.New(rand.NewSource(7))
	const n = 800
	now := time.Unix(50, 0).UTC()
	points := make([]domain.Point, n)
	for i := 0; i < n; i++ {
		points[i] = domain.Point{
			Lat: 37.70 + rng.Float64()*0.15,
			Lng: -122.52 + rng.Float64()*0.15,
		}
		if err := idx.Upsert(strconv.Itoa(i), points[i], now); err != nil {
			t.Fatal(err)
		}
	}
	query := domain.Point{Lat: 37.775, Lng: -122.445}
	const radius = 15_000
	const limit = 5
	got := idx.Nearest(query, limit, radius)

	type scored struct {
		id string
		d  float64
	}
	all := make([]scored, 0, n)
	for i, p := range points {
		d := geo.HaversineMeters(query.Lat, query.Lng, p.Lat, p.Lng)
		if d <= radius {
			all = append(all, scored{id: strconv.Itoa(i), d: d})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].d == all[j].d {
			return all[i].id < all[j].id
		}
		return all[i].d < all[j].d
	})
	if len(all) < limit {
		t.Fatalf("only %d drivers inside radius", len(all))
	}
	for i := 0; i < limit; i++ {
		if got[i].DriverID != all[i].id || math.Abs(got[i].DistanceMeters-all[i].d) > 0.01 {
			t.Fatalf("rank %d = %+v, brute %s @ %f", i, got[i], all[i].id, all[i].d)
		}
	}
}

func TestNewRejectsPrecision(t *testing.T) {
	if _, err := matching.New(0); err == nil {
		t.Fatal("expected error")
	}
	if err := (func() error {
		idx, err := matching.New(6)
		if err != nil {
			return err
		}
		return idx.Upsert("", domain.Point{}, time.Time{})
	})(); err == nil {
		t.Fatal("expected empty id error")
	}
}

func newIndex(t *testing.T, precision int) *matching.Index {
	t.Helper()
	idx, err := matching.New(precision)
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func ids(cands []matching.Candidate) []string {
	out := make([]string, len(cands))
	for i, c := range cands {
		out[i] = c.DriverID
	}
	return out
}

func stringsEqual(a, b []string) bool {
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
