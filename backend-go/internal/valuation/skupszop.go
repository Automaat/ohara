package valuation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// SkupSzopURL is the buy-back shop's site. It has no API: the sell page
// adds an ISBN to a session cart and shows the offer in the cart.
const SkupSzopURL = "https://skupszop.pl/"

// Quote is a buy-back offer for one ISBN. Offered is false when the shop
// does not buy the book now; Cents is then 0. URL is where a reader can
// check the offer.
type Quote struct {
	Offered bool
	Cents   int64
	Title   string
	URL     string
}

// BuyBack asks a buy-back shop what it pays for a book.
type BuyBack interface {
	Name() string
	Quote(ctx context.Context, isbn string) (Quote, error)
}

// SkupSzop quotes books at skupszop.pl. Each quote runs in a fresh session:
// read the sell page for its CSRF token, add the ISBN, read the cart.
type SkupSzop struct {
	Client  *http.Client
	BaseURL string
}

// Name implements BuyBack.
func (SkupSzop) Name() string { return SourceSkupSzop }

// SellURL is the page a quote links to.
func (s SkupSzop) SellURL() string { return strings.TrimSuffix(s.BaseURL, "/") + "/sprzedaj" }

var (
	csrfMeta  = regexp.MustCompile(`<meta\s+name="csrf-token"\s+content="([^"]+)"`)
	cartItem  = regexp.MustCompile(`<div\s+class="buy-cart-item"\s+data-isbn="(\d+)"`)
	cartPrice = regexp.MustCompile(`<div\s+class="price">\s*<span\s+class="value">([^<]*)</span>`)
	cartTitle = regexp.MustCompile(`<p\s+class="text__heading">([^<]*)</p>`)
	tags      = regexp.MustCompile(`<[^>]+>`)
)

// notWanted is how the shop says it does not buy a book at the moment.
const notWanted = "nie jesteśmy zainteresowani"

// maxShopReply caps a shop page; the sell page is about 400 KB.
const maxShopReply = 4 << 20

// Quote implements BuyBack. Each quote counts once in the source health
// metrics: ok once the cart shows the offer or the shop says it does not
// buy the book, with ohara_source_results 1 for an offer and 0 otherwise.
func (s SkupSzop) Quote(ctx context.Context, isbn string) (Quote, error) {
	q, status, err := s.quote(ctx, isbn)
	metrics.ObserveSource(SourceSkupSzop, status)
	if status == metrics.SourceOK {
		offers := 0
		if q.Offered {
			offers = 1
		}
		metrics.SetSourceResults(SourceSkupSzop, offers)
	}
	return q, err
}

// quote runs one quote and names its outcome for ObserveSource. A shop
// message it does not know is a parse error: it may be a reworded refusal.
func (s SkupSzop) quote(ctx context.Context, isbn string) (Quote, string, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return Quote{}, metrics.SourceError, err
	}
	client := &http.Client{Timeout: s.Client.Timeout, Transport: s.Client.Transport, Jar: jar}
	sell := s.SellURL()

	page, status, err := shopDo(ctx, client, http.MethodGet, sell, nil, "", "text/html")
	if err != nil {
		return Quote{}, status, fmt.Errorf("skupszop: sell page: %w", err)
	}
	m := csrfMeta.FindSubmatch(page)
	if m == nil {
		return Quote{}, metrics.SourceParseError, errors.New("skupszop: sell page has no CSRF token")
	}
	reply, status, err := shopDo(ctx, client, http.MethodPost, sell,
		strings.NewReader(url.Values{"isbn": {isbn}}.Encode()), string(m[1]), "application/json")
	if err != nil {
		return Quote{}, status, fmt.Errorf("skupszop: add %s: %w", isbn, err)
	}
	var added struct {
		Response *string `json:"response"`
	}
	if err := json.Unmarshal(reply, &added); err != nil {
		return Quote{}, metrics.SourceParseError, fmt.Errorf("skupszop: add %s: decode: %w", isbn, err)
	}
	if added.Response != nil {
		if msg := plainText(*added.Response); msg != "" {
			if strings.Contains(strings.ToLower(msg), notWanted) {
				return Quote{URL: sell}, metrics.SourceOK, nil
			}
			return Quote{}, metrics.SourceParseError, fmt.Errorf("skupszop: add %s: %s", isbn, msg)
		}
	}
	cart, status, err := shopDo(ctx, client, http.MethodGet, sell, nil, "", "text/html")
	if err != nil {
		return Quote{}, status, fmt.Errorf("skupszop: cart: %w", err)
	}
	q, ok := parseCart(cart, isbn)
	if !ok {
		return Quote{}, metrics.SourceParseError, fmt.Errorf("skupszop: cart has no offer for %s", isbn)
	}
	q.URL = sell
	return q, metrics.SourceOK, nil
}

// parseCart finds the offer for isbn in a sell page's cart. A cart row
// holds its price and title before the next row starts.
func parseCart(page []byte, isbn string) (Quote, bool) {
	text := string(page)
	rows := cartItem.FindAllStringSubmatchIndex(text, -1)
	for i, r := range rows {
		if text[r[2]:r[3]] != isbn {
			continue
		}
		end := len(text)
		if i+1 < len(rows) {
			end = rows[i+1][0]
		}
		row := text[r[1]:end]
		pm := cartPrice.FindStringSubmatch(row)
		if pm == nil {
			return Quote{}, false
		}
		cents, ok := parsePLN(html.UnescapeString(pm[1]))
		if !ok {
			return Quote{}, false
		}
		q := Quote{Offered: true, Cents: cents}
		if tm := cartTitle.FindStringSubmatch(row); tm != nil {
			q.Title = strings.TrimSpace(html.UnescapeString(tm[1]))
		}
		return q, true
	}
	return Quote{}, false
}

// shopDo sends one request in the quote's session and returns a 200 body.
// A token marks the request as the page's own XHR, which Laravel's CSRF
// check needs. It also returns the request's ObserveSource status; an
// error status counts as itself, however long its page.
func shopDo(ctx context.Context, client *http.Client, method, target string, body io.Reader, token, accept string) ([]byte, string, error) {
	if body == nil {
		body = http.NoBody
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, metrics.SourceError, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", accept)
	req.Header.Set("Accept-Language", "pl-PL,pl;q=0.9")
	if token != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
		req.Header.Set("X-CSRF-TOKEN", token)
		req.Header.Set("X-Requested-With", "XMLHttpRequest")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, metrics.SourceError, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxShopReply+1))
	if err != nil {
		return nil, metrics.SourceError, err
	}
	status := metrics.SourceOK
	if resp.StatusCode != http.StatusOK {
		status = fmt.Sprintf("http_%d", resp.StatusCode)
	}
	if len(data) > maxShopReply {
		if status == metrics.SourceOK {
			status = metrics.SourceParseError
		}
		return nil, status, errors.New("response too large")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, status, fmt.Errorf("status %d", resp.StatusCode)
	}
	return data, status, nil
}

// plainText strips tags and collapses spaces.
func plainText(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tags.ReplaceAllString(s, " "))), " ")
}

var priceChars = regexp.MustCompile(`[^\d,.]`)

// parsePLN reads "7,32 zł" or "1 000,00 zł" into grosze within the
// price_observation cap.
func parsePLN(s string) (int64, bool) {
	text := priceChars.ReplaceAllString(s, "")
	if strings.Contains(text, ",") {
		text = strings.ReplaceAll(text, ".", "")
		text = strings.Replace(text, ",", ".", 1)
	}
	v, err := strconv.ParseFloat(text, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	cents := int64(v*100 + 0.5)
	if cents > maxPriceCents {
		return 0, false
	}
	return cents, true
}

// maxPriceCents mirrors the price_observation cap (1 000 000 zł).
const maxPriceCents = 100_000_000

// userAgent is a browser's: the shops answer library defaults with a
// challenge page.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/130.0 Safari/537.36"
