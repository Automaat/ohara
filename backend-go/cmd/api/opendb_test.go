package main

import (
	"bytes"
	"context"
	"log/slog"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// TestOpenDBWarnsOnNewerSchema starts on a database a newer build migrated,
// one version past the embedded migrations: openDB still opens it, logs
// one error naming that version and exports it in
// ohara_db_unknown_migrations. A second start on the up-to-date database
// it left behind logs nothing and sets the gauge back to 0.
func TestOpenDBWarnsOnNewerSchema(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	embedded, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	newer := append(slices.Clone(embedded), db.Migration{Version: "9999", Name: "9999_newer.sql", SQL: "SELECT 1;"})
	if _, err := db.Migrate(ctx, pool, newer); err != nil {
		t.Fatalf("newer build: %v", err)
	}

	dsn, err := url.Parse(os.Getenv("OHARA_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatalf("OHARA_TEST_DATABASE_URL must be a URL: %v", err)
	}
	q := dsn.Query()
	q.Set("search_path", pool.Config().ConnConfig.RuntimeParams["search_path"])
	dsn.RawQuery = q.Encode()

	var logs bytes.Buffer
	started, err := openDB(ctx, dsn.String(), slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("openDB on a newer schema: %v", err)
	}
	started.Close()
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	want := "versions=9999 binary_newest=" + embedded[len(embedded)-1].Version
	if len(lines) != 1 || !strings.Contains(lines[0], "level=ERROR") || !strings.Contains(lines[0], want) {
		t.Fatalf("logged %q, want one ERROR line with %q", logs.String(), want)
	}
	if v, ok := sample(scrape(t), "ohara_db_unknown_migrations"); !ok || v != 1 {
		t.Fatalf("ohara_db_unknown_migrations = %v (exported %v), want 1", v, ok)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM schema_migrations WHERE version = '9999'`); err != nil {
		t.Fatalf("drop newer version: %v", err)
	}
	logs.Reset()
	again, err := openDB(ctx, dsn.String(), slog.New(slog.NewTextHandler(&logs, nil)))
	if err != nil {
		t.Fatalf("openDB on an up-to-date schema: %v", err)
	}
	again.Close()
	if logs.Len() != 0 {
		t.Fatalf("up-to-date start logged %q, want nothing", logs.String())
	}
	if v, ok := sample(scrape(t), "ohara_db_unknown_migrations"); !ok || v != 0 {
		t.Fatalf("ohara_db_unknown_migrations = %v (exported %v), want 0", v, ok)
	}
}
