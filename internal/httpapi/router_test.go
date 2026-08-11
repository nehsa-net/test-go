package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/nehsa-net/test-go/internal/httpapi"
	"github.com/nehsa-net/test-go/internal/weather"
)

// fakeService lets the router test drive every status-code branch without a
// network, a client, or a real weather provider.
type fakeService struct {
	report weather.Report
	err    error

	lastCity  string
	lastUnits weather.Units
	calls     int
}

func (f *fakeService) Describe(_ context.Context, city string, units weather.Units) (weather.Report, error) {
	f.calls++
	f.lastCity = city
	f.lastUnits = units
	return f.report, f.err
}

var sampleReport = weather.Report{
	City:        "Cape Canaveral",
	Description: "Clouds",
	Temp:        81.5,
	Units:       weather.Fahrenheit,
	Humidity:    74,
	ObservedAt:  "2026-08-11T15:04:05Z",
}

// do drives the router in-process. httptest.NewRecorder plus ServeHTTP is the
// fastest possible HTTP test: no port is bound and nothing needs cleaning up.
func do(t *testing.T, svc httpapi.Describer, target string) *httptest.ResponseRecorder {
	t.Helper() // so a failure reports the caller's line, not this line

	router := httpapi.New(svc)
	// NewRequestWithContext rather than NewRequest: the handler passes the
	// request context down to the service, and a test that skips it cannot
	// prove cancellation propagates.
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	t.Parallel()

	rec := do(t, &fakeService{}, "/healthz")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestWeatherStatusCodes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		target     string
		svcErr     error
		wantStatus int
		wantBody   string // matched against the "error" field
	}{
		{
			name:       "success",
			target:     "/weather?city=Cape+Canaveral",
			wantStatus: http.StatusOK,
		},
		{
			name:       "unknown city is 404",
			target:     "/weather?city=Atlantis",
			svcErr:     weather.ErrCityNotFound,
			wantStatus: http.StatusNotFound,
			wantBody:   "no weather for that city",
		},
		{
			name:       "invalid city is 400",
			target:     "/weather?city=+",
			svcErr:     weather.ErrInvalidCity,
			wantStatus: http.StatusBadRequest,
			wantBody:   "city is required",
		},
		{
			name:       "upstream failure is 502",
			target:     "/weather?city=Orlando",
			svcErr:     weather.ErrUpstream,
			wantStatus: http.StatusBadGateway,
			wantBody:   "weather is unavailable right now",
		},
		{
			name:       "bad units is 400 before the service is called",
			target:     "/weather?city=Orlando&units=kelvin",
			wantStatus: http.StatusBadRequest,
			wantBody:   "units must be metric or imperial",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := do(t, &fakeService{report: sampleReport, err: tc.svcErr}, tc.target)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, tc.wantStatus, rec.Body)
			}
			if tc.wantBody == "" {
				return
			}

			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decoding body %q: %v", rec.Body, err)
			}
			if body.Error != tc.wantBody {
				t.Errorf("error = %q, want %q", body.Error, tc.wantBody)
			}
		})
	}
}

func TestWeatherResponseBody(t *testing.T) {
	t.Parallel()

	rec := do(t, &fakeService{report: sampleReport}, "/weather?city=Cape+Canaveral")

	if got, want := rec.Header().Get("Content-Type"), "application/json"; !strings.HasPrefix(got, want) {
		t.Errorf("Content-Type = %q, want prefix %q", got, want)
	}

	var got weather.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding body %q: %v", rec.Body, err)
	}
	if diff := cmp.Diff(sampleReport, got); diff != "" {
		t.Errorf("body mismatch (-want +got):\n%s", diff)
	}
}

// The query string is the router's contract with the browser. These assertions
// live here rather than in the service tests because parsing is the router's job.
func TestWeatherQueryParsing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		target    string
		wantCity  string
		wantUnits weather.Units
	}{
		{
			name:      "explicit city and units",
			target:    "/weather?city=Orlando&units=metric",
			wantCity:  "Orlando",
			wantUnits: weather.Celsius,
		},
		{
			name:      "missing city falls back to the default",
			target:    "/weather",
			wantCity:  "Cape Canaveral, FL",
			wantUnits: weather.Fahrenheit,
		},
		{
			name:      "missing units defaults to imperial",
			target:    "/weather?city=Orlando",
			wantCity:  "Orlando",
			wantUnits: weather.Fahrenheit,
		},
		{
			name:      "url-encoded city is decoded",
			target:    "/weather?city=Cape%20Canaveral%2C%20FL",
			wantCity:  "Cape Canaveral, FL",
			wantUnits: weather.Fahrenheit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			svc := &fakeService{report: sampleReport}
			do(t, svc, tc.target)

			if svc.lastCity != tc.wantCity {
				t.Errorf("service received city %q, want %q", svc.lastCity, tc.wantCity)
			}
			if svc.lastUnits != tc.wantUnits {
				t.Errorf("service received units %q, want %q", svc.lastUnits, tc.wantUnits)
			}
		})
	}
}

// A regression test for the rule that internal detail never reaches a caller.
func TestWeatherDoesNotLeakUpstreamDetail(t *testing.T) {
	t.Parallel()

	leaky := errorWithDetail{"dial tcp 10.0.0.4:443: connect: connection refused"}
	rec := do(t, &fakeService{err: leaky}, "/weather?city=Orlando")

	body := rec.Body.String()
	if strings.Contains(body, "10.0.0.4") || strings.Contains(body, "dial tcp") {
		t.Errorf("response leaked internal detail to the caller: %s", body)
	}
}

type errorWithDetail struct{ msg string }

func (e errorWithDetail) Error() string { return e.msg }
