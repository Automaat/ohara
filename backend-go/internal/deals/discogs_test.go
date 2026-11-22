package deals

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

// fakeRates has a złoty rate for EUR and PLN only, as NBP has none for a
// currency it does not list.
type fakeRates struct {
	mu    sync.Mutex
	asked []string
}

func (f *fakeRates) PLN(_ context.Context, currency string) (float64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, currency)
	switch currency {
	case "PLN":
		return 1, nil
	case "EUR":
		return 4.25, nil
	}
	return 0, errors.New("nbp: " + currency + " rate: status 404")
}

func parseDiscogsFixture(t *testing.T) []Listing {
	t.Helper()
	listings, _, err := parseDiscogsInventory(context.Background(), fixture(t, "discogs_inventory.json"), &fakeRates{})
	if err != nil {
		t.Fatalf("parseDiscogsInventory: %v", err)
	}
	return listings
}

func TestParseDiscogsInventory(t *testing.T) {
	rates := &fakeRates{}
	listings, seen, err := parseDiscogsInventory(context.Background(), fixture(t, "discogs_inventory.json"), rates)
	if err != nil {
		t.Fatalf("parseDiscogsInventory: %v", err)
	}
	wantSeen := []string{"3401234501", "3401234502", "3401234503"}
	if !slices.Equal(seen, wantSeen) {
		t.Fatalf("seen = %v, want the listings for sale at a price %v", seen, wantSeen)
	}
	if !slices.Equal(rates.asked, []string{"EUR", "PLN", "JPY"}) {
		t.Fatalf("rates asked for %v, want each currency once", rates.asked)
	}
	got := byID(listings)
	if len(got) != 3 {
		t.Fatalf("got %d listings, want the 3 for sale in a currency with a rate", len(got))
	}
	if _, ok := got["3401234504"]; ok {
		t.Fatal("a listing priced in JPY, which has no rate, was kept")
	}
	lp := got["3401234501"]
	if lp.Source != DiscogsSource || lp.Title != "Pink Floyd - A Momentary Lapse Of Reason (LP, Album, Gat)" ||
		lp.URL != "https://www.discogs.com/sell/item/3401234501" ||
		lp.PhotoURL != "https://i.discogs.example.test/R-1873013.jpeg" || lp.Medium != "vinyl" {
		t.Fatalf("LP listing = %+v", lp)
	}
	if lp.PriceCents != 63325 {
		t.Fatalf("149.00 EUR = %d grosze, want 63325 at 4.25 zł", lp.PriceCents)
	}
	if lp.AcquiredAs == nil || *lp.AcquiredAs != "used" {
		t.Fatalf("VG+ listing acquired as %v, want used", lp.AcquiredAs)
	}
	cd := got["3401234502"]
	if cd.AcquiredAs != nil || cd.PriceCents != 4500 || cd.PhotoURL != "" || cd.Medium != "cd" {
		t.Fatalf("Mint CD listing = %+v, want no condition, 4500 grosze, no photo", cd)
	}
	if tape := got["3401234503"]; tape.PriceCents != 2550 || tape.Medium != "cassette" ||
		tape.AcquiredAs == nil || *tape.AcquiredAs != "used" {
		t.Fatalf("cassette listing = %+v", tape)
	}
}

func TestParseDiscogsInventoryRejectsOtherShapes(t *testing.T) {
	for _, body := range []string{
		`{}`, `<html><body>Just a moment...</body></html>`, `{"listings": 3}`, `{"listings": [{}]}`,
		`{"listings": [{"id": 7, "price": {"value": 10, "currency": "PLN"}, "release": {"description": "A"}}]}`,
		`{"listings": [{"status": "For Sale", "price": {"value": 10, "currency": "PLN"}, "release": {"description": "A"}}]}`,
	} {
		_, _, err := parseDiscogsInventory(context.Background(), []byte(body), &fakeRates{})
		if err == nil {
			t.Errorf("parseDiscogsInventory(%s) accepted it", body)
			continue
		}
		if got := sourceStatus(err); got != metrics.SourceParseError {
			t.Errorf("parseDiscogsInventory(%s) status = %q, want %q", body, got, metrics.SourceParseError)
		}
	}
	listings, seen, err := parseDiscogsInventory(context.Background(), []byte(`{"listings": []}`), &fakeRates{})
	if err != nil || len(listings) != 0 || len(seen) != 0 {
		t.Fatalf("empty inventory = %v, %v, %v; want no listings and no error", listings, seen, err)
	}
}

func TestDiscogsMedium(t *testing.T) {
	for format, want := range map[string]string{
		"LP, Album, Gat":        "vinyl",
		"2xLP, Album, RE":       "vinyl",
		`12", 33 ⅓ RPM, Single`: "vinyl",
		"CD, Album, RE":         "cd",
		"2xCD, Comp":            "cd",
		"Cass, Album":           "cassette",
		"Box Set, Comp + 4xCD":  "cd",
		"DVD, Album, NTSC":      "other",
		"File, MP3, Album, 320": "other",
		"":                      "",
	} {
		if got := discogsMedium(format); got != want {
			t.Errorf("discogsMedium(%q) = %q, want %q", format, got, want)
		}
	}
}

func TestDiscogsListingsMatchTheirMedium(t *testing.T) {
	listings := parseDiscogsFixture(t)
	for _, tc := range []struct {
		format string
		want   []string
	}{
		{"cd", []string{"3401234502"}},
		{"vinyl", []string{"3401234501"}},
		{"cassette", []string{"3401234503"}},
		{"", []string{"3401234501", "3401234502", "3401234503"}},
	} {
		w := Want{CopyID: 1, TitleID: 1, Kind: "music", Name: "A Momentary Lapse Of Reason", Authors: []string{"Pink Floyd"}}
		if tc.format != "" {
			w.Format = new(tc.format)
		}
		wants := []Want{w}
		wants[0].Prepare()
		var got []string
		for i := range listings {
			if pick(wants, &listings[i], Rules{}, Titles{}) != nil {
				got = append(got, listings[i].ExternalID)
			}
		}
		if !slices.Equal(got, tc.want) {
			t.Errorf("%q want matched %v, want %v", tc.format, got, tc.want)
		}
	}
	movie := []Want{{CopyID: 2, TitleID: 2, Kind: "movie", Name: "A Momentary Lapse Of Reason", Format: new("dvd")}}
	movie[0].Prepare()
	dvd := Listing{Title: "Pink Floyd - A Momentary Lapse Of Reason (DVD, Album)", Medium: "other"}
	if pick(movie, &dvd, Rules{}, Titles{}) != nil {
		t.Fatal("a Discogs listing matched a movie want")
	}
}

// discogsDouble serves seller inventories: answers maps a seller to its
// status and body, and every request is recorded.
type discogsDouble struct {
	srv     *httptest.Server
	mu      sync.Mutex
	answers map[string]discogsAnswer
	asked   []*http.Request
}

type discogsAnswer struct {
	status int
	body   string
}

func newDiscogsDouble(t *testing.T) *discogsDouble {
	t.Helper()
	d := &discogsDouble{answers: map[string]discogsAnswer{}}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.asked = append(d.asked, r)
		seller, ok := strings.CutSuffix(strings.TrimPrefix(r.URL.Path, "/users/"), "/inventory")
		a, known := d.answers[seller]
		d.mu.Unlock()
		if !ok || !known {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(a.status)
		_, _ = w.Write([]byte(a.body))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *discogsDouble) answer(seller string, status int, body string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.answers[seller] = discogsAnswer{status: status, body: body}
}

func (d *discogsDouble) requests() []*http.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.asked)
}

func (d *discogsDouble) seller(name string) DiscogsSeller {
	return DiscogsSeller{
		Client: &discogs.Client{HTTP: d.srv.Client(), BaseURL: d.srv.URL, Token: "secret"},
		Rates:  &fakeRates{},
		Seller: name,
	}
}

func TestDiscogsSellerAsksItsNewestInventory(t *testing.T) {
	d := newDiscogsDouble(t)
	d.answer("heyjoerecords", http.StatusOK, string(fixture(t, "discogs_inventory.json")))
	page, err := d.seller("heyjoerecords").SearchPage(context.Background(), "")
	if err != nil || len(page.Listings) != 3 || len(page.Seen) != 3 || page.Complete {
		t.Fatalf("page = %d listings, %d seen, complete %v, %v; want the 3 of 4 listings for sale at a price",
			len(page.Listings), len(page.Seen), page.Complete, err)
	}
	for i := range page.Listings {
		if q := page.Listings[i].Query; q != "heyjoerecords" {
			t.Fatalf("listing %s query %q, want the seller", page.Listings[i].ExternalID, q)
		}
	}
	reqs := d.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}
	r := reqs[0]
	if r.URL.RawQuery != "per_page=100&sort=listed&sort_order=desc" ||
		r.Header.Get("Authorization") != "Discogs token=secret" || r.UserAgent() != discogs.UserAgent {
		t.Fatalf("request query %q, auth %q, user agent %q", r.URL.RawQuery, r.Header.Get("Authorization"), r.UserAgent())
	}

	d.answer("busy", http.StatusTooManyRequests, `{"message": "You are making requests too quickly."}`)
	_, err = d.seller("busy").SearchPage(context.Background(), "")
	if got := sourceStatus(err); got != "http_429" {
		t.Fatalf("429 answer status = %q (%v), want http_429", got, err)
	}
	if !(&Refused{}).Note(err) {
		t.Fatal("a 429 answer is not a refusal")
	}
}

// discogsSeries is one source health series of DiscogsSource, labels
// holding any labels after source.
func discogsSeries(name, labels string) string {
	return name + `{source="` + DiscogsSource + `"` + labels + `}`
}

func discogsWish(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	return wishlist(t, pool, &Want{
		Kind: "music", Name: "A Momentary Lapse Of Reason", Authors: []string{"Pink Floyd"},
		Format: new("vinyl"), MaxPriceCents: new(int64(70000)),
	})
}

func TestRunAlertsOnDiscogsListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	discogsWish(t, pool)
	d := newDiscogsDouble(t)
	d.answer("busy", http.StatusTooManyRequests, `{"message": "You are making requests too quickly."}`)
	d.answer("broken", http.StatusOK, `{}`)
	d.answer("heyjoerecords", http.StatusOK, string(fixture(t, "discogs_inventory.json")))
	notifier := &fakeNotifier{}
	h := newTestHunter(pool, notifier, d.seller("busy"), d.seller("broken"), d.seller("heyjoerecords"))
	h.cfg.CanaryQuery = "dvd"
	pauses := 0
	h.sleep = func(ctx context.Context, _ time.Duration) error {
		pauses++
		return ctx.Err()
	}
	before := map[string]float64{}
	for _, status := range []string{"ok", "http_429", "parse_error"} {
		before[status], _ = metric(t, discogsSeries("ohara_source_requests_total", `,status="`+status+`"`))
	}

	res := mustRun(t, h)
	if len(d.requests()) != 3 || pauses != 2 {
		t.Fatalf("%d requests with %d pauses, want each seller asked once, paced", len(d.requests()), pauses)
	}
	if res.Matched != 1 || res.New != 1 || res.Alerts != 1 || res.Sent != 1 {
		t.Fatalf("run = %+v, want the LP alerted", res)
	}
	if !slices.Contains(res.Errors, DiscogsSource+" busy: refused with status 429, skipped 0 queries") {
		t.Fatalf("errors = %q, want the refusal of busy", res.Errors)
	}
	for status, want := range map[string]float64{"ok": 1, "http_429": 1, "parse_error": 1} {
		got := mustMetric(t, discogsSeries("ohara_source_requests_total", `,status="`+status+`"`)) - before[status]
		if got != want {
			t.Errorf("%s requests grew by %v, want %v", status, got, want)
		}
	}
	if got := mustMetric(t, discogsSeries("ohara_source_canary_ok", "")); got != 1 {
		t.Errorf("canary = %v, want 1 after a seller answered with listings", got)
	}
	if got := mustMetric(t, discogsSeries("ohara_source_results", "")); got != 3 {
		t.Errorf("results = %v, want the 3 listings of the sellers that answered", got)
	}
	msgs := notifier.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	for _, part := range []string{
		"🎯 Okazja: A Momentary Lapse Of Reason · Winyl", "633,25 zł (limit 700,00 zł) · Discogs · używany",
		"Pink Floyd - A Momentary Lapse Of Reason (LP, Album, Gat)",
		"https://www.discogs.com/sell/item/3401234501\nData provided by Discogs.",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	recent, err := h.store.Recent(ctx, RecentFilter{Limit: 10})
	if err != nil || len(recent) != 1 || recent[0].Source != DiscogsSource || recent[0].Alert != AlertSent {
		t.Fatalf("deals view = %+v, %v; want the sent Discogs listing", recent, err)
	}

	d.answer("heyjoerecords", http.StatusOK, strings.Replace(string(fixture(t, "discogs_inventory.json")),
		`"id": 3401234501`, `"id": 3401234599`, 1))
	if res := mustRun(t, h); res.New != 1 {
		t.Fatalf("run without the listing = %+v, want its replacement stored", res)
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE external_id = '3401234501' AND gone_at IS NOT NULL`); n != 1 {
		t.Fatal("a listing the seller's answer no longer holds is not gone")
	}
	recent, err = h.store.Recent(ctx, RecentFilter{Limit: 10})
	if err != nil || len(recent) != 1 || recent[0].ExternalID != "3401234599" {
		t.Fatalf("deals view = %+v, %v; want only the listing still offered", recent, err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO price_observation (source, external_id, price_cents, url, raw, last_offered_at)
		VALUES ($1, '77', 1000, 'https://www.discogs.com/sell/item/77', '{"query": "busy"}', now())`, DiscogsSource); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h)
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE external_id = '77' AND gone_at IS NULL`); n != 1 {
		t.Fatal("the listing of a seller that refused was marked gone")
	}

	if _, err := pool.Exec(ctx, `UPDATE price_observation SET last_seen_at = now() - interval '31 days',
		last_offered_at = now() - interval '31 days' WHERE external_id = '3401234501'`); err != nil {
		t.Fatal(err)
	}
	mustRun(t, h)
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE external_id = '3401234501'`); n != 0 {
		t.Fatal("a Discogs listing unseen for 31 days was kept")
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE source = '`+DiscogsSource+`'`); n != 2 {
		t.Fatalf("%d Discogs listings left, want the 2 offered this month", n)
	}
}

func TestStaleDiscogsListingLeavesTheDealsView(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	copyID := discogsWish(t, pool)
	store := NewStore(pool)
	lp := Listing{
		Source: DiscogsSource, ExternalID: "1", Title: "Pink Floyd - A Momentary Lapse Of Reason (LP, Album)",
		URL: "https://www.discogs.com/sell/item/1", PriceCents: 5000, Medium: "vinyl",
	}
	olx := Listing{
		Source: "olx", ExternalID: "2", Title: "Pink Floyd A Momentary Lapse Of Reason winyl",
		URL: "https://www.olx.pl/d/oferta/x-ID2.html", PriceCents: 5000,
	}
	wants, err := store.Wants(ctx)
	if err != nil || len(wants) != 1 || wants[0].CopyID != copyID {
		t.Fatalf("wants = %+v, %v", wants, err)
	}
	for _, l := range []*Listing{&lp, &olx} {
		if _, alerted, err := store.Record(ctx, l, &wants[0], true); err != nil || !alerted {
			t.Fatalf("record %s = %v, %v", l.Source, alerted, err)
		}
	}
	shown := func() []string {
		t.Helper()
		recent, err := store.Recent(ctx, RecentFilter{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		alerts, err := store.PendingAlerts(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for i := range recent {
			out = append(out, "view:"+recent[i].Source)
		}
		for i := range alerts {
			out = append(out, "alert:"+alerts[i].Source)
		}
		slices.Sort(out)
		return out
	}
	if got := shown(); !slices.Equal(got, []string{"alert:" + DiscogsSource, "alert:olx", "view:" + DiscogsSource, "view:olx"}) {
		t.Fatalf("fresh listings = %v", got)
	}
	if _, err := pool.Exec(ctx, `UPDATE price_observation SET last_offered_at = now() - interval '7 hours',
		last_seen_at = now() - interval '7 hours'`); err != nil {
		t.Fatal(err)
	}
	if got := shown(); !slices.Equal(got, []string{"alert:olx", "view:olx"}) {
		t.Fatalf("listings offered 7 hours ago = %v, want the Discogs one out of the view and its alert held", got)
	}
}

func TestDiscogsListingWithoutFormatMatchesNoFilm(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, &Want{Kind: "movie", Name: "Blade Runner", Format: new("dvd"), MaxPriceCents: new(int64(10000))})
	d := newDiscogsDouble(t)
	d.answer("seller", http.StatusOK, `{"listings": [{"id": 123, "status": "For Sale",
		"price": {"value": 50, "currency": "PLN"}, "release": {"description": "Blade Runner (DVD)"}}]}`)
	notifier := &fakeNotifier{}
	if res := mustRun(t, newTestHunter(pool, notifier, d.seller("seller"))); res.Matched != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("a Discogs listing without a format = %+v, %v; want no film match", res, notifier.messages())
	}
}

func TestDiscogsRowWithoutIdentityKeepsStoredListings(t *testing.T) {
	pool := dbtest.Migrated(t)
	discogsWish(t, pool)
	d := newDiscogsDouble(t)
	d.answer("seller", http.StatusOK, string(fixture(t, "discogs_inventory.json")))
	h := newTestHunter(pool, nil, d.seller("seller"))
	if res := mustRun(t, h); res.New != 1 {
		t.Fatalf("first run = %+v, want the LP stored", res)
	}
	before := mustMetric(t, discogsSeries("ohara_source_requests_total", `,status="`+metrics.SourceParseError+`"`))
	d.answer("seller", http.StatusOK, `{"listings": [{}]}`)
	mustRun(t, h)
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE gone_at IS NOT NULL`); n != 0 {
		t.Fatalf("an answer with an unreadable row marked %d stored listings gone", n)
	}
	if got := mustMetric(t, discogsSeries("ohara_source_requests_total", `,status="`+metrics.SourceParseError+`"`)) - before; got != 1 {
		t.Fatalf("parse errors counted = %v, want 1", got)
	}
}

func TestDiscogsListingWithoutRateIsNotAlertedAtItsOldPrice(t *testing.T) {
	pool := dbtest.Migrated(t)
	discogsWish(t, pool)
	d := newDiscogsDouble(t)
	body := string(fixture(t, "discogs_inventory.json"))
	d.answer("seller", http.StatusOK, body)
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, d.seller("seller"))
	if res := mustRun(t, h); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("first run = %+v, want one alert left to retry", res)
	}
	d.answer("seller", http.StatusOK, strings.Replace(body, `"currency": "EUR"`, `"currency": "ZZZ"`, 1))
	if res := mustRun(t, h); res.Sent != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("listing now priced in a currency without a rate = %+v, %v; want no alert at its old price",
			res, notifier.messages())
	}
	recent, err := NewStore(pool).Recent(context.Background(), RecentFilter{Limit: 10})
	if err != nil || len(recent) != 0 {
		t.Fatalf("deals view = %+v, %v; want the listing out of it", recent, err)
	}
}

func TestDiscogsListingLeavesAWishEditedToAnotherMedium(t *testing.T) {
	pool := dbtest.Migrated(t)
	ctx := context.Background()
	copyID := discogsWish(t, pool)
	d := newDiscogsDouble(t)
	d.answer("seller", http.StatusOK, `{"listings": [{"id": 123, "status": "For Sale",
		"price": {"value": 50, "currency": "PLN"},
		"release": {"description": "Pink Floyd - A Momentary Lapse Of Reason (LP, Album)", "format": "LP, Album"}}]}`)
	notifier := &fakeNotifier{fail: 1}
	h := newTestHunter(pool, notifier, d.seller("seller"))
	if res := mustRun(t, h); res.Alerts != 1 || res.Sent != 0 {
		t.Fatalf("first run = %+v, want one alert left to retry", res)
	}
	if _, err := pool.Exec(ctx, `UPDATE edition SET format = 'cd'
		WHERE id = (SELECT edition_id FROM copy WHERE id = $1)`, copyID); err != nil {
		t.Fatal(err)
	}
	if res := mustRun(t, h); res.Sent != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("LP listing for a wish edited to CD = %+v, %v; want no alert", res, notifier.messages())
	}
	recent, err := NewStore(pool).Recent(ctx, RecentFilter{Limit: 10, Matched: true})
	if err != nil || len(recent) != 0 {
		t.Fatalf("matched deals = %+v, %v; want the LP no longer matched to the CD wish", recent, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE edition SET format = 'vinyl'
		WHERE id = (SELECT edition_id FROM copy WHERE id = $1)`, copyID); err != nil {
		t.Fatal(err)
	}
	if recent, err := NewStore(pool).Recent(ctx, RecentFilter{Limit: 10, Matched: true}); err != nil || len(recent) != 1 {
		t.Fatalf("matched deals after the wish is vinyl again = %+v, %v; want the LP", recent, err)
	}
}

func TestDiscogsConditionFollowsTheAnswer(t *testing.T) {
	pool := dbtest.Migrated(t)
	discogsWish(t, pool)
	d := newDiscogsDouble(t)
	body := string(fixture(t, "discogs_inventory.json"))
	d.answer("seller", http.StatusOK, body)
	h := newTestHunter(pool, nil, d.seller("seller"))
	mustRun(t, h)
	condition := func() *string {
		t.Helper()
		var acquired *string
		if err := pool.QueryRow(context.Background(), `SELECT acquired_as FROM price_observation
			WHERE external_id = '3401234501'`).Scan(&acquired); err != nil {
			t.Fatal(err)
		}
		return acquired
	}
	if got := condition(); got == nil || *got != "used" {
		t.Fatalf("VG+ listing stored as %v, want used", got)
	}
	d.answer("seller", http.StatusOK, strings.Replace(body, "Very Good Plus (VG+)", "Mint (M)", 1))
	mustRun(t, h)
	if got := condition(); got != nil {
		t.Fatalf("listing regraded Mint stored as %s, want no condition", *got)
	}
}
