// Package discogs is a small client for the Discogs API: the barcode
// search the music lookup uses and the marketplace endpoints the valuation
// prices album editions with. Every request needs a personal access token
// and counts in the source health metrics under the source "discogs".
package discogs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// Source names Discogs in the source health metrics.
const Source = "discogs"

// BaseURL is the public API.
const BaseURL = "https://api.discogs.com"

// UserAgent identifies the app, as the Discogs API terms require.
const UserAgent = "ohara/1.0 +https://github.com/Automaat/ohara"

// maxBody caps a response; a search page of releases is a few dozen KiB.
const maxBody = 4 << 20

// maxResults bounds the releases a barcode search keeps: the user picks
// among them.
const maxResults = 10

// Client calls the Discogs API with a personal access token. The API
// allows 60 authenticated requests a minute: every request, and every
// redirect it follows, waits for Limiter at Priority, then has
// RequestTimeout to finish. Every Client of the same token shares one
// Limiter, with HTTP from NewHTTPClient; a nil Limiter leaves the pacing
// to the caller.
type Client struct {
	HTTP     *http.Client
	BaseURL  string
	Token    string
	Limiter  *Limiter
	Priority Priority
}

// maxRedirects is how many redirects a request follows, as net/http does.
const maxRedirects = 10

// RequestTimeout bounds every request sent, the first and each redirect,
// from its own turn in the budget: one that has not reached Discogs by
// then is given up, so it cannot arrive late and crowd a later minute.
const RequestTimeout = 8 * time.Second

// NewHTTPClient returns the HTTP client a Client of a Limiter needs: it
// speaks HTTP/1.1 on a fresh connection for every request. net/http
// resends a GET by itself when a reused connection drops, and that resend
// would skip the budget.
func NewHTTPClient() *http.Client {
	var protocols http.Protocols
	protocols.SetHTTP1(true)
	return &http.Client{Transport: &http.Transport{
		Proxy:               http.ProxyFromEnvironment,
		DisableKeepAlives:   true,
		Protocols:           &protocols,
		TLSHandshakeTimeout: 5 * time.Second,
	}}
}

// Release is one release a barcode search found. Title is Discogs'
// "Artist - Album"; Format lists the medium first, then its descriptions
// ("CD", "Album", "Reissue").
type Release struct {
	ID         int64    `json:"id"`
	MasterID   int64    `json:"master_id,omitempty"`
	Title      string   `json:"title"`
	Year       string   `json:"year,omitempty"`
	Country    string   `json:"country,omitempty"`
	Format     []string `json:"format,omitempty"`
	Label      []string `json:"label,omitempty"`
	CatNo      string   `json:"catno,omitempty"`
	Barcode    []string `json:"barcode,omitempty"`
	CoverImage string   `json:"cover_image,omitempty"`
}

// StatusError is a response with an unexpected HTTP status.
type StatusError struct{ Code int }

func (e StatusError) Error() string { return "discogs: unexpected status " + strconv.Itoa(e.Code) }

// SearchBarcode returns the releases whose barcode Discogs matches with
// code; none is an empty slice and a nil error.
func (c *Client) SearchBarcode(ctx context.Context, code string) ([]Release, error) {
	return c.search(ctx, url.Values{"barcode": {code}})
}

// SearchText returns the releases a full-text search for query finds,
// limited to format ("CD", "Vinyl", "Cassette") unless it is empty; none
// is an empty slice and a nil error.
func (c *Client) SearchText(ctx context.Context, query, format string) ([]Release, error) {
	q := url.Values{"q": {query}}
	if format != "" {
		q.Set("format", format)
	}
	return c.search(ctx, q)
}

func (c *Client) search(ctx context.Context, q url.Values) ([]Release, error) {
	q.Set("type", "release")
	q.Set("per_page", strconv.Itoa(maxResults))
	var r struct {
		Results *[]Release `json:"results"`
	}
	if err := c.get(ctx, "/database/search?"+q.Encode(), &r, func() error {
		if r.Results == nil {
			return errors.New("no results field")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	res := *r.Results
	if len(res) > maxResults {
		res = res[:maxResults]
	}
	metrics.SetSourceResults(Source, len(res))
	return res, nil
}

// Money is an amount in a currency, as the marketplace endpoints send it.
type Money struct {
	Currency string  `json:"currency"`
	Value    float64 `json:"value"`
}

// Stats is a release's marketplace summary: the cheapest copy for sale,
// nil when none is, and how many are for sale.
type Stats struct {
	LowestPrice *Money `json:"lowest_price"`
	NumForSale  int    `json:"num_for_sale"`
}

// Stats returns the marketplace summary of release id, priced in currency
// (one of the currencies Discogs sells in, such as EUR or USD).
func (c *Client) Stats(ctx context.Context, id int64, currency string) (Stats, error) {
	q := url.Values{"curr_abbr": {currency}}
	var s Stats
	err := c.get(ctx, "/marketplace/stats/"+strconv.FormatInt(id, 10)+"?"+q.Encode(), &s, nil)
	return s, err
}

// Suggestions are the prices Discogs suggests for a release by media
// condition grade ("Mint (M)", "Very Good Plus (VG+)", ...), in the
// currency of the token owner's seller settings.
type Suggestions map[string]Money

// PriceSuggestions returns the suggested prices of release id. Discogs
// answers only once the token owner has filled in the seller settings.
func (c *Client) PriceSuggestions(ctx context.Context, id int64) (Suggestions, error) {
	s := Suggestions{}
	err := c.get(ctx, "/marketplace/price_suggestions/"+strconv.FormatInt(id, 10), &s, nil)
	return s, err
}

// httpClient is c.HTTP, or the default client, with every redirect hop
// waiting for the budget too, since net/http resends the token to the same
// host, and every request sent bounded by RequestTimeout from its turn.
func (c *Client) httpClient() *http.Client {
	client := http.DefaultClient
	if c.HTTP != nil {
		client = c.HTTP
	}
	paced := *client
	paced.Transport = hopTimeout{next: client.Transport}
	check := client.CheckRedirect
	paced.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if check != nil {
			if err := check(req, via); err != nil {
				return err
			}
		} else if len(via) >= maxRedirects {
			return errors.New("stopped after 10 redirects")
		}
		return c.Limiter.Wait(req.Context(), c.Priority)
	}
	return &paced
}

// hopTimeout gives each request it sends RequestTimeout, counted when it
// starts: right after the request's turn in the budget. The response body
// keeps the deadline until it is closed.
type hopTimeout struct{ next http.RoundTripper }

func (h hopTimeout) RoundTrip(r *http.Request) (*http.Response, error) {
	next := h.next
	if next == nil {
		next = http.DefaultTransport
	}
	ctx, cancel := context.WithTimeout(r.Context(), RequestTimeout)
	resp, err := next.RoundTrip(r.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	resp.Body = cancelOnClose{ReadCloser: resp.Body, cancel: cancel}
	return resp, nil
}

// cancelOnClose ends a request's deadline once its body is closed.
type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (b cancelOnClose) Close() error {
	defer b.cancel()
	return b.ReadCloser.Close()
}

// get waits for the token's budget, fetches path, decodes the JSON body
// into out and runs check on it. The outcome counts in the source health
// metrics: ok only once the body decodes and passes check; a request the
// budget kept back was never sent and does not count. Errors name the
// path without the query.
func (c *Client) get(ctx context.Context, path string, out any, check func() error) error {
	base := c.BaseURL
	if base == "" {
		base = BaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, http.NoBody)
	if err != nil {
		metrics.ObserveSource(Source, metrics.SourceError)
		return errors.New("discogs: build request: invalid URL")
	}
	req.Header.Set("Accept", "application/vnd.discogs.v2.discogs+json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("Authorization", "Discogs token="+c.Token)
	if err := c.Limiter.Wait(ctx, c.Priority); err != nil {
		return fmt.Errorf("discogs: GET %s: wait for rate budget: %w", req.URL.Path, err)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		metrics.ObserveSource(Source, metrics.SourceError)
		if uerr, ok := errors.AsType[*url.Error](err); ok {
			err = uerr.Err
		}
		return fmt.Errorf("discogs: GET %s: %w", req.URL.Path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		metrics.ObserveSource(Source, metrics.SourceError)
		return fmt.Errorf("discogs: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		metrics.ObserveSource(Source, "http_"+strconv.Itoa(resp.StatusCode))
		return StatusError{Code: resp.StatusCode}
	}
	if err := json.Unmarshal(body, out); err != nil {
		metrics.ObserveSource(Source, metrics.SourceParseError)
		return fmt.Errorf("discogs: parse: %w", err)
	}
	if check != nil {
		if err := check(); err != nil {
			metrics.ObserveSource(Source, metrics.SourceParseError)
			return fmt.Errorf("discogs: parse: %w", err)
		}
	}
	metrics.ObserveSource(Source, metrics.SourceOK)
	return nil
}
