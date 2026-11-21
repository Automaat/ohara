// Package dbtest gives each test its own Postgres schema, so test packages can
// run in parallel against the one database CI provides.
package dbtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/db"
)

// extensionLockID serializes CREATE EXTENSION across test packages, which
// would otherwise race on the catalog.
const extensionLockID int64 = 0x6f68617274

// Pool returns a pool whose search_path is a fresh schema dropped at cleanup.
// It skips the test unless OHARA_TEST_DATABASE_URL points at a disposable
// database. pg_trgm is created in public first: extensions outlive schemas,
// and one created inside a test schema would vanish with it.
func Pool(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	dsn := os.Getenv("OHARA_TEST_DATABASE_URL")
	if dsn == "" {
		tb.Skip("OHARA_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		tb.Fatalf("connect: %v", err)
	}
	tb.Cleanup(func() { _ = admin.Close(context.Background()) })

	if _, err := admin.Exec(ctx, `SELECT pg_advisory_lock($1)`, extensionLockID); err != nil {
		tb.Fatalf("lock: %v", err)
	}
	_, err = admin.Exec(ctx, `CREATE EXTENSION IF NOT EXISTS pg_trgm SCHEMA public`)
	_, _ = admin.Exec(ctx, `SELECT pg_advisory_unlock($1)`, extensionLockID)
	if err != nil {
		tb.Fatalf("create extension: %v", err)
	}

	buf := make([]byte, 6)
	_, _ = rand.Read(buf)
	schema := "test_" + hex.EncodeToString(buf)
	if _, err := admin.Exec(ctx, `CREATE SCHEMA `+schema); err != nil {
		tb.Fatalf("create schema: %v", err)
	}
	tb.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), `DROP SCHEMA `+schema+` CASCADE`)
	})

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		tb.Fatalf("parse dsn: %v", err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema + ", public"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		tb.Fatalf("open pool: %v", err)
	}
	tb.Cleanup(pool.Close)
	return pool
}

// Migrated is Pool with the embedded migrations applied.
func Migrated(tb testing.TB) *pgxpool.Pool {
	tb.Helper()
	pool := Pool(tb)
	migrations, err := db.EmbeddedMigrations()
	if err != nil {
		tb.Fatalf("embedded migrations: %v", err)
	}
	if _, err := db.Migrate(context.Background(), pool, migrations); err != nil {
		tb.Fatalf("migrate: %v", err)
	}
	return pool
}
