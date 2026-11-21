package movie

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// slowSearcher is TMDB taking delay per search; it finds "Kursk" only in
// the search without a year.
type slowSearcher struct{ delay time.Duration }

func (s slowSearcher) Search(ctx context.Context, q string, year *int) ([]TMDBMovie, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
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

	tests := []struct {
		name     string
		slow     NameSource
		failed   string
		delay    time.Duration
		maxTaken time.Duration
	}{
		{name: "fast tmdb", slow: upcitemdb, failed: "upcitemdb", maxTaken: 5 * time.Second},
		{name: "tmdb at 1.2 s per search", slow: upcitemdb, failed: "upcitemdb", delay: 1200 * time.Millisecond, maxTaken: 10 * time.Second},
		{name: "source ignoring its context", slow: stuckSource{release: release}, failed: "stuck", maxTaken: 5 * time.Second},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			catalog := fakeSource{name: "allegro", products: []Product{{Source: "allegro", Name: "Kursk (2019) [Blu-ray]"}}}
			p := &Provider{Sources: []NameSource{catalog, tc.slow}, TMDB: slowSearcher{delay: tc.delay}}
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
			if len(res.Errors) != 1 || res.Errors[0].Provider != "movie" || !strings.Contains(res.Errors[0].Error, tc.failed+":") {
				t.Fatalf("errors = %+v, want one naming %s", res.Errors, tc.failed)
			}
			cache.mu.Lock()
			defer cache.mu.Unlock()
			if cache.puts != 0 {
				t.Fatalf("partial answer cached %d times", cache.puts)
			}
		})
	}
}
