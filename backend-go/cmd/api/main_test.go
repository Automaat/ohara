package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/lookup/music"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

// upstreams stands in for every outside source the deal hunter and the
// valuation ask, serving recorded answers under one path prefix per source
// and keeping each request's path.
type upstreams struct {
	mu    sync.Mutex
	paths []string
	srv   *httptest.Server
}

func newUpstreams(t *testing.T) *upstreams {
	t.Helper()
	fixtures := map[string]string{
		"/olx/":     "../../internal/deals/testdata/olx.json",
		"/pepper":   "../../internal/deals/testdata/pepper.xml",
		"/skupszop": "",
	}
	u := &upstreams{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u.mu.Lock()
		u.paths = append(u.paths, r.URL.Path)
		u.mu.Unlock()
		for prefix, file := range fixtures {
			if !strings.HasPrefix(r.URL.Path, prefix) {
				continue
			}
			if file == "" {
				http.Error(w, "unavailable", http.StatusServiceUnavailable)
				return
			}
			b, err := os.ReadFile(file)
			if err != nil {
				t.Errorf("fixture: %v", err)
			}
			_, _ = w.Write(b)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(u.srv.Close)
	t.Setenv("OHARA_DEALS_OLX_URL", u.srv.URL+"/olx/")
	t.Setenv("OHARA_DEALS_PEPPER_URL", u.srv.URL+"/pepper")
	t.Setenv("OHARA_VALUATION_SKUPSZOP_URL", u.srv.URL+"/skupszop")
	t.Setenv("OHARA_DEALS_DELAY", "1s")
	t.Setenv("OHARA_DEALS_CANARY_QUERY", "dvd")
	t.Setenv("OHARA_DISCOGS_TOKEN", "")
	t.Setenv("OHARA_TELEGRAM_BOT_TOKEN", "")
	t.Setenv("OHARA_TELEGRAM_CHAT_ID", "")
	return u
}

// asked returns the first path segment of every request, in order of first
// request, and every path requested.
func (u *upstreams) asked() ([]string, []string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	var segments []string
	for _, p := range u.paths {
		seg, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/")
		if !slices.Contains(segments, seg) {
			segments = append(segments, seg)
		}
	}
	return segments, slices.Clone(u.paths)
}

var sourceSeries = regexp.MustCompile(`(?m)^(ohara_source_[a-z_]+)\{[^}]*\bsource="([^"]*)"`)

// metricSources returns the sources named by the ohara_source_* series whose
// name starts with prefix, sorted and without repeats. A source the hunter
// or the valuation was built with has series even when the run never
// reached the test server.
func metricSources(t *testing.T, prefix string) []string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	var out []string
	for _, m := range sourceSeries.FindAllStringSubmatch(rec.Body.String(), -1) {
		if strings.HasPrefix(m[1], prefix) && !slices.Contains(out, m[2]) {
			out = append(out, m[2])
		}
	}
	slices.Sort(out)
	return out
}

// checkSources fails unless every source with metrics is one of the
// marketplaces and the buy-back the hunter and the valuation may ask.
func checkSources(t *testing.T) {
	t.Helper()
	for _, s := range metricSources(t, "ohara_source_") {
		if !slices.Contains([]string{"olx", "pepper", "skupszop"}, s) {
			t.Errorf("metrics name source %q", s)
		}
	}
}

func TestHunterAsksOnlyItsSources(t *testing.T) {
	pool := dbtest.Migrated(t)
	u := newUpstreams(t)
	h, _, err := newHunter(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newHunter: %v", err)
	}
	res, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "pepper"}) {
		t.Fatalf("asked %v (paths %v), want olx and pepper only", got, paths)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %q", res.Errors)
	}
	if got := metricSources(t, "ohara_source_canary_ok"); !slices.Equal(got, []string{"olx", "pepper"}) {
		t.Fatalf("canary series for %v, want olx and pepper only", got)
	}
	checkSources(t)
}

func TestValuationAsksOnlyItsSources(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE VALUES (1, 'book', 'Katedra', '{"Jacek Dukaj"}')`,
		`INSERT INTO edition (id, title_id, barcode, binding) OVERRIDING SYSTEM VALUE VALUES (10, 1, '9788308073209', 'paperback')`,
		`INSERT INTO copy (edition_id, status, price_cents) VALUES (10, 'owned', 3000)`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	u := newUpstreams(t)
	r, _, err := newValuation(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newValuation: %v", err)
	}
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "skupszop"}) {
		t.Fatalf("asked %v (paths %v), want olx and skupszop only", got, paths)
	}
	checkSources(t)
}

func TestDiscogsCallersShareOneBudget(t *testing.T) {
	t.Setenv("OHARA_DISCOGS_TOKEN", "secret")
	t.Setenv("OHARA_MUSICBRAINZ", "false")
	logger := slog.New(slog.DiscardHandler)

	search, ok := titleSearchers()["music"].(music.TextSearch)
	if !ok {
		t.Fatalf("title search for music is not music.TextSearch")
	}
	provider := newMusicProvider(nil, logger)
	if provider == nil {
		t.Fatal("no music provider with a Discogs token")
	}
	runner, _, err := newValuation(nil, logger)
	if err != nil {
		t.Fatalf("newValuation: %v", err)
	}
	pricer, ok := runner.Pricer().(valuation.Discogs)
	if !ok {
		t.Fatalf("valuation pricer is %T, want valuation.Discogs", runner.Pricer())
	}
	callers := []struct {
		name     string
		caller   any
		priority discogs.Priority
	}{
		{"title search", search.Discogs, discogs.Interactive},
		{"music provider", provider.Discogs, discogs.Interactive},
		{"valuation", pricer.Market, discogs.Background},
	}
	for _, tc := range callers {
		c, ok := tc.caller.(*discogs.Client)
		if !ok {
			t.Fatalf("%s: Discogs caller is %T, want *discogs.Client", tc.name, tc.caller)
		}
		if c.Limiter == nil || c.Limiter != discogsBudget {
			t.Errorf("%s: limiter %p, want the shared budget %p", tc.name, c.Limiter, discogsBudget)
		}
		if c.HTTP != discogsHTTP {
			t.Errorf("%s: HTTP client is not discogsHTTP, which keeps no connection to resend on", tc.name)
		}
		if c.Priority != tc.priority {
			t.Errorf("%s: priority %d, want %d", tc.name, c.Priority, tc.priority)
		}
	}
}
