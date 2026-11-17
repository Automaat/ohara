package movie

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// Ceneo reads product names from the Ceneo search page for a barcode. Ceneo
// has no public product API; the page carries each result's name and
// category in data attributes, which change less often than its markup.
type Ceneo struct {
	Client  *http.Client
	BaseURL string
}

// ceneoMaxResults keeps a broad search from flooding the TMDB step.
const ceneoMaxResults = 3

var (
	ceneoRow      = regexp.MustCompile(`(?s)<div[^>]*\bcat-prod-row\b[^>]*>`)
	ceneoName     = regexp.MustCompile(`\bdata-productName="([^"]*)"`)
	ceneoCategory = regexp.MustCompile(`\bdata-GACategoryName="([^"]*)"`)
	ceneoNoMatch  = regexp.MustCompile(`Brak wyników dla frazy`)
	ceneoListing  = regexp.MustCompile(`js_search-results`)
)

// Name is "ceneo".
func (Ceneo) Name() string { return "ceneo" }

// Products searches Ceneo for barcode and keeps results from its film
// categories.
func (c Ceneo) Products(ctx context.Context, code string) ([]Product, error) {
	base := c.BaseURL
	if base == "" {
		base = "https://www.ceneo.pl"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/;szukaj-"+url.PathEscape(code), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("ceneo: %w", err)
	}
	req.Header.Set("User-Agent", browserUA)
	req.Header.Set("Accept-Language", "pl-PL,pl;q=0.9")
	_, body, err := get(c.client(), c.Name(), req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	products, err := parseCeneo(string(body))
	if err != nil {
		return nil, parseFailed(c.Name(), err)
	}
	succeeded(c.Name())
	return products, nil
}

func parseCeneo(page string) ([]Product, error) {
	if ceneoNoMatch.MatchString(page) {
		return []Product{}, nil
	}
	if !ceneoListing.MatchString(page) {
		return nil, fmt.Errorf("no search results container")
	}
	products := []Product{}
	seen := map[string]bool{}
	for _, row := range ceneoRow.FindAllString(page, -1) {
		name := ceneoName.FindStringSubmatch(row)
		if name == nil {
			continue
		}
		var category string
		if m := ceneoCategory.FindStringSubmatch(row); m != nil {
			category = html.UnescapeString(m[1])
		}
		if !strings.HasPrefix(category, "/Filmy") {
			continue
		}
		n := strings.TrimSpace(html.UnescapeString(name[1]))
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		products = append(products, Product{Source: "ceneo", Name: n, Category: category})
		if len(products) == ceneoMaxResults {
			break
		}
	}
	return products, nil
}

func (c Ceneo) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return http.DefaultClient
}
