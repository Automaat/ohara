package deals

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func recordComparableOffers(t *testing.T, store *Store, wish Want, samples int) {
	t.Helper()
	wish.CopyID = 0
	for i := range samples {
		l := automaticAlertOffer(10000)
		l.ExternalID = "sample-" + strconv.Itoa(i)
		if _, _, err := store.Record(context.Background(), &l, &wish, false); err != nil {
			t.Fatal(err)
		}
	}
}

func automaticAlertOffer(price int64) Listing {
	return Listing{
		Source: "olx", ExternalID: "current", Title: "Blade Runner Blu-ray",
		PriceCents: price, AcquiredAs: new("used"), URL: "https://example.test/current",
	}
}

func TestAutomaticAlertsRequireOptInAndTheOkazjePriceRule(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		samples int
		price   int64
		limit   *int64
		want    int
	}{
		{name: "off by default", samples: 5, price: 8000},
		{name: "four other offers", enabled: true, samples: 4, price: 8000},
		{name: "twenty percent below five other offers", enabled: true, samples: 5, price: 8000, want: 1},
		{name: "one grosz above the cutoff", enabled: true, samples: 5, price: 8001},
		{name: "manual limit takes precedence", enabled: true, samples: 5, price: 8000, limit: new(int64(7500))},
		{name: "manual alert with automatic off", price: 8000, limit: new(int64(8000)), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			src := &fakeSource{name: "olx", listings: []Listing{automaticAlertOffer(tt.price)}}
			notifier := &fakeNotifier{}
			h := newTestHunter(pool, notifier, src)
			h.cfg.AutomaticAlerts = tt.enabled
			wish := wished(t, pool, *wantBluray(tt.limit))
			recordComparableOffers(t, h.store, wish, tt.samples)

			first, repeat := mustRun(t, h), mustRun(t, h)

			if first.Alerts != tt.want || first.Sent != tt.want || repeat.Alerts != 0 || repeat.Sent != 0 ||
				len(notifier.messages()) != tt.want {
				t.Errorf("first run queued/sent %d/%d, repeat %d/%d, messages %q; want %d initial and zero repeats",
					first.Alerts, first.Sent, repeat.Alerts, repeat.Sent, notifier.messages(), tt.want)
			}
		})
	}
}

func TestAutomaticAlertsRecheckRetriesAndAppearInSent(t *testing.T) {
	pool := dbtest.Migrated(t)
	src := &fakeSource{name: "olx", listings: []Listing{automaticAlertOffer(8000)}}
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, src)
	h.cfg.AutomaticAlerts = true
	wish := wished(t, pool, *wantBluray(nil))
	recordComparableOffers(t, h.store, wish, 5)
	if first := mustRun(t, h); first.Alerts != 1 || first.Sent != 0 {
		t.Fatalf("first run queued/sent %d/%d, want 1/0 after delivery failure", first.Alerts, first.Sent)
	}
	h.sources = nil

	setSampleAge(t, pool, 91)
	if retry := mustRun(t, h); retry.Sent != 0 {
		t.Fatalf("retry sent %d alerts using only four current samples", retry.Sent)
	}
	setSampleAge(t, pool, 0)
	h.cfg.AutomaticAlerts = false
	if retry := mustRun(t, h); retry.Sent != 0 {
		t.Fatalf("retry sent %d automatic alerts after opt-out", retry.Sent)
	}
	h.cfg.AutomaticAlerts = true
	if retry := mustRun(t, h); retry.Alerts != 0 || retry.Sent != 1 {
		t.Fatalf("retry queued/sent %d/%d, want 0/1 after restoring baseline and opt-in", retry.Alerts, retry.Sent)
	}
	sent, err := h.store.Recent(context.Background(), RecentFilter{Sent: true, Limit: 10})
	if err != nil || len(sent) != 1 || sent[0].ExternalID != "current" || sent[0].Alert != AlertSent {
		t.Fatalf("sent list = %+v, %v; want the delivered automatic alert", sent, err)
	}
}

func TestAutomaticAlertsRepeatOnlyAtLowerPricesAndRespectDismissal(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	src := &fakeSource{name: "olx", listings: []Listing{automaticAlertOffer(8000)}}
	notifier := &fakeNotifier{}
	h := newTestHunter(pool, notifier, src)
	h.cfg.AutomaticAlerts = true
	recordComparableOffers(t, h.store, wished(t, pool, *wantBluray(nil)), 5)
	if first := mustRun(t, h); first.Sent != 1 {
		t.Fatalf("first run sent %d alerts, want 1", first.Sent)
	}

	src.listings[0].PriceCents = 7500
	if repeat := mustRun(t, h); repeat.Alerts != 1 || repeat.Sent != 1 {
		t.Fatalf("price drop queued/sent %d/%d, want 1/1", repeat.Alerts, repeat.Sent)
	}
	if sent := notifier.messages(); len(sent) != 2 || !strings.Contains(sent[1], "75,00 zł (było 80,00 zł)") {
		t.Fatalf("repeat message = %q, want new and previous prices", sent)
	}

	sent, err := h.store.Recent(ctx, RecentFilter{Sent: true, Limit: 10})
	if err != nil || len(sent) != 1 {
		t.Fatalf("sent list = %+v, %v; want one listing", sent, err)
	}
	if err := h.store.Dismiss(ctx, sent[0].ID); err != nil {
		t.Fatal(err)
	}
	src.listings[0].PriceCents = 7000
	if dismissed := mustRun(t, h); dismissed.Alerts != 0 || dismissed.Sent != 0 {
		t.Fatalf("dismissed listing queued/sent %d/%d alerts, want 0/0", dismissed.Alerts, dismissed.Sent)
	}
}

func setSampleAge(t *testing.T, pool *pgxpool.Pool, days int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `UPDATE price_observation
		SET last_seen_at = now() - make_interval(days => $1) WHERE external_id = 'sample-0'`, days); err != nil {
		t.Fatal(err)
	}
}
