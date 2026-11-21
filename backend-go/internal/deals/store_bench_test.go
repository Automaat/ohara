package deals

import (
	"context"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// benchObservations is how many listings seedObservations stores.
const benchObservations = 100_000

// seedObservations fills an empty schema with a collection of 2000 owned
// editions and 50 wished ones, then benchObservations listings, newest last:
// nine in ten are valuation listings of owned editions, the rest were
// recorded for a wish, and one in a hundred is under its wish's limit and
// has an alert, every other one of them sent. Each raw carries a listing
// record of about 2 kB, like an OLX ad with its description. It returns a wish to record for.
func seedObservations(tb testing.TB, pool *pgxpool.Pool) *Want {
	tb.Helper()
	ctx := context.Background()
	for _, q := range []string{
		`INSERT INTO title (kind, name) SELECT 'movie', 'Owned ' || g FROM generate_series(1, 2000) g`,
		`INSERT INTO title (kind, name) SELECT 'movie', 'Wished ' || g FROM generate_series(1, 50) g`,
		`INSERT INTO edition (title_id, format) SELECT id, 'bluray' FROM title ORDER BY id`,
		`INSERT INTO copy (edition_id, status) SELECT e.id, 'owned' FROM edition e
			JOIN title t ON t.id = e.title_id WHERE t.name LIKE 'Owned %'`,
		`INSERT INTO copy (edition_id, status, max_price_cents) SELECT e.id, 'wishlist', 5000 FROM edition e
			JOIN title t ON t.id = e.title_id WHERE t.name LIKE 'Wished %'`,
		`CREATE TEMP TABLE bench_owned AS SELECT row_number() OVER (ORDER BY c.id) - 1 AS n, c.edition_id, e.title_id
			FROM copy c JOIN edition e ON e.id = c.edition_id WHERE c.status = 'owned'`,
		`CREATE TEMP TABLE bench_wished AS SELECT row_number() OVER (ORDER BY c.id) - 1 AS n, c.id AS copy_id, c.edition_id, e.title_id
			FROM copy c JOIN edition e ON e.id = c.edition_id WHERE c.status = 'wishlist'`,
		`INSERT INTO price_observation (source, external_id, observed_at, last_seen_at, price_cents, url, edition_id, title_id, raw)
			SELECT CASE WHEN g % 7 = 0 THEN 'pepper' ELSE 'olx' END, 'bench-' || g,
				now() - (` + strconv.Itoa(benchObservations) + ` - g) * interval '1 minute',
				now() - (` + strconv.Itoa(benchObservations) + ` - g) * interval '1 minute',
				CASE WHEN g % 10 <> 0 THEN 1000 + g % 9000 WHEN g % 100 = 0 THEN 4000 ELSE 6000 END,
				'https://www.olx.pl/d/oferta/bench-' || g || '.html',
				coalesce(w.edition_id, o.edition_id), coalesce(w.title_id, o.title_id),
				jsonb_build_object('title', 'Listing ' || g, 'photo_url', 'https://images.example.test/' || g || '.jpg',
					'data', jsonb_build_object('description', (SELECT string_agg(md5(g::text || '-' || i), ' ')
						FROM generate_series(1, 60) i)))
				|| CASE WHEN w.copy_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('wishlist_copy_id', w.copy_id) END
			FROM generate_series(1, ` + strconv.Itoa(benchObservations) + `) g
			LEFT JOIN bench_owned o ON g % 10 <> 0 AND o.n = g % 2000
			LEFT JOIN bench_wished w ON g % 10 = 0 AND w.n = (g / 10) % 50`,
		`INSERT INTO deal_alert (observation_id, copy_id, created_at, sent_at, attempts)
			SELECT o.id, (o.raw->>'wishlist_copy_id')::bigint, o.observed_at,
				CASE WHEN o.id % 2 = 0 THEN o.observed_at END, CASE WHEN o.id % 2 = 0 THEN 1 ELSE 0 END
			FROM price_observation o WHERE o.raw ? 'wishlist_copy_id' AND o.price_cents = 4000`,
		`ANALYZE`,
	} {
		if _, err := pool.Exec(ctx, q); err != nil {
			tb.Fatalf("seed: %v\n%s", err, q)
		}
	}
	var w Want
	if err := pool.QueryRow(ctx, `SELECT copy_id, edition_id, title_id FROM bench_wished WHERE n = 0`).
		Scan(&w.CopyID, &w.EditionID, &w.TitleID); err != nil {
		tb.Fatalf("seed wish: %v", err)
	}
	return &w
}

// BenchmarkRecent times the deals lists the app asks for, on
// benchObservations stored listings:
//
//	OHARA_TEST_DATABASE_URL=... go test ./internal/deals -run '^$' -bench Recent
func BenchmarkRecent(b *testing.B) {
	pool := dbtest.Migrated(b)
	seedObservations(b, pool)
	store := NewStore(pool)
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		filter RecentFilter
	}{
		{name: "all", filter: RecentFilter{Limit: 500}},
		{name: "under_limit", filter: RecentFilter{Limit: 500, UnderLimit: true}},
		{name: "matched", filter: RecentFilter{Limit: 500, Matched: true}},
		{name: "sent", filter: RecentFilter{Limit: 500, Sent: true}},
	} {
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := store.Recent(ctx, tc.filter); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRecord times storing a new listing for a wish, with its alert,
// next to benchObservations stored listings.
func BenchmarkRecord(b *testing.B) {
	pool := dbtest.Migrated(b)
	want := seedObservations(b, pool)
	store := NewStore(pool)
	ctx := context.Background()
	n := 0
	for b.Loop() {
		n++
		l := Listing{
			Source: "olx", ExternalID: "record-" + strconv.Itoa(n), Title: "Wished 1 Blu-ray",
			PriceCents: 4500, URL: "https://www.olx.pl/d/oferta/record.html", Raw: []byte(`{"id":1}`),
		}
		if _, _, err := store.Record(ctx, &l, want, true); err != nil {
			b.Fatal(err)
		}
	}
}
