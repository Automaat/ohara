package books

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// GoogleBooksBaseURL is the Google Books API.
const GoogleBooksBaseURL = "https://www.googleapis.com"

// ErrNoAPIKey reports a Google Books provider without a key; the API gives
// keyless clients no daily quota.
var ErrNoAPIKey = errors.New("google books needs an API key")

// GoogleBooks looks an ISBN up in Google Books, mostly for descriptions and
// covers the catalogs lack.
type GoogleBooks struct {
	APIKey  string
	BaseURL string
	Client  *http.Client
}

// Name is "google_books".
func (GoogleBooks) Name() string { return "google_books" }

// Accepts reports whether code is an ISBN.
func (GoogleBooks) Accepts(code string) bool { return barcode.IsBook(code) }

// Fetch searches volumes by ISBN.
func (p GoogleBooks) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	if p.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	base := p.BaseURL
	if base == "" {
		base = GoogleBooksBaseURL
	}
	q := url.Values{"q": {"isbn:" + code}, "key": {p.APIKey}}
	return getJSON(ctx, p.Client, p.Name(), base+"/books/v1/volumes?"+q.Encode(), func(raw json.RawMessage) ([]lookup.Candidate, error) {
		return p.Parse(code, raw)
	})
}

type googleResponse struct {
	Items []struct {
		ID         string `json:"id"`
		VolumeInfo struct {
			Title               string   `json:"title"`
			Authors             []string `json:"authors"`
			Publisher           string   `json:"publisher"`
			PublishedDate       string   `json:"publishedDate"`
			Description         string   `json:"description"`
			IndustryIdentifiers []struct {
				Type       string `json:"type"`
				Identifier string `json:"identifier"`
			} `json:"industryIdentifiers"`
			ImageLinks struct {
				Thumbnail string `json:"thumbnail"`
			} `json:"imageLinks"`
		} `json:"volumeInfo"`
	} `json:"items"`
}

// Parse maps each volume that carries this ISBN to a book candidate; the
// search also returns loose matches, which are dropped.
func (GoogleBooks) Parse(code string, raw json.RawMessage) ([]lookup.Candidate, error) {
	var resp googleResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode google books response: %w", err)
	}
	isbn10, _ := barcode.ISBN10(code)
	var out []lookup.Candidate
	for i := range resp.Items {
		v := &resp.Items[i].VolumeInfo
		match := false
		for _, id := range v.IndustryIdentifiers {
			isbn := leadingISBN(id.Identifier)
			if (id.Type == "ISBN_13" && isbn == code) || (id.Type == "ISBN_10" && isbn10 != "" && isbn == isbn10) {
				match = true
			}
		}
		name := clean(v.Title)
		if !match || name == "" {
			continue
		}
		authors := make([]string, 0, len(v.Authors))
		for _, a := range v.Authors {
			authors = append(authors, personName(a))
		}
		out = append(out, lookup.Candidate{
			Type:  "book",
			Title: lookup.TitleFields{Name: name, Authors: dedupe(authors)},
			Edition: lookup.EditionFields{
				Barcode:   code,
				Publisher: clean(v.Publisher),
				Year:      year(v.PublishedDate),
			},
			CoverURL:    strings.Replace(v.ImageLinks.Thumbnail, "http://", "https://", 1),
			Description: strings.TrimSpace(v.Description),
			Confidence:  0.7,
		})
	}
	return out, nil
}
