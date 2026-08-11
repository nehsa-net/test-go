//go:build integration

package integration_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/nehsa-net/test-go/internal/store"
	"github.com/nehsa-net/test-go/internal/testkit"
	"github.com/nehsa-net/test-go/internal/weather"
)

// sharedDB is started once for the whole package by TestMain. Starting a
// container per test would be correct but unbearably slow; isolating per test
// with a unique schema or a rolled-back transaction is the usual compromise.
var sharedDB *sql.DB

// TestMain owns the expensive setup for the package. Note it calls os.Exit, so
// nothing deferred here runs — every cleanup has to happen before the call.
func TestMain(m *testing.M) {
	ctx := context.Background()

	container, db, err := startPostgres(ctx)
	if err != nil {
		// A missing Docker daemon is an environment problem, not a test failure.
		// Skipping loudly beats failing: it tells the developer what to start,
		// and it does not train anybody to ignore a red suite.
		fmt.Fprintf(os.Stderr, "\nSKIPPING integration DB tests: %v\n"+
			"Start Docker and re-run: make test-integration\n\n", err)
		os.Exit(0)
	}

	sharedDB = db
	code := m.Run()

	_ = db.Close()
	_ = testcontainers.TerminateContainer(container)
	os.Exit(code)
}

func startPostgres(ctx context.Context) (testcontainers.Container, *sql.DB, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	// A pinned image tag, never :latest. A test suite whose dependency changes
	// under it fails for reasons nobody can reproduce.
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("weather_test"),
		tcpostgres.WithUsername("test"),
		tcpostgres.WithPassword("test"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(60*time.Second),
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("starting postgres container: %w", err)
	}

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return container, nil, fmt.Errorf("resolving connection string: %w", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return container, nil, fmt.Errorf("opening database: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return container, nil, fmt.Errorf("pinging database: %w", err)
	}
	return container, db, nil
}

// newStore returns a Store with the schema applied and a guarantee that this
// test's rows are its own. Every test picks a unique city, which is cheaper than
// truncating between tests and keeps the package runnable in parallel.
func newStore(t *testing.T) (*store.Store, string) {
	t.Helper()

	if sharedDB == nil {
		t.Skip("no database available")
	}

	st := store.New(sharedDB)
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	city := fmt.Sprintf("City-%s", t.Name())
	t.Cleanup(func() {
		// Clean up on the way out, not on the way in: a failed test then leaves
		// its rows behind for inspection only if you disable this.
		//
		// context.Background(), NOT t.Context(): the test context is cancelled
		// just BEFORE cleanup functions run, so t.Context() here would abort the
		// delete every time and leave rows behind silently.
		_, _ = sharedDB.ExecContext(context.Background(), `DELETE FROM observations WHERE city = $1`, city)
	})
	return st, city
}

func TestStoreRecordAndRead(t *testing.T) {
	st, city := newStore(t)

	want := weather.Conditions{
		City:        city,
		Description: "Clouds",
		TempC:       27.5,
		Humidity:    74,
		ObservedAt:  time.Date(2026, 8, 11, 15, 4, 5, 0, time.UTC),
	}

	if err := st.Record(t.Context(), want); err != nil {
		t.Fatalf("Record() error: %v", err)
	}

	got, err := st.Latest(t.Context(), city)
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}

	// This is what an in-memory fake cannot tell you: whether NUMERIC(5,2)
	// round-trips the float, and whether TIMESTAMPTZ comes back as UTC.
	if got.TempC != want.TempC {
		t.Errorf("TempC round-tripped as %v, want %v", got.TempC, want.TempC)
	}
	if !got.ObservedAt.Equal(want.ObservedAt) {
		t.Errorf("ObservedAt round-tripped as %v, want %v", got.ObservedAt, want.ObservedAt)
	}
	if got.Description != want.Description {
		t.Errorf("Description = %q, want %q", got.Description, want.Description)
	}
}

func TestStoreLatestReturnsMostRecent(t *testing.T) {
	st, city := newStore(t)

	base := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	for i, temp := range []float64{20, 25, 22} {
		err := st.Record(t.Context(), weather.Conditions{
			City:        city,
			Description: "Clouds",
			TempC:       temp,
			Humidity:    50,
			ObservedAt:  base.Add(time.Duration(i) * time.Hour),
		})
		if err != nil {
			t.Fatalf("Record(%d) error: %v", i, err)
		}
	}

	got, err := st.Latest(t.Context(), city)
	if err != nil {
		t.Fatalf("Latest() error: %v", err)
	}
	if got.TempC != 22 {
		t.Errorf("TempC = %v, want 22 (the newest row, not the warmest)", got.TempC)
	}
}

// The unique constraint only exists in the database. Nothing in the Go code
// enforces it, so nothing but a real database can prove it works.
func TestStoreRejectsDuplicateObservation(t *testing.T) {
	st, city := newStore(t)

	obs := weather.Conditions{
		City:        city,
		Description: "Clouds",
		TempC:       27.5,
		Humidity:    74,
		ObservedAt:  time.Date(2026, 8, 11, 15, 4, 5, 0, time.UTC),
	}

	if err := st.Record(t.Context(), obs); err != nil {
		t.Fatalf("first Record() error: %v", err)
	}

	err := st.Record(t.Context(), obs)
	if !errors.Is(err, store.ErrDuplicate) {
		t.Fatalf("second Record() error = %v, want store.ErrDuplicate", err)
	}

	count, err := st.CountForCity(t.Context(), city)
	if err != nil {
		t.Fatalf("CountForCity() error: %v", err)
	}
	if count != 1 {
		t.Errorf("stored %d rows, want 1", count)
	}
}

// The CHECK constraint is likewise invisible to Go. A humidity of 150 is a bug
// somewhere upstream, and the database is the last line that catches it.
func TestStoreRejectsImpossibleHumidity(t *testing.T) {
	st, city := newStore(t)

	err := st.Record(t.Context(), weather.Conditions{
		City:        city,
		Description: "Clouds",
		TempC:       20,
		Humidity:    150,
		ObservedAt:  time.Now().UTC(),
	})

	if err == nil {
		t.Fatal("Record() accepted humidity=150; the CHECK constraint is missing")
	}
}

func TestStoreLatestUnknownCity(t *testing.T) {
	st, _ := newStore(t)

	_, err := st.Latest(t.Context(), "no-such-city-anywhere")

	if !errors.Is(err, weather.ErrCityNotFound) {
		t.Fatalf("Latest() error = %v, want weather.ErrCityNotFound", err)
	}
}

// The full vertical slice: HTTP request in, row in Postgres out. This is the
// only tier that proves the service actually wires the recorder to the store.
func TestRequestPersistsObservation(t *testing.T) {
	if sharedDB == nil {
		t.Skip("no database available")
	}

	st := store.New(sharedDB)
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("migrating: %v", err)
	}
	t.Cleanup(func() {
		_, _ = sharedDB.ExecContext(context.Background(), `DELETE FROM observations WHERE city = $1`, "Cape Canaveral")
	})

	app := newStack(t, weather.WithRecorder(st))

	var report weather.Report
	if status := testkit.GetJSON(t.Context(), t, app.URL+"/weather?city=Cape+Canaveral", &report); status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}

	stored, err := st.Latest(t.Context(), "Cape Canaveral")
	if err != nil {
		t.Fatalf("nothing was persisted: %v", err)
	}
	// Celsius in the database, Fahrenheit in the response — the store must hold
	// the canonical value, not whatever unit the caller happened to ask for.
	if stored.TempC != 27.5 {
		t.Errorf("stored TempC = %v, want 27.5 (canonical celsius)", stored.TempC)
	}
	if report.Temp != 81.5 {
		t.Errorf("response Temp = %v, want 81.5 (converted fahrenheit)", report.Temp)
	}
}

// Compile-time proof that Store satisfies the interface the service needs. If
// the signature drifts, this fails at build time instead of at 3am.
var _ weather.Recorder = (*store.Store)(nil)
