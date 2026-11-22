package deals

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
)

// WinylowniaURL is the product search of winylownia.pl, a shop of new
// vinyl records (IdoSell).
const WinylowniaURL = "https://winylownia.pl/search.php"

// Winylownia searches the winylownia.pl shop: the first results page of
// each query, in the shop's default order. Its listings are new copies.
// The shop's terms forbid copying its photos and descriptions, so a
// listing keeps only a tile's title, price and link.
type Winylownia struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (Winylownia) Name() string { return "winylownia" }

// PerQuery implements Source.
func (Winylownia) PerQuery() bool { return true }

// Kinds implements KindSource: Winylownia sells records.
func (Winylownia) Kinds() []string { return []string{"music"} }

// winylowniaCrawlDelay is the gap the shop's robots.txt asks between two
// requests (Crawl-delay: 1). The hunter's jittered OHARA_DEALS_DELAY can
// fall below it, and a search's redirect hops are not paced at all, so
// every request to the shop's host waits for it on winylowniaClock.
const winylowniaCrawlDelay = time.Second

// winylowniaClock spaces every request sent to the Winylownia host.
var winylowniaClock = newCrawlClock()

// Search implements Source. IdoSell redirects a search without results
// to its no-results page, which answers 404; that page is an answer
// without listings, any other 404 a failure. Every request, redirect hops
// included, is sent at least winylowniaCrawlDelay after the last one to
// the same host.
func (s Winylownia) Search(ctx context.Context, query string) ([]Listing, error) {
	u := s.BaseURL + "?" + url.Values{"text": {query}}.Encode()
	resp, err := fetch.Do(ctx, crawlDelayed(s.Client, winylowniaClock, winylowniaCrawlDelay), fetch.Request{
		Source:  s.Name(),
		URL:     u,
		Header:  browserHeader("text/html"),
		MaxBody: maxBody,
		OK:      []int{http.StatusOK, http.StatusNotFound},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Name(), err)
	}
	if resp.Status == http.StatusNotFound && !idoSellNoProduct(withoutInert(resp.Body)) {
		return nil, fmt.Errorf("%s: %w", s.Name(), fetch.StatusError{Code: http.StatusNotFound})
	}
	return parseIdoSellSearch(s.Name(), s.BaseURL, resp.Body)
}

// crawlClock spaces the requests sent to each host.
type crawlClock struct {
	mu   sync.Mutex
	next map[string]time.Time
}

func newCrawlClock() *crawlClock { return &crawlClock{next: map[string]time.Time{}} }

// wait books the next free slot for host, delay after the one before, and
// blocks until it comes or ctx ends; the slot after it starts delay after
// the wait ends. A slot booked for a request that was then canceled stays
// taken.
func (c *crawlClock) wait(ctx context.Context, host string, delay time.Duration) error {
	c.mu.Lock()
	at := c.next[host]
	if now := time.Now(); at.Before(now) {
		at = now
	}
	c.next[host] = at.Add(delay)
	c.mu.Unlock()
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
		return ctx.Err()
	}
	// The timer fires a little late; the next slot counts from now.
	c.mu.Lock()
	if later := time.Now().Add(delay); later.After(c.next[host]) {
		c.next[host] = later
	}
	c.mu.Unlock()
	return nil
}

// crawlDelayTransport sends each request once clock lets it through.
type crawlDelayTransport struct {
	base  http.RoundTripper
	clock *crawlClock
	delay time.Duration
}

// RoundTrip implements http.RoundTripper.
func (t crawlDelayTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if err := t.clock.wait(r.Context(), r.URL.Host, t.delay); err != nil {
		return nil, err
	}
	return t.base.RoundTrip(r)
}

// crawlDelayed is a copy of client (http.DefaultClient when nil) whose
// every request, redirect hops included, waits on clock until delay has
// passed since the last request to the same host.
func crawlDelayed(client *http.Client, clock *crawlClock, delay time.Duration) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	base := c.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	c.Transport = crawlDelayTransport{base: base, clock: clock, delay: delay}
	return &c
}

var (
	// idoSellInert matches what a browser never renders as markup: comments
	// and the content of script and style elements.
	idoSellInert = regexp.MustCompile(`(?is)<!--.*?-->|<script\b.*?</script\s*>|<style\b.*?</style\s*>`)
	idoSellAttr  = regexp.MustCompile(`\s([\w:-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)
	// idoSellTags match an element's opening tag, idoSellEnds its opening
	// or closing tag (group 1 is "/" for a closing one), by element name.
	idoSellTags = tagPatterns(`<`)
	idoSellEnds = tagPatterns(`<(/?)`)
)

// tagPatterns are the tag patterns, starting with prefix, of the elements
// a search page is read from. A quoted attribute value may hold ">".
func tagPatterns(prefix string) map[string]*regexp.Regexp {
	out := map[string]*regexp.Regexp{}
	for _, name := range []string{"div", "section", "a", "strong"} {
		out[name] = regexp.MustCompile(`(?i)` + prefix + name + `\b(?:[^>"']|"[^"]*"|'[^']*')*>`)
	}
	return out
}

// withoutInert is body without its HTML comments, scripts and styles, so
// markup inside them counts for nothing.
func withoutInert(body []byte) []byte {
	return idoSellInert.ReplaceAll(body, nil)
}

// htmlElement is one element found in a page: its attributes and the
// markup between its opening and closing tags.
type htmlElement struct {
	attrs map[string]string
	inner []byte
	end   int
}

// findElements returns the name elements in b, in order, whose attributes
// satisfy match. An element's inner markup ends at its own closing tag
// (nested elements of the same name counted), or at the end of b; a match
// inside another match is not returned.
func findElements(b []byte, name string, match func(map[string]string) bool) []htmlElement {
	var out []htmlElement
	for from := 0; from < len(b); {
		loc := idoSellTags[name].FindIndex(b[from:])
		if loc == nil {
			break
		}
		open := b[from+loc[0] : from+loc[1]]
		innerStart := from + loc[1]
		attrs := tagAttrs(open)
		if !match(attrs) {
			from = innerStart
			continue
		}
		innerEnd, end := closingTag(b, innerStart, name)
		out = append(out, htmlElement{attrs: attrs, inner: b[innerStart:innerEnd], end: end})
		from = end
	}
	return out
}

// closingTag finds the tag closing the name element whose content starts
// at from: the index where its closing tag starts and the one after it,
// or len(b) twice when it is not closed.
func closingTag(b []byte, from int, name string) (int, int) {
	depth := 1
	for _, m := range idoSellEnds[name].FindAllSubmatchIndex(b[from:], -1) {
		if m[3] > m[2] {
			depth--
		} else if !bytes.HasSuffix(bytes.TrimRight(b[from+m[0]:from+m[1]], ">"), []byte("/")) {
			depth++
		}
		if depth == 0 {
			return from + m[0], from + m[1]
		}
	}
	return len(b), len(b)
}

// hasClasses reports whether the class attribute in attrs holds every
// token, in any order.
func hasClasses(attrs map[string]string, tokens ...string) bool {
	classes := strings.Fields(attrs["class"])
	for _, t := range tokens {
		if !slices.Contains(classes, t) {
			return false
		}
	}
	return true
}

// idoSellNoProduct reports whether body, without inert markup, is an IdoSell
// shop's no-results page: its container div is classed noproduct_page.
func idoSellNoProduct(body []byte) bool {
	return len(findElements(body, "div", func(a map[string]string) bool {
		return a["id"] == "container" && hasClasses(a, "noproduct_page")
	})) > 0
}

// idoSellRaw is the part of a product tile kept with its listing.
type idoSellRaw struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Price string `json:"price"`
}

// parseIdoSellSearch reads an IdoSell shop's search page for source, with
// base the search URL its links resolve against. HTML comments, scripts
// and styles count for nothing. Each available product tile (a div classed product with a
// data-product_id) in the results section (section#search classed search
// and products) becomes a new-condition listing, its title and price read
// only inside the tile; a tile marked data-product_disable="true" (out of
// stock) and one without a title, link or price are left out. The
// no-results page is an answer without listings; a page that is neither,
// or a results section without a product tile, is a parse error.
func parseIdoSellSearch(source, base string, body []byte) ([]Listing, error) {
	body = withoutInert(body)
	if idoSellNoProduct(body) {
		return nil, nil
	}
	sections := findElements(body, "section", func(a map[string]string) bool {
		return a["id"] == "search" && hasClasses(a, "search", "products")
	})
	if len(sections) == 0 {
		return nil, parseError{errors.New(source + ": not a search results page")}
	}
	tiles := findElements(sections[0].inner, "div", func(a map[string]string) bool {
		return hasClasses(a, "product") && idoSellID.MatchString(a["data-product_id"])
	})
	if len(tiles) == 0 {
		return nil, parseError{errors.New(source + ": results page holds no product")}
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("%s: base URL: %w", source, err)
	}
	out := make([]Listing, 0, len(tiles))
	for _, t := range tiles {
		if t.attrs["data-product_disable"] == "true" {
			continue
		}
		l, ok := idoSellListing(source, baseURL, t.attrs["data-product_id"], t.inner)
		if !ok {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

var idoSellID = regexp.MustCompile(`^\d+$`)

// idoSellListing reads one available tile: its id and the markup inside
// it.
func idoSellListing(source string, base *url.URL, id string, tile []byte) (Listing, bool) {
	names := findElements(tile, "a", func(a map[string]string) bool { return hasClasses(a, "product__name") })
	prices := findElements(tile, "strong", func(a map[string]string) bool { return hasClasses(a, "price", "--main") })
	if len(names) == 0 || len(prices) == 0 {
		return Listing{}, false
	}
	title := strings.TrimSpace(names[0].attrs["title"])
	link := idoSellLink(base, names[0].attrs["href"])
	if title == "" || link == "" {
		return Listing{}, false
	}
	text := prices[0].inner
	if i := bytes.IndexByte(text, '<'); i >= 0 {
		text = text[:i]
	}
	priceText := strings.TrimSpace(html.UnescapeString(string(text)))
	cents, ok := parsePrice(priceText)
	if !ok || cents == 0 {
		return Listing{}, false
	}
	raw, err := json.Marshal(idoSellRaw{ID: id, Title: title, URL: link, Price: priceText})
	if err != nil {
		return Listing{}, false
	}
	return Listing{
		Source:     source,
		ExternalID: id,
		Title:      title,
		URL:        link,
		PriceCents: cents,
		AcquiredAs: new("new"),
		Raw:        raw,
	}, true
}

// tagAttrs reads the quoted attributes of one HTML tag, entities decoded.
// IdoSell single-quotes a value holding a double quote, such as the title
// of a `2LP + 10"` set.
func tagAttrs(tag []byte) map[string]string {
	out := map[string]string{}
	for _, m := range idoSellAttr.FindAllSubmatchIndex(tag, -1) {
		v := m[4:6]
		if v[0] < 0 {
			v = m[6:8]
		}
		out[string(tag[m[2]:m[3]])] = html.UnescapeString(string(tag[v[0]:v[1]]))
	}
	return out
}

// idoSellLink is href resolved against base, without the query_id
// parameter IdoSell adds to search result links, or "" when it is not an
// http(s) link.
func idoSellLink(base *url.URL, href string) string {
	if strings.TrimSpace(href) == "" {
		return ""
	}
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return ""
	}
	u := base.ResolveReference(ref)
	if u.Hostname() == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	q := u.Query()
	q.Del("query_id")
	u.RawQuery = q.Encode()
	u.Fragment = ""
	return u.String()
}
