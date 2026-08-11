// Package testkit holds helpers shared by the integration and e2e tiers.
//
// Everything here takes testing.TB and calls tb.Helper(), so a failure is
// reported at the caller's line rather than inside the helper. That one habit
// is the difference between "assertion failed in testkit.go:41" and a message
// that points at the test you actually need to read.
//
// The parameter is named tb rather than t by convention: it accepts *testing.T,
// *testing.B and *testing.F alike, and the linter (thelper) enforces the name.
package testkit

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// FreePort asks the kernel for an unused TCP port and returns it.
//
// Hardcoding a port makes tests fail when run in parallel, on a busy machine,
// or twice at once in CI. There is a small race between closing the listener
// and the service binding it, which is unavoidable and in practice harmless.
func FreePort(tb testing.TB) int {
	tb.Helper()

	var lc net.ListenConfig
	listener, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("testkit: reserving a port: %v", err)
	}
	defer func() {
		if cerr := listener.Close(); cerr != nil {
			tb.Logf("testkit: closing probe listener: %v", cerr)
		}
	}()

	return listener.Addr().(*net.TCPAddr).Port
}

// WaitForHTTP polls url until it answers 200 or the deadline passes.
//
// This replaces time.Sleep. A sleep is either too short (flaky) or too long
// (slow), and it is wrong on a different machine either way; polling is correct
// on both and finishes as soon as the service is actually up.
func WaitForHTTP(tb testing.TB, url string, timeout time.Duration) {
	tb.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := &http.Client{Timeout: time.Second}

	var lastErr error
	for {
		if ctx.Err() != nil {
			tb.Fatalf("testkit: %s never became ready within %s (last error: %v)", url, timeout, lastErr)
		}

		lastErr = probe(ctx, client, url)
		if lastErr == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func probe(ctx context.Context, client *http.Client, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}

// GetJSON issues a GET and decodes the body into out, returning the status code.
// Passing nil for out skips decoding, which is what you want when only the
// status matters.
func GetJSON(ctx context.Context, tb testing.TB, url string, out any) int {
	tb.Helper()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		tb.Fatalf("testkit: building request for %s: %v", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		tb.Fatalf("testkit: GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			tb.Fatalf("testkit: decoding response from %s: %v", url, err)
		}
	}
	return resp.StatusCode
}

// Golden compares got against the recorded file in testdata, and rewrites the
// file instead of failing when -update is passed:
//
//	go test ./... -update
//
// Golden files earn their keep for large, stable payloads where an inline
// literal would swamp the test. They are a liability for small values — a
// wrong golden file is still "passing", so review the diff on every update.
func Golden(tb testing.TB, name string, got []byte, update bool) {
	tb.Helper()

	path := filepath.Join("testdata", name)

	if update {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			tb.Fatalf("testkit: creating testdata dir: %v", err)
		}
		if err := os.WriteFile(path, got, 0o600); err != nil {
			tb.Fatalf("testkit: writing golden file %s: %v", path, err)
		}
		tb.Logf("testkit: updated golden file %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		tb.Fatalf("testkit: reading golden file %s: %v (run: go test ./... -update)", path, err)
	}
	if string(got) != string(want) {
		tb.Errorf("golden mismatch for %s\n--- want ---\n%s\n--- got ---\n%s", path, want, got)
	}
}
