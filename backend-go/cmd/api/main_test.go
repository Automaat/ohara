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
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
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
		"/olx/":          "../../internal/deals/testdata/olx.json",
		"/bluedvd/":      "../../internal/deals/testdata/bluedvd.json",
		"/swiatksiazki/": "../../internal/deals/testdata/swiatksiazki.json",
		"/pepper":        "../../internal/deals/testdata/pepper.xml",
		"/tezeusz/":      "../../internal/deals/testdata/tezeusz.html",
		"/skupszop":      "",
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
	t.Setenv("OHARA_DEALS_BLUEDVD_URL", u.srv.URL+"/bluedvd/wp-json/wc/store/v1/products")
	t.Setenv("OHARA_BLUEDVD", "true")
	t.Setenv("OHARA_DEALS_SWIATKSIAZKI_URL", u.srv.URL+"/swiatksiazki/graphql")
	t.Setenv("OHARA_SWIATKSIAZKI", "true")
	t.Setenv("OHARA_DEALS_PEPPER_URL", u.srv.URL+"/pepper")
	t.Setenv("OHARA_DEALS_TEZEUSZ_URL", u.srv.URL+"/tezeusz")
	t.Setenv("OHARA_TEZEUSZ", "")
	t.Setenv("OHARA_VALUATION_SKUPSZOP_URL", u.srv.URL+"/skupszop")
	t.Setenv("OHARA_DEALS_DELAY", "1s")
	t.Setenv("OHARA_DEALS_CANARY_QUERY", "dvd")
	t.Setenv("OHARA_DISCOGS_TOKEN", "")
	t.Setenv("OHARA_DISCOGS_SELLERS", "heyjoerecords")
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
		if !slices.Contains([]string{"bluedvd", "olx", "pepper", "swiatksiazki", "tezeusz", "skupszop"}, s) {
			t.Errorf("metrics name source %q", s)
		}
	}
}

func TestHunterAsksOnlyItsSources(t *testing.T) {
	pool := dbtest.Migrated(t)
	u := newUpstreams(t)
	h, _, err := newHunter(pool, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newHunter: %v", err)
	}
	res, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "bluedvd", "swiatksiazki", "pepper", "tezeusz"}) {
		t.Fatalf("asked %v (paths %v), want olx, bluedvd, swiatksiazki, pepper and tezeusz only", got, paths)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("errors = %q", res.Errors)
	}
	if got := metricSources(t, "ohara_source_canary_ok"); !slices.Equal(got, []string{"bluedvd", "olx", "pepper", "swiatksiazki", "tezeusz"}) {
		t.Fatalf("canary series for %v, want bluedvd, olx, pepper, swiatksiazki and tezeusz only", got)
	}
	if got, ok := sample(scrape(t), `ohara_source_canary_ok{source="swiatksiazki"}`); !ok || got != 1 {
		t.Fatalf("swiatksiazki canary = %v (exported %v), want 1", got, ok)
	}
	if got, ok := sample(scrape(t), `ohara_source_canary_ok{source="bluedvd"}`); !ok || got != 1 {
		t.Fatalf("bluedvd canary = %v (exported %v), want 1", got, ok)
	}
	if v, _ := sample(scrape(t), `ohara_source_canary_ok{source="tezeusz"}`); v != 1 {
		t.Fatalf("tezeusz canary = %v, want 1", v)
	}
	checkSources(t)
}

func TestHunterWithoutTezeusz(t *testing.T) {
	pool := dbtest.Migrated(t)
	u := newUpstreams(t)
	t.Setenv("OHARA_TEZEUSZ", "false")
	h, _, err := newHunter(pool, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newHunter: %v", err)
	}
	if _, err := h.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "bluedvd", "swiatksiazki", "pepper"}) {
		t.Fatalf("asked %v (paths %v), want olx, bluedvd, swiatksiazki and pepper only", got, paths)
	}
}

func TestHunterLeavesBlueDVDOutWhenTurnedOff(t *testing.T) {
	pool := dbtest.Migrated(t)
	u := newUpstreams(t)
	t.Setenv("OHARA_BLUEDVD", "false")
	h, _, err := newHunter(pool, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newHunter: %v", err)
	}
	if _, err := h.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "swiatksiazki", "pepper", "tezeusz"}) {
		t.Fatalf("asked %v (paths %v), want olx, swiatksiazki, pepper and tezeusz only", got, paths)
	}
}

func TestHunterLeavesSwiatKsiazkiOutWhenTurnedOff(t *testing.T) {
	pool := dbtest.Migrated(t)
	u := newUpstreams(t)
	t.Setenv("OHARA_SWIATKSIAZKI", "false")
	h, _, err := newHunter(pool, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newHunter: %v", err)
	}
	if _, err := h.Run(context.Background()); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, paths := u.asked(); !slices.Equal(got, []string{"olx", "bluedvd", "pepper", "tezeusz"}) {
		t.Fatalf("asked %v (paths %v), want olx, bluedvd, pepper and tezeusz only", got, paths)
	}
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
	r, _, err := newValuation(pool, deals.NewPacer(0).Lane(), slog.New(slog.DiscardHandler))
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

func TestHuntAndValuationSpaceTheirRequestsToOneSite(t *testing.T) {
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
	newUpstreams(t)
	sent := &sendTimes{base: http.DefaultTransport}
	http.DefaultTransport = sent
	t.Cleanup(func() { http.DefaultTransport = sent.base })
	h, _, r, _, err := newRuns(pool, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("newRuns: %v", err)
	}
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := h.Run(ctx); err != nil {
			t.Errorf("hunt: %v", err)
		}
	})
	wg.Go(func() {
		if _, err := r.Run(ctx); err != nil {
			t.Errorf("valuation: %v", err)
		}
	})
	wg.Wait()

	olx := sent.olx()
	if len(olx) != 2 {
		t.Fatalf("OLX asked %d times, want the hunt's canary and the valuation's book", len(olx))
	}
	slices.SortFunc(olx, func(a, b time.Time) int { return a.Compare(b) })
	if gap := olx[1].Sub(olx[0]); gap < 500*time.Millisecond {
		t.Fatalf("OLX asked twice %v apart, want at least half of the 1s source delay", gap)
	}
}

// sendTimes stands in for http.DefaultTransport under the paced transport
// and keeps when each request to OLX was handed on to be sent. The pacer
// spaces requests by when they are sent, not by when the test server's
// handler sees them, so arrival times can come out a little short of the
// gap while send times cannot.
type sendTimes struct {
	base  http.RoundTripper
	mu    sync.Mutex
	times []time.Time
}

func (s *sendTimes) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Path, "/olx/") {
		s.mu.Lock()
		s.times = append(s.times, time.Now())
		s.mu.Unlock()
	}
	return s.base.RoundTrip(req)
}

func (s *sendTimes) olx() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.times)
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
	runner, _, err := newValuation(nil, deals.NewPacer(0).Lane(), logger)
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
