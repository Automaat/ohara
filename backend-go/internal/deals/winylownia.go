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
	"strings"

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

// Search implements Source. IdoSell redirects a search without results
// to its no-results page, which answers 404; that page is an answer
// without listings, any other 404 a failure.
func (s Winylownia) Search(ctx context.Context, query string) ([]Listing, error) {
	u := s.BaseURL + "?" + url.Values{"text": {query}}.Encode()
	resp, err := fetch.Do(ctx, s.Client, fetch.Request{
		Source:  s.Name(),
		URL:     u,
		Header:  browserHeader("text/html"),
		MaxBody: maxBody,
		OK:      []int{http.StatusOK, http.StatusNotFound},
	})
	if err != nil {
		return nil, fmt.Errorf("%s: %w", s.Name(), err)
	}
	if resp.Status == http.StatusNotFound && !idoSellNoProduct(resp.Body) {
		return nil, fmt.Errorf("%s: %w", s.Name(), fetch.StatusError{Code: http.StatusNotFound})
	}
	return parseIdoSellSearch(s.Name(), s.BaseURL, resp.Body)
}

var (
	idoSellNoProductPage = regexp.MustCompile(`<div id="container" class="noproduct_page[\s"]`)
	idoSellResults       = regexp.MustCompile(`<section id="search" class="search products[\s"]`)
	idoSellTile          = regexp.MustCompile(`<div class="product [^>]*\bdata-product_id="(\d+)"[^>]*>`)
	idoSellName          = regexp.MustCompile(`<a class="product__name"[^>]*>`)
	idoSellPrice         = regexp.MustCompile(`<strong class="price --main">([^<]*)<`)
	idoSellDisabled      = []byte(`data-product_disable="true"`)
	idoSellAttr          = regexp.MustCompile(`\s([a-z_-]+)=(?:"([^"]*)"|'([^']*)')`)
)

// idoSellNoProduct reports whether body is an IdoSell shop's no-results
// page.
func idoSellNoProduct(body []byte) bool {
	return idoSellNoProductPage.Match(body)
}

// idoSellRaw is the part of a product tile kept with its listing.
type idoSellRaw struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
	Price string `json:"price"`
}

// parseIdoSellSearch reads an IdoSell shop's search page for source, with
// base the search URL its links resolve against. Each available product
// tile becomes a new-condition listing; a tile marked
// data-product_disable="true" (out of stock) and one without a title,
// link or price are left out. The no-results page is an answer without
// listings; a page that is neither, or a results page without a product
// tile, is a parse error.
func parseIdoSellSearch(source, base string, body []byte) ([]Listing, error) {
	if idoSellNoProduct(body) {
		return nil, nil
	}
	start := idoSellResults.FindIndex(body)
	if start == nil {
		return nil, parseError{errors.New(source + ": not a search results page")}
	}
	section := body[start[0]:]
	if end := bytes.Index(section, []byte("</section>")); end >= 0 {
		section = section[:end]
	}
	tiles := idoSellTile.FindAllSubmatchIndex(section, -1)
	if len(tiles) == 0 {
		return nil, parseError{errors.New(source + ": results page holds no product")}
	}
	baseURL, err := url.Parse(base)
	if err != nil {
		return nil, fmt.Errorf("%s: base URL: %w", source, err)
	}
	out := make([]Listing, 0, len(tiles))
	for i, t := range tiles {
		open := section[t[0]:t[1]]
		if bytes.Contains(open, idoSellDisabled) {
			continue
		}
		end := len(section)
		if i+1 < len(tiles) {
			end = tiles[i+1][0]
		}
		l, ok := idoSellListing(source, baseURL, string(section[t[2]:t[3]]), section[t[1]:end])
		if !ok {
			continue
		}
		out = append(out, l)
	}
	return out, nil
}

// idoSellListing reads one available tile, id and its markup after the
// opening tag.
func idoSellListing(source string, base *url.URL, id string, tile []byte) (Listing, bool) {
	name := idoSellName.Find(tile)
	if name == nil {
		return Listing{}, false
	}
	attrs := tagAttrs(name)
	title := strings.TrimSpace(attrs["title"])
	link := idoSellLink(base, attrs["href"])
	price := idoSellPrice.FindSubmatch(tile)
	if title == "" || link == "" || price == nil {
		return Listing{}, false
	}
	priceText := strings.TrimSpace(html.UnescapeString(string(price[1])))
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
