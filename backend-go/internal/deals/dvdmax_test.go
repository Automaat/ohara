package deals

import (
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func TestDVDmaxSearchFindsOnlyAvailableFilmFormats(t *testing.T) {
	body := dvdmaxFixture(t)
	for _, query := range []string{"Past Lives", "Poprzednie zycie"} {
		listings, err := parseDVDmax(body, query)
		if err != nil || len(listings) != 1 {
			t.Fatalf("%q listings = %+v, err = %v", query, listings, err)
		}
		ld := listings[0]
		if ld.ExternalID != "2729806" || ld.PriceCents != 9299 || ld.AcquiredAs == nil || *ld.AcquiredAs != "new" ||
			ld.URL != "https://dvdmax.pl/past-lives-poprzednie-zycie-blu-ray,art2729806" || ld.PhotoURL == "" {
			t.Fatalf("Past Lives listing = %+v", ld)
		}
	}
	listings, err := parseDVDmax(body, "Blade Runner")
	if err != nil || len(listings) != 1 || listings[0].PriceCents != 7999 || listingFormat(words(listings[0].Title)) != "uhd" {
		t.Fatalf("Blade Runner listings = %+v, err = %v", listings, err)
	}
	if rows, err := parseDVDmax(body, "Czerwona jaskółka"); err != nil || len(rows) != 0 {
		t.Fatalf("film bundle results = %+v, err = %v", rows, err)
	}
	for _, invalid := range []string{`{}`, `{"products":null}`, `{"products":[{}]}`} {
		if _, err := parseDVDmax([]byte(invalid), "Past Lives"); err == nil {
			t.Errorf("accepted malformed response %s", invalid)
		}
	}
	if rows, err := parseDVDmax([]byte(`{"products":[]}`), "Past Lives"); err != nil || len(rows) != 0 {
		t.Fatalf("empty products = %+v, err = %v", rows, err)
	}
}

func TestDVDmaxAndBlueDVDSearchOnlyFilmWishes(t *testing.T) {
	pool := dbtest.Migrated(t)
	body := dvdmaxFixture(t)
	movie := Want{Kind: "movie", Name: "Past Lives", Format: new("bluray")}
	movieID := wishlist(t, pool, &movie)
	book := Want{Kind: "book", Name: "Lalka", Authors: []string{"Bolesław Prus"}}
	wishlist(t, pool, &book)
	music := album("Born to Run", "Bruce Springsteen", "cd")
	wishlist(t, pool, &music)

	var mu sync.Mutex
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.URL.RequestURI())
		mu.Unlock()
		if r.URL.Path == "/blue" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	d := DVDmax{Client: srv.Client(), BaseURL: srv.URL + "/dvdmax"}
	b := BlueDVD{Client: srv.Client(), BaseURL: srv.URL + "/blue"}
	res := mustRun(t, newTestHunter(pool, nil, d, b))
	if res.Matched != 1 || res.New != 1 || len(res.Errors) != 0 {
		t.Fatalf("run = %+v", res)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"/dvdmax?filters%5Bcat%5D=1&limit=50&page=1&title=Past+Lives",
		"/blue?per_page=50&search=Past+Lives+blu-ray",
	}
	if !slices.Equal(requests, want) {
		t.Fatalf("requests = %q, want %q", requests, want)
	}
	for _, row := range res.coverage.Rows {
		if row.Source == d.Name() && row.CopyID == movieID && row.Matched == 1 {
			return
		}
	}
	t.Fatalf("missing DVDmax match for film in coverage: %+v", res.coverage)
}

func dvdmaxFixture(t *testing.T) []byte {
	t.Helper()
	body, err := os.ReadFile("testdata/dvdmax.json")
	if err != nil {
		t.Fatal(err)
	}
	return body
}
