// Package geo encodes coordinates as geohashes and measures distances.
//
// Neighbors use the standard base32 border tables so cell expansion does not
// depend on floating-point steps across a boundary.
package geo

import (
	"math"
	"strings"
)

const base32Alphabet = "0123456789bcdefghjkmnpqrstuvwxyz"

const (
	dirNorth = "n"
	dirSouth = "s"
	dirEast  = "e"
	dirWest  = "w"
	parEven  = "even"
	parOdd   = "odd"
)

// neighbor and border tables are the classic geohash adjacency permutations.
// Odd rows follow the usual even/odd swap used by geohash libraries.
var (
	neighbor = map[string]map[string]string{
		dirNorth: {
			parEven: "p0r21436x8zb9dcf5h7kjnmqesgutwvy",
			parOdd:  "bc01fg45238967deuvhjyznpkmstqrwx",
		},
		dirSouth: {
			parEven: "14365h7k9dcfesgujnmqp0r2twvyx8zb",
			parOdd:  "238967debc01fg45kmstqrwxuvhjyznp",
		},
		dirEast: {
			parEven: "bc01fg45238967deuvhjyznpkmstqrwx",
			parOdd:  "p0r21436x8zb9dcf5h7kjnmqesgutwvy",
		},
		dirWest: {
			parEven: "238967debc01fg45kmstqrwxuvhjyznp",
			parOdd:  "14365h7k9dcfesgujnmqp0r2twvyx8zb",
		},
	}
	border = map[string]map[string]string{
		dirNorth: {parEven: "prxz", parOdd: "bcfguvyz"},
		dirSouth: {parEven: "028b", parOdd: "0145hjnp"},
		dirEast:  {parEven: "bcfguvyz", parOdd: "prxz"},
		dirWest:  {parEven: "0145hjnp", parOdd: "028b"},
	}
)

var decodeTable [256]byte

func init() {
	for i := range decodeTable {
		decodeTable[i] = 0xFF
	}
	for i := 0; i < len(base32Alphabet); i++ {
		decodeTable[base32Alphabet[i]] = byte(i)
	}
}

// Box is the lat/lng rectangle covered by a geohash.
type Box struct {
	MinLat float64
	MaxLat float64
	MinLng float64
	MaxLng float64
}

// Center returns the midpoint of the box.
func (b Box) Center() (lat, lng float64) {
	return (b.MinLat + b.MaxLat) / 2, (b.MinLng + b.MaxLng) / 2
}

// Encode returns a geohash of the given character precision (1-12).
// Coordinates outside WGS84 are clamped. Precision outside 1-12 is clamped.
func Encode(lat, lng float64, precision int) string {
	if precision < 1 {
		precision = 1
	}
	if precision > 12 {
		precision = 12
	}
	lat = clamp(lat, -90, 90)
	lng = clamp(lng, -180, 180)

	var out [12]byte
	minLat, maxLat := -90.0, 90.0
	minLng, maxLng := -180.0, 180.0
	var bit uint
	var ch byte
	even := true
	for i := 0; i < precision; {
		if even {
			mid := (minLng + maxLng) / 2
			if lng >= mid {
				ch |= 1 << (4 - bit)
				minLng = mid
			} else {
				maxLng = mid
			}
		} else {
			mid := (minLat + maxLat) / 2
			if lat >= mid {
				ch |= 1 << (4 - bit)
				minLat = mid
			} else {
				maxLat = mid
			}
		}
		even = !even
		if bit < 4 {
			bit++
			continue
		}
		out[i] = base32Alphabet[ch]
		i++
		bit = 0
		ch = 0
	}
	return string(out[:precision])
}

// BoundingBox decodes a geohash into the rectangle it represents.
func BoundingBox(hash string) (Box, error) {
	minLat, maxLat := -90.0, 90.0
	minLng, maxLng := -180.0, 180.0
	even := true
	for i := 0; i < len(hash); i++ {
		cd := decodeTable[hash[i]]
		if cd == 0xFF {
			return Box{}, errInvalidHash
		}
		for mask := byte(16); mask != 0; mask >>= 1 {
			if even {
				mid := (minLng + maxLng) / 2
				if cd&mask != 0 {
					minLng = mid
				} else {
					maxLng = mid
				}
			} else {
				mid := (minLat + maxLat) / 2
				if cd&mask != 0 {
					minLat = mid
				} else {
					maxLat = mid
				}
			}
			even = !even
		}
	}
	return Box{MinLat: minLat, MaxLat: maxLat, MinLng: minLng, MaxLng: maxLng}, nil
}

// DecodeCenter returns the center of the geohash cell.
func DecodeCenter(hash string) (lat, lng float64, err error) {
	box, err := BoundingBox(hash)
	if err != nil {
		return 0, 0, err
	}
	lat, lng = box.Center()
	return lat, lng, nil
}

// Neighbors returns the adjacent cells that exist. Hashes on the polar edge
// omit neighbors that would fall past the pole.
func Neighbors(hash string) []string {
	north, northOK := adjacent(hash, dirNorth)
	south, southOK := adjacent(hash, dirSouth)
	east, eastOK := adjacent(hash, dirEast)
	west, westOK := adjacent(hash, dirWest)

	out := make([]string, 0, 8)
	add := func(h string, ok bool) {
		if ok && h != "" {
			out = append(out, h)
		}
	}
	add(north, northOK)
	add(south, southOK)
	add(east, eastOK)
	add(west, westOK)
	if northOK {
		h, ok := adjacent(north, dirEast)
		add(h, ok)
		h, ok = adjacent(north, dirWest)
		add(h, ok)
	}
	if southOK {
		h, ok := adjacent(south, dirEast)
		add(h, ok)
		h, ok = adjacent(south, dirWest)
		add(h, ok)
	}
	return out
}

// Cover returns the center cell plus every cell within rings steps of it.
func Cover(hash string, rings int) []string {
	if hash == "" {
		return nil
	}
	if rings < 0 {
		rings = 0
	}
	seen := map[string]struct{}{hash: {}}
	frontier := []string{hash}
	out := []string{hash}
	for r := 0; r < rings; r++ {
		next := make([]string, 0, len(frontier)*2)
		for _, cell := range frontier {
			for _, n := range Neighbors(cell) {
				if _, ok := seen[n]; ok {
					continue
				}
				seen[n] = struct{}{}
				next = append(next, n)
				out = append(out, n)
			}
		}
		if len(next) == 0 {
			break
		}
		frontier = next
	}
	return out
}

// CellSizeMeters estimates the width (east-west) and height (north-south) of a
// precision-N cell at the given latitude.
func CellSizeMeters(precision int, lat float64) (width, height float64) {
	if precision < 1 {
		precision = 1
	}
	bits := precision * 5
	lngBits := (bits + 1) / 2
	latBits := bits / 2
	lngDeg := 360.0 / math.Ldexp(1, lngBits)
	latDeg := 180.0 / math.Ldexp(1, latBits)
	const metersPerDegree = 111_320.0
	width = lngDeg * metersPerDegree * math.Cos(lat*math.Pi/180)
	height = latDeg * metersPerDegree
	return width, height
}

// HaversineMeters returns the great-circle distance between two WGS84 points.
func HaversineMeters(lat1, lng1, lat2, lng2 float64) float64 {
	const earthRadius = 6_371_008.8
	p1 := lat1 * math.Pi / 180
	p2 := lat2 * math.Pi / 180
	dLat := p2 - p1
	dLng := (lng2 - lng1) * math.Pi / 180
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(p1)*math.Cos(p2)*math.Sin(dLng/2)*math.Sin(dLng/2)
	if h > 1 {
		h = 1
	}
	return 2 * earthRadius * math.Asin(math.Sqrt(h))
}

func adjacent(hash, dir string) (string, bool) {
	if hash == "" {
		return "", false
	}
	last := hash[len(hash)-1]
	parity := parEven
	if len(hash)%2 == 1 {
		parity = parOdd
	}
	parent := hash[:len(hash)-1]
	if strings.Contains(border[dir][parity], string(last)) {
		next, ok := adjacent(parent, dir)
		if !ok {
			return "", false
		}
		parent = next
	}
	idx := strings.IndexByte(neighbor[dir][parity], last)
	if idx < 0 || idx >= len(base32Alphabet) {
		return "", false
	}
	return parent + base32Alphabet[idx:idx+1], true
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type hashError string

func (e hashError) Error() string { return string(e) }

const errInvalidHash hashError = "invalid geohash"
