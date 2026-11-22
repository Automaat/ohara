package valuation

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
)

// pagedSource is an OLX double that answers every query with the same
// page, its listings carrying the query, or with err. unpriced are offers
// the page holds without a listing; during runs before each answer.
type pagedSource struct {
	mu       sync.Mutex
	listings []deals.Listing
	unpriced []string
	complete bool
	err      error
	during   func()
}

func (*pagedSource) Name() string   { return SourceOLX }
func (*pagedSource) PerQuery() bool { return true }

func (p *pagedSource) Search(ctx context.Context, q string) ([]deals.Listing, error) {
	page, err := p.SearchPage(ctx, q)
	return page.Listings, err
}

func (p *pagedSource) SearchPage(_ context.Context, q string) (deals.Page, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.during != nil {
		p.during()
	}
	if p.err != nil {
		return deals.Page{}, p.err
	}
	listings := slices.Clone(p.listings)
	seen := slices.Clone(p.unpriced)
	for i := range listings {
		listings[i].Query = q
		seen = append(seen, listings[i].ExternalID)
	}
	return deals.Page{Listings: listings, Seen: seen, Complete: p.complete}, nil
}

func (p *pagedSource) answer(complete bool, err error, listings ...deals.Listing) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.listings, p.complete, p.err, p.unpriced, p.during = listings, complete, err, nil, nil
}

func (p *pagedSource) alsoUnpriced(ids ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.unpriced = ids
}

func (p *pagedSource) onAnswer(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.during = f
}

type goneState struct {
	gone     bool
	lastSeen time.Time
}

func goneStates(t *testing.T, pool *pgxpool.Pool) map[string]goneState {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT external_id, gone_at IS NOT NULL, last_seen_at
		FROM price_observation WHERE source = 'olx'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]goneState{}
	for rows.Next() {
		var id string
		var s goneState
		if err := rows.Scan(&id, &s.gone, &s.lastSeen); err != nil {
			t.Fatal(err)
		}
		out[id] = s
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestListingsGone(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	ctx := context.Background()
	exec(t, pool, `INSERT INTO price_observation (source, external_id, price_cents, edition_id, title_id, raw)
		VALUES ('olx', 'legacy', 2200, 10, 1, '{"title": "Katedra Dukaj"}')`)
	olx := &pagedSource{}
	r := newPagedRunner(pool, olx)
	run := func(t *testing.T) {
		t.Helper()
		if _, err := r.Run(ctx); err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	a := listing("a", "Katedra Jacek Dukaj", 2000, used())
	b := listing("b", "Katedra Dukaj stan dobry", 2500, used())
	want := func(t *testing.T, gone ...string) map[string]goneState {
		t.Helper()
		states := goneStates(t, pool)
		for id, s := range states {
			if s.gone != slices.Contains(gone, id) {
				t.Fatalf("listing %s gone %v; want gone exactly %v", id, s.gone, gone)
			}
		}
		return states
	}

	olx.answer(true, nil, a, b)
	run(t)
	want(t)
	exec(t, pool, `UPDATE price_observation SET last_seen_at = last_seen_at - interval '1 day'`)
	before := goneStates(t, pool)

	t.Run("a listing missing from a first page is not gone", func(t *testing.T) {
		olx.answer(false, nil, a)
		run(t)
		want(t)
	})

	t.Run("a listing missing from a failed query is not gone", func(t *testing.T) {
		olx.answer(true, errors.New("olx: status 503"))
		run(t)
		want(t)
	})

	t.Run("a listing still offered but no longer matching is not gone", func(t *testing.T) {
		exec(t, pool, `INSERT INTO deal_keyword (kind, phrase) VALUES ('reject', 'stan')`)
		olx.answer(true, nil, a, b)
		run(t)
		exec(t, pool, `DELETE FROM deal_keyword WHERE phrase = 'stan'`)
		want(t)
	})

	t.Run("a listing still offered without a price is not gone", func(t *testing.T) {
		olx.answer(true, nil, a)
		olx.alsoUnpriced("b")
		run(t)
		want(t)
	})

	t.Run("a listing another run records during the valuation is not gone", func(t *testing.T) {
		olx.answer(true, nil, a)
		olx.onAnswer(func() {
			exec(t, pool, `UPDATE price_observation SET last_seen_at = now() WHERE external_id = 'b'`)
		})
		run(t)
		want(t)
		exec(t, pool, `UPDATE price_observation SET last_seen_at = last_seen_at - interval '1 day'
			WHERE external_id = 'b'`)
		before = goneStates(t, pool)
	})

	t.Run("a listing missing from a complete answer is gone and keeps its last sighting", func(t *testing.T) {
		olx.answer(true, nil, a)
		run(t)
		states := want(t, "b")
		if !states["b"].lastSeen.Equal(before["b"].lastSeen) {
			t.Fatalf("gone listing last seen %v, want %v kept", states["b"].lastSeen, before["b"].lastSeen)
		}
	})

	t.Run("a gone listing still prices its edition", func(t *testing.T) {
		prices, err := NewStore(pool).Prices(ctx, 90*24*time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.ContainsFunc(prices, func(p Price) bool { return p.Cents == 2500 }) {
			t.Fatalf("Prices = %+v, want the gone listing's 2500", prices)
		}
	})

	t.Run("a gone listing offered again but no longer matching is no longer gone", func(t *testing.T) {
		exec(t, pool, `INSERT INTO deal_keyword (kind, phrase) VALUES ('reject', 'stan')`)
		olx.answer(true, nil, a, b)
		run(t)
		exec(t, pool, `DELETE FROM deal_keyword WHERE phrase = 'stan'`)
		want(t)
	})

	t.Run("a gone listing offered again is no longer gone", func(t *testing.T) {
		olx.answer(true, nil, a)
		run(t)
		want(t, "b")
		olx.answer(true, nil, a, b)
		run(t)
		want(t)
	})
}

func newPagedRunner(pool *pgxpool.Pool, src deals.Source) *Runner {
	r := newTestRunner(pool, &fakeBuyBack{})
	r.sources = []deals.Source{src}
	return r
}
