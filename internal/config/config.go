// Package config reads runtime settings from the environment.
//
// Reading a secret from a file next to the binary (as the original services do)
// cannot be tested without writing that file, and cannot be deployed without
// baking a secret into an image. The environment is both testable and the shape
// every container runtime already speaks.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config is the fully-resolved runtime configuration.
type Config struct {
	Addr           string
	UpstreamURL    string
	APIKey         string
	DatabaseURL    string // optional; empty disables recording
	RequestTimeout int    // seconds
}

// Load resolves configuration from the environment, applying defaults.
//
// It takes a lookup function rather than calling os.Getenv directly, so a test
// can pass a map. That is the same seam as weather.Doer, applied to the
// environment instead of the network.
func Load(lookup func(string) (string, bool)) (Config, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}

	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok && v != "" {
			return v
		}
		return fallback
	}

	cfg := Config{
		Addr:        get("ADDR", ":8080"),
		UpstreamURL: get("WEATHER_UPSTREAM_URL", "https://api.openweathermap.org"),
		APIKey:      get("WEATHER_API_KEY", ""),
		DatabaseURL: get("DATABASE_URL", ""),
	}

	timeout := get("REQUEST_TIMEOUT_SECONDS", "10")
	n, err := strconv.Atoi(timeout)
	if err != nil {
		return Config{}, fmt.Errorf("REQUEST_TIMEOUT_SECONDS must be an integer, got %q", timeout)
	}
	if n <= 0 {
		return Config{}, fmt.Errorf("REQUEST_TIMEOUT_SECONDS must be positive, got %d", n)
	}
	cfg.RequestTimeout = n

	if cfg.APIKey == "" {
		return Config{}, fmt.Errorf("WEATHER_API_KEY is required")
	}
	return cfg, nil
}
