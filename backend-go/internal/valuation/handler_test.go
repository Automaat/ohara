package valuation

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// seedPooledBlurays adds to seed a second owned Blu-ray of "Diuna" (23)
// and a wished one (22), both priced with edition 20 as one group, and
// "Solaris", owned without any price.
func seedPooledBlurays(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (22, 2, '5902600069720', 'bluray'), (23, 2, '5902600069737', 'bluray')`)
	exec(t, pool, `INSERT INTO title (id, kind, name) OVERRIDING SYSTEM VALUE VALUES (3, 'movie', 'Solaris')`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (30, 3, '5902600069744', 'dvd')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES
		(23, 'owned', 4500), (22, 'wishlist', NULL), (30, 'owned', 2000)`)
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, acquired_as, observed_at, last_seen_at)
		VALUES ('olx', 'k1', 2000, 10, 'used', now(), now()),
		       ('olx', 'k2', 2600, 10, 'used', now(), now()),
		       ('skupszop', '9788308073209', 537, 10, NULL, now(), now()),
		       ('olx', 'b1', 4000, 20, 'used', now(), now()),
		       ('olx', 'b2', 4400, 20, 'used', now(), now()),
		       ('olx', 'b3', 9900, 23, 'new', now(), now()),
		       ('olx', 'b4', 4800, 23, 'used', now(), now()),
		       ('olx', 'b5', 5000, 22, 'used', now(), now()),
		       ('olx', 'old', 100, 23, 'used', now() - interval '100 days', now() - interval '100 days'),
		       ('olx', 'd1', 1500, 21, 'used', now(), now())`)
}

func TestTitlePricesAreTheTitlesShare(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	seedPooledBlurays(t, pool)
	ctx := context.Background()
	window := 90 * 24 * time.Hour
	all, err := prices(ctx, pool, window, 0)
	if err != nil {
		t.Fatalf("prices: %v", err)
	}
	byPrice := func(a, b Price) int {
		return cmp.Or(cmp.Compare(a.EditionID, b.EditionID), cmp.Compare(a.Source, b.Source),
			cmp.Compare(a.Cents, b.Cents))
	}
	for _, title := range []int64{1, 2, 3} {
		want := []Price{}
		for _, p := range all {
			if p.TitleID == title {
				want = append(want, p)
			}
		}
		got, err := prices(ctx, pool, window, title)
		if err != nil {
			t.Fatalf("prices of title %d: %v", title, err)
		}
		slices.SortFunc(want, byPrice)
		slices.SortFunc(got, byPrice)
		if len(got) != len(want) || (len(want) > 0 && !reflect.DeepEqual(got, want)) {
			t.Fatalf("prices of title %d = %+v, want the full read's %+v", title, got, want)
		}
	}
}

func TestTitleCopiesAPI(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	seedPooledBlurays(t, pool)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	runner := NewRunner(store, nil, nil, nil, Config{Window: 90 * 24 * time.Hour}, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger,
		server.Deps{DB: pool, API: []server.Mounter{NewHandler(store, runner, logger)}}))
	t.Cleanup(srv.Close)

	all, _, err := store.Valued(context.Background(), runner.Window())
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	for _, title := range []int64{1, 2, 3} {
		want := []CopyValue{}
		for _, c := range all {
			if c.TitleID == title {
				want = append(want, c)
			}
		}
		var got TitleCopies
		url := srv.URL + "/api/valuation/copies?title_id=" + strconv.FormatInt(title, 10)
		if code := get(t, url, &got); code != http.StatusOK {
			t.Fatalf("GET title %d = %d", title, code)
		}
		if len(got.Copies) == 0 || !reflect.DeepEqual(got.Copies, want) {
			t.Fatalf("title %d copies = %+v, want the full view's %+v", title, got.Copies, want)
		}
	}

	var body map[string]json.RawMessage
	get(t, srv.URL+"/api/valuation/copies?title_id=2", &body)
	if _, ok := body["copies"]; !ok || len(body) != 1 {
		t.Fatalf("title answer keys = %v, want only copies", slices.Collect(maps.Keys(body)))
	}

	var diuna TitleCopies
	get(t, srv.URL+"/api/valuation/copies?title_id=2", &diuna)
	for _, c := range diuna.Copies {
		if c.Mid == nil || c.Mid.Count != 4 {
			t.Fatalf("Diuna copy %d mid = %+v, want the 4 used Blu-ray prices of editions 20, 22 and 23", c.CopyID, c.Mid)
		}
	}
	var solaris TitleCopies
	get(t, srv.URL+"/api/valuation/copies?title_id=3", &solaris)
	if c := solaris.Copies[0]; c.Floor != nil || c.Mid != nil || c.Ceiling != nil {
		t.Fatalf("Solaris copy = %+v, want no value without prices", c)
	}

	var none TitleCopies
	if code := get(t, srv.URL+"/api/valuation/copies?title_id=999", &none); code != http.StatusOK ||
		none.Copies == nil || len(none.Copies) != 0 {
		t.Fatalf("unknown title = %d %+v, want 200 with no copies", code, none)
	}

	for _, raw := range []string{"", "0", "-1", "abc", "1.5", "99999999999999999999"} {
		var body map[string]string
		code := get(t, srv.URL+"/api/valuation/copies?title_id="+raw, &body)
		if code != http.StatusUnprocessableEntity || body["field"] != "title_id" {
			t.Fatalf("title_id %q = %d %v, want 422 naming title_id", raw, code, body)
		}
	}
}

// recordDVDWish stores found as the deal hunter does for seed's wished
// DVD edition 21 of "Diuna": against that edition, whatever the price.
func recordDVDWish(t *testing.T, pool *pgxpool.Pool, found ...deals.Listing) {
	t.Helper()
	dvd := "dvd"
	wish := deals.Want{CopyID: 4, EditionID: 21, TitleID: 2, Kind: "movie", Name: "Diuna", Format: &dvd}
	for i := range found {
		if _, _, err := deals.NewStore(pool).Record(context.Background(), &found[i], &wish, false); err != nil {
			t.Fatal(err)
		}
	}
}

// TestWishMarketPrice prices a wished title and format from the OLX
// listings the hunter stored for it, as the valuation prices a middle:
// used prices within the window, outliers dropped. With title_id it also
// gives an owned format the middle its copies are valued at, and a
// listing first seen before its edition last changed stops counting.
func TestWishMarketPrice(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	logger := slog.New(slog.DiscardHandler)
	store := NewStore(pool)
	runner := NewRunner(store, nil, nil, nil, Config{Window: 90 * 24 * time.Hour}, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger,
		server.Deps{DB: pool, API: []server.Mounter{NewHandler(store, runner, logger)}}))
	t.Cleanup(srv.Close)

	exec(t, pool, `UPDATE edition SET updated_at = now() - interval '200 days' WHERE id = 21`)
	recordDVDWish(t, pool,
		listing("u1", "Diuna DVD", 3000, used()),
		listing("u2", "Diuna DVD PL", 4000, used()),
		listing("u3", "Diuna DVD stan dobry", 4500, nil),
		listing("u4", "Diuna DVD lektor", 5000, used()),
		listing("u5", "Diuna DVD kolekcjonerska", 40000, used()),
		listing("n1", "Diuna DVD nowa w folii", 2000, fresh()))
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, title_id, acquired_as, observed_at, last_seen_at)
		VALUES ('pepper', 'p1', 1000, 21, 2, 'used', now(), now()),
		       ('ceneo', 'c1', 1100, 21, 2, 'used', now(), now()),
		       ('olx', 'old', 1200, 21, 2, 'used', now() - interval '100 days', now() - interval '91 days'),
		       ('olx', 'b1', 4000, 20, 2, 'used', now(), now()),
		       ('olx', 'b2', 4400, 20, 2, 'used', now(), now())`)

	dvd := "dvd"
	dvdMarket := Market{TitleID: 2, Format: &dvd, Estimate: Estimate{Cents: 4250, Source: SourceOLX, Count: 4, Trimmed: 1}}
	var wished MarketView
	if code := get(t, srv.URL+"/api/valuation/market", &wished); code != http.StatusOK {
		t.Fatalf("GET market = %d", code)
	}
	if wished.WindowDays != 90 || !reflect.DeepEqual(wished.Markets, []Market{dvdMarket}) {
		t.Fatalf("wished markets = %+v, want only the DVD at 42,50 zł from 4 listings with 1 dropped", wished)
	}

	var title MarketView
	if code := get(t, srv.URL+"/api/valuation/market?title_id=2", &title); code != http.StatusOK {
		t.Fatalf("GET title market = %d", code)
	}
	owned, err := store.TitleValued(context.Background(), runner.Window(), 2)
	if err != nil || len(owned) == 0 || owned[0].Mid == nil {
		t.Fatalf("TitleValued = %+v, %v; want a valued owned Blu-ray", owned, err)
	}
	bluray := "bluray"
	want := []Market{{TitleID: 2, Format: &bluray, Estimate: *owned[0].Mid}, dvdMarket}
	if !reflect.DeepEqual(title.Markets, want) {
		t.Fatalf("title markets = %+v, want the owned Blu-ray's middle and the DVD's %+v", title.Markets, want)
	}

	exec(t, pool, `UPDATE edition SET updated_at = now() WHERE id = 21`)
	var edited MarketView
	get(t, srv.URL+"/api/valuation/market", &edited)
	if edited.Markets == nil || len(edited.Markets) != 0 {
		t.Fatalf("markets after the wished edition changed = %+v, want none", edited.Markets)
	}

	for _, raw := range []string{"0", "-1", "abc", "1.5", "99999999999999999999"} {
		var body map[string]string
		code := get(t, srv.URL+"/api/valuation/market?title_id="+raw, &body)
		if code != http.StatusUnprocessableEntity || body["field"] != "title_id" {
			t.Fatalf("title_id %q = %d %v, want 422 naming title_id", raw, code, body)
		}
	}
}
