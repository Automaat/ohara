// Package deals hunts for listings of wishlist copies on OLX, BlueDVD,
// Świat Książki, Pepper.pl and Tezeusz. A run asks every source, keeps the listings that
// match a wishlist copy, stores each once as a price_observation and sends
// one Telegram message per new listing at or below the copy's maximum price.
package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// Listing is one offer a source found. AcquiredAs is "new", "used" or nil
// when the source does not say. Raw is the source's own record of the
// listing, kept for later valuation work. Query is the search a Pager
// found it with, empty for other sources; an Expiring source's listings
// carry its Scope. Kinds are the title kinds the listing can be when its
// source's catalog says ("book", or "movie" and "tv" for a film disc), nil
// when it does not; a listing with Kinds belongs to no want of another
// kind. Medium is the album medium a source names apart from the title
// ("vinyl", "cd", "cassette" or "other"), which makes the listing an
// album's; empty leaves the medium to the title. DiscogsReleaseID is
// the inventory release ID when Discogs supplies it.
type Listing struct {
	Source           string
	ExternalID       string
	Title            string
	URL              string
	PhotoURL         string
	PriceCents       int64
	AcquiredAs       *string
	Raw              json.RawMessage
	Query            string
	Medium           string
	Kinds            []string
	DiscogsReleaseID int64
}

func imageURL(value string) string {
	if strings.HasPrefix(value, "//") {
		value = "https:" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return value
}

// Source finds listings. A query source is asked once per search query; a
// feed source (PerQuery false) once per run with an empty query.
type Source interface {
	Name() string
	PerQuery() bool
	Search(ctx context.Context, query string) ([]Listing, error)
}

// Expiring is a feed source whose answer is all of its Scope that may be
// shown: a stored listing of the scope that a successful answer no longer
// holds is gone, and gone or stale listings leave the deals view and send
// no alert (freshListing).
type Expiring interface {
	Source
	Scope() string
}

// KindSource is a source that offers titles of some kinds only, such as a
// bookshop: a run asks it only the queries of wishes of those kinds and
// matches its listings only to those wishes, so a book named like a wished
// album never alerts as that album.
type KindSource interface {
	Source
	Kinds() []string
}

// Canaried is a query source whose catalog the configured canary query
// does not fit; it names the query that always finds listings there.
type Canaried interface {
	Source
	CanaryQuery() string
}

// canaryQuery is the canary src is asked: its own when it is Canaried,
// else configured. An empty configured query turns every canary off.
func canaryQuery(src Source, configured string) string {
	if c, ok := src.(Canaried); ok && configured != "" {
		return c.CanaryQuery()
	}
	return configured
}

// Page is a Pager's answer to one query. Seen holds the external ID of
// every offer in the answer, also those Listings leave out (no price).
// Complete reports that the answer holds every offer the site has for the
// query, not just its first page, so a stored listing the query found
// before and Seen lacks is gone.
type Page struct {
	Listings []Listing
	Seen     []string
	Complete bool
}

// Pager is a query source that tells a complete answer from a first page.
// Its listings carry the query that found them.
type Pager interface {
	Source
	SearchPage(ctx context.Context, query string) (Page, error)
}

// QueryKey is the form a listing's query is stored and compared in:
// lower case, single spaces.
func QueryKey(query string) string {
	return strings.Join(strings.Fields(strings.ToLower(query)), " ")
}

// maxPriceCents mirrors the price_observation cap (1 000 000 zł).
const maxPriceCents = 100_000_000

// maxBody caps a source response; an HTML results page is a few hundred KB.
const maxBody = 16 << 20

// NewHTTPClient is the client the sources share.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// statusCoder is an error that carries the HTTP status a site answered,
// such as fetch.StatusError.
type statusCoder interface {
	error
	StatusCode() int
}

// Refused tracks one site within a run. A 403 or 429 answer means the site
// is blocking us, so the run sends it nothing more until the next run and
// reports the refusal once, with the requests it skipped. Other failures,
// such as timeouts, 5xx answers and parse errors, are not refusals.
type Refused struct {
	code    int
	skipped int
}

// Note records err as the site's refusal and reports true, or reports
// false for any other failure.
func (f *Refused) Note(err error) bool {
	se, ok := errors.AsType[statusCoder](err)
	if !ok {
		return false
	}
	code := se.StatusCode()
	if code != http.StatusForbidden && code != http.StatusTooManyRequests {
		return false
	}
	f.code = code
	return true
}

// Skip reports whether the site refused already, counting the request the
// run then skips.
func (f *Refused) Skip() bool {
	if f.code == 0 {
		return false
	}
	f.skipped++
	return true
}

// Message is the run error naming the refusal and the skipped requests,
// counted in unit ("queries", "quotes"), or "" when the site did not
// refuse.
func (f *Refused) Message(site, unit string) string {
	if f.code == 0 {
		return ""
	}
	return fmt.Sprintf("%s: refused with status %d, skipped %d %s", site, f.code, f.skipped, unit)
}

// parseError is a response a source could not read.
type parseError struct{ err error }

func (e parseError) Error() string { return e.err.Error() }

func (e parseError) Unwrap() error { return e.err }

// sourceStatus is the metrics outcome of a source request that returned err.
func sourceStatus(err error) string {
	if err == nil {
		return metrics.SourceOK
	}
	if se, ok := errors.AsType[statusCoder](err); ok {
		return "http_" + strconv.Itoa(se.StatusCode())
	}
	if _, ok := errors.AsType[parseError](err); ok {
		return metrics.SourceParseError
	}
	return fetch.Outcome(err)
}

// ObserveSearch counts the outcome of one Search on src in the source
// health metrics. Callers skip it when their own context was canceled.
func ObserveSearch(src Source, err error) {
	metrics.ObserveSource(src.Name(), sourceStatus(err))
}

// get fetches the endpoint for source and returns the body of a 200
// response.
func get(ctx context.Context, client *http.Client, source, endpoint, accept string) ([]byte, error) {
	resp, err := fetch.Do(ctx, client, fetch.Request{
		Source: source,
		URL:    endpoint,
		Header: http.Header{
			"User-Agent":      {fetch.BrowserUserAgent},
			"Accept":          {accept},
			"Accept-Language": {"pl-PL,pl;q=0.9,en;q=0.8"},
		},
		MaxBody: maxBody,
	})
	if err != nil {
		return nil, err
	}
	return resp.Body, nil
}

var priceChars = regexp.MustCompile(`[^\d,.]`)

// parsePrice reads a Polish price such as "3 000,21zł", "45 zł" or "33.7"
// into grosze. A comma marks decimals, so dots before it group thousands.
func parsePrice(s string) (int64, bool) {
	text := priceChars.ReplaceAllString(s, "")
	if strings.Contains(text, ",") {
		text = strings.ReplaceAll(text, ".", "")
		text = strings.Replace(text, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return 0, false
	}
	return toCents(v)
}

// toCents converts złoty to grosze within the price_observation cap.
func toCents(v float64) (int64, bool) {
	if math.IsNaN(v) || v < 0 {
		return 0, false
	}
	cents := math.Round(v * 100)
	if cents > maxPriceCents {
		return 0, false
	}
	return int64(cents), true
}
