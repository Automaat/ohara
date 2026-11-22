package export

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// TestReadKeepsOneSnapshot changes an owned album's Discogs release and
// stores the new release's prices after the export's snapshot began: the
// export still pairs the old release with no prices, never with the new
// release's floor.
func TestReadKeepsOneSnapshot(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE VALUES (1, 'music', 'Album', '{"A"}')`,
		`INSERT INTO edition (id, title_id, format, discogs_release_id) OVERRIDING SYSTEM VALUE
			VALUES (10, 1, 'vinyl', 101)`,
		`INSERT INTO copy (id, edition_id, status) OVERRIDING SYSTEM VALUE VALUES (100, 10, 'owned')`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatalf("take snapshot: %v", err)
	}
	for _, sql := range []string{
		`UPDATE edition SET discogs_release_id = 202 WHERE id = 10`,
		`INSERT INTO price_observation (source, external_id, price_cents, edition_id, observed_at, last_seen_at)
			VALUES ('discogs_lowest', '202/10', 9900, 10, now(), now())`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("change after the snapshot: %v", err)
		}
	}

	titles, _, err := readIn(ctx, tx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(titles) != 1 || len(titles[0].Editions) != 1 || len(titles[0].Editions[0].Copies) != 1 {
		t.Fatalf("export = %+v, want one album with one copy", titles)
	}
	edition := titles[0].Editions[0]
	if edition.DiscogsReleaseID == nil || *edition.DiscogsReleaseID != 101 {
		t.Fatalf("release = %v, want the snapshot's 101", edition.DiscogsReleaseID)
	}
	if c := edition.Copies[0]; c.Floor != nil || c.Mid != nil || c.Ceiling != nil {
		t.Errorf("copy of release 101 has bounds %+v, %+v, %+v from a later read", c.Floor, c.Mid, c.Ceiling)
	}
}

// TestReadSeesCommittedRelease is the control for TestReadKeepsOneSnapshot:
// read after the change, the new release carries its floor.
func TestReadSeesCommittedRelease(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	for _, sql := range []string{
		`INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE VALUES (1, 'music', 'Album', '{"A"}')`,
		`INSERT INTO edition (id, title_id, format, discogs_release_id) OVERRIDING SYSTEM VALUE
			VALUES (10, 1, 'vinyl', 202)`,
		`INSERT INTO copy (id, edition_id, status) OVERRIDING SYSTEM VALUE VALUES (100, 10, 'owned')`,
		`INSERT INTO price_observation (source, external_id, price_cents, edition_id, observed_at, last_seen_at)
			VALUES ('discogs_lowest', '202/10', 9900, 10, now(), now())`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	titles, _, err := readIn(ctx, tx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	c := titles[0].Editions[0].Copies[0]
	if c.Floor == nil || c.Floor.Cents != 9900 {
		t.Fatalf("floor = %+v, want release 202's 9900", c.Floor)
	}
}
