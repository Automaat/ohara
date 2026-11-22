package deals

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

const winylowniaSearch = "https://winylownia.pl/search.php"

func TestParseWinylownia(t *testing.T) {
	listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, fixture(t, "winylownia.html"))
	if err != nil {
		t.Fatalf("parseIdoSellSearch: %v", err)
	}
	if len(listings) != 1 {
		t.Fatalf("got %d listings (%+v), want the one available tile, not the 3 disabled ones", len(listings), listings)
	}
	l := listings[0]
	if l.Source != "winylownia" || l.ExternalID != "246548" ||
		l.Title != "PINK FLOYD The Dark Side of the Moon (50th Anniversary Remaster) LP" ||
		l.URL != "https://winylownia.pl/pl/products/pink-floyd-the-dark-side-of-the-moon-50th-anniversary-remaster-lp-246548.html" ||
		l.AcquiredAs == nil || *l.AcquiredAs != "new" || len(l.Raw) == 0 {
		t.Fatalf("listing = %+v", l)
	}
	if l.PriceCents != 12797 {
		t.Fatalf("price = %d, want the tile's 127,97 zł, not the gtag price before discount", l.PriceCents)
	}
	if l.PhotoURL != "" {
		t.Fatalf("photo = %q, want none: the shop's terms forbid copying its photos", l.PhotoURL)
	}
}

func TestParseWinylowniaTitlesMatchByFormat(t *testing.T) {
	listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, fixture(t, "winylownia.html"))
	if err != nil || len(listings) != 1 {
		t.Fatalf("parseIdoSellSearch = %d listings, %v", len(listings), err)
	}
	for _, title := range []string{listings[0].Title, "PINK FLOYD Dark Side Of The Moon LP 50TH ANNIVERSARY"} {
		for _, tc := range []struct {
			format string
			match  bool
		}{{"vinyl", true}, {"cd", false}} {
			w := Want{Kind: "music", Name: "The Dark Side of the Moon", Authors: []string{"Pink Floyd"}, Format: new(tc.format)}
			w.Prepare()
			if got := w.Matches(title); got != tc.match {
				t.Errorf("%s want Matches(%q) = %v, want %v", tc.format, title, got, tc.match)
			}
		}
	}
}

func TestParseIdoSellSearchAnswers(t *testing.T) {
	listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, fixture(t, "winylownia-noproduct.html"))
	if err != nil || len(listings) != 0 {
		t.Fatalf("no-results page = %v, %v; want no listings and no error", listings, err)
	}
	for _, body := range []string{
		``,
		`{}`,
		`<html><body>Błąd</body></html>`,
		`<html><body><div id="container" class="main_page container"></div></body></html>`,
		`<html><body><div id="container" class="search_page container"><section id="search" class="search products d-flex"></section></div></body></html>`,
	} {
		_, err := parseIdoSellSearch("winylownia", winylowniaSearch, []byte(body))
		if got := sourceStatus(err); got != metrics.SourceParseError {
			t.Errorf("parseIdoSellSearch(%q) status %q (err %v), want %s", body, got, err, metrics.SourceParseError)
		}
	}
}

func TestParseIdoSellSearchTiles(t *testing.T) {
	tile := func(id, extra, inner string) string {
		return `<div class="product col-6" data-product_id="` + id + `"` + extra + `>` + inner + `</div>`
	}
	body := `<html><body><div id="container" class="search_page container"><section id="search" class="search products d-flex">` +
		tile("1", ``, `<h2><a class="product__name" tabindex="0" href="/pl/products/a-1.html?query_id=7&amp;color=red" title="NIRVANA Nevermind LP &amp; poster">x</a></h2><strong class="price --main">1&nbsp;377,39 zł<span class="price_vat"> brutto</span></strong>`) +
		tile("2", ` data-product_disable="true"`, `<a class="product__name" href="/pl/products/b-2.html" title="NIRVANA Bleach LP">x</a><strong class="price --main">99,99 zł</strong>`) +
		tile("3", ``, `<a class="product__name" href="/pl/products/c-3.html" title="NIRVANA In Utero LP">x</a><strong class="price --main">Cena na telefon</strong>`) +
		tile("4", ``, `<a class="product__name" href="/pl/products/d-4.html" title="">x</a><strong class="price --main">49,99 zł</strong>`) +
		tile("5", ``, `<a class="product__name" href="javascript:void(0)" title="NIRVANA Unplugged LP">x</a><strong class="price --main">59,99 zł</strong>`) +
		tile("6", ``, `<a class="product__name" href="https://winylownia.pl/pl/products/f-6.html?query_id=1" title="NIRVANA Incesticide LP">x</a><strong class="price --main">89,00 zł</strong>`) +
		tile("7", ``, `<a class="product__name" tabindex="0" href='/pl/products/g-7.html?query_id=1' title='KEITH RICHARDS &amp; THE X-PENSIVE WINOS Live At The Hollywood Palladium 2LP + 10" + DVD + CD'>x</a><strong class="price --main">580,05 zł</strong>`) +
		`</section></div></body></html>`
	listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, []byte(body))
	if err != nil {
		t.Fatalf("parseIdoSellSearch: %v", err)
	}
	got := byID(listings)
	if len(got) != 3 {
		t.Fatalf("got %d listings (%+v), want tiles 1, 6 and 7 only", len(got), listings)
	}
	if l := got["1"]; l.Title != "NIRVANA Nevermind LP & poster" || l.PriceCents != 137739 ||
		l.URL != "https://winylownia.pl/pl/products/a-1.html?color=red" {
		t.Fatalf("tile 1 = %+v", l)
	}
	if l := got["6"]; l.PriceCents != 8900 || l.URL != "https://winylownia.pl/pl/products/f-6.html" {
		t.Fatalf("tile 6 = %+v", l)
	}
	if l := got["7"]; l.PriceCents != 58005 || l.URL != "https://winylownia.pl/pl/products/g-7.html" ||
		l.Title != `KEITH RICHARDS & THE X-PENSIVE WINOS Live At The Hollywood Palladium 2LP + 10" + DVD + CD` {
		t.Fatalf("tile 7, single-quoted attributes = %+v", l)
	}
}

// winylowniaDouble serves the fixture on /search.php, sends the query
// "brak" the way IdoSell does a search without results (a redirect to the
// no-results page, which answers 404), answers a plain 404 under /gone/
// and 403 to any other path. It records the search request.
func winylowniaDouble(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/search.php" && r.URL.Query().Get("text") == "brak":
			http.Redirect(w, r, "/pl/noproduct.html?reason=product&text=brak", http.StatusFound)
		case r.URL.Path == "/search.php":
			gotPath = r.URL.Path + "?" + r.URL.RawQuery
			_, _ = w.Write(fixture(t, "winylownia.html"))
		case r.URL.Path == "/pl/noproduct.html":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(fixture(t, "winylownia-noproduct.html"))
		case strings.HasPrefix(r.URL.Path, "/gone/"):
			http.NotFound(w, r)
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &gotPath
}

func TestWinylowniaSearchesOverHTTP(t *testing.T) {
	srv, gotPath := winylowniaDouble(t)
	ctx := context.Background()
	src := Winylownia{Client: srv.Client(), BaseURL: srv.URL + "/search.php"}
	listings, err := src.Search(ctx, "Pink Floyd dark side")
	if err != nil || len(listings) != 1 {
		t.Fatalf("winylownia = %d listings, %v", len(listings), err)
	}
	if *gotPath != "/search.php?text=Pink+Floyd+dark+side" {
		t.Fatalf("request %q", *gotPath)
	}
	listings, err = src.Search(ctx, "brak")
	if err != nil || len(listings) != 0 {
		t.Fatalf("no results = %v, %v; want no listings and no error", listings, err)
	}
	for _, tc := range []struct{ path, status string }{{"/blocked/search.php", "http_403"}, {"/gone/search.php", "http_404"}} {
		_, err = Winylownia{Client: srv.Client(), BaseURL: srv.URL + tc.path}.Search(ctx, "x")
		if got := sourceStatus(err); got != tc.status {
			t.Errorf("%s status %q (err %v), want %s", tc.path, got, err, tc.status)
		}
	}
}

func TestRunAlertsOnWinylowniaListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	w := Want{
		Kind: "music", Name: "The Dark Side of the Moon", Authors: []string{"Pink Floyd"},
		Format: new("vinyl"), MaxPriceCents: new(int64(13000)),
	}
	wishlist(t, pool, &w)
	srv, _ := winylowniaDouble(t)
	notifier := &fakeNotifier{}
	src := Winylownia{Client: srv.Client(), BaseURL: srv.URL + "/search.php"}
	res := mustRun(t, newTestHunter(pool, notifier, src))
	if res.Matched != 1 || res.New != 1 || res.Sent != 1 || len(res.Errors) != 0 {
		t.Fatalf("run = %+v", res)
	}
	msgs := notifier.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	for _, part := range []string{
		"127,97 zł (limit 130,00 zł) · Winylownia · nowy",
		"PINK FLOYD The Dark Side of the Moon (50th Anniversary Remaster) LP",
		"https://winylownia.pl/pl/products/pink-floyd-the-dark-side-of-the-moon-50th-anniversary-remaster-lp-246548.html",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE source = 'winylownia'`); n != 1 {
		t.Fatalf("stored %d Winylownia observations, want 1", n)
	}
}
