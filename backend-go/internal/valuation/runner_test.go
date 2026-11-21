package valuation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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

// listing is an OLX listing.
func listing(id, title string, cents int64, state *string) deals.Listing {
	return deals.Listing{
		Source: SourceOLX, ExternalID: id, Title: title, URL: "https://example.test/" + id,
		PriceCents: cents, AcquiredAs: state, Raw: json.RawMessage(`{}`),
	}
}

func newTestRunner(pool *pgxpool.Pool, bb *fakeBuyBack, sources ...*fakeSource) *Runner {
	logger := slog.New(slog.DiscardHandler)
	src := make([]deals.Source, 0, len(sources))
	for _, s := range sources {
		src = append(src, s)
	}
	r := NewRunner(NewStore(pool), src, []BuyBack{bb}, nil,
		Config{Window: 90 * 24 * time.Hour}, logger)
	r.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return r
}

func TestRunValuesOwnedCopies(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	olx := &fakeSource{name: "olx", listings: []deals.Listing{
		listing("1", "Katedra Jacek Dukaj", 2000, used()),
		listing("2", "Dukaj - Katedra, stan dobry", 2500, nil),
		listing("3", "Katedra. Opowiadania. Dukaj", 3000, used()),
		listing("4", "Diuna Blu-ray", 4000, used()),
		listing("5", "Diuna DVD", 1000, used()),
		listing("6", "Katedra Dukaj okładka", 100, used()),
		listing("7", "Katedra Dukaj nowa w folii", 9000, fresh()),
	}}
	bb := &fakeBuyBack{quotes: map[string]Quote{
		"9788308073209": {Offered: true, Cents: 537, Title: "Katedra", URL: "https://skupszop.test/sprzedaj"},
	}}
	r := newTestRunner(pool, bb, olx)

	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := RunResult{Editions: 2, Queries: 3, Listings: 21, Matched: 15, New: 5, Quotes: 1, Errors: []string{}}
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
		book.Ceiling != nil || book.LowConfidence {
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
		`ohara_collection_valued_copies{bound="ceiling"} 0`,
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
	if err != nil || res.New != 0 || res.Matched != 15 || res.Quotes != 1 {
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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_observation`).Scan(&rows); err != nil || rows != 6 {
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
	bb := &fakeBuyBack{quotes: map[string]Quote{}}
	r := newTestRunner(pool, bb, olx)
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

func TestRunCountsSourceHealth(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	failing := &fakeSource{name: "valuation-failing", err: errors.New("boom")}
	working := &fakeSource{name: "valuation-working"}
	if _, err := newTestRunner(pool, &fakeBuyBack{quotes: map[string]Quote{}}, failing, working).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	body := scrape(t)
	for _, want := range []string{
		`ohara_source_requests_total{source="valuation-failing",status="error"}`,
		`ohara_source_errors_total{source="valuation-failing"}`,
		`ohara_source_requests_total{source="valuation-working",status="ok"}`,
		`ohara_source_last_success_timestamp_seconds{source="valuation-working"}`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %s", want)
		}
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
		       ('skupszop', 's', 250, 10, now()),
		       ('pepper', 'p', 400, 10, now())`)
	prices, err := NewStore(pool).Prices(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Prices: %v", err)
	}
	slices.SortFunc(prices, func(a, b Price) int { return int(a.Cents - b.Cents) })
	if len(prices) != 2 || prices[0].Cents != 200 || prices[1].Source != SourceSkupSzop {
		t.Fatalf("Prices = %+v, want the recent OLX and SkupSzop prices of an owned edition", prices)
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
	l := listing("9", "Diuna", 450, used())
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

// wishBluray adds a wished Blu-ray edition 22 of "Diuna" next to seed's
// owned Blu-ray 20 and records found against it, as the deal hunter does.
func wishBluray(t *testing.T, pool *pgxpool.Pool, found []deals.Listing) {
	t.Helper()
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (22, 2, '5902600069720', 'bluray')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status) VALUES (22, 'wishlist')`)
	bluray := "bluray"
	wish := deals.Want{EditionID: 22, TitleID: 2, Kind: "movie", Name: "Diuna", Format: &bluray}
	for i := range found {
		if _, _, err := deals.NewStore(pool).Record(context.Background(), &found[i], &wish, false); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWishedEditionListingsPriceTheOwnedEdition(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	found := []deals.Listing{
		listing("b1", "Diuna Blu-ray", 4000, used()),
		listing("b2", "Diuna Blu-ray PL", 4500, used()),
		listing("b3", "Diuna Blu-ray stan idealny", 5000, used()),
	}
	wishBluray(t, pool, found)
	r := newTestRunner(pool, &fakeBuyBack{quotes: map[string]Quote{}}, &fakeSource{name: "olx", listings: found})
	if _, err := r.Run(ctx); err != nil {
		t.Fatal(err)
	}
	copies, _, err := r.store.Valued(ctx, r.Window())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range copies {
		if c.EditionID == 20 && (c.Mid == nil || c.Mid.Count != 3) {
			t.Fatalf("owned Blu-ray mid = %+v, want the 3 OLX Blu-ray prices", c.Mid)
		}
	}
}

func TestPricesPoolOnlyUsedMiddles(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, binding) OVERRIDING SYSTEM VALUE
		VALUES (12, 1, '9788308049396', 'hardcover')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format, discogs_release_id)
		OVERRIDING SYSTEM VALUE VALUES (22, 2, '5902600069720', 'bluray', 77)`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format, updated_at) OVERRIDING SYSTEM VALUE
		VALUES (23, 2, '5902600069737', 'bluray', now() - interval '1 hour')`)
	exec(t, pool, `INSERT INTO title (id, kind, name) OVERRIDING SYSTEM VALUE VALUES (3, 'movie', 'Solaris')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (30, 3, '5902600069744', 'bluray')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status) VALUES (22, 'wishlist'), (30, 'wishlist')`)
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, observed_at, last_seen_at)
		VALUES ('olx', 'printing', 100, 12, now(), now()),
		       ('olx', 'printing-2', 110, 12, now(), now()),
		       ('skupszop', '9788308049396', 120, 12, now(), now()),
		       ('olx', 'wish', 200, 22, now(), now()),
		       ('discogs_vg_plus', '77/22', 210, 22, now(), now()),
		       ('olx', 'first-seen-since-edit', 220, 23, now(), now()),
		       ('olx', 'first-seen-before-edit', 230, 23, now() - interval '2 hours', now()),
		       ('olx', 'dvd', 300, 21, now(), now()),
		       ('olx', 'dvd-2', 310, 21, now(), now()),
		       ('olx', 'other-title', 400, 30, now(), now())`)
	prices, err := NewStore(pool).Prices(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Prices: %v", err)
	}
	got := make([]int64, 0, len(prices))
	for _, p := range prices {
		got = append(got, p.Cents)
	}
	slices.Sort(got)
	if !slices.Equal(got, []int64{100, 110, 200, 220}) {
		t.Fatalf("Prices = %+v, want only the OLX prices of unowned editions in an owned group first seen since their last edit", prices)
	}
}

func TestEditedFormatDropsEarlierListings(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (24, 2, '5902600069751', 'dvd')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status) VALUES (24, 'owned')`)
	record := func(edition int64, format string, found ...deals.Listing) {
		t.Helper()
		want := deals.Want{EditionID: edition, TitleID: 2, Kind: "movie", Name: "Diuna", Format: &format}
		for i := range found {
			if _, _, err := deals.NewStore(pool).Record(ctx, &found[i], &want, false); err != nil {
				t.Fatal(err)
			}
		}
	}
	dvds := []deals.Listing{
		listing("d1", "Diuna DVD", 1000, used()),
		listing("d2", "Diuna DVD PL", 2000, used()),
	}
	record(21, "dvd", dvds...)
	exec(t, pool, `UPDATE edition SET format = 'bluray', updated_at = now(), edited_at = now() WHERE id = 21`)
	record(24, "dvd", dvds...)
	record(21, "bluray", listing("b1", "Diuna Blu-ray", 4000, used()))
	prices, err := NewStore(pool).Prices(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Prices: %v", err)
	}
	var bluray []int64
	for _, p := range prices {
		if p.Format != nil && *p.Format == "bluray" {
			bluray = append(bluray, p.Cents)
		}
	}
	if !slices.Equal(bluray, []int64{4000}) {
		t.Fatalf("Blu-ray prices = %v, want only the listing first seen after the format edit", bluray)
	}
}

func TestStartSchedulesAfterTheLastValue(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := t.Context()
	exec(t, pool, `INSERT INTO valuation_history (day, copies, spent_cents, floor_cents, floor_copies,
			mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence, computed_at)
		VALUES (current_date, 1, 0, 0, 0, 0, 0, 0, 0, 1, now() - interval '2 hours')`)
	logger := slog.New(slog.DiscardHandler)
	r := NewRunner(NewStore(pool), nil, nil, nil, Config{Interval: 24 * time.Hour, Window: time.Hour}, logger)
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
	r := NewRunner(NewStore(pool), nil, nil, nil, Config{Interval: 24 * time.Hour}, logger)
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
	runner := NewRunner(store, []deals.Source{src}, nil, nil, Config{Window: 90 * 24 * time.Hour}, logger)
	runner.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	h := NewHandler(store, runner, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	exec(t, pool, `UPDATE title SET cover_url = 'https://covers.test/katedra.jpg' WHERE id = 1`)
	exec(t, pool, `UPDATE title SET cover_url = 'https://covers.test/diuna.jpg' WHERE id = 2`)
	exec(t, pool, `UPDATE edition SET cover_url = 'https://covers.test/diuna-bluray.jpg' WHERE id = 20`)

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
	for i, want := range []string{
		"https://covers.test/diuna-bluray.jpg",
		"https://covers.test/diuna-bluray.jpg",
		"https://covers.test/katedra.jpg",
	} {
		if got := view.Copies[i].CoverURL; got == nil || *got != want {
			t.Fatalf("copy %d cover = %v, want %s", i, got, want)
		}
	}
	if view.LastValuation != nil {
		t.Fatalf("last valuation before any = %q, want null", *view.LastValuation)
	}

	// A valuation older than the history the view returns still counts as
	// the last one.
	exec(t, pool, `INSERT INTO valuation_history (day, copies, spent_cents, floor_cents, floor_copies,
			mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence)
		VALUES (current_date - 400, 3, 8000, 0, 0, 5000, 2, 0, 0, 1)`)
	var old View
	if code := get(t, srv.URL+"/api/valuation", &old); code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	var oldDay string
	if err := pool.QueryRow(context.Background(),
		`SELECT to_char(current_date - 400, 'YYYY-MM-DD')`).Scan(&oldDay); err != nil {
		t.Fatal(err)
	}
	if len(old.History) != 0 || old.LastValuation == nil || *old.LastValuation != oldDay {
		t.Fatalf("old valuation: history %+v, last %v, want %s", old.History, old.LastValuation, oldDay)
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
	if view.LastValuation == nil || *view.LastValuation != view.History[0].Day {
		t.Fatalf("after run: last valuation %v, history %+v", view.LastValuation, view.History)
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

func TestRunValuesEveryOwnedPrinting(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, binding) OVERRIDING SYSTEM VALUE
		VALUES (11, 1, '9788308049396', 'hardcover')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES (11, 'owned', 6000)`)
	olx := &fakeSource{name: "olx", listings: []deals.Listing{
		listing("1", "Katedra Jacek Dukaj", 2000, used()),
		listing("2", "Katedra Dukaj twarda oprawa", 3000, used()),
		listing("3", "Katedra Dukaj", 4000, used()),
	}}
	bb := &fakeBuyBack{quotes: map[string]Quote{
		"9788308073209": {Offered: true, Cents: 537},
		"9788308049396": {Offered: true, Cents: 900},
	}}
	r := newTestRunner(pool, bb, olx)
	if _, err := r.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	copies, _, err := r.store.Valued(context.Background(), r.Window())
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	floors := map[int64]int64{}
	for _, c := range copies {
		if c.TitleID != 1 {
			continue
		}
		if c.Mid == nil || c.Mid.Cents != 3000 || c.Mid.Count != 3 {
			t.Fatalf("edition %d mid = %+v, want the title's 3 used prices", c.EditionID, c.Mid)
		}
		if c.Floor == nil {
			t.Fatalf("edition %d has no floor", c.EditionID)
		}
		floors[c.EditionID] = c.Floor.Cents
	}
	if floors[10] != 537 || floors[11] != 900 {
		t.Fatalf("floors = %v, want each printing's own buy-back offer", floors)
	}
}

type fakePricer struct {
	prices map[int64]ReleasePrice
	err    error
	asked  []int64
}

func (*fakePricer) Name() string { return SourceDiscogs }

func (f *fakePricer) Price(_ context.Context, id int64) (ReleasePrice, error) {
	f.asked = append(f.asked, id)
	return f.prices[id], f.err
}

func TestRunPricesAlbumsOnDiscogs(t *testing.T) {
	pool := dbtest.Migrated(t)
	exec(t, pool, `INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE
		VALUES (7, 'music', 'The Dark Side Of The Moon', '{"Pink Floyd"}')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format, discogs_release_id) OVERRIDING SYSTEM VALUE
		VALUES (70, 7, '5099902987620', 'cd', 1873013), (71, 7, NULL, 'vinyl', NULL),
		       (72, 7, '0602537869336', 'cd', 1873013)`)
	exec(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES
		(70, 'owned', 3500), (71, 'owned', 9000), (72, 'wishlist', NULL)`)
	olx := &fakeSource{name: "olx", listings: []deals.Listing{
		listing("1", "Pink Floyd The Dark Side Of The Moon CD", 2000, used()),
		listing("2", "Pink Floyd Dark Side of the Moon winyl LP", 8000, used()),
	}}
	lowest, vgPlus, mint := int64(5313), int64(7098), int64(11700)
	pricer := &fakePricer{prices: map[int64]ReleasePrice{
		1873013: {LowestCents: &lowest, NumForSale: 34, VGPlusCents: &vgPlus, MintCents: &mint},
	}}
	r := NewRunner(NewStore(pool), []deals.Source{olx}, nil, pricer,
		Config{Window: 90 * 24 * time.Hour}, slog.New(slog.DiscardHandler))
	r.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	ctx := context.Background()

	res, err := r.Run(ctx)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Releases != 1 || len(pricer.asked) != 1 || pricer.asked[0] != 1873013 {
		t.Fatalf("Run = %+v, asked %v: want the one owned edition naming a release", res, pricer.asked)
	}
	if res.Matched != 2 || len(olx.queries) != 1 || olx.queries[0] != "Pink Floyd The Dark Side Of The Moon" {
		t.Fatalf("Run = %+v, queries %q", res, olx.queries)
	}
	copies, sum, err := r.store.Valued(ctx, r.Window())
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	byEdition := map[int64]CopyValue{}
	for _, c := range copies {
		byEdition[c.EditionID] = c
	}
	cd := byEdition[70]
	if cd.Floor == nil || cd.Floor.Cents != lowest || cd.Floor.Source != SourceDiscogs ||
		cd.Mid == nil || cd.Mid.Cents != vgPlus || cd.Ceiling == nil || cd.Ceiling.Cents != mint || cd.LowConfidence {
		t.Fatalf("CD value = %+v", cd)
	}
	vinyl := byEdition[71]
	if vinyl.Floor != nil || vinyl.Mid == nil || vinyl.Mid.Cents != 8000 || vinyl.Mid.Source != SourceOLX || !vinyl.LowConfidence {
		t.Fatalf("vinyl without a release = %+v", vinyl)
	}
	if sum.Copies != 2 || sum.Floor.Cents != lowest || sum.Mid.Cents != vgPlus+8000 {
		t.Fatalf("summary = %+v", sum)
	}

	pricer.err = errors.New("discogs: unexpected status 429")
	pricer.prices = map[int64]ReleasePrice{}
	res, err = r.Run(ctx)
	if err != nil || res.Releases != 0 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "429") {
		t.Fatalf("Run with Discogs down = %+v, %v", res, err)
	}
	copies, _, err = r.store.Valued(ctx, r.Window())
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	for _, c := range copies {
		if c.EditionID == 70 && (c.Mid == nil || c.Mid.Cents != vgPlus) {
			t.Fatalf("a failed run dropped the last Discogs prices: %+v", c)
		}
	}
}

func TestChangedReleaseDropsItsDiscogsPrices(t *testing.T) {
	pool := dbtest.Migrated(t)
	exec(t, pool, `INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE
		VALUES (8, 'music', 'Nevermind', '{"Nirvana"}')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, format, discogs_release_id) OVERRIDING SYSTEM VALUE
		VALUES (80, 8, 'cd', 100)`)
	exec(t, pool, `INSERT INTO copy (edition_id, status) VALUES (80, 'owned')`)
	vgPlus := int64(50000)
	pricer := &fakePricer{prices: map[int64]ReleasePrice{100: {VGPlusCents: &vgPlus}}}
	r := NewRunner(NewStore(pool), nil, nil, pricer, Config{Window: 90 * 24 * time.Hour}, slog.New(slog.DiscardHandler))
	r.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	ctx := context.Background()
	mid := func() *Estimate {
		t.Helper()
		if _, err := r.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
		copies, _, err := r.store.Valued(ctx, r.Window())
		if err != nil || len(copies) != 1 {
			t.Fatalf("Valued = %+v, %v", copies, err)
		}
		return copies[0].Mid
	}
	if m := mid(); m == nil || m.Cents != vgPlus {
		t.Fatalf("release 100 mid = %+v", m)
	}
	exec(t, pool, `UPDATE edition SET discogs_release_id = 200 WHERE id = 80`)
	if m := mid(); m != nil {
		t.Fatalf("release 200 without prices still valued at %+v from release 100", m)
	}
	exec(t, pool, `UPDATE edition SET discogs_release_id = NULL WHERE id = 80`)
	if m := mid(); m != nil {
		t.Fatalf("cleared release still valued at %+v", m)
	}
	exec(t, pool, `UPDATE edition SET discogs_release_id = 100 WHERE id = 80`)
	if m := mid(); m == nil || m.Cents != vgPlus {
		t.Fatalf("release 100 named again: mid = %+v", m)
	}
}

// panickingSource trips an index past the end of a slice on every search,
// like a parser meeting a malformed answer.
type panickingSource struct{ calls atomic.Int32 }

func (*panickingSource) Name() string   { return "olx" }
func (*panickingSource) PerQuery() bool { return true }

func (p *panickingSource) Search(context.Context, string) ([]deals.Listing, error) {
	p.calls.Add(1)
	return []deals.Listing{{Title: third(strings.Fields(""))}}, nil
}

// third is a parser that expects at least three fields.
func third(fields []string) string { return fields[2] }

func TestPanickingRunFailsAndTheScheduleGoesOn(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	src := &panickingSource{}
	var logs strings.Builder
	r := NewRunner(NewStore(pool), []deals.Source{src}, nil, nil,
		Config{Interval: time.Hour, Window: 90 * 24 * time.Hour}, slog.New(slog.NewJSONHandler(&logs, nil)))
	var sleeps atomic.Int32
	r.sleep = func(ctx context.Context, _ time.Duration) error {
		if sleeps.Add(1) > 2 {
			return context.Canceled
		}
		return ctx.Err()
	}

	r.Start(t.Context())
	r.Wait()
	if got := src.calls.Load(); got != 2 {
		t.Fatalf("scheduled runs searched %d times, want 2", got)
	}
	st := r.Status()
	if st.Running || st.LastResult == nil || st.LastFinishedAt == nil ||
		!strings.Contains(st.LastResult.Failure, "panic: runtime error: index out of range [2] with length 0") {
		t.Fatalf("status = %+v, result %+v", st, st.LastResult)
	}

	if !r.Trigger() {
		t.Fatal("Trigger after a panicking run = false")
	}
	r.Wait()
	if got := src.calls.Load(); got != 3 {
		t.Fatalf("manual run searched %d times in all, want 3", got)
	}
	if st := r.Status(); st.Running || st.LastResult == nil || st.LastResult.Failure == "" {
		t.Fatalf("status after manual run = %+v", st)
	}
	for _, want := range []string{`"level":"ERROR"`, `"msg":"valuation panicked"`, `"stack":"goroutine `} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("log lacks %s:\n%s", want, logs.String())
		}
	}
}
