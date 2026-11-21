// Package movie resolves a DVD, Blu-ray or 4K barcode to TMDB titles. TMDB
// has no barcode search, so the chain first asks product catalogs (Allegro,
// UPCitemdb) for the product name, cleans the name into a title and then
// searches TMDB for it.
package movie

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

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

// statusError reports an unexpected HTTP status and keeps the response
// headers, which can say when to retry.
type statusError struct {
	code   int
	header http.Header
}

func (e statusError) Error() string { return "unexpected status " + strconv.Itoa(e.code) }

// get runs req and returns the body of a response whose status is in ok,
// counting failures under source. Transport errors name the URL without its
// query, which can carry an API key or a refresh token.
func get(client *http.Client, source string, req *http.Request, ok ...int) (int, []byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		metrics.ObserveSource(source, metrics.SourceError)
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err
		}
		return 0, nil, fmt.Errorf("%s: %s %s://%s%s: %w", source, req.Method, req.URL.Scheme, req.URL.Host, req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		metrics.ObserveSource(source, metrics.SourceError)
		return 0, nil, fmt.Errorf("%s: read body: %w", source, err)
	}
	if slices.Contains(ok, resp.StatusCode) {
		return resp.StatusCode, body, nil
	}
	metrics.ObserveSource(source, "http_"+strconv.Itoa(resp.StatusCode))
	return resp.StatusCode, nil, fmt.Errorf("%s: %w", source, statusError{code: resp.StatusCode, header: resp.Header})
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

// limitReset reads the reset time, Unix seconds in X-RateLimit-Reset, from
// the response err carries; zero when the header is missing or unreadable.
func limitReset(err error) time.Time {
	e, ok := errors.AsType[statusError](err)
	if !ok {
		return time.Time{}
	}
	sec, perr := strconv.ParseInt(strings.TrimSpace(e.header.Get("X-RateLimit-Reset")), 10, 64)
	if perr != nil || sec <= 0 {
		return time.Time{}
	}
	return time.Unix(sec, 0)
}

// isStatus reports whether err carries HTTP status code.
func isStatus(err error, code int) bool {
	var e statusError
	return errors.As(err, &e) && e.code == code
}
