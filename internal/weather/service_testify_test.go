package weather_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nehsa-net/test-go/internal/weather"
)

// This file exists purely as a side-by-side reference. It re-tests things
// already covered in service_test.go using testify instead of the standard
// library, so you can see both styles against identical subject code and pick
// one deliberately.
//
// Pick ONE style per repo and stay with it. A codebase where half the tests use
// require and half use t.Fatalf is harder to read than either style alone.
//
//	stdlib  — no dependency, explicit, verbose. What the Go team writes.
//	testify — terse, familiar to anyone from JUnit or Jest, huge dependency tree.
//
// The one rule that matters whichever you choose:
//
//	require.X  stops the test on failure  (like t.Fatalf)
//	assert.X   records it and continues   (like t.Errorf)
//
// Using assert where require belongs is the classic testify bug: the assertion
// fails, execution continues, and the next line panics on a nil pointer — so the
// report shows a panic instead of the assertion that actually explains it.

func TestServiceDescribeTestifyStyle(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{conditions: sampleConditions}
	svc := weather.NewService(provider)

	got, err := svc.Describe(t.Context(), "Cape Canaveral", weather.Fahrenheit)

	// require: if this fails there is no point evaluating anything below.
	require.NoError(t, err)

	// assert: independent checks, so report all the failures in one run.
	assert.Equal(t, "Cape Canaveral", got.City)
	assert.Equal(t, "Clouds", got.Description)
	assert.InDelta(t, 81.5, got.Temp, 1e-9) // InDelta, never Equal, for floats
	assert.Equal(t, weather.Fahrenheit, got.Units)
	assert.Equal(t, 74, got.Humidity)
	assert.Equal(t, 1, provider.calls)
}

func TestServiceDescribeErrorTestifyStyle(t *testing.T) {
	t.Parallel()

	svc := weather.NewService(&fakeProvider{err: weather.ErrCityNotFound})

	_, err := svc.Describe(t.Context(), "Atlantis", weather.Celsius)

	// ErrorIs, not EqualError. EqualError compares the message string and breaks
	// the moment somebody wraps the error with extra context.
	require.ErrorIs(t, err, weather.ErrCityNotFound)
}

func TestServiceAgeTestifyStyle(t *testing.T) {
	t.Parallel()

	frozen := time.Date(2026, 8, 11, 16, 4, 5, 0, time.UTC)
	svc := weather.NewService(&fakeProvider{}, weather.WithClock(func() time.Time { return frozen }))

	assert.Equal(t, time.Hour, svc.Age(sampleConditions))
}

// Table-driven tests work identically in both styles; only the assertion lines
// change. The structure is the part worth copying.
func TestConvertTempTestifyStyle(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		tempC float64
		units weather.Units
		want  float64
	}{
		"freezing to fahrenheit": {tempC: 0, units: weather.Fahrenheit, want: 32},
		"boiling to fahrenheit":  {tempC: 100, units: weather.Fahrenheit, want: 212},
		"metric passes through":  {tempC: 21.5, units: weather.Celsius, want: 21.5},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			assert.InDelta(t, tc.want, weather.ConvertTemp(tc.tempC, tc.units), 1e-9)
		})
	}
}
