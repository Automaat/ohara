package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
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
	var out []string
	for _, m := range sourceSeries.FindAllStringSubmatch(scrape(t), -1) {
		if strings.HasPrefix(m[1], prefix) && !slices.Contains(out, m[2]) {
			out = append(out, m[2])
		}
	}
	slices.Sort(out)
	return out
}

// scrape returns the metrics page.
func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

// sample returns the value of series in a scrape and whether it is there;
// an absent series reads as 0.
func sample(body, series string) (float64, bool) {
	for line := range strings.Lines(body) {
		if rest, ok := strings.CutPrefix(line, series+" "); ok {
			v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
			return v, err == nil
		}
	}
	return 0, false
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
	const errorsSeries = `ohara_source_errors_total{source="skupszop"}`
	const unavailableSeries = `ohara_source_requests_total{source="skupszop",status="http_503"}`
	errorsBefore, _ := sample(scrape(t), errorsSeries)
	r, _, err := newValuation(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newValuation: %v", err)
	}
	built := scrape(t)
	if got, ok := sample(built, errorsSeries); !ok || got != errorsBefore {
		t.Fatalf("after newValuation %s = %v (exported %v), want %v before the first quote", errorsSeries, got, ok, errorsBefore)
	}
	unavailableBefore, _ := sample(built, unavailableSeries)
	if _, err := r.Run(ctx); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "skupszop"}) {
		t.Fatalf("asked %v (paths %v), want olx and skupszop only", got, paths)
	}
	if got, _ := sample(scrape(t), unavailableSeries); got != unavailableBefore+1 {
		t.Fatalf("%s = %v after the run, want %v: the quote must count once as http_503", unavailableSeries, got, unavailableBefore+1)
	}
	checkSources(t)
}
