package weather_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/nehsa-net/test-go/internal/weather"
)

// fakeProvider is a hand-written fake. Go rarely needs a mocking framework:
// an interface with one method is three lines to fake, and the fake records
// exactly what this test cares about.
type fakeProvider struct {
	conditions weather.Conditions
	err        error

	calls      int
	lastCity   string
	lastCtxErr error
}

func (f *fakeProvider) Fetch(ctx context.Context, city string) (weather.Conditions, error) {
	f.calls++
	f.lastCity = city
	f.lastCtxErr = ctx.Err()
	return f.conditions, f.err
}

// failingRecorder proves that a broken audit sink does not fail the request.
type failingRecorder struct{ calls int }

func (r *failingRecorder) Record(context.Context, weather.Conditions) error {
	r.calls++
	return errors.New("database is on fire")
}

type spyRecorder struct {
	got []weather.Conditions
}

func (r *spyRecorder) Record(_ context.Context, c weather.Conditions) error {
	r.got = append(r.got, c)
	return nil
}

var sampleConditions = weather.Conditions{
	City:        "Cape Canaveral",
	Description: "Clouds",
	TempC:       27.5,
	Humidity:    74,
	ObservedAt:  time.Date(2026, 8, 11, 15, 4, 5, 0, time.UTC),
}

func TestServiceDescribe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		units weather.Units
		want  weather.Report
	}{
		{
			name:  "imperial converts the temperature",
			units: weather.Fahrenheit,
			want: weather.Report{
				City:        "Cape Canaveral",
				Description: "Clouds",
				Temp:        81.5,
				Units:       weather.Fahrenheit,
				Humidity:    74,
				ObservedAt:  "2026-08-11T15:04:05Z",
			},
		},
		{
			name:  "metric passes the temperature through",
			units: weather.Celsius,
			want: weather.Report{
				City:        "Cape Canaveral",
				Description: "Clouds",
				Temp:        27.5,
				Units:       weather.Celsius,
				Humidity:    74,
				ObservedAt:  "2026-08-11T15:04:05Z",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			provider := &fakeProvider{conditions: sampleConditions}
			svc := weather.NewService(provider)

			got, err := svc.Describe(t.Context(), "Cape Canaveral", tc.units)
			if err != nil {
				t.Fatalf("Describe() unexpected error: %v", err)
			}

			// cmp.Diff on the whole struct beats field-by-field assertions: it
			// catches a field you forgot to check, and prints a readable diff.
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("Describe() mismatch (-want +got):\n%s", diff)
			}
			if provider.calls != 1 {
				t.Errorf("provider called %d times, want exactly 1", provider.calls)
			}
		})
	}
}

func TestServiceDescribePropagatesProviderError(t *testing.T) {
	t.Parallel()

	// Table over the sentinel errors, because the HTTP layer maps each one to a
	// different status code and must keep being able to tell them apart.
	for _, wantErr := range []error{
		weather.ErrCityNotFound,
		weather.ErrUpstream,
		weather.ErrInvalidCity,
	} {
		t.Run(wantErr.Error(), func(t *testing.T) {
			t.Parallel()

			svc := weather.NewService(&fakeProvider{err: wantErr})

			_, err := svc.Describe(t.Context(), "Orlando", weather.Celsius)

			if !errors.Is(err, wantErr) {
				t.Fatalf("Describe() error = %v, want %v", err, wantErr)
			}
		})
	}
}

func TestServiceDescribeRecordsObservation(t *testing.T) {
	t.Parallel()

	recorder := &spyRecorder{}
	svc := weather.NewService(&fakeProvider{conditions: sampleConditions}, weather.WithRecorder(recorder))

	if _, err := svc.Describe(t.Context(), "Cape Canaveral", weather.Celsius); err != nil {
		t.Fatalf("Describe() unexpected error: %v", err)
	}

	if len(recorder.got) != 1 {
		t.Fatalf("recorded %d observations, want 1", len(recorder.got))
	}
	// The recorder must see the raw Celsius conditions, not the converted
	// report — otherwise the stored history changes meaning with the caller's
	// query string.
	if diff := cmp.Diff(sampleConditions, recorder.got[0]); diff != "" {
		t.Errorf("recorded observation mismatch (-want +got):\n%s", diff)
	}
}

// This is the test that pins a deliberate product decision in place. Without it,
// somebody "fixes" the ignored error in service.go and the API starts 502-ing
// whenever the audit database hiccups.
func TestServiceDescribeSurvivesRecorderFailure(t *testing.T) {
	t.Parallel()

	recorder := &failingRecorder{}
	svc := weather.NewService(&fakeProvider{conditions: sampleConditions}, weather.WithRecorder(recorder))

	got, err := svc.Describe(t.Context(), "Cape Canaveral", weather.Celsius)

	if err != nil {
		t.Fatalf("Describe() failed because the recorder failed: %v", err)
	}
	if got.City != "Cape Canaveral" {
		t.Errorf("City = %q, want the report to be served anyway", got.City)
	}
	if recorder.calls != 1 {
		t.Errorf("recorder called %d times, want 1", recorder.calls)
	}
}

func TestServiceAgeUsesInjectedClock(t *testing.T) {
	t.Parallel()

	// Freeze time. Calling time.Now() in the assertion instead would make this
	// test a race against the clock — usually passing, occasionally not.
	frozen := time.Date(2026, 8, 11, 16, 4, 5, 0, time.UTC)
	svc := weather.NewService(&fakeProvider{}, weather.WithClock(func() time.Time { return frozen }))

	got := svc.Age(sampleConditions)

	if want := time.Hour; got != want {
		t.Errorf("Age() = %v, want %v", got, want)
	}
}

func TestServiceDescribePassesContextThrough(t *testing.T) {
	t.Parallel()

	provider := &fakeProvider{conditions: sampleConditions}
	svc := weather.NewService(provider)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // already cancelled before the call

	_, _ = svc.Describe(ctx, "Orlando", weather.Celsius)

	// The provider must receive the caller's context, not context.Background().
	// A service that swallows cancellation keeps working after the client hung up.
	if !errors.Is(provider.lastCtxErr, context.Canceled) {
		t.Errorf("provider saw ctx.Err() = %v, want context.Canceled", provider.lastCtxErr)
	}
}
