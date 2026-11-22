package deals

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
)

func TestParseBlueDVD(t *testing.T) {
	listings, err := parseWooStore("bluedvd", fixture(t, "bluedvd.json"))
	if err != nil {
		t.Fatalf("parseWooStore: %v", err)
	}
	got := byID(listings)
	if len(got) != 2 {
		t.Fatalf("got %d listings (%v), want the one in stock and the preorder, not the 2 out of stock", len(got), listings)
	}
	l := got["27861"]
	if l.Source != "bluedvd" || l.Title != "Blade Runner 2049 – 4K UHD + Blu-ray" || l.PriceCents != 14990 ||
		l.URL != "https://bluedvd.pl/film/blade-runner-2049-4k-uhd-blu-ray-2/" ||
		l.PhotoURL != "https://bluedvd.pl/wp-content/uploads/okladki/blade-runner-2049-4k-uhd-blu-ray-uhdrh4933.jpg" ||
		l.AcquiredAs == nil || *l.AcquiredAs != "new" || len(l.Raw) == 0 {
		t.Fatalf("listing = %+v", l)
	}
	if p, ok := got["28081"]; !ok || p.PriceCents != 13999 || p.Title != "Ucieczka z Alcatraz (1979) – 4K UHD" {
		t.Fatalf("preorder = %+v (kept %v), want it kept at 139,99 zł", p, ok)
	}
}

func TestParseBlueDVDTitlesMatchByFormat(t *testing.T) {
	listings, err := parseWooStore("bluedvd", fixture(t, "bluedvd.json"))
	if err != nil {
		t.Fatalf("parseWooStore: %v", err)
	}
	title := byID(listings)["27861"].Title
	for _, tc := range []struct {
		format string
		match  bool
	}{{"uhd", true}, {"bluray", false}} {
		w := Want{Kind: "movie", Name: "Blade Runner 2049", Format: new(tc.format)}
		w.Prepare()
		if got := w.Matches(title); got != tc.match {
			t.Errorf("%s want Matches(%q) = %v, want %v", tc.format, title, got, tc.match)
		}
	}
}

func TestParseWooStoreAnswers(t *testing.T) {
	listings, err := parseWooStore("bluedvd", []byte(`[]`))
	if err != nil || len(listings) != 0 {
		t.Fatalf("empty array = %v, %v; want no listings and no error", listings, err)
	}
	for _, body := range []string{
		`{}`, `<html><body>Błąd</body></html>`, `null`, `{"code":"rest_no_route"}`, `["maintenance"]`, `[{}, null]`,
	} {
		_, err := parseWooStore("bluedvd", []byte(body))
		if got := sourceStatus(err); got != metrics.SourceParseError {
			t.Errorf("parseWooStore(%s) status %q (err %v), want %s", body, got, err, metrics.SourceParseError)
		}
	}
}

func TestParseWooStoreLeavesOutOtherCurrencies(t *testing.T) {
	body := `[
		{"id": 1, "name": "Diuna &#8211; Blu-ray", "permalink": "https://shop.test/1", "prices": {"price": "4999", "currency_code": "EUR", "currency_minor_unit": 2}, "stock_availability": {"class": "in-stock"}},
		{"id": 2, "name": "Diuna &#8211; Blu-ray", "permalink": "https://shop.test/2", "prices": {"price": "4999", "currency_code": "PLN", "currency_minor_unit": 2}, "stock_availability": {"class": "in-stock"}},
		{"id": 3, "name": "Diuna &#8211; Blu-ray", "permalink": "https://shop.test/3", "prices": {"price": "", "currency_code": "PLN", "currency_minor_unit": 2}, "stock_availability": {"class": "in-stock"}}
	]`
	listings, err := parseWooStore("bluedvd", []byte(body))
	if err != nil || len(listings) != 1 || listings[0].ExternalID != "2" || listings[0].PhotoURL != "" {
		t.Fatalf("listings = %+v, %v; want only the priced PLN product, without a photo", listings, err)
	}
}

func TestWooCents(t *testing.T) {
	for _, tc := range []struct {
		price string
		minor *int
		cents int64
		ok    bool
	}{
		{"14990", new(2), 14990, true},
		{"14990", nil, 14990, true},
		{"150", new(0), 15000, true},
		{"149905", new(3), 14991, true},
		{"149904", new(3), 14990, true},
		{"-100", new(2), 0, false},
		{"149,90", new(2), 0, false},
		{"100000001", new(2), 0, false},
	} {
		got, ok := wooCents(wooPrices{Price: tc.price, MinorUnit: tc.minor})
		if got != tc.cents || ok != tc.ok {
			t.Errorf("wooCents(%q) = %d, %v; want %d, %v", tc.price, got, ok, tc.cents, tc.ok)
		}
	}
}

// blueDVDDouble serves the fixture on the Store API path and answers 403
// to any other.
func blueDVDDouble(t *testing.T) (*httptest.Server, *string) {
	t.Helper()
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path + "?" + r.URL.RawQuery
		if r.URL.Path != "/wp-json/wc/store/v1/products" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write(fixture(t, "bluedvd.json"))
	}))
	t.Cleanup(srv.Close)
	return srv, &gotPath
}

func TestBlueDVDSearchesOverHTTP(t *testing.T) {
	srv, gotPath := blueDVDDouble(t)
	ctx := context.Background()
	src := BlueDVD{Client: srv.Client(), BaseURL: srv.URL + "/wp-json/wc/store/v1/products"}
	listings, err := src.Search(ctx, "Blade Runner 2049")
	if err != nil || len(listings) != 2 {
		t.Fatalf("bluedvd = %d listings, %v", len(listings), err)
	}
	if *gotPath != "/wp-json/wc/store/v1/products?per_page=50&search=Blade+Runner+2049" {
		t.Fatalf("request %q", *gotPath)
	}
	_, err = BlueDVD{Client: srv.Client(), BaseURL: srv.URL + "/blocked"}.Search(ctx, "x")
	if got := sourceStatus(err); got != "http_403" {
		t.Fatalf("blocked status %q (err %v), want http_403", got, err)
	}
}

func TestRunAlertsOnBlueDVDListing(t *testing.T) {
	pool := dbtest.Migrated(t)
	w := Want{Kind: "movie", Name: "Blade Runner 2049", Format: new("uhd"), MaxPriceCents: new(int64(15000))}
	wishlist(t, pool, &w)
	srv, _ := blueDVDDouble(t)
	notifier := &fakeNotifier{}
	src := BlueDVD{Client: srv.Client(), BaseURL: srv.URL + "/wp-json/wc/store/v1/products"}
	res := mustRun(t, newTestHunter(pool, notifier, src))
	if res.Matched != 1 || res.New != 1 || res.Sent != 1 || len(res.Errors) != 0 {
		t.Fatalf("run = %+v", res)
	}
	msgs := notifier.messages()
	if len(msgs) != 1 {
		t.Fatalf("sent %d messages, want 1", len(msgs))
	}
	for _, part := range []string{
		"🎯 Okazja: Blade Runner 2049 · 4K UHD", "149,90 zł (limit 150,00 zł) · BlueDVD · nowy",
		"Blade Runner 2049 – 4K UHD + Blu-ray", "https://bluedvd.pl/film/blade-runner-2049-4k-uhd-blu-ray-2/",
	} {
		if !strings.Contains(msgs[0], part) {
			t.Fatalf("message %q lacks %q", msgs[0], part)
		}
	}
	if n := count(t, pool, `SELECT count(*) FROM price_observation WHERE source = 'bluedvd'`); n != 1 {
		t.Fatalf("stored %d BlueDVD observations, want 1", n)
	}
}

func TestBlueDVDAsksItsOwnCanary(t *testing.T) {
	pool := dbtest.Migrated(t)
	srv, gotPath := blueDVDDouble(t)
	src := BlueDVD{Client: srv.Client(), BaseURL: srv.URL + "/wp-json/wc/store/v1/products"}
	h := NewHunter(NewStore(pool), []Source{src}, nil, Config{CanaryQuery: "dvd"}, slog.New(slog.DiscardHandler))
	h.sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	if res := mustRun(t, h); len(res.Errors) != 0 {
		t.Fatalf("run errors = %q", res.Errors)
	}
	if *gotPath != "/wp-json/wc/store/v1/products?per_page=50&search=4k" {
		t.Fatalf("canary request %q, want the 4k search", *gotPath)
	}
	*gotPath = ""
	mustRun(t, newTestHunter(pool, nil, src))
	if *gotPath != "" {
		t.Fatalf("without a canary query the run asked %q", *gotPath)
	}
}
