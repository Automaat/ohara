package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// SwiatKsiazkiURL is the GraphQL endpoint of swiatksiazki.pl, a shop of new
// books, films and music. The source asks it with GET only: robots.txt
// disallows the shop's REST API.
const SwiatKsiazkiURL = "https://www.swiatksiazki.pl/graphql"

// swiatKsiazkiShop is where a product's url_key is a page.
const swiatKsiazkiShop = "https://www.swiatksiazki.pl/"

// swiatKsiazkiPageSize is how many products one query asks for; the
// source reads only that first page, in the shop's relevance order.
const swiatKsiazkiPageSize = 50

// swiatKsiazkiFields are the product fields a query asks for. Product
// names leave out the author and the artist, which dictionary holds.
const swiatKsiazkiFields = `items{sku name url_key stock_status small_image{url} ` +
	`price_range{minimum_price{final_price{value currency}}} ` +
	`dictionary{authors{name} performers{name} composers{name}}}`

// SwiatKsiazki searches the swiatksiazki.pl shop, 50 products per query.
// Its listings are new copies.
type SwiatKsiazki struct {
	Client  *http.Client
	BaseURL string
}

// Name implements Source.
func (SwiatKsiazki) Name() string { return "swiatksiazki" }

// PerQuery implements Source.
func (SwiatKsiazki) PerQuery() bool { return true }

// Search implements Source: one GET of the GraphQL endpoint with the
// products search in the query parameter.
func (s SwiatKsiazki) Search(ctx context.Context, query string) ([]Listing, error) {
	document, err := swiatKsiazkiQuery(query)
	if err != nil {
		return nil, err
	}
	u := s.BaseURL + "?" + url.Values{"query": {document}}.Encode()
	body, err := get(ctx, s.Client, s.Name(), u, "application/json")
	if err != nil {
		return nil, fmt.Errorf("swiatksiazki: %w", err)
	}
	listings, err := parseSwiatKsiazki(body)
	if err != nil {
		return nil, err
	}
	for i := range listings {
		listings[i].Query = query
	}
	return listings, nil
}

// swiatKsiazkiQuery is the GraphQL document searching the shop for query.
// A JSON string is a valid GraphQL string literal, so the search cannot
// break out of it.
func swiatKsiazkiQuery(query string) (string, error) {
	literal, err := json.Marshal(query)
	if err != nil {
		return "", fmt.Errorf("swiatksiazki: query: %w", err)
	}
	return "{products(search:" + string(literal) + ",pageSize:" + strconv.Itoa(swiatKsiazkiPageSize) +
		"){" + swiatKsiazkiFields + "}}", nil
}

type swiatKsiazkiResponse struct {
	Errors []json.RawMessage `json:"errors"`
	Data   *struct {
		Products *struct {
			Items []json.RawMessage `json:"items"`
		} `json:"products"`
	} `json:"data"`
}

type swiatKsiazkiProduct struct {
	SKU         string `json:"sku"`
	Name        string `json:"name"`
	URLKey      string `json:"url_key"`
	StockStatus string `json:"stock_status"`
	SmallImage  *struct {
		URL string `json:"url"`
	} `json:"small_image"`
	PriceRange struct {
		MinimumPrice struct {
			FinalPrice struct {
				Value    *float64 `json:"value"`
				Currency string   `json:"currency"`
			} `json:"final_price"`
		} `json:"minimum_price"`
	} `json:"price_range"`
	Dictionary struct {
		Authors    []swiatKsiazkiPerson `json:"authors"`
		Performers []swiatKsiazkiPerson `json:"performers"`
		Composers  []swiatKsiazkiPerson `json:"composers"`
	} `json:"dictionary"`
}

type swiatKsiazkiPerson struct {
	Name string `json:"name"`
}

// swiatKsiazkiKinds are the title kinds of a product by its url_key's
// last word, the shop's catalog: a film disc can be a movie or a TV
// season. A product with another ending says nothing of its kind.
var swiatKsiazkiKinds = map[string][]string{
	"ksiazka":     {"book"},
	"podreczniki": {"book"},
	"muzyka":      {"music"},
	"film":        {"movie", "tv"},
}

// swiatKsiazkiFiles are the url_key endings of products that are files,
// not copies: e-books and audio files.
var swiatKsiazkiFiles = []string{"-e-book", "-audiobook"}

// parseSwiatKsiazki reads a products search answer into listings. A
// product out of stock, a file (swiatKsiazkiFiles) or one without a PLN
// price is left out. The listing title is the product's authors,
// performers and composers, then its name ("Pink Floyd - The Dark Side of
// the Moon (Vinyl)"), since the name alone leaves them out and the
// matcher needs a book's author and an album's artist. Its Kinds come
// from the url_key (swiatKsiazkiKinds), so a book about an album, whose
// name carries the artist, never alerts an album wish. The name's disc
// count follows its medium (discCountAfterMedium). An answer with
// GraphQL errors or without a products list is a parse error.
func parseSwiatKsiazki(body []byte) ([]Listing, error) {
	var resp swiatKsiazkiResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, parseError{fmt.Errorf("swiatksiazki: decode: %w", err)}
	}
	if len(resp.Errors) > 0 {
		return nil, parseError{fmt.Errorf("swiatksiazki: graphql errors: %s", firstMessage(resp.Errors[0]))}
	}
	if resp.Data == nil || resp.Data.Products == nil || resp.Data.Products.Items == nil {
		return nil, parseError{errors.New("swiatksiazki: response has no products")}
	}
	out := make([]Listing, 0, len(resp.Data.Products.Items))
	for _, raw := range resp.Data.Products.Items {
		var p swiatKsiazkiProduct
		if json.Unmarshal(raw, &p) != nil {
			continue
		}
		name := discCountAfterMedium(strings.TrimSpace(p.Name))
		price := p.PriceRange.MinimumPrice.FinalPrice
		if p.SKU == "" || name == "" || p.URLKey == "" || p.StockStatus != "IN_STOCK" ||
			price.Value == nil || price.Currency != "PLN" || isSwiatKsiazkiFile(p.URLKey) {
			continue
		}
		cents, ok := toCents(*price.Value)
		if !ok {
			continue
		}
		l := Listing{
			Source:     "swiatksiazki",
			ExternalID: p.SKU,
			Title:      swiatKsiazkiTitle(name, p),
			URL:        swiatKsiazkiShop + url.PathEscape(p.URLKey) + ".html",
			PriceCents: cents,
			AcquiredAs: new("new"),
			Raw:        raw,
			Kinds:      swiatKsiazkiKinds[p.URLKey[strings.LastIndexByte(p.URLKey, '-')+1:]],
		}
		if p.SmallImage != nil {
			l.PhotoURL = imageURL(p.SmallImage.URL)
		}
		out = append(out, l)
	}
	return out, nil
}

// isSwiatKsiazkiFile reports whether urlKey names an e-book or an audio
// file.
func isSwiatKsiazkiFile(urlKey string) bool {
	return slices.ContainsFunc(swiatKsiazkiFiles, func(end string) bool { return strings.HasSuffix(urlKey, end) })
}

// swiatKsiazkiTitle is name after the product's people, each once, or
// name alone when it has none (films).
func swiatKsiazkiTitle(name string, p swiatKsiazkiProduct) string {
	var people []string
	for _, group := range [][]swiatKsiazkiPerson{p.Dictionary.Authors, p.Dictionary.Performers, p.Dictionary.Composers} {
		for _, person := range group {
			if n := strings.TrimSpace(person.Name); n != "" && !slices.Contains(people, n) {
				people = append(people, n)
			}
		}
	}
	if len(people) == 0 {
		return name
	}
	return strings.Join(people, ", ") + " - " + name
}

// countedMedium is a parenthesized disc count before its medium, as the
// shop names a film or album on several discs: "(5 DVD)", "(2 Blu-ray)",
// "(2 CD)".
var countedMedium = regexp.MustCompile(`(?i)\((\d{1,3})\s+((?:dvd|blu-?ray|bd|cd|4k|uhd|ultra hd)\b[^()]*?)\s*\)`)

// discCountAfterMedium moves a disc count behind its medium, "Shogun (5
// DVD)" to "Shogun (DVD, 5 płyt)": the matcher reads a number right after
// a film's name as a sequel ("Shogun 5"), while a count of discs past the
// name describes one title's edition. One disc needs no count.
func discCountAfterMedium(name string) string {
	return countedMedium.ReplaceAllStringFunc(name, func(group string) string {
		m := countedMedium.FindStringSubmatch(group)
		n, err := strconv.Atoi(m[1])
		if err != nil || n < 2 {
			return "(" + m[2] + ")"
		}
		return "(" + m[2] + ", " + m[1] + " " + discs(n) + ")"
	})
}

// discs is the Polish plural of "płyta" after n, at least 2.
func discs(n int) string {
	if d, t := n%10, n%100; d >= 2 && d <= 4 && (t < 12 || t > 14) {
		return "płyty"
	}
	return "płyt"
}

// firstMessage is the message of a GraphQL error, or the error itself
// when it has none.
func firstMessage(raw json.RawMessage) string {
	var e struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	return string(raw)
}
