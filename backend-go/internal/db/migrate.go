package db

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationLockID is the advisory lock key that serializes concurrent
// migrators, such as two containers starting at once. Its bytes spell "ohara".
const migrationLockID int64 = 0x6f68617261

var migrationName = regexp.MustCompile(`^(\d{4})_[a-z0-9_]+\.sql$`)

// Migration is one forward-only schema change, applied once in version order.
// SQL may hold many statements: it runs over the simple protocol.
type Migration struct {
	Version string
	Name    string
	SQL     string
}

// ErrBadMigrationName reports a file in migrations/ that does not follow the
// NNNN_snake_case.sql convention.
var ErrBadMigrationName = errors.New("migration file name must match NNNN_name.sql")

// ErrDuplicateVersion reports two migration files sharing one version prefix.
var ErrDuplicateVersion = errors.New("duplicate migration version")

// LoadMigrations reads every migration from fsys (rooted at the directory that
// holds the .sql files) sorted by version.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	migrations := make([]Migration, 0, len(entries))
	seen := make(map[string]string, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("%w: %s", ErrBadMigrationName, e.Name())
		}
		if prev, ok := seen[m[1]]; ok {
			return nil, fmt.Errorf("%w %s: %s and %s", ErrDuplicateVersion, m[1], prev, e.Name())
		}
		seen[m[1]] = e.Name()
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		migrations = append(migrations, Migration{Version: m[1], Name: e.Name(), SQL: string(body)})
	}
	slices.SortFunc(migrations, func(a, b Migration) int { return strings.Compare(a.Version, b.Version) })
	return migrations, nil
}

// EmbeddedMigrations returns the migrations compiled into the binary.
func EmbeddedMigrations() ([]Migration, error) {
	sub, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	return LoadMigrations(sub)
}

// Migrate applies every migration not yet recorded in schema_migrations, each
// in its own transaction, and returns the versions it applied.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations []Migration) ([]string, error) {
	res, err := ApplyMigrations(ctx, pool, migrations)
	return res.Applied, err
}

// MigrateResult is what one migration run found and did.
type MigrateResult struct {
	// Applied holds the versions this run applied, in order.
	Applied []string
	// Unknown holds the versions in schema_migrations that the run's
	// migrations have no file for, sorted: the schema is newer than this
	// binary, as after a rollback to an older release.
	Unknown []string
}

// ApplyMigrations is Migrate that also reports the unknown versions, read
// from the same query that decides what to apply. The advisory lock is
// released on a context detached from ctx, so cancellation never leaves it
// held on a pooled connection.
func ApplyMigrations(ctx context.Context, pool *pgxpool.Pool, migrations []Migration) (MigrateResult, error) {
	var res MigrateResult
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return res, fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return res, fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return res, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return res, fmt.Errorf("list applied migrations: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return res, fmt.Errorf("scan applied migrations: %w", err)
	}

	known := make(map[string]bool, len(migrations))
	for _, m := range migrations {
		known[m.Version] = true
	}
	for _, v := range applied {
		if !known[v] {
			res.Unknown = append(res.Unknown, v)
		}
	}
	slices.Sort(res.Unknown)

	for _, m := range migrations {
		if slices.Contains(applied, m.Version) {
			continue
		}
		if err := applyOne(ctx, conn.Conn(), m); err != nil {
			return res, err
		}
		res.Applied = append(res.Applied, m.Version)
	}
	return res, nil
}

// WarnUnknown exports len(unknown) as ohara_db_unknown_migrations and, when
// there are any, logs one error naming them and the newest version among
// migrations. It reads nothing, so it adds no way for startup to fail: a
// rollback by image tag must still start.
func WarnUnknown(logger *slog.Logger, migrations []Migration, unknown []string) {
	metrics.SetUnknownMigrations(len(unknown))
	if len(unknown) == 0 {
		return
	}
	newest := ""
	for _, m := range migrations {
		newest = max(newest, m.Version)
	}
	logger.Error("database schema is newer than this binary: applied migrations it does not know",
		"versions", strings.Join(unknown, ","), "binary_newest", newest)
}

func applyOne(ctx context.Context, conn *pgx.Conn, m Migration) error {
	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", m.Name, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, m.SQL, pgx.QueryExecModeSimpleProtocol); err != nil {
		return fmt.Errorf("apply migration %s: %w", m.Name, err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`, m.Version, m.Name,
	); err != nil {
		return fmt.Errorf("record migration %s: %w", m.Name, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit migration %s: %w", m.Name, err)
	}
	return nil
}
