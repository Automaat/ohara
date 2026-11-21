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
// in its own transaction, and returns the versions it applied. The advisory
// lock is released on a context detached from ctx, so cancellation never
// leaves it held on a pooled connection.
func Migrate(ctx context.Context, pool *pgxpool.Pool, migrations []Migration) ([]string, error) {
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire migration conn: %w", err)
	}
	defer conn.Release()

	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, migrationLockID); err != nil {
		return nil, fmt.Errorf("take migration lock: %w", err)
	}
	defer func() {
		_, _ = conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock($1)`, migrationLockID)
	}()

	if _, err := conn.Exec(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version text PRIMARY KEY,
		name text NOT NULL,
		applied_at timestamptz NOT NULL DEFAULT now()
	)`); err != nil {
		return nil, fmt.Errorf("create schema_migrations: %w", err)
	}

	rows, err := conn.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan applied migrations: %w", err)
	}

	var done []string
	for _, m := range migrations {
		if slices.Contains(applied, m.Version) {
			continue
		}
		if err := applyOne(ctx, conn.Conn(), m); err != nil {
			return done, err
		}
		done = append(done, m.Version)
	}
	return done, nil
}

// CheckUnknown returns the versions in schema_migrations that migrations has
// no file for, sorted: the schema is newer than this binary, as after a
// rollback to an older release. It exports their count as
// ohara_db_unknown_migrations and, when there are any, logs one error naming
// them and the binary's newest version. The app keeps running: a rollback by
// image tag must still start.
func CheckUnknown(ctx context.Context, pool *pgxpool.Pool, migrations []Migration, logger *slog.Logger) ([]string, error) {
	rows, err := pool.Query(ctx, `SELECT version FROM schema_migrations`)
	if err != nil {
		return nil, fmt.Errorf("list applied migrations: %w", err)
	}
	applied, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return nil, fmt.Errorf("scan applied migrations: %w", err)
	}
	known := make(map[string]bool, len(migrations))
	newest := ""
	for _, m := range migrations {
		known[m.Version] = true
		newest = max(newest, m.Version)
	}
	unknown := slices.DeleteFunc(applied, func(v string) bool { return known[v] })
	slices.Sort(unknown)
	metrics.SetUnknownMigrations(len(unknown))
	if len(unknown) > 0 {
		logger.Error("database schema is newer than this binary: applied migrations it does not know",
			"versions", strings.Join(unknown, ","), "binary_newest", newest)
	}
	return unknown, nil
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
