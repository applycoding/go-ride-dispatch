// Package metrics registers Prometheus collectors for the dispatch process.
package metrics

import (
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metrics is an isolated Prometheus registry for one process.
type Metrics struct {
	Registry      *prometheus.Registry
	HTTPRequests  *prometheus.CounterVec
	HTTPDuration  *prometheus.HistogramVec
	MatchDuration prometheus.Histogram
	MatchResults  *prometheus.CounterVec
	Transitions   *prometheus.CounterVec
	PublishErrors prometheus.Counter
	WSConnections prometheus.Gauge
}

// New registers the dispatch collectors.
func New() *Metrics {
	reg := prometheus.NewRegistry()
	m := &Metrics{
		Registry: reg,
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dispatch_http_requests_total",
			Help: "HTTP requests handled by the dispatch API.",
		}, []string{"method", "path", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "dispatch_http_request_duration_seconds",
			Help:    "HTTP request latency, excluding long-lived websocket handlers.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "path"}),
		MatchDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "dispatch_match_duration_seconds",
			Help:    "Time spent selecting a driver after a trip is stored.",
			Buckets: []float64{0.0005, 0.001, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1},
		}),
		MatchResults: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dispatch_match_results_total",
			Help: "Nearest-driver match outcomes.",
		}, []string{"result"}),
		Transitions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "dispatch_trip_transitions_total",
			Help: "Trip status values entered.",
		}, []string{"status"}),
		PublishErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "dispatch_event_publish_errors_total",
			Help: "Events that failed to publish.",
		}),
		WSConnections: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "dispatch_websocket_connections",
			Help: "Open websocket connections.",
		}),
	}
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.HTTPRequests,
		m.HTTPDuration,
		m.MatchDuration,
		m.MatchResults,
		m.Transitions,
		m.PublishErrors,
		m.WSConnections,
	)
	return m
}

// RecordMatch implements trip.Recorder.
func (m *Metrics) RecordMatch(elapsed time.Duration, matched bool) {
	m.MatchDuration.Observe(elapsed.Seconds())
	result := "unmatched"
	if matched {
		result = "assigned"
	}
	m.MatchResults.WithLabelValues(result).Inc()
}

// RecordTransition implements trip.Recorder.
func (m *Metrics) RecordTransition(status string) {
	m.Transitions.WithLabelValues(status).Inc()
}

// RecordPublishError implements trip.Recorder.
func (m *Metrics) RecordPublishError() {
	m.PublishErrors.Inc()
}

// ObserveHTTP records one finished HTTP request.
func (m *Metrics) ObserveHTTP(method, path string, status int, elapsed time.Duration, includeLatency bool) {
	if path == "" {
		path = "unmatched"
	}
	code := strconv.Itoa(status)
	m.HTTPRequests.WithLabelValues(method, path, code).Inc()
	if includeLatency {
		m.HTTPDuration.WithLabelValues(method, path).Observe(elapsed.Seconds())
	}
}
