package deals

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// CeneoURL is the base of Ceneo's site search; the query follows
// ";szukaj-".
const CeneoURL = "https://www.ceneo.pl/"

// Ceneo searches Ceneo for new copies. Each result tile is a product with
// the lowest price across shops, so a listing is a product, not a shop
// offer.
type Ceneo struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (Ceneo) Name() string { return "ceneo" }

// PerQuery implements Source.
func (Ceneo) PerQuery() bool { return true }

// Search implements Source.
func (c Ceneo) Search(ctx context.Context, query string) ([]Listing, error) {
	body, err := get(ctx, c.Client, c.BaseURL+";szukaj-"+url.QueryEscape(query), "text/html,application/xhtml+xml")
	if err != nil {
		return nil, fmt.Errorf("ceneo: %w", err)
	}
	return parseCeneo(body, strings.TrimSuffix(c.BaseURL, "/")), nil
}

// ceneoTile opens a search result: a div whose class list starts with
// cat-prod-row, which carries the product in data- attributes.
var ceneoTile = regexp.MustCompile(`<div\s+class="\s*cat-prod-row\s[^>]*>`)

// tagAttr is one name="value" pair inside a tag, as price_tracker.py reads
// them.
var tagAttr = regexp.MustCompile(`([\w-]+)="([^"]*)"`)

// attrs returns a tag's attributes with lowercased names and unescaped
// values.
func attrs(tag string) map[string]string {
	out := map[string]string{}
	for _, m := range tagAttr.FindAllStringSubmatch(tag, -1) {
		out[strings.ToLower(m[1])] = html.UnescapeString(m[2])
	}
	return out
}

type ceneoRaw struct {
	ProductID string `json:"product_id"`
	Name      string `json:"name"`
	MinPrice  string `json:"min_price"`
	Shop      string `json:"shop"`
}

// parseCeneo reads the products of a search results page. A page that is a
// single product, or a bot check, has no tiles and yields no listings.
func parseCeneo(page []byte, base string) []Listing {
	var out []Listing
	seen := map[string]bool{}
	for _, tag := range ceneoTile.FindAllString(string(page), -1) {
		a := attrs(tag)
		pid, name := a["data-productid"], strings.TrimSpace(a["data-productname"])
		if pid == "" || name == "" || seen[pid] {
			continue
		}
		cents, ok := parsePrice(a["data-productminprice"])
		if !ok {
			continue
		}
		seen[pid] = true
		raw, err := json.Marshal(ceneoRaw{
			ProductID: pid, Name: name, MinPrice: a["data-productminprice"], Shop: a["data-shopurl"],
		})
		if err != nil {
			continue
		}
		out = append(out, Listing{
			Source:     "ceneo",
			ExternalID: pid,
			Title:      name,
			URL:        base + "/" + pid,
			PriceCents: cents,
			AcquiredAs: new("new"),
			Raw:        raw,
		})
	}
	return out
}
