package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// DVDmaxURL is DVDmax's catalog search for product titles.
const DVDmaxURL = "https://api.dvdmax.pl/app/v2/products/q/title/"

// DVDmax searches the first 50 products in the shop's film catalog.
type DVDmax struct {
	Client  *http.Client
	BaseURL string
}

func (DVDmax) Name() string        { return "dvdmax" }
func (DVDmax) PerQuery() bool      { return true }
func (DVDmax) Kinds() []string     { return []string{"movie"} }
func (DVDmax) CanaryQuery() string { return "Blade Runner" }

func (DVDmax) wishQueries(w *Want) []string { return w.queries(false) }

// Search asks for films by title. The structured medium in the answer lets
// the matcher distinguish DVD, Blu-ray and UHD after fetching the products.
func (d DVDmax) Search(ctx context.Context, query string) ([]Listing, error) {
	u := d.BaseURL + "?" + url.Values{
		"title":        {query},
		"page":         {"1"},
		"limit":        {"50"},
		"filters[cat]": {"1"},
	}.Encode()
	body, err := get(ctx, d.Client, d.Name(), u, "application/json")
	if err != nil {
		return nil, fmt.Errorf("dvdmax: %w", err)
	}
	return parseDVDmax(body, query)
}

type dvdmaxProduct struct {
	ID        int64    `json:"aId"`
	Title     string   `json:"aName"`
	Price     string   `json:"aPrice"`
	Promotion string   `json:"aPricePromotion"`
	Status    string   `json:"dictionaryDcStatus"`
	Format    string   `json:"nosnik"`
	Images    []string `json:"images"`
	URL       string   `json:"url"`
}

func parseDVDmax(body []byte, query string) ([]Listing, error) {
	var answer struct {
		Products []dvdmaxProduct `json:"products"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return nil, parseError{fmt.Errorf("dvdmax: decode: %w", err)}
	}
	if answer.Products == nil {
		return nil, parseError{errors.New("dvdmax: response has no products")}
	}
	var out []Listing
	queryWords := words(query)
	for i := range answer.Products {
		product := &answer.Products[i]
		if product.ID <= 0 || strings.TrimSpace(product.Title) == "" {
			return nil, parseError{errors.New("dvdmax: product has no ID or title")}
		}
		if !dvdmaxTitleMatches(product.Title, queryWords) {
			continue
		}
		if listing, ok := product.listing(); ok {
			out = append(out, listing)
		}
	}
	return out, nil
}

// DVDmax's fuzzy search returns longer, unrelated titles containing the query.
// Require a whole title and allow parenthesized translations, which the shop
// uses for bilingual titles like "Past Lives (Poprzednie życie)".
func dvdmaxTitleMatches(title string, query []string) bool {
	if len(query) == 0 {
		return false
	}
	if slices.Equal(words(title), query) {
		return true
	}
	for _, part := range strings.FieldsFunc(title, func(r rune) bool { return r == '(' || r == ')' }) {
		if slices.Equal(words(part), query) {
			return true
		}
	}
	return false
}

func (p *dvdmaxProduct) listing() (Listing, bool) {
	switch p.Status {
	case "DOSTEPNY24", "DOSTEPNY48H", "DOSTEPNY72H", "DOSTEPNY120", "DOSTEPNY48", "DOSTEPNY72", "DOSTEPNY336", "PROMOCJA", "ZAPOWIEDZ":
	default:
		return Listing{}, false
	}
	format, ok := map[string]string{"DVD": "DVD", "BLU-RAY": "Blu-ray", "BLU-RAY 4K": "4K UHD"}[p.Format]
	// The site separates the titles in multi-film packages with a spaced slash.
	if !ok || strings.Contains(p.Title, " / ") {
		return Listing{}, false
	}
	link, err := url.Parse(p.URL)
	if err != nil || link.Host != "" || link.IsAbs() || !strings.HasPrefix(link.Path, "/") || strings.HasPrefix(link.Path, "//") {
		return Listing{}, false
	}
	cents, ok := dvdmaxPrice(p.Promotion)
	if !ok {
		cents, ok = dvdmaxPrice(p.Price)
	}
	if !ok {
		return Listing{}, false
	}
	var photo string
	if len(p.Images) > 0 && p.Images[0] != "" {
		photo = imageURL("https://media.dvdmax.pl/IMG/360x460/" + p.Images[0])
	}
	link.Scheme, link.Host = "https", "dvdmax.pl"
	condition := "new"
	return Listing{
		Source: DVDmax{}.Name(), ExternalID: strconv.FormatInt(p.ID, 10),
		Title: strings.TrimSpace(p.Title) + " (" + format + ")", URL: link.String(),
		PhotoURL: photo, PriceCents: cents, AcquiredAs: &condition, Kinds: []string{"movie"},
	}, true
}

func dvdmaxPrice(raw string) (int64, bool) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	cents, ok := toCents(value)
	return cents, ok && cents > 0
}
