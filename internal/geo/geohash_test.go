package geo_test

import (
	"math"
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/geo"
)

func TestEncodeWikipedia(t *testing.T) {
	got := geo.Encode(57.64911, 10.40744, 11)
	const want = "u4pruydqqvj"
	if got != want {
		t.Fatalf("Encode() = %s, want %s", got, want)
	}
	lat, lng, err := geo.DecodeCenter(want)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(lat-57.64911) > 0.0001 || math.Abs(lng-10.40744) > 0.0001 {
		t.Fatalf("center = (%f, %f)", lat, lng)
	}
	if again := geo.Encode(lat, lng, len(want)); again != want {
		t.Fatalf("round trip %s -> %s", want, again)
	}
}

func TestBoundingBoxRejectsBadHash(t *testing.T) {
	if _, err := geo.BoundingBox("u4pruydqqa"); err == nil {
		t.Fatal("expected error for illegal geohash character")
	}
}

func TestNeighborTablesArePermutations(t *testing.T) {
	// Exercise every parity through public neighbors of even and odd lengths.
	for _, hash := range []string{"u4pruy", "u4pruyd"} {
		for _, n := range geo.Neighbors(hash) {
			if len(n) != len(hash) {
				t.Fatalf("neighbor %s length mismatch", n)
			}
			if _, _, err := geo.DecodeCenter(n); err != nil {
				t.Fatalf("neighbor %s: %v", n, err)
			}
		}
	}
}

func TestNeighborRoundTrip(t *testing.T) {
	cities := []struct {
		name string
		lat  float64
		lng  float64
	}{
		{"sf", 37.7749, -122.4194},
		{"nyc", 40.7128, -74.0060},
		{"london", 51.5074, -0.1278},
		{"sydney", -33.8688, 151.2093},
	}
	for _, city := range cities {
		for precision := 4; precision <= 7; precision++ {
			hash := geo.Encode(city.lat, city.lng, precision)
			lat, lng, err := geo.DecodeCenter(hash)
			if err != nil {
				t.Fatal(err)
			}
			neighbors := geo.Neighbors(hash)
			if len(neighbors) != 8 {
				t.Fatalf("%s precision %d: got %d neighbors", city.name, precision, len(neighbors))
			}
			byOffset := map[string]string{}
			for _, n := range neighbors {
				nLat, nLng, err := geo.DecodeCenter(n)
				if err != nil {
					t.Fatal(err)
				}
				key := offsetKey(nLat-lat, nLng-lng)
				byOffset[key] = n
			}
			east := byOffset["E"]
			west := byOffset["W"]
			north := byOffset["N"]
			south := byOffset["S"]
			if east == "" || west == "" || north == "" || south == "" {
				t.Fatalf("%s p%d missing cardinal neighbor: %#v", city.name, precision, byOffset)
			}
			if back := cardinal(east, "W"); back != hash {
				t.Fatalf("east then west = %s, want %s", back, hash)
			}
			if back := cardinal(west, "E"); back != hash {
				t.Fatalf("west then east = %s, want %s", back, hash)
			}
			if back := cardinal(north, "S"); back != hash {
				t.Fatalf("north then south = %s, want %s", back, hash)
			}
			if back := cardinal(south, "N"); back != hash {
				t.Fatalf("south then north = %s, want %s", back, hash)
			}
		}
	}
}

func TestCoverGrowsWithRings(t *testing.T) {
	hash := geo.Encode(37.77, -122.42, 6)
	r0 := geo.Cover(hash, 0)
	r1 := geo.Cover(hash, 1)
	r2 := geo.Cover(hash, 2)
	if len(r0) != 1 || r0[0] != hash {
		t.Fatalf("ring 0 = %v", r0)
	}
	if len(r1) != 9 {
		t.Fatalf("ring 1 = %d cells", len(r1))
	}
	if len(r2) <= len(r1) {
		t.Fatalf("ring 2 (%d) should exceed ring 1 (%d)", len(r2), len(r1))
	}
}

func TestCellSizePrecision1(t *testing.T) {
	w, h := geo.CellSizeMeters(1, 0)
	// Precision 1 is 3 longitude bits and 2 latitude bits: 45 degrees each.
	const metersPerDegree = 111_320.0
	if math.Abs(w-45*metersPerDegree) > 1 || math.Abs(h-45*metersPerDegree) > 1 {
		t.Fatalf("cell size = (%f, %f)", w, h)
	}
}

func TestHaversineLatitudeDegree(t *testing.T) {
	d := geo.HaversineMeters(0, 0, 1, 0)
	const want = 6_371_008.8 * math.Pi / 180
	if math.Abs(d-want) > 0.01 {
		t.Fatalf("distance = %f, want %f", d, want)
	}
	if geo.HaversineMeters(10, 20, 10, 20) != 0 {
		t.Fatal("identical points should be 0m apart")
	}
}

func offsetKey(dLat, dLng float64) string {
	const eps = 1e-9
	ns, ew := "", ""
	switch {
	case dLat > eps:
		ns = "N"
	case dLat < -eps:
		ns = "S"
	}
	switch {
	case dLng > eps:
		ew = "E"
	case dLng < -eps:
		ew = "W"
	}
	return ns + ew
}

func cardinal(hash, dir string) string {
	lat, lng, err := geo.DecodeCenter(hash)
	if err != nil {
		return ""
	}
	for _, n := range geo.Neighbors(hash) {
		nLat, nLng, err := geo.DecodeCenter(n)
		if err != nil {
			continue
		}
		if offsetKey(nLat-lat, nLng-lng) == dir {
			return n
		}
	}
	return ""
}
