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

const dvdmaxSource = "dvdmax"

// DVDmax searches the first 50 products in the shop's film catalog.
type DVDmax struct {
	Client  *http.Client
	BaseURL string
}

func (DVDmax) Name() string        { return dvdmaxSource }
func (DVDmax) PerQuery() bool      { return true }
func (DVDmax) Kinds() []string     { return []string{"movie"} }
func (DVDmax) CanaryQuery() string { return "Blade Runner" }

func (DVDmax) wishQueries(w *Want) []string { return w.queries(false) }

// Search asks for films by title. The structured medium in the answer lets
// the matcher distinguish DVD, Blu-ray and UHD after fetching the products.
func (d DVDmax) Search(ctx context.Context, query string) ([]Listing, error) {
	page, err := d.SearchPage(ctx, query)
	return page.Listings, err
}

// SearchPage retains explicit unavailability even when the product cannot match.
func (d DVDmax) SearchPage(ctx context.Context, query string) (Page, error) {
	u := d.BaseURL + "?" + url.Values{
		"title":        {query},
		"page":         {"1"},
		"limit":        {"50"},
		"filters[cat]": {"1"},
	}.Encode()
	body, err := get(ctx, d.Client, d.Name(), u, "application/json")
	if err != nil {
		return Page{}, fmt.Errorf("dvdmax: %w", err)
	}
	return parseDVDmaxPage(body, query)
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
	page, err := parseDVDmaxPage(body, query)
	return page.Listings, err
}

func parseDVDmaxPage(body []byte, query string) (Page, error) {
	var answer struct {
		Products []dvdmaxProduct `json:"products"`
	}
	if err := json.Unmarshal(body, &answer); err != nil {
		return Page{}, parseError{fmt.Errorf("dvdmax: decode: %w", err)}
	}
	if answer.Products == nil {
		return Page{}, parseError{errors.New("dvdmax: response has no products")}
	}
	var page Page
	queryWords := words(query)
	for i := range answer.Products {
		product := &answer.Products[i]
		if product.ID <= 0 || strings.TrimSpace(product.Title) == "" {
			return Page{}, parseError{errors.New("dvdmax: product has no ID or title")}
		}
		if !product.available() {
			if product.Status != "" {
				page.Unavailable = append(page.Unavailable, strconv.FormatInt(product.ID, 10))
			}
			continue
		}
		if !dvdmaxTitleMatches(product.Title, queryWords) {
			continue
		}
		if listing, ok := product.listing(); ok {
			page.Listings = append(page.Listings, listing)
		}
	}
	page.Listings = slices.DeleteFunc(page.Listings, func(l Listing) bool {
		return slices.Contains(page.Unavailable, l.ExternalID)
	})
	for i := range page.Listings {
		page.Seen = append(page.Seen, page.Listings[i].ExternalID)
	}
	return page, nil
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

func (p *dvdmaxProduct) available() bool {
	switch p.Status {
	case "DOSTEPNY24", "DOSTEPNY48H", "DOSTEPNY72H", "DOSTEPNY120", "DOSTEPNY48", "DOSTEPNY72", "DOSTEPNY336", "PROMOCJA", "ZAPOWIEDZ":
		return true
	default:
		return false
	}
}

func (p *dvdmaxProduct) listing() (Listing, bool) {
	format, ok := map[string]string{"DVD": "dvd", "BLU-RAY": "bluray", "BLU-RAY 4K": "uhd"}[p.Format]
	// The site separates the titles in multi-film packages with a spaced slash.
	if !ok || strings.Contains(p.Title, " / ") {
		return Listing{}, false
	}
	title := strings.TrimSpace(p.Title) + " (" + formatLabels[format] + ")"
	// A Blu-ray can advertise a 4K restoration in its title. The generic
	// matcher reads that as a UHD disc, so leave out conflicting products.
	if listingFormat(words(title)) != format {
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
		Title: title, URL: link.String(),
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
