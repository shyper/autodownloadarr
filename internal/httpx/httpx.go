// Package httpx holds HTTP helpers shared by the clients for external
// services (TMDB, indexers, Plex).
package httpx

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/shyper/autodownloadarr/internal/buildinfo"
)

// Errors classifying failures of external services, see docs/PLAN.md
// section 9.
var (
	// ErrUnauthorized means the credentials were rejected (configuration error).
	ErrUnauthorized = errors.New("credentials rejected")
	// ErrRateLimited means the service asked us to slow down (temporary).
	ErrRateLimited = errors.New("rate limit reached")
	// ErrNotFound means the requested item does not exist.
	ErrNotFound = errors.New("not found")
	// ErrUnavailable covers timeouts, connection errors and 5xx responses
	// (temporary).
	ErrUnavailable = errors.New("service unavailable")
)

// StatusError is a non-2xx response.
type StatusError struct {
	Code       int
	RetryAfter time.Duration
	kind       error
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("%v (HTTP %d)", e.kind, e.Code)
}

func (e *StatusError) Unwrap() error { return e.kind }

// NewClient returns an HTTP client with a sane timeout.
func NewClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// UserAgent identifies the application to external services.
func UserAgent() string { return "AutoDownloadarr/" + buildinfo.Version }

// Do sends req and returns the body of a 2xx response (at most limit bytes).
// Other responses become a *StatusError; network problems wrap
// ErrUnavailable.
func Do(client *http.Client, req *http.Request, limit int64) ([]byte, http.Header, error) {
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", UserAgent())
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctxErr := req.Context().Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return nil, nil, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, nil, fmt.Errorf("%w: read response: %w", ErrUnavailable, err)
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return body, resp.Header, nil
	}
	se := &StatusError{Code: resp.StatusCode}
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		se.kind = ErrUnauthorized
	case http.StatusNotFound:
		se.kind = ErrNotFound
	case http.StatusTooManyRequests:
		se.kind = ErrRateLimited
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			se.RetryAfter = time.Duration(s) * time.Second
		}
	default:
		se.kind = ErrUnavailable
	}
	return body, resp.Header, se
}
