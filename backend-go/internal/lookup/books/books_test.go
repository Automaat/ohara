package books_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/books"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// catalog serves the recorded fixtures under the three providers' API paths.
// A query without a fixture gets the provider's real "no match" answer.
type catalog struct {
	t      *testing.T
	failBN bool

	mu   sync.Mutex
	hits map[string]int
	keys []string
}

func (c *catalog) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var dir, name, empty string
	switch r.URL.Path {
	case "/api/institutions/bibs.json":
		dir, name, empty = "bn", q.Get("isbnIssn"), `{"nextPage":"","bibs":[]}`
		if q.Get("limit") == "" {
			c.t.Errorf("bn query without limit: %s", r.URL)
		}
	case "/api/books":
		if q.Get("jscmd") != "details" || q.Get("format") != "json" {
			c.t.Errorf("openlibrary query = %s", r.URL)
		}
		dir, name, empty = "openlibrary", strings.TrimPrefix(q.Get("bibkeys"), "ISBN:"), `{}`
	case "/books/v1/volumes":
		dir, name, empty = "google", strings.TrimPrefix(q.Get("q"), "isbn:"), `{"kind":"books#volumes","totalItems":0}`
		c.mu.Lock()
		c.keys = append(c.keys, q.Get("key"))
		c.mu.Unlock()
	default:
		http.NotFound(w, r)
		return
	}
	c.mu.Lock()
	c.hits[dir]++
	c.mu.Unlock()
	if dir == "bn" && c.failBN {
		http.Error(w, "maintenance", http.StatusServiceUnavailable)
		return
	}
	body, err := os.ReadFile(filepath.Join("testdata", dir, name+".json"))
	if os.IsNotExist(err) {
		body = []byte(empty)
	} else if err != nil {
		c.t.Fatal(err)
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(body)
}

func (c *catalog) hitCount(dir string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits[dir]
}

func newCatalog(t *testing.T) (*catalog, *httptest.Server) {
	t.Helper()
	c := &catalog{t: t, hits: map[string]int{}}
	srv := httptest.NewServer(c)
	t.Cleanup(srv.Close)
	return c, srv
}

// chain registers the providers in production order against srv.
func chain(t *testing.T, srv *httptest.Server, logger *slog.Logger) *lookup.Service {
	t.Helper()
	r := &lookup.Registry{}
	for _, p := range []lookup.Provider{
		books.BN{BaseURL: srv.URL, Client: srv.Client()},
		books.OpenLibrary{BaseURL: srv.URL, Client: srv.Client()},
		books.GoogleBooks{APIKey: "test-key", BaseURL: srv.URL, Client: srv.Client()},
	} {
		if err := r.Register(p, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return lookup.NewService(r, nil, logger)
}

type want struct {
	name, publisher, label string
	authors, sources       []string
	titleYear, editionYear int
	binding, bnID, olID    string
	cover, description     bool
}

func TestBookChainFixtures(t *testing.T) {
	tests := []struct {
		isbn string
		want *want
	}{
		{"9788375780635", &want{
			name: "Ostatnie życzenie", authors: []string{"Andrzej Sapkowski"},
			publisher: "SuperNowa", label: "Wydanie I w tej edycji", titleYear: 1993, editionYear: 2014,
			binding: "paperback", bnID: "5382027", olID: "OL2577485W", cover: true, description: true,
			sources: []string{"bn", "openlibrary", "google_books"},
		}},
		{"9788308049396", &want{
			name:      "Księgi Jakubowe albo Wielka podróż przez siedem granic, pięć języków i trzy duże religie, nie licząc tych małych",
			authors:   []string{"Olga Tokarczuk"},
			publisher: "Wydawnictwo Literackie", titleYear: 2014, editionYear: 2014, binding: "hardcover",
			bnID: "3252369", olID: "OL20317821W", cover: true,
			sources: []string{"bn", "openlibrary"},
		}},
		{"9788308025345", &want{
			name: "Solaris", authors: []string{"Stanisław Lem"},
			publisher: "Wydawnictwo Literackie", editionYear: 1994, bnID: "1018707", olID: "OL109524W",
			sources: []string{"bn", "openlibrary"},
		}},
		{"9780140449136", &want{
			name: "Crime and punishment", authors: []string{"Fiódor Dostoievski"},
			publisher: "Penguin", editionYear: 2003, olID: "OL166894W", cover: true, description: true,
			sources: []string{"openlibrary", "google_books"},
		}},
		{"9780261103573", &want{
			name: "The Fellowship of the Ring", publisher: "HarperCollinsPublishers", editionYear: 2011,
			binding: "paperback", olID: "OL27513W", cover: true, description: true,
			sources: []string{"openlibrary"},
		}},
		{"9788300000005", nil},
	}
	_, srv := newCatalog(t)
	svc := chain(t, srv, nil)
	for _, tt := range tests {
		t.Run(tt.isbn, func(t *testing.T) {
			res := svc.Lookup(context.Background(), tt.isbn)
			if len(res.Errors) != 0 {
				t.Fatalf("errors = %+v", res.Errors)
			}
			if tt.want == nil {
				if len(res.Candidates) != 0 {
					t.Fatalf("unknown ISBN matched: %+v", res.Candidates)
				}
				return
			}
			if len(res.Candidates) != 1 {
				t.Fatalf("want one merged book, got %+v", res.Candidates)
			}
			checkBook(t, tt.isbn, res.Candidates[0], tt.want)
		})
	}
}

func checkBook(t *testing.T, isbn string, c lookup.Candidate, w *want) {
	t.Helper()
	if c.Type != "book" || c.Edition.Barcode != isbn || c.Provider != w.sources[0] {
		t.Errorf("type/barcode/provider = %s %s %s", c.Type, c.Edition.Barcode, c.Provider)
	}
	if !slices.Equal(c.Sources, w.sources) {
		t.Errorf("sources = %v, want %v", c.Sources, w.sources)
	}
	if c.Title.Name != w.name {
		t.Errorf("name = %q, want %q", c.Title.Name, w.name)
	}
	if !slices.Equal(c.Title.Authors, w.authors) {
		t.Errorf("authors = %q, want %q", c.Title.Authors, w.authors)
	}
	if c.Edition.Publisher != w.publisher || c.Edition.Label != w.label {
		t.Errorf("publisher/label = %q %q, want %q %q", c.Edition.Publisher, c.Edition.Label, w.publisher, w.label)
	}
	if got := deref(c.Title.Year); got != w.titleYear {
		t.Errorf("title year = %d, want %d", got, w.titleYear)
	}
	if got := deref(c.Edition.Year); got != w.editionYear {
		t.Errorf("edition year = %d, want %d", got, w.editionYear)
	}
	if got := deref(c.Edition.Binding); got != w.binding {
		t.Errorf("binding = %q, want %q", got, w.binding)
	}
	if got := deref(c.Title.BNID); got != w.bnID {
		t.Errorf("bn_id = %q, want %q", got, w.bnID)
	}
	if got := deref(c.Title.OpenLibraryID); got != w.olID {
		t.Errorf("openlibrary_id = %q, want %q", got, w.olID)
	}
	if (c.CoverURL != "") != w.cover || (c.CoverURL != "" && !strings.HasPrefix(c.CoverURL, "https://")) {
		t.Errorf("cover = %q, want present=%v over https", c.CoverURL, w.cover)
	}
	if (c.Description != "") != w.description {
		t.Errorf("description = %q, want present=%v", c.Description, w.description)
	}
	if c.Confidence < 0.5 || c.Confidence > 1 {
		t.Errorf("confidence = %v", c.Confidence)
	}
}

func deref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func TestBNKeepsOnlyTheRecordOfThisISBN(t *testing.T) {
	raw, err := os.ReadFile("testdata/bn/9788375780635.json")
	if err != nil {
		t.Fatal(err)
	}
	cands, err := books.BN{}.Parse("9788375780635", raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(cands) != 1 || deref(cands[0].Title.BNID) != "5382027" {
		t.Fatalf("translations citing the ISBN as original must not match: %+v", cands)
	}
	if cands[0].Title.OriginalTitle != nil {
		t.Fatalf("original title of a Polish book = %q", *cands[0].Title.OriginalTitle)
	}

	translated, err := books.BN{}.Parse("9786171204997", raw)
	if err != nil || len(translated) != 1 {
		t.Fatalf("translation = %+v, %v", translated, err)
	}
	c := translated[0]
	if deref(c.Title.OriginalTitle) != "Ostatnie życzenie" || deref(c.Edition.Binding) != "hardcover" {
		t.Fatalf("translation original/binding = %q %q", deref(c.Title.OriginalTitle), deref(c.Edition.Binding))
	}
	if !slices.Equal(c.Title.Authors, []string{"Andrzej Sapkowski"}) {
		t.Fatalf("translators and analytic entries must not be authors: %q", c.Title.Authors)
	}
}

func TestBNRetriesWithISBN10(t *testing.T) {
	c, srv := newCatalog(t)
	p := books.BN{BaseURL: srv.URL, Client: srv.Client()}
	raw, err := p.Fetch(context.Background(), "9788308025345")
	if err != nil {
		t.Fatal(err)
	}
	if c.hitCount("bn") != 2 {
		t.Fatalf("bn requests = %d, want ISBN-13 then ISBN-10", c.hitCount("bn"))
	}
	cands, err := p.Parse("9788308025345", raw)
	if err != nil || len(cands) != 1 {
		t.Fatalf("parse = %+v, %v", cands, err)
	}

	if _, err := p.Fetch(context.Background(), "9788375780635"); err != nil {
		t.Fatal(err)
	}
	if c.hitCount("bn") != 3 {
		t.Fatalf("a matching ISBN-13 answer must not be retried: %d requests", c.hitCount("bn"))
	}
}

func TestProviderFailureFallsThrough(t *testing.T) {
	c, srv := newCatalog(t)
	c.failBN = true
	var logs bytes.Buffer
	svc := chain(t, srv, slog.New(slog.NewTextHandler(&logs, nil)))

	before := scrape(t)
	res := svc.Lookup(context.Background(), "9788375780635")
	after := scrape(t)

	if len(res.Errors) != 1 || res.Errors[0].Provider != "bn" || !strings.Contains(res.Errors[0].Error, "503") {
		t.Fatalf("errors = %+v", res.Errors)
	}
	if len(res.Candidates) != 1 || res.Candidates[0].Provider != "openlibrary" || res.Candidates[0].Title.Name != "Ostatnie życzenie" {
		t.Fatalf("next providers must answer: %+v", res.Candidates)
	}
	if !strings.Contains(logs.String(), "provider=bn") {
		t.Fatalf("failure not logged with the provider name: %s", logs.String())
	}
	for _, tt := range []struct {
		metric, provider string
		delta            float64
	}{
		{"ohara_lookup_provider_requests_total", "bn", 1},
		{"ohara_lookup_provider_errors_total", "bn", 1},
		{"ohara_lookup_provider_requests_total", "openlibrary", 1},
		{"ohara_lookup_provider_errors_total", "openlibrary", 0},
		{"ohara_lookup_provider_requests_total", "google_books", 1},
	} {
		key := tt.metric + `{provider="` + tt.provider + `"}`
		if got := after[key] - before[key]; got != tt.delta {
			t.Errorf("%s grew by %v, want %v", key, got, tt.delta)
		}
	}
}

func TestBookProvidersSkipOtherBarcodes(t *testing.T) {
	c, srv := newCatalog(t)
	res := chain(t, srv, nil).Lookup(context.Background(), "5901234123457")
	if len(res.Candidates) != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v", res)
	}
	for _, dir := range []string{"bn", "openlibrary", "google"} {
		if n := c.hitCount(dir); n != 0 {
			t.Errorf("%s queried %d times for a movie barcode", dir, n)
		}
	}
}

func TestGoogleBooksKey(t *testing.T) {
	c, srv := newCatalog(t)
	p := books.GoogleBooks{APIKey: "s3cr3t", BaseURL: srv.URL, Client: srv.Client()}
	if _, err := p.Fetch(context.Background(), "9780140449136"); err != nil {
		t.Fatal(err)
	}
	if len(c.keys) != 1 || c.keys[0] != "s3cr3t" {
		t.Fatalf("keys sent = %v", c.keys)
	}

	if _, err := (books.GoogleBooks{}).Fetch(context.Background(), "9780140449136"); !errors.Is(err, books.ErrNoAPIKey) {
		t.Fatalf("keyless fetch err = %v", err)
	}

	srv.Close()
	_, err := p.Fetch(context.Background(), "9780140449136")
	if err == nil || strings.Contains(err.Error(), "s3cr3t") {
		t.Fatalf("transport error must not leak the key: %v", err)
	}
}

func TestProvidersRejectBadResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/books/v1/volumes" {
			_, _ = w.Write(bytes.Repeat([]byte(" "), 5<<20))
			return
		}
		_, _ = w.Write([]byte("<html>busy</html>"))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	if _, err := (books.BN{BaseURL: srv.URL}).Fetch(ctx, "9788375780635"); err == nil {
		t.Fatal("bn accepted HTML")
	}
	if _, err := (books.OpenLibrary{BaseURL: srv.URL}).Fetch(ctx, "9788375780635"); err == nil {
		t.Fatal("openlibrary accepted HTML")
	}
	if _, err := (books.GoogleBooks{APIKey: "k", BaseURL: srv.URL}).Fetch(ctx, "9788375780635"); err == nil {
		t.Fatal("google books accepted an oversized body")
	}
	for _, p := range []lookup.Provider{books.BN{}, books.OpenLibrary{}, books.GoogleBooks{}} {
		if _, err := p.Parse("9788375780635", json.RawMessage(`"x"`)); err == nil {
			t.Errorf("%s parsed garbage", p.Name())
		}
	}
}

func TestProvidersCountSourceOutcomes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/institutions/bibs.json":
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case "/api/books":
			_, _ = w.Write([]byte("<html>busy</html>"))
		default:
			_, _ = w.Write([]byte(`{"kind":"books#volumes","items":"changed"}`))
		}
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	before := scrape(t)
	_, _ = (books.BN{BaseURL: srv.URL}).Fetch(ctx, "9788375780635")
	_, _ = (books.OpenLibrary{BaseURL: srv.URL}).Fetch(ctx, "9788375780635")
	if _, err := (books.GoogleBooks{APIKey: "k", BaseURL: srv.URL}).Fetch(ctx, "9788375780635"); err == nil {
		t.Fatal("google books accepted a changed response shape")
	}
	after := scrape(t)
	for series, want := range map[string]float64{
		`ohara_source_requests_total{source="bn",status="http_429"}`:             1,
		`ohara_source_errors_total{source="bn"}`:                                 1,
		`ohara_source_requests_total{source="openlibrary",status="parse_error"}`: 1,
		`ohara_source_parse_errors_total{source="openlibrary"}`:                  1,
		`ohara_source_requests_total{source="google_books",status="ok"}`:         0,
		`ohara_source_parse_errors_total{source="google_books"}`:                 1,
	} {
		if d := after[series] - before[series]; d != want {
			t.Errorf("%s grew by %v, want %v", series, d, want)
		}
	}
}

// TestProvidersTimeSourceRequests checks that each request is timed under
// its source whatever its outcome, a response shape that fails to parse
// after a 200 included.
func TestProvidersTimeSourceRequests(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/institutions/bibs.json":
			http.Error(w, "slow down", http.StatusTooManyRequests)
		case "/api/books":
			_, _ = w.Write([]byte("<html>busy</html>"))
		default:
			_, _ = w.Write([]byte(`{"kind":"books#volumes","items":"changed"}`))
		}
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	before := scrape(t)
	_, _ = (books.BN{BaseURL: srv.URL}).Fetch(ctx, "9788375780635")
	_, _ = (books.OpenLibrary{BaseURL: srv.URL}).Fetch(ctx, "9788375780635")
	_, _ = (books.GoogleBooks{APIKey: "k", BaseURL: srv.URL}).Fetch(ctx, "9788375780635")
	after := scrape(t)
	for _, source := range []string{"bn", "openlibrary", "google_books"} {
		series := `ohara_source_request_duration_seconds_count{source="` + source + `"}`
		if d := after[series] - before[series]; d != 1 {
			t.Errorf("%s grew by %v, want 1", series, d)
		}
	}
}

func TestLookupRecordsSourceResults(t *testing.T) {
	_, srv := newCatalog(t)
	res := chain(t, srv, nil).Lookup(context.Background(), "9788375780635")
	if len(res.Candidates) == 0 {
		t.Fatalf("lookup = %+v", res)
	}
	got := scrape(t)
	for _, source := range []string{"bn", "openlibrary", "google_books"} {
		if v, ok := got[`ohara_source_results{source="`+source+`"}`]; !ok || v != 1 {
			t.Errorf("%s results = %v (exported %v), want 1", source, v, ok)
		}
		if _, ok := got[`ohara_source_last_success_timestamp_seconds{source="`+source+`"}`]; !ok {
			t.Errorf("%s last success not exported", source)
		}
	}
}

// scrape reads the lookup counters from /metrics as "name{labels}" → value.
func scrape(t *testing.T) map[string]float64 {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	out := map[string]float64{}
	sc := bufio.NewScanner(rec.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "ohara_lookup_") && !strings.HasPrefix(line, "ohara_source_") {
			continue
		}
		key, val, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(val, 64)
		if err != nil {
			t.Fatal(err)
		}
		out[key] = v
	}
	return out
}

func TestBNAuthorsFollowRelatorCodes(t *testing.T) {
	raw := json.RawMessage(`{"bibs":[{"id":7,"publicationYear":"2003","marc":{"fields":[
		{"001":"b7"},
		{"020":{"ind1":" ","ind2":" ","subfields":[{"a":"9780140449136"}]}},
		{"100":{"ind1":"1","ind2":" ","subfields":[{"a":"Dostoyevsky, Fyodor"}]}},
		{"245":{"ind1":"1","ind2":"0","subfields":[{"a":"Crime and punishment /"}]}},
		{"700":{"ind1":"1","ind2":" ","subfields":[{"a":"McDuff, David,"},{"4":"trl"}]}},
		{"700":{"ind1":"1","ind2":" ","subfields":[{"a":"Smith, Jane"},{"4":"aut"}]}},
		{"700":{"ind1":"1","ind2":" ","subfields":[{"a":"Nowak, Anna"},{"e":"Autor"}]}},
		{"700":{"ind1":"1","ind2":" ","subfields":[{"a":"Kowalski, Jan"},{"e":"Tłumaczenie"}]}},
		{"700":{"ind1":"1","ind2":" ","subfields":[{"a":"Lee, Kim"}]}}
	]}}]}`)
	cands, err := books.BN{}.Parse("9780140449136", raw)
	if err != nil || len(cands) != 1 {
		t.Fatalf("parse = %+v, %v", cands, err)
	}
	want := []string{"Fyodor Dostoyevsky", "Jane Smith", "Anna Nowak", "Kim Lee"}
	if got := cands[0].Title.Authors; !slices.Equal(got, want) {
		t.Fatalf("authors = %q, want %q", got, want)
	}
}
