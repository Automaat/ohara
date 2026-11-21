package migrations_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// TestObservationWishMigration stores a listing recorded for a wish and a
// valuation listing before 0015, then applies it: the first gets its
// wishlist copy as a column, the second none, and raw stays as it was.
func TestObservationWishMigration(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	all, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(all), func(m db.Migration) bool { return m.Version >= "0015" })
	if len(before) == len(all) {
		t.Fatal("no migration 0015")
	}
	if _, err := db.Migrate(ctx, pool, before); err != nil {
		t.Fatalf("migrate before 0015: %v", err)
	}
	const wished = `{"title": "Blade Runner", "wishlist_copy_id": 42}`
	if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents, raw)
		VALUES ('olx', 'wished', 2500, $1), ('olx', 'valued', 3000, '{"title": "Dune"}'), ('skupszop', 'bare', 500, NULL)`,
		wished); err != nil {
		t.Fatalf("observations: %v", err)
	}
	if _, err := db.Migrate(ctx, pool, all); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT external_id, wishlist_copy_id, raw = $1::jsonb
		FROM price_observation ORDER BY external_id`, wished)
	if err != nil {
		t.Fatalf("observations: %v", err)
	}
	defer rows.Close()
	type row struct {
		id      string
		copyID  *int64
		sameRaw *bool
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.copyID, &r.sameRaw); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 3 || got[0].id != "bare" || got[0].copyID != nil || got[0].sameRaw != nil ||
		got[1].id != "valued" || got[1].copyID != nil ||
		got[2].id != "wished" || got[2].copyID == nil || *got[2].copyID != 42 || got[2].sameRaw == nil || !*got[2].sameRaw {
		t.Fatalf("observations after 0015 = %+v; want wished with copy 42 and its raw, the others without a copy", got)
	}
}
