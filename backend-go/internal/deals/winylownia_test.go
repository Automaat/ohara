package deals

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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

func TestParseIdoSellSearchReadsEachTileOnly(t *testing.T) {
	// A tile without a price must not take one from markup after it.
	body := `<div id="container" class="search_page"><section id="search" class="search products">` +
		`<div class="product col-6" data-product_id="1"><h2><a class="product__name" href="/p/1.html" title="NIRVANA Nevermind LP">x</a></h2>` +
		`<div class="product__prices"></div></div>` +
		`<aside><strong class="price --main">19,99 zł</strong></aside></section></div>`
	listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, []byte(body))
	if err != nil || len(listings) != 0 {
		t.Fatalf("tile without its own price = %+v, %v; want no listing", listings, err)
	}
}

func TestParseIdoSellSearchIgnoresAttributeOrder(t *testing.T) {
	body := `<div class="search_page container" id="container"><section class="products d-flex search" id="search">` +
		`<div data-product_page="0" data-product_id = "7" class = "col-6 product"><a title="NIRVANA Bleach LP" href="/p/7.html" class="product__name">x</a>` +
		`<strong class="--main price">49,99 zł</strong></div></section></div>`
	listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, []byte(body))
	if err != nil || len(listings) != 1 || listings[0].ExternalID != "7" || listings[0].PriceCents != 4999 {
		t.Fatalf("reordered attributes = %+v, %v; want listing 7 at 49,99 zł", listings, err)
	}
}

func TestParseIdoSellSearchIgnoresInertMarkup(t *testing.T) {
	const tile = `<section id="search" class="search products"><div class="product" data-product_id="1">` +
		`<a class="product__name" href="/p/1.html" title="NIRVANA Nevermind LP">x</a><strong class="price --main">9,99 zł</strong></div></section>`
	for name, body := range map[string]string{
		"commented marker": `<html><body><!-- <div id="container" class="noproduct_page"> --><h1>Przerwa techniczna</h1></body></html>`,
		"commented tiles":  `<html><body><!--` + tile + `--><h1>Przerwa techniczna</h1></body></html>`,
		"scripted tiles":   "<html><script>const dormant = `" + tile + "`;</script><h1>Serwis niedostępny</h1></html>",
		"styled marker":    `<html><style>/* <div id="container" class="noproduct_page"> */</style><h1>Błąd</h1></html>`,
	} {
		listings, err := parseIdoSellSearch("winylownia", winylowniaSearch, []byte(body))
		if got := sourceStatus(err); got != metrics.SourceParseError || len(listings) != 0 {
			t.Errorf("%s: %d listings, status %q (err %v); want none and %s", name, len(listings), got, err, metrics.SourceParseError)
		}
	}
}

func TestCrawlClockSpacesRequestsPerHost(t *testing.T) {
	const delay = 50 * time.Millisecond
	c := newCrawlClock()
	ctx := context.Background()
	start := time.Now()
	for range 3 {
		release, err := c.acquire(ctx, "shop.test", delay)
		if err != nil {
			t.Fatalf("acquire: %v", err)
		}
		release()
	}
	if got := time.Since(start); got < 2*delay {
		t.Fatalf("3 requests to one host took %v, want at least %v", got, 2*delay)
	}
	other := time.Now()
	release, err := c.acquire(ctx, "other.test", delay)
	if err != nil || time.Since(other) > delay/2 {
		t.Fatalf("another host waited %v (err %v), want no wait", time.Since(other), err)
	}
	release()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.acquire(canceled, "shop.test", time.Hour); err == nil {
		t.Fatal("a canceled acquire returned no error")
	}
	if release, err := c.acquire(ctx, "shop.test", delay); err != nil {
		t.Fatalf("acquire after a canceled one: %v", err)
	} else {
		release()
	}
}

// TestCrawlDelayedSpacesArrivals checks the gap where the shop sees it:
// between the arrivals of concurrent and redirected requests, measured by
// the server.
func TestCrawlDelayedSpacesArrivals(t *testing.T) {
	const delay = 20 * time.Millisecond
	var mu sync.Mutex
	var arrivals []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		if r.URL.Path == "/hop" {
			http.Redirect(w, r, "/end", http.StatusFound)
		}
	}))
	t.Cleanup(srv.Close)
	client := crawlDelayed(srv.Client(), newCrawlClock(), delay)
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/hop", http.NoBody)
			if err != nil {
				t.Errorf("request: %v", err)
				return
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Errorf("get: %v", err)
				return
			}
			_ = resp.Body.Close()
		})
	}
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	slices.SortFunc(arrivals, func(a, b time.Time) int { return a.Compare(b) })
	if len(arrivals) != 8 {
		t.Fatalf("server saw %d requests, want 8", len(arrivals))
	}
	for i := 1; i < len(arrivals); i++ {
		if gap := arrivals[i].Sub(arrivals[i-1]); gap < delay {
			t.Fatalf("arrivals %d and %d were %v apart, want at least %v", i-1, i, gap, delay)
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
// no-results page, which answers 404), answers a plain 404 under /gone/,
// a 404 whose no-results marker is only in a comment under /fake404/ and
// 403 to any other path. It records the search request and when the
// no-results redirect and its hop arrived, and fails the test when two
// requests reached it less than the crawl delay apart.
func winylowniaDouble(t *testing.T) (*httptest.Server, *string, *[]time.Time) {
	t.Helper()
	var gotPath string
	var hops []time.Time
	var mu sync.Mutex
	var all []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		all = append(all, time.Now())
		mu.Unlock()
		switch {
		case r.URL.Path == "/search.php" && r.URL.Query().Get("text") == "brak":
			hops = append(hops, time.Now())
			http.Redirect(w, r, "/pl/noproduct.html?reason=product&text=brak", http.StatusFound)
		case r.URL.Path == "/search.php":
			gotPath = r.URL.Path + "?" + r.URL.RawQuery
			_, _ = w.Write(fixture(t, "winylownia.html"))
		case r.URL.Path == "/pl/noproduct.html":
			hops = append(hops, time.Now())
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write(fixture(t, "winylownia-noproduct.html"))
		case strings.HasPrefix(r.URL.Path, "/gone/"):
			http.NotFound(w, r)
		case strings.HasPrefix(r.URL.Path, "/fake404/"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`<html><!-- <div id="container" class="noproduct_page"> --></html>`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for i := 1; i < len(all); i++ {
			if gap := all[i].Sub(all[i-1]); gap < winylowniaCrawlDelay {
				t.Errorf("requests %d and %d to the shop were %v apart, want at least %v", i-1, i, gap, winylowniaCrawlDelay)
			}
		}
	})
	return srv, &gotPath, &hops
}

func TestWinylowniaSearchesOverHTTP(t *testing.T) {
	srv, gotPath, hops := winylowniaDouble(t)
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
	if len(*hops) != 2 || (*hops)[1].Sub((*hops)[0]) < winylowniaCrawlDelay {
		t.Fatalf("no-results redirect hops at %v, want 2 at least %v apart", *hops, winylowniaCrawlDelay)
	}
	for _, tc := range []struct{ path, status string }{
		{"/blocked/search.php", "http_403"}, {"/gone/search.php", "http_404"}, {"/fake404/search.php", "http_404"},
	} {
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
	srv, _, _ := winylowniaDouble(t)
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

// TestWinylowniaServesAlbumWishesOnly: a record shop is asked only the
// queries of album wishes, so a film named like a wished album's soundtrack
// is never searched there.
func TestWinylowniaServesAlbumWishesOnly(t *testing.T) {
	pool := dbtest.Migrated(t)
	film := Want{Kind: "movie", Name: "Blade Runner 2049", Format: new("uhd"), MaxPriceCents: new(int64(15000))}
	wishlist(t, pool, &film)
	lp := album("The Dark Side of the Moon", "Pink Floyd", "vinyl")
	lp.MaxPriceCents = new(int64(13000))
	wishlist(t, pool, &lp)
	var mu sync.Mutex
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked = append(asked, r.URL.Query().Get("text"))
		mu.Unlock()
		_, _ = w.Write(fixture(t, "winylownia.html"))
	}))
	t.Cleanup(srv.Close)
	notifier := &fakeNotifier{}
	res := mustRun(t, newTestHunter(pool, notifier, Winylownia{Client: srv.Client(), BaseURL: srv.URL + "/search.php"}))
	mu.Lock()
	defer mu.Unlock()
	if len(asked) != 1 || !strings.Contains(asked[0], "Dark Side") {
		t.Fatalf("winylownia asked %q, want the album wish's query only", asked)
	}
	if res.Sent != 1 || len(notifier.messages()) != 1 {
		t.Fatalf("run = %+v, want one alert for the album wish", res)
	}
}
