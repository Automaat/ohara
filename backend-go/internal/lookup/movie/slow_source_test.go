package movie

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// slowSearcher is TMDB taking delay per search; it finds "Kursk" only in
// the search without a year. err fails every search.
type slowSearcher struct {
	delay time.Duration
	err   error
}

func (s slowSearcher) Search(ctx context.Context, q string, year *int) ([]TMDBMovie, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	if s.err != nil {
		return nil, s.err
	}
	if q == "Kursk" && year == nil {
		return []TMDBMovie{{ID: 429415, Title: "Kursk", OriginalTitle: "Kursk", ReleaseDate: "2018-11-07"}}, nil
	}
	return []TMDBMovie{}, nil
}

// stuckSource ignores its context and answers only when release closes,
// like a source waiting on a lock another lookup holds.
type stuckSource struct{ release <-chan struct{} }

func (stuckSource) Name() string { return "stuck" }

func (s stuckSource) Products(context.Context, string) ([]Product, error) {
	<-s.release
	return []Product{}, nil
}

// namedSource names Kursk under its own name, after delay.
type namedSource struct {
	name  string
	delay time.Duration
}

func (s namedSource) Name() string { return s.name }

func (s namedSource) Products(ctx context.Context, _ string) ([]Product, error) {
	select {
	case <-time.After(s.delay):
		return []Product{{Source: s.name, Name: "Kursk (2019) [Blu-ray]"}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// countingCache records every stored answer.
type countingCache struct {
	mu   sync.Mutex
	puts int
}

func (*countingCache) Get(context.Context, string, string, time.Duration) (json.RawMessage, bool, error) {
	return nil, false, nil
}

func (c *countingCache) Put(context.Context, string, string, json.RawMessage) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.puts++
	return nil
}

// TestHangingSourceLeavesTMDBTime runs the lookup with its real 10 s
// budget and the production 8 s client timeout: a catalog that never
// answers must not hold the lookup until the client gives up.
func TestHangingSourceLeavesTMDBTime(t *testing.T) {
	hanging := serve(t, func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	upcitemdb := UPCitemdb{Client: &http.Client{Timeout: 8 * time.Second}, BaseURL: hanging.URL}
	catalog := namedSource{name: "allegro"}
	tmdbDown := errors.New("tmdb: service unavailable")

	tests := []struct {
		name       string
		sources    []NameSource
		fallbacks  []NameSource
		tmdb       slowSearcher
		failed     []string
		candidates int
		maxTaken   time.Duration
	}{
		{name: "fast tmdb", sources: []NameSource{catalog, upcitemdb}, failed: []string{"upcitemdb"}, candidates: 1, maxTaken: 5 * time.Second},
		{name: "tmdb at 1.2 s per search", sources: []NameSource{catalog, upcitemdb}, tmdb: slowSearcher{delay: 1200 * time.Millisecond}, failed: []string{"upcitemdb"}, candidates: 1, maxTaken: 10 * time.Second},
		{name: "source ignoring its context", sources: []NameSource{catalog, stuckSource{release: release}}, failed: []string{"stuck"}, candidates: 1, maxTaken: 5 * time.Second},
		{name: "hanging source, fallback names it", sources: []NameSource{upcitemdb}, fallbacks: []NameSource{namedSource{name: "fallback"}}, tmdb: slowSearcher{delay: 1200 * time.Millisecond}, failed: []string{"upcitemdb"}, candidates: 1, maxTaken: 10 * time.Second},
		{name: "hanging source, slow fallback", sources: []NameSource{upcitemdb}, fallbacks: []NameSource{namedSource{name: "fallback", delay: 3 * time.Second}}, failed: []string{"upcitemdb"}, candidates: 1, maxTaken: 10 * time.Second},
		{name: "hanging source, tmdb down", sources: []NameSource{catalog, upcitemdb}, tmdb: slowSearcher{err: tmdbDown}, failed: []string{"upcitemdb", "tmdb"}, maxTaken: 5 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{Sources: tc.sources, Fallbacks: tc.fallbacks, TMDB: tc.tmdb}
			registry := &lookup.Registry{}
			if err := registry.Register(p, time.Hour); err != nil {
				t.Fatal(err)
			}
			cache := &countingCache{}
			svc := lookup.NewService(registry, cache, slog.New(slog.DiscardHandler))

			start := time.Now()
			res := svc.Lookup(context.Background(), "5906190326195")
			taken := time.Since(start)

			if taken > tc.maxTaken {
				t.Errorf("lookup took %v, want at most %v", taken, tc.maxTaken)
			}
			if len(res.Candidates) != tc.candidates || (tc.candidates > 0 && res.Candidates[0].Title.Name != "Kursk") {
				t.Fatalf("candidates = %+v, errors = %+v", res.Candidates, res.Errors)
			}
			if len(res.Errors) != 1 || res.Errors[0].Provider != "movie" {
				t.Fatalf("errors = %+v, want one from movie", res.Errors)
			}
			for _, name := range tc.failed {
				if !strings.Contains(res.Errors[0].Error, name+":") {
					t.Errorf("error %q does not name %s", res.Errors[0].Error, name)
				}
			}
			cache.mu.Lock()
			defer cache.mu.Unlock()
			if cache.puts != 0 {
				t.Fatalf("answer cached %d times", cache.puts)
			}
		})
	}
}

// emptySource answers with no product after delay, or fails when its
// context ends first.
type emptySource struct {
	name  string
	delay time.Duration
}

func (s emptySource) Name() string { return s.name }

func (s emptySource) Products(ctx context.Context, _ string) ([]Product, error) {
	select {
	case <-time.After(s.delay):
		return []Product{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// TestSlowAllegroMissLeavesUPCitemdbTime runs the lookup with its real
// limits: Allegro answering nothing after 6 s still leaves UPCitemdb,
// answering after 5 s, and TMDB, taking 2.4 s for its two searches, the
// time to resolve the barcode, past the 10 s other providers get. UPCitemdb
// is not asked when Allegro names a searchable title.
func TestSlowAllegroMissLeavesUPCitemdbTime(t *testing.T) {
	tests := []struct {
		name     string
		allegro  NameSource
		failed   []string
		asked    int
		cached   int
		maxTaken time.Duration
	}{
		{name: "slow miss", allegro: emptySource{name: "allegro", delay: 6 * time.Second}, failed: []string{"allegro"}, asked: 1, maxTaken: 13 * time.Second},
		{name: "slow hit", allegro: namedSource{name: "allegro", delay: 3 * time.Second}, asked: 0, cached: 1, maxTaken: 7 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			asked := 0
			srv := serve(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				asked++
				mu.Unlock()
				select {
				case <-time.After(5 * time.Second):
				case <-r.Context().Done():
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"code":"OK","total":1,"items":[{"title":"Kursk (2019) [Blu-ray]","category":"Media > DVDs & Videos"}]}`))
			})
			upcitemdb := UPCitemdb{Client: &http.Client{Timeout: 8 * time.Second}, BaseURL: srv.URL}
			p := &Provider{Sources: []NameSource{tc.allegro}, Fallbacks: []NameSource{upcitemdb}, TMDB: slowSearcher{delay: 1200 * time.Millisecond}}
			registry := &lookup.Registry{}
			if err := registry.Register(p, time.Hour); err != nil {
				t.Fatal(err)
			}
			cache := &countingCache{}
			svc := lookup.NewService(registry, cache, slog.New(slog.DiscardHandler))

			start := time.Now()
			res := svc.Lookup(context.Background(), "5906190326195")
			taken := time.Since(start)

			if taken > tc.maxTaken {
				t.Errorf("lookup took %v, want at most %v", taken, tc.maxTaken)
			}
			if len(res.Candidates) != 1 || res.Candidates[0].Title.Name != "Kursk" {
				t.Fatalf("candidates = %+v, errors = %+v", res.Candidates, res.Errors)
			}
			if len(res.Errors) != len(tc.failed) {
				t.Fatalf("errors = %+v, want %d", res.Errors, len(tc.failed))
			}
			for _, name := range tc.failed {
				if res.Errors[0].Provider != "movie" || !strings.Contains(res.Errors[0].Error, name+":") {
					t.Errorf("error %+v does not name %s", res.Errors[0], name)
				}
			}
			mu.Lock()
			if asked != tc.asked {
				t.Errorf("upcitemdb asked %d times, want %d", asked, tc.asked)
			}
			mu.Unlock()
			cache.mu.Lock()
			defer cache.mu.Unlock()
			if cache.puts != tc.cached {
				t.Fatalf("answer cached %d times, want %d", cache.puts, tc.cached)
			}
		})
	}
}

// TestAllegroRefreshOutlivesTheSourceTimeout: Allegro retires the old
// refresh token once it answers a refresh, so a refresh slower than the
// source timeout must still store the new token.
func TestAllegroRefreshOutlivesTheSourceTimeout(t *testing.T) {
	now := time.Now()
	store := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(-time.Minute)}}
	srv := &allegroServer{}
	handler := srv.handler(t)
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/oauth/token" {
			time.Sleep(sourceTimeout + 200*time.Millisecond)
		}
		handler(w, r)
	})
	a := &Allegro{
		ClientID: "id", ClientSecret: "secret", Store: store, Client: s.Client(), APIBase: s.URL, AuthBase: s.URL,
		Now: func() time.Time { return now },
	}
	p := &Provider{Sources: []NameSource{a}, TMDB: &fakeSearcher{}}

	_, err := p.Fetch(context.Background(), "5906190326195")
	if err == nil || !strings.Contains(err.Error(), "allegro:") {
		t.Fatalf("err = %v, want the timed-out allegro named", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		store.mu.Lock()
		refresh := store.token.Refresh
		store.mu.Unlock()
		if refresh == refreshFor(1) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("stored refresh token %q, want the rotated %q", refresh, refreshFor(1))
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := p.Fetch(context.Background(), "5906190326195"); err != nil {
		t.Fatalf("next lookup: %v", err)
	}
}

// lateLimitSource reports a rate limit only after the source timeout.
type lateLimitSource struct{ reset time.Time }

func (lateLimitSource) Name() string { return "upcitemdb" }

func (s lateLimitSource) Products(context.Context, string) ([]Product, error) {
	time.Sleep(sourceTimeout + 200*time.Millisecond)
	return nil, &RateLimitError{Reset: s.reset}
}

// TestLateRateLimitStillPausesTheSource: a 429 that arrives after the
// lookup stopped waiting still pauses the source until its reset.
func TestLateRateLimitStillPausesTheSource(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	p := &Provider{Sources: []NameSource{lateLimitSource{reset: reset}}, TMDB: &fakeSearcher{}}
	if _, err := p.Fetch(context.Background(), "5906190326195"); err == nil {
		t.Fatal("want the timed-out source to fail the fetch")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if until, ok := p.pausedUntil("upcitemdb"); ok {
			if !until.Equal(reset) {
				t.Fatalf("paused until %s, want %s", until, reset)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("source not paused after its late 429")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
