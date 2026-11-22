package valuation

import (
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
)

func TestLegacySelfTitledListingsRequireAlbumEvidence(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := t.Context()
	exec(t, pool, `INSERT INTO title (id, kind, name, authors) OVERRIDING SYSTEM VALUE
		VALUES (1, 'music', 'David Bowie aka Space Oddity', ARRAY['David Bowie']);
		INSERT INTO edition (id, title_id, format) OVERRIDING SYSTEM VALUE VALUES (1, 1, 'vinyl');
		INSERT INTO copy (id, edition_id, status, max_price_cents) OVERRIDING SYSTEM VALUE
		VALUES (1, 1, 'wishlist', 20000), (2, 1, 'owned', NULL);
		INSERT INTO price_observation
			(id, source, external_id, price_cents, edition_id, acquired_as, raw, dismissed_at)
		OVERRIDING SYSTEM VALUE
		VALUES
			(1, 'olx', 'space-oddity', 10000, 1, 'used',
				'{"title":"David Bowie Space Oddity Parlophone 2016 LP","wishlist_copy_id":1}', NULL),
			(2, 'olx', 'low', 2000, 1, 'used',
				'{"title":"David Bowie Low LP","wishlist_copy_id":1}', NULL),
			(3, 'olx', 'blackstar', 3000, 1, 'used',
				'{"title":"David Bowie Blackstar LP","wishlist_copy_id":1}', now()),
			(4, 'olx', 'artist-only', 4000, 1, 'used',
				'{"title":"David Bowie LP","wishlist_copy_id":1}', NULL);
		INSERT INTO deal_alert (observation_id, copy_id, queued_price_cents, sent_at)
		VALUES (1, 1, 10000, NULL), (2, 1, 2000, now()), (4, 1, 4000, NULL);`)

	dealStore := deals.NewStore(pool)
	for _, filter := range []deals.RecentFilter{{Limit: 1, Matched: true}, {Limit: 1, UnderLimit: true}} {
		got, err := dealStore.Recent(ctx, filter)
		if err != nil || len(got) != 1 || got[0].ExternalID != "space-oddity" {
			t.Fatalf("recent with %+v = %+v, %v", filter, got, err)
		}
	}
	sent, err := dealStore.Recent(ctx, deals.RecentFilter{Limit: 10, Sent: true})
	if err != nil || len(sent) != 0 {
		t.Fatalf("sent matches = %+v, %v", sent, err)
	}
	all, err := dealStore.Recent(ctx, deals.RecentFilter{Limit: 1})
	if err != nil || len(all) != 1 || all[0].ExternalID != "artist-only" || all[0].MaxPriceCents != nil {
		t.Fatalf("unfiltered observations = %+v, %v", all, err)
	}
	alerts, err := dealStore.PendingAlerts(ctx)
	if err != nil || len(alerts) != 1 || alerts[0].ExternalID != "space-oddity" {
		t.Fatalf("pending alerts = %+v, %v", alerts, err)
	}

	store := NewStore(pool)
	const window = 90 * 24 * time.Hour
	prices, err := store.Prices(ctx, window)
	if err != nil || len(prices) != 1 || prices[0].Cents != 10000 {
		t.Fatalf("valuation prices = %+v, %v", prices, err)
	}
	markets, err := store.Markets(ctx, window, 0)
	if err != nil || len(markets) != 1 || markets[0].Count != 1 || markets[0].Cents != 10000 {
		t.Fatalf("wish markets = %+v, %v", markets, err)
	}
	var dismissed, wasSent, pending bool
	err = pool.QueryRow(ctx, `SELECT
		EXISTS (SELECT FROM price_observation WHERE id = 3 AND dismissed_at IS NOT NULL),
		EXISTS (SELECT FROM deal_alert WHERE observation_id = 2 AND sent_at IS NOT NULL),
		EXISTS (SELECT FROM deal_alert WHERE observation_id = 4 AND sent_at IS NULL)`).
		Scan(&dismissed, &wasSent, &pending)
	if err != nil || !dismissed || !wasSent || !pending {
		t.Fatalf("legacy decisions = dismissed %t, sent %t, pending %t, %v", dismissed, wasSent, pending, err)
	}
}
