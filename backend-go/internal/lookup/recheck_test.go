package lookup_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// clockCache is a lookup cache with a settable clock: Get serves an answer
// only while it is younger than maxAge, and Put stamps it with now, as
// PGCache does with fetched_at.
type clockCache struct {
	mu      sync.Mutex
	now     time.Time
	entries map[string]clockEntry
}

type clockEntry struct {
	raw       json.RawMessage
	fetchedAt time.Time
}

func newClockCache() *clockCache {
	return &clockCache{now: time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC), entries: map[string]clockEntry{}}
}

func (c *clockCache) Get(_ context.Context, provider, code string, maxAge time.Duration) (json.RawMessage, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[provider+"/"+code]
	if !ok || c.now.Sub(e.fetchedAt) >= maxAge {
		return nil, false, nil
	}
	return e.raw, true, nil
}

func (c *clockCache) Put(_ context.Context, provider, code string, raw json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[provider+"/"+code] = clockEntry{raw: raw, fetchedAt: c.now}
	return nil
}

// seed stores raw as if it was fetched age ago.
func (c *clockCache) seed(provider, code, raw string, age time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[provider+"/"+code] = clockEntry{raw: json.RawMessage(raw), fetchedAt: c.now.Add(-age)}
}

func (c *clockCache) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *clockCache) raw(provider, code string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return string(c.entries[provider+"/"+code].raw)
}

// learnedProvider is a fakeProvider whose answer without a name is a "no
// match": its cached miss parses to no candidates, while Fetch now answers
// one, as a source that learned the barcode since the last lookup.
type learnedProvider struct {
	fakeProvider
}

func (p *learnedProvider) Parse(code string, raw json.RawMessage) ([]lookup.Candidate, error) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, err
	}
	if body.Name == "" {
		return nil, nil
	}
	return p.fakeProvider.Parse(code, raw)
}

const (
	recheckCode = "5901234123457"
	missAnswer  = `{}`
	weekTTL     = 7 * 24 * time.Hour
)

func recheckService(t *testing.T) (*lookup.Service, *learnedProvider, *clockCache) {
	t.Helper()
	p := &learnedProvider{fakeProvider{name: "discogs", confidence: 0.9}}
	r := &lookup.Registry{}
	if err := r.Register(p, weekTTL); err != nil {
		t.Fatal(err)
	}
	cache := newClockCache()
	return lookup.NewService(r, cache, discard()), p, cache
}

func TestRecheckAsksAgainOnlyPastTheFloor(t *testing.T) {
	tests := []struct {
		name        string
		cachedAge   time.Duration
		recheck     bool
		wantFetches int
	}{
		{name: "recheck over a 2-hour-old miss", cachedAge: 2 * time.Hour, recheck: true, wantFetches: 1},
		{name: "recheck over a 10-minute-old miss", cachedAge: 10 * time.Minute, recheck: true, wantFetches: 0},
		{name: "recheck just under the floor", cachedAge: lookup.RecheckAfter - time.Second, recheck: true, wantFetches: 0},
		{name: "lookup over a 2-hour-old miss", cachedAge: 2 * time.Hour, recheck: false, wantFetches: 0},
		{name: "lookup over an expired miss", cachedAge: weekTTL + time.Hour, recheck: false, wantFetches: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, p, cache := recheckService(t)
			cache.seed("discogs", recheckCode, missAnswer, tt.cachedAge)
			lookupFn := svc.Lookup
			if tt.recheck {
				lookupFn = svc.Recheck
			}
			res := lookupFn(context.Background(), recheckCode)
			if got := p.fetchCount(); got != tt.wantFetches {
				t.Fatalf("fetches = %d, want %d", got, tt.wantFetches)
			}
			wantCands := tt.wantFetches
			if len(res.Candidates) != wantCands || len(res.Errors) != 0 {
				t.Fatalf("result = %+v, want %d candidates", res, wantCands)
			}
		})
	}
}

func TestRecheckReplacesTheCachedAnswer(t *testing.T) {
	svc, p, cache := recheckService(t)
	ctx := context.Background()
	cache.seed("discogs", recheckCode, missAnswer, 2*time.Hour)

	for range 3 {
		res := svc.Recheck(ctx, recheckCode)
		if len(res.Candidates) != 1 || res.Candidates[0].Title.Name != "discogs "+recheckCode {
			t.Fatalf("recheck result = %+v", res)
		}
		cache.advance(10 * time.Minute)
	}
	if got := p.fetchCount(); got != 1 {
		t.Fatalf("three rechecks within an hour made %d fetches, want 1", got)
	}
	if got := cache.raw("discogs", recheckCode); got == missAnswer {
		t.Fatalf("cached answer still the old miss: %s", got)
	}
	if res := svc.Lookup(ctx, recheckCode); len(res.Candidates) != 1 || p.fetchCount() != 1 {
		t.Fatalf("lookup after recheck: %+v, fetches %d", res, p.fetchCount())
	}

	cache.advance(lookup.RecheckAfter)
	svc.Recheck(ctx, recheckCode)
	if got := p.fetchCount(); got != 2 {
		t.Fatalf("recheck an hour after the fresh answer made %d fetches in all, want 2", got)
	}
}

func TestRecheckKeepsAShorterTTL(t *testing.T) {
	p := &learnedProvider{fakeProvider{name: "upc", confidence: 0.9}}
	r := &lookup.Registry{}
	if err := r.Register(p, 10*time.Minute); err != nil {
		t.Fatal(err)
	}
	cache := newClockCache()
	svc := lookup.NewService(r, cache, discard())
	cache.seed("upc", recheckCode, missAnswer, 20*time.Minute)
	svc.Recheck(context.Background(), recheckCode)
	if got := p.fetchCount(); got != 1 {
		t.Fatalf("recheck past a 10-minute TTL made %d fetches, want 1", got)
	}
}

func TestRecheckAnswersWithTheSelectionFirst(t *testing.T) {
	svc, p, cache := recheckService(t)
	ctx := context.Background()
	cache.seed("discogs", recheckCode, missAnswer, 2*time.Hour)
	picked := lookup.Candidate{Type: "music", Title: lookup.TitleFields{Name: "Picked"}}
	if _, err := svc.Select(ctx, recheckCode, picked); err != nil {
		t.Fatal(err)
	}
	res := svc.Recheck(ctx, recheckCode)
	if len(res.Candidates) != 1 || res.Candidates[0].Provider != lookup.SelectionProvider || res.Candidates[0].Title.Name != "Picked" {
		t.Fatalf("recheck result = %+v, want the selection", res)
	}
	if got := p.fetchCount(); got != 0 {
		t.Fatalf("recheck with a selection made %d fetches, want 0", got)
	}
}

func TestLookupHandlerRecheck(t *testing.T) {
	tests := []struct {
		query       string
		wantFetches int
	}{
		{query: "", wantFetches: 0},
		{query: "?recheck=0", wantFetches: 0},
		{query: "?recheck=true", wantFetches: 0},
		{query: "?recheck=1", wantFetches: 1},
	}
	for _, tt := range tests {
		t.Run("query "+tt.query, func(t *testing.T) {
			svc, p, cache := recheckService(t)
			cache.seed("discogs", recheckCode, missAnswer, 2*time.Hour)
			h := lookup.NewHandler(svc, discard())
			srv := httptest.NewServer(server.New(server.Config{}, discard(), server.Deps{API: []server.Mounter{h}}))
			t.Cleanup(srv.Close)

			code, body := get(t, srv.URL+"/api/lookup/"+recheckCode+tt.query)
			if code != http.StatusOK {
				t.Fatalf("lookup = %d %s", code, body)
			}
			if got := p.fetchCount(); got != tt.wantFetches {
				t.Fatalf("fetches = %d, want %d", got, tt.wantFetches)
			}
		})
	}
}
