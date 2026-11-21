package deals

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

type fakeSource struct {
	name     string
	perQuery bool
	mu       sync.Mutex
	listings []Listing
	err      error
	queries  []string
}

func (f *fakeSource) Name() string   { return f.name }
func (f *fakeSource) PerQuery() bool { return f.perQuery }

func (f *fakeSource) Search(_ context.Context, query string) ([]Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, query)
	return append([]Listing(nil), f.listings...), f.err
}

func (f *fakeSource) add(l Listing) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listings = append(f.listings, l)
}

type fakeNotifier struct {
	mu   sync.Mutex
	sent []string
	fail int
}

func (f *fakeNotifier) Send(_ context.Context, text string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail > 0 {
		f.fail--
		return errors.New("telegram: status 502: Bad Gateway")
	}
	f.sent = append(f.sent, text)
	return nil
}

func (f *fakeNotifier) messages() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.sent...)
}

// wishlist stores w as a title, an edition and a wishlist copy and returns
// the copy's id.
func wishlist(t *testing.T, pool *pgxpool.Pool, w *Want) int64 {
	t.Helper()
	ctx := context.Background()
	var titleID, editionID, copyID int64
	if err := pool.QueryRow(ctx, `INSERT INTO title (kind, name, polish_title, original_title, authors)
		VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		w.Kind, w.Name, w.PolishTitle, w.OriginalTitle, append([]string{}, w.Authors...)).Scan(&titleID); err != nil {
		t.Fatalf("title: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO edition (title_id, format) VALUES ($1, $2) RETURNING id`,
		titleID, w.Format).Scan(&editionID); err != nil {
		t.Fatalf("edition: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy (edition_id, status, max_price_cents)
		VALUES ($1, 'wishlist', $2) RETURNING id`, editionID, w.MaxPriceCents).Scan(&copyID); err != nil {
		t.Fatalf("copy: %v", err)
	}
	return copyID
}

// wantBluray is the Blade Runner Blu-ray wish with an optional limit.
func wantBluray(maxPrice *int64) *Want {
	w := bladeRunner("bluray")
	w.MaxPriceCents = maxPrice
	return &w
}

func newTestHunter(pool *pgxpool.Pool, notifier Notifier, sources ...Source) *Hunter {
	h := NewHunter(NewStore(pool), sources, notifier, Config{}, slog.New(slog.DiscardHandler))
	h.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return h
}

func olxFixture(t *testing.T) *fakeSource {
	t.Helper()
	listings, err := parseOLX(fixture(t, "olx.json"))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return &fakeSource{name: "olx", perQuery: true, listings: listings}
}

func count(t *testing.T, pool *pgxpool.Pool, query string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), query).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func mustRun(t *testing.T, h *Hunter) RunResult {
	t.Helper()
	res, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

func TestRunAlertsOncePerNewOLXListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	olx := olxFixture(t)
	notifier := &fakeNotifier{}
	h := newTestHunter(pool, notifier, olx, &fakeSource{name: "pepper"})

	res := mustRun(t, h)
	if res.Wants != 1 || res.Queries != 2 || res.Matched != 4 || res.New != 2 || res.Alerts != 1 || res.Sent != 1 {
		t.Fatalf("first run = %+v", res)
	}
	msgs := notifier.messages()
	if len(msgs) != 1 {
		t.Fatalf("first run sent %d messages, want 1", len(msgs))
	}
	for _, part := range []string{
		"🎯 Okazja: Blade Runner · Blu-ray", "45,00 zł (limit 50,00 zł) · OLX · używany",
		"Blade Runner The Final Cut BluRay", "https://www.olx.pl/d/oferta/",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE source = 'olx'`); n != 2 {
		t.Fatalf("stored %d observations, want the 2 matching listings", n)
	}

	if res := mustRun(t, h); res.New != 0 || res.Sent != 0 {
		t.Fatalf("repeat run = %+v, want nothing new", res)
	}
	olx.add(Listing{Source: "olx", ExternalID: "999", Title: "Łowca androidów Blu-ray PL", URL: "https://www.olx.pl/d/oferta/z-ID999.html", PriceCents: 4000})
	olx.add(Listing{Source: "olx", ExternalID: "998", Title: "Łowca androidów Blu-ray drogi", URL: "https://www.olx.pl/d/oferta/z-ID998.html", PriceCents: 9000})
	if res := mustRun(t, h); res.New != 2 || res.Alerts != 1 || res.Sent != 1 {
		t.Fatalf("run with new listings = %+v", res)
	}
	if got := len(notifier.messages()); got != 2 {
		t.Fatalf("sent %d messages in total, want 2", got)
	}
}

func TestRunRetriesFailedAlerts(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, olxFixture(t))

	if res := mustRun(t, h); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("failing run = %+v", res)
	}
	obs, err := h.store.Recent(context.Background(), RecentFilter{Limit: 10})
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	var pending int
	for _, o := range obs {
		if o.Alert == AlertPending && o.AlertAttempts == 1 && strings.Contains(o.AlertError, "502") {
			pending++
		}
	}
	if pending != 1 {
		t.Fatalf("observations = %+v, want one pending alert after a failure", obs)
	}
	if res := mustRun(t, h); res.Sent != 1 {
		t.Fatalf("retry run = %+v, want the alert delivered", res)
	}
	if res := mustRun(t, h); res.Sent != 0 {
		t.Fatalf("third run = %+v, want no resend", res)
	}
}

func TestRunGivesUpAfterMaxAttempts(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	notifier := &fakeNotifier{fail: MaxAttempts + 1}
	h := newTestHunter(pool, notifier, olxFixture(t))
	for range MaxAttempts + 1 {
		mustRun(t, h)
	}
	if n := count(t, pool, `SELECT count(*) FROM deal_alert WHERE attempts = 5 AND sent_at IS NULL`); n != 1 {
		t.Fatalf("alerts at the attempt limit = %d, want 1", n)
	}
	if notifier.fail != 1 || len(notifier.messages()) != 0 {
		t.Fatalf("notifier tried %d times past the limit", MaxAttempts+1-notifier.fail)
	}
	obs, err := h.store.Recent(context.Background(), RecentFilter{Limit: 10})
	if err != nil {
		t.Fatalf("recent: %v", err)
	}
	failed := 0
	for _, o := range obs {
		if o.Alert == AlertFailed {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed alerts = %d, want 1", failed)
	}
}

func TestRunWithoutLimitOrNotifierOnlyRecords(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(nil))
	if res := mustRun(t, newTestHunter(pool, &fakeNotifier{}, olxFixture(t))); res.New != 2 || res.Alerts != 0 {
		t.Fatalf("no-limit run = %+v", res)
	}
	pool2 := dbtest.Migrated(t)
	wishlist(t, pool2, wantBluray(new(int64(5000))))
	if res := mustRun(t, newTestHunter(pool2, nil, olxFixture(t))); res.New != 2 || res.Alerts != 0 || res.Sent != 0 {
		t.Fatalf("no-notifier run = %+v", res)
	}
	if n := count(t, pool2, `SELECT count(*) FROM deal_alert`); n != 0 {
		t.Fatalf("queued %d alerts without a notifier", n)
	}
}

func TestRunKeepsGoingAfterSourceErrors(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(nil))
	broken := &fakeSource{name: "pepper", err: errors.New("pepper: status 503")}
	res := mustRun(t, newTestHunter(pool, nil, broken, olxFixture(t)))
	if len(res.Errors) != 1 || res.Errors[0] != "pepper: status 503" || res.New != 2 {
		t.Fatalf("run = %+v", res)
	}
	if len(broken.queries) != 1 || broken.queries[0] != "" {
		t.Fatalf("feed source queries = %q, want one empty query", broken.queries)
	}
}

func TestPendingAlertsSkipCopiesNoLongerWanted(t *testing.T) {
	pool := dbtest.Migrated(t)
	copyID := wishlist(t, pool, wantBluray(new(int64(5000))))
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, olxFixture(t))
	mustRun(t, h)
	if _, err := pool.Exec(context.Background(),
		`UPDATE copy SET status = 'owned', max_price_cents = NULL WHERE id = $1`, copyID); err != nil {
		t.Fatalf("buy: %v", err)
	}
	if res := mustRun(t, h); res.Sent != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("run after buying = %+v, want no alert", res)
	}
}

func TestPendingAlertsSkipListingsAboveLoweredLimit(t *testing.T) {
	pool := dbtest.Migrated(t)
	copyID := wishlist(t, pool, wantBluray(new(int64(5000))))
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, olxFixture(t))
	if res := mustRun(t, h); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("failing run = %+v", res)
	}
	if _, err := pool.Exec(context.Background(),
		`UPDATE copy SET max_price_cents = 1000 WHERE id = $1`, copyID); err != nil {
		t.Fatalf("lower limit: %v", err)
	}
	if res := mustRun(t, h); res.Sent != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("run after lowering the limit = %+v, want no alert above 10 zł", res)
	}
}

// cancellingNotifier delivers the message, then cancels the run, as a
// shutdown arriving mid-send does.
type cancellingNotifier struct {
	fakeNotifier
	cancel context.CancelFunc
}

func (c *cancellingNotifier) Send(ctx context.Context, text string) error {
	err := c.fakeNotifier.Send(ctx, text)
	c.cancel()
	return err
}

func TestRunRecordsAlertSentBeforeCancellation(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	notifier := &cancellingNotifier{cancel: cancel}
	h := newTestHunter(pool, notifier, olxFixture(t))
	if res, err := h.Run(ctx); (err != nil && !errors.Is(err, context.Canceled)) || res.Sent != 1 {
		t.Fatalf("canceled run = %+v, %v; want the delivered alert recorded and at most the cancellation", res, err)
	}
	if n := count(t, pool, `SELECT count(*) FROM deal_alert WHERE sent_at IS NOT NULL`); n != 1 {
		t.Fatalf("sent alerts = %d, want 1", n)
	}
	notifier.cancel = func() {}
	if res := mustRun(t, h); res.Sent != 0 || len(notifier.messages()) != 1 {
		t.Fatalf("next run = %+v, want no resend", res)
	}
}

func TestRunWithEmptyWishlistAsksNoSource(t *testing.T) {
	pool := dbtest.Migrated(t)
	olx := olxFixture(t)
	if res := mustRun(t, newTestHunter(pool, &fakeNotifier{}, olx)); res.Wants != 0 || len(olx.queries) != 0 {
		t.Fatalf("run = %+v, queries %q", res, olx.queries)
	}
}

func TestRunMatchesBooksOnPepper(t *testing.T) {
	pool := dbtest.Migrated(t)
	book := wiedzmin()
	book.MaxPriceCents = new(int64(3000))
	wishlist(t, pool, &book)
	pepper := &fakeSource{name: "pepper", listings: []Listing{
		{Source: "pepper", ExternalID: "p1", Title: "Ostatnie życzenie, Sapkowski, twarda okładka – 29 zł @ Empik", URL: "https://www.pepper.pl/promocje/p1", PriceCents: 2900},
		{Source: "pepper", ExternalID: "p2", Title: "Ostatnie życzenie Sapkowski sama okładka", URL: "https://www.pepper.pl/promocje/p2", PriceCents: 500},
		{Source: "pepper", ExternalID: "p3", Title: "Krew elfów Sapkowski", URL: "https://www.pepper.pl/promocje/p3", PriceCents: 1500},
	}}
	notifier := &fakeNotifier{}
	res := mustRun(t, newTestHunter(pool, notifier, pepper))
	if res.Matched != 1 || res.New != 1 || res.Sent != 1 {
		t.Fatalf("run = %+v", res)
	}
	if msgs := notifier.messages(); len(msgs) != 1 || !strings.Contains(msgs[0], "29,00 zł (limit 30,00 zł) · Pepper") ||
		strings.Contains(msgs[0], "Blu-ray") {
		t.Fatalf("messages = %q", msgs)
	}
}

func TestRunAlertsOnListingsStoredBefore(t *testing.T) {
	pool := dbtest.Migrated(t)
	copyID := wishlist(t, pool, wantBluray(new(int64(5000))))
	var editionID int64
	if err := pool.QueryRow(context.Background(), `SELECT edition_id FROM copy WHERE id = $1`, copyID).Scan(&editionID); err != nil {
		t.Fatal(err)
	}
	listing := Listing{
		Source: "olx", ExternalID: "777", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/z-ID777.html",
		PriceCents: 6000,
	}
	if _, err := pool.Exec(context.Background(), `INSERT INTO price_observation
			(source, external_id, price_cents, url, edition_id) VALUES ('olx', '777', 4000, $1, $2)`,
		listing.URL, editionID); err != nil {
		t.Fatal(err)
	}
	olx := &fakeSource{name: "olx", perQuery: true, listings: []Listing{listing}}
	notifier := &fakeNotifier{}
	h := newTestHunter(pool, notifier, olx)

	if res := mustRun(t, h); res.New != 0 || res.Alerts != 0 || res.Sent != 0 {
		t.Fatalf("listing above the limit = %+v, want no alert", res)
	}
	if n := count(t, pool, `SELECT price_cents FROM price_observation WHERE external_id = '777'`); n != 6000 {
		t.Fatalf("stored price %d, want the price seen now", n)
	}

	olx.listings[0].PriceCents = 4500
	if res := mustRun(t, h); res.New != 0 || res.Alerts != 1 || res.Sent != 1 {
		t.Fatalf("listing stored earlier, now under the limit = %+v, want one alert", res)
	}
	if res := mustRun(t, h); res.Alerts != 0 || res.Sent != 0 {
		t.Fatalf("repeat run = %+v, want no second alert", res)
	}
	if got := len(notifier.messages()); got != 1 {
		t.Fatalf("sent %d messages, want 1", got)
	}
}

// metric reads one series from /metrics, such as
// `ohara_source_canary_ok{source="olx"}`; the bool is false when it is
// absent.
func metric(t *testing.T, series string) (float64, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody))
	for line := range strings.Lines(rec.Body.String()) {
		key, val, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || key != series {
			continue
		}
		v, err := strconv.ParseFloat(val, 64)
		if err != nil {
			t.Fatal(err)
		}
		return v, true
	}
	return 0, false
}

func mustMetric(t *testing.T, series string) float64 {
	t.Helper()
	v, ok := metric(t, series)
	if !ok {
		t.Fatalf("%s not exported", series)
	}
	return v
}

func newCanaryHunter(pool *pgxpool.Pool, sources ...Source) *Hunter {
	h := NewHunter(NewStore(pool), sources, nil, Config{CanaryQuery: "dvd"}, slog.New(slog.DiscardHandler))
	h.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return h
}

// TestBrokenOLXParserFailsCanary is the issue's acceptance check: an OLX
// response the parser cannot read turns the canary off and counts a parse
// error in the first run, before any wishlist exists.
func TestBrokenOLXParserFailsCanary(t *testing.T) {
	pool := dbtest.Migrated(t)
	var broken atomic.Bool
	broken.Store(true)
	var gotQuery atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.Query().Get("query"))
		if broken.Load() {
			_, _ = w.Write([]byte(`{"offers":[]}`))
			return
		}
		_, _ = w.Write(fixture(t, "olx.json"))
	}))
	t.Cleanup(srv.Close)
	h := newCanaryHunter(pool, OLX{Client: srv.Client(), BaseURL: srv.URL + "/"})

	parseBefore := mustMetric(t, `ohara_source_parse_errors_total{source="olx"}`)
	res := mustRun(t, h)
	if gotQuery.Load() != "dvd" || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "olx") {
		t.Fatalf("broken run = %+v, query %v", res, gotQuery.Load())
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="olx"}`); v != 0 {
		t.Fatalf("canary with broken parser = %v", v)
	}
	if d := mustMetric(t, `ohara_source_parse_errors_total{source="olx"}`) - parseBefore; d != 1 {
		t.Fatalf("parse errors grew by %v", d)
	}
	if v := mustMetric(t, `ohara_source_results{source="olx"}`); v != 0 {
		t.Fatalf("results with broken parser = %v", v)
	}

	broken.Store(false)
	res = mustRun(t, h)
	if len(res.Errors) != 0 || res.Listings != 0 {
		t.Fatalf("fixed run = %+v", res)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="olx"}`); v != 1 {
		t.Fatalf("canary after fix = %v", v)
	}
	if v := mustMetric(t, `ohara_source_results{source="olx"}`); v != 8 {
		t.Fatalf("results after fix = %v", v)
	}
	if v := mustMetric(t, `ohara_source_last_success_timestamp_seconds{source="olx"}`); v < float64(time.Now().Add(-time.Minute).Unix()) {
		t.Fatalf("last success = %v", v)
	}
}

// TestCanaryRunsBesideWishlistQueries checks that canary listings count in
// the results gauge but not in the run's listings, and that an empty feed
// fails its canary.
func TestCanaryRunsBesideWishlistQueries(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(nil))
	query := &fakeSource{name: "canary-query", perQuery: true, listings: []Listing{
		{Source: "canary-query", ExternalID: "1", Title: "Something else DVD", PriceCents: 100},
	}}
	feed := &fakeSource{name: "canary-feed"}
	res := mustRun(t, newCanaryHunter(pool, query, feed))

	if len(query.queries) != 1+res.Queries || query.queries[0] != "dvd" {
		t.Fatalf("query source asked %q for %d wishlist queries", query.queries, res.Queries)
	}
	if len(feed.queries) != 1 || feed.queries[0] != "" {
		t.Fatalf("feed asked %q, want its feed once", feed.queries)
	}
	if res.Listings != res.Queries {
		t.Fatalf("run = %+v", res)
	}
	if v := mustMetric(t, `ohara_source_results{source="canary-query"}`); v != float64(1+res.Queries) {
		t.Fatalf("query results = %v", v)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="canary-query"}`); v != 1 {
		t.Fatalf("query canary = %v", v)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="canary-feed"}`); v != 0 {
		t.Fatalf("empty feed canary = %v", v)
	}
	if !slices.Contains(res.Errors, "canary-feed: canary query found no listings") {
		t.Fatalf("errors = %q", res.Errors)
	}
}

func TestSourceStatusCountsBlocking(t *testing.T) {
	pool := dbtest.Migrated(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	t.Cleanup(srv.Close)
	series := `ohara_source_requests_total{source="pepper",status="http_429"}`
	before, _ := metric(t, series)
	mustRun(t, newCanaryHunter(pool, Pepper{Client: srv.Client(), BaseURL: srv.URL}))
	if d := mustMetric(t, series) - before; d != 1 {
		t.Fatalf("http_429 grew by %v", d)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="pepper"}`); v != 0 {
		t.Fatalf("blocked canary = %v", v)
	}
}

func TestAlertTextNamesTheMedium(t *testing.T) {
	tests := []struct {
		name   string
		format *string
		head   string
	}{
		{"cd", new("cd"), "🎯 Okazja: The Wall · CD"},
		{"vinyl", new("vinyl"), "🎯 Okazja: The Wall · Winyl"},
		{"cassette", new("cassette"), "🎯 Okazja: The Wall · Kaseta"},
		{"movie", new("bluray"), "🎯 Okazja: The Wall · Blu-ray"},
		{"no format", nil, "🎯 Okazja: The Wall"},
		{"unknown format", new("laserdisc"), "🎯 Okazja: The Wall"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := alertText(&Alert{
				Source: "olx", URL: "https://www.olx.pl/d/oferta/the-wall", ListingTitle: "Pink Floyd The Wall",
				PriceCents: 4500, AcquiredAs: new("used"), TitleName: "The Wall", Format: tt.format,
				MaxPriceCents: new(int64(5000)),
			})
			want := strings.Join([]string{
				tt.head, "45,00 zł (limit 50,00 zł) · OLX · używany",
				"Pink Floyd The Wall", "https://www.olx.pl/d/oferta/the-wall",
			}, "\n")
			if got != want {
				t.Errorf("alertText = %q, want %q", got, want)
			}
		})
	}
}

// panickingSource trips an index past the end of a slice on every search,
// like a parser meeting a malformed answer.
type panickingSource struct{ calls atomic.Int32 }

func (*panickingSource) Name() string   { return "olx" }
func (*panickingSource) PerQuery() bool { return true }

func (p *panickingSource) Search(context.Context, string) ([]Listing, error) {
	p.calls.Add(1)
	return []Listing{{Title: third(strings.Fields(""))}}, nil
}

// third is a parser that expects at least three fields.
func third(fields []string) string { return fields[2] }

func TestPanickingRunFailsAndTheScheduleGoesOn(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(nil))
	src := &panickingSource{}
	var logs strings.Builder
	h := NewHunter(NewStore(pool), []Source{src}, nil, Config{Interval: time.Hour},
		slog.New(slog.NewJSONHandler(&logs, nil)))
	var sleeps atomic.Int32
	h.sleep = func(ctx context.Context, _ time.Duration) error {
		if sleeps.Add(1) > 2 {
			return context.Canceled
		}
		return ctx.Err()
	}

	h.Start(t.Context())
	h.Wait()
	if got := src.calls.Load(); got != 2 {
		t.Fatalf("scheduled runs searched %d times, want 2", got)
	}
	st := h.Status()
	if st.Running || st.LastResult == nil || st.LastFinishedAt == nil ||
		!strings.Contains(st.LastResult.Failure, "panic: runtime error: index out of range [2] with length 0") {
		t.Fatalf("status = %+v, result %+v", st, st.LastResult)
	}

	if !h.Trigger() {
		t.Fatal("Trigger after a panicking run = false")
	}
	h.Wait()
	if got := src.calls.Load(); got != 3 {
		t.Fatalf("manual run searched %d times in all, want 3", got)
	}
	if st := h.Status(); st.Running || st.LastResult == nil || st.LastResult.Failure == "" {
		t.Fatalf("status after manual run = %+v", st)
	}
	for _, want := range []string{`"level":"ERROR"`, `"msg":"deal search panicked"`, `"stack":"goroutine `} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, logs.String())
		}
	}
}

// refusingSource answers every query with no listings until its
// refuseFrom-th request, counted over all runs, and with refusal from then
// on.
type refusingSource struct {
	name       string
	refuseFrom int
	refusal    error
	queries    []string
}

func (f *refusingSource) Name() string   { return f.name }
func (f *refusingSource) PerQuery() bool { return true }

func (f *refusingSource) Search(_ context.Context, query string) ([]Listing, error) {
	f.queries = append(f.queries, query)
	if len(f.queries) >= f.refuseFrom {
		return nil, f.refusal
	}
	return nil, nil
}

// fiveWants wishes five DVDs, one query each.
func fiveWants(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	for _, name := range []string{"Alien", "Aliens", "Predator", "Terminator", "Robocop"} {
		wishlist(t, pool, &Want{Kind: "movie", Name: name, Format: new("dvd")})
	}
}

func TestRunStopsAskingASourceThatRefused(t *testing.T) {
	pool := dbtest.Migrated(t)
	fiveWants(t, pool)
	refusing := &refusingSource{name: "refusing-source", refuseFrom: 2, refusal: statusError{http.StatusTooManyRequests}}
	next := &fakeSource{name: "after-refusal", perQuery: true}
	h := newTestHunter(pool, nil, refusing, next)
	pauses := 0
	h.sleep = func(ctx context.Context, _ time.Duration) error {
		pauses++
		return ctx.Err()
	}
	okBefore, _ := metric(t, `ohara_source_requests_total{source="refusing-source",status="ok"}`)
	refusedBefore, _ := metric(t, `ohara_source_requests_total{source="refusing-source",status="http_429"}`)

	res := mustRun(t, h)
	if res.Queries != 5 {
		t.Fatalf("queries = %d, want 5", res.Queries)
	}
	if len(refusing.queries) != 2 {
		t.Fatalf("refusing source asked %q, want 2 queries", refusing.queries)
	}
	if len(next.queries) != 5 {
		t.Fatalf("next source asked %q, want all 5 queries", next.queries)
	}
	want := []string{"refusing-source: refused with status 429, skipped 3 queries"}
	if !slices.Equal(res.Errors, want) {
		t.Fatalf("errors = %q, want %q", res.Errors, want)
	}
	if pauses != 6 {
		t.Fatalf("pauses = %d, want one between each of the 7 requests", pauses)
	}
	if d := mustMetric(t, `ohara_source_requests_total{source="refusing-source",status="ok"}`) - okBefore; d != 1 {
		t.Fatalf("ok requests grew by %v, want 1", d)
	}
	if d := mustMetric(t, `ohara_source_requests_total{source="refusing-source",status="http_429"}`) - refusedBefore; d != 1 {
		t.Fatalf("http_429 requests grew by %v, want 1", d)
	}

	res = mustRun(t, h)
	if len(refusing.queries) != 3 || refusing.queries[2] != refusing.queries[0] {
		t.Fatalf("second run asked %q, want the first query again", refusing.queries[2:])
	}
	want = []string{"refusing-source: refused with status 429, skipped 4 queries"}
	if !slices.Equal(res.Errors, want) {
		t.Fatalf("second run errors = %q, want %q", res.Errors, want)
	}
	if d := mustMetric(t, `ohara_source_requests_total{source="refusing-source",status="http_429"}`) - refusedBefore; d != 2 {
		t.Fatalf("http_429 requests grew by %v over two runs, want 2", d)
	}
}

func TestRunTreatsOnly403And429AsRefusals(t *testing.T) {
	pool := dbtest.Migrated(t)
	fiveWants(t, pool)
	tests := []struct {
		name    string
		err     error
		refused bool
	}{
		{name: "forbidden", err: statusError{http.StatusForbidden}, refused: true},
		{name: "too many requests", err: fmt.Errorf("olx: %w", statusError{http.StatusTooManyRequests}), refused: true},
		{name: "server error", err: statusError{http.StatusServiceUnavailable}},
		{name: "not found", err: statusError{http.StatusNotFound}},
		{name: "timeout", err: context.DeadlineExceeded},
		{name: "parse error", err: parseError{errors.New("olx: decode")}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := &refusingSource{name: "refusal-kind", refuseFrom: 1, refusal: tt.err}
			res := mustRun(t, newTestHunter(pool, nil, src))
			asked := 5
			if tt.refused {
				asked = 1
			}
			if len(src.queries) != asked {
				t.Fatalf("asked %d queries, want %d", len(src.queries), asked)
			}
			if tt.refused != (len(res.Errors) == 1) {
				t.Fatalf("errors = %q", res.Errors)
			}
		})
	}
}

// failThenRefuse answers its first fails queries with a 503 and every
// later one with a 429.
type failThenRefuse struct {
	fails   int
	queries []string
}

func (*failThenRefuse) Name() string   { return "fail-then-refuse" }
func (*failThenRefuse) PerQuery() bool { return true }

func (f *failThenRefuse) Search(_ context.Context, query string) ([]Listing, error) {
	f.queries = append(f.queries, query)
	if len(f.queries) <= f.fails {
		return nil, statusError{http.StatusServiceUnavailable}
	}
	return nil, statusError{http.StatusTooManyRequests}
}

func TestRunKeepsTheRefusalBeyondTheErrorCap(t *testing.T) {
	pool := dbtest.Migrated(t)
	for i := range maxRunErrors + 2 {
		wishlist(t, pool, &Want{Kind: "movie", Name: "Film " + strconv.Itoa(i+1), Format: new("dvd")})
	}
	src := &failThenRefuse{fails: maxRunErrors}
	res := mustRun(t, newTestHunter(pool, nil, src))
	if res.Queries != maxRunErrors+2 || len(src.queries) != maxRunErrors+1 {
		t.Fatalf("asked %d of %d queries, want all but the last", len(src.queries), res.Queries)
	}
	if len(res.Errors) != maxRunErrors+1 {
		t.Fatalf("errors = %d, want %d failures and the refusal", len(res.Errors), maxRunErrors)
	}
	if last := res.Errors[maxRunErrors]; last != "fail-then-refuse: refused with status 429, skipped 1 queries" {
		t.Fatalf("last error = %q", last)
	}
}

// callLog records source requests and notifier sends in the order they
// happen.
type callLog struct {
	mu    sync.Mutex
	calls []string
}

func (c *callLog) add(call string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls = append(c.calls, call)
}

func (c *callLog) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.calls...)
}

type loggedSource struct {
	*fakeSource
	log *callLog
}

func (s loggedSource) Search(ctx context.Context, query string) ([]Listing, error) {
	s.log.add("search " + s.name)
	return s.fakeSource.Search(ctx, query)
}

type loggedNotifier struct {
	fakeNotifier
	log *callLog
}

func (n *loggedNotifier) Send(ctx context.Context, text string) error {
	n.log.add("send")
	return n.fakeNotifier.Send(ctx, text)
}

func TestRunSendsAlertBeforeTheNextRequest(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	log := &callLog{}
	olx := loggedSource{olxFixture(t), log}
	other := loggedSource{&fakeSource{name: "other", perQuery: true}, log}
	notifier := &loggedNotifier{log: log}
	if res := mustRun(t, newTestHunter(pool, notifier, olx, other)); res.Sent != 1 {
		t.Fatalf("run = %+v", res)
	}
	want := []string{"search olx", "send", "search olx", "search other", "search other"}
	if got := log.list(); !slices.Equal(got, want) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
}

func TestRunTriesAFailedAlertOncePerRun(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	notifier := &fakeNotifier{fail: 100}
	other := &fakeSource{name: "other", perQuery: true}
	h := newTestHunter(pool, notifier, olxFixture(t), other)
	if res := mustRun(t, h); res.Alerts != 1 || res.Sent != 0 || len(other.queries) != 2 {
		t.Fatalf("run = %+v, other asked %q", res, other.queries)
	}
	if notifier.fail != 99 {
		t.Fatalf("notifier tried %d times in one run, want 1", 100-notifier.fail)
	}
	if n := count(t, pool, `SELECT attempts FROM deal_alert`); n != 1 {
		t.Fatalf("attempts = %d after one run, want 1", n)
	}
	mustRun(t, h)
	if n := count(t, pool, `SELECT attempts FROM deal_alert`); n != 2 {
		t.Fatalf("attempts = %d after two runs, want 2", n)
	}
}

// slowNotifier takes delay to deliver each message, as a slow Telegram
// answer does.
type slowNotifier struct {
	fakeNotifier
	delay time.Duration
}

func (n *slowNotifier) Send(ctx context.Context, text string) error {
	time.Sleep(n.delay)
	return n.fakeNotifier.Send(ctx, text)
}

// recordWaits makes h's sleeps return at once and keeps their lengths.
func recordWaits(h *Hunter) func() []time.Duration {
	var mu sync.Mutex
	var waits []time.Duration
	h.sleep = func(ctx context.Context, d time.Duration) error {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err()
	}
	return func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Duration(nil), waits...)
	}
}

// pacedFromReturn reports whether a wait began a full telegramPause after a
// send that took delay returned, not after it started.
func pacedFromReturn(waits []time.Duration, delay time.Duration) bool {
	return slices.ContainsFunc(waits, func(d time.Duration) bool {
		return d > telegramPause-delay/2 && d <= telegramPause
	})
}

func TestRunAlertsOnceForAListingInTwoAnswersAndPacesMessages(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	cheap := Listing{Source: "olx", ExternalID: "1", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/z-ID1.html", PriceCents: 4000}
	olx := &fakeSource{name: "olx", perQuery: true, listings: []Listing{cheap}}
	other := &fakeSource{name: "other", perQuery: true, listings: []Listing{{
		Source: "other", ExternalID: "2", Title: "Blade Runner Blu-ray PL", URL: "https://example.com/2", PriceCents: 4500,
	}}}
	const delay = 200 * time.Millisecond
	notifier := &slowNotifier{delay: delay}
	h := newTestHunter(pool, notifier, olx, other)
	waits := recordWaits(h)
	res := mustRun(t, h)
	if len(olx.queries) != 2 || res.Alerts != 2 || res.Sent != 2 {
		t.Fatalf("run = %+v, olx asked %q", res, olx.queries)
	}
	if got := len(notifier.messages()); got != 2 {
		t.Fatalf("sent %d messages, want one per listing", got)
	}
	if !pacedFromReturn(waits(), delay) {
		t.Fatalf("waits = %v, want about telegramPause after the first send returned", waits())
	}
}

func TestRunPacesMessagesAcrossRuns(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	olx := &fakeSource{name: "olx", perQuery: true, listings: []Listing{
		{Source: "olx", ExternalID: "1", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/z-ID1.html", PriceCents: 4000},
	}}
	const delay = 200 * time.Millisecond
	notifier := &slowNotifier{delay: delay}
	h := newTestHunter(pool, notifier, olx)
	waits := recordWaits(h)
	if res := mustRun(t, h); res.Sent != 1 {
		t.Fatalf("first run = %+v", res)
	}
	olx.add(Listing{Source: "olx", ExternalID: "2", Title: "Blade Runner Blu-ray PL", URL: "https://www.olx.pl/d/oferta/z-ID2.html", PriceCents: 4500})
	if res := mustRun(t, h); res.Sent != 1 {
		t.Fatalf("second run = %+v", res)
	}
	if !pacedFromReturn(waits(), delay) {
		t.Fatalf("waits = %v, want about telegramPause before the second run's message", waits())
	}
}

func TestRunRetriesAnAlertOnlyAtItsRefreshedPrice(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	olx := &fakeSource{name: "olx", perQuery: true, listings: []Listing{
		{Source: "olx", ExternalID: "1", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/z-ID1.html", PriceCents: 4000},
	}}
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, olx)
	if res := mustRun(t, h); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("failing run = %+v", res)
	}
	olx.listings[0].PriceCents = 6500
	if res := mustRun(t, h); res.Sent != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("run with the price now above the limit = %+v, want no alert", res)
	}
}

// hookSource calls before once, ahead of its first search, and logs each
// search.
type hookSource struct {
	*fakeSource
	before func()
	once   sync.Once
	log    *callLog
}

func (s *hookSource) Search(ctx context.Context, query string) ([]Listing, error) {
	s.once.Do(s.before)
	s.log.add("search " + s.name)
	return s.fakeSource.Search(ctx, query)
}

func TestRunSendsAnAlertWhoseLimitRoseDuringTheRun(t *testing.T) {
	pool := dbtest.Migrated(t)
	copyID := wishlist(t, pool, wantBluray(new(int64(5000))))
	listing := Listing{Source: "olx", ExternalID: "1", Title: "Blade Runner Blu-ray", URL: "https://www.olx.pl/d/oferta/z-ID1.html", PriceCents: 4000}
	notifier := &fakeNotifier{fail: 1}
	if res := mustRun(t, newTestHunter(pool, notifier, &fakeSource{name: "olx", perQuery: true, listings: []Listing{listing}})); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("failing run = %+v", res)
	}
	setLimit := func(cents int64) {
		if _, err := pool.Exec(context.Background(), `UPDATE copy SET max_price_cents = $2 WHERE id = $1`, copyID, cents); err != nil {
			t.Fatalf("limit: %v", err)
		}
	}
	setLimit(3000)
	log := &callLog{}
	olx := &hookSource{fakeSource: &fakeSource{name: "olx", perQuery: true, listings: []Listing{listing}}, before: func() { setLimit(5000) }, log: log}
	if res := mustRun(t, newTestHunter(pool, &loggedNotifier{log: log}, olx)); res.Sent != 1 {
		t.Fatalf("run with the limit raised during it = %+v, want the pending alert sent", res)
	}
	if want := []string{"search olx", "send", "search olx"}; !slices.Equal(log.list(), want) {
		t.Fatalf("calls = %q, want the alert sent before the next request", log.list())
	}
}

// restarted builds a hunter on a 2h schedule on pool, as a restart would,
// and starts it; its first scheduled wait arrives on the returned channel.
func restarted(t *testing.T, pool *pgxpool.Pool) (*Hunter, <-chan time.Duration) {
	t.Helper()
	h := NewHunter(NewStore(pool), nil, nil, Config{Interval: 2 * time.Hour}, slog.New(slog.DiscardHandler))
	waits := make(chan time.Duration, 1)
	h.sleep = func(_ context.Context, d time.Duration) error {
		waits <- d
		return context.Canceled
	}
	h.Start(t.Context())
	h.Wait()
	return h, waits
}

func storeRun(t *testing.T, pool *pgxpool.Pool, age string) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), `INSERT INTO deal_run (started_at, finished_at, result)
		VALUES (now() - $1::interval, now() - $1::interval + interval '1 minute', '{"wants": 1}')`, age); err != nil {
		t.Fatalf("store run: %v", err)
	}
}

func TestStartSchedulesAfterTheLastRun(t *testing.T) {
	pool := dbtest.Migrated(t)
	storeRun(t, pool, "30 minutes")
	_, waits := restarted(t, pool)
	if got := <-waits; got < 80*time.Minute || got > 100*time.Minute {
		t.Fatalf("first wait = %v, want about 90m after a run started 30m ago", got)
	}
}

func TestStartRunsAnOverdueHuntAfterFiveMinutes(t *testing.T) {
	pool := dbtest.Migrated(t)
	storeRun(t, pool, "3 hours")
	_, waits := restarted(t, pool)
	if got := <-waits; got < 5*time.Minute || got > 6*time.Minute {
		t.Fatalf("first wait = %v, want the 5m minimum after a run started 3h ago", got)
	}
}

func TestStartWithoutHistoryWaitsFiveMinutes(t *testing.T) {
	pool := dbtest.Migrated(t)
	_, waits := restarted(t, pool)
	if got := <-waits; got < 5*time.Minute || got > 6*time.Minute {
		t.Fatalf("first wait = %v, want about 5m", got)
	}
}

// sameStatus fails unless after holds before's last run.
func sameStatus(t *testing.T, before, after Status) {
	t.Helper()
	if before.LastStartedAt == nil || before.LastFinishedAt == nil || before.LastResult == nil {
		t.Fatalf("status before the restart lacks the run: %+v", before)
	}
	if after.LastStartedAt == nil || !after.LastStartedAt.Equal(*before.LastStartedAt) ||
		after.LastFinishedAt == nil || !after.LastFinishedAt.Equal(*before.LastFinishedAt) ||
		!reflect.DeepEqual(after.LastResult, before.LastResult) {
		t.Fatalf("status after the restart = %+v, result %+v; want %+v, result %+v",
			after, after.LastResult, before, before.LastResult)
	}
}

func TestManualRunSurvivesARestart(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(new(int64(5000))))
	notifier := &fakeNotifier{}
	h := newTestHunter(pool, notifier, olxFixture(t))
	h.Start(t.Context())
	if !h.Trigger() {
		t.Fatal("Trigger = false")
	}
	h.Wait()
	before := h.Status()
	if before.LastResult == nil || before.LastResult.Sent != 1 {
		t.Fatalf("manual run = %+v", before.LastResult)
	}

	after, waits := restarted(t, pool)
	sameStatus(t, before, after.Status())
	if got := <-waits; got < 105*time.Minute || got > 135*time.Minute {
		t.Fatalf("first wait = %v, want about 2h after a manual run just now", got)
	}
}

// shutdownSource answers only once its request is canceled.
type shutdownSource struct{ started chan struct{} }

func (shutdownSource) Name() string   { return "olx" }
func (shutdownSource) PerQuery() bool { return false }

func (s shutdownSource) Search(ctx context.Context, _ string) ([]Listing, error) {
	s.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestRunCutShortByShutdownCounts(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, wantBluray(nil))
	src := shutdownSource{started: make(chan struct{}, 1)}
	h := newTestHunter(pool, nil, src)
	ctx, cancel := context.WithCancel(t.Context())
	h.Start(ctx)
	if !h.Trigger() {
		t.Fatal("Trigger = false")
	}
	<-src.started
	cancel()
	h.Wait()
	before := h.Status()
	if before.LastResult == nil || !strings.Contains(before.LastResult.Failure, "context canceled") {
		t.Fatalf("run cut short = %+v", before.LastResult)
	}

	after, waits := restarted(t, pool)
	sameStatus(t, before, after.Status())
	if got := <-waits; got < 105*time.Minute || got > 135*time.Minute {
		t.Fatalf("first wait = %v, want about 2h after a run started just now", got)
	}
}
