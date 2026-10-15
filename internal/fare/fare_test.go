package fare_test

import (
	"testing"

	"github.com/applycoding/go-ride-dispatch/internal/fare"
)

func TestEstimate(t *testing.T) {
	p := fare.Params{BaseCents: 250, PerKmCents: 150, PerMinCents: 30, Currency: "USD"}
	tests := []struct {
		name     string
		meters   float64
		seconds  int
		surge    float64
		want     int64
		wantDist int64
		wantDur  int64
		wantErr  bool
	}{
		{name: "base only", meters: 0, seconds: 0, surge: 1, want: 250},
		{name: "one km one minute", meters: 1000, seconds: 60, surge: 1, want: 430, wantDist: 150, wantDur: 30},
		{name: "surge", meters: 1000, seconds: 60, surge: 2, want: 860, wantDist: 150, wantDur: 30},
		{name: "fractional km", meters: 1500, seconds: 0, surge: 1, want: 475, wantDist: 225},
		{name: "half minute rounds", meters: 0, seconds: 90, surge: 1, want: 295, wantDur: 45},
		{name: "surge cap", meters: 0, seconds: 0, surge: 4, want: 1000},
		{name: "below min surge", surge: 0.5, wantErr: true},
		{name: "above max surge", surge: 4.1, wantErr: true},
		{name: "negative distance", meters: -1, surge: 1, wantErr: true},
		{name: "negative duration", seconds: -1, surge: 1, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := p.Estimate(tt.meters, tt.meters, tt.seconds, tt.surge)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if q.AmountCents != tt.want || q.DistanceCents != tt.wantDist || q.DurationCents != tt.wantDur {
				t.Fatalf("quote = %+v, want amount %d dist %d dur %d", q, tt.want, tt.wantDist, tt.wantDur)
			}
			if q.Currency != "USD" || q.Surge != tt.surge {
				t.Fatalf("currency/surge = %s %f", q.Currency, q.Surge)
			}
		})
	}
}

func TestEstimateRejectsBadTariff(t *testing.T) {
	if _, err := (fare.Params{Currency: "US"}).Estimate(0, 0, 0, 1); err == nil {
		t.Fatal("expected currency error")
	}
	if _, err := (fare.Params{BaseCents: -1, Currency: "USD"}).Estimate(0, 0, 0, 1); err == nil {
		t.Fatal("expected tariff error")
	}
}

func TestDurationSeconds(t *testing.T) {
	// 30 km/h is 8.333 m/s, so 1000m is 120s.
	if got := fare.DurationSeconds(1000, 30); got != 120 {
		t.Fatalf("duration = %d, want 120", got)
	}
	if got := fare.DurationSeconds(0, 30); got != 0 {
		t.Fatalf("duration = %d, want 0", got)
	}
	if got := fare.DurationSeconds(1000, 0); got != 120 {
		t.Fatalf("default speed duration = %d, want 120", got)
	}
}
