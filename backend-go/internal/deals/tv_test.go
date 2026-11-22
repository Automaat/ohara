package deals

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
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
