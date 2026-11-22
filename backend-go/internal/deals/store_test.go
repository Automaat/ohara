package deals

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
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

// TestUnmatchTakesOnlyNamedUnsentAlerts: Unmatch takes the unsent alerts
// of the named listings of one source off their wish and leaves a sent
// alert, a listing it does not name and another source's listing with the
// same ID.
func TestUnmatchTakesOnlyNamedUnsentAlerts(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	wish := wished(t, pool, bladeRunner("bluray"))
	for _, l := range []Listing{
		{Source: "olx", ExternalID: "sent"},
		{Source: "olx", ExternalID: "pending"},
		{Source: "olx", ExternalID: "other"},
		{Source: "bluedvd", ExternalID: "pending"},
	} {
		l.Title, l.URL, l.PriceCents = "Blade Runner Blu-ray", "https://example.test/"+l.ExternalID, 4000
		if _, alerted, err := store.Record(ctx, &l, &wish, true); err != nil || !alerted {
			t.Fatalf("Record %s %s = %v, %v; want an alert", l.Source, l.ExternalID, alerted, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE deal_alert a SET sent_at = now() FROM price_observation o
		WHERE o.id = a.observation_id AND o.external_id = 'sent'`); err != nil {
		t.Fatal(err)
	}

	if err := store.Unmatch(ctx, "olx", []string{"sent", "pending", "never-stored"}); err != nil {
		t.Fatalf("Unmatch: %v", err)
	}
	rows, err := pool.Query(ctx, `SELECT o.source || ' ' || o.external_id FROM deal_alert a
		JOIN price_observation o ON o.id = a.observation_id WHERE a.copy_id = $1 ORDER BY 1`, wish.CopyID)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"bluedvd pending", "olx other", "olx sent"}; !slices.Equal(bound, want) {
		t.Fatalf("alerts still for the wish = %q, want %q", bound, want)
	}
}

// TestUnmatchWaitsForASendInProgress: Unmatch of a listing whose alert
// SendAlert is sending waits for its outcome, so the message goes out and
// its sent alert keeps its wish.
func TestUnmatchWaitsForASendInProgress(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	w := bladeRunner("bluray")
	w.MaxPriceCents = new(int64(5000))
	wish := wished(t, pool, w)
	l := Listing{Source: "olx", ExternalID: "1", Title: "Blade Runner Blu-ray", URL: "https://example.test/1", PriceCents: 4000}
	if _, alerted, err := store.Record(ctx, &l, &wish, true); err != nil || !alerted {
		t.Fatalf("Record = %v, %v; want an alert", alerted, err)
	}
	unmatched := make(chan error, 1)
	delivered, err := store.SendAlert(ctx, stored(t, pool).ID, func(*Alert) error {
		go func() { unmatched <- store.Unmatch(ctx, "olx", []string{"1"}) }()
		return waitForUnmatch(ctx, pool, unmatched)
	})
	if err != nil || !delivered {
		t.Fatalf("SendAlert = %v, %v; want the message sent", delivered, err)
	}
	if err := <-unmatched; err != nil {
		t.Fatalf("Unmatch: %v", err)
	}
	if got := stored(t, pool); !got.AlertSent || !is(got.AlertCopyID, wish.CopyID) {
		t.Fatalf("alert = sent %v for copy %v, want sent for %d", got.AlertSent, deref(got.AlertCopyID), wish.CopyID)
	}
}

// waitForUnmatch returns nil once Unmatch waits for a row lock, or an error
// when it finished first or never waited.
func waitForUnmatch(ctx context.Context, pool *pgxpool.Pool, unmatched <-chan error) error {
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		select {
		case err := <-unmatched:
			return fmt.Errorf("unmatch finished during the send: %w", err)
		default:
		}
		var waiting bool
		if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT FROM pg_stat_activity
			WHERE datname = current_database() AND wait_event_type = 'Lock' AND query LIKE 'WITH locked AS%')`).
			Scan(&waiting); err != nil {
			return err
		}
		if waiting {
			return nil
		}
	}
	return errors.New("unmatch never waited for the listing's row")
}

// TestRecordAsOfKeepsANewerMove follows a hunt that read the wishlist
// before a valuation run moved its "Alien" listing to the owned "Alien:
// Covenant" and matches the listing to "Alien" again afterwards: the
// listing stays with "Alien: Covenant" and the old wish gets no alert. A
// hunt that starts after the move moves it back.
func TestRecordAsOfKeepsANewerMove(t *testing.T) {
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
	if _, alerted, err := store.RecordAsOf(ctx, &l, &alien, true, dbNow(t, store)); err != nil || !alerted {
		t.Fatalf("first hunt = %v, %v; want an alert", alerted, err)
	}
	staleHunt := dbNow(t, store)
	if _, _, err := store.RecordAsOf(ctx, &l, &covenant, false, dbNow(t, store)); err != nil {
		t.Fatal(err)
	}
	if _, alerted, err := store.RecordAsOf(ctx, &l, &alien, true, staleHunt); err != nil || alerted {
		t.Fatalf("stale hunt = %v, %v; want no alert", alerted, err)
	}
	got := stored(t, pool)
	if !is(got.TitleID, covenant.TitleID) || !is(got.EditionID, covenant.EditionID) ||
		got.WishlistCopyID != nil || got.AlertCopyID != nil {
		t.Fatalf("after the stale hunt listing = %+v, want it on edition %d with no wish", got, covenant.EditionID)
	}
	if pending, err := store.PendingAlerts(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending alerts = %+v, %v; want none", pending, err)
	}

	if _, _, err := store.RecordAsOf(ctx, &l, &alien, true, dbNow(t, store)); err != nil {
		t.Fatal(err)
	}
	got = stored(t, pool)
	if !is(got.TitleID, alien.TitleID) || !is(got.WishlistCopyID, alien.CopyID) || !is(got.AlertCopyID, alien.CopyID) {
		t.Fatalf("after a later hunt listing = %+v, want it back on wish %d", got, alien.CopyID)
	}
	if pending, err := store.PendingAlerts(ctx); err != nil || len(pending) != 1 || pending[0].TitleName != "Alien" {
		t.Fatalf("pending alerts = %+v, %v; want the Alien alert", pending, err)
	}
}

// TestRecordAsOfMovesAStaleRunsInsert has a hunt that read the wishlist
// before an owned "Alien: Covenant" existed store a listing for the
// "Alien" wish, whose alert then fails to send. A valuation run that read
// its holdings after "Alien: Covenant" was added moves the listing there,
// and the "Alien" alert no longer waits.
func TestRecordAsOfMovesAStaleRunsInsert(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	alien := wished(t, pool, Want{Kind: "movie", Name: "Alien", Format: new("dvd"), MaxPriceCents: new(int64(5000))})
	staleHunt := dbNow(t, store)
	covenant := wished(t, pool, Want{Kind: "movie", Name: "Alien: Covenant", Format: new("dvd")})
	if _, err := pool.Exec(ctx, `UPDATE copy SET status = 'owned' WHERE id = $1`, covenant.CopyID); err != nil {
		t.Fatal(err)
	}
	covenant.CopyID = 0
	newerValuation := dbNow(t, store)
	l := Listing{Source: "olx", ExternalID: "1", Title: "Alien Covenant DVD", URL: "https://example.test/1", PriceCents: 3000}
	if _, alerted, err := store.RecordAsOf(ctx, &l, &alien, true, staleHunt); err != nil || !alerted {
		t.Fatalf("stale hunt = %v, %v; want an alert", alerted, err)
	}
	if err := store.MarkFailed(ctx, stored(t, pool).ID, l.PriceCents, "telegram down"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.RecordAsOf(ctx, &l, &covenant, false, newerValuation); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool); !is(got.TitleID, covenant.TitleID) || got.AlertCopyID != nil {
		t.Fatalf("listing = %+v, want it on title %d with no alert wish", got, covenant.TitleID)
	}
	if pending, err := store.PendingAlerts(ctx); err != nil || len(pending) != 0 {
		t.Fatalf("pending alerts = %+v, %v; want none", pending, err)
	}
}

// TestRecordAsOfAlertsAfterAStaleRunsInsert has a valuation run that read
// its holdings before "Alien: Covenant" was wished for store a listing for
// the owned "Alien". A hunt that read the wishlist after that moves it to
// the "Alien: Covenant" wish and queues its alert at once.
func TestRecordAsOfAlertsAfterAStaleRunsInsert(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	alien := wished(t, pool, Want{Kind: "movie", Name: "Alien", Format: new("dvd")})
	if _, err := pool.Exec(ctx, `UPDATE copy SET status = 'owned' WHERE id = $1`, alien.CopyID); err != nil {
		t.Fatal(err)
	}
	alien.CopyID = 0
	staleValuation := dbNow(t, store)
	covenant := wished(t, pool, Want{Kind: "movie", Name: "Alien: Covenant", Format: new("dvd"), MaxPriceCents: new(int64(5000))})
	newerHunt := dbNow(t, store)
	l := Listing{Source: "olx", ExternalID: "1", Title: "Alien Covenant DVD", URL: "https://example.test/1", PriceCents: 3000}
	if _, _, err := store.RecordAsOf(ctx, &l, &alien, false, staleValuation); err != nil {
		t.Fatal(err)
	}
	if _, alerted, err := store.RecordAsOf(ctx, &l, &covenant, true, newerHunt); err != nil || !alerted {
		t.Fatalf("newer hunt = %v, %v; want an alert", alerted, err)
	}
	if got := stored(t, pool); !is(got.TitleID, covenant.TitleID) || !is(got.WishlistCopyID, covenant.CopyID) {
		t.Fatalf("listing = %+v, want it recorded for wish %d", got, covenant.CopyID)
	}
}

// TestRecordAsOfMovesARunsOwnInsert has one run store a listing for
// "Alien" and, after the seller renamed it, match it to "Alien: Covenant"
// in a later query: the run moves its own listing.
func TestRecordAsOfMovesARunsOwnInsert(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	alien := wished(t, pool, Want{Kind: "movie", Name: "Alien", Format: new("dvd")})
	covenant := wished(t, pool, Want{Kind: "movie", Name: "Alien: Covenant", Format: new("dvd")})
	hunt := dbNow(t, store)
	l := Listing{Source: "olx", ExternalID: "1", Title: "Alien DVD", URL: "https://example.test/1", PriceCents: 3000}
	if _, _, err := store.RecordAsOf(ctx, &l, &alien, false, hunt); err != nil {
		t.Fatal(err)
	}
	l.Title = "Alien Covenant DVD"
	if _, _, err := store.RecordAsOf(ctx, &l, &covenant, false, hunt); err != nil {
		t.Fatal(err)
	}
	if got := stored(t, pool); !is(got.TitleID, covenant.TitleID) || !is(got.WishlistCopyID, covenant.CopyID) {
		t.Fatalf("listing = %+v, want it moved to wish %d", got, covenant.CopyID)
	}
}

// dbNow is the database's time, as a run reads it before its data.
func dbNow(t *testing.T, store *Store) time.Time {
	t.Helper()
	at, err := store.Now(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func TestWantsCarryEditionBarcode(t *testing.T) {
	pool := dbtest.Migrated(t)
	polish := wished(t, pool, diuna(new("9788381880206")))
	none := wished(t, pool, diuna(nil))
	wants, err := NewStore(pool).Wants(context.Background())
	if err != nil {
		t.Fatalf("Wants: %v", err)
	}
	got := map[int64]*string{}
	for _, w := range wants {
		got[w.CopyID] = w.Barcode
	}
	if b := got[polish.CopyID]; b == nil || *b != "9788381880206" {
		t.Errorf("Polish wish barcode = %v, want 9788381880206", b)
	}
	if b, ok := got[none.CopyID]; !ok || b != nil {
		t.Errorf("barcode-less wish barcode = %v (listed %v), want nil", b, ok)
	}
	for _, w := range wants {
		if w.CopyID == polish.CopyID && !slices.Equal(w.Queries(), []string{"Diuna herbert"}) {
			t.Errorf("stored Polish wish queries = %q, want [Diuna herbert]", w.Queries())
		}
	}
}

func TestWantsCarryEditionDetails(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	w := wished(t, pool, Want{Kind: "movie", Name: "Blade Runner 2049", Format: new("bluray")})
	if _, err := pool.Exec(ctx, `UPDATE edition SET packaging = '{steelbook}', pl_voiceover = 'yes',
		pl_subtitles = 'no' WHERE id = $1`, w.EditionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE copy SET exact_edition = true WHERE id = $1`, w.CopyID); err != nil {
		t.Fatal(err)
	}
	book := wished(t, pool, diuna(new("9788381880206")))
	if _, err := pool.Exec(ctx, `UPDATE edition SET binding = 'hardcover' WHERE id = $1`, book.EditionID); err != nil {
		t.Fatal(err)
	}
	wants, err := NewStore(pool).Wants(ctx)
	if err != nil {
		t.Fatalf("Wants: %v", err)
	}
	got := map[int64]Want{}
	for _, w := range wants {
		got[w.CopyID] = w
	}
	movie := got[w.CopyID]
	if !movie.ExactEdition || !slices.Equal(movie.Packaging, []string{"steelbook"}) ||
		movie.PLVoiceover != "yes" || movie.PLSubtitles != "no" || movie.Binding != nil {
		t.Errorf("movie want = %+v, want an exact steelbook with Polish voice-over", movie)
	}
	if !movie.Matches("Blade Runner 2049 Blu-ray steelbook lektor") || movie.Matches("Blade Runner 2049 Blu-ray") {
		t.Error("stored exact movie want matches as a want of any edition")
	}
	b := got[book.CopyID]
	if b.ExactEdition || b.Binding == nil || *b.Binding != "hardcover" || b.PLVoiceover != "unknown" {
		t.Errorf("book want = %+v, want any edition, hardcover", b)
	}
	if !b.Matches("Diuna Frank Herbert Rebis") {
		t.Error("stored book want of any edition needs its binding")
	}
}

// TestExactEditionHoldsStoredListings checks that turning a wish into one
// of only its edition holds the listings stored for it before: their
// queued alerts no longer go out and the wished deals lists drop them,
// while every list keeps the listing itself.
func TestExactEditionHoldsStoredListings(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	w := wished(t, pool, Want{Kind: "movie", Name: "Blade Runner 2049", Format: new("bluray"), MaxPriceCents: new(int64(10000))})
	if _, err := pool.Exec(ctx, `UPDATE edition SET packaging = '{steelbook}' WHERE id = $1`, w.EditionID); err != nil {
		t.Fatal(err)
	}
	for _, l := range []Listing{
		{Source: "olx", ExternalID: "plain", Title: "Blade Runner 2049 Blu-ray", PriceCents: 3000},
		{Source: "olx", ExternalID: "steelbook", Title: "Blade Runner 2049 Blu-ray steelbook", PriceCents: 5000},
		{Source: "olx", ExternalID: "legacy", Title: "Blade Runner 2049 Blu-ray steelbook", PriceCents: 4000},
	} {
		if _, alerted, err := store.Record(ctx, &l, &w, true); err != nil || !alerted {
			t.Fatalf("record %s: alerted=%v err=%v", l.ExternalID, alerted, err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE price_observation SET edition_details = NULL WHERE external_id = 'legacy'`); err != nil {
		t.Fatal(err)
	}
	pending := func() []string {
		t.Helper()
		alerts, err := store.PendingAlerts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, a := range alerts {
			ids = append(ids, a.ExternalID)
		}
		slices.Sort(ids)
		return ids
	}
	recent := func(f RecentFilter) map[string]bool {
		t.Helper()
		f.Limit = 10
		observations, err := store.Recent(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		wished := map[string]bool{}
		for _, o := range observations {
			wished[o.ExternalID] = o.MaxPriceCents != nil
		}
		return wished
	}
	if got := pending(); !slices.Equal(got, []string{"legacy", "plain", "steelbook"}) {
		t.Fatalf("pending alerts of a wish of any edition = %v", got)
	}

	if _, err := pool.Exec(ctx, `UPDATE copy SET exact_edition = true WHERE id = $1`, w.CopyID); err != nil {
		t.Fatal(err)
	}
	if got := pending(); !slices.Equal(got, []string{"steelbook"}) {
		t.Errorf("pending alerts of a wish of only its edition = %v, want [steelbook]", got)
	}
	for _, f := range []RecentFilter{{Matched: true}, {UnderLimit: true}} {
		if got := recent(f); len(got) != 1 || !got["steelbook"] {
			t.Errorf("Recent(%+v) = %v, want the steelbook only", f, got)
		}
	}
	if got := recent(RecentFilter{}); len(got) != 3 || got["plain"] || got["legacy"] || !got["steelbook"] {
		t.Errorf("Recent() = %v, want every listing, only the steelbook for the wish", got)
	}

	if _, err := pool.Exec(ctx, `UPDATE edition SET packaging = '{}' WHERE id = $1`, w.EditionID); err != nil {
		t.Fatal(err)
	}
	if got := pending(); !slices.Equal(got, []string{"legacy", "plain", "steelbook"}) {
		t.Errorf("pending alerts of a wish whose edition needs nothing = %v", got)
	}
}

// TestRecentFreshAsOfSnapshot pins the clock a filtered page judges a Discogs listing's
// freshness by: its snapshot's, so a listing that expired before an alert was sent is not shown
// with that alert, even when the send commits after the page's transaction began.
func TestRecentFreshAsOfSnapshot(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := NewStore(pool)
	ctx := context.Background()
	want := wished(t, pool, *wantBluray(new(int64(5000))))
	listing := Listing{Source: DiscogsSource, ExternalID: "expiring", Title: "Blade Runner Blu-ray", PriceCents: 4000}
	if _, alerted, err := store.Record(ctx, &listing, &want, true); err != nil || !alerted {
		t.Fatalf("record = %v, %v", alerted, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE price_observation
		SET last_offered_at = now() - make_interval(secs => $1) + interval '2 seconds'`,
		discogsShown.Seconds()); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	time.Sleep(3 * time.Second)
	if _, err := pool.Exec(ctx, `UPDATE deal_alert SET sent_at = now(), attempts = 1`); err != nil {
		t.Fatal(err)
	}
	wishes, err := readWishedCopies(ctx, tx)
	if err != nil {
		t.Fatal(err)
	}
	page, err := wishedPage(ctx, tx, RecentFilter{Limit: 1, Sent: true},
		marketTable{titles: []int64{}, formats: []string{}, cents: []int64{}}, wishes, []int64{})
	if err != nil || len(page) != 0 {
		t.Fatalf("page = %v, %v; want the expired listing left out", page, err)
	}
}

// TestRecentLeavesOutUnverifiedAlbums pins the album recheck on both pages: a stored listing its
// album wish no longer verifies keeps its place in the deals list without that wish, and no
// filter keeps it, whether the hunter recorded it for the wish or only a sent alert names the
// wish.
func TestRecentLeavesOutUnverifiedAlbums(t *testing.T) {
	for _, tc := range []struct {
		name     string
		recorded bool
	}{
		{name: "recorded for the wish", recorded: true},
		{name: "alert only"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pool := dbtest.Migrated(t)
			store := NewStore(pool)
			ctx := context.Background()
			want := wished(t, pool, Want{
				Kind: "music", Name: "David Bowie aka Space Oddity", Authors: []string{"David Bowie"},
				Format: new("vinyl"), MaxPriceCents: new(int64(9000)),
			})
			raw := `jsonb_build_object('title', $4::text)`
			if tc.recorded {
				raw = `jsonb_build_object('title', $4::text, 'wishlist_copy_id', $5::bigint)`
			}
			for _, l := range []struct{ id, title string }{
				{id: "verified", title: "David Bowie – Space Oddity (Parlophone 2016) LP"},
				{id: "unverified", title: "David Bowie – Hunky Dory Parlophone 2016 LP"},
			} {
				if _, err := pool.Exec(ctx, `WITH o AS (INSERT INTO price_observation
						(source, external_id, price_cents, url, edition_id, title_id, raw)
					VALUES ('olx', $1, 5000, 'https://www.olx.pl/d/oferta/' || $1 || '.html', $2, $3, `+raw+`)
					RETURNING id)
					INSERT INTO deal_alert (observation_id, copy_id, sent_at, attempts)
					SELECT id, $5::bigint, now(), 1 FROM o`,
					l.id, want.EditionID, want.TitleID, l.title, want.CopyID); err != nil {
					t.Fatal(err)
				}
			}
			all, err := store.Recent(ctx, RecentFilter{Limit: 10})
			if err != nil || !slices.Equal(externalIDs(all), []string{"unverified", "verified"}) {
				t.Fatalf("Recent() = %v, %v; want both listings", externalIDs(all), err)
			}
			if all[0].MaxPriceCents != nil || all[1].MaxPriceCents == nil {
				t.Fatalf("Recent() limits = %v, %v; want the wish's only on the verified listing",
					all[0].MaxPriceCents, all[1].MaxPriceCents)
			}
			for _, f := range []RecentFilter{
				{Limit: 10, Matched: true}, {Limit: 10, UnderLimit: true}, {Limit: 10, Sent: true},
			} {
				got, err := store.Recent(ctx, f)
				if err != nil || !slices.Equal(externalIDs(got), []string{"verified"}) {
					t.Errorf("Recent(%+v) = %v, %v; want the verified listing only", f, externalIDs(got), err)
				}
			}
		})
	}
}
