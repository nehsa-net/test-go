package weather_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/nehsa-net/test-go/internal/weather"
)

const validPayload = `{
	"name": "Cape Canaveral",
	"weather": [{"main": "Clouds", "description": "broken clouds"}],
	"main": {"temp": 27.5, "humidity": 74},
	"dt": 1723400000,
	"cod": 200
}`

// TestClientFetch covers the happy path and every failure branch.
//
// httptest.NewServer is a real HTTP server on a real loopback port, which makes
// this arguably an integration test of net/http. It stays in the unit tier
// because it has no dependency outside the process and needs no setup: the rule
// that matters is "can a developer run it with no environment", not purity.
func TestClientFetch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		body       string
		city       string
		wantErr    error
		wantDesc   string
		wantTempC  float64
		wantCity   string
		wantHumid  int
		wantQuery  string
		skipServer bool
	}{
		{
			name:      "success",
			status:    http.StatusOK,
			body:      validPayload,
			city:      "Cape Canaveral",
			wantDesc:  "Clouds",
			wantTempC: 27.5,
			wantCity:  "Cape Canaveral",
			wantHumid: 74,
			wantQuery: "Cape Canaveral",
		},
		{
			name:      "city is normalised before the request goes out",
			status:    http.StatusOK,
			body:      validPayload,
			city:      "  Cape   Canaveral ",
			wantDesc:  "Clouds",
			wantTempC: 27.5,
			wantCity:  "Cape Canaveral",
			wantHumid: 74,
			wantQuery: "Cape Canaveral",
		},
		{
			name:    "404 becomes ErrCityNotFound",
			status:  http.StatusNotFound,
			body:    `{"cod":"404","message":"city not found"}`,
			city:    "Atlantis",
			wantErr: weather.ErrCityNotFound,
		},
		{
			name:    "500 becomes ErrUpstream",
			status:  http.StatusInternalServerError,
			body:    `upstream exploded`,
			city:    "Orlando",
			wantErr: weather.ErrUpstream,
		},
		{
			name:    "401 becomes ErrUpstream",
			status:  http.StatusUnauthorized,
			body:    `{"cod":401,"message":"Invalid API key"}`,
			city:    "Orlando",
			wantErr: weather.ErrUpstream,
		},
		{
			name:    "malformed json becomes ErrUpstream",
			status:  http.StatusOK,
			body:    `{"name": "Orlando", "main": {`,
			city:    "Orlando",
			wantErr: weather.ErrUpstream,
		},
		{
			name:    "empty weather array becomes ErrUpstream",
			status:  http.StatusOK,
			body:    `{"name":"Orlando","weather":[],"main":{"temp":1,"humidity":1},"dt":1}`,
			city:    "Orlando",
			wantErr: weather.ErrUpstream,
		},
		{
			name:       "empty city never reaches the network",
			city:       "   ",
			wantErr:    weather.ErrInvalidCity,
			skipServer: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var gotQuery string
			var gotPath string
			var gotAccept string

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query().Get("q")
				gotPath = r.URL.Path
				gotAccept = r.Header.Get("Accept")
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			}))
			// t.Cleanup beats defer here: it runs even if the test calls
			// t.Fatal, and it keeps setup and teardown adjacent.
			t.Cleanup(srv.Close)

			client := weather.NewClient(srv.URL, "test-key", srv.Client())

			// Every test gets a context with a deadline. A hung upstream then
			// fails the test in seconds instead of blocking the run for 10m.
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()

			got, err := client.Fetch(ctx, tc.city)

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("Fetch() error = %v, want %v", err, tc.wantErr)
				}
				if tc.skipServer && gotPath != "" {
					t.Error("Fetch() hit the network for an invalid city; it should fail before the request")
				}
				return
			}
			if err != nil {
				t.Fatalf("Fetch() unexpected error: %v", err)
			}

			if got.City != tc.wantCity {
				t.Errorf("City = %q, want %q", got.City, tc.wantCity)
			}
			if got.Description != tc.wantDesc {
				t.Errorf("Description = %q, want %q", got.Description, tc.wantDesc)
			}
			if got.TempC != tc.wantTempC {
				t.Errorf("TempC = %v, want %v", got.TempC, tc.wantTempC)
			}
			if got.Humidity != tc.wantHumid {
				t.Errorf("Humidity = %d, want %d", got.Humidity, tc.wantHumid)
			}

			// Assert on the request the client *sent*, not only the response it
			// parsed. A client that silently drops the city parameter would
			// otherwise pass every response-shaped assertion above.
			if gotQuery != tc.wantQuery {
				t.Errorf("upstream received q=%q, want %q", gotQuery, tc.wantQuery)
			}
			if gotPath != "/data/2.5/weather" {
				t.Errorf("upstream path = %q, want /data/2.5/weather", gotPath)
			}
			if gotAccept != "application/json" {
				t.Errorf("Accept header = %q, want application/json", gotAccept)
			}
			if want := time.Unix(1723400000, 0).UTC(); !got.ObservedAt.Equal(want) {
				t.Errorf("ObservedAt = %v, want %v", got.ObservedAt, want)
			}
		})
	}
}

// stubDoer is the alternative to httptest when you need to simulate something a
// real server cannot easily produce — here, a transport-level failure.
type stubDoer struct {
	resp *http.Response
	err  error
}

func (s stubDoer) Do(*http.Request) (*http.Response, error) { return s.resp, s.err }

func TestClientFetchTransportError(t *testing.T) {
	t.Parallel()

	client := weather.NewClient("https://example.invalid", "key", stubDoer{
		err: errors.New("dial tcp: connection refused"),
	})

	_, err := client.Fetch(t.Context(), "Orlando")

	if !errors.Is(err, weather.ErrUpstream) {
		t.Fatalf("error = %v, want it to wrap ErrUpstream", err)
	}
	// The cause must survive wrapping, so operators can still read it in a log.
	if want := "connection refused"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q lost the underlying cause %q", err, want)
	}
}

func TestClientFetchHonoursContextCancellation(t *testing.T) {
	t.Parallel()

	// A server that never answers, to prove the caller's deadline wins.
	blocked := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-blocked
	}))
	t.Cleanup(func() {
		close(blocked)
		srv.Close()
	})

	client := weather.NewClient(srv.URL, "key", srv.Client())

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := client.Fetch(ctx, "Orlando")
	elapsed := time.Since(start)

	if !errors.Is(err, weather.ErrUpstream) {
		t.Fatalf("error = %v, want ErrUpstream", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("error = %v, want it to wrap context.DeadlineExceeded", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v; the context deadline was not honoured", elapsed)
	}
}
