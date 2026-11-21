package deals

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestPacerSpacesLanes(t *testing.T) {
	const gap = 50 * time.Millisecond
	p := NewPacer(gap)
	ctx := context.Background()
	var mu sync.Mutex
	var starts []time.Time
	var wg sync.WaitGroup
	for range 4 {
		lane := p.Lane()
		wg.Go(func() {
			sent, err := lane.Wait(ctx, "olx")
			if err != nil {
				t.Errorf("Wait: %v", err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
			sent()
		})
	}
	wg.Wait()
	if len(starts) != 4 {
		t.Fatalf("%d requests started, want 4", len(starts))
	}
	for i := 1; i < len(starts); i++ {
		if d := starts[i].Sub(starts[i-1]); d < gap {
			t.Fatalf("request %d started %v after the previous one, want at least %v", i, d, gap)
		}
	}
}

func TestLaneDoesNotWaitForItself(t *testing.T) {
	p := NewPacer(time.Hour)
	lane := p.Lane()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := range 3 {
		sent, err := lane.Wait(ctx, "olx")
		if err != nil {
			t.Fatalf("request %d of one lane: %v", i, err)
		}
		sent()
	}
}

func TestPacerLeavesOtherSitesAlone(t *testing.T) {
	p := NewPacer(time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	first, second := p.Lane(), p.Lane()
	for _, site := range []string{"olx", "pepper", "skupszop"} {
		sent, err := first.Wait(ctx, site)
		if err != nil {
			t.Fatalf("first lane on %s: %v", site, err)
		}
		sent()
	}
	sent, err := second.Wait(ctx, "allegro")
	if err != nil {
		t.Fatalf("second lane on a site the first never asked: %v", err)
	}
	sent()
}

func TestPacerWaitStopsWithTheContext(t *testing.T) {
	p := NewPacer(time.Hour)
	first, second := p.Lane(), p.Lane()
	sent, err := first.Wait(context.Background(), "olx")
	if err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	sent()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := second.Wait(ctx, "olx"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want the context's deadline", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := second.Wait(canceled, "olx"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait on a canceled context = %v, want context.Canceled", err)
	}
	again, cancelAgain := context.WithTimeout(context.Background(), time.Second)
	defer cancelAgain()
	sent, err = first.Wait(again, "olx")
	if err != nil {
		t.Fatalf("the lanes that gave up kept holding the site: %v", err)
	}
	sent()
}

func TestPacedSourceKeepsTheSource(t *testing.T) {
	src := &fakeSource{name: "olx", perQuery: true, listings: []Listing{{Title: "Diuna DVD"}}}
	paced := NewPacer(time.Hour).Lane().Source(src)
	if paced.Name() != "olx" || !paced.PerQuery() {
		t.Fatalf("paced source is %q, per query %v", paced.Name(), paced.PerQuery())
	}
	got, err := paced.Search(context.Background(), "diuna")
	if err != nil || len(got) != 1 || got[0].Title != "Diuna DVD" {
		t.Fatalf("Search = %v, %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := paced.Search(ctx, "diuna"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Search on a canceled context = %v", err)
	}
	if len(src.queries) != 1 {
		t.Fatalf("source asked %q, want only the first search", src.queries)
	}
}

// stalledTransport waits before sending, like a request still connecting.
type stalledTransport struct{ stall time.Duration }

func (s stalledTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	time.Sleep(s.stall)
	return http.DefaultTransport.RoundTrip(r)
}

// olxServer answers with the OLX fixture and keeps when each request
// arrived. A request whose query is held waits until release closes.
func olxServer(t *testing.T, held string, release <-chan struct{}) (*httptest.Server, func() []time.Time) {
	t.Helper()
	body, err := os.ReadFile("testdata/olx.json")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var arrived []time.Time
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrived = append(arrived, time.Now())
		mu.Unlock()
		if r.URL.Query().Get("query") == held {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []time.Time {
		mu.Lock()
		defer mu.Unlock()
		return append([]time.Time(nil), arrived...)
	}
}

func TestPacedSourceHoldsTheSiteUntilTheRequestIsSent(t *testing.T) {
	const gap = 100 * time.Millisecond
	srv, arrived := olxServer(t, "", nil)
	p := NewPacer(gap)
	stalled := p.Lane().Source(OLX{Client: &http.Client{Transport: stalledTransport{stall: gap * 3 / 2}}, BaseURL: srv.URL + "/"})
	quick := p.Lane().Source(OLX{Client: NewHTTPClient(), BaseURL: srv.URL + "/"})
	ctx := context.Background()
	var wg sync.WaitGroup
	wg.Go(func() {
		if _, err := stalled.Search(ctx, "diuna"); err != nil {
			t.Errorf("stalled search: %v", err)
		}
	})
	time.Sleep(gap / 10)
	if _, err := quick.Search(ctx, "katedra"); err != nil {
		t.Fatalf("quick search: %v", err)
	}
	wg.Wait()
	times := arrived()
	if len(times) != 2 {
		t.Fatalf("OLX got %d requests, want 2", len(times))
	}
	const networkJitter = gap / 20
	if d := times[1].Sub(times[0]); d < gap-networkJitter {
		t.Fatalf("the second lane's request arrived %v after the stalled one, want about %v", d, gap)
	}
}

func TestPacedSourceDoesNotHoldTheSiteForAnAnswer(t *testing.T) {
	const gap = 100 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	srv, _ := olxServer(t, "held", release)
	p := NewPacer(gap)
	held := p.Lane().Source(OLX{Client: NewHTTPClient(), BaseURL: srv.URL + "/"})
	other := p.Lane().Source(OLX{Client: NewHTTPClient(), BaseURL: srv.URL + "/"})
	heldCtx, stopHeld := context.WithCancel(context.Background())
	defer stopHeld()
	go func() { _, _ = held.Search(heldCtx, "held") }()
	time.Sleep(gap / 2)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := other.Search(ctx, "diuna"); err != nil {
		t.Fatalf("search beside a request waiting for its answer: %v", err)
	}
}
