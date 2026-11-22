package movie

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strconv"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// TMDBShow is one /3/search/tv result. Name is the pl-PL name, which TMDB
// falls back to the original name for when no translation exists.
type TMDBShow struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	OriginalName string `json:"original_name"`
	FirstAirDate string `json:"first_air_date"`
	PosterPath   string `json:"poster_path"`
}

// TMDBSeason is one season in a show's /3/tv/{id} details; season 0 holds
// the specials.
type TMDBSeason struct {
	SeasonNumber int    `json:"season_number"`
	Name         string `json:"name"`
	AirDate      string `json:"air_date"`
	EpisodeCount int    `json:"episode_count"`
	PosterPath   string `json:"poster_path"`
}

// tvMaxShows is how many shows a TV search lists the seasons of: each
// costs one more request.
const tvMaxShows = 3

// maxSeason is the highest season number a title takes (collection.MaxSeason).
const maxSeason = 9999

// SearchTV runs /3/search/tv in Polish and returns the first tvMaxShows
// shows.
func (t TMDB) SearchTV(ctx context.Context, query string) ([]TMDBShow, error) {
	q := url.Values{"query": {query}, "language": {"pl-PL"}, "include_adult": {"false"}}
	resp, err := t.get(ctx, "/3/search/tv", q)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results *[]TMDBShow `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return nil, parseFailed("tmdb", err)
	}
	if r.Results == nil {
		return nil, parseFailed("tmdb", fmt.Errorf("no results field"))
	}
	shows := *r.Results
	shows = shows[:min(len(shows), tvMaxShows)]
	found("tmdb", len(shows))
	return shows, nil
}

// Seasons reads the seasons of show id from /3/tv/{id} in Polish.
func (t TMDB) Seasons(ctx context.Context, id int64) ([]TMDBSeason, error) {
	resp, err := t.get(ctx, "/3/tv/"+strconv.FormatInt(id, 10), url.Values{"language": {"pl-PL"}})
	if err != nil {
		return nil, err
	}
	var r struct {
		Seasons *[]TMDBSeason `json:"seasons"`
	}
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return nil, parseFailed("tmdb", err)
	}
	if r.Seasons == nil {
		return nil, parseFailed("tmdb", fmt.Errorf("no seasons field"))
	}
	found("tmdb", len(*r.Seasons))
	return *r.Seasons, nil
}

// TVSearch finds seasons of TV series on TMDB by the show name a user types.
type TVSearch struct {
	TMDB TMDB
}

// Name is "tmdb".
func (TVSearch) Name() string { return "tmdb" }

// SearchText searches TMDB shows in Polish and answers one candidate per
// season of the first shows found, in season order with the specials last;
// a picked DVD, Blu-ray or 4K format stays on every candidate's edition.
func (s TVSearch) SearchText(ctx context.Context, query, format string) ([]lookup.Candidate, error) {
	shows, err := s.TMDB.SearchTV(ctx, query)
	if err != nil {
		return nil, err
	}
	var edition lookup.EditionFields
	if movieFormats[format] {
		f := format
		edition.Format = &f
	}
	out := []lookup.Candidate{}
	for _, show := range shows {
		seasons, err := s.TMDB.Seasons(ctx, show.ID)
		if err != nil {
			return nil, err
		}
		seasons = slices.DeleteFunc(slices.Clone(seasons), func(se TMDBSeason) bool {
			return se.SeasonNumber < 0 || se.SeasonNumber > maxSeason
		})
		slices.SortStableFunc(seasons, func(a, b TMDBSeason) int {
			return cmp.Or(cmp.Compare(specials(a), specials(b)), cmp.Compare(a.SeasonNumber, b.SeasonNumber))
		})
		for _, se := range seasons {
			out = append(out, seasonCandidate(show, se, edition))
		}
	}
	return out, nil
}

// specials sorts the specials (season 0) after the numbered seasons.
func specials(s TMDBSeason) int {
	if s.SeasonNumber == 0 {
		return 1
	}
	return 0
}

// seasonCandidate is season se of show as a title search candidate.
func seasonCandidate(show TMDBShow, se TMDBSeason, edition lookup.EditionFields) lookup.Candidate {
	name := show.Name
	if name == "" {
		name = show.OriginalName
	}
	id, number := show.ID, se.SeasonNumber
	title := lookup.TitleFields{Name: name, TMDBID: &id, Season: &number, Year: dateYear(se.AirDate)}
	if title.Year == nil {
		title.Year = dateYear(show.FirstAirDate)
	}
	if show.OriginalName != "" {
		orig := show.OriginalName
		title.OriginalTitle = &orig
	}
	if show.Name != "" {
		pl := show.Name
		title.PolishTitle = &pl
	}
	poster := se.PosterPath
	if poster == "" {
		poster = show.PosterPath
	}
	c := lookup.Candidate{Type: "tv", Title: title, Edition: edition, Confidence: searchConfidence}
	if poster != "" {
		c.CoverURL = tmdbPosterBase + poster
	}
	return c
}
