// Package migrations_test checks what a migration does to existing rows.
package migrations_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// TestRemoveVintedMigration stores a Vinted and an OLX listing, each with a
// deal alert, before 0013, then applies it: only the OLX listing and its
// alert stay.
func TestRemoveVintedMigration(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	all, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(all), func(m db.Migration) bool { return m.Version >= "0013" })
	if len(before) == len(all) {
		t.Fatal("no migration 0013")
	}
	if _, err := db.Migrate(ctx, pool, before); err != nil {
		t.Fatalf("migrate to 0012: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents)
		VALUES ('vinted', '10464769656', 25000), ('olx', '900', 2500)`); err != nil {
		t.Fatalf("observations: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deal_alert (observation_id) SELECT id FROM price_observation`); err != nil {
		t.Fatalf("alerts: %v", err)
	}
	if _, err := db.Migrate(ctx, pool, all); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var sources []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(source ORDER BY source) FROM price_observation`).Scan(&sources); err != nil {
		t.Fatalf("observations: %v", err)
	}
	var alerts []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(o.source ORDER BY o.source) FROM deal_alert a
		JOIN price_observation o ON o.id = a.observation_id`).Scan(&alerts); err != nil {
		t.Fatalf("alerts: %v", err)
	}
	var orphans int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM deal_alert`).Scan(&orphans); err != nil {
		t.Fatalf("alert count: %v", err)
	}
	if !slices.Equal(sources, []string{"olx"}) || !slices.Equal(alerts, []string{"olx"}) || orphans != 1 {
		t.Fatalf("observations %v, alerts %v (%d rows); want only the OLX listing and its alert", sources, alerts, orphans)
	}
}
