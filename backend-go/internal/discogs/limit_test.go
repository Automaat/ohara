package discogs_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
)

// arrivals stands in for the Discogs API inside a synctest bubble: it
// answers every endpoint and keeps each request's arrival on the bubble's
// clock. Requests reach it in process, since a bubble's clock stands still
// while a goroutine waits on a real socket.
type arrivals struct {
	mu    sync.Mutex
	times []time.Time
}

func (a *arrivals) RoundTrip(r *http.Request) (*http.Response, error) {
	a.mu.Lock()
	a.times = append(a.times, time.Now())
	a.mu.Unlock()
	rec := httptest.NewRecorder()
	if r.URL.Path == "/database/search" {
		_, _ = rec.WriteString(`{"results":[{"id":1,"title":"Artist - Album"}]}`)
	} else {
		_, _ = rec.WriteString(`{}`)
	}
	return rec.Result(), nil
}

func (a *arrivals) seen() []time.Time {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.times)
}

func (a *arrivals) client(limiter *discogs.Limiter) *discogs.Client {
	return &discogs.Client{HTTP: &http.Client{Transport: a}, BaseURL: "http://discogs.test", Token: "secret", Limiter: limiter}
}

func TestLimiterKeepsEveryMinuteWithinBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &arrivals{}
		limiter := discogs.NewLimiter()
		clients := []*discogs.Client{api.client(limiter), api.client(limiter), api.client(limiter)}
		const goroutines, perGoroutine = 8, 20
		var wg sync.WaitGroup
		for g := range goroutines {
			c := clients[g%len(clients)]
			wg.Go(func() {
				for i := range perGoroutine {
					var err error
					if i%2 == 0 {
						_, err = c.SearchBarcode(t.Context(), "5099902894225")
					} else {
						_, err = c.Stats(t.Context(), int64(g), "EUR")
					}
					if err != nil {
						t.Errorf("request: %v", err)
					}
				}
			})
		}
		wg.Wait()
		times := api.seen()
		if len(times) != goroutines*perGoroutine {
			t.Fatalf("server saw %d requests, want %d", len(times), goroutines*perGoroutine)
		}
		slices.SortFunc(times, func(a, b time.Time) int { return a.Compare(b) })
		for i := 0; i+60 < len(times); i++ {
			if span := times[i+60].Sub(times[i]); span <= time.Minute {
				t.Fatalf("requests %d to %d arrived within %v: 61 requests in one minute", i, i+60, span)
			}
		}
	})
}

func TestLookupGetsTurnWhileValuationUsesWholeBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &arrivals{}
		limiter := discogs.NewLimiter()
		pricing, lookup := api.client(limiter), api.client(limiter)
		ctx, stop := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			for id := int64(1); id <= 500 && ctx.Err() == nil; id++ {
				if _, err := pricing.Stats(ctx, id, "EUR"); err != nil && ctx.Err() == nil {
					t.Errorf("stats: %v", err)
				}
				if _, err := pricing.PriceSuggestions(ctx, id); err != nil && ctx.Err() == nil {
					t.Errorf("suggestions: %v", err)
				}
			}
		}()
		time.Sleep(2 * time.Minute)
		if n := len(api.seen()); n < 100 {
			t.Fatalf("pricing loop sent %d requests in 2 minutes, want the whole budget", n)
		}

		lookupCtx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		start := time.Now()
		rel, err := lookup.SearchBarcode(lookupCtx, "5099902894225")
		if err != nil {
			t.Fatalf("lookup during valuation: %v", err)
		}
		if len(rel) != 1 {
			t.Fatalf("releases = %+v", rel)
		}
		if took := time.Since(start); took >= 10*time.Second {
			t.Fatalf("lookup took %v, want under the 10 s lookup budget", took)
		}
		stop()
		<-done
	})
}

func TestLimiterNeverWaitsPastContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &arrivals{}
		c := api.client(discogs.NewLimiter())
		for range 5 {
			if _, err := c.Stats(t.Context(), 1, "EUR"); err != nil {
				t.Fatalf("burst: %v", err)
			}
		}
		spent := time.Now()

		short, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		_, err := c.SearchBarcode(short, "5099902894225")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("short deadline: err = %v, want context.DeadlineExceeded", err)
		}

		canceled, cancelWait := context.WithCancel(t.Context())
		go func() {
			time.Sleep(500 * time.Millisecond)
			cancelWait()
		}()
		_, err = c.SearchBarcode(canceled, "5099902894225")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait: err = %v, want context.Canceled", err)
		}
		if n := len(api.seen()); n != 5 {
			t.Fatalf("server saw %d requests, want only the 5 of the burst", n)
		}

		if _, err := c.SearchBarcode(t.Context(), "5099902894225"); err != nil {
			t.Fatalf("after the given-up waits: %v", err)
		}
		times := api.seen()
		if got := times[len(times)-1].Sub(spent); got != 1200*time.Millisecond {
			t.Fatalf("next request came %v after the burst, want 1.2s: a given-up wait spent budget", got)
		}
	})
}

func TestNilLimiterSendsAtOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		api := &arrivals{}
		c := api.client(nil)
		start := time.Now()
		for range 100 {
			if _, err := c.Stats(t.Context(), 1, "EUR"); err != nil {
				t.Fatalf("stats: %v", err)
			}
		}
		if took := time.Since(start); took != 0 {
			t.Fatalf("100 requests without a limiter took %v", took)
		}
	})
}
