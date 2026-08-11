//go:build integration

// Package integration_test exercises the seams: real router, real HTTP client,
// real TCP listener, real JSON encoding and decoding, real database.
//
// The build tag is what keeps this tier out of the default `go test ./...` run.
// Developers get a fast unit suite by default and opt in to the slow tiers
// explicitly, which is the only arrangement where people actually run the fast
// one on every save.
package integration_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nehsa-net/test-go/internal/httpapi"
	"github.com/nehsa-net/test-go/internal/testkit"
	"github.com/nehsa-net/test-go/internal/weather"
)

var update = flag.Bool("update", false, "rewrite golden files instead of comparing against them")

// stubUpstream stands in for OpenWeatherMap. It is a real HTTP server serving
// recorded fixtures, so everything between the router and the socket is the
// production code path — only the third party is replaced.
//
// Why not call the real API? Because a test that depends on somebody else's
// uptime, rate limit, and today's weather is not a test, it is a monitor. Keep
// the real call in a separate contract test that is allowed to fail loudly.
func stubUpstream(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		city := r.URL.Query().Get("q")

		if r.URL.Query().Get("appid") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"cod":401,"message":"Invalid API key"}`)
			return
		}

		fixture, err := os.ReadFile(fixtureName(city))
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"cod":"404","message":"city not found"}`)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture)
	}))
	t.Cleanup(srv.Close)

	return srv
}

// get issues a GET carrying the test's context, so a hung server fails the
// test instead of blocking the run.
func get(t *testing.T, url string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	return resp
}

func fixtureName(city string) string {
	switch city {
	case "Cape Canaveral":
		return "testdata/upstream_cape_canaveral.json"
	case "Orlando":
		return "testdata/upstream_orlando.json"
	default:
		return "testdata/does_not_exist.json"
	}
}

// newStack wires the real client, real service and real router together and
// serves them on a real port. This is the assembly main() performs, which is
// exactly the thing unit tests cannot check.
func newStack(t *testing.T, opts ...weather.Option) *httptest.Server {
	t.Helper()

	upstream := stubUpstream(t)
	client := weather.NewClient(upstream.URL, "integration-test-key", &http.Client{Timeout: 5 * time.Second})
	svc := weather.NewService(client, opts...)

	app := httptest.NewServer(httpapi.New(svc))
	t.Cleanup(app.Close)

	return app
}

func TestWeatherEndToEndThroughTheStack(t *testing.T) {
	app := newStack(t)

	var got weather.Report
	status := testkit.GetJSON(t.Context(), t, app.URL+"/weather?city=Cape+Canaveral&units=metric", &got)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got.City != "Cape Canaveral" {
		t.Errorf("City = %q, want Cape Canaveral", got.City)
	}
	if got.Temp != 27.5 {
		t.Errorf("Temp = %v, want 27.5 (metric)", got.Temp)
	}
	if got.Units != weather.Celsius {
		t.Errorf("Units = %q, want metric", got.Units)
	}
}

// The golden file pins the exact serialised response. It catches a renamed JSON
// tag, a dropped field, or a changed number format — none of which a
// field-by-field assertion notices unless somebody remembers to add the field.
func TestWeatherResponseMatchesGolden(t *testing.T) {
	app := newStack(t)

	resp := get(t, app.URL+"/weather?city=Orlando&units=imperial")
	defer func() { _ = resp.Body.Close() }()

	var pretty json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&pretty); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	formatted, err := json.MarshalIndent(pretty, "", "  ")
	if err != nil {
		t.Fatalf("formatting: %v", err)
	}

	testkit.Golden(t, "weather_orlando_imperial.json", append(formatted, '\n'), *update)
}

func TestUnknownCityIsA404ThroughTheStack(t *testing.T) {
	app := newStack(t)

	var body struct {
		Error string `json:"error"`
	}
	status := testkit.GetJSON(t.Context(), t, app.URL+"/weather?city=Atlantis", &body)

	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	if body.Error != "no weather for that city" {
		t.Errorf("error = %q, want the flat public sentence", body.Error)
	}
}

// This is the seam a unit test structurally cannot reach: the upstream rejects
// the credential, the client turns that into ErrUpstream, and the router turns
// that into a 502 whose body says nothing about API keys.
func TestUpstreamAuthFailureBecomes502(t *testing.T) {
	upstream := stubUpstream(t)
	client := weather.NewClient(upstream.URL, "", &http.Client{Timeout: 5 * time.Second}) // no key
	app := httptest.NewServer(httpapi.New(weather.NewService(client)))
	t.Cleanup(app.Close)

	resp := get(t, app.URL+"/weather?city=Orlando")
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", resp.StatusCode)
	}

	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decoding: %v", err)
	}
	for _, forbidden := range []string{"API key", "appid", "401", "Invalid"} {
		if v := body["error"]; strings.Contains(v, forbidden) {
			t.Errorf("502 body leaked upstream detail %q: %q", forbidden, v)
		}
	}
}
