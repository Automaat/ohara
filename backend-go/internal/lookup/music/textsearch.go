package music

import (
	"context"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// searchFormats maps an edition format to the Discogs format filter.
var searchFormats = map[string]string{"cd": "CD", "vinyl": "Vinyl", "cassette": "Cassette"}

// TextSearch finds albums on Discogs by the artist and title a user types.
type TextSearch struct {
	Discogs DiscogsSearcher
}

// Name is "discogs".
func (TextSearch) Name() string { return "discogs" }

// SearchText searches Discogs releases, narrowed to the picked medium.
func (s TextSearch) SearchText(ctx context.Context, query, format string) ([]lookup.Candidate, error) {
	releases, err := s.Discogs.SearchText(ctx, query, searchFormats[format])
	if err != nil {
		return nil, err
	}
	out := make([]lookup.Candidate, 0, len(releases))
	for i := range releases {
		out = append(out, discogsCandidate("", &releases[i]))
	}
	return out, nil
}
