package movie_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
)

// catalog is a name source that names the products in names and fails
// barcodes in down.
type catalog struct {
	names map[string]string
	down  map[string]bool
}

func (catalog) Name() string { return "catalog" }

func (c catalog) Products(_ context.Context, code string) ([]movie.Product, error) {
	if c.down[code] {
		return nil, errors.New("catalog: unexpected status 503")
	}
	if n, ok := c.names[code]; ok {
		return []movie.Product{{Source: "catalog", Name: n}}, nil
	}
	return []movie.Product{}, nil
}

// tmdbStub answers every search with the 2018 Kursk.
type tmdbStub struct{}

func (tmdbStub) Search(context.Context, string, *int) ([]movie.TMDBMovie, error) {
	return []movie.TMDBMovie{{ID: 401200, Title: "Kursk", OriginalTitle: "Kursk", ReleaseDate: "2018-12-14"}}, nil
}

// upcServer is a UPCitemdb test double. While limited it answers 429 with
// reset as its X-RateLimit-Reset header (none when empty); otherwise it
// knows no barcode.
type upcServer struct {
	mu      sync.Mutex
	hits    int
	limited bool
	reset   string
	server  *httptest.Server
}

func newUPCServer(t *testing.T) *upcServer {
	t.Helper()
	u := &upcServer{}
	u.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		u.mu.Lock()
		defer u.mu.Unlock()
		u.hits++
		if u.limited {
			if u.reset != "" {
				w.Header().Set("X-RateLimit-Reset", u.reset)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"code":"TOO_FAST"}`))
			return
		}
		writeJSON(w, map[string]any{"code": "OK", "total": 0, "items": []any{}})
	}))
	t.Cleanup(u.server.Close)
	return u
}

func (u *upcServer) limit(reset string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.limited, u.reset = true, reset
}

func (u *upcServer) lift() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.limited = false
}

func (u *upcServer) count() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.hits
}

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}

// limitChain is a lookup over a catalog with UPCitemdb as its fallback.
type limitChain struct {
	svc   *lookup.Service
	cache *memCache
	upc   *upcServer
	clock *clock
}

func newLimitChain(t *testing.T, cat catalog) *limitChain {
	t.Helper()
	upc := newUPCServer(t)
	clk := &clock{now: time.Unix(1_800_000_000, 0)}
	p := &movie.Provider{
		Sources:   []movie.NameSource{cat},
		Fallbacks: []movie.NameSource{movie.UPCitemdb{Client: upc.server.Client(), BaseURL: upc.server.URL}},
		TMDB:      tmdbStub{},
		Now:       clk.Now,
	}
	registry := &lookup.Registry{}
	if err := registry.Register(p, time.Hour); err != nil {
		t.Fatal(err)
	}
	cache := &memCache{}
	return &limitChain{svc: lookup.NewService(registry, cache, slog.New(slog.DiscardHandler)), cache: cache, upc: upc, clock: clk}
}

func (c *limitChain) cached(code string) bool {
	raw, ok, _ := c.cache.Get(context.Background(), "movie", code, time.Hour)
	return ok && raw != nil
}

// Barcodes for the rate-limit tests: the catalog names named, knows
// nothing of unknown and fails on broken.
const (
	namedEAN   = "5906190326195"
	unknownEAN = "5903570157561"
	brokenEAN  = "5900058301379"
)

func limitCatalog() catalog {
	return catalog{names: map[string]string{namedEAN: "Kursk [Blu-ray]"}, down: map[string]bool{brokenEAN: true}}
}

func TestUPCitemdbAskedOnlyWhenTheCatalogNamesNothing(t *testing.T) {
	tests := []struct {
		name, code string
		upcHits    int
	}{
		{name: "catalog names the product", code: namedEAN, upcHits: 0},
		{name: "catalog names nothing", code: unknownEAN, upcHits: 1},
		{name: "catalog down", code: brokenEAN, upcHits: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newLimitChain(t, limitCatalog())
			c.svc.Lookup(context.Background(), tc.code)
			if got := c.upc.count(); got != tc.upcHits {
				t.Fatalf("upcitemdb requests = %d, want %d", got, tc.upcHits)
			}
		})
	}
}

func TestUPCitemdbPausedUntilItsLimitResets(t *testing.T) {
	c := newLimitChain(t, limitCatalog())
	start := c.clock.Now()
	c.upc.limit(strconv.FormatInt(start.Add(60*time.Second).Unix(), 10))

	res := c.svc.Lookup(context.Background(), unknownEAN)
	if c.upc.count() != 1 || !mentionsLimit(res) || c.cached(unknownEAN) {
		t.Fatalf("first lookup: upcitemdb requests %d, result %+v, cached %v", c.upc.count(), res, c.cached(unknownEAN))
	}

	for _, at := range []time.Duration{0, 59 * time.Second} {
		c.clock.set(start.Add(at))
		res := c.svc.Lookup(context.Background(), unknownEAN)
		if c.upc.count() != 1 {
			t.Fatalf("lookup %s after the 429 asked upcitemdb", at)
		}
		if !mentionsLimit(res) || len(res.Candidates) != 0 || c.cached(unknownEAN) {
			t.Fatalf("lookup %s after the 429: result %+v, cached %v", at, res, c.cached(unknownEAN))
		}
	}

	t.Run("a product another catalog names is cached without an error", func(t *testing.T) {
		res := c.svc.Lookup(context.Background(), namedEAN)
		if len(res.Errors) != 0 || len(res.Candidates) == 0 || !c.cached(namedEAN) {
			t.Fatalf("result %+v, cached %v", res, c.cached(namedEAN))
		}
		if c.upc.count() != 1 {
			t.Fatalf("upcitemdb requests = %d", c.upc.count())
		}
	})

	c.upc.lift()
	c.clock.set(start.Add(60 * time.Second))
	res = c.svc.Lookup(context.Background(), unknownEAN)
	if c.upc.count() != 2 {
		t.Fatalf("lookup after the reset: upcitemdb requests = %d, want 2", c.upc.count())
	}
	if len(res.Errors) != 0 || !c.cached(unknownEAN) {
		t.Fatalf("lookup after the reset: result %+v, cached %v", res, c.cached(unknownEAN))
	}
}

func TestUPCitemdbPauseLength(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	unix := func(d time.Duration) string { return strconv.FormatInt(base.Add(d).Unix(), 10) }
	tests := []struct {
		name  string
		reset string
		pause time.Duration
	}{
		{name: "reset a minute ahead", reset: unix(time.Minute), pause: time.Minute},
		{name: "daily reset", reset: unix(9 * time.Hour), pause: 9 * time.Hour},
		{name: "no header", reset: "", pause: time.Hour},
		{name: "unreadable header", reset: "soon", pause: time.Hour},
		{name: "reset already past", reset: unix(-time.Minute), pause: time.Hour},
		{name: "reset beyond a day", reset: unix(72 * time.Hour), pause: 24 * time.Hour},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newLimitChain(t, limitCatalog())
			c.upc.limit(tc.reset)
			c.svc.Lookup(context.Background(), unknownEAN)
			c.upc.lift()
			c.clock.set(base.Add(tc.pause - time.Second))
			c.svc.Lookup(context.Background(), unknownEAN)
			if c.upc.count() != 1 {
				t.Fatalf("asked upcitemdb a second before the pause ends")
			}
			c.clock.set(base.Add(tc.pause))
			c.svc.Lookup(context.Background(), unknownEAN)
			if c.upc.count() != 2 {
				t.Fatalf("did not ask upcitemdb once the pause ended")
			}
		})
	}
}

// mentionsLimit reports whether the lookup failed naming UPCitemdb's rate
// limit.
func mentionsLimit(res lookup.Result) bool {
	for _, e := range res.Errors {
		if strings.Contains(e.Error, "upcitemdb") && strings.Contains(e.Error, "rate limit") {
			return true
		}
	}
	return false
}
