package seed

import (
	"bytes"
	"context"
	"errors"
	"image"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

var tables = []string{
	"title", "edition", "copy", "price_observation", "deal_alert",
	"valuation_history", "scan_queue", "deal_run", "deal_keyword", "lookup_cache",
}

func versions(t *testing.T) []string {
	t.Helper()
	migrations, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	out := make([]string, len(migrations))
	for i, m := range migrations {
		out[i] = m.Version
	}
	return out
}

// waitForLock polls until a lock of locktype is waited for, failing when
// Run finishes first or nothing waits within 10 s.
func waitForLock(t *testing.T, pool *pgxpool.Pool, locktype string, done <-chan error) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		err := pool.QueryRow(context.Background(), `SELECT count(*) FROM pg_locks
			WHERE NOT granted AND locktype = $1`, locktype).Scan(&waiting)
		if err != nil {
			t.Fatalf("pg_locks: %v", err)
		}
		if waiting > 0 {
			return
		}
		select {
		case err := <-done:
			t.Fatalf("Run finished with %v before the writer committed", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("Run never waited for a %s lock", locktype)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func rowCounts(t *testing.T, pool *pgxpool.Pool) map[string]int {
	t.Helper()
	counts := map[string]int{}
	for _, table := range tables {
		var n int
		if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = n
	}
	return counts
}

func scalar(t *testing.T, pool *pgxpool.Pool, sql string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), sql).Scan(&n); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return n
}

func TestRunFillsEmptyDatabase(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()

	got, err := Run(ctx, pool, versions(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	rows := rowCounts(t, pool)
	want := Counts{
		Titles: rows["title"], Editions: rows["edition"], Copies: rows["copy"],
		Observations: rows["price_observation"], Alerts: rows["deal_alert"],
		HistoryDays: rows["valuation_history"], ScanQueue: rows["scan_queue"],
	}
	if got != want {
		t.Errorf("Run = %+v, tables hold %+v", got, want)
	}

	for _, kind := range []string{"movie", "book", "music"} {
		n := scalar(t, pool, `SELECT count(*) FROM copy c JOIN edition e ON e.id = c.edition_id
			JOIN title t ON t.id = e.title_id
			WHERE c.status = 'owned' AND c.price_cents > 0 AND t.kind = '`+kind+`'`)
		if n == 0 {
			t.Errorf("no priced owned copy of kind %s", kind)
		}
	}
	if n := rows["valuation_history"]; n < 30 {
		t.Errorf("valuation_history holds %d days, want at least 30", n)
	}
	if n := rows["scan_queue"]; n < 3 {
		t.Errorf("scan_queue holds %d entries, want at least 3", n)
	}
	if rows["deal_run"] != 0 {
		t.Errorf("deal_run holds %d rows, want none: a running hunter would not show it", rows["deal_run"])
	}

	editionBarcodes, err := pool.Query(ctx, `SELECT barcode FROM edition UNION ALL SELECT barcode FROM scan_queue`)
	if err != nil {
		t.Fatalf("barcodes: %v", err)
	}
	var codes []string
	for editionBarcodes.Next() {
		var code string
		if err := editionBarcodes.Scan(&code); err != nil {
			t.Fatalf("scan barcode: %v", err)
		}
		codes = append(codes, code)
	}
	if err := editionBarcodes.Err(); err != nil {
		t.Fatalf("barcodes: %v", err)
	}
	for _, code := range codes {
		if norm, err := barcode.Normalize(code); err != nil || norm != code {
			t.Errorf("barcode %s normalizes to %q, %v", code, norm, err)
		}
	}

	var photo []byte
	err = pool.QueryRow(ctx, `SELECT cover_photo FROM edition WHERE cover_photo IS NOT NULL AND edited_at IS NOT NULL`).
		Scan(&photo)
	if err != nil {
		t.Fatalf("cover photo: %v", err)
	}
	if _, format, err := image.Decode(bytes.NewReader(photo)); err != nil || format != "png" {
		t.Errorf("cover photo decodes as %q, %v", format, err)
	}

	dealStore := deals.NewStore(pool)
	under, err := dealStore.Recent(ctx, deals.RecentFilter{Limit: 100, UnderLimit: true})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	if len(under) == 0 {
		t.Error("deals list no listing under a wish's limit")
	}
	matched, err := dealStore.Recent(ctx, deals.RecentFilter{Limit: 100, Matched: true})
	if err != nil {
		t.Fatalf("Recent: %v", err)
	}
	sources := map[string]bool{}
	for _, o := range matched {
		sources[o.Source] = true
	}
	if len(sources) < 2 {
		t.Errorf("wished listings come from %v, want more than one source", sources)
	}
	if len(matched) <= len(under) {
		t.Errorf("all %d wished listings are under the limit, want some above", len(matched))
	}
	pending, err := dealStore.PendingAlerts(ctx)
	if err != nil {
		t.Fatalf("PendingAlerts: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("%d alerts would be sent to Telegram", len(pending))
	}

	valueStore := valuation.NewStore(pool)
	_, sum, err := valueStore.Valued(ctx, 90*24*time.Hour)
	if err != nil {
		t.Fatalf("Valued: %v", err)
	}
	if sum.Mid.Cents == 0 || sum.Floor.Cents == 0 {
		t.Errorf("collection value = %+v, want a floor and a middle", sum)
	}
	history, err := valueStore.History(ctx, 90)
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(history) < 30 {
		t.Errorf("History holds %d days, want at least 30", len(history))
	}
}

func TestRunRefusesFilledDatabase(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	if _, err := Run(ctx, pool, versions(t)); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before := rowCounts(t, pool)

	if _, err := Run(ctx, pool, versions(t)); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("second Run = %v, want ErrNotEmpty", err)
	}
	after := rowCounts(t, pool)
	for _, table := range tables {
		if before[table] != after[table] {
			t.Errorf("%s: %d rows before the second run, %d after", table, before[table], after[table])
		}
	}
}

func TestRunRefusesExistingData(t *testing.T) {
	for _, tc := range []struct {
		name, insert string
	}{
		{"title", `INSERT INTO title (kind, name) VALUES ('book', 'Katedra')`},
		{"price observation", `INSERT INTO price_observation (source, price_cents) VALUES ('olx', 1000)`},
		{"scan queue", `INSERT INTO scan_queue (barcode, reason) VALUES ('9788308073209', 'not_found')`},
		{"valuation history", `INSERT INTO valuation_history (day, copies, spent_cents, floor_cents, floor_copies,
			mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence)
			VALUES (current_date, 0, 0, 0, 0, 0, 0, 0, 0, 0)`},
		{"deal run", `INSERT INTO deal_run (started_at) VALUES (now())`},
		{"added deal keyword", `INSERT INTO deal_keyword (kind, phrase) VALUES ('reject', 'zestaw')`},
		{"deleted default deal keyword", `DELETE FROM deal_keyword WHERE kind = 'reject' AND phrase = 'plakat'`},
		{"lookup selection", `INSERT INTO lookup_cache (provider, barcode, response)
			VALUES ('selection', '9788308073209', '{}')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			ctx := context.Background()
			if _, err := pool.Exec(ctx, tc.insert); err != nil {
				t.Fatalf("insert: %v", err)
			}
			before := rowCounts(t, pool)

			if _, err := Run(ctx, pool, versions(t)); !errors.Is(err, ErrNotEmpty) {
				t.Fatalf("Run = %v, want ErrNotEmpty", err)
			}
			after := rowCounts(t, pool)
			for _, table := range tables {
				if before[table] != after[table] {
					t.Errorf("%s: %d rows before, %d after", table, before[table], after[table])
				}
			}
		})
	}
}

// TestRunIgnoresCachesAndLogins seeds a database that holds a cached
// provider answer, an Allegro login and the default deal keywords: none of
// them is the user's data, so the seed fills it and leaves them in place.
func TestRunIgnoresCachesAndLogins(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO lookup_cache (provider, barcode, response)
		VALUES ('openlibrary', '9788308073209', '[]')`)
	if err != nil {
		t.Fatalf("insert cache: %v", err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO oauth_token (provider, access_token, refresh_token, expires_at)
		VALUES ('allegro', 'access', 'refresh', now() + interval '1 hour')`)
	if err != nil {
		t.Fatalf("insert token: %v", err)
	}

	if _, err := Run(ctx, pool, versions(t)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := scalar(t, pool, `SELECT count(*) FROM title`); n != len(demo) {
		t.Errorf("titles = %d, want %d", n, len(demo))
	}
	if n := scalar(t, pool, `SELECT count(*) FROM lookup_cache`); n != 1 {
		t.Errorf("lookup_cache rows = %d, want 1", n)
	}
	if n := scalar(t, pool, `SELECT count(*) FROM oauth_token`); n != 1 {
		t.Errorf("oauth_token rows = %d, want 1", n)
	}
}

// TestRunWaitsForGuardedWrites checks that seed waits for in-flight writes
// to commit before deciding whether the database is empty.
func TestRunWaitsForGuardedWrites(t *testing.T) {
	for _, tc := range []struct {
		name, insert string
	}{
		{"deal keyword", `INSERT INTO deal_keyword (kind, phrase) VALUES ('reject', 'zestaw')`},
		{"lookup selection", `INSERT INTO lookup_cache (provider, barcode, response)
			VALUES ('selection', '9788308073209', '{}')`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			ctx := t.Context()
			writer, err := pool.Begin(ctx)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = writer.Rollback(context.WithoutCancel(ctx)) }()
			if _, err := writer.Exec(ctx, tc.insert); err != nil {
				t.Fatalf("insert: %v", err)
			}

			migrations := versions(t)
			done := make(chan error, 1)
			go func() {
				_, err := Run(ctx, pool, migrations)
				done <- err
			}()
			waitForLock(t, pool, "relation", done)
			if err := writer.Commit(ctx); err != nil {
				t.Fatalf("commit: %v", err)
			}
			if err := <-done; !errors.Is(err, ErrNotEmpty) {
				t.Fatalf("Run = %v, want ErrNotEmpty", err)
			}
			if n := scalar(t, pool, `SELECT count(*) FROM title`); n != 0 {
				t.Errorf("titles = %d, want 0", n)
			}
		})
	}
}

// TestRunWaitsForScanQueueWriter queues a scan in a transaction that is
// still open when the seed starts. The seed must wait for it, see the
// committed entry and refuse, instead of seeding past it.
func TestRunWaitsForScanQueueWriter(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err := writer.Exec(ctx, `INSERT INTO scan_queue (barcode, reason) VALUES ('5903567700015', 'not_found')`); err != nil {
		t.Fatalf("queue: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, pool, versions(t))
		done <- err
	}()
	waitForLock(t, pool, "relation", done)
	if err := writer.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := <-done; !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Run = %v, want ErrNotEmpty", err)
	}
	if n := scalar(t, pool, `SELECT count(*) FROM scan_queue WHERE barcode = '5903567700015'`); n != 1 {
		t.Errorf("queued scan rows = %d, want 1", n)
	}
	if n := scalar(t, pool, `SELECT count(*) FROM title`); n != 0 {
		t.Errorf("titles = %d, want 0", n)
	}
}

// TestRunWaitsForScanOfDemoBarcode queues a demo barcode the way the scan
// queue does: its barcode lock first, then the insert. The seed takes the
// barcode locks before its table locks, so it waits for the scan instead
// of deadlocking with it, then sees the entry and refuses.
func TestRunWaitsForScanOfDemoBarcode(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	writer, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = writer.Rollback(ctx) }()
	if _, err := writer.Exec(ctx, `SELECT scan_queue_lock('5903567700015')`); err != nil {
		t.Fatalf("barcode lock: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := Run(ctx, pool, versions(t))
		done <- err
	}()
	waitForLock(t, pool, "advisory", done)
	if _, err := writer.Exec(ctx, `INSERT INTO scan_queue (barcode, reason) VALUES ('5903567700015', 'not_found')`); err != nil {
		t.Fatalf("queue: %v", err)
	}
	if err := writer.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	if err := <-done; !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("Run = %v, want ErrNotEmpty", err)
	}
	if n := scalar(t, pool, `SELECT count(*) FROM scan_queue WHERE barcode = '5903567700015'`); n != 1 {
		t.Errorf("queued scan rows = %d, want 1", n)
	}
}

// TestRunRefusesUnmigratedDatabase seeds a database without migrations and
// one an older release left at 0012 with a Vinted listing, which 0013
// deletes. The seed never migrates, so both are refused unchanged.
func TestRunRefusesUnmigratedDatabase(t *testing.T) {
	ctx := context.Background()
	t.Run("fresh", func(t *testing.T) {
		pool := dbtest.Pool(t)
		if _, err := Run(ctx, pool, versions(t)); !errors.Is(err, ErrNotMigrated) {
			t.Fatalf("Run = %v, want ErrNotMigrated", err)
		}
		if n := scalar(t, pool, `SELECT count(*) FROM pg_tables WHERE schemaname = current_schema()`); n != 0 {
			t.Errorf("tables = %d, want 0", n)
		}
	})
	t.Run("older release", func(t *testing.T) {
		pool := dbtest.Pool(t)
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
		if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents)
			VALUES ('vinted', '1', 1000)`); err != nil {
			t.Fatalf("listing: %v", err)
		}

		if _, err := Run(ctx, pool, versions(t)); !errors.Is(err, ErrNotMigrated) {
			t.Fatalf("Run = %v, want ErrNotMigrated", err)
		}
		if n := scalar(t, pool, `SELECT count(*) FROM price_observation WHERE source = 'vinted'`); n != 1 {
			t.Errorf("Vinted listings = %d, want 1", n)
		}
		if n := scalar(t, pool, `SELECT count(*) FROM schema_migrations`); n != len(older) {
			t.Errorf("applied migrations = %d, want %d", n, len(older))
		}
		if n := scalar(t, pool, `SELECT count(*) FROM title`); n != 0 {
			t.Errorf("titles = %d, want 0", n)
		}
	})
}
