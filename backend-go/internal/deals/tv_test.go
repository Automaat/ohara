package deals

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// wishTVSeason stores a wished Blu-ray of a TV season named name, with a
// price limit.
func wishTVSeason(t *testing.T, pool *pgxpool.Pool, name string) {
	t.Helper()
	var copyID int64
	err := pool.QueryRow(context.Background(), `WITH t AS (
			INSERT INTO title (kind, name, season, tmdb_id) VALUES ('tv', $1, 1, 1399) RETURNING id
		), e AS (
			INSERT INTO edition (title_id, format) SELECT id, 'bluray' FROM t RETURNING id
		)
		INSERT INTO copy (edition_id, status, max_price_cents) SELECT id, 'wishlist', 100000 FROM e RETURNING id`,
		name).Scan(&copyID)
	if err != nil {
		t.Fatalf("TV wish: %v", err)
	}
}

func TestWantsLeaveTVSeasonsOut(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishTVSeason(t, pool, "Blade Runner")
	movie := wishlist(t, pool, wantBluray(new(int64(5000))))
	wants, err := NewStore(pool).Wants(context.Background())
	if err != nil || len(wants) != 1 || wants[0].CopyID != movie {
		t.Fatalf("Wants = %+v, %v, want only the film", wants, err)
	}
}

func TestSearchedWishesLeaveTVSeasonsOut(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishTVSeason(t, pool, "Dark")
	store := NewStore(pool)
	logger := slog.New(slog.DiscardHandler)
	hunter := NewHunter(store, nil, nil, Config{}, logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{
		DB: pool, API: []server.Mounter{NewHandler(store, hunter, logger)},
	}))
	t.Cleanup(srv.Close)
	var view View
	if code := call(t, http.MethodGet, srv.URL+"/api/deals", nil, &view); code != http.StatusOK ||
		view.Wishes != 1 || view.SearchedWishes != 0 {
		t.Fatalf("GET deals = %d, wishes %d, searched %d, want 1 wish and none searched", code, view.Wishes, view.SearchedWishes)
	}
	wishlist(t, pool, wantBluray(nil))
	if code := call(t, http.MethodGet, srv.URL+"/api/deals", nil, &view); code != http.StatusOK ||
		view.Wishes != 2 || view.SearchedWishes != 1 {
		t.Fatalf("GET deals = %d, wishes %d, searched %d, want 2 wishes and the film searched", code, view.Wishes, view.SearchedWishes)
	}
}

func TestRunSearchesNoListingForATVSeason(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishTVSeason(t, pool, "Blade Runner")
	olx := olxFixture(t)
	notifier := &fakeNotifier{}
	res := mustRun(t, newTestHunter(pool, notifier, olx))
	if res.Wants != 0 || res.Matched != 0 || res.Alerts != 0 || len(olx.queries) != 0 {
		t.Fatalf("run = %+v, queries %q, want no search for a TV season", res, olx.queries)
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation`); n != 0 {
		t.Fatalf("listings stored = %d", n)
	}
}

func TestAWishMovedToATVSeasonLeavesItsListingsAndAlerts(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	copyID := wishlist(t, pool, wantBluray(new(int64(6000))))
	store := NewStore(pool)
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 1 {
		t.Fatalf("Wants = %d, %v", len(wants), err)
	}
	l := Listing{Source: "olx", ExternalID: "1", Title: "Blade Runner Blu-ray", PriceCents: 4000}
	if _, _, err := store.Record(ctx, &l, &wants[0], true); err != nil {
		t.Fatalf("Record: %v", err)
	}
	listed := func() (int, int) {
		t.Helper()
		matched, err := store.Recent(ctx, RecentFilter{Limit: 50, Matched: true})
		if err != nil {
			t.Fatalf("Recent: %v", err)
		}
		all, err := store.Recent(ctx, RecentFilter{Limit: 50})
		if err != nil {
			t.Fatalf("Recent: %v", err)
		}
		wished := 0
		for _, o := range all {
			if o.MaxPriceCents != nil {
				wished++
			}
		}
		return len(matched), wished
	}
	pending := func() int {
		t.Helper()
		alerts, err := store.PendingAlerts(ctx)
		if err != nil {
			t.Fatalf("PendingAlerts: %v", err)
		}
		return len(alerts)
	}
	if matched, wished := listed(); matched != 1 || wished != 1 || pending() != 1 {
		t.Fatalf("film wish: matched %d, wished in all %d, pending %d, want 1 each", matched, wished, pending())
	}

	if _, err := pool.Exec(ctx, `WITH t AS (
			INSERT INTO title (kind, name, season) VALUES ('tv', 'Nocny Serial', 1) RETURNING id
		), e AS (
			INSERT INTO edition (title_id, format) SELECT id, 'bluray' FROM t RETURNING id
		)
		UPDATE copy SET edition_id = (SELECT id FROM e) WHERE id = $1`, copyID); err != nil {
		t.Fatalf("move wish: %v", err)
	}
	if matched, wished := listed(); matched != 0 || wished != 0 || pending() != 0 {
		t.Fatalf("TV wish: matched %d, wished in all %d, pending %d, want none", matched, wished, pending())
	}
}
