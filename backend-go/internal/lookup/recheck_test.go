package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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

// seedMiss stores the provider's "no match" answer for recheckCode as if it
// was fetched age ago.
func (c *clockCache) seedMiss(provider string, age time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[provider+"/"+recheckCode] = clockEntry{raw: json.RawMessage(missAnswer), fetchedAt: c.now.Add(-age)}
}

func (c *clockCache) clock() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clockCache) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
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
	svc, cache := serviceWith(t, p)
	return svc, p, cache
}

// serviceWith registers p with a week's TTL over a cache and service that
// share one clock.
func serviceWith(t *testing.T, p lookup.Provider) (*lookup.Service, *clockCache) {
	t.Helper()
	r := &lookup.Registry{}
	if err := r.Register(p, weekTTL); err != nil {
		t.Fatal(err)
	}
	cache := newClockCache()
	svc := lookup.NewService(r, cache, discard())
	lookup.SetClock(svc, cache.clock)
	return svc, cache
}

// partialLearned's every source but one fails: its answer is a miss
// together with the failure.
type partialLearned struct {
	learnedProvider
}

func (p *partialLearned) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	if _, err := p.learnedProvider.Fetch(ctx, code); err != nil {
		return nil, err
	}
	return nil, &lookup.PartialError{Raw: json.RawMessage(missAnswer), Err: errors.New("one source down")}
}

// gatedProvider reports each Fetch on started; the first answers once
// release closes, later ones at once.
type gatedProvider struct {
	learnedProvider
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (p *gatedProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	p.started <- struct{}{}
	if p.calls.Add(1) == 1 {
		<-p.release
	}
	return p.learnedProvider.Fetch(ctx, code)
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
			cache.seedMiss("discogs", tt.cachedAge)
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
	cache.seedMiss("discogs", 2*time.Hour)

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
	// The fresh answer replaced the cached miss: a plain lookup serves it.
	if res := svc.Lookup(ctx, recheckCode); len(res.Candidates) != 1 || p.fetchCount() != 1 {
		t.Fatalf("lookup after recheck: %+v, fetches %d", res, p.fetchCount())
	}

	cache.advance(lookup.RecheckAfter)
	svc.Recheck(ctx, recheckCode)
	if got := p.fetchCount(); got != 2 {
		t.Fatalf("recheck an hour after the fresh answer made %d fetches in all, want 2", got)
	}
}

func TestRecheckFloorHoldsAfterAFailure(t *testing.T) {
	type counted interface {
		lookup.Provider
		fetchCount() int
	}
	tests := []struct {
		name string
		p    counted
	}{
		{name: "fetch fails", p: &learnedProvider{fakeProvider{name: "discogs", fetchErr: errors.New("timeout")}}},
		{name: "fetch answers in part", p: &partialLearned{learnedProvider{fakeProvider{name: "discogs"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, cache := serviceWith(t, tt.p)
			ctx := context.Background()
			cache.seedMiss("discogs", 2*time.Hour)

			if res := svc.Recheck(ctx, recheckCode); len(res.Errors) != 1 {
				t.Fatalf("first recheck must report the failure: %+v", res)
			}
			for range 2 {
				cache.advance(10 * time.Minute)
				if res := svc.Recheck(ctx, recheckCode); len(res.Errors) != 0 || len(res.Candidates) != 0 {
					t.Fatalf("recheck within the hour must serve the cached miss: %+v", res)
				}
			}
			if got := tt.p.fetchCount(); got != 1 {
				t.Fatalf("three rechecks within an hour made %d fetches, want 1", got)
			}

			cache.advance(lookup.RecheckAfter)
			svc.Recheck(ctx, recheckCode)
			if got := tt.p.fetchCount(); got != 2 {
				t.Fatalf("recheck an hour after the failed one made %d fetches in all, want 2", got)
			}
		})
	}
}

func TestRecheckInFlightServesTheCache(t *testing.T) {
	p := &gatedProvider{
		learnedProvider: learnedProvider{fakeProvider{name: "discogs", confidence: 0.9}},
		started:         make(chan struct{}, 2),
		release:         make(chan struct{}),
	}
	svc, cache := serviceWith(t, p)
	ctx := context.Background()
	cache.seedMiss("discogs", 2*time.Hour)

	first := make(chan lookup.Result)
	go func() { first <- svc.Recheck(ctx, recheckCode) }()
	<-p.started
	if res := svc.Recheck(ctx, recheckCode); len(res.Candidates) != 0 || len(res.Errors) != 0 {
		t.Fatalf("second recheck while the first is in flight = %+v, want the cached miss", res)
	}
	if n := len(p.started); n != 0 {
		t.Fatalf("second recheck fetched too (%d more)", n)
	}
	close(p.release)
	if res := <-first; len(res.Candidates) != 1 {
		t.Fatalf("first recheck = %+v", res)
	}
	if res := svc.Lookup(ctx, recheckCode); len(res.Candidates) != 1 || p.fetchCount() != 1 {
		t.Fatalf("lookup after the recheck = %+v, fetches %d", res, p.fetchCount())
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
	lookup.SetClock(svc, cache.clock)
	cache.seedMiss("upc", 20*time.Minute)
	svc.Recheck(context.Background(), recheckCode)
	if got := p.fetchCount(); got != 1 {
		t.Fatalf("recheck past a 10-minute TTL made %d fetches, want 1", got)
	}
}

func TestRecheckAnswersWithTheSelectionFirst(t *testing.T) {
	svc, p, cache := recheckService(t)
	ctx := context.Background()
	cache.seedMiss("discogs", 2*time.Hour)
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
			cache.seedMiss("discogs", 2*time.Hour)
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
