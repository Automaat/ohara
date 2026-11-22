package movie

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// tmdbTV is a TMDB double answering /3/search/tv with two shows and
// /3/tv/{id} with their seasons, recording the paths asked.
func tmdbTV(t *testing.T, asked *[]string) *TMDB {
	t.Helper()
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("language") != "pl-PL" || q.Get("api_key") != "k" {
			t.Errorf("query %s", r.URL.RawQuery)
		}
		*asked = append(*asked, r.URL.Path)
		switch r.URL.Path {
		case "/3/search/tv":
			if q.Get("query") != "Dark" {
				t.Errorf("search query %q", q.Get("query"))
			}
			_, _ = w.Write([]byte(`{"results":[
				{"id":70523,"name":"Dark","original_name":"Dark","first_air_date":"2017-12-01","poster_path":"/dark.jpg"},
				{"id":1405,"name":"Dexter","original_name":"Dexter","first_air_date":"2006-10-01"}]}`))
		case "/3/tv/70523":
			_, _ = w.Write([]byte(`{"id":70523,"seasons":[
				{"season_number":0,"name":"Odcinki specjalne","air_date":"2019-06-20","episode_count":3},
				{"season_number":1,"name":"Sezon 1","air_date":"2017-12-01","episode_count":10,"poster_path":"/s1.jpg"},
				{"season_number":2,"name":"Sezon 2","air_date":"2019-06-21","episode_count":8}]}`))
		case "/3/tv/1405":
			_, _ = w.Write([]byte(`{"id":1405,"seasons":[{"season_number":1,"name":"Sezon 1","episode_count":12}]}`))
		default:
			http.NotFound(w, r)
		}
	})
	return &TMDB{Client: s.Client(), BaseURL: s.URL, Key: "k"}
}

func TestTVSearchListsEachSeasonOfTheShowsFound(t *testing.T) {
	var asked []string
	got, err := TVSearch{TMDB: *tmdbTV(t, &asked)}.SearchText(t.Context(), "Dark", "bluray")
	if err != nil {
		t.Fatal(err)
	}
	type season struct {
		show   int64
		season int
		year   string
		cover  string
	}
	var seasons []season
	for _, c := range got {
		if c.Type != "tv" || c.Title.TMDBID == nil || c.Title.Season == nil || c.Edition.Format == nil ||
			*c.Edition.Format != "bluray" || c.Confidence >= lookup.ConfirmThreshold {
			t.Fatalf("candidate = %+v", c)
		}
		year := ""
		if c.Title.Year != nil {
			year = strconv.Itoa(*c.Title.Year)
		}
		seasons = append(seasons, season{*c.Title.TMDBID, *c.Title.Season, year, c.CoverURL})
	}
	want := []season{
		{70523, 1, "2017", "https://image.tmdb.org/t/p/w342/s1.jpg"},
		{70523, 2, "2019", "https://image.tmdb.org/t/p/w342/dark.jpg"},
		{70523, 0, "2019", "https://image.tmdb.org/t/p/w342/dark.jpg"},
		{1405, 1, "2006", ""},
	}
	if fmt.Sprint(seasons) != fmt.Sprint(want) {
		t.Fatalf("seasons = %v, want %v", seasons, want)
	}
	if first := got[0].Title; first.Name != "Dark" || *first.OriginalTitle != "Dark" || *first.PolishTitle != "Dark" {
		t.Fatalf("title = %+v", first)
	}
	if strings.Join(asked, " ") != "/3/search/tv /3/tv/70523 /3/tv/1405" {
		t.Fatalf("asked %v", asked)
	}
}

func TestTVSearchFailsWhenASeasonListFails(t *testing.T) {
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/3/search/tv" {
			_, _ = w.Write([]byte(`{"results":[{"id":7,"name":"X"}]}`))
			return
		}
		http.Error(w, "down", http.StatusInternalServerError)
	})
	_, err := TVSearch{TMDB: TMDB{Client: s.Client(), BaseURL: s.URL, Key: "k"}}.SearchText(t.Context(), "X", "")
	if err == nil {
		t.Fatal("no error for a failed season list")
	}
}

func TestSearchEndpointAnswersTVSeasons(t *testing.T) {
	var asked []string
	h := &lookup.SearchHandler{Searchers: map[string]lookup.TextSearcher{"tv": TVSearch{TMDB: *tmdbTV(t, &asked)}}}
	r := chi.NewRouter()
	h.Mount(r)
	rec := httptest.NewRecorder()
	q := url.Values{"kind": {"tv"}, "q": {"Dark"}, "barcode": {"5906619080172"}}
	r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?"+q.Encode(), http.NoBody))
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var res lookup.SearchResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || len(res.Candidates) != 4 {
		t.Fatalf("body %s, err %v", rec.Body, err)
	}
	c := res.Candidates[1]
	if c.Type != "tv" || c.Title.Name != "Dark" || *c.Title.Season != 2 || *c.Title.TMDBID != 70523 ||
		*c.Title.Year != 2019 || c.Edition.Barcode != "5906619080172" || c.Provider != "tmdb" {
		t.Fatalf("candidate = %+v", c)
	}
}
