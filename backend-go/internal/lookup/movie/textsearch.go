package movie

import (
	"context"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// movieFormats are the edition formats a picked film keeps.
var movieFormats = map[string]bool{"dvd": true, "bluray": true, "uhd": true}

// searchConfidence marks a candidate the user picked by title, not one a
// barcode matched.
const searchConfidence = 0.6

// TextSearch finds films on TMDB by the title a user types.
type TextSearch struct {
	TMDB TMDB
}

// Name is "tmdb".
func (TextSearch) Name() string { return "tmdb" }

// SearchText searches TMDB in Polish; a picked DVD, Blu-ray or 4K format
// stays on every candidate's edition.
func (s TextSearch) SearchText(ctx context.Context, query, format string) ([]lookup.Candidate, error) {
	films, err := s.TMDB.Search(ctx, query, nil)
	if err != nil {
		return nil, err
	}
	var edition lookup.EditionFields
	if movieFormats[format] {
		f := format
		edition.Format = &f
	}
	out := make([]lookup.Candidate, 0, len(films))
	for _, m := range films {
		out = append(out, lookup.Candidate{
			Type:       "movie",
			Title:      titleFields(m),
			Edition:    edition,
			CoverURL:   m.Poster(),
			Confidence: searchConfidence,
		})
	}
	return out, nil
}
