package api

import (
	"context"
	"net/http"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
)

type nameBody struct {
	Name string `json:"name"`
}

type pointBody struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

func (p pointBody) point() domain.Point { return domain.Point{Lat: p.Lat, Lng: p.Lng} }

type statusBody struct {
	Status domain.DriverStatus `json:"status"`
}

type estimateBody struct {
	Pickup  domain.Point `json:"pickup"`
	Dropoff domain.Point `json:"dropoff"`
	Surge   *float64     `json:"surge"`
}

type tripBody struct {
	RiderID string       `json:"rider_id"`
	Pickup  domain.Point `json:"pickup"`
	Dropoff domain.Point `json:"dropoff"`
	Surge   *float64     `json:"surge"`
}

type driverBody struct {
	DriverID string `json:"driver_id"`
}

type cancelBody struct {
	ActorID string `json:"actor_id"`
}

type locationResponse struct {
	DriverID     string              `json:"driver_id"`
	Lat          float64             `json:"lat"`
	Lng          float64             `json:"lng"`
	UpdatedAt    time.Time           `json:"updated_at"`
	Status       domain.DriverStatus `json:"status"`
	Dispatchable bool                `json:"dispatchable"`
}

func (s *Server) createRider(w http.ResponseWriter, r *http.Request) {
	var body nameBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	rider, err := s.svc.RegisterRider(r.Context(), body.Name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rider)
}

func (s *Server) getRider(w http.ResponseWriter, r *http.Request) {
	rider, err := s.svc.GetRider(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rider)
}

func (s *Server) createDriver(w http.ResponseWriter, r *http.Request) {
	var body nameBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	driver, err := s.svc.RegisterDriver(r.Context(), body.Name)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, driver)
}

func (s *Server) getDriver(w http.ResponseWriter, r *http.Request) {
	driver, err := s.svc.GetDriver(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, driver)
}

func (s *Server) setDriverStatus(w http.ResponseWriter, r *http.Request) {
	var body statusBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	driver, err := s.svc.SetDriverStatus(r.Context(), r.PathValue("id"), body.Status)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, driver)
}

func (s *Server) ingestLocation(w http.ResponseWriter, r *http.Request) {
	var body pointBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	loc, status, err := s.svc.IngestLocation(r.Context(), r.PathValue("id"), body.point())
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, locationResponse{
		DriverID:     loc.DriverID,
		Lat:          loc.Point.Lat,
		Lng:          loc.Point.Lng,
		UpdatedAt:    loc.UpdatedAt,
		Status:       status,
		Dispatchable: status == domain.DriverAvailable,
	})
}

func (s *Server) estimate(w http.ResponseWriter, r *http.Request) {
	var body estimateBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	quote, err := s.svc.Estimate(r.Context(), body.Pickup, body.Dropoff, surgeOrOne(body.Surge))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, quote)
}

func (s *Server) createTrip(w http.ResponseWriter, r *http.Request) {
	var body tripBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	trip, err := s.svc.RequestTrip(r.Context(), body.RiderID, body.Pickup, body.Dropoff, surgeOrOne(body.Surge))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, trip)
}

func (s *Server) getTrip(w http.ResponseWriter, r *http.Request) {
	trip, err := s.svc.GetTrip(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trip)
}

func (s *Server) matchTrip(w http.ResponseWriter, r *http.Request) {
	trip, err := s.svc.MatchTrip(r.Context(), r.PathValue("id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trip)
}

func (s *Server) cancelTrip(w http.ResponseWriter, r *http.Request) {
	var body cancelBody
	if err := decodeJSON(w, r, &body); err != nil {
		s.writeError(w, r, err)
		return
	}
	trip, err := s.svc.CancelTrip(r.Context(), r.PathValue("id"), body.ActorID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, trip)
}

func (s *Server) driverAction(fn func(context.Context, string, string) (domain.Trip, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body driverBody
		if err := decodeJSON(w, r, &body); err != nil {
			s.writeError(w, r, err)
			return
		}
		trip, err := fn(r.Context(), r.PathValue("id"), body.DriverID)
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, trip)
	}
}
