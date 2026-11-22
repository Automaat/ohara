package deals

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// swiatKsiazkiFixture is a products search answer captured on 2026-11-22:
// the products found for "ostatnie życzenie sapkowski", "Pink Floyd The
// Dark Side of the Moon" and "shogun dvd". The shop's search lists only
// products in stock, so the fixture marks the comic 7631418 out of stock
// by hand.
const swiatKsiazkiFixture = "swiatksiazki.json"

func swiatKsiazkiListings(t *testing.T) map[string]Listing {
	t.Helper()
	listings, err := parseSwiatKsiazki(fixture(t, swiatKsiazkiFixture))
	if err != nil {
		t.Fatalf("parseSwiatKsiazki: %v", err)
	}
	return byID(listings)
}

func TestParseSwiatKsiazki(t *testing.T) {
	got := swiatKsiazkiListings(t)
	ids := make([]string, 0, len(got))
	for id := range got {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	want := []string{"5387986", "6976474", "6989579", "7420294", "7420295", "7614225", "7634092"}
	if !slices.Equal(ids, want) {
		t.Fatalf("listings %v, want %v: no e-book, audio file or product out of stock", ids, want)
	}
	l := got["5387986"]
	if l.Source != "swiatksiazki" ||
		l.Title != "Andrzej Sapkowski - Wiedźmin. Tom 1. Ostatnie życzenie (Wydanie poprawione i uzupełnione)" ||
		l.URL != "https://www.swiatksiazki.pl/wiedzmin-tom-1-ostatnie-zyczenie-wydanie-poprawione-i-uzupelnione-5387986-ksiazka.html" ||
		!strings.HasPrefix(l.PhotoURL, "https://cdn.swiatksiazki.pl/media/catalog/product/") ||
		l.PriceCents != 3787 || l.AcquiredAs == nil || *l.AcquiredAs != "new" {
		t.Fatalf("listing = %+v", l)
	}
	if !strings.Contains(string(l.Raw), `"sku": "5387986"`) {
		t.Fatalf("raw = %s", l.Raw)
	}
	if title := got["7420294"].Title; title != "Pink Floyd - The Dark Side of the Moon (50th Anniversary Remaster) (Vinyl)" {
		t.Fatalf("album title = %q", title)
	}
	if title := got["7634092"].Title; title != "Shogun (DVD, 5 płyt)" {
		t.Fatalf("film title = %q", title)
	}
}

func TestDiscCountAfterMedium(t *testing.T) {
	for name, want := range map[string]string{
		"Shogun (5 DVD)":                     "Shogun (DVD, 5 płyt)",
		"Diuna (2 Blu-ray)":                  "Diuna (Blu-ray, 2 płyty)",
		"Kolekcja (12 DVD)":                  "Kolekcja (DVD, 12 płyt)",
		"Kolekcja (22 dvd)":                  "Kolekcja (dvd, 22 płyty)",
		"The Wall (2 CD) (Remaster)":         "The Wall (CD, 2 płyty) (Remaster)",
		"Obcy (1 DVD)":                       "Obcy (DVD)",
		"Diuna (3 4K Ultra HD + Blu-ray)":    "Diuna (4K Ultra HD + Blu-ray, 3 płyty)",
		"Budapest (Ultra HD Blu-ray Video )": "Budapest (Ultra HD Blu-ray Video )",
		"Wiedźmin (2 tomy)":                  "Wiedźmin (2 tomy)",
		"Ocean's 11 (DVD)":                   "Ocean's 11 (DVD)",
	} {
		if got := discCountAfterMedium(name); got != want {
			t.Errorf("discCountAfterMedium(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSwiatKsiazkiListingsMatchWants(t *testing.T) {
	got := swiatKsiazkiListings(t)
	book := wiedzmin()
	if !book.Matches(got["5387986"].Title) {
		t.Errorf("book want does not match %q", got["5387986"].Title)
	}
	if book.Matches("Wiedźmin. Tom 1. Ostatnie życzenie (Wydanie poprawione i uzupełnione)") {
		t.Errorf("book want matches the product name without its author")
	}
	album := Want{Kind: "music", Name: "The Dark Side of the Moon", Authors: []string{"Pink Floyd"}, Format: new("vinyl")}
	if !album.Matches(got["7420294"].Title) {
		t.Errorf("vinyl want does not match %q", got["7420294"].Title)
	}
	if album.Matches(got["7420295"].Title) {
		t.Errorf("vinyl want matches the Digipack CD %q", got["7420295"].Title)
	}
	shogun := got["7634092"].Title
	for format, match := range map[string]bool{"dvd": true, "bluray": false} {
		film := Want{Kind: "movie", Name: "Shogun", Format: new(format)}
		if film.Matches(shogun) != match {
			t.Errorf("%s want matches %q = %v, want %v", format, shogun, !match, match)
		}
	}
}

func TestParseSwiatKsiazkiRejectsOtherShapes(t *testing.T) {
	for name, body := range map[string]string{
		"html":           `<html><body>Just a moment...</body></html>`,
		"graphql errors": `{"errors":[{"message":"Field \"x\" is not defined"}],"data":{"products":{"items":[]}}}`,
		"no data":        `{}`,
		"no products":    `{"data":{"products":null}}`,
		"no items":       `{"data":{"products":{}}}`,
	} {
		_, err := parseSwiatKsiazki([]byte(body))
		if _, ok := errors.AsType[parseError](err); !ok {
			t.Errorf("%s: err = %v, want a parse error", name, err)
		}
		if got := sourceStatus(err); got != "parse_error" {
			t.Errorf("%s: status = %q, want parse_error", name, got)
		}
	}
	listings, err := parseSwiatKsiazki([]byte(`{"data":{"products":{"items":[]}}}`))
	if err != nil || len(listings) != 0 {
		t.Fatalf("empty items = %d listings, %v", len(listings), err)
	}
}

// swiatKsiazkiDouble serves the fixture at /graphql and 403 anywhere else,
// keeping every request.
type swiatKsiazkiDouble struct {
	mu       sync.Mutex
	requests []*http.Request
	srv      *httptest.Server
}

func newSwiatKsiazkiDouble(t *testing.T) *swiatKsiazkiDouble {
	t.Helper()
	d := &swiatKsiazkiDouble{}
	d.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		d.requests = append(d.requests, r)
		d.mu.Unlock()
		if r.URL.Path != "/graphql" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fixture(t, swiatKsiazkiFixture))
	}))
	t.Cleanup(d.srv.Close)
	return d
}

func (d *swiatKsiazkiDouble) asked() []*http.Request {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.requests)
}

func TestSwiatKsiazkiSearchOverHTTP(t *testing.T) {
	d := newSwiatKsiazkiDouble(t)
	ctx := context.Background()
	src := SwiatKsiazki{Client: d.srv.Client(), BaseURL: d.srv.URL + "/graphql"}

	listings, err := src.Search(ctx, `Pink Floyd "The Wall" łowca`)
	if err != nil || len(listings) != 7 {
		t.Fatalf("swiatksiazki = %d listings, %v", len(listings), err)
	}
	if listings[0].Query != `Pink Floyd "The Wall" łowca` {
		t.Fatalf("listing query = %q", listings[0].Query)
	}
	reqs := d.asked()
	if len(reqs) != 1 {
		t.Fatalf("sent %d requests, want 1", len(reqs))
	}
	r := reqs[0]
	query := r.URL.Query()
	if r.Method != http.MethodGet || r.URL.Path != "/graphql" || len(query) != 1 ||
		!strings.HasPrefix(r.UserAgent(), "Mozilla/5.0") {
		t.Fatalf("request %s %s, user agent %q", r.Method, r.URL, r.UserAgent())
	}
	if doc := query.Get("query"); !strings.HasPrefix(doc, `{products(search:"Pink Floyd \"The Wall\" łowca",pageSize:50){items{sku `) {
		t.Fatalf("graphql query = %q", doc)
	}

	_, err = SwiatKsiazki{Client: d.srv.Client(), BaseURL: d.srv.URL + "/blocked"}.Search(ctx, "x")
	if got := sourceStatus(err); got != "http_403" {
		t.Fatalf("blocked status = %q (err %v), want http_403", got, err)
	}
}

// TestRunAlertsOnSwiatKsiazkiListing is the issue's acceptance check: a
// Świat Książki listing of a wished album under its limit is stored and
// sends one alert naming the shop, and the default canary keeps the
// source's canary up.
func TestRunAlertsOnSwiatKsiazkiListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, &Want{
		Kind: "music", Name: "The Dark Side of the Moon", Authors: []string{"Pink Floyd"},
		Format: new("vinyl"), MaxPriceCents: new(int64(14000)),
	})
	d := newSwiatKsiazkiDouble(t)
	notifier := &fakeNotifier{}
	h := NewHunter(NewStore(pool), []Source{SwiatKsiazki{Client: d.srv.Client(), BaseURL: d.srv.URL + "/graphql"}},
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
		"🎯 Okazja: The Dark Side of the Moon", "133,00 zł (limit 140,00 zł) · Świat Książki · nowy",
		"Pink Floyd - The Dark Side of the Moon (50th Anniversary Remaster) (Vinyl)",
		"https://www.swiatksiazki.pl/the-dark-side-of-the-moon-50th-anniversary-remaster-vinyl-7420294-muzyka.html",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation
		WHERE source = 'swiatksiazki' AND external_id = '7420294' AND acquired_as = 'new'`); n != 1 {
		t.Fatalf("stored %d observations of the vinyl listing", n)
	}
	if v := mustMetric(t, `ohara_source_canary_ok{source="swiatksiazki"}`); v != 1 {
		t.Fatalf("swiatksiazki canary = %v", v)
	}
	if res := mustRun(t, h); res.New != 0 || res.Sent != 0 {
		t.Fatalf("repeat run = %+v, want nothing new", res)
	}
}
