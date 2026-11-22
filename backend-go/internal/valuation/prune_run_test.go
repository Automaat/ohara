package valuation

import (
	"context"
	"errors"
	"log/slog"
	"reflect"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
)

func TestPruneDuringAValuation(t *testing.T) {
	const day = 24 * time.Hour

	t.Run("a prune between two queries deletes nothing the run saw", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		seed(t, pool)
		exec(t, pool, `INSERT INTO price_observation (source, external_id, price_cents, edition_id, title_id, raw,
				observed_at, last_seen_at)
			VALUES ('olx', 'unpriced', 2000, 10, 1, '{}', now() - interval '200 days', now() - interval '200 days')`)
		store := NewStore(pool)
		olx := &pagedSource{}
		olx.answer(false, nil)
		olx.alsoUnpriced("unpriced")
		var during []error
		olx.onAnswer(func() {
			_, err := store.Prune(context.Background(), 100*day)
			during = append(during, err)
		})
		r := newPagedRunner(pool, olx)
		if _, err := r.Run(t.Context()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if len(during) < 2 {
			t.Fatalf("%d answers, want a prune between at least two queries", len(during))
		}
		for i, err := range during {
			if !errors.Is(err, deals.ErrRunning) {
				t.Fatalf("prune before answer %d = %v, want ErrRunning", i, err)
			}
		}
		r.cfg.Retention = 100 * day
		if !r.prune(t.Context()) {
			t.Fatal("prune after the run reported a run still going")
		}
		if got := storedListings(t, pool); !reflect.DeepEqual(got, []string{"unpriced"}) {
			t.Fatalf("listings %v, want [unpriced]: the run still saw it", got)
		}
	})

	t.Run("a prune that meets a run tries again in ten minutes", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		seed(t, pool)
		exec(t, pool, `INSERT INTO price_observation (source, external_id, price_cents, raw, last_seen_at)
			VALUES ('olx', 'old', 2000, '{}', now() - interval '200 days')`)
		store := NewStore(pool)
		release, err := store.HoldRun(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		r := NewRunner(store, nil, nil, nil, Config{Window: 90 * day, Retention: 100 * day}, slog.New(slog.DiscardHandler))
		var waits []time.Duration
		r.sleep = func(_ context.Context, d time.Duration) error {
			waits = append(waits, d)
			switch len(waits) {
			case 1:
				return nil
			case 2:
				release()
				return nil
			}
			return context.Canceled
		}
		r.Start(t.Context())
		r.Wait()
		if len(waits) != 3 || waits[1] < 9*time.Minute || waits[1] > 11*time.Minute ||
			waits[2] < 21*time.Hour || waits[2] > 27*time.Hour {
			t.Fatalf("waits = %v, want about 5m, about 10m after the busy prune, then about a day", waits)
		}
		if got := storedListings(t, pool); len(got) != 0 {
			t.Fatalf("listings %v, want the old one pruned once the run ended", got)
		}
	})
}
