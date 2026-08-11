package weather

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Doer is the seam. Depending on *http.Client directly would make this type
// untestable without real network access; depending on the one method actually
// used lets a test pass a stub, and lets production pass *http.Client unchanged.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Client talks to the upstream weather API.
//
// BaseURL is a field rather than a constant for exactly one reason: a test can
// point it at an httptest.Server. That single change is what makes the whole
// integration tier possible.
type Client struct {
	BaseURL string
	APIKey  string
	HTTP    Doer
}

// NewClient builds a Client with a sane default timeout. Passing a nil Doer is
// allowed and yields a real *http.Client, so production callers stay terse.
func NewClient(baseURL, apiKey string, doer Doer) *Client {
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  apiKey,
		HTTP:    doer,
	}
}

// upstreamPayload is the wire format. It is unexported on purpose: the shape of
// somebody else's JSON is not part of this package's contract.
type upstreamPayload struct {
	Name    string `json:"name"`
	Weather []struct {
		Main        string `json:"main"`
		Description string `json:"description"`
	} `json:"weather"`
	Main struct {
		Temp     float64 `json:"temp"`
		Humidity int     `json:"humidity"`
	} `json:"main"`
	Dt  int64 `json:"dt"`
	Cod int   `json:"cod"`
}

// Fetch returns current conditions for city, always in Celsius. Converting to
// the caller's preferred units is the service's job, not the client's — one
// responsibility per layer keeps the unit tests small.
func (c *Client) Fetch(ctx context.Context, city string) (Conditions, error) {
	city, err := NormaliseCity(city)
	if err != nil {
		return Conditions{}, err
	}

	endpoint, err := url.Parse(c.BaseURL + "/data/2.5/weather")
	if err != nil {
		return Conditions{}, fmt.Errorf("%w: bad base url: %w", ErrUpstream, err)
	}
	q := endpoint.Query()
	q.Set("q", city)
	q.Set("appid", c.APIKey)
	q.Set("units", "metric")
	endpoint.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return Conditions{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return Conditions{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	// The error from closing a response body is not actionable, but it must be
	// read: an unclosed body leaks a connection per request.
	defer func() { _ = resp.Body.Close() }()

	// Cap the read. An upstream that streams forever should fail the test run,
	// not exhaust the machine.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return Conditions{}, fmt.Errorf("%w: reading body: %w", ErrUpstream, err)
	}

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return Conditions{}, fmt.Errorf("%w: %q", ErrCityNotFound, city)
	case resp.StatusCode != http.StatusOK:
		return Conditions{}, fmt.Errorf("%w: status %d: %s", ErrUpstream, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload upstreamPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return Conditions{}, fmt.Errorf("%w: decoding body: %w", ErrUpstream, err)
	}
	if len(payload.Weather) == 0 {
		return Conditions{}, fmt.Errorf("%w: payload carried no weather entries", ErrUpstream)
	}

	return Conditions{
		City:        payload.Name,
		Description: payload.Weather[0].Main,
		TempC:       payload.Main.Temp,
		Humidity:    payload.Main.Humidity,
		ObservedAt:  time.Unix(payload.Dt, 0).UTC(),
	}, nil
}
