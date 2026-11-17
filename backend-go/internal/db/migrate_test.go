package db_test

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"testing/fstest"

	"github.com/Automaat/ohara/backend-go/internal/db"
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
	dsn := os.Getenv("OHARA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OHARA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := db.New(ctx, dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, `DROP TABLE IF EXISTS schema_migrations, migrate_probe`); err != nil {
		t.Fatalf("reset: %v", err)
	}

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
