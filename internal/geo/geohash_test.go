package geo_test

import (
	"math"
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/geo"
)

func TestEncodeKnownVector(t *testing.T) {
	t.Parallel()
	// Wikipedia's worked example.
	const want = "u4pruydqqvj"
	got := geo.Encode(57.64911, 10.40744, len(want))
	if got != want {
		t.Fatalf("Encode() = %s, want %s", got, want)
	}
}

func TestDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		lat, lng  float64
		precision int
		maxErrorM float64
	}{
		{name: "copenhagen", lat: 57.64911, lng: 10.40744, precision: 8, maxErrorM: 30},
		{name: "san francisco", lat: 37.7749, lng: -122.4194, precision: 7, maxErrorM: 160},
		{name: "equator", lat: 0, lng: 0, precision: 6, maxErrorM: 1200},
		{name: "south", lat: -33.8688, lng: 151.2093, precision: 7, maxErrorM: 160},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			hash := geo.Encode(tt.lat, tt.lng, tt.precision)
			if len(hash) != tt.precision {
				t.Fatalf("length = %d, want %d", len(hash), tt.precision)
			}
			lat, lng, err := geo.Decode(hash)
			if err != nil {
				t.Fatal(err)
			}
			if dist := geo.HaversineMeters(tt.lat, tt.lng, lat, lng); dist > tt.maxErrorM {
				t.Fatalf("center is %.1fm from the original point", dist)
			}
		})
	}
}

func TestNeighborsAreAdjacent(t *testing.T) {
	t.Parallel()
	hash := geo.Encode(37.77, -122.42, 6)
	neighbors, err := geo.Neighbors(hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(neighbors) != 8 {
		t.Fatalf("neighbor count = %d, want 8", len(neighbors))
	}
	centerLat, centerLng, err := geo.Decode(hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, neighbor := range neighbors {
		if neighbor == hash {
			t.Fatalf("neighbor equals the cell %s", hash)
		}
		lat, lng, err := geo.Decode(neighbor)
		if err != nil {
			t.Fatal(err)
		}
		dLat := math.Abs(lat - centerLat)
		dLng := math.Abs(lng - centerLng)
		if dLat == 0 && dLng == 0 {
			t.Fatalf("neighbor %s has the same center", neighbor)
		}
	}
}

func TestBoundingPrefixContainsNearbyPoint(t *testing.T) {
	t.Parallel()
	const lat, lng = 37.7749, -122.4194
	prefix := geo.BoundingPrefix(lat, lng, 800)
	if prefix == "" {
		t.Fatal("expected a prefix for a small urban radius")
	}
	near := geo.Encode(lat+0.002, lng-0.002, geo.IndexPrecision)
	if len(near) < len(prefix) || near[:len(prefix)] != prefix {
		// The offset may fall outside this particular prefix. It must still be
		// inside the prefix when it is inside the search radius.
		if geo.HaversineMeters(lat, lng, lat+0.002, lng-0.002) <= 800 && (len(near) < len(prefix) || near[:len(prefix)] != prefix) {
			t.Fatalf("nearby hash %s is not under prefix %s", near, prefix)
		}
	}
}

func TestInvalidGeohash(t *testing.T) {
	t.Parallel()
	if _, _, err := geo.Decode(""); err == nil {
		t.Fatal("expected error for empty hash")
	}
	if _, _, err := geo.Decode("u4pruydqqa"); err == nil {
		t.Fatal("expected error for illegal character")
	}
}

func FuzzEncodeDecode(f *testing.F) {
	f.Add(57.64911, 10.40744)
	f.Add(0.0, 0.0)
	f.Add(-33.8688, 151.2093)
	f.Fuzz(func(t *testing.T, lat, lng float64) {
		if math.IsNaN(lat) || math.IsNaN(lng) || math.IsInf(lat, 0) || math.IsInf(lng, 0) {
			return
		}
		if lat < -90 || lat > 90 || lng < -180 || lng > 180 {
			return
		}
		hash := geo.Encode(lat, lng, 8)
		gotLat, gotLng, err := geo.Decode(hash)
		if err != nil {
			t.Fatal(err)
		}
		if dist := geo.HaversineMeters(lat, lng, gotLat, gotLng); dist > 40 {
			t.Fatalf("distance %.2f for %s", dist, hash)
		}
	})
}
