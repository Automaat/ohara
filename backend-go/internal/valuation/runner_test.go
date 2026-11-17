package valuation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

type fakeSource struct {
	name     string
	listings []deals.Listing
	err      error

	mu      sync.Mutex
	queries []string
}

func (f *fakeSource) Name() string   { return f.name }
func (f *fakeSource) PerQuery() bool { return true }

func (f *fakeSource) Search(_ context.Context, q string) ([]deals.Listing, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queries = append(f.queries, q)
	return f.listings, f.err
}

type fakeBuyBack struct {
	quotes map[string]Quote
	asked  []string
}

func (*fakeBuyBack) Name() string { return SourceSkupSzop }

func (f *fakeBuyBack) Quote(_ context.Context, isbn string) (Quote, error) {
	f.asked = append(f.asked, isbn)
	q, ok := f.quotes[isbn]
	if !ok {
		return Quote{}, errors.New("skupszop: status 503")
	}
	return q, nil
}

func used() *string  { s := "used"; return &s }
func fresh() *string { s := "new"; return &s }

func exec(t *testing.T, pool *pgxpool.Pool, sql string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// seed owns a book and a Blu-ray and wants a DVD.
func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE
		VALUES (1, 'book', 'Katedra', '{"Jacek Dukaj"}')`)
	exec(t, pool, `INSERT INTO title (id, kind, name, original_title) OVERRIDING SYSTEM VALUE
		VALUES (2, 'movie', 'Diuna', 'Dune')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, binding) OVERRIDING SYSTEM VALUE
		VALUES (10, 1, '9788308073209', 'paperback')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (20, 2, '5902600069706', 'bluray'), (21, 2, '5902600069713', 'dvd')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES
		(10, 'owned', 3000), (20, 'owned', 5000), (20, 'owned', NULL)`)
	exec(t, pool, `INSERT INTO copy (edition_id, status) VALUES (21, 'wishlist')`)
}

func listing(source, id, title string, cents int64, state *string) deals.Listing {
	return deals.Listing{
		Source: source, ExternalID: id, Title: title, URL: "https://example.test/" + id,
		PriceCents: cents, AcquiredAs: state, Raw: json.RawMessage(`{}`),
	}
}

func newTestRunner(pool *pgxpool.Pool, olx, ceneo *fakeSource, bb *fakeBuyBack) *Runner {
	logger := slog.New(slog.DiscardHandler)
	r := NewRunner(NewStore(pool), []deals.Source{olx, ceneo}, []BuyBack{bb},
		Config{Window: 90 * 24 * time.Hour}, logger)
	r.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return r
}

func TestRunValuesOwnedCopies(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	olx := &fakeSource{name: "olx", listings: []deals.Listing{
		listing("olx", "1", "Katedra Jacek Dukaj", 2000, used()),
		listing("olx", "2", "Dukaj - Katedra, stan dobry", 2500, nil),
		listing("olx", "3", "Katedra. Opowiadania. Dukaj", 3000, used()),
		listing("olx", "4", "Diuna Blu-ray", 4000, used()),
		listing("olx", "5", "Diuna DVD", 1000, used()),
		listing("olx", "6", "Katedra Dukaj okładka", 100, used()),
		listing("olx", "7", "Katedra Dukaj nowa w folii", 9000, fresh()),
	}}
	ceneo := &fakeSource{name: "ceneo", listings: []deals.Listing{
		listing("ceneo", "c1", "Katedra - Jacek Dukaj", 4999, fresh()),
	}}
	bb := &fakeBuyBack{quotes: map[string]Quote{
		"9788308073209": {Offered: true, Cents: 537, Title: "Katedra", URL: "https://skupszop.test/sprzedaj"},
	}}
	r := newTestRunner(pool, olx, ceneo, bb)

	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := RunResult{Editions: 2, Queries: 3, Listings: 24, Matched: 18, New: 6, Quotes: 1, Errors: []string{}}
	if res.Editions != want.Editions || res.Queries != want.Queries || res.Listings != want.Listings ||
		res.Matched != want.Matched || res.New != want.New || res.Quotes != want.Quotes || len(res.Errors) != 0 {
		t.Fatalf("Run = %+v, want %+v", res, want)
	}
	if strings.Join(bb.asked, ",") != "9788308073209" {
		t.Fatalf("buy-back asked for %v, want the owned book only", bb.asked)
	}

	copies, sum, err := r.store.Valued(ctx, r.Window())
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	if len(copies) != 3 {
		t.Fatalf("copies = %d, want the 3 owned copies", len(copies))
	}
	byEdition := map[int64]CopyValue{}
	for _, c := range copies {
		byEdition[c.EditionID] = c
	}
	book := byEdition[10]
	if book.Floor == nil || book.Floor.Cents != 537 || book.Mid == nil || *book.Mid != (Estimate{Cents: 2500, Source: SourceOLX, Count: 3}) ||
		book.Ceiling == nil || book.Ceiling.Cents != 4999 || book.LowConfidence {
		t.Fatalf("book value = %+v %+v %+v low=%v", book.Floor, book.Mid, book.Ceiling, book.LowConfidence)
	}
	movie := byEdition[20]
	if movie.Floor != nil || movie.Mid == nil || movie.Mid.Cents != 4000 || movie.Mid.Count != 1 || !movie.LowConfidence {
		t.Fatalf("movie value = %+v %+v low=%v", movie.Floor, movie.Mid, movie.LowConfidence)
	}
	wantSum := Summary{
		Copies: 3, SpentCents: 8000,
		Floor:         Bound{Cents: 537, Copies: 1},
		Mid:           Bound{Cents: 2500 + 4000 + 4000, Copies: 3},
		Ceiling:       Bound{Cents: 4999, Copies: 1},
		LowConfidence: 2,
	}
	if sum != wantSum {
		t.Fatalf("summary = %+v, want %+v", sum, wantSum)
	}

	history, err := r.store.History(ctx, 30)
	if err != nil || len(history) != 1 || history[0].Summary != wantSum {
		t.Fatalf("history = %+v, %v", history, err)
	}
	body := scrape(t)
	for _, line := range []string{
		`ohara_collection_value_pln{bound="mid"} 105`,
		`ohara_collection_value_pln{bound="floor"} 5.37`,
		`ohara_collection_valued_copies{bound="ceiling"} 1`,
		`ohara_collection_copies 3`,
		`ohara_collection_spent_pln 80`,
		`ohara_collection_low_confidence_copies 2`,
	} {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("metrics lack %q", line)
		}
	}

	var firstSeen, firstObserved time.Time
	if err := pool.QueryRow(ctx, `SELECT observed_at, last_seen_at FROM price_observation
		WHERE source = 'olx' AND external_id = '1'`).Scan(&firstObserved, &firstSeen); err != nil {
		t.Fatal(err)
	}
	olx.listings[0].PriceCents = 2100
	res, err = r.Run(ctx)
	if err != nil || res.New != 0 || res.Matched != 18 || res.Quotes != 1 {
		t.Fatalf("second Run = %+v, %v", res, err)
	}
	var price int64
	var observed, seen time.Time
	if err := pool.QueryRow(ctx, `SELECT price_cents, observed_at, last_seen_at FROM price_observation
		WHERE source = 'olx' AND external_id = '1'`).Scan(&price, &observed, &seen); err != nil {
		t.Fatal(err)
	}
	if price != 2100 || !observed.Equal(firstObserved) || !seen.After(firstSeen) {
		t.Fatalf("seen again: price %d observed %v (was %v) seen %v (was %v)", price, observed, firstObserved, seen, firstSeen)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_observation`).Scan(&rows); err != nil || rows != 7 {
		t.Fatalf("observations = %d, %v; a second run must not duplicate rows", rows, err)
	}
	history, err = r.store.History(ctx, 30)
	if err != nil || len(history) != 1 {
		t.Fatalf("a second run the same day must replace the day: %+v, %v", history, err)
	}
}

func scrape(t *testing.T) string {
	t.Helper()
	rec := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

func TestRunKeepsGoingOnSourceErrors(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	olx := &fakeSource{name: "olx", err: errors.New("olx: status 403")}
	ceneo := &fakeSource{name: "ceneo"}
	bb := &fakeBuyBack{quotes: map[string]Quote{}}
	r := newTestRunner(pool, olx, ceneo, bb)
	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Errors) != 4 || res.Errors[0] != "olx: status 403" || res.Errors[3] != "skupszop: status 503" {
		t.Fatalf("errors = %q", res.Errors)
	}
	if _, ok, err := r.store.Latest(context.Background()); err != nil || !ok {
		t.Fatalf("Latest = %v, %v; a run with failing sources still stores the day", ok, err)
	}
}

func TestPricesWindowAndOwnership(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	exec(t, pool, `INSERT INTO price_observation (source, external_id, price_cents, edition_id, last_seen_at)
		VALUES ('olx', 'old', 100, 10, now() - interval '100 days'),
		       ('olx', 'recent', 200, 10, now() - interval '10 days'),
		       ('olx', 'wish', 300, 21, now()),
		       ('pepper', 'p', 400, 10, now())`)
	prices, err := NewStore(pool).Prices(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Prices: %v", err)
	}
	if len(prices) != 1 || prices[0].Cents != 200 {
		t.Fatalf("Prices = %+v, want only the recent OLX price of an owned edition", prices)
	}
}

func TestRecordKeepsAnEarlierEdition(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	exec(t, pool, `INSERT INTO price_observation (source, external_id, price_cents, edition_id, title_id)
		VALUES ('olx', '9', 500, 21, 2)`)
	store := NewStore(pool)
	h := Holding{Want: deals.Want{EditionID: 20, TitleID: 2}}
	l := listing("olx", "9", "Diuna", 450, used())
	isNew, err := store.Record(ctx, &l, &h)
	if err != nil || isNew {
		t.Fatalf("Record = %v, %v; want an existing listing", isNew, err)
	}
	var edition int64
	var price int64
	if err := pool.QueryRow(ctx, `SELECT edition_id, price_cents FROM price_observation
		WHERE external_id = '9'`).Scan(&edition, &price); err != nil {
		t.Fatal(err)
	}
	if edition != 21 || price != 450 {
		t.Fatalf("edition %d price %d, want edition 21 kept and price 450", edition, price)
	}
}

func TestStartSchedulesAfterTheLastValue(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := t.Context()
	exec(t, pool, `INSERT INTO valuation_history (day, copies, spent_cents, floor_cents, floor_copies,
			mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence, computed_at)
		VALUES (current_date, 1, 0, 0, 0, 0, 0, 0, 0, 1, now() - interval '2 hours')`)
	logger := slog.New(slog.DiscardHandler)
	r := NewRunner(NewStore(pool), nil, nil, Config{Interval: 24 * time.Hour, Window: time.Hour}, logger)
	waits := make(chan time.Duration, 1)
	r.sleep = func(_ context.Context, d time.Duration) error {
		waits <- d
		return context.Canceled
	}
	r.Start(ctx)
	r.Wait()
	got := <-waits
	if got < 19*time.Hour || got > 25*time.Hour {
		t.Fatalf("first wait = %v, want about 22h after a value 2h old", got)
	}
}

func TestStartWithoutHistoryWaitsFiveMinutes(t *testing.T) {
	pool := dbtest.Migrated(t)
	logger := slog.New(slog.DiscardHandler)
	r := NewRunner(NewStore(pool), nil, nil, Config{Interval: 24 * time.Hour}, logger)
	waits := make(chan time.Duration, 1)
	r.sleep = func(_ context.Context, d time.Duration) error {
		waits <- d
		return context.Canceled
	}
	r.Start(context.Background())
	r.Wait()
	if got := <-waits; got < 4*time.Minute || got > 6*time.Minute {
		t.Fatalf("first wait = %v, want about 5m", got)
	}
}

type blockingSource struct {
	started chan struct{}
	release chan struct{}
}

func (blockingSource) Name() string   { return "olx" }
func (blockingSource) PerQuery() bool { return true }

func (b blockingSource) Search(ctx context.Context, _ string) ([]deals.Listing, error) {
	b.started <- struct{}{}
	select {
	case <-b.release:
	case <-ctx.Done():
	}
	return nil, nil
}

func TestValuationAPI(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	logger := slog.New(slog.DiscardHandler)
	src := blockingSource{started: make(chan struct{}, 1), release: make(chan struct{})}
	store := NewStore(pool)
	runner := NewRunner(store, []deals.Source{src}, nil, Config{Window: 90 * 24 * time.Hour}, logger)
	runner.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	h := NewHandler(store, runner, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)

	var view View
	if code := get(t, srv.URL+"/api/valuation", &view); code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	if view.WindowDays != 90 || view.MinObservations != MinObservations || len(view.Copies) != 3 ||
		view.Summary.Copies != 3 || view.Summary.SpentCents != 8000 || len(view.History) != 0 ||
		view.Status.Running || view.Status.Scheduled {
		t.Fatalf("view = %+v", view)
	}
	if view.Copies[0].TitleName != "Diuna" || view.Copies[2].TitleName != "Katedra" || !view.Copies[0].LowConfidence {
		t.Fatalf("copies = %+v", view.Copies)
	}

	if code := post(t, srv.URL+"/api/valuation/run"); code != http.StatusAccepted {
		t.Fatalf("POST run = %d", code)
	}
	<-src.started
	if code := post(t, srv.URL+"/api/valuation/run"); code != http.StatusConflict {
		t.Fatalf("second POST run = %d, want 409", code)
	}
	close(src.release)
	go func() {
		for range src.started {
		}
	}()
	runner.Wait()
	if code := get(t, srv.URL+"/api/valuation", &view); code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	if view.Status.LastResult == nil || view.Status.LastResult.Failure != "" || len(view.History) != 1 {
		t.Fatalf("after run: status %+v history %+v", view.Status, view.History)
	}
}

func get(t *testing.T, url string, out any) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %q: %v", raw, err)
	}
	return resp.StatusCode
}

func post(t *testing.T, url string) int {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}
