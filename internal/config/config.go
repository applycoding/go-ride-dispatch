// Package config reads process settings from the environment.
package config

import (
	"os"
	"strconv"
	"strings"
)

// Config is the runtime configuration of the dispatch process.
type Config struct {
	Port         string
	DatabaseURL  string
	KafkaBrokers []string
	KafkaTopic   string
	MatchRadiusM float64
}

// Load reads configuration from the environment.
// An empty DatabaseURL selects the in-memory store.
// Empty KafkaBrokers selects the in-memory event bus only.
func Load() Config {
	return Config{
		Port:         env("PORT", "8080"),
		DatabaseURL:  os.Getenv("DATABASE_URL"),
		KafkaBrokers: splitCSV(os.Getenv("KAFKA_BROKERS")),
		KafkaTopic:   env("KAFKA_TOPIC", "dispatch.events"),
		MatchRadiusM: envFloat("MATCH_RADIUS_M", 5000),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envFloat(key string, fallback float64) float64 {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f <= 0 {
		return fallback
	}
	return f
}

func splitCSV(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
