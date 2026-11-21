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
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

var tables = []string{
	"title", "edition", "copy", "price_observation", "deal_alert",
	"valuation_history", "scan_queue", "deal_run",
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

	got, err := Run(ctx, pool)
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
	if rows["deal_run"] != 1 {
		t.Errorf("deal_run holds %d rows, want 1", rows["deal_run"])
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
	if _, ok, err := dealStore.LastRun(ctx); err != nil || !ok {
		t.Errorf("LastRun = %v, %v", ok, err)
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
	if _, err := Run(ctx, pool); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before := rowCounts(t, pool)

	if _, err := Run(ctx, pool); !errors.Is(err, ErrNotEmpty) {
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
		{"scan queue", `INSERT INTO scan_queue (barcode, reason) VALUES ('9788308073209', 'not_found')`},
		{"valuation history", `INSERT INTO valuation_history (day, copies, spent_cents, floor_cents, floor_copies,
			mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence)
			VALUES (current_date, 0, 0, 0, 0, 0, 0, 0, 0, 0)`},
		{"deal run", `INSERT INTO deal_run (started_at) VALUES (now())`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			ctx := context.Background()
			if _, err := pool.Exec(ctx, tc.insert); err != nil {
				t.Fatalf("insert: %v", err)
			}
			before := rowCounts(t, pool)

			if _, err := Run(ctx, pool); !errors.Is(err, ErrNotEmpty) {
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
