// Package fare prices a trip from distance, duration, and a surge multiplier.
// Amounts are integer cents. This is a fixed tariff, not a metering engine.
package fare

import (
	"fmt"
	"math"
	"unicode"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

const (
	// MinSurge and MaxSurge bound the multiplier accepted by Estimate.
	MinSurge = 1.0
	MaxSurge = 4.0
	// DefaultRoadFactor scales straight-line distance toward a typical urban path.
	DefaultRoadFactor = 1.3
	// DefaultSpeedKmh is the assumed city speed when the caller does not supply one.
	DefaultSpeedKmh = 30
)

// Params is the published tariff.
type Params struct {
	BaseCents   int64
	PerKmCents  int64
	PerMinCents int64
	Currency    string
}

// Quote is a fare estimate. Component cents are pre-surge; AmountCents includes surge.
type Quote struct {
	Currency           string  `json:"currency"`
	AmountCents        int64   `json:"amount_cents"`
	BaseCents          int64   `json:"base_cents"`
	DistanceCents      int64   `json:"distance_cents"`
	DurationCents      int64   `json:"duration_cents"`
	DistanceMeters     float64 `json:"distance_meters"`
	StraightLineMeters float64 `json:"straight_line_meters"`
	DurationSeconds    int     `json:"duration_seconds"`
	Surge              float64 `json:"surge"`
}

// Estimate prices billedMeters. straightMeters is reported unchanged for the caller.
func (p Params) Estimate(billedMeters, straightMeters float64, durationSeconds int, surge float64) (Quote, error) {
	if p.BaseCents < 0 || p.PerKmCents < 0 || p.PerMinCents < 0 {
		return Quote{}, fmt.Errorf("%w: negative tariff", domain.ErrInvalid)
	}
	if !validCurrency(p.Currency) {
		return Quote{}, fmt.Errorf("%w: currency must be a 3-letter code", domain.ErrInvalid)
	}
	if math.IsNaN(billedMeters) || billedMeters < 0 || straightMeters < 0 {
		return Quote{}, fmt.Errorf("%w: distance must be >= 0", domain.ErrInvalid)
	}
	if durationSeconds < 0 {
		return Quote{}, fmt.Errorf("%w: duration must be >= 0", domain.ErrInvalid)
	}
	if math.IsNaN(surge) || surge < MinSurge || surge > MaxSurge {
		return Quote{}, fmt.Errorf("%w: surge must be between %g and %g", domain.ErrInvalid, MinSurge, MaxSurge)
	}

	distanceCents := int64(math.Round((billedMeters / 1000) * float64(p.PerKmCents)))
	durationCents := int64(math.Round((float64(durationSeconds) / 60) * float64(p.PerMinCents)))
	subtotal := p.BaseCents + distanceCents + durationCents
	amount := int64(math.Round(float64(subtotal) * surge))
	return Quote{
		Currency:           p.Currency,
		AmountCents:        amount,
		BaseCents:          p.BaseCents,
		DistanceCents:      distanceCents,
		DurationCents:      durationCents,
		DistanceMeters:     billedMeters,
		StraightLineMeters: straightMeters,
		DurationSeconds:    durationSeconds,
		Surge:              surge,
	}, nil
}

// DurationSeconds estimates travel time at a constant speed.
func DurationSeconds(distanceMeters, speedKmh float64) int {
	if speedKmh <= 0 {
		speedKmh = DefaultSpeedKmh
	}
	if distanceMeters <= 0 {
		return 0
	}
	metersPerSecond := speedKmh * 1000 / 3600
	return int(math.Round(distanceMeters / metersPerSecond))
}

func validCurrency(code string) bool {
	if len(code) != 3 {
		return false
	}
	for _, r := range code {
		if !unicode.IsLetter(r) {
			return false
		}
	}
	return true
}
