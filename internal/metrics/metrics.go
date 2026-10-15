// Package metrics registers the Prometheus collectors used by the API and dispatcher.
package metrics

import "github.com/prometheus/client_golang/prometheus"

// Metrics is the process-wide set of dispatch instruments.
type Metrics struct {
	Reg             *prometheus.Registry
	HTTPRequests    *prometheus.CounterVec
	HTTPDuration    *prometheus.HistogramVec
	TripTransitions *prometheus.CounterVec
	MatchDuration   prometheus.Histogram
	MatchCandidates prometheus.Histogram
	LocationIngest  prometheus.Counter
	EventsPublished prometheus.Counter
	EventsFailed    prometheus.Counter
}

// New registers collectors on reg.
func New(reg *prometheus.Registry) (*Metrics, error) {
	m := &Metrics{
		Reg: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dispatch_http_requests_total",
			Help: "HTTP requests handled by the dispatch API.",
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dispatch_http_request_duration_seconds",
			Help:    "HTTP request latency.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		TripTransitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dispatch_trip_transitions_total",
			Help: "Trip state transitions.",
		}, []string{"from", "to"}),
		MatchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "dispatch_match_duration_seconds",
			Help:    "Time spent selecting a nearest driver.",
			Buckets: prometheus.DefBuckets,
		}),
		MatchCandidates: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "dispatch_match_candidates",
			Help:    "Drivers returned by the spatial index before filtering.",
			Buckets: []float64{0, 1, 2, 4, 8, 16, 32},
		}),
		LocationIngest: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dispatch_location_ingest_total",
			Help: "Driver location updates accepted.",
		}),
		EventsPublished: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dispatch_events_published_total",
			Help: "Trip events accepted by the publisher.",
		}),
		EventsFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dispatch_events_failed_total",
			Help: "Trip events the publisher rejected.",
		}),
	}
	collectors := []prometheus.Collector{
		m.HTTPRequests,
		m.HTTPDuration,
		m.TripTransitions,
		m.MatchDuration,
		m.MatchCandidates,
		m.LocationIngest,
		m.EventsPublished,
		m.EventsFailed,
	}
	for _, c := range collectors {
		if err := reg.Register(c); err != nil {
			return nil, err
		}
	}
	return m, nil
}
