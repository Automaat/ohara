package collection_test

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

func TestTVTitleNeedsItsSeason(t *testing.T) {
	a := newAPI(t)
	for _, tc := range []struct {
		name string
		body obj
	}{
		{"tv without a season", obj{"kind": "tv", "name": "Dark"}},
		{"season on a film", obj{"kind": "movie", "name": "Miś", "season": 1}},
		{"season on a book", obj{"kind": "book", "name": "Lalka", "season": 0}},
		{"negative season", obj{"kind": "tv", "name": "Dark", "season": -1}},
		{"season past the cap", obj{"kind": "tv", "name": "Dark", "season": 10000}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a.fails(http.MethodPost, "/api/titles", tc.body, http.StatusUnprocessableEntity, "season")
		})
	}
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "season": 1.5}, http.StatusUnprocessableEntity, "body")
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "season": 1, "authors": []string{"Baran bo Odar"}},
		http.StatusUnprocessableEntity, "authors")

	var specials collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "season": 0}, &specials, http.StatusCreated)
	if specials.Kind != "tv" || specials.Season == nil || *specials.Season != 0 {
		t.Fatalf("specials = %+v", specials)
	}

	movie := a.title("movie", "Miś")
	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d", movie), obj{"kind": "tv", "name": "Miś"}, http.StatusUnprocessableEntity, "season")
	var show collection.Title
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", movie), obj{"kind": "tv", "name": "Miś", "season": 3}, &show, http.StatusOK)
	if show.Kind != "tv" || show.Season == nil || *show.Season != 3 {
		t.Fatalf("film turned show = %+v", show)
	}
	var film collection.Title
	a.mustDo(http.MethodPut, fmt.Sprintf("/api/titles/%d", movie), obj{"kind": "movie", "name": "Miś"}, &film, http.StatusOK)
	if film.Season != nil {
		t.Fatalf("show turned film keeps its season: %+v", film)
	}
}

func TestTVSeasonsShareTheShowTMDBID(t *testing.T) {
	a := newAPI(t)
	var first, second collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "tmdb_id": 70523, "season": 1}, &first, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "tmdb_id": 70523, "season": 2}, &second, http.StatusCreated)
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "tmdb_id": 70523, "season": 2},
		http.StatusConflict, "tmdb_id")
	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d", second.ID),
		obj{"kind": "tv", "name": "Dark", "tmdb_id": 70523, "season": 1}, http.StatusConflict, "tmdb_id")

	var listed []collection.Title
	a.mustDo(http.MethodGet, "/api/titles?kind=tv&tmdb_id=70523", nil, &listed, http.StatusOK)
	if len(listed) != 2 || *listed[0].Season != 1 || *listed[1].Season != 2 {
		t.Fatalf("seasons listed = %+v", listed)
	}
}

func TestFilmMayCarryAShowTMDBNumberButNotAnotherFilms(t *testing.T) {
	a := newAPI(t)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "tmdb_id": 70523, "season": 1}, nil, http.StatusCreated)
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Inny", "tmdb_id": 70523}, nil, http.StatusCreated)
	a.fails(http.MethodPost, "/api/titles", obj{"kind": "movie", "name": "Inny 2", "tmdb_id": 70523}, http.StatusConflict, "tmdb_id")
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "tmdb_id": 70523, "season": 2}, nil, http.StatusCreated)
}

func TestTVEditionsFollowTheFilmRules(t *testing.T) {
	a := newAPI(t)
	var show collection.Title
	a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "season": 2}, &show, http.StatusCreated)

	a.fails(http.MethodPost, "/api/editions", obj{"title_id": show.ID}, http.StatusUnprocessableEntity, "format")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": show.ID, "format": "cd"}, http.StatusUnprocessableEntity, "format")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": show.ID, "format": "dvd", "binding": "paperback"},
		http.StatusUnprocessableEntity, "binding")
	a.fails(http.MethodPost, "/api/editions", obj{"title_id": show.ID, "format": "dvd", "discogs_release_id": 1},
		http.StatusUnprocessableEntity, "discogs_release_id")

	var e collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", obj{
		"title_id": show.ID, "format": "bluray", "pl_subtitles": "yes", "pl_voiceover": "no",
		"packaging": []string{"steelbook", "slipcover"},
	}, &e, http.StatusCreated)
	if *e.Format != "bluray" || e.PLSubtitles != "yes" || e.PLVoiceover != "no" || len(e.Packaging) != 2 {
		t.Fatalf("TV edition = %+v", e)
	}
	a.fails(http.MethodPut, fmt.Sprintf("/api/titles/%d", show.ID), obj{"kind": "movie", "name": "Dark"}, http.StatusConflict, "kind")

	a.copyOf(obj{"edition_id": e.ID, "status": "owned"})
	var view collection.CollectionView
	a.mustDo(http.MethodGet, "/api/collection?kind=tv&format=bluray", nil, &view, http.StatusOK)
	if len(view.Items) != 1 || view.Items[0].Kind != "tv" || view.Items[0].Season == nil || *view.Items[0].Season != 2 {
		t.Fatalf("TV view = %+v", view.Items)
	}
}

func TestTVTitleTakesOnlyItsOwnSeasonsCover(t *testing.T) {
	const firstCover = "https://example.org/s1.jpg"
	const secondCover = "https://example.org/s2.jpg"
	season := func(n int, cover string) lookup.Candidate {
		show := int64(70523)
		return lookup.Candidate{
			Type: collection.KindTV, CoverURL: cover,
			Title: lookup.TitleFields{Name: "Dark", TMDBID: &show, Season: &n},
		}
	}
	for _, tt := range []struct {
		name   string
		found  []lookup.Candidate
		tmdbID *int64
		want   string
	}{
		{name: "by name, only another season found", found: []lookup.Candidate{season(1, firstCover)}},
		{name: "by TMDB ID, only another season found", found: []lookup.Candidate{season(1, firstCover)}, tmdbID: new(int64(70523))},
		{name: "by name, its season among others", found: []lookup.Candidate{season(1, firstCover), season(2, secondCover)}, want: secondCover},
		{
			name: "by TMDB ID, its season among others", found: []lookup.Candidate{season(1, firstCover), season(2, secondCover)},
			tmdbID: new(int64(70523)), want: secondCover,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			logger := slog.New(slog.DiscardHandler)
			service := lookup.NewService(&lookup.Registry{}, lookup.NewPGCache(pool), logger)
			searchers := map[string]lookup.TextSearcher{collection.KindTV: coverCatalog{results: tt.found}}
			handler := collection.NewHandler(collection.NewStore(pool), logger).WithCatalogCovers(searchers, service)
			srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{handler}}))
			t.Cleanup(srv.Close)
			a := &api{t: t, url: srv.URL}

			var created collection.Title
			a.mustDo(http.MethodPost, "/api/titles", obj{"kind": "tv", "name": "Dark", "season": 2, "tmdb_id": tt.tmdbID}, &created, http.StatusCreated)
			got := ""
			if created.CoverURL != nil {
				got = *created.CoverURL
			}
			if got != tt.want {
				t.Fatalf("season 2 cover = %q, want %q", got, tt.want)
			}
		})
	}
}
