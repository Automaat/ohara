package movie

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
)

// TMDB searches The Movie Database. Key is either a v3 API key, sent as the
// api_key parameter, or a v4 read access token (a JWT), sent as a bearer
// token.
type TMDB struct {
	Client  *http.Client
	BaseURL string
	Key     string
}

// TMDBMovie is one search result. Title is the pl-PL title, which TMDB falls
// back to the original title for when no translation exists.
type TMDBMovie struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	ReleaseDate   string  `json:"release_date"`
	Popularity    float64 `json:"popularity"`
	PosterPath    string  `json:"poster_path"`
}

// tmdbPosterBase serves TMDB posters 342 pixels wide.
const tmdbPosterBase = "https://image.tmdb.org/t/p/w342"

// Poster is the poster URL, or empty when TMDB has none.
func (m TMDBMovie) Poster() string {
	if m.PosterPath == "" {
		return ""
	}
	return tmdbPosterBase + m.PosterPath
}

// tmdbMaxResults keeps the candidates a user picks from short.
const tmdbMaxResults = 5

// Year is the release year, or nil when TMDB has no release date.
func (m TMDBMovie) Year() *int {
	return dateYear(m.ReleaseDate)
}

// dateYear is the year of a TMDB YYYY-MM-DD date, or nil when it has none.
func dateYear(date string) *int {
	if len(date) < 4 {
		return nil
	}
	y, err := strconv.Atoi(date[:4])
	if err != nil {
		return nil
	}
	return &y
}

// Search runs /3/search/movie in Polish. A non-nil year narrows the search to
// films released that year.
func (t TMDB) Search(ctx context.Context, query string, year *int) ([]TMDBMovie, error) {
	q := url.Values{"query": {query}, "language": {"pl-PL"}, "include_adult": {"false"}}
	if year != nil {
		q.Set("year", strconv.Itoa(*year))
	}
	resp, err := t.get(ctx, "/3/search/movie", q)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results *[]TMDBMovie `json:"results"`
	}
	if err := json.Unmarshal(resp.Body, &r); err != nil {
		return nil, parseFailed("tmdb", err)
	}
	if r.Results == nil {
		return nil, parseFailed("tmdb", fmt.Errorf("no results field"))
	}
	res := *r.Results
	if len(res) > tmdbMaxResults {
		res = res[:tmdbMaxResults]
	}
	found("tmdb", len(res))
	return res, nil
}

// get sends one TMDB API request for path with query q, authorized by the
// key.
func (t TMDB) get(ctx context.Context, path string, q url.Values) (fetch.Response, error) {
	base := t.BaseURL
	if base == "" {
		base = "https://api.themoviedb.org"
	}
	bearer := strings.HasPrefix(t.Key, "eyJ")
	if !bearer {
		q.Set("api_key", t.Key)
	}
	header := http.Header{"Accept": {"application/json"}}
	if bearer {
		header.Set("Authorization", "Bearer "+t.Key)
	}
	return get(ctx, t.Client, "tmdb", fetch.Request{URL: base + path + "?" + q.Encode(), Header: header})
}
