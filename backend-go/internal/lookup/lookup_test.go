package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

type fakeProvider struct {
	name       string
	confidence float64
	fetchErr   error
	parseErr   error

	mu      sync.Mutex
	fetches int
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Fetch(_ context.Context, code string) (json.RawMessage, error) {
	p.mu.Lock()
	p.fetches++
	p.mu.Unlock()
	if p.fetchErr != nil {
		return nil, p.fetchErr
	}
	return json.Marshal(map[string]string{"name": p.name + " " + code})
}

func (p *fakeProvider) Parse(code string, raw json.RawMessage) ([]lookup.Candidate, error) {
	if p.parseErr != nil {
		return nil, p.parseErr
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	return []lookup.Candidate{{
		Type:       "movie",
		Title:      lookup.TitleFields{Name: body.Name},
		Edition:    lookup.EditionFields{Barcode: code},
		Provider:   "spoofed",
		Confidence: p.confidence,
	}}, nil
}

func (p *fakeProvider) fetchCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fetches
}

type memCache struct {
	mu      sync.Mutex
	entries map[string]json.RawMessage
	getErr  error
}

func (c *memCache) Get(_ context.Context, provider, code string, _ time.Duration) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.getErr != nil {
		return nil, false, c.getErr
	}
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

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestRegistryRejectsDuplicates(t *testing.T) {
	r := &lookup.Registry{}
	if err := r.Register(&fakeProvider{name: "a"}, 0); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := r.Register(&fakeProvider{name: "a"}, time.Hour); !errors.Is(err, lookup.ErrDuplicateProvider) {
		t.Fatalf("duplicate err = %v", err)
	}
	if got := r.Names(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("names = %v", got)
	}
}

func TestLookupMergesSortsAndReportsFailures(t *testing.T) {
	r := &lookup.Registry{}
	low := &fakeProvider{name: "low", confidence: 0.2}
	high := &fakeProvider{name: "high", confidence: 1.7}
	tie := &fakeProvider{name: "tie", confidence: 0.2}
	broken := &fakeProvider{name: "broken", fetchErr: errors.New("timeout")}
	garbled := &fakeProvider{name: "garbled", parseErr: errors.New("bad html")}
	for _, p := range []*fakeProvider{low, high, broken, tie, garbled} {
		if err := r.Register(p, 0); err != nil {
			t.Fatalf("register: %v", err)
		}
	}
	res := lookup.NewService(r, nil, discard()).Lookup(context.Background(), "5901234123457")

	if res.Barcode != "5901234123457" {
		t.Fatalf("barcode = %q", res.Barcode)
	}
	var order []string
	for _, c := range res.Candidates {
		order = append(order, c.Provider)
	}
	if len(order) != 3 || order[0] != "high" || order[1] != "low" || order[2] != "tie" {
		t.Fatalf("candidate order = %v, want [high low tie]", order)
	}
	if res.Candidates[0].Confidence != 1 {
		t.Fatalf("confidence not clamped: %v", res.Candidates[0].Confidence)
	}
	if len(res.Errors) != 2 || res.Errors[0].Provider != "broken" || res.Errors[1].Provider != "garbled" {
		t.Fatalf("errors = %+v", res.Errors)
	}
}

func TestLookupWithoutProviders(t *testing.T) {
	res := lookup.NewService(&lookup.Registry{}, nil, discard()).Lookup(context.Background(), "96385074")
	b, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(b) != `{"barcode":"96385074","candidates":[],"needs_confirmation":true,"errors":[]}` {
		t.Fatalf("body = %s", b)
	}
}

func TestLookupCache(t *testing.T) {
	cached := &fakeProvider{name: "cached", confidence: 0.5}
	uncached := &fakeProvider{name: "uncached", confidence: 0.5}
	r := &lookup.Registry{}
	if err := r.Register(cached, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(uncached, 0); err != nil {
		t.Fatal(err)
	}
	cache := &memCache{}
	svc := lookup.NewService(r, cache, discard())
	for range 3 {
		if res := svc.Lookup(context.Background(), "96385074"); len(res.Candidates) != 2 {
			t.Fatalf("candidates = %+v", res.Candidates)
		}
	}
	if cached.fetchCount() != 1 || uncached.fetchCount() != 3 {
		t.Fatalf("fetches cached=%d uncached=%d, want 1 and 3", cached.fetchCount(), uncached.fetchCount())
	}

	cache.entries["cached/96385074"] = json.RawMessage(`"unparseable"`)
	if res := svc.Lookup(context.Background(), "96385074"); len(res.Errors) != 0 || cached.fetchCount() != 2 {
		t.Fatalf("unreadable cache entry: errors %+v, fetches %d", res.Errors, cached.fetchCount())
	}

	cache.getErr = errors.New("db down")
	if res := svc.Lookup(context.Background(), "96385074"); len(res.Candidates) != 2 || len(res.Errors) != 0 {
		t.Fatalf("cache failure must not fail the lookup: %+v", res)
	}
}

func TestStub(t *testing.T) {
	tests := []struct {
		code, kind string
	}{
		{"9788373012349", "book"},
		{"5901234123457", "movie"},
	}
	for _, tt := range tests {
		raw, err := lookup.Stub{}.Fetch(context.Background(), tt.code)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		cands, err := lookup.Stub{}.Parse(tt.code, raw)
		if err != nil || len(cands) != 1 {
			t.Fatalf("parse = %+v, %v", cands, err)
		}
		if c := cands[0]; c.Type != tt.kind || c.Edition.Barcode != tt.code || c.Title.Name == "" {
			t.Fatalf("candidate = %+v", c)
		}
	}
	if _, err := (lookup.Stub{}).Parse("x", json.RawMessage(`[]`)); err == nil {
		t.Fatal("stub parsed garbage")
	}
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

func TestLookupEndpoint(t *testing.T) {
	r := &lookup.Registry{}
	if err := r.Register(lookup.Stub{}, 0); err != nil {
		t.Fatal(err)
	}
	h := lookup.NewHandler(lookup.NewService(r, nil, discard()), discard())
	srv := httptest.NewServer(server.New(server.Config{}, discard(), server.Deps{API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)

	code, body := get(t, srv.URL+"/api/lookup/0-306-40615-2")
	if code != http.StatusOK {
		t.Fatalf("lookup = %d %s", code, body)
	}
	var res lookup.Result
	if err := json.Unmarshal([]byte(body), &res); err != nil {
		t.Fatal(err)
	}
	if res.Barcode != "9780306406157" || len(res.Candidates) != 1 {
		t.Fatalf("result = %+v", res)
	}
	c := res.Candidates[0]
	if c.Type != "book" || c.Provider != "stub" || c.Confidence != 0.1 || c.Edition.Barcode != "9780306406157" {
		t.Fatalf("candidate = %+v", c)
	}

	for _, bad := range []string{"123", "5901234123458"} {
		if code, body := get(t, srv.URL+"/api/lookup/"+bad); code != http.StatusUnprocessableEntity {
			t.Fatalf("lookup %s = %d %s, want 422", bad, code, body)
		}
	}
}

func TestPGCache(t *testing.T) {
	pool := dbtest.Migrated(t)
	cache := lookup.NewPGCache(pool)
	ctx := context.Background()

	if _, ok, err := cache.Get(ctx, "bn", "9788373012349", time.Hour); ok || err != nil {
		t.Fatalf("empty cache get = %v, %v", ok, err)
	}
	if err := cache.Put(ctx, "bn", "9788373012349", json.RawMessage(`{"v":1}`)); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := cache.Put(ctx, "bn", "9788373012349", json.RawMessage(`{"v":2}`)); err != nil {
		t.Fatalf("replace: %v", err)
	}
	raw, ok, err := cache.Get(ctx, "bn", "9788373012349", time.Hour)
	if err != nil || !ok {
		t.Fatalf("get = %v, %v", ok, err)
	}
	var v struct {
		V int `json:"v"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.V != 2 {
		t.Fatalf("cached = %s", raw)
	}
	if _, err := pool.Exec(ctx, `UPDATE lookup_cache SET fetched_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.Get(ctx, "bn", "9788373012349", time.Hour); ok || err != nil {
		t.Fatalf("stale entry served: %v, %v", ok, err)
	}
	if _, ok, _ := cache.Get(ctx, "openlibrary", "9788373012349", 24*time.Hour); ok {
		t.Fatal("cache leaked across providers")
	}
}

// fixedProvider answers every barcode with the same candidates; scope, when
// set, limits the barcodes it accepts.
type fixedProvider struct {
	name  string
	cands []lookup.Candidate
	scope func(string) bool

	mu      sync.Mutex
	fetches int
}

func (p *fixedProvider) Name() string { return p.name }

func (p *fixedProvider) Accepts(code string) bool { return p.scope == nil || p.scope(code) }

func (p *fixedProvider) Fetch(context.Context, string) (json.RawMessage, error) {
	p.mu.Lock()
	p.fetches++
	p.mu.Unlock()
	return json.RawMessage(`{}`), nil
}

func (p *fixedProvider) Parse(string, json.RawMessage) ([]lookup.Candidate, error) {
	return p.cands, nil
}

func book(code, name string, conf float64) lookup.Candidate {
	return lookup.Candidate{Type: "book", Title: lookup.TitleFields{Name: name}, Edition: lookup.EditionFields{Barcode: code}, Confidence: conf}
}

func TestLookupMergesBookAnswers(t *testing.T) {
	const code = "9788375780635"
	national := book(code, "Ostatnie życzenie", 0.9)
	national.Title.Authors = []string{"Andrzej Sapkowski"}
	national.Title.BNID = new("5382027")
	national.Edition.Publisher = "SuperNowa"

	other := book("9788375780710", "Wiedźmin. T. 1-8", 0.9)
	library := book(code, "Ostatnie zyczenie", 0.8)
	library.Title.Authors = []string{"A. Sapkowski"}
	library.Title.OpenLibraryID = new("OL2577485W")
	library.Edition.Year = new(2014)
	library.Edition.Publisher = "SuperNOWA"
	weaker := book(code, "Ostatnie życzenie (dodruk)", 0.6)

	store := book(code, "Ostatnie życzenie", 0.95)
	store.CoverURL = "https://example.test/cover.jpg"
	store.Description = "Geralt z Rivii."
	store.Edition.Binding = new("paperback")

	placeholder := book(code, "Książka "+code, 0.1)
	movie := lookup.Candidate{Type: "movie", Title: lookup.TitleFields{Name: "Film"}, Edition: lookup.EditionFields{Barcode: code}, Confidence: 0.3}

	r := &lookup.Registry{}
	for _, p := range []*fixedProvider{
		{name: "national", cands: []lookup.Candidate{national, other}},
		{name: "library", cands: []lookup.Candidate{weaker, library}},
		{name: "store", cands: []lookup.Candidate{store}},
		{name: "placeholder", cands: []lookup.Candidate{placeholder, movie}},
	} {
		if err := r.Register(p, 0); err != nil {
			t.Fatal(err)
		}
	}
	res := lookup.NewService(r, nil, discard()).Lookup(context.Background(), code)

	if len(res.Candidates) != 5 {
		t.Fatalf("want merged book + other ISBN + weaker match + placeholder + movie, got %+v", res.Candidates)
	}
	m := res.Candidates[0]
	if m.Provider != "national" || m.Confidence != 0.95 {
		t.Fatalf("merged provider/confidence = %s %v", m.Provider, m.Confidence)
	}
	if got := m.Sources; len(got) != 3 || got[0] != "national" || got[1] != "library" || got[2] != "store" {
		t.Fatalf("sources = %v", got)
	}
	if m.Title.Name != "Ostatnie życzenie" || m.Title.Authors[0] != "Andrzej Sapkowski" || m.Edition.Publisher != "SuperNowa" {
		t.Fatalf("earlier provider must win: %+v", m)
	}
	if *m.Title.BNID != "5382027" || *m.Title.OpenLibraryID != "OL2577485W" || *m.Edition.Year != 2014 ||
		*m.Edition.Binding != "paperback" || m.CoverURL == "" || m.Description == "" {
		t.Fatalf("later providers must fill gaps: %+v", m)
	}
	for _, c := range res.Candidates[1:] {
		if len(c.Sources) != 1 || c.Sources[0] != c.Provider {
			t.Fatalf("unmerged candidate sources = %v for %s", c.Sources, c.Provider)
		}
	}
	names := map[string]bool{}
	for _, c := range res.Candidates[1:] {
		names[c.Title.Name] = true
	}
	for _, n := range []string{"Wiedźmin. T. 1-8", "Ostatnie życzenie (dodruk)", "Książka " + code, "Film"} {
		if !names[n] {
			t.Errorf("%q dropped by the merge", n)
		}
	}
}

func TestLookupKeepsSingleBookAnswer(t *testing.T) {
	const code = "9780140449136"
	r := &lookup.Registry{}
	if err := r.Register(&fixedProvider{name: "only", cands: []lookup.Candidate{book(code, "Crime and Punishment", 0.8)}}, 0); err != nil {
		t.Fatal(err)
	}
	res := lookup.NewService(r, nil, discard()).Lookup(context.Background(), code)
	if len(res.Candidates) != 1 || res.Candidates[0].Provider != "only" || res.Candidates[0].Sources[0] != "only" {
		t.Fatalf("result = %+v", res)
	}
}

func TestLookupSkipsOutOfScopeProviders(t *testing.T) {
	booksOnly := &fixedProvider{name: "books", scope: func(code string) bool { return code[:3] == "978" }}
	r := &lookup.Registry{}
	if err := r.Register(booksOnly, 0); err != nil {
		t.Fatal(err)
	}
	svc := lookup.NewService(r, nil, discard())
	if res := svc.Lookup(context.Background(), "5901234123457"); len(res.Errors) != 0 || len(res.Candidates) != 0 {
		t.Fatalf("result = %+v", res)
	}
	if booksOnly.fetches != 0 {
		t.Fatalf("out-of-scope provider fetched %d times", booksOnly.fetches)
	}
	svc.Lookup(context.Background(), "9780140449136")
	if booksOnly.fetches != 1 {
		t.Fatalf("in-scope fetches = %d", booksOnly.fetches)
	}
}

func TestLookupFitsCandidatesToCollectionLimits(t *testing.T) {
	const code = "9788375780635"
	long := strings.Repeat("ż", 300)
	c := book(code, long, 0.9)
	c.Title.OriginalTitle = new(long)
	c.Title.Year = new(1500)
	for range 25 {
		c.Title.Authors = append(c.Title.Authors, long)
	}
	c.Edition.Publisher = long
	c.Edition.Label = long
	c.Edition.Year = new(2500)
	r := &lookup.Registry{}
	if err := r.Register(&fixedProvider{name: "verbose", cands: []lookup.Candidate{c}}, 0); err != nil {
		t.Fatal(err)
	}
	res := lookup.NewService(r, nil, discard()).Lookup(context.Background(), code)
	if len(res.Candidates) != 1 {
		t.Fatalf("result = %+v", res)
	}
	got := res.Candidates[0]
	if !utf8.ValidString(got.Title.Name) || got.Title.Name == "" || !strings.HasPrefix(long, got.Title.Name) {
		t.Fatalf("name cut badly: %q", got.Title.Name)
	}

	var title collection.TitleInput
	var edition collection.EditionInput
	roundTrip(t, got.Title, &title)
	roundTrip(t, got.Edition, &edition)
	title.Kind = got.Type
	edition.TitleID = 1
	if e := title.Normalize(); e != nil {
		t.Fatalf("POST /api/titles would reject the candidate: %s %s", e.Field, e.Msg)
	}
	if e := edition.Normalize(); e != nil {
		t.Fatalf("POST /api/editions would reject the candidate: %s %s", e.Field, e.Msg)
	}
	if e := edition.CheckKind(got.Type); e != nil {
		t.Fatalf("edition kind check: %s %s", e.Field, e.Msg)
	}
}

func roundTrip(t *testing.T, in, out any) {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func TestNeedsConfirmation(t *testing.T) {
	tests := []struct {
		name        string
		confidences []float64
		want        bool
	}{
		{name: "no candidates", want: true},
		{name: "lone confident candidate", confidences: []float64{0.9, 0.5}, want: false},
		{name: "best below threshold", confidences: []float64{0.79}, want: true},
		{name: "two confident candidates", confidences: []float64{0.9, 0.85}, want: true},
		{name: "exactly at threshold", confidences: []float64{0.8}, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &lookup.Registry{}
			for i, c := range tc.confidences {
				if err := r.Register(&fakeProvider{name: string(rune('a' + i)), confidence: c}, 0); err != nil {
					t.Fatal(err)
				}
			}
			res := lookup.NewService(r, nil, discard()).Lookup(context.Background(), "5901234123457")
			if res.NeedsConfirmation != tc.want {
				t.Fatalf("needs_confirmation = %v, want %v", res.NeedsConfirmation, tc.want)
			}
		})
	}
}

func send(t *testing.T, method, url, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b)
}

func TestSelection(t *testing.T) {
	p := &fakeProvider{name: "movie", confidence: 0.5}
	r := &lookup.Registry{}
	if err := r.Register(p, time.Hour); err != nil {
		t.Fatal(err)
	}
	h := lookup.NewHandler(lookup.NewService(r, &memCache{}, discard()), discard())
	srv := httptest.NewServer(server.New(server.Config{}, discard(), server.Deps{API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)

	if code, body := get(t, srv.URL+"/api/lookup/5901234123457"); code != http.StatusOK || !strings.Contains(body, `"needs_confirmation":true`) {
		t.Fatalf("first lookup = %d %s", code, body)
	}
	bad := map[string]string{
		`{"type":"music","title":{"name":"X"}}`:                                  "type",
		`{"type":"movie","title":{"name":"  "}}`:                                 "title.name",
		`{"type":"movie","title":{"name":"X"},"edition":{"barcode":"96385074"}}`: "edition.barcode",
		`{"type":"movie","title":{"name":"X"},"extra":1}{}`:                      "body",
	}
	for in, field := range bad {
		code, body := send(t, http.MethodPut, srv.URL+"/api/lookup/5901234123457/selection", in)
		if code != http.StatusUnprocessableEntity || !strings.Contains(body, `"field":"`+field+`"`) {
			t.Fatalf("PUT %s = %d %s, want 422 on %s", in, code, body, field)
		}
	}
	if code, _ := send(t, http.MethodPut, srv.URL+"/api/lookup/123/selection", `{}`); code != http.StatusUnprocessableEntity {
		t.Fatalf("bad path barcode = %d", code)
	}

	code, body := send(t, http.MethodPut, srv.URL+"/api/lookup/5901234123457/selection",
		`{"type":"movie","title":{"name":" Kursk ","tmdb_id":401200},"edition":{"barcode":"5901234123457","format":"bluray"},"provider":"x","confidence":0.2}`)
	if code != http.StatusOK {
		t.Fatalf("select = %d %s", code, body)
	}
	fetches := p.fetchCount()
	code, body = get(t, srv.URL+"/api/lookup/5901234123457")
	var res lookup.Result
	if err := json.Unmarshal([]byte(body), &res); err != nil || code != http.StatusOK {
		t.Fatalf("lookup = %d %s", code, body)
	}
	if res.NeedsConfirmation || len(res.Candidates) != 1 || p.fetchCount() != fetches {
		t.Fatalf("result = %+v, fetches %d -> %d", res, fetches, p.fetchCount())
	}
	c := res.Candidates[0]
	if c.Provider != lookup.SelectionProvider || c.Confidence != 1 || c.Title.Name != "Kursk" || *c.Title.TMDBID != 401200 || c.Edition.Barcode != "5901234123457" {
		t.Fatalf("selection = %+v", c)
	}
}

func TestSelectionWithoutCache(t *testing.T) {
	svc := lookup.NewService(&lookup.Registry{}, nil, discard())
	if _, err := svc.Select(context.Background(), "5901234123457", lookup.Candidate{Type: "movie"}); !errors.Is(err, lookup.ErrNoCache) {
		t.Fatalf("err = %v", err)
	}
}

type partialProvider struct {
	fakeProvider
}

func (p *partialProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw, err := p.fakeProvider.Fetch(ctx, code)
	if err != nil {
		return nil, err
	}
	return nil, &lookup.PartialError{Raw: raw, Err: errors.New("allegro refused catalog access")}
}

func TestPartialAnswerIsReportedAndNotCached(t *testing.T) {
	p := &partialProvider{fakeProvider{name: "movie", confidence: 0.9}}
	r := &lookup.Registry{}
	if err := r.Register(p, time.Hour); err != nil {
		t.Fatal(err)
	}
	cache := &memCache{}
	svc := lookup.NewService(r, cache, discard())
	for range 2 {
		res := svc.Lookup(context.Background(), "5901234123457")
		if len(res.Candidates) != 1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error, "allegro refused") {
			t.Fatalf("result = %+v", res)
		}
	}
	if p.fetchCount() != 2 || len(cache.entries) != 0 {
		t.Fatalf("fetches %d, cache %v", p.fetchCount(), cache.entries)
	}
}
