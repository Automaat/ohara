// Package deals hunts for listings of wishlist copies on OLX, Pepper.pl and
// Ceneo. A run asks every source, keeps the listings that match a wishlist
// copy, stores each once as a price_observation and sends one Telegram
// message per new listing at or below the copy's maximum price.
package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Listing is one offer a source found. AcquiredAs is "new", "used" or nil
// when the source does not say. Raw is the source's own record of the
// listing, kept for later valuation work.
type Listing struct {
	Source     string
	ExternalID string
	Title      string
	URL        string
	PriceCents int64
	AcquiredAs *string
	Raw        json.RawMessage
}

// Source finds listings. A query source is asked once per search query; a
// feed source (PerQuery false) once per run with an empty query.
type Source interface {
	Name() string
	PerQuery() bool
	Search(ctx context.Context, query string) ([]Listing, error)
}

// maxPriceCents mirrors the price_observation cap (1 000 000 zł).
const maxPriceCents = 100_000_000

// maxBody caps a source response; a Ceneo results page is about 300 KB.
const maxBody = 16 << 20

// userAgent is a browser's: OLX and Ceneo answer 403 to library defaults.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/130.0 Safari/537.36"

// NewHTTPClient is the client the sources share.
func NewHTTPClient() *http.Client {
	return &http.Client{Timeout: 30 * time.Second}
}

// get fetches url and returns the body of a 200 response.
func get(ctx context.Context, client *http.Client, url, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "pl-PL,pl;q=0.9,en;q=0.8")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxBody {
		return nil, errors.New("response too large")
	}
	return body, nil
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
