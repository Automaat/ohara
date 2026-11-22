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

// seedCollection holds the collection the seeds store listings for: 2000
// owned editions and 50 wished ones, each wish with a 50 zł limit, listed
// in bench_owned and bench_wished by n.
var seedCollection = []string{
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
}

// benchListings is SQL inserting benchObservations listings, newest last,
// each recorded for the bench_wished wish that wishN picks (none when it
// is null) or else for the bench_owned edition that ownedN picks, at
// priceCents. Each raw carries a listing record of about 2 kB, like an OLX
// ad with its description.
func benchListings(wishN, ownedN, priceCents string) string {
	n := strconv.Itoa(benchObservations)
	return `INSERT INTO price_observation (source, external_id, observed_at, last_seen_at, price_cents, url, edition_id, title_id, raw)
		SELECT CASE WHEN g % 7 = 0 THEN 'pepper' ELSE 'olx' END, 'bench-' || g,
			now() - (` + n + ` - g) * interval '1 minute',
			now() - (` + n + ` - g) * interval '1 minute',
			` + priceCents + `,
			'https://www.olx.pl/d/oferta/bench-' || g || '.html',
			coalesce(w.edition_id, o.edition_id), coalesce(w.title_id, o.title_id),
			jsonb_build_object('title', 'Listing ' || g, 'photo_url', 'https://images.example.test/' || g || '.jpg',
				'data', jsonb_build_object('description', (SELECT string_agg(md5(g::text || '-' || i), ' ')
					FROM generate_series(1, 60) i)))
			|| CASE WHEN w.copy_id IS NULL THEN '{}'::jsonb ELSE jsonb_build_object('wishlist_copy_id', w.copy_id) END
		FROM generate_series(1, ` + n + `) g
		LEFT JOIN bench_owned o ON o.n = ` + ownedN + `
		LEFT JOIN bench_wished w ON w.n = ` + wishN
}

// seed runs the seedCollection queries, then queries, then ANALYZE, and
// returns a wish to record for.
func seed(tb testing.TB, pool *pgxpool.Pool, queries ...string) *Want {
	tb.Helper()
	ctx := context.Background()
	for _, q := range append(append(append([]string{}, seedCollection...), queries...), `ANALYZE`) {
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

// seedObservations fills an empty schema with seedCollection and
// benchObservations listings: nine in ten are valuation listings of owned
// editions, the rest were recorded for a wish, and one in a hundred is
// under its wish's limit and has an alert, every other one of them sent.
// It returns a wish to record for.
func seedObservations(tb testing.TB, pool *pgxpool.Pool) *Want {
	tb.Helper()
	return seed(tb, pool,
		benchListings(`CASE WHEN g % 10 = 0 THEN (g / 10) % 50 END`, `CASE WHEN g % 10 <> 0 THEN g % 2000 END`,
			`CASE WHEN g % 10 <> 0 THEN 1000 + g % 9000 WHEN g % 100 = 0 THEN 4000 ELSE 6000 END`),
		`INSERT INTO deal_alert (observation_id, copy_id, created_at, sent_at, attempts)
			SELECT o.id, (o.raw->>'wishlist_copy_id')::bigint, o.observed_at,
				CASE WHEN o.id % 2 = 0 THEN o.observed_at END, CASE WHEN o.id % 2 = 0 THEN 1 ELSE 0 END
			FROM price_observation o WHERE o.raw ? 'wishlist_copy_id' AND o.price_cents = 4000`)
}

// seedDenseObservations fills an empty schema with seedCollection and
// benchObservations listings that were all recorded for one of the 50
// wishes, all under its limit and each with a sent alert: the history of
// a long-running hunt with Telegram alerts and few owned editions. It
// returns a wish to record for.
func seedDenseObservations(tb testing.TB, pool *pgxpool.Pool) *Want {
	tb.Helper()
	return seed(tb, pool,
		benchListings(`g % 50`, `NULL`, `4000`),
		`INSERT INTO deal_alert (observation_id, copy_id, created_at, sent_at, attempts)
			SELECT o.id, o.wishlist_copy_id, o.observed_at, o.observed_at, 1 FROM price_observation o`)
}

// BenchmarkRecent times the deals lists the app asks for, on
// benchObservations stored listings: those of seedObservations, then as
// dense/ those of seedDenseObservations, which every filter keeps.
//
//	OHARA_TEST_DATABASE_URL=... go test ./internal/deals -run '^$' -bench Recent
func BenchmarkRecent(b *testing.B) {
	ctx := context.Background()
	for _, history := range []struct {
		prefix string
		seed   func(testing.TB, *pgxpool.Pool) *Want
	}{
		{prefix: "", seed: seedObservations},
		{prefix: "dense/", seed: seedDenseObservations},
	} {
		pool := dbtest.Migrated(b)
		history.seed(b, pool)
		store := NewStore(pool)
		for _, tc := range []struct {
			name   string
			filter RecentFilter
		}{
			{name: "all", filter: RecentFilter{Limit: 500}},
			{name: "under_limit", filter: RecentFilter{Limit: 500, UnderLimit: true}},
			{name: "matched", filter: RecentFilter{Limit: 500, Matched: true}},
			{name: "sent", filter: RecentFilter{Limit: 500, Sent: true}},
		} {
			b.Run(history.prefix+tc.name, func(b *testing.B) {
				for b.Loop() {
					if _, err := store.Recent(ctx, tc.filter); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
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
