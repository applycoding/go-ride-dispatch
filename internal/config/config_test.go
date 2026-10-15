package config_test

import (
	"log/slog"
	"testing"
	"time"

	"github.com/applycoding/go-ride-dispatch/internal/config"
)

func TestFromEnvDefaults(t *testing.T) {
	cfg, err := config.FromEnv(func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":8080" || cfg.GeohashPrecision != 6 || cfg.MatchRadiusMeters != 5000 {
		t.Fatalf("defaults = %+v", cfg)
	}
	if cfg.Fare.BaseCents != 250 || cfg.Fare.Currency != "USD" {
		t.Fatalf("fare = %+v", cfg.Fare)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.ShutdownTimeout != 10*time.Second {
		t.Fatalf("log/shutdown = %v %v", cfg.LogLevel, cfg.ShutdownTimeout)
	}
	if len(cfg.KafkaBrokers) != 0 || cfg.KafkaTopic != "dispatch.trips" {
		t.Fatalf("kafka = %v %s", cfg.KafkaBrokers, cfg.KafkaTopic)
	}
}

func TestFromEnvOverrides(t *testing.T) {
	env := map[string]string{
		"HTTP_ADDR":           ":9090",
		"DATABASE_URL":        "postgres://localhost/dispatch",
		"REDIS_ADDR":          "localhost:6379",
		"KAFKA_BROKERS":       "a:9092, b:9092",
		"LOG_LEVEL":           "debug",
		"GEOHASH_PRECISION":   "5",
		"MATCH_RADIUS_METERS": "2500",
		"FARE_BASE_CENTS":     "0",
		"LOCATION_MAX_AGE":    "30s",
		"ROAD_FACTOR":         "1.4",
	}
	cfg, err := config.FromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.DatabaseURL == "" || cfg.RedisAddr == "" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.KafkaBrokers) != 2 || cfg.KafkaBrokers[1] != "b:9092" {
		t.Fatalf("brokers = %v", cfg.KafkaBrokers)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.GeohashPrecision != 5 || cfg.Fare.BaseCents != 0 {
		t.Fatalf("parsed = %+v", cfg)
	}
	if cfg.LocationMaxAge != 30*time.Second || cfg.RoadFactor != 1.4 || cfg.MatchRadiusMeters != 2500 {
		t.Fatalf("tunables = %+v", cfg)
	}
}

func TestFromEnvRejectsBadValues(t *testing.T) {
	cases := []map[string]string{
		{"LOG_LEVEL": "loud"},
		{"GEOHASH_PRECISION": "0"},
		{"GEOHASH_PRECISION": "nope"},
		{"MATCH_RADIUS_METERS": "-1"},
		{"SHUTDOWN_TIMEOUT": "0s"},
		{"FARE_BASE_CENTS": "-5"},
		{"REDIS_DB": "-1"},
		{"ROAD_FACTOR": "0"},
	}
	for _, env := range cases {
		_, err := config.FromEnv(func(k string) string { return env[k] })
		if err == nil {
			t.Fatalf("expected error for %v", env)
		}
	}
}
