package deals

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/server"
)

func TestAutomaticDealsRequireFiveOtherComparableOffers(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	wish := wished(t, pool, Want{Kind: "movie", Name: "Blade Runner", Format: new("bluray")})
	if _, err := pool.Exec(ctx, `UPDATE edition SET packaging = '{steelbook}' WHERE id = $1`, wish.EditionID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE copy SET exact_edition = true WHERE id = $1`, wish.CopyID); err != nil {
		t.Fatal(err)
	}
	record := func(w *Want, id, title string, price int64, condition *string) {
		t.Helper()
		l := Listing{
			Source: "olx", ExternalID: id, Title: title, PriceCents: price,
			URL: "https://example.test/" + id, AcquiredAs: condition,
		}
		if _, _, err := store.Record(ctx, &l, w, false); err != nil {
			t.Fatal(err)
		}
	}
	const title = "Blade Runner Blu-ray steelbook"
	used := new("used")
	record(&wish, "candidate", title, 8000, used)
	// Valuation sightings have no wish ID and still count when the listing verifies.
	valuation := wish
	valuation.CopyID = 0
	for i, price := range []int64{8000, 9000, 10000, 12000} {
		record(&valuation, strconv.Itoa(i), title, price, used)
	}
	record(&valuation, "0", title, 8000, used)
	record(&valuation, "expired", title, 11000, used)
	if _, err := pool.Exec(ctx, `UPDATE price_observation SET last_seen_at = now() - interval '91 days'
		WHERE external_id = 'expired'`); err != nil {
		t.Fatal(err)
	}
	record(&valuation, "unknown", title, 100000, nil)
	record(&valuation, "new", title, 100000, new("new"))
	record(&valuation, "dvd", "Blade Runner DVD steelbook", 100000, used)
	record(&valuation, "uhd", "Blade Runner 4K UHD steelbook", 100000, used)
	record(&valuation, "plain", "Blade Runner Blu-ray", 100000, used)
	record(&valuation, "digipak", "Blade Runner Blu-ray steelbook digipak", 100000, used)
	record(&valuation, "wrong-title", "Alien Blu-ray steelbook", 100000, used)

	logger := slog.New(slog.DiscardHandler)
	h := NewHandler(store, NewHunter(store, nil, nil, Config{}, logger), logger)
	srv := httptest.NewServer(server.New(server.Config{}, logger, server.Deps{DB: pool, API: []server.Mounter{h}}))
	t.Cleanup(srv.Close)
	base := srv.URL + "/api/deals"
	read := func() AutomaticDeal {
		t.Helper()
		var got MatchedView
		if code := call(t, http.MethodGet, base+"/matched?limit=500", nil, &got); code != http.StatusOK ||
			len(got.Observations) != 1 || len(got.Automatic) != 1 || got.Observations[0].ExternalID != "candidate" ||
			got.Automatic[0].ObservationID != got.Observations[0].ID {
			t.Fatalf("matched view = %d %+v", code, got)
		}
		return got.Automatic[0]
	}
	if got := read(); got.Qualifies || got.TypicalCents != nil || got.SampleCount != 4 {
		t.Fatalf("four unique comparisons = %+v, want no baseline", got)
	}

	record(&valuation, "fifth", title, 11000, used)
	if got := read(); !got.Qualifies || got.TypicalCents == nil || *got.TypicalCents != 10000 ||
		got.DiscountPercent == nil || *got.DiscountPercent != 20 || got.SampleCount != 5 {
		t.Fatalf("five comparable offers = %+v, want 20%% below 10000", got)
	}
	record(&wish, "candidate", title, 8001, used)
	if got := read(); got.Qualifies || got.DiscountPercent == nil || *got.DiscountPercent != 19 {
		t.Fatalf("price one grosz above threshold = %+v", got)
	}

	record(&wish, "candidate", title, 8000, used)
	var legacy map[string]any
	if code := call(t, http.MethodGet, base+"?under_limit=true", nil, &legacy); code != http.StatusOK {
		t.Fatalf("GET manual under limit = %d", code)
	}
	if _, exists := legacy["automatic"]; exists {
		t.Fatal("existing view carries an automatic field")
	}
	if rows, ok := legacy["observations"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("manual under limit = %v, want no automatic listings", legacy["observations"])
	}
}

func TestAutomaticDealsExcludeAmbiguousAlbumComparisons(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	store := NewStore(pool)
	wish := wished(t, pool, Want{
		Kind: "music", Name: "David Bowie", OriginalTitle: new("Space Oddity"),
		Authors: []string{"David Bowie"}, Format: new("vinyl"),
	})
	const verified = "Space Oddity – David Bowie LP"
	for i, title := range []string{verified, verified, verified, verified, verified, "David Bowie LP", "David Bowie Low LP"} {
		w := wish
		price := int64(10000)
		if i == 0 {
			price = 6000
		} else {
			w.CopyID = 0
		}
		l := Listing{Source: "olx", ExternalID: strconv.Itoa(i), Title: title, PriceCents: price, AcquiredAs: new("used")}
		if _, _, err := store.Record(ctx, &l, &w, false); err != nil {
			t.Fatal(err)
		}
	}
	observations, comparisons, err := store.recent(ctx, RecentFilter{Matched: true, Limit: 10}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) != 1 || observations[0].ExternalID != "0" || len(comparisons) != 1 {
		t.Fatalf("matched album observations = %+v, comparisons = %+v", observations, comparisons)
	}
	if got := comparisons[0]; got.SampleCount != 4 || got.Qualifies {
		t.Fatalf("album comparison = %+v, want four verified comparables", got)
	}
}

func TestAutomaticDealsUseTheMedianOfEvenSamples(t *testing.T) {
	prices := []int64{3000, 4000, 5000, 5001, 6000, 7000}
	var deal AutomaticDeal
	deal.compare(askingPrice{cents: 4001}, prices, 0)
	if deal.Qualifies || deal.TypicalCents == nil || *deal.TypicalCents != 5000.5 {
		t.Fatalf("comparison to half-grosz median = %+v", deal)
	}
}
