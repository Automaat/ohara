package movie_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// Fixtures. Ceneo and UPCitemdb pages in testdata/ are recorded responses.
// Allegro catalog answers follow the documented /sale/products schema with
// the product names Polish shops list for these EANs. TMDB answers carry the
// IDs, pl-PL titles and release dates shown on themoviedb.org, trimmed to
// the fields the provider reads.
var (
	allegroNames = map[string][]string{
		"5906190326195": {"Kursk [Blu-ray]"},
		"5900058301362": {"Wkręceni 2 (Blu-ray) Lektor PL"},
		"5906190325662": {"Listy do M. 4 DVD"},
		"5906190327048": {"25 lat niewinności. Sprawa Tomka Komendy (Blu-ray)"},
	}
	tmdbResults = map[string][]movie.TMDBMovie{
		"Kursk": {
			{ID: 401200, Title: "Kursk", OriginalTitle: "Kursk", ReleaseDate: "2018-12-14"},
			{ID: 1422382, Title: "Kursk", OriginalTitle: "Kursk", ReleaseDate: "2012-07-31"},
			{ID: 705914, Title: "The Raising of the Kursk", OriginalTitle: "The Raising of the Kursk", ReleaseDate: "2014-03-24"},
		},
		"Wkręceni 2": {
			{ID: 340389, Title: "Wkręceni 2", OriginalTitle: "Wkręceni 2", ReleaseDate: "2015-01-16"},
		},
		"Listy do M. 4": {
			{ID: 792219, Title: "Listy do M. 4", OriginalTitle: "Listy do M. 4", ReleaseDate: "2021-02-01"},
			{ID: 79221, Title: "Listy do M.", OriginalTitle: "Listy do M.", ReleaseDate: "2011-11-11"},
			{ID: 370291, Title: "Listy do M. 2", OriginalTitle: "Listy do M. 2", ReleaseDate: "2015-11-13"},
			{ID: 480973, Title: "Listy do M. 3", OriginalTitle: "Listy do M. 3", ReleaseDate: "2017-11-10"},
		},
		"25 lat niewinności": {
			{ID: 741652, Title: "25 lat niewinności", OriginalTitle: "25 lat niewinności", ReleaseDate: "2020-09-18"},
		},
	}
)

// refusedEAN is a barcode the fake Allegro refuses while UPCitemdb knows
// the product.
const refusedEAN = "5900058301379"

type upstream struct {
	t      *testing.T
	mu     sync.Mutex
	hits   map[string]int
	server *httptest.Server
}

func newUpstream(t *testing.T) *upstream {
	t.Helper()
	u := &upstream{t: t, hits: map[string]int{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sale/products", func(w http.ResponseWriter, r *http.Request) {
		u.hit("allegro")
		if r.URL.Query().Get("phrase") == refusedEAN {
			http.Error(w, `{"errors":[{"code":"AccessDenied"}]}`, http.StatusForbidden)
			return
		}
		if r.Header.Get("Authorization") != "Bearer access-1" || r.URL.Query().Get("mode") != "GTIN" {
			http.Error(w, `{"errors":[{"code":"Unauthorized"}]}`, http.StatusUnauthorized)
			return
		}
		type product struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		out := struct {
			Products []product `json:"products"`
		}{Products: []product{}}
		for _, n := range allegroNames[r.URL.Query().Get("phrase")] {
			out.Products = append(out.Products, product{ID: "p-" + n, Name: n})
		}
		writeJSON(w, out)
	})
	mux.HandleFunc("GET /{search}", func(w http.ResponseWriter, r *http.Request) {
		u.hit("ceneo")
		if !strings.HasPrefix(r.PathValue("search"), ";szukaj-") {
			http.NotFound(w, r)
			return
		}
		serveFile(t, w, "testdata/ceneo-no-match.html")
	})
	mux.HandleFunc("GET /prod/trial/lookup", func(w http.ResponseWriter, r *http.Request) {
		u.hit("upcitemdb")
		if r.URL.Query().Get("upc") == refusedEAN {
			writeJSON(w, map[string]any{"code": "OK", "items": []map[string]string{{"title": "Wkręceni 2 [Blu-ray]"}}})
			return
		}
		if r.URL.Query().Get("upc") == "5902600069706" {
			serveFile(t, w, "testdata/upcitemdb-5902600069706.json")
			return
		}
		serveFile(t, w, "testdata/upcitemdb-empty.json")
	})
	mux.HandleFunc("GET /3/search/movie", func(w http.ResponseWriter, r *http.Request) {
		u.hit("tmdb")
		q := r.URL.Query()
		if q.Get("api_key") != "tmdb-key" || q.Get("language") != "pl-PL" {
			http.Error(w, `{"status_code":7}`, http.StatusUnauthorized)
			return
		}
		res := tmdbResults[q.Get("query")]
		if res == nil {
			res = []movie.TMDBMovie{}
		}
		writeJSON(w, map[string]any{"page": 1, "results": res, "total_results": len(res)})
	})
	u.server = httptest.NewServer(mux)
	t.Cleanup(u.server.Close)
	return u
}

func (u *upstream) hit(name string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.hits[name]++
}

func (u *upstream) count(name string) int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits[name]
}

type staticStore struct{ token movie.Token }

func (s *staticStore) Load(context.Context, string) (movie.Token, bool, error) {
	return s.token, true, nil
}

func (s *staticStore) Save(_ context.Context, _ string, t movie.Token) error {
	s.token = t
	return nil
}

func (u *upstream) provider() *movie.Provider {
	c := u.server.Client()
	return &movie.Provider{
		Sources: []movie.NameSource{
			&movie.Allegro{
				ClientID: "id", ClientSecret: "secret", Client: c, APIBase: u.server.URL, AuthBase: u.server.URL,
				Store: &staticStore{token: movie.Token{Access: "access-1", Refresh: "r", Expiry: time.Now().Add(time.Hour)}},
			},
			movie.Ceneo{Client: c, BaseURL: u.server.URL},
			movie.UPCitemdb{Client: c, BaseURL: u.server.URL},
		},
		TMDB: movie.TMDB{Client: c, BaseURL: u.server.URL, Key: "tmdb-key"},
	}
}

type memCache struct {
	mu      sync.Mutex
	entries map[string]json.RawMessage
}

func (c *memCache) Get(_ context.Context, provider, code string, _ time.Duration) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, ok := c.entries[provider+"/"+code]
	return raw, ok, nil
}

func (c *memCache) Put(_ context.Context, provider, code string, raw json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]json.RawMessage{}
	}
	c.entries[provider+"/"+code] = raw
	return nil
}

func TestPolishEANsResolveToTMDB(t *testing.T) {
	u := newUpstream(t)
	registry := &lookup.Registry{}
	if err := registry.Register(u.provider(), time.Hour); err != nil {
		t.Fatal(err)
	}
	svc := lookup.NewService(registry, &memCache{}, slog.New(slog.DiscardHandler))

	tests := []struct {
		ean, title string
		tmdbID     int64
		confirm    bool
		format     string
		voiceover  string
	}{
		{ean: "5900058301362", title: "Wkręceni 2", tmdbID: 340389, confirm: false, format: "bluray", voiceover: "yes"},
		{ean: "5906190325662", title: "Listy do M. 4", tmdbID: 792219, confirm: false, format: "dvd", voiceover: "unknown"},
		{ean: "5906190326195", title: "Kursk", tmdbID: 401200, confirm: true, format: "bluray", voiceover: "unknown"},
		{ean: "5906190327048", title: "25 lat niewinności", tmdbID: 741652, confirm: true, format: "bluray", voiceover: "unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.ean, func(t *testing.T) {
			res := svc.Lookup(context.Background(), tc.ean)
			if len(res.Errors) != 0 || len(res.Candidates) == 0 {
				t.Fatalf("result = %+v", res)
			}
			top := res.Candidates[0]
			if top.Type != "movie" || top.Provider != "movie" || top.Title.TMDBID == nil || *top.Title.TMDBID != tc.tmdbID || top.Title.Name != tc.title {
				t.Fatalf("top candidate = %+v", top)
			}
			if res.NeedsConfirmation != tc.confirm {
				t.Fatalf("needs_confirmation = %v, want %v (confidences %v)", res.NeedsConfirmation, tc.confirm, confidences(res))
			}
			e := top.Edition
			if e.Barcode != tc.ean || e.Format == nil || *e.Format != tc.format || e.PLVoiceover != tc.voiceover {
				t.Fatalf("edition = %+v", e)
			}
		})
	}

	t.Run("ambiguous same-name films stay below the threshold", func(t *testing.T) {
		res := svc.Lookup(context.Background(), "5906190326195")
		if len(res.Candidates) < 2 || res.Candidates[0].Confidence >= lookup.ConfirmThreshold {
			t.Fatalf("confidences = %v", confidences(res))
		}
	})

	t.Run("product without a TMDB match goes to manual entry", func(t *testing.T) {
		res := svc.Lookup(context.Background(), "5902600069706")
		if len(res.Errors) != 0 || len(res.Candidates) != 0 || !res.NeedsConfirmation {
			t.Fatalf("result = %+v", res)
		}
	})

	t.Run("an Allegro refusal is reported and not cached", func(t *testing.T) {
		for range 2 {
			before := u.count("allegro")
			res := svc.Lookup(context.Background(), refusedEAN)
			if len(res.Candidates) == 0 || *res.Candidates[0].Title.TMDBID != 340389 {
				t.Fatalf("candidates = %+v", res.Candidates)
			}
			if len(res.Errors) != 1 || res.Errors[0].Provider != "movie" || !strings.Contains(res.Errors[0].Error, "allegro refused catalog access") {
				t.Fatalf("errors = %+v", res.Errors)
			}
			if u.count("allegro") != before+1 {
				t.Fatal("refused answer served from the cache")
			}
		}
	})

	t.Run("a rescan is served from the cache", func(t *testing.T) {
		before := u.count("tmdb") + u.count("allegro")
		svc.Lookup(context.Background(), "5900058301362")
		if after := u.count("tmdb") + u.count("allegro"); after != before {
			t.Fatalf("upstream requests %d -> %d", before, after)
		}
	})

	t.Run("books are not looked up", func(t *testing.T) {
		if u.provider().Accepts("9788324631766") || !u.provider().Accepts("5906190326195") {
			t.Fatal("scope")
		}
		before := u.count("allegro")
		res := svc.Lookup(context.Background(), "9788324631766")
		if len(res.Candidates) != 0 || u.count("allegro") != before {
			t.Fatalf("result = %+v, allegro hits %d -> %d", res, before, u.count("allegro"))
		}
	})

	t.Run("per-source counters are exposed", func(t *testing.T) {
		body := scrapeMetrics(t)
		for _, want := range []string{
			`ohara_source_requests_total{source="allegro",status="ok"}`,
			`ohara_source_requests_total{source="ceneo",status="ok"}`,
			`ohara_source_requests_total{source="upcitemdb",status="ok"}`,
			`ohara_source_requests_total{source="tmdb",status="ok"}`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("metrics missing %s", want)
			}
		}
	})
}

func confidences(res lookup.Result) []float64 {
	out := make([]float64, 0, len(res.Candidates))
	for i := range res.Candidates {
		out = append(out, res.Candidates[i].Confidence)
	}
	return out
}

func scrapeMetrics(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func serveFile(t *testing.T, w http.ResponseWriter, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Errorf("fixture: %v", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if strings.HasSuffix(path, ".json") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	if _, err := w.Write(b); err != nil {
		t.Errorf("write fixture: %v", err)
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
