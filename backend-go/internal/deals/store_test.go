package deals

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// wished stores w on the wishlist, as wishlist does, and returns it with
// the ids of its copy, edition and title filled in.
func wished(t *testing.T, pool *pgxpool.Pool, w Want) Want {
	t.Helper()
	w.CopyID = wishlist(t, pool, &w)
	if err := pool.QueryRow(context.Background(), `SELECT e.id, e.title_id FROM copy c
		JOIN edition e ON e.id = c.edition_id WHERE c.id = $1`, w.CopyID).Scan(&w.EditionID, &w.TitleID); err != nil {
		t.Fatal(err)
	}
	return w
}

// storedListing is how price_observation and deal_alert hold one listing.
type storedListing struct {
	ID                                              int64
	EditionID, TitleID, WishlistCopyID, AlertCopyID *int64
	AlertSent                                       bool
	AlertedPriceCents                               *int64
}

// stored reads the listing the tests record, OLX listing "1".
func stored(t *testing.T, pool *pgxpool.Pool) storedListing {
	t.Helper()
	var s storedListing
	if err := pool.QueryRow(context.Background(), `SELECT o.id, o.edition_id, o.title_id, o.wishlist_copy_id,
			a.copy_id, a.sent_at IS NOT NULL, a.alerted_price_cents
		FROM price_observation o LEFT JOIN deal_alert a ON a.observation_id = o.id
		WHERE o.source = 'olx' AND o.external_id = '1'`).Scan(&s.ID, &s.EditionID, &s.TitleID,
		&s.WishlistCopyID, &s.AlertCopyID, &s.AlertSent, &s.AlertedPriceCents); err != nil {
		t.Fatal(err)
	}
	return s
}

func is(p *int64, want int64) bool { return p != nil && *p == want }

func deref(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

// TestRecordMovesToAnotherTitle records "Alien Covenant DVD" for an
// "Alien" wish with a queued alert, then for "Alien: Covenant": a wish
// with or without a limit, or an owned edition as the valuation run
// records it. The old wish's alert is sent first in some cases. The deals
// view, filtered to wished listings or not, shows it under its new title
// only, with the state of its alert.
func TestRecordMovesToAnotherTitle(t *testing.T) {
	listing := Listing{Source: "olx", ExternalID: "1", Title: "Alien Covenant DVD", URL: "https://example.test/1", PriceCents: 3000}
	tests := []struct {
		name      string
		sent      bool
		limit     int64
		valuation bool
		pending   string
		state     string
	}{
		{name: "an unsent alert never goes out for the old wish", state: AlertPending},
		{name: "an unsent alert goes out for the new wish", limit: 5000, pending: "Alien: Covenant", state: AlertPending},
		{name: "a sent alert stays recorded", sent: true, limit: 5000, state: AlertSent},
		{name: "a valuation match drops the old wish", valuation: true, state: AlertNone},
		{name: "a valuation match keeps a sent alert off the old wish", sent: true, valuation: true, state: AlertSent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			ctx := context.Background()
			store := NewStore(pool)
			alien := wished(t, pool, Want{Kind: "movie", Name: "Alien", Format: new("dvd"), MaxPriceCents: new(int64(5000))})
			covenant := Want{Kind: "movie", Name: "Alien: Covenant", Format: new("dvd")}
			if tt.limit != 0 {
				covenant.MaxPriceCents = new(tt.limit)
			}
			covenant = wished(t, pool, covenant)
			if tt.valuation {
				if _, err := pool.Exec(ctx, `UPDATE copy SET status = 'owned', max_price_cents = NULL
					WHERE id = $1`, covenant.CopyID); err != nil {
					t.Fatal(err)
				}
				covenant.CopyID = 0
				covenant.MaxPriceCents = nil
			}

			l := listing
			if isNew, alerted, err := store.Record(ctx, &l, &alien, true); err != nil || !isNew || !alerted {
				t.Fatalf("first Record = %v, %v, %v; want a new listing with an alert", isNew, alerted, err)
			}
			if tt.sent {
				if err := store.MarkSent(ctx, stored(t, pool).ID, l.PriceCents); err != nil {
					t.Fatal(err)
				}
			}
			alert := covenant.MaxPriceCents != nil && l.PriceCents <= *covenant.MaxPriceCents
			if isNew, _, err := store.Record(ctx, &l, &covenant, alert); err != nil || isNew {
				t.Fatalf("second Record = %v, %v; want the stored listing", isNew, err)
			}

			got := stored(t, pool)
			if !is(got.EditionID, covenant.EditionID) || !is(got.TitleID, covenant.TitleID) {
				t.Fatalf("listing on edition %v of title %v, want edition %d of title %d",
					deref(got.EditionID), deref(got.TitleID), covenant.EditionID, covenant.TitleID)
			}
			switch {
			case tt.valuation && got.WishlistCopyID != nil:
				t.Fatalf("listing still recorded for wish %d", *got.WishlistCopyID)
			case !tt.valuation && !is(got.WishlistCopyID, covenant.CopyID):
				t.Fatalf("listing recorded for wish %v, want %d", deref(got.WishlistCopyID), covenant.CopyID)
			case tt.sent && (!got.AlertSent || !is(got.AlertCopyID, alien.CopyID) || !is(got.AlertedPriceCents, 3000)):
				t.Fatalf("alert = %+v, want the sent alert for wish %d at 3000 kept", got, alien.CopyID)
			case !tt.sent && is(got.AlertCopyID, alien.CopyID):
				t.Fatalf("unsent alert still for the old wish %d", alien.CopyID)
			}

			pending, err := store.PendingAlerts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var titles []string
			for _, a := range pending {
				titles = append(titles, a.TitleName)
			}
			if tt.pending == "" && len(pending) != 0 || tt.pending != "" && (len(pending) != 1 || titles[0] != tt.pending) {
				t.Fatalf("pending alerts for %q, want %q", titles, tt.pending)
			}

			for _, matched := range []bool{false, true} {
				recent, err := store.Recent(ctx, RecentFilter{Limit: 10, Matched: matched})
				if err != nil {
					t.Fatal(err)
				}
				if matched && tt.valuation {
					if len(recent) != 0 {
						t.Fatalf("wished listings = %+v, want none after a valuation match", recent)
					}
					continue
				}
				if len(recent) != 1 {
					t.Fatalf("deals view (matched %v) has %d listings, want 1", matched, len(recent))
				}
				var name string
				if recent[0].TitleName != nil {
					name = *recent[0].TitleName
				}
				if name != "Alien: Covenant" || recent[0].Alert != tt.state {
					t.Fatalf("deals view (matched %v) shows %q with alert %s, want Alien: Covenant with alert %s",
						matched, name, recent[0].Alert, tt.state)
				}
			}
		})
	}
}

// TestRecordKeepsTheEditionOfTheSameTitle records a listing for a DVD wish
// and then for a Blu-ray wish of the same title: it keeps the DVD edition
// and that wish's alert, as before listings moved between titles.
func TestRecordKeepsTheEditionOfTheSameTitle(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	dvd := wished(t, pool, Want{Kind: "movie", Name: "Alien", Format: new("dvd"), MaxPriceCents: new(int64(5000))})
	bluray := Want{TitleID: dvd.TitleID, Kind: "movie", Name: "Alien", Format: new("bluray"), MaxPriceCents: new(int64(5000))}
	if err := pool.QueryRow(ctx, `INSERT INTO edition (title_id, format) VALUES ($1, 'bluray') RETURNING id`,
		dvd.TitleID).Scan(&bluray.EditionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO copy (edition_id, status, max_price_cents)
		VALUES ($1, 'wishlist', 5000) RETURNING id`, bluray.EditionID).Scan(&bluray.CopyID); err != nil {
		t.Fatal(err)
	}
	l := Listing{Source: "olx", ExternalID: "1", Title: "Alien DVD Blu-ray", URL: "https://example.test/1", PriceCents: 3000}
	for _, w := range []*Want{&dvd, &bluray} {
		if _, _, err := store.Record(ctx, &l, w, true); err != nil {
			t.Fatal(err)
		}
	}
	got := stored(t, pool)
	if !is(got.EditionID, dvd.EditionID) || !is(got.AlertCopyID, dvd.CopyID) || !is(got.WishlistCopyID, bluray.CopyID) {
		t.Fatalf("listing = %+v, want edition %d and its alert for wish %d kept, recorded for wish %d",
			got, dvd.EditionID, dvd.CopyID, bluray.CopyID)
	}
}

// TestSendAlertSkipsAnAlertMovedAway reads the "Alien" wish's alert as the
// hunter does before it sends, then a valuation run moves the listing to
// "Alien: Covenant": the hunter's send finds nothing to send.
func TestSendAlertSkipsAnAlertMovedAway(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	alien := wished(t, pool, Want{Kind: "movie", Name: "Alien", Format: new("dvd"), MaxPriceCents: new(int64(5000))})
	covenant := wished(t, pool, Want{Kind: "movie", Name: "Alien: Covenant", Format: new("dvd")})
	if _, err := pool.Exec(ctx, `UPDATE copy SET status = 'owned' WHERE id = $1`, covenant.CopyID); err != nil {
		t.Fatal(err)
	}
	covenant.CopyID = 0
	l := Listing{Source: "olx", ExternalID: "1", Title: "Alien Covenant DVD", URL: "https://example.test/1", PriceCents: 3000}
	if _, _, err := store.Record(ctx, &l, &alien, true); err != nil {
		t.Fatal(err)
	}
	pending, err := store.PendingAlerts(ctx)
	if err != nil || len(pending) != 1 {
		t.Fatalf("PendingAlerts = %d, %v; want the Alien alert", len(pending), err)
	}
	if _, _, err := store.Record(ctx, &l, &covenant, false); err != nil {
		t.Fatal(err)
	}
	var sent []string
	delivered, err := store.SendAlert(ctx, pending[0].ObservationID, func(a *Alert) error {
		sent = append(sent, a.TitleName)
		return nil
	})
	if err != nil || delivered || len(sent) != 0 {
		t.Fatalf("SendAlert = %v, %v, sent %q; want nothing sent after the move", delivered, err, sent)
	}
	if got := stored(t, pool); got.AlertSent {
		t.Fatalf("alert = %+v, want it unsent", got)
	}
}
