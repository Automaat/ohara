package movie

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
}

// tmdbMaxResults keeps the candidates a user picks from short.
const tmdbMaxResults = 5

// Year is the release year, or nil when TMDB has no release date.
func (m TMDBMovie) Year() *int {
	if len(m.ReleaseDate) < 4 {
		return nil
	}
	y, err := strconv.Atoi(m.ReleaseDate[:4])
	if err != nil {
		return nil
	}
	return &y
}

// Search runs /3/search/movie in Polish. A non-nil year narrows the search to
// films released that year.
func (t TMDB) Search(ctx context.Context, query string, year *int) ([]TMDBMovie, error) {
	base := t.BaseURL
	if base == "" {
		base = "https://api.themoviedb.org"
	}
	q := url.Values{"query": {query}, "language": {"pl-PL"}, "include_adult": {"false"}}
	if year != nil {
		q.Set("year", strconv.Itoa(*year))
	}
	bearer := strings.HasPrefix(t.Key, "eyJ")
	if !bearer {
		q.Set("api_key", t.Key)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/3/search/movie?"+q.Encode(), http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("tmdb: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if bearer {
		req.Header.Set("Authorization", "Bearer "+t.Key)
	}
	client := t.Client
	if client == nil {
		client = http.DefaultClient
	}
	_, body, err := get(client, "tmdb", req, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var r struct {
		Results *[]TMDBMovie `json:"results"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
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
