package weather

import (
	"context"
	"time"
)

// Provider is what Service needs from the outside world. Client satisfies it,
// and so does a three-line fake in a test file. Declaring the interface here —
// next to the consumer, not next to Client — is the Go convention, and it keeps
// the interface as narrow as the consumer actually requires.
type Provider interface {
	Fetch(ctx context.Context, city string) (Conditions, error)
}

// Recorder is an optional sink for observations. A nil Recorder is valid and
// disables recording, which keeps the unit tests free of database concerns.
type Recorder interface {
	Record(ctx context.Context, c Conditions) error
}

// Service turns raw conditions into the report the API serves.
type Service struct {
	provider Provider
	recorder Recorder
	now      func() time.Time // injected so age calculations are deterministic
}

// Option configures a Service. The functional-options pattern keeps the common
// construction short while leaving every dependency swappable from a test.
type Option func(*Service)

// WithRecorder attaches a store to persist each successful observation.
func WithRecorder(r Recorder) Option { return func(s *Service) { s.recorder = r } }

// WithClock replaces time.Now. Any test that asserts on elapsed time needs this;
// without it the assertion is a race against the wall clock.
func WithClock(now func() time.Time) Option { return func(s *Service) { s.now = now } }

// NewService wires a Service over a Provider.
func NewService(p Provider, opts ...Option) *Service {
	s := &Service{provider: p, now: time.Now}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Describe fetches current conditions and renders them in the requested units.
//
// Recording failures are deliberately swallowed: the caller asked for weather,
// and a broken audit sink is not a reason to fail their request. That is a real
// product decision, so it gets an explicit test rather than a comment alone.
func (s *Service) Describe(ctx context.Context, city string, units Units) (Report, error) {
	conditions, err := s.provider.Fetch(ctx, city)
	if err != nil {
		return Report{}, err
	}

	if s.recorder != nil {
		_ = s.recorder.Record(ctx, conditions)
	}

	return Report{
		City:        conditions.City,
		Description: conditions.Description,
		Temp:        ConvertTemp(conditions.TempC, units),
		Units:       units,
		Humidity:    conditions.Humidity,
		ObservedAt:  conditions.ObservedAt.Format(time.RFC3339),
	}, nil
}

// Age reports how stale an observation is, using the injected clock.
func (s *Service) Age(c Conditions) time.Duration {
	return s.now().Sub(c.ObservedAt)
}
