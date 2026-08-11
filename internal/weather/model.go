// Package weather holds the domain model, the upstream client, and the service
// logic for the reference service. It is deliberately split into three files so
// each test tier has an obvious target:
//
//	model.go   -> pure values and parsing        (unit)
//	client.go  -> the one place that does I/O    (unit via httptest, integration via a real listener)
//	service.go -> orchestration over an interface (unit via a fake)
package weather

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// Sentinel errors. Callers compare with errors.Is, never by string match, so
// wrapping with %w further up the stack stays safe.
var (
	ErrInvalidCity  = errors.New("weather: city must not be empty")
	ErrCityNotFound = errors.New("weather: city not found")
	ErrUpstream     = errors.New("weather: upstream request failed")
	ErrInvalidUnits = errors.New("weather: unknown units")
)

// Units is the temperature scale a caller asked for.
type Units string

const (
	Celsius    Units = "metric"
	Fahrenheit Units = "imperial"
)

// ParseUnits maps a query-string value onto a Units. An empty string is not an
// error: it means "the caller did not care", and the default applies.
func ParseUnits(s string) (Units, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "imperial", "f", "fahrenheit":
		return Fahrenheit, nil
	case "metric", "c", "celsius":
		return Celsius, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidUnits, s)
	}
}

// Conditions is the normalised observation the rest of the app works with. The
// upstream payload shape stops at the edge of client.go; nothing downstream
// knows what OpenWeatherMap's JSON looks like.
type Conditions struct {
	City        string
	Description string
	TempC       float64
	Humidity    int
	ObservedAt  time.Time
}

// Report is what the HTTP layer serialises.
type Report struct {
	City        string  `json:"city"`
	Description string  `json:"description"`
	Temp        float64 `json:"temp"`
	Units       Units   `json:"units"`
	Humidity    int     `json:"humidity"`
	ObservedAt  string  `json:"observed_at"`
}

// CelsiusToFahrenheit is the kind of tiny pure function that costs nothing to
// test and is wrong surprisingly often.
func CelsiusToFahrenheit(c float64) float64 { return c*9/5 + 32 }

// ConvertTemp returns tempC expressed in the requested units, rounded to one
// decimal place so the output is stable enough to assert on.
func ConvertTemp(tempC float64, u Units) float64 {
	v := tempC
	if u == Fahrenheit {
		v = CelsiusToFahrenheit(tempC)
	}
	return roundTo(v, 1)
}

func roundTo(v float64, places int) float64 {
	pow := 1.0
	for range places {
		pow *= 10
	}
	// +0.5 with truncation, sign-aware, so -0.05 does not round to 0.0.
	if v < 0 {
		return float64(int64(v*pow-0.5)) / pow
	}
	return float64(int64(v*pow+0.5)) / pow
}

// NormaliseCity trims and collapses whitespace so " cape  canaveral " and
// "cape canaveral" cache to the same key.
func NormaliseCity(city string) (string, error) {
	fields := strings.Fields(city)
	if len(fields) == 0 {
		return "", ErrInvalidCity
	}
	return strings.Join(fields, " "), nil
}
