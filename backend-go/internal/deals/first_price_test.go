package deals

import (
	"context"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func TestRecordKeepsFirstPrice(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	wishlist(t, pool, wantBluray(nil))
	store := NewStore(pool)
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 1 {
		t.Fatalf("Wants = %d, %v", len(wants), err)
	}
	record := func(t *testing.T, source, externalID string, cents int64) {
		t.Helper()
		l := Listing{Source: source, ExternalID: externalID, Title: "Blade Runner Blu-ray", PriceCents: cents}
		if _, _, err := store.Record(ctx, &l, &wants[0], false); err != nil {
			t.Fatalf("Record %s/%s: %v", source, externalID, err)
		}
	}
	firstAndLatest := func(t *testing.T, source, externalID string) (*int64, int64) {
		t.Helper()
		var first *int64
		var latest int64
		if err := pool.QueryRow(ctx, `SELECT first_price_cents, price_cents
			FROM price_observation WHERE source = $1 AND external_id = $2`, source, externalID).
			Scan(&first, &latest); err != nil {
			t.Fatalf("listing %s/%s: %v", source, externalID, err)
		}
		return first, latest
	}

	t.Run("an OLX listing keeps its first price beside its latest", func(t *testing.T) {
		record(t, "olx", "1", 5000)
		record(t, "olx", "1", 4500)
		if first, latest := firstAndLatest(t, "olx", "1"); first == nil || *first != 5000 || latest != 4500 {
			t.Fatalf("seen at 50 then 45 zł: first %v, latest %d; want 5000 and 4500", first, latest)
		}
	})

	t.Run("a listing stored before the change keeps an unknown first price", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents, raw)
			VALUES ('olx', 'old', 6000, '{}')`); err != nil {
			t.Fatal(err)
		}
		record(t, "olx", "old", 5500)
		if first, latest := firstAndLatest(t, "olx", "old"); first != nil || latest != 5500 {
			t.Fatalf("first %v, latest %d; want unknown and 5500", first, latest)
		}
	})

	t.Run("a Pepper listing keeps no first price", func(t *testing.T) {
		record(t, "pepper", "p", 3000)
		if first, _ := firstAndLatest(t, "pepper", "p"); first != nil {
			t.Fatalf("first %d, want none", *first)
		}
	})
}
