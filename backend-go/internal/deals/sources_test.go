package deals

import (
	"context"
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
	for _, l := range listings {
		out[l.ExternalID] = l
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
	if listings[0].PriceCents != 300021 {
		t.Fatalf("grouped price = %d, want 300021", listings[0].PriceCents)
	}
}

func TestParseCeneo(t *testing.T) {
	listings := parseCeneo(fixture(t, "ceneo.html"), "https://www.ceneo.pl")
	if len(listings) != 6 {
		t.Fatalf("got %d listings, want 6", len(listings))
	}
	l := byID(listings)["165912980"]
	if l.Title != "Łowca androidów (Blade Runner) (Blu-Ray)" || l.PriceCents != 3370 ||
		l.URL != "https://www.ceneo.pl/165912980" || l.AcquiredAs == nil || *l.AcquiredAs != "new" {
		t.Fatalf("listing = %+v", l)
	}
	if n := len(parseCeneo([]byte(`<html><div class="cat-prod-row__body"></div></html>`), "")); n != 0 {
		t.Fatalf("page without tiles gave %d listings", n)
	}
}

func TestSourcesSearchOverHTTP(t *testing.T) {
	var gotPath, gotQuery, gotUA string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotUA = r.URL.Path, r.URL.RawQuery, r.UserAgent()
		switch {
		case strings.HasPrefix(r.URL.Path, "/olx"):
			_, _ = w.Write(fixture(t, "olx.json"))
		case strings.HasPrefix(r.URL.Path, "/pepper"):
			_, _ = w.Write(fixture(t, "pepper.xml"))
		case strings.HasPrefix(r.URL.Path, "/ceneo/"):
			_, _ = w.Write(fixture(t, "ceneo.html"))
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
	ceneo, err := Ceneo{Client: client, BaseURL: srv.URL + "/ceneo/"}.Search(ctx, "łowca androidów")
	if err != nil || len(ceneo) != 6 {
		t.Fatalf("ceneo = %d listings, %v", len(ceneo), err)
	}
	if gotPath != "/ceneo/;szukaj-łowca+androidów" {
		t.Fatalf("ceneo path = %q", gotPath)
	}
	if !strings.HasPrefix(ceneo[0].URL, srv.URL+"/ceneo/") {
		t.Fatalf("ceneo url = %q", ceneo[0].URL)
	}
	if _, err := (OLX{Client: client, BaseURL: srv.URL + "/blocked"}).Search(ctx, "x"); err == nil ||
		!strings.Contains(err.Error(), "status 403") {
		t.Fatalf("blocked olx err = %v", err)
	}
}
