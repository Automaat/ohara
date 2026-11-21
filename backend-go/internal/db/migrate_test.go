package db_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func TestLoadMigrations(t *testing.T) {
	tests := []struct {
		name     string
		files    fstest.MapFS
		versions []string
		wantErr  error
	}{
		{
			name: "sorted by version",
			files: fstest.MapFS{
				"0002_items.sql": {Data: []byte("SELECT 2;")},
				"0001_init.sql":  {Data: []byte("SELECT 1;")},
				"0010_later.sql": {Data: []byte("SELECT 10;")},
			},
			versions: []string{"0001", "0002", "0010"},
		},
		{
			name:     "empty directory",
			files:    fstest.MapFS{},
			versions: []string{},
		},
		{
			name:    "bad name",
			files:   fstest.MapFS{"init.sql": {Data: []byte("SELECT 1;")}},
			wantErr: db.ErrBadMigrationName,
		},
		{
			name:    "non-sql file",
			files:   fstest.MapFS{"0001_init.txt": {Data: []byte("x")}},
			wantErr: db.ErrBadMigrationName,
		},
		{
			name: "duplicate version",
			files: fstest.MapFS{
				"0001_a.sql": {Data: []byte("SELECT 1;")},
				"0001_b.sql": {Data: []byte("SELECT 1;")},
			},
			wantErr: db.ErrDuplicateVersion,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := db.LoadMigrations(tt.files)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			versions := make([]string, 0, len(got))
			for _, m := range got {
				versions = append(versions, m.Version)
			}
			if len(versions) != len(tt.versions) {
				t.Fatalf("versions = %v, want %v", versions, tt.versions)
			}
			for i := range versions {
				if versions[i] != tt.versions[i] {
					t.Fatalf("versions = %v, want %v", versions, tt.versions)
				}
			}
		})
	}
}

func TestEmbeddedMigrationsLoad(t *testing.T) {
	got, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no embedded migrations")
	}
}

// TestMigrateAgainstPostgres runs only when OHARA_TEST_DATABASE_URL points at
// a disposable database (CI provides one).
func TestMigrateAgainstPostgres(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()

	first := []db.Migration{
		{Version: "0001", Name: "0001_probe.sql", SQL: "CREATE TABLE migrate_probe (id int); INSERT INTO migrate_probe VALUES (1);"},
	}
	applied, err := db.Migrate(ctx, pool, first)
	if err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if len(applied) != 1 {
		t.Fatalf("first run applied %v, want [0001]", applied)
	}

	again, err := db.Migrate(ctx, pool, first)
	if err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("second run applied %v, want none", again)
	}

	broken := append(slices.Clone(first), db.Migration{Version: "0002", Name: "0002_broken.sql", SQL: "INSERT INTO migrate_probe VALUES (2); SELECT nope;"})
	if _, err := db.Migrate(ctx, pool, broken); err == nil {
		t.Fatal("broken migration succeeded")
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM migrate_probe`).Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Fatalf("rows = %d, want 1: failed migration must roll back", count)
	}

	embedded, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	if _, err := pool.Exec(ctx, `DROP TABLE schema_migrations, migrate_probe`); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if _, err := db.Migrate(ctx, pool, embedded); err != nil {
		t.Fatalf("embedded migrate: %v", err)
	}
}

// TestRemoveCeneoMigration stores a Ceneo listing with a deal alert beside
// an OLX one, and movie lookups cached with and without a Ceneo product
// name beside a picked candidate, before 0012, then applies it: the Ceneo
// listing, its alert and the lookup built on Ceneo are gone, the rest stay.
func TestRemoveCeneoMigration(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	all, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(all), func(m db.Migration) bool { return m.Version >= "0012" })
	if len(before) == len(all) {
		t.Fatal("no migration 0012")
	}
	if _, err := db.Migrate(ctx, pool, before); err != nil {
		t.Fatalf("migrate to 0011: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents)
		VALUES ('ceneo', '165912980', 3370), ('olx', '900', 2500)`); err != nil {
		t.Fatalf("observations: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO deal_alert (observation_id) SELECT id FROM price_observation`); err != nil {
		t.Fatalf("alerts: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO lookup_cache (provider, barcode, response) VALUES
		('movie', '5903570157561', '{"products":[{"source":"ceneo","name":"Incepcja [Blu-Ray]"}],"searches":[]}'),
		('movie', '5906190326195', '{"products":[{"source":"allegro","name":"Kursk [Blu-ray]"}],"searches":[]}'),
		('selection', '5903570157561', '{"provider":"selection"}')`); err != nil {
		t.Fatalf("lookup cache: %v", err)
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
	if !slices.Equal(sources, []string{"olx"}) || !slices.Equal(alerts, []string{"olx"}) {
		t.Fatalf("observations %v, alerts %v; want only the OLX listing and its alert", sources, alerts)
	}
	var cached []string
	if err := pool.QueryRow(ctx, `SELECT array_agg(provider || '/' || barcode ORDER BY provider, barcode)
		FROM lookup_cache`).Scan(&cached); err != nil {
		t.Fatalf("lookup cache: %v", err)
	}
	if !slices.Equal(cached, []string{"movie/5906190326195", "selection/5903570157561"}) {
		t.Fatalf("lookup cache %v; want the Allegro lookup and the picked candidate", cached)
	}
}

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
