package deals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// unpricedPager is an OLX-like Pager whose answer holds priced listings
// and offers without a price, named only in Seen.
type unpricedPager struct {
	listings []Listing
	unpriced []string
}

func (unpricedPager) Name() string   { return "olx" }
func (unpricedPager) PerQuery() bool { return true }

func (p unpricedPager) Search(ctx context.Context, q string) ([]Listing, error) {
	page, err := p.SearchPage(ctx, q)
	return page.Listings, err
}

func (p unpricedPager) SearchPage(_ context.Context, q string) (Page, error) {
	listings := slices.Clone(p.listings)
	seen := slices.Clone(p.unpriced)
	for i := range listings {
		listings[i].Query = q
		seen = append(seen, listings[i].ExternalID)
	}
	return Page{Listings: listings, Seen: seen}, nil
}

// pepperServer serves the Pepper fixture: a priced Blade Runner deal and a
// voucher without a merchant or price.
func pepperServer(t *testing.T) Pepper {
	t.Helper()
	body := fixture(t, "pepper.xml")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return Pepper{Client: srv.Client(), BaseURL: srv.URL}
}

// oldListing stores a listing of source last seen 40 days ago.
func oldListing(t *testing.T, pool *pgxpool.Pool, source, externalID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `INSERT INTO price_observation
			(source, external_id, price_cents, raw, observed_at, last_seen_at)
		VALUES ($1, $2, 4000, '{}', now() - interval '40 days', now() - interval '40 days')`,
		source, externalID); err != nil {
		t.Fatalf("listing %s/%s: %v", source, externalID, err)
	}
}

func hunterWithCanary(pool *pgxpool.Pool, sources ...Source) *Hunter {
	h := NewHunter(NewStore(pool), sources, nil, Config{CanaryQuery: "dvd"}, slog.New(slog.DiscardHandler))
	h.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	return h
}

func pruneAt30Days(t *testing.T, pool *pgxpool.Pool) []string {
	t.Helper()
	if _, err := NewStore(pool).Prune(context.Background(), 30*24*time.Hour); err != nil {
		t.Fatalf("Prune: %v", err)
	}
	return listings(t, pool)
}

const (
	pepperVoucher = "https://www.pepper.pl/kupony/kod-1"
	pepperFilm    = "https://www.pepper.pl/promocje/lowca-androidow-4k-1392999"
)

func TestHuntKeepsListingsItsAnswersHold(t *testing.T) {
	t.Run("an OLX answer's offer without a price", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		wishlist(t, pool, wantBluray(nil))
		oldListing(t, pool, "olx", "unpriced")
		oldListing(t, pool, "olx", "unseen")
		src := unpricedPager{unpriced: []string{"unpriced"}}
		mustRun(t, newTestHunter(pool, nil, src))
		if got := pruneAt30Days(t, pool); !slices.Equal(got, []string{"unpriced"}) {
			t.Fatalf("listings %v, want [unpriced]: the hunt's answer still offered it", got)
		}
	})

	t.Run("a Pepper deal without a merchant or price", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		wishlist(t, pool, wantBluray(nil))
		oldListing(t, pool, "pepper", pepperVoucher)
		oldListing(t, pool, "pepper", "https://www.pepper.pl/promocje/gone-1")
		mustRun(t, newTestHunter(pool, nil, pepperServer(t)))
		if got := pruneAt30Days(t, pool); !slices.Contains(got, pepperVoucher) || slices.Contains(got, "https://www.pepper.pl/promocje/gone-1") {
			t.Fatalf("listings %v, want the voucher the feed holds kept and the gone deal pruned", got)
		}
	})

	t.Run("a Pepper feed answer with an empty wishlist", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		oldListing(t, pool, "pepper", pepperFilm)
		oldListing(t, pool, "pepper", "https://www.pepper.pl/promocje/gone-1")
		res := mustRun(t, hunterWithCanary(pool, pepperServer(t)))
		if res.Wants != 0 {
			t.Fatalf("run = %+v, want no wishes", res)
		}
		if got := pruneAt30Days(t, pool); !slices.Contains(got, pepperFilm) || slices.Contains(got, "https://www.pepper.pl/promocje/gone-1") {
			t.Fatalf("listings %v, want the deal the feed holds kept and the gone deal pruned", got)
		}
	})

	t.Run("an OLX canary answer with an empty wishlist", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		oldListing(t, pool, "olx", "canary")
		src := unpricedPager{unpriced: []string{"canary"}}
		mustRun(t, hunterWithCanary(pool, src))
		if got := pruneAt30Days(t, pool); !slices.Equal(got, []string{"canary"}) {
			t.Fatalf("listings %v, want [canary]: the canary answer still offered it", got)
		}
	})
}

func TestPruneWaitsForRuns(t *testing.T) {
	ctx := context.Background()

	t.Run("deletes nothing while a run holds the run lock", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		oldListing(t, pool, "olx", "old")
		store := NewStore(pool)
		release, err := store.HoldRun(ctx)
		if err != nil {
			t.Fatalf("HoldRun: %v", err)
		}
		t.Cleanup(release)
		n, err := store.Prune(ctx, 30*24*time.Hour)
		if !errors.Is(err, ErrRunning) || n != 0 {
			t.Fatalf("Prune during a run = %d, %v; want 0, ErrRunning", n, err)
		}
		release()
		if got := pruneAt30Days(t, pool); len(got) != 0 {
			t.Fatalf("listings %v after the run, want the old one pruned", got)
		}
	})

	t.Run("defers to a run whose lock session ended", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		oldListing(t, pool, "olx", "old")
		store := NewStore(pool)
		release, err := store.HoldRun(ctx)
		if err != nil {
			t.Fatalf("HoldRun: %v", err)
		}
		t.Cleanup(release)
		var ended int
		if err := pool.QueryRow(ctx, `SELECT count(pg_terminate_backend(l.pid)) FROM pg_locks l
			WHERE l.locktype = 'advisory' AND l.mode = 'ShareLock'
				AND (l.classid::bigint << 32 | l.objid::bigint) = (`+runLock+` & x'ffffffffffffffff'::bigint)`).
			Scan(&ended); err != nil {
			t.Fatal(err)
		}
		if ended != 1 {
			t.Fatalf("ended %d lock sessions, want the run's one", ended)
		}
		n, err := store.Prune(ctx, 30*24*time.Hour)
		if !errors.Is(err, ErrRunning) || n != 0 {
			t.Fatalf("Prune while the run goes on without its lock = %d, %v; want 0, ErrRunning", n, err)
		}
		release()
		release()
		if got := pruneAt30Days(t, pool); len(got) != 0 {
			t.Fatalf("listings %v after the run, want the old one pruned", got)
		}
	})

	t.Run("two runs hold the lock together", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		store := NewStore(pool)
		first, err := store.HoldRun(ctx)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(first)
		timeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		second, err := store.HoldRun(timeout)
		if err != nil {
			t.Fatalf("second HoldRun while the first holds: %v", err)
		}
		second()
	})

	t.Run("a hunt prunes nothing between its answers", func(t *testing.T) {
		pool := dbtest.Migrated(t)
		wishlist(t, pool, wantBluray(nil))
		oldListing(t, pool, "olx", "old")
		store := NewStore(pool)
		var during error
		src := &duringSource{during: func() { _, during = store.Prune(ctx, 30*24*time.Hour) }}
		mustRun(t, newTestHunter(pool, nil, src))
		if !errors.Is(during, ErrRunning) {
			t.Fatalf("Prune during the hunt = %v, want ErrRunning", during)
		}
	})
}

// duringSource answers nothing and runs during on each request.
type duringSource struct{ during func() }

func (*duringSource) Name() string   { return "olx" }
func (*duringSource) PerQuery() bool { return true }

func (s *duringSource) Search(context.Context, string) ([]Listing, error) {
	s.during()
	return nil, nil
}

func TestGoneKeepsAListingANewerAnswerOffered(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	since, err := store.Now(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO price_observation
			(source, external_id, price_cents, raw, last_seen_at, last_offered_at)
		VALUES ('olx', 'offered', 4000, '{"query": "blade runner"}', now() - interval '2 days', now() + interval '1 second'),
		       ('olx', 'gone', 4000, '{"query": "blade runner"}', now() - interval '2 days', NULL)`); err != nil {
		t.Fatal(err)
	}
	n, err := store.UpdateGone(ctx, "olx", []string{"blade runner"}, nil, since)
	if err != nil {
		t.Fatalf("UpdateGone: %v", err)
	}
	var gone []string
	if err := pool.QueryRow(ctx, `SELECT coalesce(array_agg(external_id), '{}') FROM price_observation
		WHERE gone_at IS NOT NULL`).Scan(&gone); err != nil {
		t.Fatal(err)
	}
	if n != 1 || !slices.Equal(gone, []string{"gone"}) {
		t.Fatalf("marked %d gone: %v; want only the listing no answer offered after the run started", n, gone)
	}
}
