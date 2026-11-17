package deals

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
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
	obs, err := h.store.Recent(context.Background(), 10)
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
	obs, err := h.store.Recent(context.Background(), 10)
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
