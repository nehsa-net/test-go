package config_test

import (
	"strings"
	"testing"

	"github.com/nehsa-net/test-go/internal/config"
)

// envMap turns a map into the lookup function config.Load expects. This is why
// Load takes a function instead of calling os.Getenv: these tests run in
// parallel, and t.Setenv would forbid that (it panics in a parallel test,
// precisely because the process environment is shared mutable state).
func envMap(m map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := m[key]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(envMap(map[string]string{"WEATHER_API_KEY": "abc123"}))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", cfg.Addr)
	}
	if cfg.UpstreamURL != "https://api.openweathermap.org" {
		t.Errorf("UpstreamURL = %q, want the public API", cfg.UpstreamURL)
	}
	if cfg.RequestTimeout != 10 {
		t.Errorf("RequestTimeout = %d, want 10", cfg.RequestTimeout)
	}
	if cfg.DatabaseURL != "" {
		t.Errorf("DatabaseURL = %q, want empty (recording off by default)", cfg.DatabaseURL)
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(envMap(map[string]string{
		"WEATHER_API_KEY":         "abc123",
		"ADDR":                    ":9999",
		"WEATHER_UPSTREAM_URL":    "http://127.0.0.1:1234",
		"DATABASE_URL":            "postgres://localhost/test",
		"REQUEST_TIMEOUT_SECONDS": "3",
	}))
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Addr != ":9999" {
		t.Errorf("Addr = %q, want :9999", cfg.Addr)
	}
	if cfg.RequestTimeout != 3 {
		t.Errorf("RequestTimeout = %d, want 3", cfg.RequestTimeout)
	}
}

func TestLoadValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{
			name:    "missing api key",
			env:     map[string]string{},
			wantErr: "WEATHER_API_KEY is required",
		},
		{
			name:    "empty api key is treated as missing",
			env:     map[string]string{"WEATHER_API_KEY": ""},
			wantErr: "WEATHER_API_KEY is required",
		},
		{
			name:    "non-numeric timeout",
			env:     map[string]string{"WEATHER_API_KEY": "k", "REQUEST_TIMEOUT_SECONDS": "soon"},
			wantErr: "must be an integer",
		},
		{
			name:    "zero timeout",
			env:     map[string]string{"WEATHER_API_KEY": "k", "REQUEST_TIMEOUT_SECONDS": "0"},
			wantErr: "must be positive",
		},
		{
			name:    "negative timeout",
			env:     map[string]string{"WEATHER_API_KEY": "k", "REQUEST_TIMEOUT_SECONDS": "-5"},
			wantErr: "must be positive",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := config.Load(envMap(tc.env))

			if err == nil {
				t.Fatalf("Load() succeeded, want error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Load() error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// A config test worth writing in every repo: the error must not echo the secret
// it just read. This one fails loudly if somebody adds %q of the API key.
func TestLoadErrorsDoNotEchoSecrets(t *testing.T) {
	t.Parallel()

	const secret = "super-secret-key-value"

	_, err := config.Load(envMap(map[string]string{
		"WEATHER_API_KEY":         secret,
		"REQUEST_TIMEOUT_SECONDS": "not-a-number",
	}))
	if err == nil {
		t.Fatal("Load() succeeded, want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("error message leaked the API key: %v", err)
	}
}
