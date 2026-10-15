// Package config loads dispatch settings from the environment.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/fare"
)

// Config is the process configuration.
type Config struct {
	HTTPAddr          string
	DatabaseURL       string
	RedisAddr         string
	RedisPassword     string
	RedisDB           int
	KafkaBrokers      []string
	KafkaTopic        string
	LogLevel          slog.Level
	ShutdownTimeout   time.Duration
	GeohashPrecision  int
	MatchRadiusMeters float64
	LocationMaxAge    time.Duration
	Fare              fare.Params
	SpeedKmh          float64
	RoadFactor        float64
}

// Load reads configuration from the process environment.
func Load() (Config, error) {
	return FromEnv(os.Getenv)
}

// FromEnv reads configuration using getenv. Empty optional values keep defaults.
func FromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{
		HTTPAddr:          ":8080",
		KafkaTopic:        "dispatch.trips",
		LogLevel:          slog.LevelInfo,
		ShutdownTimeout:   10 * time.Second,
		GeohashPrecision:  6,
		MatchRadiusMeters: 5000,
		LocationMaxAge:    2 * time.Minute,
		Fare: fare.Params{
			BaseCents:   250,
			PerKmCents:  150,
			PerMinCents: 30,
			Currency:    "USD",
		},
		SpeedKmh:   fare.DefaultSpeedKmh,
		RoadFactor: fare.DefaultRoadFactor,
	}
	if v := getenv("HTTP_ADDR"); v != "" {
		cfg.HTTPAddr = v
	}
	cfg.DatabaseURL = getenv("DATABASE_URL")
	cfg.RedisAddr = getenv("REDIS_ADDR")
	cfg.RedisPassword = getenv("REDIS_PASSWORD")
	if v := getenv("KAFKA_TOPIC"); v != "" {
		cfg.KafkaTopic = v
	}
	if v := getenv("KAFKA_BROKERS"); v != "" {
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				cfg.KafkaBrokers = append(cfg.KafkaBrokers, part)
			}
		}
	}
	if err := parseLogLevel(getenv("LOG_LEVEL"), &cfg.LogLevel); err != nil {
		return Config{}, err
	}
	if err := parseDuration(getenv, "SHUTDOWN_TIMEOUT", &cfg.ShutdownTimeout); err != nil {
		return Config{}, err
	}
	if err := parseDuration(getenv, "LOCATION_MAX_AGE", &cfg.LocationMaxAge); err != nil {
		return Config{}, err
	}
	if err := parseInt(getenv, "GEOHASH_PRECISION", &cfg.GeohashPrecision); err != nil {
		return Config{}, err
	}
	if cfg.GeohashPrecision < 1 || cfg.GeohashPrecision > 12 {
		return Config{}, fmt.Errorf("GEOHASH_PRECISION must be 1-12")
	}
	if err := parseInt(getenv, "REDIS_DB", &cfg.RedisDB); err != nil {
		return Config{}, err
	}
	if cfg.RedisDB < 0 {
		return Config{}, fmt.Errorf("REDIS_DB must be >= 0")
	}
	if err := parseFloat(getenv, "MATCH_RADIUS_METERS", &cfg.MatchRadiusMeters); err != nil {
		return Config{}, err
	}
	if cfg.MatchRadiusMeters <= 0 {
		return Config{}, fmt.Errorf("MATCH_RADIUS_METERS must be > 0")
	}
	if err := parseInt64(getenv, "FARE_BASE_CENTS", &cfg.Fare.BaseCents); err != nil {
		return Config{}, err
	}
	if err := parseInt64(getenv, "FARE_PER_KM_CENTS", &cfg.Fare.PerKmCents); err != nil {
		return Config{}, err
	}
	if err := parseInt64(getenv, "FARE_PER_MIN_CENTS", &cfg.Fare.PerMinCents); err != nil {
		return Config{}, err
	}
	if v := getenv("FARE_CURRENCY"); v != "" {
		cfg.Fare.Currency = v
	}
	if err := parseFloat(getenv, "FARE_SPEED_KMH", &cfg.SpeedKmh); err != nil {
		return Config{}, err
	}
	if err := parseFloat(getenv, "ROAD_FACTOR", &cfg.RoadFactor); err != nil {
		return Config{}, err
	}
	if cfg.SpeedKmh <= 0 || cfg.RoadFactor <= 0 {
		return Config{}, fmt.Errorf("FARE_SPEED_KMH and ROAD_FACTOR must be > 0")
	}
	return cfg, nil
}

func parseLogLevel(v string, dst *slog.Level) error {
	if v == "" {
		return nil
	}
	switch strings.ToLower(v) {
	case "debug":
		*dst = slog.LevelDebug
	case "info":
		*dst = slog.LevelInfo
	case "warn", "warning":
		*dst = slog.LevelWarn
	case "error":
		*dst = slog.LevelError
	default:
		return fmt.Errorf("LOG_LEVEL must be debug, info, warn, or error")
	}
	return nil
}

func parseDuration(getenv func(string) string, key string, dst *time.Duration) error {
	v := getenv(key)
	if v == "" {
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return fmt.Errorf("%s: want a positive duration", key)
	}
	*dst = d
	return nil
}

func parseInt(getenv func(string) string, key string, dst *int) error {
	v := getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = n
	return nil
}

func parseInt64(getenv func(string) string, key string, dst *int64) error {
	v := getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return fmt.Errorf("%s: want a non-negative integer", key)
	}
	*dst = n
	return nil
}

func parseFloat(getenv func(string) string, key string, dst *float64) error {
	v := getenv(key)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	*dst = n
	return nil
}
