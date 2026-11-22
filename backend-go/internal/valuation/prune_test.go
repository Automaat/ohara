package valuation

import (
	"context"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// seedPrices stores, for seed's holdings, prices inside the 90-day window
// (OLX used and new, SkupSzop) and outside it, some years old.
func seedPrices(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	exec(t, pool, `INSERT INTO price_observation
			(source, external_id, price_cents, edition_id, title_id, acquired_as, observed_at, last_seen_at)
		VALUES ('olx', 'k1', 2000, 10, 1, 'used', now() - interval '20 days', now() - interval '2 days'),
		       ('olx', 'k2', 2600, 10, 1, 'used', now() - interval '40 days', now() - interval '30 days'),
		       ('olx', 'k3', 2300, 10, 1, 'used', now() - interval '5 days', now() - interval '60 days'),
		       ('olx', 'k-new', 4000, 10, 1, 'new', now(), now()),
		       ('olx', 'k-old', 900, 10, 1, 'used', now() - interval '400 days', now() - interval '200 days'),
		       ('skupszop', '9788308073209', 537, 10, 1, NULL, now() - interval '1 day', now() - interval '1 day'),
		       ('skupszop', 'old-quote', 999, 10, 1, NULL, now() - interval '3 years', now() - interval '3 years'),
		       ('olx', 'd1', 4000, 20, 2, 'used', now() - interval '10 days', now() - interval '10 days'),
		       ('olx', 'd2', 4500, 21, 2, 'used', now() - interval '10 days', now() - interval '10 days'),
		       ('olx', 'd-old', 100, 20, 2, 'used', now() - interval '5 years', now() - interval '5 years')`)
}

// storedListings returns the external IDs of every stored listing.
func storedListings(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	var ids []string
	if err := pool.QueryRow(context.Background(),
		`SELECT coalesce(array_agg(external_id ORDER BY external_id COLLATE "C"), '{}') FROM price_observation`).Scan(&ids); err != nil {
		t.Fatalf("listings: %v", err)
	}
	return ids
}

// startOnce starts r with its scheduled sleeps answered by waits: the
// first sleep returns at once, the second stops the loop. It returns the
// waits asked for.
func startOnce(t *testing.T, r *Runner) []time.Duration {
	t.Helper()
	var waits []time.Duration
	r.sleep = func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) > 1 {
			return context.Canceled
		}
		return nil
	}
	r.Start(t.Context())
	r.Wait()
	return waits
}

func TestRetentionPrunesListings(t *testing.T) {
	const day = 24 * time.Hour
	logger := slog.New(slog.DiscardHandler)

	t.Run("without a retention nothing is deleted", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		seed(t, pool)
		seedPrices(t, pool)
		before := storedListings(t, pool)
		r := NewRunner(NewStore(pool), nil, nil, nil, Config{Window: 90 * day}, logger)
		if waits := startOnce(t, r); len(waits) != 0 {
			t.Fatalf("waits = %v, want no timer without a retention or schedule", waits)
		}
		if after := storedListings(t, pool); !reflect.DeepEqual(after, before) {
			t.Fatalf("listings %v, want all of %v kept", after, before)
		}
	})

	t.Run("a retention deletes listings last seen before it five minutes after start", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		seed(t, pool)
		seedPrices(t, pool)
		r := NewRunner(NewStore(pool), nil, nil, nil, Config{Window: 90 * day, Retention: 100 * day}, logger)
		waits := startOnce(t, r)
		if len(waits) != 2 || waits[0] < 4*time.Minute || waits[0] > 6*time.Minute ||
			waits[1] < 21*time.Hour || waits[1] > 27*time.Hour {
			t.Fatalf("waits = %v, want about 5m, then about a day", waits)
		}
		want := []string{"9788308073209", "d1", "d2", "k-new", "k1", "k2", "k3"}
		if got := storedListings(t, pool); !reflect.DeepEqual(got, want) {
			t.Fatalf("listings %v, want %v", got, want)
		}
	})

	t.Run("pruning at the window leaves the collection's value unchanged", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		seed(t, pool)
		seedPrices(t, pool)
		ctx := t.Context()
		store := NewStore(pool)
		copiesBefore, sumBefore, err := store.Valued(ctx, 90*day)
		if err != nil {
			t.Fatal(err)
		}
		r := NewRunner(store, nil, nil, nil, Config{Window: 90 * day, Retention: 90 * day}, logger)
		r.prune(ctx)
		if got := storedListings(t, pool); len(got) != 7 {
			t.Fatalf("listings %v, want the 3 older than 90 days pruned", got)
		}
		copiesAfter, sumAfter, err := store.Valued(ctx, 90*day)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(copiesAfter, copiesBefore) || !reflect.DeepEqual(sumAfter, sumBefore) {
			t.Fatalf("after pruning %+v %+v, want the value before: %+v %+v", copiesAfter, sumAfter, copiesBefore, sumBefore)
		}
		if sumBefore.Mid.Copies == 0 || sumBefore.Floor.Copies == 0 {
			t.Fatalf("summary %+v values no copy: the comparison proves nothing", sumBefore)
		}
	})
}
