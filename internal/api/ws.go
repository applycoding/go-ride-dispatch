package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"github.com/applycoding/go-ride-dispatch/internal/domain"
	"github.com/applycoding/go-ride-dispatch/internal/events"
)

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	s.serveSocket(w, r, splitCSV(r.URL.Query().Get("types")), "")
}

func (s *Server) handleDriverStream(w http.ResponseWriter, r *http.Request) {
	driverID := r.PathValue("id")
	if _, err := s.svc.GetDriver(r.Context(), driverID); err != nil {
		writeError(w, s.log, err)
		return
	}
	s.serveSocket(w, r, nil, driverID)
}

func (s *Server) serveSocket(w http.ResponseWriter, r *http.Request, types []string, driverID string) {
	if s.bus == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "event stream unavailable"})
		return
	}
	// Subscribe before the handshake finishes so a location sent immediately
	// after dial is not published into an empty subscriber list.
	ch, unsub := s.bus.Subscribe(types...)
	defer unsub()

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		s.log.Error("websocket accept", "err", err)
		return
	}
	defer func() { _ = conn.Close(websocket.StatusNormalClosure, "") }()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	s.metrics.WSConnections.Inc()
	defer s.metrics.WSConnections.Dec()

	errc := make(chan error, 2)
	go func() {
		errc <- s.writeEvents(ctx, conn, ch, driverID)
	}()
	go func() {
		if driverID == "" {
			errc <- discardReads(ctx, conn)
			return
		}
		errc <- s.readLocations(ctx, conn, driverID)
	}()

	err = <-errc
	cancel()
	_ = conn.CloseNow()
	<-errc
	if err != nil && !benignClose(err) {
		s.log.Info("websocket closed", "err", err)
	}
}

func (s *Server) writeEvents(ctx context.Context, conn *websocket.Conn, ch <-chan events.Event, driverID string) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-ch:
			if !ok {
				return nil
			}
			if driverID != "" && event.DriverID != driverID {
				continue
			}
			body, err := json.Marshal(event)
			if err != nil {
				return err
			}
			writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err = conn.Write(writeCtx, websocket.MessageText, body)
			cancel()
			if err != nil {
				return err
			}
		}
	}
}

func (s *Server) readLocations(ctx context.Context, conn *websocket.Conn, driverID string) error {
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return err
		}
		var point domain.Point
		if err := json.Unmarshal(data, &point); err != nil || !point.Valid() {
			continue
		}
		if _, err := s.svc.UpdateLocation(ctx, driverID, point); err != nil {
			return err
		}
	}
}

func discardReads(ctx context.Context, conn *websocket.Conn) error {
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			return err
		}
	}
}

func splitCSV(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func benignClose(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return true
	}
	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway, websocket.StatusNoStatusRcvd:
		return true
	default:
		return false
	}
}
