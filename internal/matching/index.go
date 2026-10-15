// Package matching keeps an in-process geohash grid of available drivers.
//
// A query hashes the pickup, expands a ring of neighboring cells wide enough
// to cover the search radius, then ranks those drivers by haversine distance.
// The grid only limits the candidate set. Distance, freshness, and driver
// status are applied by the caller.
package matching

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/geo"
)

const maxRings = 32

// Candidate is a driver inside the search radius.
type Candidate struct {
	DriverID       string
	Point          domain.Point
	DistanceMeters float64
	UpdatedAt      time.Time
}

type entry struct {
	point domain.Point
	at    time.Time
}

// Index is a geohash cell map guarded by a mutex.
type Index struct {
	mu        sync.RWMutex
	precision int
	cells     map[string]map[string]entry
	byDriver  map[string]string
}

// New returns an index that files drivers at the given geohash precision.
func New(precision int) (*Index, error) {
	if precision < 1 || precision > 12 {
		return nil, fmt.Errorf("matching: precision %d out of range", precision)
	}
	return &Index{
		precision: precision,
		cells:     make(map[string]map[string]entry),
		byDriver:  make(map[string]string),
	}, nil
}

// Upsert inserts or moves a driver.
func (idx *Index) Upsert(id string, p domain.Point, at time.Time) error {
	if id == "" {
		return fmt.Errorf("matching: empty driver id")
	}
	cell := geo.Encode(p.Lat, p.Lng, idx.precision)
	idx.mu.Lock()
	defer idx.mu.Unlock()
	if old, ok := idx.byDriver[id]; ok && old != cell {
		delete(idx.cells[old], id)
		if len(idx.cells[old]) == 0 {
			delete(idx.cells, old)
		}
	}
	bucket := idx.cells[cell]
	if bucket == nil {
		bucket = make(map[string]entry)
		idx.cells[cell] = bucket
	}
	bucket[id] = entry{point: p, at: at}
	idx.byDriver[id] = cell
	return nil
}

// Remove drops a driver. Removing an unknown driver is a no-op.
func (idx *Index) Remove(id string) {
	idx.mu.Lock()
	defer idx.mu.Unlock()
	cell, ok := idx.byDriver[id]
	if !ok {
		return
	}
	delete(idx.byDriver, id)
	delete(idx.cells[cell], id)
	if len(idx.cells[cell]) == 0 {
		delete(idx.cells, cell)
	}
}

// Len reports how many drivers are indexed.
func (idx *Index) Len() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.byDriver)
}

// Nearest returns up to limit drivers within maxRadiusMeters, closest first.
// Equal distances are ordered by driver id.
func (idx *Index) Nearest(p domain.Point, limit int, maxRadiusMeters float64) []Candidate {
	if limit <= 0 || maxRadiusMeters <= 0 {
		return nil
	}
	center := geo.Encode(p.Lat, p.Lng, idx.precision)
	cells := geo.Cover(center, idx.rings(p.Lat, maxRadiusMeters))

	idx.mu.RLock()
	cands := make([]Candidate, 0, 32)
	for _, cell := range cells {
		for id, e := range idx.cells[cell] {
			d := geo.HaversineMeters(p.Lat, p.Lng, e.point.Lat, e.point.Lng)
			if d > maxRadiusMeters {
				continue
			}
			cands = append(cands, Candidate{
				DriverID:       id,
				Point:          e.point,
				DistanceMeters: d,
				UpdatedAt:      e.at,
			})
		}
	}
	idx.mu.RUnlock()

	sort.Slice(cands, func(i, j int) bool {
		if cands[i].DistanceMeters == cands[j].DistanceMeters {
			return cands[i].DriverID < cands[j].DriverID
		}
		return cands[i].DistanceMeters < cands[j].DistanceMeters
	})
	if len(cands) > limit {
		cands = cands[:limit]
	}
	return cands
}

func (idx *Index) rings(lat, radius float64) int {
	width, height := geo.CellSizeMeters(idx.precision, lat)
	step := math.Min(math.Abs(width), math.Abs(height))
	if step < 1 {
		step = 1
	}
	// +2 covers a pickup that sits on the far corner of its own cell.
	rings := int(math.Ceil(radius/step)) + 2
	if rings < 1 {
		return 1
	}
	if rings > maxRings {
		return maxRings
	}
	return rings
}
