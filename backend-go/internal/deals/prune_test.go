package deals

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// listingSeen stores a listing last seen days ago and returns its id.
func listingSeen(t *testing.T, pool *pgxpool.Pool, externalID string, days int) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `INSERT INTO price_observation
			(source, external_id, price_cents, raw, observed_at, last_seen_at)
		VALUES ('olx', $1, 4000, '{}', now() - make_interval(days => $2), now() - make_interval(days => $2))
		RETURNING id`, externalID, days).Scan(&id); err != nil {
		t.Fatalf("listing %s: %v", externalID, err)
	}
	return id
}

// alertFor queues an alert for the listing id created days ago.
func alertFor(t *testing.T, pool *pgxpool.Pool, id int64, days int, sent bool, attempts int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO deal_alert (observation_id, created_at, sent_at, attempts)
		VALUES ($1, now() - make_interval(days => $2), CASE WHEN $3 THEN now() END, $4)`,
		id, days, sent, attempts); err != nil {
		t.Fatalf("alert for %d: %v", id, err)
	}
}

// listings returns the external IDs still stored, in order.
func listings(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	var ids []string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(array_agg(external_id ORDER BY external_id COLLATE "C"), '{}') FROM price_observation`).Scan(&ids); err != nil {
		t.Fatalf("listings: %v", err)
	}
	return ids
}

func TestPrune(t *testing.T) {
	const day = 24 * time.Hour
	ctx := context.Background()

	t.Run("deletes listings last seen before the retention and keeps newer ones", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		listingSeen(t, pool, "a-recent", 29)
		listingSeen(t, pool, "b-old", 31)
		n, err := NewStore(pool).Prune(ctx, 30*day)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if got := listings(t, pool); n != 1 || len(got) != 1 || got[0] != "a-recent" {
			t.Fatalf("pruned %d, left %v; want 1 pruned and [a-recent] left", n, got)
		}
	})

	t.Run("keeps an old listing whose alert is still pending", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		alertFor(t, pool, listingSeen(t, pool, "pending", 3), 3, false, 0)
		alertFor(t, pool, listingSeen(t, pool, "sent", 3), 3, true, 1)
		alertFor(t, pool, listingSeen(t, pool, "gave-up", 3), 3, false, MaxAttempts)
		alertFor(t, pool, listingSeen(t, pool, "expired", 10), 8, false, 0)
		n, err := NewStore(pool).Prune(ctx, day)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if got := listings(t, pool); n != 3 || len(got) != 1 || got[0] != "pending" {
			t.Fatalf("pruned %d, left %v; want 3 pruned and [pending] left", n, got)
		}
		if alerts := count(t, pool, `SELECT count(*) FROM deal_alert`); alerts != 1 {
			t.Fatalf("%d alerts left, want only the pending one: the others go with their listing", alerts)
		}
	})

	t.Run("deletes more listings than one batch holds", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents, raw, last_seen_at)
			SELECT 'olx', 'old-' || i, 4000, '{}', now() - interval '40 days' FROM generate_series(1, $1) i`,
			pruneBatch+5); err != nil {
			t.Fatal(err)
		}
		listingSeen(t, pool, "recent", 1)
		n, err := NewStore(pool).Prune(ctx, 30*day)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if got := listings(t, pool); n != pruneBatch+5 || len(got) != 1 {
			t.Fatalf("pruned %d, left %d; want %d pruned and the recent one left", n, len(got), pruneBatch+5)
		}
	})

	t.Run("leaves a listing another transaction holds", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		id := listingSeen(t, pool, "held", 40)
		tx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT 1 FROM price_observation WHERE id = $1 FOR UPDATE`, id); err != nil {
			t.Fatal(err)
		}
		n, err := NewStore(pool).Prune(ctx, 30*day)
		if err != nil {
			t.Fatalf("Prune: %v", err)
		}
		if got := listings(t, pool); n != 0 || len(got) != 1 {
			t.Fatalf("pruned %d, left %v; want the held listing kept", n, got)
		}
	})
}
