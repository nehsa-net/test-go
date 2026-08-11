# Go Test Framework — Reference

A working, runnable reference for testing Go services at all three tiers: unit,
integration, and end-to-end. Everything in this repo compiles and passes. Copy
the patterns, not just the words.

The subject under test is `weatherd`, a small Gin HTTP service that calls an
upstream weather API and optionally records observations in Postgres. It is
deliberately shaped like the real services in this organisation.

**Repo:** `github.com/nehsa-net/test-go` · **Licence:** MIT

---

## Pasting this into OneNote

Open the README on GitHub in a browser (the rendered view, not the "Raw" view),
select the article body, copy, and paste into OneNote. Headings, tables, and
code blocks survive intact.

Pasting from the raw `.md` file gives you plain text with `#` and backticks
showing. If that happens, you copied the wrong view.

Two small fixes after pasting, if you care about appearance:

- Code blocks paste with OneNote's default font. Select all, set to Consolas 10pt.
- Wide tables paste narrow. Drag the right edge of the table to widen.

---

## The three tiers

Each tier answers a question the others structurally cannot. A repo with only
one of them has a coverage number, not a safety net.

| Tier | Answers | Runs against | Speed | Needs |
|---|---|---|---|---|
| **Unit** | Does this function do what it says? Every branch, every error path. | Fakes and in-process stubs. No I/O. | ~1s | Nothing |
| **Integration** | Do the seams hold? Real HTTP, real SQL, real serialisation. | A real Postgres in Docker, a real TCP listener. | ~15s | Docker |
| **E2E** | Does the shipped binary work? Config, wiring, startup, shutdown. | The compiled artifact, driven only over HTTP. | ~3s | Nothing |

The rule that decides which tier a test belongs in: **write it at the cheapest
tier that can actually fail for the right reason.** A test that mocks the thing
it is meant to be testing belongs one tier up.

---

## Quick start

```bash
git clone git@github.com:nehsa-net/test-go.git
cd test-go

make test              # unit tier — no setup at all
make test-integration  # needs Docker running
make test-e2e          # builds the binary and drives it
make test-all          # all three, fastest first
make help              # every target
```

If `go` is not found, this machine keeps it at `~/.local/go/bin`:

```bash
export PATH=$HOME/.local/go/bin:$PATH
```

For `make lint`, install the linter once (it lands in `~/go/bin`):

```bash
go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest
export PATH=$HOME/go/bin:$PATH
```

This repo lints clean: `0 issues`. The `.golangci.yml` here is worth copying
verbatim — in particular it sets `build-tags: [integration, e2e]`, without which
every linter silently skips two thirds of the suite and still reports success.

---

## How it works

### Layout

```
cmd/weatherd/main.go        the binary — wires dependencies, nothing else
internal/weather/
    model.go                pure values, parsing, conversion
    client.go               the ONE place that does network I/O
    service.go              orchestration over an interface
internal/httpapi/router.go  gin routes, status codes, query parsing
internal/store/store.go     Postgres persistence
internal/config/config.go   environment parsing
internal/testkit/           helpers shared by the slow tiers
test/integration/           //go:build integration
test/e2e/                   //go:build e2e
```

### Why the tiers can exist at all: the seams

A test tier is not something you add to code. It is something the code's shape
either permits or forbids. Three seams do all the work here, and a service
missing them cannot be unit tested at any price.

**1. The HTTP client is an interface, not `*http.Client`.**

```go
type Doer interface {
    Do(req *http.Request) (*http.Response, error)
}

type Client struct {
    BaseURL string   // a field, so a test can point it at httptest.Server
    APIKey  string
    HTTP    Doer     // an interface, so a test can inject a stub
}
```

`BaseURL` being a field rather than a constant is the single change that makes
the entire integration tier possible.

**2. Dependencies arrive as interfaces declared by the consumer.**

```go
// Declared in service.go, next to the code that USES it — not next to Client.
// Narrow by design: Service needs one method, so the interface has one method.
type Provider interface {
    Fetch(ctx context.Context, city string) (Conditions, error)
}
```

A one-method interface is three lines to fake. This is why Go codebases rarely
need a mocking framework.

**3. Time and environment are injected, not read from globals.**

```go
svc := weather.NewService(provider, weather.WithClock(func() time.Time { return frozen }))
cfg, err := config.Load(func(k string) (string, bool) { return envMap[k], true })
```

Any assertion about elapsed time that calls `time.Now()` is a race against the
wall clock — it passes locally and fails in CI at 23:59:59.

### How the tiers are separated

Build tags. The integration and e2e files start with:

```go
//go:build integration
```

So `go test ./...` compiles and runs only the unit tier. Everything else is
opt-in via `-tags`. This is what keeps the default command fast enough that
people actually run it.

---

## Running each tier

### Unit tier

```bash
make test
# or: go test -race ./...
```

No Docker, no network, no environment variables, no setup. If this tier ever
needs setup, something has leaked into the wrong tier.

**Actual output:**

```
go test -race -timeout=60s ./...
?   	github.com/nehsa-net/test-go/cmd/weatherd	[no test files]
ok  	github.com/nehsa-net/test-go/internal/config	1.009s
ok  	github.com/nehsa-net/test-go/internal/httpapi	1.024s
?   	github.com/nehsa-net/test-go/internal/store	[no test files]
?   	github.com/nehsa-net/test-go/internal/testkit	[no test files]
ok  	github.com/nehsa-net/test-go/internal/weather	1.117s
```

84 test cases pass — 26 top-level tests and 58 subtests, 0 failures.
`[no test files]` on `cmd/` and `testkit/` is correct and
expected — `main()` is not unit-testable by design, and helpers are exercised by
the tiers that use them.

`-race` is not optional. It roughly doubles the runtime and catches the class of
bug that is otherwise found in production at 3am.

Useful variations:

```bash
go test -v ./internal/weather              # verbose, one package
go test -run TestParseUnits ./...          # one test by name (regex)
go test -run 'TestParseUnits/metric' ./... # one subtest
go test -count=1 ./...                     # bypass the result cache
go test -count=20 -race ./internal/weather # hunt a flaky test
```

### Integration tier

```bash
make test-integration
# or: go test -tags=integration -race ./test/integration/...
```

**Requires Docker.** Testcontainers starts a real `postgres:17-alpine`, applies
the schema, runs the tests against it, and tears it down.

**Actual output:**

```
=== RUN   TestWeatherEndToEndThroughTheStack
--- PASS: TestWeatherEndToEndThroughTheStack (0.00s)
=== RUN   TestWeatherResponseMatchesGolden
--- PASS: TestWeatherResponseMatchesGolden (0.00s)
=== RUN   TestUnknownCityIsA404ThroughTheStack
--- PASS: TestUnknownCityIsA404ThroughTheStack (0.00s)
=== RUN   TestUpstreamAuthFailureBecomes502
--- PASS: TestUpstreamAuthFailureBecomes502 (0.00s)
=== RUN   TestStoreRecordAndRead
--- PASS: TestStoreRecordAndRead (0.01s)
=== RUN   TestStoreLatestReturnsMostRecent
--- PASS: TestStoreLatestReturnsMostRecent (0.00s)
=== RUN   TestStoreRejectsDuplicateObservation
--- PASS: TestStoreRejectsDuplicateObservation (0.00s)
=== RUN   TestStoreRejectsImpossibleHumidity
--- PASS: TestStoreRejectsImpossibleHumidity (0.00s)
=== RUN   TestStoreLatestUnknownCity
--- PASS: TestStoreLatestUnknownCity (0.00s)
=== RUN   TestRequestPersistsObservation
--- PASS: TestRequestPersistsObservation (0.00s)
PASS
ok  	github.com/nehsa-net/test-go/test/integration	1.769s
```

10 tests pass. The first run also pulls the Postgres image, which takes about
12 seconds; afterwards the image is cached and the suite runs in under 2.

Without Docker the tier skips with an explanation rather than failing:

```
SKIPPING integration DB tests: starting postgres container: ...
Start Docker and re-run: make test-integration
```

That is a deliberate choice. A missing daemon is an environment problem, and
failing on it trains people to ignore red suites.

**What this tier proves that a unit test cannot:**

- `NUMERIC(5,2)` round-trips the float, and `TIMESTAMPTZ` comes back as UTC.
- The `UNIQUE (city, observed_at)` constraint actually fires — that rule lives
  only in the database, so only a database can prove it.
- The `CHECK (humidity BETWEEN 0 AND 100)` constraint rejects bad data.
- The response body serialises to exactly the expected JSON (golden file).
- An upstream 401 becomes a 502 whose body mentions no API key.

### E2E tier

```bash
make test-e2e
# or: go test -tags=e2e ./test/e2e/...
```

Compiles `cmd/weatherd` with `go build`, runs the binary as a subprocess on a
free port, and drives it only over HTTP.

**Actual output:**

```
=== RUN   TestServiceServesWeather
--- PASS: TestServiceServesWeather (0.57s)
=== RUN   TestServiceReturns404ForUnknownCity
--- PASS: TestServiceReturns404ForUnknownCity (0.54s)
=== RUN   TestServiceRefusesToStartWithoutAPIKey
--- PASS: TestServiceRefusesToStartWithoutAPIKey (0.48s)
=== RUN   TestServiceShutsDownGracefullyOnSIGTERM
--- PASS: TestServiceShutsDownGracefullyOnSIGTERM (0.54s)
=== RUN   TestHealthzDoesNotDependOnUpstream
--- PASS: TestHealthzDoesNotDependOnUpstream (0.54s)
PASS
ok  	github.com/nehsa-net/test-go/test/e2e	2.679s
```

**What this tier proves that nothing else can:**

- The service refuses to start without `WEATHER_API_KEY`, instead of starting
  happily and failing on the first request.
- `SIGTERM` produces a clean exit, so deploys do not drop in-flight requests.
- `/healthz` answers even when the upstream is unreachable — otherwise the load
  balancer pulls every instance out during an upstream blip.
- The real `ADDR` env var is parsed by the real `main()`.

Note it builds the binary rather than using `go run`: `go run` differs in signal
handling and exit codes, so it would not answer the two questions above.

### All tiers

```bash
make test-all   # unit, then integration, then e2e — fastest failure first
make ci         # what GitHub Actions runs: fmt-check, lint, all tiers
```

---

## Coverage

```bash
make cover        # per-function summary
make cover-html   # line-by-line in a browser
make cover-all    # merged across all three tiers
```

**Actual output, unit tier only:**

```
ok  	github.com/nehsa-net/test-go/internal/config	coverage: 94.1% of statements
ok  	github.com/nehsa-net/test-go/internal/httpapi	coverage: 100.0% of statements
	github.com/nehsa-net/test-go/internal/store	coverage: 0.0% of statements
ok  	github.com/nehsa-net/test-go/internal/weather	coverage: 94.1% of statements
total:	(statements)	45.6%
```

**Merged across all tiers: 71.5%.**

Read those two numbers together, because either alone misleads. `store` shows
0% at the unit tier and is thoroughly tested — by the integration tier, which is
the only tier that *can* test it. A team that gates on unit coverage alone
pressures people into writing mocked store tests that prove nothing.

Two caveats worth knowing:

- **Coverage counts statements executed, not assertions made.** A test that runs
  a function and asserts nothing scores identically to one that checks every
  field. Coverage finds untested code; it cannot find untested behaviour.
- **The e2e tier reports low coverage (12.7%) and that is an artifact.** The
  binary runs in a separate process, so the coverage tooling cannot see it
  without `GOCOVERDIR` instrumentation. Do not "fix" this by deleting e2e tests.

---

## Patterns worth copying

### Table-driven tests

The Go idiom. One row per case, each named, so a failure names the case.

```go
func TestParseUnits(t *testing.T) {
    t.Parallel()

    tests := []struct {
        name    string
        input   string
        want    weather.Units
        wantErr error
    }{
        {name: "empty defaults to imperial", input: "", want: weather.Fahrenheit},
        {name: "mixed case is accepted", input: "MeTrIc", want: weather.Celsius},
        {name: "kelvin is rejected", input: "kelvin", wantErr: weather.ErrInvalidUnits},
    }

    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()

            got, err := weather.ParseUnits(tc.input)

            if !errors.Is(err, tc.wantErr) {
                t.Fatalf("ParseUnits(%q) error = %v, want %v", tc.input, err, tc.wantErr)
            }
            if got != tc.want {
                t.Errorf("ParseUnits(%q) = %q, want %q", tc.input, got, tc.want)
            }
        })
    }
}
```

Adding a case is one line. Compare that to the copy-paste alternative, where
adding a case means duplicating a function and editing two values in it.

### `t.Fatalf` vs `t.Errorf`

| | Behaviour | Use when |
|---|---|---|
| `t.Errorf` | Records the failure, test continues | Independent checks — report all of them in one run |
| `t.Fatalf` | Records and stops this test immediately | Nothing below makes sense — a nil result, a failed setup |

Getting this backwards produces the most confusing failure mode in Go testing: an
assertion fails, execution continues, and the next line panics on a nil pointer.
The report shows the panic, not the assertion that explains it.

### Compare errors with `errors.Is`, never strings

```go
if !errors.Is(err, weather.ErrCityNotFound) {   // correct
if err.Error() != "weather: city not found" {   // breaks on any added context
```

Which is why the package defines sentinels and wraps with `%w`:

```go
var ErrCityNotFound = errors.New("weather: city not found")

return fmt.Errorf("%w: %q", ErrCityNotFound, city)   // context added, identity kept
```

### Fakes, not mocking frameworks

```go
type fakeProvider struct {
    conditions weather.Conditions
    err        error

    calls    int      // recorded so the test can assert on interactions
    lastCity string
}

func (f *fakeProvider) Fetch(_ context.Context, city string) (weather.Conditions, error) {
    f.calls++
    f.lastCity = city
    return f.conditions, f.err
}
```

Twelve lines, no dependency, no code generation, and it records exactly what
this test needs. Reach for `mockgen` when an interface has a dozen methods —
which is usually a signal the interface is too wide.

### `t.Cleanup` over `defer`

```go
srv := httptest.NewServer(handler)
t.Cleanup(srv.Close)
```

`t.Cleanup` runs even when the test calls `t.Fatal`, and it can be registered
inside a helper — where a `defer` would fire when the *helper* returns, not the
test. Keeping setup and teardown on adjacent lines is a bonus.

### `t.Helper()` in every helper

```go
func do(t *testing.T, svc httpapi.Describer, target string) *httptest.ResponseRecorder {
    t.Helper()   // failures report the CALLER's line, not this one
    ...
}
```

Without it, every failure points at the same line inside the helper and you have
to read the stack to find which test actually broke.

### `httptest` — two forms, two purposes

```go
// In-process. No port bound, nothing to clean up. Fastest possible HTTP test.
req := httptest.NewRequest(http.MethodGet, "/weather?city=Orlando", nil)
rec := httptest.NewRecorder()
router.ServeHTTP(rec, req)

// A real server on a real loopback port. Use when the code under test must
// make a real outbound request.
srv := httptest.NewServer(handler)
t.Cleanup(srv.Close)
client := weather.NewClient(srv.URL, "key", srv.Client())
```

### Assert on the request that was *sent*, not only the response parsed

```go
srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    gotQuery = r.URL.Query().Get("q")   // capture what arrived
    ...
}))
...
if gotQuery != "Cape Canaveral" {
    t.Errorf("upstream received q=%q, want %q", gotQuery, "Cape Canaveral")
}
```

A client that silently drops the city parameter passes every response-shaped
assertion. Only inspecting the outbound request catches it.

### Parallel tests

```go
func TestThing(t *testing.T) {
    t.Parallel()                    // this test runs alongside its siblings
    for _, tc := range tests {
        t.Run(tc.name, func(t *testing.T) {
            t.Parallel()            // and so do the subtests
            ...
        })
    }
}
```

Since Go 1.22 the loop-variable capture bug is gone, so `tc := tc` is no longer
needed. Two things still bite: parallel subtests finish *after* the parent
returns, so anything the parent defers has already run; and `t.Setenv` panics in
a parallel test, because the process environment is shared. That is exactly why
`config.Load` takes a lookup function instead of calling `os.Getenv`.

### `t.Context()`

```go
ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
defer cancel()
```

`t.Context()` (Go 1.24+) is cancelled automatically when the test finishes, so a
hung request cannot outlive its test.

### Golden files

```go
testkit.Golden(t, "weather_orlando_imperial.json", formatted, *update)
```

```bash
make golden   # rewrites them; ALWAYS review the diff before committing
```

Worth it for large, stable payloads where an inline literal would swamp the
test — a renamed JSON tag or a dropped field is caught for free. The risk is
that `-update` makes any failure disappear, so a wrong golden file still
"passes". Review the diff every single time.

### Fuzzing

```go
func FuzzNormaliseCity(f *testing.F) {
    for _, seed := range []string{"", " ", "Orlando", "日本 東京"} {
        f.Add(seed)
    }
    f.Fuzz(func(t *testing.T, input string) {
        got, err := weather.NormaliseCity(input)
        if err == nil && got == "" {
            t.Error("success result must not be empty")
        }
    })
}
```

```bash
make fuzz   # 30 seconds; run longer when hunting
```

Fuzz targets run as ordinary tests against their seeds in a normal `go test`
run, and only explore when `-fuzz` is passed. Assert *properties* ("never
panics", "never returns empty on success"), not specific outputs. A crash gets
written to `testdata/fuzz/` — commit it; it becomes a permanent regression test.

### Benchmarks

```go
func BenchmarkConvertTemp(b *testing.B) {
    for b.Loop() {                      // Go 1.24+; replaces for i := 0; i < b.N; i++
        weather.ConvertTemp(21.456, weather.Fahrenheit)
    }
}
```

```bash
make bench   # -benchmem shows allocations, usually the more useful number
```

### Compile-time interface checks

```go
var _ weather.Recorder = (*store.Store)(nil)
```

One line, and a signature drift fails the build instead of failing at runtime.

### stdlib vs testify

Both styles appear in `internal/weather/` against identical subject code —
`service_test.go` (stdlib) and `service_testify_test.go` (testify) — so you can
compare and pick. **Pick one per repo and stay with it.**

| | stdlib | testify |
|---|---|---|
| Dependency | none | large tree |
| Style | explicit, verbose | terse, familiar from JUnit/Jest |
| Stop on failure | `t.Fatalf` | `require.X` |
| Continue on failure | `t.Errorf` | `assert.X` |

The rule that matters in testify: `require` stops, `assert` continues. Using
`assert` where `require` belongs gives you a panic instead of a readable failure.

And in both styles: **never compare floats with `==`.** Use `math.Abs(got-want) <
1e-9` or `assert.InDelta`.

---

## Setting this up in a new repo

### Step 1 — Create the seams first

This is the whole job. Tests are easy once the code permits them, and impossible
before. Three changes, in order:

**Return errors; never `panic` in a library path.**

```go
// Before — the process dies, and no test can assert on it
if resp.StatusCode != http.StatusOK {
    panic("error: status code should be 200 but got: " + resp.Status)
}

// After — the caller decides, and a test can check which error came back
if resp.StatusCode != http.StatusOK {
    return nil, fmt.Errorf("%w: status %d", ErrUpstream, resp.StatusCode)
}
```

**Make every external address a field or parameter.**

```go
// Before — hardcoded; the test must reach the real internet
url := "https://api.openweathermap.org/data/2.5/weather"

// After — injectable; the test points it at httptest.Server
type Client struct { BaseURL string }
```

**Read secrets from the environment, not from a file next to the binary.**

```go
// Before — needs a file on disk to run at all, and bakes a secret into the image
file, err := os.Open("openweatherapi.key")

// After — testable, and what every container runtime already speaks
apiKey := os.Getenv("WEATHER_API_KEY")
```

**Move logic out of `main()` and out of route closures.** Nothing inside
`func main()` can be called from a test. A handler defined inline in
`router.GET("/x", func(c *gin.Context) { ...30 lines... })` is equally
unreachable. Extract to a named function that takes its dependencies.

### Step 2 — Copy the scaffolding

```bash
# From the root of the target repo:
cp <test-go>/Makefile .
cp <test-go>/.golangci.yml .
cp -r <test-go>/.github/workflows/test.yml .github/workflows/
mkdir -p internal/testkit test/integration test/e2e
cp <test-go>/internal/testkit/testkit.go internal/testkit/
```

Then edit `Makefile` — the only thing that changes is the binary path in the
e2e target and the package globs.

### Step 3 — Fix the module path

Both Go services here declare `module main`, which cannot be imported:

```
module main        <- nothing can import this
```

Change it to a real path, and update every internal import:

```
module github.com/nehsa-net/weather-microservice-go-gin
```

### Step 4 — Add the dependencies

```bash
go get github.com/google/go-cmp@latest
go get github.com/testcontainers/testcontainers-go@latest        # integration tier only
go get github.com/testcontainers/testcontainers-go/modules/postgres@latest
go mod tidy
```

### Step 5 — Write the tiers in this order

1. **Unit tests for the pure functions first.** Parsing, conversion, validation.
   Fastest to write, and they force the seams to be real.
2. **Unit tests for the client** with `httptest.NewServer` — happy path plus
   every status code the upstream can return.
3. **Unit tests for the router** with `httptest.NewRecorder` and a fake service —
   one per status code branch.
4. **Integration tests** for anything crossing a process boundary: SQL, the
   assembled stack, serialisation.
5. **E2E tests** for the binary: config validation, startup, shutdown, health.

### Step 6 — Wire the gate

Copy `.github/workflows/test.yml`, then in GitHub set branch protection on
`main` to require the **All tiers green** check. A tier that runs only on
somebody's laptop is documentation, not a gate.

---

## Applying this to the two Go services

Both are single-file `package main` services with no tests. Neither can be unit
tested as written, and the reason is the same in each: the code that makes
decisions and the code that does I/O are the same code.

### weather-microservice-go-gin

| Blocker | Fix |
|---|---|
| `module main` | Rename to `github.com/nehsa-net/weather-microservice-go-gin` |
| API key read from `openweatherapi.key` on disk | `os.Getenv("WEATHER_API_KEY")` |
| Upstream URL hardcoded in `getWeatherv25` | `Client.BaseURL` field |
| `http.Get` called directly | Inject a `Doer` |
| All handlers are inline closures in `main()` | Extract to named funcs taking a service |
| `sendGetRequest` indexes `weatherData.Weather[0]` unchecked | Return `ErrUpstream` on an empty slice — this panics today on any error payload |
| Three handlers duplicate city/units parsing | One parse helper, unit tested once |

Map onto this repo: `weather.Client` ⇄ `sendGetRequest` + `getWeatherv25`,
`weather.Service` ⇄ `getWeatherJson`/`getWeatherTemp`/`getWeatherWords`,
`httpapi.New` ⇄ the `router.GET` block in `main()`.

### webscraper-microservice-go-gin

| Blocker | Fix |
|---|---|
| `module scraper` | Rename to `github.com/nehsa-net/webscraper-microservice-go-gin` |
| Four `panic()` calls in `getScrapedData` | Return wrapped errors |
| `http.Get` called directly | Inject a `Doer` |
| Selector list is a local variable | Package-level var, so a test can table over it |
| No URL validation — accepts any scheme and any host | Validate before fetching |
| `scraper.exe` is committed | Delete it; add to `.gitignore` |

The scraper's selector-fallback loop is the ideal table-driven unit test: feed it
recorded HTML fixtures and assert which selector wins for each. Its integration
tier serves those fixtures from `httptest.Server`.

**Security note for the scraper, worth a test of its own:** a service that
fetches an arbitrary caller-supplied URL will happily fetch
`http://169.254.169.254/` (cloud metadata) or `http://localhost:5432`. Validate
the scheme and reject private and loopback address ranges, and write the test
that proves it.

---

## Troubleshooting

| Symptom | Cause | Fix |
|---|---|---|
| `go: command not found` | Go is not on PATH on this machine | `export PATH=$HOME/.local/go/bin:$PATH` |
| Integration tier skips with "SKIPPING" | Docker is not running | Start Docker, re-run |
| `panic: test executed panic(nil)` | A `panic(nil)` in the code under test | Return an error instead |
| Test passes alone, fails in the suite | Shared state between tests | Give each test unique data; check for package-level vars |
| Test passes locally, fails in CI | Timing, or a real `time.Now()` in an assertion | Inject the clock; replace sleeps with polling |
| `t.Setenv` panics | Called in a parallel test | Inject a lookup function instead |
| Flaky by 1 in 50 runs | A genuine race | `go test -count=50 -race ./...` to reproduce |
| Coverage 0% on a tested package | Its tests are behind a build tag | Add `-tags=integration` to the coverage command |
| `go vet` clean but CI lints fail | Vet does not read tagged files | `go vet -tags=integration ./test/...` |

---

## Commands reference

| Command | What it does |
|---|---|
| `make test` | Unit tier with `-race` |
| `make test-integration` | Integration tier (needs Docker) |
| `make test-e2e` | E2E tier |
| `make test-all` | All three, fastest first |
| `make cover` | Coverage summary per function |
| `make cover-html` | Line-by-line coverage in a browser |
| `make cover-all` | Merged coverage across every tier |
| `make fuzz` | Run the property tests for 30s |
| `make bench` | Benchmarks with allocation counts |
| `make golden` | Rewrite golden files (review the diff!) |
| `make lint` | `go vet` on all tag combinations, plus golangci-lint |
| `make fmt-check` | Fail if anything is unformatted |
| `make ci` | Everything the GitHub workflow runs |
| `go test -run TestX/subtest ./...` | Run one subtest |
| `go test -count=1 ./...` | Bypass the test cache |
| `go test -count=50 -race ./...` | Hunt a flaky test |

---

## See also

- **test-jest** — the same three tiers for TypeScript and Node.
- **test-playwright** — the browser and API end-to-end tier.
