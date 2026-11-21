package db_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
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

// TestUnknownMigrations runs a migration and its warning on a database
// migrated by a newer or the same build: versions the binary has no file for are
// logged once and counted, known pending ones are still applied.
func TestUnknownMigrations(t *testing.T) {
	probe := func(versions ...string) []db.Migration {
		out := make([]db.Migration, 0, len(versions))
		for _, v := range versions {
			out = append(out, db.Migration{Version: v, Name: v + "_probe.sql", SQL: "SELECT 1;"})
		}
		return out
	}
	tests := []struct {
		name        string
		before      []string
		binary      []string
		wantApplied []string
		wantUnknown []string
	}{
		{name: "fresh database", binary: []string{"0001", "0002"}, wantApplied: []string{"0001", "0002"}},
		{name: "up to date", before: []string{"0001", "0002"}, binary: []string{"0001", "0002"}},
		{name: "one unknown", before: []string{"0001", "0002"}, binary: []string{"0001"}, wantUnknown: []string{"0002"}},
		{name: "two unknown", before: []string{"0001", "0002", "0003"}, binary: []string{"0001"}, wantUnknown: []string{"0002", "0003"}},
		{
			name:   "known pending beside unknown",
			before: []string{"0001", "0003"}, binary: []string{"0001", "0002"},
			wantApplied: []string{"0002"}, wantUnknown: []string{"0003"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := dbtest.Pool(t)
			ctx := context.Background()
			if len(tt.before) > 0 {
				if _, err := db.Migrate(ctx, pool, probe(tt.before...)); err != nil {
					t.Fatalf("newer build: %v", err)
				}
			}

			binary := probe(tt.binary...)
			res, err := db.ApplyMigrations(ctx, pool, binary)
			if err != nil {
				t.Fatalf("migrate: %v", err)
			}
			if !slices.Equal(res.Applied, tt.wantApplied) {
				t.Fatalf("applied %v, want %v", res.Applied, tt.wantApplied)
			}
			if !slices.Equal(res.Unknown, tt.wantUnknown) {
				t.Fatalf("unknown %v, want %v", res.Unknown, tt.wantUnknown)
			}
			var logs bytes.Buffer
			db.WarnUnknown(slog.New(slog.NewTextHandler(&logs, nil)), binary, res.Unknown)

			lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
			if len(tt.wantUnknown) == 0 {
				if logs.Len() != 0 {
					t.Fatalf("logged %q, want nothing", logs.String())
				}
			} else {
				want := fmt.Sprintf("versions=%s binary_newest=%s", strings.Join(tt.wantUnknown, ","), tt.binary[len(tt.binary)-1])
				if len(lines) != 1 || !strings.Contains(lines[0], "level=ERROR") || !strings.Contains(lines[0], want) {
					t.Fatalf("logged %q, want one ERROR line with %q", logs.String(), want)
				}
			}

			srv := httptest.NewServer(metrics.Handler())
			defer srv.Close()
			gauge := fmt.Sprintf("# HELP ohara_db_unknown_migrations Migrations applied to the database that this binary has no file for, counted at startup; above 0 after a rollback to an older release.\n"+
				"# TYPE ohara_db_unknown_migrations gauge\nohara_db_unknown_migrations %d\n", len(tt.wantUnknown))
			if err := testutil.ScrapeAndCompare(srv.URL, strings.NewReader(gauge), "ohara_db_unknown_migrations"); err != nil {
				t.Fatal(err)
			}
		})
	}
}
