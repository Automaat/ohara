package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

type fakeSearcher struct {
	n      int
	err    error
	query  string
	format string
}

func (*fakeSearcher) Name() string { return "fake" }

func (f *fakeSearcher) SearchText(_ context.Context, query, format string) ([]lookup.Candidate, error) {
	f.query, f.format = query, format
	if f.err != nil {
		return nil, f.err
	}
	out := make([]lookup.Candidate, 0, f.n)
	for i := range f.n {
		out = append(out, lookup.Candidate{Type: "wrong", Title: lookup.TitleFields{Name: "T" + strconv.Itoa(i)}, Confidence: 0.6})
	}
	return out, nil
}

func TestTitleSearch(t *testing.T) {
	tests := []struct {
		name     string
		query    url.Values
		searcher *fakeSearcher
		status   int
		field    string
		results  int
	}{
		{
			name: "found", query: url.Values{"kind": {"movie"}, "q": {"  Blade   Runner "}, "barcode": {"5906619080172"}, "format": {"bluray"}},
			searcher: &fakeSearcher{n: 2}, status: http.StatusOK, results: 2,
		},
		{name: "capped at ten", query: url.Values{"kind": {"movie"}, "q": {"x"}}, searcher: &fakeSearcher{n: 25}, status: http.StatusOK, results: 10},
		{name: "nothing found", query: url.Values{"kind": {"movie"}, "q": {"x"}}, searcher: &fakeSearcher{}, status: http.StatusOK},
		{name: "unknown kind", query: url.Values{"kind": {"game"}, "q": {"x"}}, status: http.StatusUnprocessableEntity, field: "kind"},
		{name: "blank query", query: url.Values{"kind": {"movie"}, "q": {"  "}}, status: http.StatusUnprocessableEntity, field: "q"},
		{name: "bad barcode", query: url.Values{"kind": {"movie"}, "q": {"x"}, "barcode": {"12"}}, status: http.StatusUnprocessableEntity, field: "barcode"},
		{name: "kind switched off", query: url.Values{"kind": {"movie"}, "q": {"x"}}, status: http.StatusServiceUnavailable},
		{name: "source down", query: url.Values{"kind": {"movie"}, "q": {"x"}}, searcher: &fakeSearcher{err: errors.New("down")}, status: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := &lookup.SearchHandler{Searchers: map[string]lookup.TextSearcher{}}
			if tt.searcher != nil {
				h.Searchers["movie"] = tt.searcher
			}
			r := chi.NewRouter()
			h.Mount(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/search?"+tt.query.Encode(), http.NoBody))
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			if tt.field != "" {
				var fe struct {
					Field string `json:"field"`
				}
				if json.Unmarshal(rec.Body.Bytes(), &fe) != nil || fe.Field != tt.field {
					t.Fatalf("body %s, want field %q", rec.Body, tt.field)
				}
			}
			if tt.status != http.StatusOK {
				return
			}
			var res lookup.SearchResult
			if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.Candidates == nil || len(res.Candidates) != tt.results {
				t.Fatalf("body %s, err %v", rec.Body, err)
			}
			if tt.searcher.query != "Blade Runner" && tt.name == "found" {
				t.Fatalf("query %q", tt.searcher.query)
			}
			if tt.searcher.format != tt.query.Get("format") {
				t.Fatalf("format %q", tt.searcher.format)
			}
			for _, c := range res.Candidates {
				if c.Type != "movie" || c.Provider != "fake" || len(c.Sources) != 1 || c.Edition.Barcode != tt.query.Get("barcode") {
					t.Fatalf("candidate = %+v", c)
				}
			}
		})
	}
}
