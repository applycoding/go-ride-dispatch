package matching_test

import (
	"math/rand"
	"strconv"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/matching"
)

func BenchmarkNearest(b *testing.B) {
	for _, n := range []int{10_000, 50_000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			idx := seed(b, n)
			q := domain.Point{Lat: 37.775, Lng: -122.445}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				idx.Nearest(q, 5, 3000)
			}
		})
	}
}

func BenchmarkNearestParallel(b *testing.B) {
	idx := seed(b, 50_000)
	q := domain.Point{Lat: 37.775, Lng: -122.445}
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			idx.Nearest(q, 5, 3000)
		}
	})
}

func seed(tb testing.TB, n int) *matching.Index {
	tb.Helper()
	idx, err := matching.New(6)
	if err != nil {
		tb.Fatal(err)
	}
	rng := rand.New(rand.NewSource(42))
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < n; i++ {
		p := domain.Point{
			Lat: 37.70 + rng.Float64()*0.15,
			Lng: -122.52 + rng.Float64()*0.15,
		}
		if err := idx.Upsert(strconv.Itoa(i), p, now); err != nil {
			tb.Fatal(err)
		}
	}
	return idx
}
