package geo_test

import (
	"math"
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/geo"
)

func TestHaversineMeters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		lat1, lng1 float64
		lat2, lng2 float64
		want       float64
		tolerance  float64
	}{
		{name: "same point", lat1: 10, lng1: 20, lat2: 10, lng2: 20, want: 0, tolerance: 0.01},
		{name: "one degree longitude at equator", lat1: 0, lng1: 0, lat2: 0, lng2: 1, want: 111194, tolerance: 50},
		{name: "one degree latitude", lat1: 0, lng1: 0, lat2: 1, lng2: 0, want: 111194, tolerance: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := geo.HaversineMeters(tt.lat1, tt.lng1, tt.lat2, tt.lng2)
			if math.Abs(got-tt.want) > tt.tolerance {
				t.Fatalf("distance = %.2f, want %.2f ± %.2f", got, tt.want, tt.tolerance)
			}
		})
	}
}
