// Package api is the HTTP and WebSocket front door for the dispatcher.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/service"
)

// Server serves the dispatch API.
type Server struct {
	svc     *service.Service
	hub     *Hub
	log     *slog.Logger
	metrics *metrics.Metrics
	ready   func(context.Context) error
}

// New builds the API server. ready is called by /readyz.
func New(svc *service.Service, hub *Hub, log *slog.Logger, m *metrics.Metrics, ready func(context.Context) error) *Server {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if ready == nil {
		ready = func(context.Context) error { return nil }
	}
	return &Server{svc: svc, hub: hub, log: log, metrics: m, ready: ready}
}

// Handler returns the routed HTTP handler with access logs and metrics.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.route(mux, "GET /healthz", s.health)
	s.route(mux, "GET /readyz", s.readyz)
	s.route(mux, "GET /metrics", s.metricsHandler)
	s.route(mux, "POST /v1/riders", s.createRider)
	s.route(mux, "GET /v1/riders/{id}", s.getRider)
	s.route(mux, "POST /v1/drivers", s.createDriver)
	s.route(mux, "GET /v1/drivers/{id}", s.getDriver)
	s.route(mux, "POST /v1/drivers/{id}/status", s.setDriverStatus)
	s.route(mux, "POST /v1/drivers/{id}/location", s.ingestLocation)
	s.route(mux, "POST /v1/estimates", s.estimate)
	s.route(mux, "POST /v1/trips", s.createTrip)
	s.route(mux, "GET /v1/trips/{id}", s.getTrip)
	s.route(mux, "POST /v1/trips/{id}/match", s.matchTrip)
	s.route(mux, "POST /v1/trips/{id}/accept", s.driverAction(s.svc.AcceptTrip))
	s.route(mux, "POST /v1/trips/{id}/arrive", s.driverAction(s.svc.Arrive))
	s.route(mux, "POST /v1/trips/{id}/start", s.driverAction(s.svc.StartTrip))
	s.route(mux, "POST /v1/trips/{id}/complete", s.driverAction(s.svc.CompleteTrip))
	s.route(mux, "POST /v1/trips/{id}/cancel", s.cancelTrip)
	s.route(mux, "GET /v1/ws", s.hub.ServeHTTP)
	return mux
}

func (s *Server) route(mux *http.ServeMux, pattern string, h http.HandlerFunc) {
	mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = uuid.NewString()
		}
		w.Header().Set("X-Request-Id", requestID)
		start := time.Now()
		ww := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		h(ww, r)
		if s.metrics != nil {
			s.metrics.HTTPRequests.WithLabelValues(r.Method, pattern, strconv.Itoa(ww.status)).Inc()
			s.metrics.HTTPDuration.WithLabelValues(r.Method, pattern).Observe(time.Since(start).Seconds())
		}
		level := slog.LevelInfo
		switch r.URL.Path {
		case "/healthz", "/readyz", "/metrics":
			level = slog.LevelDebug
		}
		s.log.Log(r.Context(), level, "request",
			"method", r.Method,
			"path", r.URL.Path,
			"route", pattern,
			"status", ww.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"request_id", requestID,
		)
	})
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.ready(ctx); err != nil {
		s.log.Warn("not ready", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not ready"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if s.metrics == nil {
		http.NotFound(w, r)
		return
	}
	promhttp.HandlerFor(s.metrics.Reg, promhttp.HandlerOpts{}).ServeHTTP(w, r)
}

type errorBody struct {
	Error string `json:"error"`
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, err error) {
	status := http.StatusInternalServerError
	msg := "internal error"
	switch {
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
		msg = err.Error()
	case errors.Is(err, domain.ErrConflict), errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrDriverUnavailable):
		status = http.StatusConflict
		msg = err.Error()
	case errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
		msg = err.Error()
	default:
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeJSON(w, status, errorBody{Error: msg})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			return fmt.Errorf("%w: body too large", domain.ErrInvalid)
		}
		return fmt.Errorf("%w: %w", domain.ErrInvalid, err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w: trailing json", domain.ErrInvalid)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func surgeOrOne(surge *float64) float64 {
	if surge == nil {
		return 1
	}
	return *surge
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *statusWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	hj, ok := w.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacker not supported")
	}
	return hj.Hijack()
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
