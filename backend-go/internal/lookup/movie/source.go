// Package movie resolves a DVD, Blu-ray or 4K barcode to TMDB titles. TMDB
// has no barcode search, so the chain first asks product catalogs (Allegro,
// UPCitemdb) for the product name, cleans the name into a title and then
// searches TMDB for it.
package movie

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// Product is one product name a catalog returned for a barcode.
type Product struct {
	Source   string `json:"source"`
	Name     string `json:"name"`
	Category string `json:"category,omitempty"`
}

// NameSource finds product names for a normalized barcode. No match is an
// empty slice and a nil error.
type NameSource interface {
	Name() string
	Products(ctx context.Context, barcode string) ([]Product, error)
}

// maxBody caps a provider response; a search page is a few hundred KiB.
const maxBody = 4 << 20

// get sends r and returns the response, whose body is read when r accepts
// its status, counting failures under source. Any other status is a
// fetch.StatusError returned with the response headers, which survive a
// broken body. Transport errors name the URL without its query, which can
// carry an API key or a refresh token.
func get(ctx context.Context, client *http.Client, source string, r fetch.Request) (fetch.Response, error) {
	r.MaxBody = maxBody
	resp, err := fetch.Do(ctx, client, r)
	if err != nil {
		metrics.ObserveSource(source, fetch.Outcome(err))
		return resp, fmt.Errorf("%s: %w", source, err)
	}
	return resp, nil
}

// succeeded counts a usable answer, a valid "no match" included.
func succeeded(source string) { metrics.ObserveSource(source, metrics.SourceOK) }

// found counts a usable answer carrying n results.
func found(source string, n int) {
	metrics.SetSourceResults(source, n)
	succeeded(source)
}

// parseFailed counts a response that could not be read.
func parseFailed(source string, err error) error {
	metrics.ObserveSource(source, metrics.SourceParseError)
	return fmt.Errorf("%s: parse: %w", source, err)
}

// RateLimitError is a source's refusal for its request limit. Reset is when
// the source takes requests again, zero when the answer does not say.
type RateLimitError struct{ Reset time.Time }

func (e *RateLimitError) Error() string {
	if e.Reset.IsZero() {
		return ErrQuotaExceeded.Error()
	}
	return ErrQuotaExceeded.Error() + ", resets at " + e.Reset.UTC().Format(time.RFC3339)
}

func (e *RateLimitError) Unwrap() error { return ErrQuotaExceeded }

// limitReset reads the reset time, Unix seconds in X-RateLimit-Reset,
// from a response's headers; zero when the header is missing or
// unreadable.
func limitReset(header http.Header) time.Time {
	sec, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64)
	if err != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// isStatus reports whether err carries HTTP status code.
func isStatus(err error, code int) bool {
	e, ok := errors.AsType[fetch.StatusError](err)
	return ok && e.Code == code
}
