package valuation

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/deals"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

// TestTVSeasonsAreListedWithoutAValue owns a Blu-ray of a TV season named
// like an owned film, with an ISBN-like code: the run asks no source about
// it, and Wartość lists the copy without a range, as any copy without
// prices.
func TestTVSeasonsAreListedWithoutAValue(t *testing.T) {
	pool := dbtest.Migrated(t)
	seed(t, pool)
	exec(t, pool, `INSERT INTO title (id, kind, name, season, tmdb_id) OVERRIDING SYSTEM VALUE
		VALUES (4, 'tv', 'Diuna: Proroctwo', 1, 90228)`)
	exec(t, pool, `INSERT INTO edition (id, title_id, barcode, format) OVERRIDING SYSTEM VALUE
		VALUES (40, 4, '9788308041239', 'bluray')`)
	exec(t, pool, `INSERT INTO copy (edition_id, status, price_cents) VALUES (40, 'owned', 9000)`)
	olx := &fakeSource{name: "olx", listings: []deals.Listing{
		listing("1", "Diuna Proroctwo sezon 1 Blu-ray", 4000, used()),
		listing("2", "Diuna Proroctwo Blu-ray", 4100, used()),
		listing("3", "Diuna Proroctwo Blu-ray", 4200, used()),
	}}
	bb := &fakeBuyBack{quotes: map[string]Quote{"9788308073209": {Offered: true, Cents: 537}}}
	r := newTestRunner(pool, bb, olx)
	logger := slog.New(slog.DiscardHandler)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{NewHandler(r.store, r, logger)}}))
	t.Cleanup(srv.Close)

	res, err := r.Run(context.Background())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Editions != 2 {
		t.Fatalf("Run valued %d editions, want the book and the film", res.Editions)
	}
	if strings.Join(bb.asked, ",") != "9788308073209" {
		t.Fatalf("buy-back asked for %v, want the book only", bb.asked)
	}
	for _, q := range olx.queries {
		if strings.Contains(q, "Proroctwo") {
			t.Fatalf("OLX asked %q for the TV season", q)
		}
	}
	var prices int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM price_observation WHERE edition_id = 40`).Scan(&prices); err != nil || prices != 0 {
		t.Fatalf("TV edition has %d prices, %v", prices, err)
	}

	var view View
	if code := get(t, srv.URL+"/api/valuation", &view); code != http.StatusOK {
		t.Fatalf("GET = %d", code)
	}
	var found bool
	for _, c := range view.Copies {
		if c.EditionID != 40 {
			continue
		}
		found = true
		if c.Kind != "tv" || c.Season == nil || *c.Season != 1 || c.Floor != nil || c.Mid != nil || c.Ceiling != nil || c.LikelySold != nil {
			t.Fatalf("TV copy = %+v, want it listed without a value", c)
		}
	}
	if !found {
		t.Fatalf("valuation does not list the TV copy: %+v", view.Copies)
	}
}
