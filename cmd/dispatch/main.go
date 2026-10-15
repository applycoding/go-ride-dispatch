package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/api"
	"github.com/applycoding/go-ride-dispatch/internal/config"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/kafka"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/store"
	"github.com/applycoding/go-ride-dispatch/internal/trip"
)

func main() {
	cfg := config.Load()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, closeStore := openStore(ctx, cfg, logger)
	defer closeStore()

	bus := events.NewMemoryBus()
	publishers := []events.Publisher{bus}
	if len(cfg.KafkaBrokers) > 0 {
		writer := kafka.NewWriter(cfg.KafkaBrokers, cfg.KafkaTopic)
		defer func() { _ = writer.Close() }()
		publishers = append(publishers, events.NewKafkaAdapter(writer, cfg.KafkaTopic))
		logger.Info("kafka adapter enabled", "topic", cfg.KafkaTopic)
	} else {
		logger.Info("event bus is in-memory; set KAFKA_BROKERS to enable the kafka adapter")
	}

	recorder := metrics.New()
	svc := trip.NewService(st, events.Multi(publishers), recorder, cfg.MatchRadiusM, logger)
	httpServer := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           api.NewServer(svc, bus, recorder, logger).Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", httpServer.Addr)
		errc <- httpServer.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down")
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "err", err)
			os.Exit(1)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("shutdown", "err", err)
		os.Exit(1)
	}
}

func openStore(ctx context.Context, cfg config.Config, logger *slog.Logger) (store.Store, func()) {
	if cfg.DatabaseURL == "" {
		logger.Info("using in-memory store")
		return store.NewMemory(), func() {}
	}
	var pg *store.Postgres
	var err error
	for attempt := 1; attempt <= 10; attempt++ {
		pg, err = store.NewPostgres(ctx, cfg.DatabaseURL)
		if err == nil {
			logger.Info("using postgres store")
			return pg, pg.Close
		}
		logger.Info("waiting for postgres", "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			logger.Error("startup cancelled")
			os.Exit(1)
		case <-time.After(time.Duration(attempt) * 300 * time.Millisecond):
		}
	}
	logger.Error("postgres unavailable", "err", err)
	os.Exit(1)
	return nil, func() {}
}
