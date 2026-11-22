package migrations_test

import (
	"context"
	"slices"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/db"
	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// TestDealAlertPriceMigration stores a sent and an unsent alert before
// 0016, then applies it: neither gets a price, since the one the sent alert
// named was overwritten since, and neither changes otherwise.
func TestDealAlertPriceMigration(t *testing.T) {
	pool := dbtest.Pool(t)
	ctx := context.Background()
	all, err := db.EmbeddedMigrations()
	if err != nil {
		t.Fatalf("embedded: %v", err)
	}
	before := slices.DeleteFunc(slices.Clone(all), func(m db.Migration) bool { return m.Version >= "0016" })
	if len(before) == len(all) {
		t.Fatal("no migration 0016")
	}
	if _, err := db.Migrate(ctx, pool, before); err != nil {
		t.Fatalf("migrate before 0016: %v", err)
	}
	if _, err := pool.Exec(ctx, `WITH o AS (
			INSERT INTO price_observation (source, external_id, price_cents)
			VALUES ('olx', 'sent', 4000), ('olx', 'pending', 3000) RETURNING id, external_id
		)
		INSERT INTO deal_alert (observation_id, sent_at, attempts, last_error)
		SELECT id, CASE WHEN external_id = 'sent' THEN now() END,
			CASE WHEN external_id = 'sent' THEN 1 ELSE 2 END,
			CASE WHEN external_id = 'sent' THEN '' ELSE 'telegram: status 502' END
		FROM o`); err != nil {
		t.Fatalf("alerts: %v", err)
	}
	if _, err := db.Migrate(ctx, pool, all); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT o.external_id, coalesce(a.alerted_price_cents, a.queued_price_cents), a.sent_at IS NOT NULL, a.attempts, a.last_error
		FROM deal_alert a JOIN price_observation o ON o.id = a.observation_id ORDER BY o.external_id`)
	if err != nil {
		t.Fatalf("alerts: %v", err)
	}
	defer rows.Close()
	type row struct {
		id       string
		price    *int64
		sent     bool
		attempts int
		lastErr  string
	}
	var got []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.price, &r.sent, &r.attempts, &r.lastErr); err != nil {
			t.Fatalf("scan: %v", err)
		}
		got = append(got, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if len(got) != 2 ||
		got[0].id != "pending" || got[0].price != nil || got[0].sent || got[0].attempts != 2 || got[0].lastErr != "telegram: status 502" ||
		got[1].id != "sent" || got[1].price != nil || !got[1].sent || got[1].attempts != 1 || got[1].lastErr != "" {
		t.Fatalf("alerts after 0016 = %+v; want both without a price and otherwise kept", got)
	}
}
