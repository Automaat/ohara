package deals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

func TestParseTezeusz(t *testing.T) {
	listings, err := parseTezeusz(fixture(t, "tezeusz.html"), TezeuszURL)
	if err != nil {
		t.Fatalf("parseTezeusz: %v", err)
	}
	got := byID(listings)
	if len(listings) != 5 || len(got) != 5 {
		t.Fatalf("got %d listings (%d distinct), want the 5 cards", len(listings), len(got))
	}
	l := got["8387757"]
	if l.Source != "tezeusz" || l.Title != "Ostatnie życzenie - Andrzej Sapkowski" ||
		l.URL != "https://tezeusz.pl/ostatnie-zyczenie-sapkowski-andrzej-8387757" ||
		l.PhotoURL != "https://storage.tezeusz.pl/product_show/ostatnie-yczenie-andrzej-sapkowski.jpeg" ||
		l.PriceCents != 2712 || l.AcquiredAs != nil {
		t.Fatalf("listing = %+v", l)
	}
	if !strings.Contains(string(l.Raw), `"price":"od 27,12 zł do 89,65 zł"`) {
		t.Fatalf("raw = %s", l.Raw)
	}
	for _, tc := range []struct {
		id    string
		cents int64
		card  string
	}{
		{"15154262", 8874, "one copy"},
		{"8776556", 875, "one copy with its struck-out 30-day lowest of 14,84 zł"},
		{"1403620", 1307, "a price range"},
		{"8335056", 4400, "a price range"},
	} {
		if got[tc.id].PriceCents != tc.cents {
			t.Errorf("card %s (%s) price = %d, want %d", tc.id, tc.card, got[tc.id].PriceCents, tc.cents)
		}
	}
}

func TestTezeuszTitlesMatchTheirBook(t *testing.T) {
	listings, err := parseTezeusz(fixture(t, "tezeusz.html"), TezeuszURL)
	if err != nil {
		t.Fatalf("parseTezeusz: %v", err)
	}
	got := byID(listings)
	wish := wiedzmin()
	other := Want{CopyID: 4, Kind: "book", Name: "Krew elfów", Authors: []string{"Andrzej Sapkowski"}}
	for _, tc := range []struct {
		want  *Want
		id    string
		match bool
	}{
		{&wish, "8387757", true},
		{&wish, "15154262", true},
		{&wish, "1403620", false},
		{&other, "8387757", false},
		{&other, "8335056", false},
	} {
		if m := tc.want.Matches(got[tc.id].Title); m != tc.match {
			t.Errorf("%q matches %q = %v, want %v", tc.want.Name, got[tc.id].Title, m, tc.match)
		}
	}
}

func TestParseTezeuszOtherPages(t *testing.T) {
	const noResults = `<div class="col col-xs-12 col-md-5-4 col-lg-5-4 col-max-8-7" id="search-products-results-list">
		<h6 class="no-results">Niestety nie znaleziono żadnego produktu, spróbuj zmienić parametry wyszukiwania.</h6>
	</div>`
	listings, err := parseTezeusz([]byte(noResults), TezeuszURL)
	if err != nil || len(listings) != 0 {
		t.Fatalf("no results = %d listings, %v; want none and no error", len(listings), err)
	}
	const unpriced = `<div id='search-products-results-list'>
		<a class='tezeusz-product-card js-ga-select-item' data-ga-product-id="1" href='bez-ceny-1'>
			<h5 class='tezeusz-product-card-title'>Bez ceny</h5>
		</a>
		<a class='tezeusz-product-card js-ga-select-item' href='bez-id'>
			<h5 class='tezeusz-product-card-title'>Bez id</h5>
			<div class='tezeusz-product-card-price'>10,00 zł</div>
		</a>
	</div>`
	if listings, err := parseTezeusz([]byte(unpriced), TezeuszURL); err != nil || len(listings) != 0 {
		t.Fatalf("cards without a price or an id = %d listings, %v; want none", len(listings), err)
	}
	for name, body := range map[string]string{
		"cloudflare challenge": `<!DOCTYPE html><html><head><title>Just a moment...</title></head>` +
			`<body><div id="challenge-running">Checking your browser</div></body></html>`,
		"product page": `<html><body><h1>Ostatnie życzenie</h1></body></html>`,
		"empty":        ``,
	} {
		_, err := parseTezeusz([]byte(body), TezeuszURL)
		if _, ok := errors.AsType[parseError](err); !ok {
			t.Errorf("%s: err = %v, want a parse error", name, err)
		}
		if got := sourceStatus(err); got != metrics.SourceParseError {
			t.Errorf("%s: status = %q, want %q", name, got, metrics.SourceParseError)
		}
	}
}

func TestTezeuszSearchOverHTTP(t *testing.T) {
	var gotPath, gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA = r.URL.Path, r.URL.RawQuery, r.UserAgent()
		if strings.HasPrefix(r.URL.Path, "/blocked") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fixture(t, "tezeusz.html"))
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()

	listings, err := Tezeusz{Client: srv.Client(), BaseURL: srv.URL}.Search(ctx, "ostatnie życzenie")
	if err != nil || len(listings) != 5 {
		t.Fatalf("tezeusz = %d listings, %v", len(listings), err)
	}
	if gotPath != "/szukaj" || gotQuery != "szukaj=ostatnie+%C5%BCyczenie&tylko_dostepne=1" ||
		!strings.HasPrefix(gotUA, "Mozilla/5.0") {
		t.Fatalf("request %s?%s, user agent %q", gotPath, gotQuery, gotUA)
	}
	l := byID(listings)["8387757"]
	if l.URL != srv.URL+"/ostatnie-zyczenie-sapkowski-andrzej-8387757" || l.Query != "ostatnie życzenie" {
		t.Fatalf("listing url %q, query %q", l.URL, l.Query)
	}
	_, err = Tezeusz{Client: srv.Client(), BaseURL: srv.URL + "/blocked/"}.Search(ctx, "x")
	if got := sourceStatus(err); got != "http_403" || gotPath != "/blocked/szukaj" {
		t.Fatalf("blocked tezeusz at %s: status %q, err %v; want http_403", gotPath, got, err)
	}
}

// TestRunAlertsOnTezeuszListing is the issue's acceptance check: a Tezeusz
// card of a wished book under its limit is stored, alerts once naming the
// source and keeps the Tezeusz canary up.
func TestRunAlertsOnTezeuszListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	book := wiedzmin()
	book.MaxPriceCents = new(int64(3000))
	wishlist(t, pool, &book)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(fixture(t, "tezeusz.html"))
	}))
	t.Cleanup(srv.Close)
	notifier := &fakeNotifier{}
	h := NewHunter(NewStore(pool), []Source{Tezeusz{Client: srv.Client(), BaseURL: srv.URL}},
		notifier, Config{CanaryQuery: "dvd"}, slog.New(slog.DiscardHandler))
	h.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }

	res := mustRun(t, h)
	if len(res.Errors) != 0 || res.Alerts != 1 || res.Sent != 1 {
		t.Fatalf("run = %+v", res)
	}
	msgs := notifier.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	for _, part := range []string{
		"🎯 Okazja: Ostatnie życzenie", "27,12 zł (limit 30,00 zł) · Tezeusz",
		"Ostatnie życzenie - Andrzej Sapkowski", srv.URL + "/ostatnie-zyczenie-sapkowski-andrzej-8387757",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation
		WHERE source = 'tezeusz' AND external_id = '8387757' AND acquired_as IS NULL`); n != 1 {
		t.Fatalf("stored %d observations of the card", n)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="tezeusz"}`); v != 1 {
		t.Fatalf("tezeusz canary = %v", v)
	}
	if res := mustRun(t, h); res.New != 0 || res.Sent != 0 {
		t.Fatalf("repeat run = %+v, want nothing new", res)
	}
}
