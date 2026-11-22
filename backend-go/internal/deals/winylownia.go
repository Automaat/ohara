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
// requests (Crawl-delay: 1); a search's redirect hops wait it too.
const winylowniaCrawlDelay = time.Second

// Search implements Source. IdoSell redirects a search without results
// to its no-results page, which answers 404; that page is an answer
// without listings, any other 404 a failure. Each redirect hop waits
// winylowniaCrawlDelay first.
func (s Winylownia) Search(ctx context.Context, query string) ([]Listing, error) {
	u := s.BaseURL + "?" + url.Values{"text": {query}}.Encode()
	resp, err := fetch.Do(ctx, crawlDelayed(s.Client, winylowniaCrawlDelay), fetch.Request{
		Source:  s.Name(),
		URL:     u,
		Header:  browserHeader("text/html"),
		MaxBody: maxBody,
		OK:      []int{http.StatusOK, http.StatusNotFound},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Name(), err)
	}
	if resp.Status == http.StatusNotFound && !idoSellNoProduct(withoutComments(resp.Body)) {
		return nil, fmt.Errorf("%s: %w", s.Name(), fetch.StatusError{Code: http.StatusNotFound})
	}
	return parseIdoSellSearch(s.Name(), s.BaseURL, resp.Body)
}

// maxRedirects is how many redirects a crawl-delayed client follows, as
// many as net/http's default.
const maxRedirects = 10

// crawlDelayed is a copy of client (http.DefaultClient when nil) that
// waits delay before following each redirect, so the hops of one request
// keep a site's crawl delay too.
func crawlDelayed(client *http.Client, delay time.Duration) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	c := *client
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= maxRedirects {
			return fmt.Errorf("stopped after %d redirects", maxRedirects)
		}
		t := time.NewTimer(delay)
		defer t.Stop()
		select {
		case <-t.C:
			return nil
		case <-req.Context().Done():
			return req.Context().Err()
		}
	}
	return &c
}

var (
	idoSellComment = regexp.MustCompile(`(?s)<!--.*?-->`)
	idoSellAttr    = regexp.MustCompile(`\s([\w:-]+)=(?:"([^"]*)"|'([^']*)')`)
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

// withoutComments is body with its HTML comments taken out, so markup
// inside a comment counts for nothing.
func withoutComments(body []byte) []byte {
	return idoSellComment.ReplaceAll(body, nil)
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

// idoSellNoProduct reports whether body, without comments, is an IdoSell
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
// base the search URL its links resolve against. HTML comments count for
// nothing. Each available product tile (a div classed product with a
// data-product_id) in the results section (section#search classed search
// and products) becomes a new-condition listing, its title and price read
// only inside the tile; a tile marked data-product_disable="true" (out of
// stock) and one without a title, link or price are left out. The
// no-results page is an answer without listings; a page that is neither,
// or a results section without a product tile, is a parse error.
func parseIdoSellSearch(source, base string, body []byte) ([]Listing, error) {
	body = withoutComments(body)
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
