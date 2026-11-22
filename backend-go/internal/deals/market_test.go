package deals_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

// listingSource answers every query with the same listings.
type listingSource struct{ listings []deals.Listing }

func (*listingSource) Name() string   { return "olx" }
func (*listingSource) PerQuery() bool { return true }

func (s *listingSource) Search(context.Context, string) ([]deals.Listing, error) {
	return append([]deals.Listing(nil), s.listings...), nil
}

// notifier keeps the messages it sends and fails the first fail sends.
type notifier struct {
	mu   sync.Mutex
	sent []string
	fail int
}

func (n *notifier) Send(_ context.Context, text string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fail > 0 {
		n.fail--
		return errors.New("telegram: status 502: Bad Gateway")
	}
	n.sent = append(n.sent, text)
	return nil
}

func (n *notifier) messages() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.sent...)
}

// marketWish stores a Blade Runner Blu-ray wish with limit and percent and
// prices used OLX listings of that Blu-ray the hunter found earlier, each at
// 50 zł, and returns the wish's copy id.
func marketWish(t *testing.T, pool *pgxpool.Pool, limit *int64, percent *int, prices int) int64 {
	t.Helper()
	ctx := context.Background()
	var titleID, editionID, copyID int64
	if err := pool.QueryRow(ctx, `INSERT INTO title (kind, name, original_title, polish_title)
		VALUES ('movie', 'Blade Runner', 'Blade Runner', 'Łowca androidów') RETURNING id`).Scan(&titleID); err != nil {
		t.Fatalf("title: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO edition (title_id, format, updated_at)
		VALUES ($1, 'bluray', now() - interval '1 day') RETURNING id`, titleID).Scan(&editionID); err != nil {
		t.Fatalf("edition: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy (edition_id, status, max_price_cents, max_market_percent)
		VALUES ($1, 'wishlist', $2, $3) RETURNING id`, editionID, limit, percent).Scan(&copyID); err != nil {
		t.Fatalf("copy: %v", err)
	}
	for i := range prices {
		if _, err := pool.Exec(ctx, `INSERT INTO price_observation
				(source, external_id, price_cents, acquired_as, url, edition_id, title_id, raw)
			VALUES ('olx', $1, 5000, 'used', 'https://www.olx.pl/d/oferta/x', $2, $3,
				jsonb_build_object('title', 'Blade Runner Blu-ray', 'wishlist_copy_id', $4::bigint))`,
			fmt.Sprintf("market-%d", i), editionID, titleID, copyID); err != nil {
			t.Fatalf("market price: %v", err)
		}
	}
	return copyID
}

func bluray(id string, cents int64) deals.Listing {
	return deals.Listing{
		Source: "olx", ExternalID: id, Title: "Łowca androidów Blu-ray " + id,
		URL: "https://www.olx.pl/d/oferta/" + id, PriceCents: cents, AcquiredAs: new("used"),
	}
}

// marketHunter is a hunter whose store reads the valuation's market
// prices, as cmd/api wires it.
func marketHunter(pool *pgxpool.Pool, n deals.Notifier, listings ...deals.Listing) (*deals.Hunter, *deals.Store) {
	logger := slog.New(slog.DiscardHandler)
	store := deals.NewStore(pool)
	runner := valuation.NewRunner(valuation.NewStore(pool), nil, nil, nil,
		valuation.Config{Window: 90 * 24 * time.Hour}, logger)
	store.UseMarkets(runner.DealMarkets)
	return deals.NewHunter(store, []deals.Source{&listingSource{listings: listings}}, n, deals.Config{}, logger), store
}

func run(t *testing.T, h *deals.Hunter) deals.RunResult {
	t.Helper()
	res, err := h.Run(context.Background())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return res
}

// TestMarketPercentAlerts alerts on a listing at or under a wish's percent
// of the used market price of its title and format, beside or instead of
// its limit, and only while that price rests on at least 3 listings.
func TestMarketPercentAlerts(t *testing.T) {
	tests := []struct {
		name    string
		limit   *int64
		percent *int
		prices  int
		listing int64
		alert   bool
	}{
		{"70% of 50 zł at 35 zł", nil, new(70), 5, 3500, true},
		{"70% of 50 zł at 36 zł", nil, new(70), 5, 3600, false},
		{"fewer than 3 prices with the listing", nil, new(70), 1, 1000, false},
		{"no market price", nil, new(100), 0, 100, false},
		{"percent met beside a limit missed", new(int64(3000)), new(70), 5, 3400, true},
		{"limit met beside a percent missed", new(int64(4000)), new(10), 5, 3900, true},
		{"both missed", new(int64(3000)), new(70), 5, 3600, false},
		{"limit only", new(int64(4000)), nil, 5, 3900, true},
		{"limit only, above it", new(int64(4000)), nil, 5, 4100, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			marketWish(t, pool, tt.limit, tt.percent, tt.prices)
			n := &notifier{}
			h, _ := marketHunter(pool, n, bluray("a", tt.listing))
			res := run(t, h)
			if got := len(n.messages()); tt.alert != (got == 1) || got > 1 || res.Alerts != got {
				t.Fatalf("run = %+v, messages %q; want alert %v", res, n.messages(), tt.alert)
			}
		})
	}

	t.Run("message, Pod limitem and Wysłane name the market price", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		marketWish(t, pool, nil, new(70), 5)
		n := &notifier{}
		h, store := marketHunter(pool, n, bluray("a", 3500), bluray("b", 3600))
		run(t, h)
		msgs := n.messages()
		if len(msgs) != 1 || !strings.Contains(msgs[0], "35,00 zł (70% ceny rynkowej 50,00 zł) · OLX") {
			t.Fatalf("messages = %q, want one for 35 zł naming 70%% of 50 zł", msgs)
		}
		for _, f := range []deals.RecentFilter{{Limit: 100, UnderLimit: true}, {Limit: 100, Sent: true}} {
			obs, err := store.Recent(context.Background(), f)
			if err != nil {
				t.Fatalf("recent %+v: %v", f, err)
			}
			if len(obs) != 1 || obs[0].ExternalID != "a" || obs[0].MaxMarketPercent == nil ||
				*obs[0].MaxMarketPercent != 70 || obs[0].MarketCents == nil || *obs[0].MarketCents != 5000 {
				t.Fatalf("recent %+v = %+v, want only the 35 zł listing with 70%% of 50 zł", f, obs)
			}
		}
	})

	t.Run("a queued alert that meets neither rule by send time is skipped", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		copyID := marketWish(t, pool, nil, new(70), 5)
		n := &notifier{fail: 1}
		h, _ := marketHunter(pool, n, bluray("a", 3500))
		if res := run(t, h); res.Alerts != 1 || res.Sent != 0 {
			t.Fatalf("failing run = %+v, want one alert queued and not sent", res)
		}
		setPercent := func(percent int) {
			if _, err := pool.Exec(context.Background(),
				`UPDATE copy SET max_market_percent = $2 WHERE id = $1`, copyID, percent); err != nil {
				t.Fatalf("percent: %v", err)
			}
		}
		setPercent(60)
		if res := run(t, h); res.Sent != 0 || len(n.messages()) != 0 {
			t.Fatalf("run at 60%% = %+v, messages %q; want no alert over 30 zł", res, n.messages())
		}
		setPercent(70)
		if res := run(t, h); res.Sent != 1 || len(n.messages()) != 1 {
			t.Fatalf("run back at 70%% = %+v, messages %q; want the queued alert sent", res, n.messages())
		}
	})
}

// TestRunAlertsOnAMarketItsOwnListingsMade judges a listing against the
// market price its answer's listings make: two stored 50 zł prices are too
// few, the answer's third makes a 50 zł market, and the 35 zł listing in
// the same answer alerts at 70% in that run.
func TestRunAlertsOnAMarketItsOwnListingsMade(t *testing.T) {
	pool := dbtest.Migrated(t)
	marketWish(t, pool, nil, new(70), 2)
	n := &notifier{}
	h, _ := marketHunter(pool, n, bluray("deal", 3500), bluray("third", 5000))
	res := run(t, h)
	if msgs := n.messages(); res.Alerts != 1 || res.Sent != 1 || len(msgs) != 1 ||
		!strings.Contains(msgs[0], "35,00 zł (70% ceny rynkowej 50,00 zł)") {
		t.Fatalf("run = %+v, messages %q; want the 35 zł listing sent at 70%% of 50 zł", res, msgs)
	}
}

// TestSendAlertReadsTheMarketOnceItHoldsTheListing skips an alert whose
// market price fell from 50 zł to 40 zł, a 28 zł cap at 70%, while
// SendAlert waited for the listing's row.
func TestSendAlertReadsTheMarketOnceItHoldsTheListing(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	marketWish(t, pool, nil, new(70), 5)
	h, store := marketHunter(pool, &notifier{fail: 1}, bluray("a", 3500))
	if res := run(t, h); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("failing run = %+v, want one alert queued and not sent", res)
	}
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM price_observation WHERE external_id = 'a'`).Scan(&id); err != nil {
		t.Fatalf("listing: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM price_observation WHERE id = $1 FOR UPDATE`, id); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE price_observation SET price_cents = 4000 WHERE external_id LIKE 'market-%'`); err != nil {
		t.Fatalf("lower market: %v", err)
	}
	type outcome struct {
		sent, called bool
		err          error
	}
	done := make(chan outcome, 1)
	go func() {
		var called bool
		sent, err := store.SendAlert(ctx, id, func(*deals.Alert) error {
			called = true
			return nil
		})
		done <- outcome{sent, called, err}
	}()
	waitForLockWait(t, pool)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	got := <-done
	if got.err != nil || got.sent || got.called {
		t.Fatalf("SendAlert = %+v, want the alert skipped under the lowered market", got)
	}
}

// waitForLockWait waits until a query of the test database waits for a
// row lock.
func waitForLockWait(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		var waiting bool
		if err := pool.QueryRow(context.Background(), `SELECT EXISTS (SELECT FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock'
				AND query LIKE 'SELECT 1 FROM price_observation WHERE id = $1 FOR UPDATE%')`).Scan(&waiting); err != nil {
			t.Fatalf("lock wait: %v", err)
		}
		if waiting {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no query waited for the listing's row")
}

// answersSource gives its answers in turn, one a query, then none.
type answersSource struct {
	mu      sync.Mutex
	answers [][]deals.Listing
}

func (*answersSource) Name() string   { return "olx" }
func (*answersSource) PerQuery() bool { return true }

func (s *answersSource) Search(context.Context, string) ([]deals.Listing, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.answers) == 0 {
		return nil, nil
	}
	next := s.answers[0]
	s.answers = s.answers[1:]
	return next, nil
}

// TestRunAlertsOnAMarketALaterAnswerMade judges a listing an earlier answer
// brought again once a later answer of the run makes its market: the first
// answer's 35 zł listing has no market yet, the second answer's three 50 zł
// listings make one, and the 35 zł listing alerts at 70% in that run.
func TestRunAlertsOnAMarketALaterAnswerMade(t *testing.T) {
	pool := dbtest.Migrated(t)
	marketWish(t, pool, nil, new(70), 0)
	n := &notifier{}
	logger := slog.New(slog.DiscardHandler)
	store := deals.NewStore(pool)
	runner := valuation.NewRunner(valuation.NewStore(pool), nil, nil, nil,
		valuation.Config{Window: 90 * 24 * time.Hour}, logger)
	store.UseMarkets(runner.DealMarkets)
	src := &answersSource{answers: [][]deals.Listing{
		{bluray("deal", 3500)},
		{bluray("m1", 5000), bluray("m2", 5000), bluray("m3", 5000)},
	}}
	h := deals.NewHunter(store, []deals.Source{src}, n, deals.Config{}, logger)
	res := run(t, h)
	if msgs := n.messages(); res.Alerts != 1 || res.Sent != 1 || len(msgs) != 1 ||
		!strings.Contains(msgs[0], "35,00 zł (70% ceny rynkowej 50,00 zł)") {
		t.Fatalf("run = %+v, messages %q; want the 35 zł listing sent at 70%% of 50 zł", res, msgs)
	}
}
