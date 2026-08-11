package weather_test

import (
	"errors"
	"math"
	"testing"

	"github.com/nehsa-net/test-go/internal/weather"
)

// Note the package name: weather_test, not weather. An external test package can
// only reach exported identifiers, so the tests are forced to exercise the public
// contract rather than the implementation. Use the internal package (`package
// weather`) only when you genuinely must reach an unexported detail.

func TestParseUnits(t *testing.T) {
	t.Parallel()

	// The table-driven test is the Go idiom. One row per case, named, so a
	// failure reports which case broke without any extra logging.
	tests := []struct {
		name    string
		input   string
		want    weather.Units
		wantErr error
	}{
		{name: "empty defaults to imperial", input: "", want: weather.Fahrenheit},
		{name: "metric", input: "metric", want: weather.Celsius},
		{name: "celsius alias", input: "celsius", want: weather.Celsius},
		{name: "single letter c", input: "c", want: weather.Celsius},
		{name: "imperial", input: "imperial", want: weather.Fahrenheit},
		{name: "mixed case is accepted", input: "MeTrIc", want: weather.Celsius},
		{name: "surrounding space is trimmed", input: "  metric  ", want: weather.Celsius},
		{name: "kelvin is rejected", input: "kelvin", wantErr: weather.ErrInvalidUnits},
		{name: "garbage is rejected", input: "!!", wantErr: weather.ErrInvalidUnits},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := weather.ParseUnits(tc.input)

			// Compare errors with errors.Is, never err.Error() == "...".
			// String matching breaks the moment somebody adds context.
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ParseUnits(%q) error = %v, want %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("ParseUnits(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestConvertTemp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		tempC float64
		units weather.Units
		want  float64
	}{
		{name: "freezing to fahrenheit", tempC: 0, units: weather.Fahrenheit, want: 32},
		{name: "boiling to fahrenheit", tempC: 100, units: weather.Fahrenheit, want: 212},
		{name: "metric passes through", tempC: 21.5, units: weather.Celsius, want: 21.5},
		{name: "rounds to one decimal", tempC: 21.44, units: weather.Celsius, want: 21.4},
		{name: "rounds half up", tempC: 21.45, units: weather.Celsius, want: 21.5},
		{name: "negative rounds away from zero", tempC: -0.05, units: weather.Celsius, want: -0.1},
		{name: "the crossover point", tempC: -40, units: weather.Fahrenheit, want: -40},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := weather.ConvertTemp(tc.tempC, tc.units)

			// Never compare floats with ==. An epsilon comparison is the only
			// safe form, and 1e-9 is far tighter than the 0.1 we round to.
			if math.Abs(got-tc.want) > 1e-9 {
				t.Errorf("ConvertTemp(%v, %q) = %v, want %v", tc.tempC, tc.units, got, tc.want)
			}
		})
	}
}

func TestNormaliseCity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		input   string
		want    string
		wantErr error
	}{
		{name: "already clean", input: "Orlando", want: "Orlando"},
		{name: "trims", input: "  Orlando  ", want: "Orlando"},
		{name: "collapses inner whitespace", input: "Cape   Canaveral", want: "Cape Canaveral"},
		{name: "collapses tabs and newlines", input: "Cape\t\nCanaveral", want: "Cape Canaveral"},
		{name: "empty is an error", input: "", wantErr: weather.ErrInvalidCity},
		{name: "whitespace only is an error", input: "   \t ", wantErr: weather.ErrInvalidCity},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := weather.NormaliseCity(tc.input)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NormaliseCity(%q) error = %v, want %v", tc.input, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("NormaliseCity(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// FuzzNormaliseCity is a property test: whatever the input, the function must
// never panic, and a successful result must never have leading, trailing, or
// doubled spaces. Fuzzing finds the inputs a hand-written table forgets —
// multi-byte runes, unusual Unicode spaces, enormous strings.
//
//	go test -run=Fuzz -fuzz=FuzzNormaliseCity -fuzztime=30s ./internal/weather
func FuzzNormaliseCity(f *testing.F) {
	for _, seed := range []string{"", " ", "Orlando", "Cape  Canaveral", "  x", "日本 東京"} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		got, err := weather.NormaliseCity(input)
		if err != nil {
			if got != "" {
				t.Errorf("error result should be empty, got %q", got)
			}
			return
		}
		if got == "" {
			t.Error("success result must not be empty")
		}
		if got != trimAll(got) {
			t.Errorf("result %q still has untrimmed or doubled whitespace", got)
		}
	})
}

func trimAll(s string) string {
	out, _ := weather.NormaliseCity(s)
	return out
}

// BenchmarkConvertTemp shows the benchmark form. Run with:
//
//	go test -bench=. -benchmem ./internal/weather
func BenchmarkConvertTemp(b *testing.B) {
	for b.Loop() {
		weather.ConvertTemp(21.456, weather.Fahrenheit)
	}
}
