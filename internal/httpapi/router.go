// Package httpapi wires the HTTP surface. It knows about status codes and
// query strings; it knows nothing about how weather is fetched.
package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/nehsa-net/test-go/internal/weather"
)

// Describer is the narrow interface the handlers need. Because it is an
// interface, a router test can drive every branch — including the 502 path —
// without a network, a real client, or a fake HTTP server.
type Describer interface {
	Describe(ctx context.Context, city string, units weather.Units) (weather.Report, error)
}

const defaultCity = "Cape Canaveral, FL"

// New builds the router. Returning *gin.Engine rather than starting a server is
// what lets tests call router.ServeHTTP directly against an httptest recorder —
// no port, no goroutine, no cleanup.
func New(svc Describer) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(gin.Recovery())

	router.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	router.GET("/weather", func(c *gin.Context) {
		city := c.Query("city")
		if city == "" {
			city = defaultCity
		}

		units, err := weather.ParseUnits(c.Query("units"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "units must be metric or imperial"})
			return
		}

		report, err := svc.Describe(c.Request.Context(), city, units)
		if err != nil {
			writeError(c, err)
			return
		}
		c.JSON(http.StatusOK, report)
	})

	return router
}

// writeError maps domain errors onto status codes in exactly one place.
//
// Note what the client never sees: the wrapped upstream detail. Internal cause
// goes to the log; the caller gets a status code and a flat sentence. Leaking
// "dial tcp 10.0.0.4:443: connect: refused" to a stranger is free reconnaissance.
func writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, weather.ErrInvalidCity):
		c.JSON(http.StatusBadRequest, gin.H{"error": "city is required"})
	case errors.Is(err, weather.ErrCityNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "no weather for that city"})
	default:
		_ = c.Error(err) // recorded for the log, not rendered to the caller
		c.JSON(http.StatusBadGateway, gin.H{"error": "weather is unavailable right now"})
	}
}
