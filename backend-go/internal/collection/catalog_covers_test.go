package collection_test

import (
	"context"
	"errors"
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

type coverCatalog struct {
	results []lookup.Candidate
	err     error
}

func (coverCatalog) Name() string { return "fixture" }

func (c coverCatalog) SearchText(context.Context, string, string) ([]lookup.Candidate, error) {
	return c.results, c.err
}

func newCoverAPI(t *testing.T, catalog coverCatalog) (*api, *lookup.Service) {
	t.Helper()
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	service := lookup.NewService(&lookup.Registry{}, lookup.NewPGCache(pool), logger)
	searchers := map[string]lookup.TextSearcher{collection.KindBook: catalog}
	handler := collection.NewHandler(collection.NewStore(pool), logger).WithCatalogCovers(searchers, service)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{handler}}))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL}, service
}

func TestCreateTitleFillsOnlyUnambiguousCatalogCover(t *testing.T) {
	const catalogURL = "https://example.org/cover.jpg"
	const ownURL = "https://example.org/my-cover.jpg"
	title := lookup.TitleFields{Name: "Lalka", Authors: []string{"Bolesław Prus"}}
	match := lookup.Candidate{Type: collection.KindBook, Title: title, CoverURL: catalogURL}
	alternative := match
	alternative.CoverURL = "https://example.org/other-edition.jpg"
	differentAuthor := match
	differentAuthor.Title.Authors = []string{"Inny autor"}
	badURL := match
	badURL.CoverURL = "javascript:invalid"
	tests := []struct {
		name     string
		catalog  coverCatalog
		provided *string
		want     string
	}{
		{name: "exact title and author", catalog: coverCatalog{results: []lookup.Candidate{match, differentAuthor}}, want: catalogURL},
		{name: "different covers for the same title", catalog: coverCatalog{results: []lookup.Candidate{match, alternative}}},
		{name: "wrong author", catalog: coverCatalog{results: []lookup.Candidate{differentAuthor}}},
		{name: "invalid image URL", catalog: coverCatalog{results: []lookup.Candidate{badURL}}},
		{name: "catalog failure", catalog: coverCatalog{err: errors.New("fixture unavailable")}},
		{name: "provided cover takes precedence", catalog: coverCatalog{results: []lookup.Candidate{match}}, provided: new(ownURL), want: ownURL},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := newCoverAPI(t, tt.catalog)
			in := collection.TitleInput{Kind: collection.KindBook, Name: "Lalka", Authors: []string{"Bolesław Prus"}, CoverURL: tt.provided}

			var created collection.Title
			a.mustDo(http.MethodPost, "/api/titles", in, &created, http.StatusCreated)

			got := ""
			if created.CoverURL != nil {
				got = *created.CoverURL
			}
			if got != tt.want {
				t.Errorf("cover = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScanFillsEditionCoverWithoutReplacingAnExistingCover(t *testing.T) {
	const code = "9788300006014"
	const catalogURL = "https://example.org/known-isbn.jpg"
	const ownURL = "https://example.org/owned-cover.jpg"
	a, service := newCoverAPI(t, coverCatalog{})
	title := collection.TitleInput{Kind: collection.KindBook, Name: "Lalka"}
	var created collection.Title
	a.mustDo(http.MethodPost, "/api/titles", title, &created, http.StatusCreated)
	in := collection.EditionInput{TitleID: created.ID, Barcode: new(code)}
	var edition collection.Edition
	a.mustDo(http.MethodPost, "/api/editions", in, &edition, http.StatusCreated)
	candidate := lookup.Candidate{
		Type: collection.KindBook, Title: lookup.TitleFields{Name: "Lalka"}, CoverURL: catalogURL,
	}
	if _, err := service.Select(t.Context(), code, candidate); err != nil {
		t.Fatalf("seed barcode match: %v", err)
	}

	path := fmt.Sprintf("/api/editions/%d", edition.ID)
	copyInput := collection.CopyInput{EditionID: edition.ID, Status: collection.StatusOwned}
	a.mustDo(http.MethodPost, "/api/copies?cover=auto", copyInput, nil, http.StatusCreated)
	a.mustDo(http.MethodGet, path, nil, &edition, http.StatusOK)
	if edition.CoverURL == nil || *edition.CoverURL != catalogURL {
		t.Fatalf("scanned edition cover = %v, want %s", edition.CoverURL, catalogURL)
	}

	in.CoverURL = new(ownURL)
	a.mustDo(http.MethodPut, path, in, nil, http.StatusOK)
	a.mustDo(http.MethodPost, "/api/copies?cover=auto", copyInput, nil, http.StatusCreated)
	a.mustDo(http.MethodGet, path, nil, &edition, http.StatusOK)
	if edition.CoverURL == nil || *edition.CoverURL != ownURL {
		t.Fatalf("existing edition cover = %v, want %s", edition.CoverURL, ownURL)
	}
}
