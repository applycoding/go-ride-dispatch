// Package geo implements a pure-Go geohash grid used as the dispatch spatial index.
package geo

import (
	"fmt"
	"math"
)

const (
	base32 = "0123456789bcdefghjkmnpqrstuvwxyz"
	// IndexPrecision is the geohash length stored on each driver.
	// Precision 7 is about 150m by 150m, which is enough for a city demo.
	IndexPrecision = 7
)

var decodemap [256]byte

func init() {
	for i := range decodemap {
		decodemap[i] = 0xff
	}
	for i := 0; i < len(base32); i++ {
		decodemap[base32[i]] = byte(i)
	}
}

// Encode returns the geohash of lat/lng at the given character precision.
func Encode(lat, lng float64, precision int) string {
	if precision < 1 {
		precision = 1
	}
	idx := 0
	bit := 0
	even := true
	latMin, latMax := -90.0, 90.0
	lngMin, lngMax := -180.0, 180.0
	out := make([]byte, 0, precision)
	for len(out) < precision {
		if even {
			mid := (lngMin + lngMax) / 2
			if lng >= mid {
				idx = idx*2 + 1
				lngMin = mid
			} else {
				idx *= 2
				lngMax = mid
			}
		} else {
			mid := (latMin + latMax) / 2
			if lat >= mid {
				idx = idx*2 + 1
				latMin = mid
			} else {
				idx *= 2
				latMax = mid
			}
		}
		even = !even
		if bit < 4 {
			bit++
			continue
		}
		out = append(out, base32[idx])
		bit = 0
		idx = 0
	}
	return string(out)
}

// Box returns the latitude and longitude rectangle for a geohash.
func Box(hash string) (minLat, maxLat, minLng, maxLng float64, err error) {
	if hash == "" {
		return 0, 0, 0, 0, fmt.Errorf("empty geohash")
	}
	minLat, maxLat = -90, 90
	minLng, maxLng = -180, 180
	even := true
	for i := 0; i < len(hash); i++ {
		c := decodemap[hash[i]]
		if c == 0xff {
			return 0, 0, 0, 0, fmt.Errorf("invalid geohash")
		}
		for mask := 16; mask != 0; mask >>= 1 {
			if even {
				mid := (minLng + maxLng) / 2
				if c&byte(mask) != 0 {
					minLng = mid
				} else {
					maxLng = mid
				}
			} else {
				mid := (minLat + maxLat) / 2
				if c&byte(mask) != 0 {
					minLat = mid
				} else {
					maxLat = mid
				}
			}
			even = !even
		}
	}
	return minLat, maxLat, minLng, maxLng, nil
}

// Decode returns the center of the geohash cell.
func Decode(hash string) (lat, lng float64, err error) {
	minLat, maxLat, minLng, maxLng, err := Box(hash)
	if err != nil {
		return 0, 0, err
	}
	return (minLat + maxLat) / 2, (minLng + maxLng) / 2, nil
}

// Neighbors returns the up-to-eight adjacent cells at the same precision.
// Cells that would cross a pole are omitted. Longitude wraps at ±180.
func Neighbors(hash string) ([]string, error) {
	minLat, maxLat, minLng, maxLng, err := Box(hash)
	if err != nil {
		return nil, err
	}
	lat, lng, err := Decode(hash)
	if err != nil {
		return nil, err
	}
	dLat := maxLat - minLat
	dLng := maxLng - minLng
	prec := len(hash)
	steps := [][2]float64{
		{dLat, 0},
		{-dLat, 0},
		{0, dLng},
		{0, -dLng},
		{dLat, dLng},
		{dLat, -dLng},
		{-dLat, dLng},
		{-dLat, -dLng},
	}
	out := make([]string, 0, len(steps))
	seen := map[string]struct{}{hash: {}}
	for _, step := range steps {
		nLat := lat + step[0]
		nLng := wrapLng(lng + step[1])
		if nLat > 90 || nLat < -90 {
			continue
		}
		next := Encode(nLat, nLng, prec)
		if _, ok := seen[next]; ok {
			continue
		}
		seen[next] = struct{}{}
		out = append(out, next)
	}
	return out, nil
}

func wrapLng(lng float64) float64 {
	for lng > 180 {
		lng -= 360
	}
	for lng <= -180 {
		lng += 360
	}
	return lng
}

// BoundingPrefix returns a geohash prefix that contains every point within
// radiusM meters of lat/lng. An empty prefix means the box crosses a coarse
// cell boundary (or the antimeridian) and the caller should scan all drivers.
//
// The prefix is the shared hash of the bounding-box corners. Geohash cells are
// axis-aligned rectangles, so a box whose corners share a prefix lies entirely
// inside that cell. Haversine filtering still removes candidates outside the circle.
func BoundingPrefix(lat, lng, radiusM float64) string {
	const metersPerDeg = 110_000.0
	if radiusM < 0 || math.IsNaN(lat) || math.IsNaN(lng) || math.IsNaN(radiusM) {
		return ""
	}
	dLat := radiusM / metersPerDeg
	cos := math.Cos(lat * math.Pi / 180)
	if math.Abs(cos) < 0.01 {
		return ""
	}
	dLng := radiusM / (metersPerDeg * math.Abs(cos))
	north, south := lat+dLat, lat-dLat
	east, west := lng+dLng, lng-dLng
	if south < -90 || north > 90 || west < -180 || east > 180 {
		return ""
	}
	corners := []string{
		Encode(north, east, IndexPrecision),
		Encode(north, west, IndexPrecision),
		Encode(south, east, IndexPrecision),
		Encode(south, west, IndexPrecision),
		Encode(lat, lng, IndexPrecision),
	}
	prefix := corners[0]
	for _, corner := range corners[1:] {
		prefix = sharedPrefix(prefix, corner)
		if prefix == "" {
			return ""
		}
	}
	return prefix
}

func sharedPrefix(a, b string) string {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return a[:n]
}
