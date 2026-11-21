package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/seed"
)

// TestSeedDBRefusesOlderSchemaUnchanged seeds a database an older release
// migrated to 0012 that holds a title and a Vinted listing, which 0013
// deletes. The seed must refuse before migrating, leaving the listing and
// the schema version as they were.
func TestSeedDBRefusesOlderSchemaUnchanged(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	embedded, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	var older []db.Migration
	for _, m := range embedded {
		if m.Version <= "0012" {
			older = append(older, m)
		}
	}
	if _, err := db.Migrate(ctx, pool, older); err != nil {
		t.Fatalf("older release: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO title (kind, name) VALUES ('book', 'Katedra')`); err != nil {
		t.Fatalf("title: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents)
		VALUES ('vinted', '1', 1000)`); err != nil {
		t.Fatalf("listing: %v", err)
	}

	dsn := os.Getenv("OHARA_TEST_DATABASE_URL")
	searchPath := strings.ReplaceAll(pool.Config().ConnConfig.RuntimeParams["search_path"], " ", "")
	t.Setenv("PGOPTIONS", "-c search_path="+searchPath)
	if _, err := seedDB(ctx, dsn, slog.New(slog.DiscardHandler)); !errors.Is(err, seed.ErrNotEmpty) {
		t.Fatalf("seedDB = %v, want ErrNotEmpty", err)
	}

	var listings, versions int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM price_observation WHERE source = 'vinted'`).Scan(&listings); err != nil {
		t.Fatalf("listings: %v", err)
	}
	if listings != 1 {
		t.Errorf("Vinted listings = %d, want 1", listings)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM schema_migrations`).Scan(&versions); err != nil {
		t.Fatalf("versions: %v", err)
	}
	if versions != len(older) {
		t.Errorf("applied migrations = %d, want %d", versions, len(older))
	}
}
