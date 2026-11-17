package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// OpenLibraryBaseURL is the Open Library API.
const OpenLibraryBaseURL = "https://openlibrary.org"

const openLibraryCovers = "https://covers.openlibrary.org/b/id/"

// OpenLibrary looks an ISBN up in Open Library, which resolves ISBN-10 and
// ISBN-13 alike and carries covers for many English and Polish editions.
type OpenLibrary struct {
	BaseURL string
	Client  *http.Client
}

// Name is "openlibrary".
func (OpenLibrary) Name() string { return "openlibrary" }

// Accepts reports whether code is an ISBN.
func (OpenLibrary) Accepts(code string) bool { return barcode.IsBook(code) }

// Fetch reads the edition details for the ISBN; an unknown ISBN answers {}.
func (p OpenLibrary) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	base := p.BaseURL
	if base == "" {
		base = OpenLibraryBaseURL
	}
	return getJSON(ctx, p.Client, base+"/api/books?format=json&jscmd=details&bibkeys=ISBN:"+url.QueryEscape(code))
}

type olEntry struct {
	Details struct {
		Key     string `json:"key"`
		Title   string `json:"title"`
		Authors []struct {
			Name string `json:"name"`
		} `json:"authors"`
		Publishers     []string        `json:"publishers"`
		PublishDate    string          `json:"publish_date"`
		PhysicalFormat string          `json:"physical_format"`
		Covers         []int64         `json:"covers"`
		Description    json.RawMessage `json:"description"`
		Works          []struct {
			Key string `json:"key"`
		} `json:"works"`
	} `json:"details"`
}

// Parse maps the edition to a book candidate.
func (OpenLibrary) Parse(code string, raw json.RawMessage) ([]lookup.Candidate, error) {
	var resp map[string]olEntry
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode openlibrary response: %w", err)
	}
	entry, ok := resp["ISBN:"+code]
	if !ok {
		return nil, nil
	}
	d := entry.Details
	name := clean(d.Title)
	if name == "" {
		return nil, nil
	}
	title := lookup.TitleFields{Name: name}
	for _, a := range d.Authors {
		title.Authors = append(title.Authors, personName(a.Name))
	}
	title.Authors = dedupe(title.Authors)
	if len(d.Works) > 0 {
		if id := strings.TrimPrefix(d.Works[0].Key, "/works/"); id != "" {
			title.OpenLibraryID = &id
		}
	}
	edition := lookup.EditionFields{
		Barcode: code,
		Year:    year(d.PublishDate),
		Binding: binding(d.PhysicalFormat),
	}
	if len(d.Publishers) > 0 {
		edition.Publisher = clean(d.Publishers[0])
	}
	c := lookup.Candidate{
		Type:        "book",
		Title:       title,
		Edition:     edition,
		Description: olText(d.Description),
		Confidence:  0.8,
	}
	for _, id := range d.Covers {
		if id > 0 {
			c.CoverURL = openLibraryCovers + strconv.FormatInt(id, 10) + "-L.jpg"
			break
		}
	}
	return []lookup.Candidate{c}, nil
}

// olText reads an Open Library text value: a plain string or
// {"type": "/type/text", "value": "..."}.
func olText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var t struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &t) == nil {
		return strings.TrimSpace(t.Value)
	}
	return ""
}
