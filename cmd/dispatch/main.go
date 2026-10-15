package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/redis/go-redis/v9"

	"github.com/applycoding/go-ride-dispatch/internal/api"
	"github.com/applycoding/go-ride-dispatch/internal/config"
	"github.com/applycoding/go-ride-dispatch/internal/events"
	"github.com/applycoding/go-ride-dispatch/internal/location"
	"github.com/applycoding/go-ride-dispatch/internal/metrics"
	"github.com/applycoding/go-ride-dispatch/internal/service"
	"github.com/applycoding/go-ride-dispatch/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("exit", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector())
	reg.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	m, err := metrics.New(reg)
	if err != nil {
		return err
	}

	repo, closeRepo, err := openRepo(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeRepo()

	locs, closeLocs, err := openLocations(ctx, cfg, log)
	if err != nil {
		return err
	}
	defer closeLocs()

	pub, closePub, err := openPublisher(cfg, log)
	if err != nil {
		return err
	}
	defer closePub()

	hub := api.NewHub(log)
	svc, err := service.New(service.Deps{
		Repo:              repo,
		Locations:         locs,
		Publisher:         pub,
		Listener:          hub,
		Fares:             cfg.Fare,
		MatchRadiusMeters: cfg.MatchRadiusMeters,
		LocationMaxAge:    cfg.LocationMaxAge,
		SpeedKmh:          cfg.SpeedKmh,
		RoadFactor:        cfg.RoadFactor,
		GeohashPrecision:  cfg.GeohashPrecision,
		Log:               log,
		Metrics:           m,
	})
	if err != nil {
		return err
	}
	if err := svc.Recover(ctx); err != nil {
		return fmt.Errorf("recover locations: %w", err)
	}

	apiSrv := api.New(svc, hub, log, m, func(c context.Context) error {
		pingCtx, cancel := context.WithTimeout(c, 2*time.Second)
		defer cancel()
		if err := repo.Ping(pingCtx); err != nil {
			return err
		}
		return locs.Ping(pingCtx)
	})

	// ReadTimeout and WriteTimeout are left unset so a WebSocket is not cut off
	// by the header-to-body deadline that http.Server applies to ordinary requests.
	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           apiSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.HTTPAddr)
		errCh <- httpSrv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("shutdown requested")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

func openRepo(ctx context.Context, cfg config.Config, log *slog.Logger) (store.Repository, func(), error) {
	if cfg.DatabaseURL == "" {
		log.Warn("DATABASE_URL is unset; trips are stored in memory")
		return store.NewMemory(), func() {}, nil
	}
	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, nil, fmt.Errorf("postgres: %w", err)
	}
	pg := store.NewPostgres(pool)
	if err := pg.Migrate(ctx); err != nil {
		pool.Close()
		return nil, nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, nil, fmt.Errorf("postgres ping: %w", err)
	}
	log.Info("repository", "backend", "postgres")
	return pg, pool.Close, nil
}

func openLocations(ctx context.Context, cfg config.Config, log *slog.Logger) (location.Store, func(), error) {
	if cfg.RedisAddr == "" {
		log.Warn("REDIS_ADDR is unset; locations are stored in memory")
		return location.NewMemory(), func() {}, nil
	}
	client := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, nil, fmt.Errorf("redis ping: %w", err)
	}
	log.Info("locations", "backend", "redis")
	return location.NewRedis(client), func() { _ = client.Close() }, nil
}

func openPublisher(cfg config.Config, log *slog.Logger) (events.Publisher, func(), error) {
	if len(cfg.KafkaBrokers) == 0 {
		log.Info("event publisher", "backend", "memory")
		return events.NewMemory(), func() {}, nil
	}
	publisher, err := events.NewKafka(cfg.KafkaBrokers, cfg.KafkaTopic, log)
	if err != nil {
		return nil, nil, err
	}
	log.Info("event publisher", "backend", "kafka", "topic", cfg.KafkaTopic)
	return publisher, func() { _ = publisher.Close() }, nil
}
