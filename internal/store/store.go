// Package store persists observations in Postgres.
//
// It exists in this reference so the integration tier has a real dependency to
// run against. A store tested only against an in-memory fake proves the fake
// works; it says nothing about whether the SQL is valid, the column types line
// up, or the unique constraint fires.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nehsa-net/test-go/internal/weather"
)

// Schema is applied by the integration tier before the tests run. Keeping it in
// the package (rather than a migration tool) keeps this reference self-contained;
// a real service would point the same test at its real migrations.
const Schema = `
CREATE TABLE IF NOT EXISTS observations (
	id           BIGSERIAL PRIMARY KEY,
	city         TEXT        NOT NULL,
	description  TEXT        NOT NULL,
	temp_c       NUMERIC(5,2) NOT NULL,
	humidity     INT         NOT NULL CHECK (humidity BETWEEN 0 AND 100),
	observed_at  TIMESTAMPTZ NOT NULL,
	UNIQUE (city, observed_at)
);`

// ErrDuplicate is returned when the same city/timestamp is recorded twice.
var ErrDuplicate = errors.New("store: observation already recorded")

// Store is a thin data-access layer over *sql.DB.
type Store struct {
	db *sql.DB
}

// New wraps an open database handle.
func New(db *sql.DB) *Store { return &Store{db: db} }

// Migrate applies the schema. Safe to call repeatedly.
func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, Schema); err != nil {
		return fmt.Errorf("store: applying schema: %w", err)
	}
	return nil
}

// Record inserts one observation, satisfying weather.Recorder.
func (s *Store) Record(ctx context.Context, c weather.Conditions) error {
	const q = `
		INSERT INTO observations (city, description, temp_c, humidity, observed_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (city, observed_at) DO NOTHING`

	res, err := s.db.ExecContext(ctx, q, c.City, c.Description, c.TempC, c.Humidity, c.ObservedAt)
	if err != nil {
		return fmt.Errorf("store: recording observation: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: reading result: %w", err)
	}
	if affected == 0 {
		return ErrDuplicate
	}
	return nil
}

// Latest returns the most recent observation for a city.
func (s *Store) Latest(ctx context.Context, city string) (weather.Conditions, error) {
	const q = `
		SELECT city, description, temp_c, humidity, observed_at
		FROM observations
		WHERE city = $1
		ORDER BY observed_at DESC
		LIMIT 1`

	var (
		c          weather.Conditions
		observedAt time.Time
	)
	err := s.db.QueryRowContext(ctx, q, city).
		Scan(&c.City, &c.Description, &c.TempC, &c.Humidity, &observedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return weather.Conditions{}, weather.ErrCityNotFound
	}
	if err != nil {
		return weather.Conditions{}, fmt.Errorf("store: reading latest: %w", err)
	}
	c.ObservedAt = observedAt.UTC()
	return c, nil
}

// CountForCity is a convenience used by assertions in the integration tier.
func (s *Store) CountForCity(ctx context.Context, city string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM observations WHERE city = $1`, city).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("store: counting: %w", err)
	}
	return n, nil
}
