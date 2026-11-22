package deals

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	return b
}

func byID(listings []Listing) map[string]Listing {
	out := map[string]Listing{}
	for i := range listings {
		out[listings[i].ExternalID] = listings[i]
	}
	return out
}

func TestParseOLX(t *testing.T) {
	listings, err := parseOLX(fixture(t, "olx.json"))
	if err != nil {
		t.Fatalf("parseOLX: %v", err)
	}
	got := byID(listings)
	if len(got) != 8 {
		t.Fatalf("got %d listings, want the 8 priced in PLN", len(got))
	}
	l := got["1112718649"]
	if l.Title != "Blade Runner The Final Cut BluRay" || l.PriceCents != 4500 ||
		l.AcquiredAs == nil || *l.AcquiredAs != "used" || !strings.HasPrefix(l.URL, "https://www.olx.pl/d/oferta/") {
		t.Fatalf("listing = %+v", l)
	}
	if l.PhotoURL != "https://images.example.test/bluray;s=400x300" || got["1045624035"].PhotoURL != "" {
		t.Fatalf("OLX photo with fixture = %q, without fixture = %q", l.PhotoURL, got["1045624035"].PhotoURL)
	}
	if got["1045624035"].PriceCents != 2999 {
		t.Fatalf("decimal price = %d, want 2999", got["1045624035"].PriceCents)
	}
	if p := got["1088939492"]; p.AcquiredAs != nil {
		t.Fatalf("listing without state got %q", *p.AcquiredAs)
	}
	for _, missing := range []string{"1", "2"} {
		if _, ok := got[missing]; ok {
			t.Fatalf("listing %s without a PLN price was kept", missing)
		}
	}
}

func TestParseOLXRejectsOtherShapes(t *testing.T) {
	for _, body := range []string{`<html>`, `{"error": "blocked"}`} {
		if _, err := parseOLX([]byte(body)); err == nil {
			t.Errorf("parseOLX(%s) accepted it", body)
		}
	}
}

func TestParsePepper(t *testing.T) {
	listings, err := parsePepper(fixture(t, "pepper.xml"))
	if err != nil {
		t.Fatalf("parsePepper: %v", err)
	}
	if len(listings) != 4 {
		t.Fatalf("got %d listings, want 4 priced deals", len(listings))
	}
	l := byID(listings)["https://www.pepper.pl/promocje/lowca-androidow-4k-1392999"]
	if l.PriceCents != 8999 || l.Source != "pepper" || !strings.HasPrefix(l.Title, "Łowca androidów 4K UHD") {
		t.Fatalf("listing = %+v", l)
	}
	if !strings.HasPrefix(listings[0].PhotoURL, "https://static.pepper.pl/threads/") || l.PhotoURL != "" {
		t.Fatalf("Pepper photo with fixture = %q, without fixture = %q", listings[0].PhotoURL, l.PhotoURL)
	}
	if listings[0].PriceCents != 300021 {
		t.Fatalf("grouped price = %d, want 300021", listings[0].PriceCents)
	}
}

func TestSourcesSearchOverHTTP(t *testing.T) {
	var gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery, gotUA = r.URL.RawQuery, r.UserAgent()
		switch {
		case strings.HasPrefix(r.URL.Path, "/olx"):
			_, _ = w.Write(fixture(t, "olx.json"))
		case strings.HasPrefix(r.URL.Path, "/pepper"):
			_, _ = w.Write(fixture(t, "pepper.xml"))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	t.Cleanup(srv.Close)
	ctx := context.Background()
	client := srv.Client()

	olx, err := OLX{Client: client, BaseURL: srv.URL + "/olx/"}.Search(ctx, "Blade Runner blu-ray")
	if err != nil || len(olx) != 8 {
		t.Fatalf("olx = %d listings, %v", len(olx), err)
	}
	if gotQuery != "limit=50&offset=0&query=Blade+Runner+blu-ray" || !strings.HasPrefix(gotUA, "Mozilla/5.0") {
		t.Fatalf("olx request query %q, user agent %q", gotQuery, gotUA)
	}
	if pepper, err := (Pepper{Client: client, BaseURL: srv.URL + "/pepper"}).Search(ctx, ""); err != nil || len(pepper) != 4 {
		t.Fatalf("pepper = %d listings, %v", len(pepper), err)
	}
	if _, err := (OLX{Client: client, BaseURL: srv.URL + "/blocked"}).Search(ctx, "x"); err == nil ||
		!strings.Contains(err.Error(), "status 403") {
		t.Fatalf("blocked olx err = %v", err)
	}
}

// olxOffers is an OLX answer of n offers without a price, which the parser
// leaves out but a page still counts.
func olxOffers(n int) string {
	offers := make([]string, n)
	for i := range offers {
		offers[i] = fmt.Sprintf(`{"id": %d, "title": "Oddam za darmo", "params": []}`, i+1)
	}
	return `{"data": [` + strings.Join(offers, ",") + `]}`
}

func TestOLXPageIsCompleteBelowItsLimit(t *testing.T) {
	var answer string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(answer))
	}))
	t.Cleanup(srv.Close)
	olx := OLX{Client: srv.Client(), BaseURL: srv.URL + "/"}
	ctx := context.Background()

	answer = string(fixture(t, "olx.json"))
	page, err := olx.SearchPage(ctx, "Blade Runner blu-ray")
	if err != nil || !page.Complete || len(page.Listings) != 8 || len(page.Seen) != 10 {
		t.Fatalf("fixture page = %d listings, %d seen, complete %v, %v; want 8 of 10 offers of a complete answer",
			len(page.Listings), len(page.Seen), page.Complete, err)
	}
	for i := range page.Listings {
		if l := &page.Listings[i]; l.Query != "Blade Runner blu-ray" {
			t.Fatalf("listing %s query %q, want the query that found it", l.ExternalID, l.Query)
		}
	}
	for _, tc := range []struct {
		offers   int
		complete bool
	}{{49, true}, {50, false}} {
		answer = olxOffers(tc.offers)
		page, err := olx.SearchPage(ctx, "x")
		if err != nil || page.Complete != tc.complete || len(page.Listings) != 0 || len(page.Seen) != tc.offers {
			t.Fatalf("%d unpriced offers: %d listings, %d seen, complete %v, %v; want complete %v, all seen",
				tc.offers, len(page.Listings), len(page.Seen), page.Complete, err, tc.complete)
		}
	}
}
