package deals

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

func TestBlueDVDMatchesOnlyTheWishedTVSeasonAndFormat(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	wishTVSeason(t, pool, "Severance")
	var mu sync.Mutex
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.Query().Get("search"))
		mu.Unlock()
		_, _ = w.Write([]byte(`[
			{"id":1,"name":"Severance Season 1 Blu-ray","prices":{"price":"4099","currency_code":"PLN"}},
			{"id":2,"name":"Severance Season 2 Blu-ray","prices":{"price":"4099","currency_code":"PLN"}},
			{"id":3,"name":"Severance Sezon 1 4K UHD","prices":{"price":"4099","currency_code":"PLN"}},
			{"id":4,"name":"Severance Seasons Blu-ray","prices":{"price":"4099","currency_code":"PLN"}},
			{"id":5,"name":"Severance Sezon 1-2 Blu-ray","prices":{"price":"4099","currency_code":"PLN"}}
		]`))
	}))
	t.Cleanup(srv.Close)
	other := &fakeSource{name: "olx", perQuery: true}
	notifier := &fakeNotifier{}
	hunter := newTestHunter(pool, notifier, BlueDVD{Client: srv.Client(), BaseURL: srv.URL}, other)

	run := mustRun(t, hunter)
	listed, err := hunter.store.Recent(ctx, RecentFilter{Limit: 50, UnderLimit: true})

	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(queries, []string{"Severance"}) || len(other.queries) != 0 {
		t.Fatalf("BlueDVD queries = %q, other source queries = %q", queries, other.queries)
	}
	if err != nil || len(listed) != 1 || listed[0].ExternalID != "1" {
		t.Fatalf("under-limit offers = %+v, err = %v, want only season 1 Blu-ray", listed, err)
	}
	if run.Wants != 1 || run.Matched != 1 || len(notifier.messages()) != 1 {
		t.Fatalf("run = %+v, notifications = %q, want one TV match and alert", run, notifier.messages())
	}
}

func TestDVDmaxUnavailableOfferStopsRetriesUntilItsNewPrice(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	wishlist(t, pool, &Want{Kind: "movie", Name: "Past Lives", Format: new("bluray"), MaxPriceCents: new(int64(5000))})
	var response atomic.Pointer[string]
	offer := func(status, price string) *string {
		return new(fmt.Sprintf(`{"products":[{"aId":175,"aName":"Past Lives","aPrice":%q,
			"dictionaryDcStatus":%q,"nosnik":"BLU-RAY","url":"/past-lives,art175"}]}`, price, status))
	}
	response.Store(offer("DOSTEPNY24", "40.99"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(*response.Load()))
	}))
	t.Cleanup(srv.Close)
	notifier := &fakeNotifier{fail: 1}
	hunter := newTestHunter(pool, notifier, DVDmax{Client: srv.Client(), BaseURL: srv.URL})

	first := mustRun(t, hunter)
	if first.Alerts != 1 || len(notifier.messages()) != 0 {
		t.Fatalf("first run = %+v, want queued alert with failed delivery", first)
	}
	response.Store(offer("WKROTCE", "40.99"))
	offline := mustRun(t, hunter)
	listed, err := hunter.store.Recent(ctx, RecentFilter{Limit: 50, UnderLimit: true})
	if err != nil || len(listed) != 0 || offline.Matched != 0 || len(notifier.messages()) != 0 {
		t.Fatalf("unavailable run = %+v, offers = %+v, err = %v, notifications = %q",
			offline, listed, err, notifier.messages())
	}
	response.Store(offer("DOSTEPNY24", "38.99"))
	mustRun(t, hunter)
	listed, err = hunter.store.Recent(ctx, RecentFilter{Limit: 50, UnderLimit: true})
	if err != nil || len(listed) != 1 || listed[0].PriceCents != 3899 || len(notifier.messages()) != 1 {
		t.Fatalf("restocked offers = %+v, err = %v, notifications = %q", listed, err, notifier.messages())
	}
}

func TestDVDmaxCanaryAnswersACaseVariantOfTheWishlistQuery(t *testing.T) {
	pool := dbtest.Migrated(t)
	wishlist(t, pool, &Want{Kind: "movie", Name: "Blade runner", Format: new("uhd")})
	body := dvdmaxFixture(t)
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	hunter := newCanaryHunter(pool, DVDmax{Client: srv.Client(), BaseURL: srv.URL})

	run := mustRun(t, hunter)

	if requests.Load() != 1 || run.Matched != 1 {
		t.Fatalf("requests = %d, matched = %d, want one shared result", requests.Load(), run.Matched)
	}
}
