//go:build e2e

// Package e2e_test builds the real binary, runs it as a separate process, and
// drives it only over HTTP.
//
// Nothing in this file imports the service's internal packages for anything but
// decoding a response. That restriction is the point: this tier answers "does
// the shipped artifact work", including the parts no in-process test can reach
// — flag and environment parsing, the wiring in main(), the listen address, and
// shutdown on SIGTERM.
package e2e_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/nehsa-net/test-go/internal/testkit"
)

// service is a handle on the running binary under test.
type service struct {
	baseURL string
	cmd     *exec.Cmd
	logs    *strings.Builder
}

// buildBinary compiles the service once per package run. Building inside each
// test would triple the runtime for no extra coverage.
func buildBinary(t *testing.T) string {
	t.Helper()

	binary := filepath.Join(t.TempDir(), "weatherd")

	// The e2e tier tests the artifact, so build it the way CI does rather than
	// running `go run`, which differs in signal handling and exit codes.
	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", binary, "./cmd/weatherd")
	cmd.Dir = repoRoot(t)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building weatherd: %v\n%s", err, out)
	}
	return binary
}

func repoRoot(t *testing.T) string {
	t.Helper()

	out, err := exec.CommandContext(t.Context(), "git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		// Fall back to walking up from the test's directory, so the suite still
		// runs from a tarball with no .git directory.
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			t.Fatalf("locating repo root: %v", wdErr)
		}
		return filepath.Clean(filepath.Join(wd, "..", ".."))
	}
	return strings.TrimSpace(string(out))
}

// startService launches the binary on a free port with the given environment
// and waits until it answers /healthz.
func startService(t *testing.T, binary string, env map[string]string) *service {
	t.Helper()

	port := testkit.FreePort(t)
	logs := &strings.Builder{}

	// context.Background(), not t.Context(): the test context is cancelled when
	// the test ends, which would kill the process before Cleanup can stop it
	// gracefully — and this test is specifically about graceful shutdown.
	cmd := exec.CommandContext(context.Background(), binary)
	cmd.Env = append(os.Environ(), fmt.Sprintf("ADDR=127.0.0.1:%d", port))
	for k, v := range env {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}
	cmd.Stdout = logs
	cmd.Stderr = logs

	if err := cmd.Start(); err != nil {
		t.Fatalf("starting weatherd: %v", err)
	}

	svc := &service{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", port),
		cmd:     cmd,
		logs:    logs,
	}

	t.Cleanup(func() {
		// Best-effort stop. A test that already killed the process gets an
		// error here, which is fine and deliberately ignored.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_, _ = cmd.Process.Wait()

		// Dump the service log only when the test failed. Printing it always
		// buries the one run you need to read.
		if t.Failed() {
			t.Logf("weatherd output:\n%s", logs.String())
		}
	})

	testkit.WaitForHTTP(t, svc.baseURL+"/healthz", 15*time.Second)
	return svc
}

// upstream returns a stub OpenWeatherMap the child process can reach over the
// loopback interface.
func upstream(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "Atlantis" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"cod":"404","message":"city not found"}`)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{
			"name": "Cape Canaveral",
			"weather": [{"main": "Clouds", "description": "broken clouds"}],
			"main": {"temp": 27.5, "humidity": 74},
			"dt": 1723400000,
			"cod": 200
		}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestServiceServesWeather(t *testing.T) {
	binary := buildBinary(t)
	stub := upstream(t)

	svc := startService(t, binary, map[string]string{
		"WEATHER_API_KEY":      "e2e-key",
		"WEATHER_UPSTREAM_URL": stub.URL,
	})

	var report struct {
		City     string  `json:"city"`
		Temp     float64 `json:"temp"`
		Units    string  `json:"units"`
		Humidity int     `json:"humidity"`
	}
	status := testkit.GetJSON(t.Context(), t, svc.baseURL+"/weather?city=Cape+Canaveral", &report)

	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if report.City != "Cape Canaveral" {
		t.Errorf("city = %q, want Cape Canaveral", report.City)
	}
	if report.Temp != 81.5 {
		t.Errorf("temp = %v, want 81.5 (imperial default)", report.Temp)
	}
	if report.Units != "imperial" {
		t.Errorf("units = %q, want imperial", report.Units)
	}
}

func TestServiceReturns404ForUnknownCity(t *testing.T) {
	binary := buildBinary(t)
	stub := upstream(t)

	svc := startService(t, binary, map[string]string{
		"WEATHER_API_KEY":      "e2e-key",
		"WEATHER_UPSTREAM_URL": stub.URL,
	})

	status := testkit.GetJSON(t.Context(), t, svc.baseURL+"/weather?city=Atlantis", nil)

	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
}

// Configuration errors are a main() concern, so this is the only tier that can
// check them. A service that starts happily without its API key and fails on
// the first request is strictly worse than one that refuses to start.
func TestServiceRefusesToStartWithoutAPIKey(t *testing.T) {
	binary := buildBinary(t)

	cmd := exec.CommandContext(t.Context(), binary)
	cmd.Env = append(os.Environ(), "ADDR=127.0.0.1:0", "WEATHER_API_KEY=")

	out, err := cmd.CombinedOutput()

	if err == nil {
		t.Fatal("service started without WEATHER_API_KEY; it should have exited non-zero")
	}
	if !strings.Contains(string(out), "WEATHER_API_KEY is required") {
		t.Errorf("output did not explain the problem:\n%s", out)
	}
}

// Graceful shutdown is invisible to every other tier. If this breaks, deploys
// drop in-flight requests and nobody notices until a customer does.
func TestServiceShutsDownGracefullyOnSIGTERM(t *testing.T) {
	binary := buildBinary(t)
	stub := upstream(t)

	svc := startService(t, binary, map[string]string{
		"WEATHER_API_KEY":      "e2e-key",
		"WEATHER_UPSTREAM_URL": stub.URL,
	})

	if err := svc.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- svc.cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("exited with %v, want a clean exit\n%s", err, svc.logs)
		}
		if !strings.Contains(svc.logs.String(), "stopped cleanly") {
			t.Errorf("service did not log a clean stop:\n%s", svc.logs)
		}
	case <-time.After(10 * time.Second):
		_ = svc.cmd.Process.Kill()
		t.Fatal("service ignored SIGTERM for 10s")
	}
}

// A smoke test worth copying into any service: the health endpoint must be
// cheap, unauthenticated, and independent of every downstream dependency.
// Here the upstream does not exist at all, and /healthz must still answer.
func TestHealthzDoesNotDependOnUpstream(t *testing.T) {
	binary := buildBinary(t)

	svc := startService(t, binary, map[string]string{
		"WEATHER_API_KEY":      "e2e-key",
		"WEATHER_UPSTREAM_URL": "http://127.0.0.1:1", // nothing listens here
	})

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var body map[string]string
	if status := testkit.GetJSON(ctx, t, svc.baseURL+"/healthz", &body); status != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", status)
	}
	if body["status"] != "ok" {
		t.Errorf("healthz body = %v, want status ok", body)
	}
}
