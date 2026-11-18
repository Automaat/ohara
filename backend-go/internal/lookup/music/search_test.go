package music_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/lookup/music"
)

func TestAlbumSearch(t *testing.T) {
	tests := []struct {
		name       string
		query      url.Values
		noDiscogs  bool
		status     int
		field      string
		candidates int
		hit        string
	}{
		{
			name: "found by name", query: url.Values{"q": {"  Taco Hemingway   1-800-Oświecenie "}, "barcode": {"0602458447651"}},
			status: http.StatusOK, candidates: 2, hit: "discogs q=Taco Hemingway 1-800-Oświecenie format=",
		},
		{
			name: "format filter", query: url.Values{"q": {"Taco Hemingway 1-800-Oświecenie"}, "format": {"cd"}},
			status: http.StatusOK, candidates: 2, hit: "discogs q=Taco Hemingway 1-800-Oświecenie format=CD",
		},
		{name: "nothing found", query: url.Values{"q": {"no such album"}}, status: http.StatusOK},
		{name: "blank query", query: url.Values{"q": {"   "}}, status: http.StatusUnprocessableEntity, field: "q"},
		{name: "unknown format", query: url.Values{"q": {"x"}, "format": {"dvd"}}, status: http.StatusUnprocessableEntity, field: "format"},
		{name: "bad barcode", query: url.Values{"q": {"x"}, "barcode": {"123"}}, status: http.StatusUnprocessableEntity, field: "barcode"},
		{name: "no token", query: url.Values{"q": {"x"}}, noDiscogs: true, status: http.StatusServiceUnavailable},
		{name: "discogs down", query: url.Values{"q": {"down"}}, status: http.StatusBadGateway},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := newUpstream(t)
			h := &music.SearchHandler{}
			if !tt.noDiscogs {
				h.Discogs = &discogs.Client{HTTP: u.server.Client(), BaseURL: u.server.URL, Token: "secret"}
			}
			r := chi.NewRouter()
			h.Mount(r)
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/music/search?"+tt.query.Encode(), http.NoBody))
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
			var res music.SearchResult
			if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil || res.Candidates == nil || len(res.Candidates) != tt.candidates {
				t.Fatalf("body %s, err %v", rec.Body, err)
			}
			if tt.hit != "" && u.count(tt.hit) != 1 {
				t.Fatalf("discogs hits = %v", u.hits)
			}
			for _, c := range res.Candidates {
				if c.Provider != "discogs" || c.Type != "music" || c.Edition.DiscogsReleaseID == nil ||
					c.Edition.Barcode != tt.query.Get("barcode") {
					t.Fatalf("candidate = %+v", c)
				}
			}
		})
	}
}
