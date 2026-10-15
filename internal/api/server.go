// Package api exposes the dispatch service over HTTP and WebSocket.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

// Server is the HTTP front door.
type Server struct {
	svc     *trip.Service
	bus     events.Subscriber
	metrics *metrics.Metrics
	log     *slog.Logger
	mux     *http.ServeMux
}

// NewServer wires routes. bus may be nil when websocket streams are unused.
func NewServer(svc *trip.Service, bus events.Subscriber, m *metrics.Metrics, logger *slog.Logger) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{svc: svc, bus: bus, metrics: m, log: logger, mux: http.NewServeMux()}
	s.routes()
	return s
}

// Handler returns the instrumented HTTP handler.
func (s *Server) Handler() http.Handler {
	return s.instrument(s.mux)
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("GET /readyz", s.handleReady)
	s.mux.Handle("GET /metrics", promhttp.HandlerFor(s.metrics.Registry, promhttp.HandlerOpts{}))
	s.mux.HandleFunc("POST /v1/drivers", s.handleRegisterDriver)
	s.mux.HandleFunc("GET /v1/drivers", s.handleNearbyDrivers)
	s.mux.HandleFunc("GET /v1/drivers/{id}", s.handleGetDriver)
	s.mux.HandleFunc("POST /v1/drivers/{id}/location", s.handleLocation)
	s.mux.HandleFunc("POST /v1/drivers/{id}/status", s.handleDriverStatus)
	s.mux.HandleFunc("GET /v1/drivers/{id}/stream", s.handleDriverStream)
	s.mux.HandleFunc("POST /v1/trips", s.handleRequestTrip)
	s.mux.HandleFunc("GET /v1/trips/{id}", s.handleGetTrip)
	s.mux.HandleFunc("POST /v1/trips/{id}/en-route", s.handleEnRoute)
	s.mux.HandleFunc("POST /v1/trips/{id}/complete", s.handleComplete)
	s.mux.HandleFunc("POST /v1/trips/{id}/cancel", s.handleCancel)
	s.mux.HandleFunc("GET /v1/ws", s.handleEventStream)
}

func (s *Server) instrument(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		_, path := s.mux.Handler(r)
		next.ServeHTTP(rec, r)
		streaming := strings.Contains(path, "/stream") || strings.HasSuffix(path, "/ws")
		s.metrics.ObserveHTTP(r.Method, path, rec.status, time.Since(start), !streaming)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.svc.Ping(r.Context()); err != nil {
		s.log.Error("ready check", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleRegisterDriver(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	driver, err := s.svc.RegisterDriver(r.Context(), req.ID, req.Name)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	w.Header().Set("Location", "/v1/drivers/"+driver.ID)
	writeJSON(w, http.StatusCreated, driver)
}

func (s *Server) handleGetDriver(w http.ResponseWriter, r *http.Request) {
	driver, err := s.svc.GetDriver(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, driver)
}

func (s *Server) handleLocation(w http.ResponseWriter, r *http.Request) {
	var point domain.Point
	if err := decodeJSON(w, r, &point); err != nil {
		writeError(w, s.log, err)
		return
	}
	driver, err := s.svc.UpdateLocation(r.Context(), r.PathValue("id"), point)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, driver)
}

func (s *Server) handleDriverStatus(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Status domain.DriverStatus `json:"status"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	driver, err := s.svc.SetStatus(r.Context(), r.PathValue("id"), req.Status)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, driver)
}

func (s *Server) handleNearbyDrivers(w http.ResponseWriter, r *http.Request) {
	point, radius, limit, err := nearbyQuery(r)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	drivers, err := s.svc.NearbyDrivers(r.Context(), point, radius, limit)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	if drivers == nil {
		drivers = []domain.Driver{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"drivers": drivers})
}

func (s *Server) handleRequestTrip(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RiderID string       `json:"rider_id"`
		Pickup  domain.Point `json:"pickup"`
		Dropoff domain.Point `json:"dropoff"`
		RadiusM float64      `json:"radius_m"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeError(w, s.log, err)
		return
	}
	trip, err := s.svc.RequestTrip(r.Context(), req.RiderID, req.Pickup, req.Dropoff, req.RadiusM)
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	w.Header().Set("Location", "/v1/trips/"+trip.ID)
	writeJSON(w, http.StatusCreated, trip)
}

func (s *Server) handleGetTrip(w http.ResponseWriter, r *http.Request) {
	trip, err := s.svc.GetTrip(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, trip)
}

func (s *Server) handleEnRoute(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, s.svc.MarkEnRoute)
}

func (s *Server) handleComplete(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, s.svc.CompleteTrip)
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request) {
	s.transition(w, r, s.svc.CancelTrip)
}

func (s *Server) transition(w http.ResponseWriter, r *http.Request, fn func(context.Context, string) (domain.Trip, error)) {
	updated, err := fn(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, s.log, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func nearbyQuery(r *http.Request) (domain.Point, float64, int, error) {
	q := r.URL.Query()
	lat, err := strconv.ParseFloat(q.Get("lat"), 64)
	if err != nil {
		return domain.Point{}, 0, 0, fmt.Errorf("%w: lat", domain.ErrInvalid)
	}
	lng, err := strconv.ParseFloat(q.Get("lng"), 64)
	if err != nil {
		return domain.Point{}, 0, 0, fmt.Errorf("%w: lng", domain.ErrInvalid)
	}
	var radius float64
	if raw := q.Get("radius_m"); raw != "" {
		radius, err = strconv.ParseFloat(raw, 64)
		if err != nil {
			return domain.Point{}, 0, 0, fmt.Errorf("%w: radius_m", domain.ErrInvalid)
		}
	}
	var limit int
	if raw := q.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil {
			return domain.Point{}, 0, 0, fmt.Errorf("%w: limit", domain.ErrInvalid)
		}
	}
	return domain.Point{Lat: lat, Lng: lng}, radius, limit, nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmtError(err)
	}
	return nil
}

func fmtError(err error) error {
	return errors.Join(domain.ErrInvalid, err)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		return
	}
}

func writeError(w http.ResponseWriter, logger *slog.Logger, err error) {
	status := http.StatusInternalServerError
	msg := "internal error"
	switch {
	case errors.Is(err, domain.ErrInvalid):
		status = http.StatusBadRequest
		msg = err.Error()
	case errors.Is(err, domain.ErrNotFound):
		status = http.StatusNotFound
		msg = err.Error()
	case errors.Is(err, domain.ErrConflict):
		status = http.StatusConflict
		msg = err.Error()
	case errors.Is(err, domain.ErrInvalidTransition), errors.Is(err, domain.ErrDriverUnavailable), errors.Is(err, domain.ErrDriverBusy):
		status = http.StatusConflict
		msg = err.Error()
	default:
		logger.Error("request failed", "err", err)
	}
	writeJSON(w, status, map[string]string{"error": msg})
}
