package books

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// searchLimit is how many volumes or works one title search asks for.
const searchLimit = 10

// searchConfidence marks a candidate the user picked by title, not one an
// ISBN matched.
const searchConfidence = 0.6

// SearchText searches Google Books volumes by the title or author a user
// types, in Google's relevance order.
func (p GoogleBooks) SearchText(ctx context.Context, query, _ string) ([]lookup.Candidate, error) {
	if p.APIKey == "" {
		return nil, ErrNoAPIKey
	}
	base := p.BaseURL
	if base == "" {
		base = GoogleBooksBaseURL
	}
	q := url.Values{"q": {query}, "key": {p.APIKey}, "printType": {"books"}, "maxResults": {strconv.Itoa(searchLimit)}}
	var out []lookup.Candidate
	_, err := getJSON(ctx, p.Client, p.Name(), base+"/books/v1/volumes?"+q.Encode(), func(raw json.RawMessage) ([]lookup.Candidate, error) {
		var resp googleResponse
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode google books response: %w", err)
		}
		for i := range resp.Items {
			if c, ok := volumeCandidate("", &resp.Items[i].VolumeInfo); ok {
				c.Confidence = searchConfidence
				out = append(out, c)
			}
		}
		return out, nil
	})
	return out, err
}

type olSearch struct {
	Docs *[]struct {
		Key              string   `json:"key"`
		Title            string   `json:"title"`
		AuthorName       []string `json:"author_name"`
		FirstPublishYear int      `json:"first_publish_year"`
		CoverI           int64    `json:"cover_i"`
	} `json:"docs"`
}

// SearchText searches Open Library works by the title or author a user
// types; a work carries no publisher, so the edition stays to fill in.
func (p OpenLibrary) SearchText(ctx context.Context, query, _ string) ([]lookup.Candidate, error) {
	base := p.BaseURL
	if base == "" {
		base = OpenLibraryBaseURL
	}
	q := url.Values{"q": {query}, "limit": {strconv.Itoa(searchLimit)}, "fields": {"key,title,author_name,first_publish_year,cover_i"}}
	var out []lookup.Candidate
	_, err := getJSON(ctx, p.Client, p.Name(), base+"/search.json?"+q.Encode(), func(raw json.RawMessage) ([]lookup.Candidate, error) {
		var resp olSearch
		if err := json.Unmarshal(raw, &resp); err != nil {
			return nil, fmt.Errorf("decode openlibrary search: %w", err)
		}
		if resp.Docs == nil {
			return nil, fmt.Errorf("openlibrary search: no docs field")
		}
		for _, d := range *resp.Docs {
			name := clean(d.Title)
			if name == "" {
				continue
			}
			title := lookup.TitleFields{Name: name}
			for _, a := range d.AuthorName {
				title.Authors = append(title.Authors, personName(a))
			}
			title.Authors = dedupe(title.Authors)
			if id := strings.TrimPrefix(d.Key, "/works/"); id != "" {
				title.OpenLibraryID = &id
			}
			if d.FirstPublishYear > 0 {
				y := d.FirstPublishYear
				title.Year = &y
			}
			c := lookup.Candidate{Type: "book", Title: title, Confidence: searchConfidence}
			if d.CoverI > 0 {
				c.CoverURL = openLibraryCovers + strconv.FormatInt(d.CoverI, 10) + "-L.jpg"
			}
			out = append(out, c)
		}
		return out, nil
	})
	return out, err
}
