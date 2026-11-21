package movie

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowTokenAllegro is an Allegro whose token endpoint answers after delay
// and whose stored token has expired, so the next catalog call refreshes.
func slowTokenAllegro(t *testing.T, delay time.Duration) (*Allegro, *allegroServer, *memStore) {
	t.Helper()
	now := time.Now()
	store := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(-time.Minute)}}
	srv := &allegroServer{}
	handler := srv.handler(t)
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/auth/oauth/token" {
			time.Sleep(delay)
		}
		handler(w, r)
	})
	a := &Allegro{
		ClientID: "id", ClientSecret: "secret", Store: store, Client: s.Client(), APIBase: s.URL, AuthBase: s.URL,
		Now: func() time.Time { return now },
	}
	return a, srv, store
}

func (a *allegroServer) refreshCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.refreshes
}

func (m *memStore) refresh() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.token.Refresh
}

// TestWaitKeepsTheRotatedTokenThroughShutdown: a lookup gives up on a slow
// refresh, then the server shuts down. Wait must return only once the
// rotated token is stored, so closing the database after it loses nothing.
func TestWaitKeepsTheRotatedTokenThroughShutdown(t *testing.T) {
	a, _, store := slowTokenAllegro(t, sourceTimeout+300*time.Millisecond)
	p := &Provider{Sources: []NameSource{a}, TMDB: &fakeSearcher{}}

	if _, err := p.Fetch(context.Background(), "5906190326195"); err == nil || !strings.Contains(err.Error(), "allegro:") {
		t.Fatalf("err = %v, want the timed-out allegro named", err)
	}
	if got := store.refresh(); got != refreshFor(0) {
		t.Fatalf("token stored before the refresh answered: %q", got)
	}
	waitAllegro(t, a)
	if got := store.refresh(); got != refreshFor(1) {
		t.Fatalf("after Wait the stored refresh token is %q, want the rotated %q", got, refreshFor(1))
	}
}

// TestAllegroCallerStopsWaitingAtItsDeadline: a slow refresh holds no
// caller past its own deadline, and the refresh it started still lands.
func TestAllegroCallerStopsWaitingAtItsDeadline(t *testing.T) {
	a, srv, store := slowTokenAllegro(t, 1500*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()
	_, err := a.Products(ctx, "5906190326195")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want the caller's deadline", err)
	}
	if took := time.Since(start); took > time.Second {
		t.Fatalf("caller waited %s for the refresh", took)
	}
	waitAllegro(t, a)
	if got := store.refresh(); got != refreshFor(1) {
		t.Fatalf("stored refresh token %q, want %q", got, refreshFor(1))
	}
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil || srv.refreshCount() != 1 {
		t.Fatalf("next lookup: %v, refreshes %d", err, srv.refreshCount())
	}
}

// TestAllegroCallersShareOneRefresh: lookups that find the token expired at
// the same time send one refresh; a second would present the refresh token
// the first just retired.
func TestAllegroCallersShareOneRefresh(t *testing.T) {
	a, srv, _ := slowTokenAllegro(t, 300*time.Millisecond)
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { _, errs[i] = a.Products(context.Background(), "5906190326195") })
	}
	wg.Wait()
	waitAllegro(t, a)
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if n := srv.refreshCount(); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
}

// TestAllegroExpiredCallerStartsNoRefresh: a caller whose deadline passed
// (for example while it waited for the token lock) sends no refresh.
func TestAllegroExpiredCallerStartsNoRefresh(t *testing.T) {
	a, srv, store := slowTokenAllegro(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.Products(ctx, "5906190326195"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	waitAllegro(t, a)
	if n := srv.refreshCount(); n != 0 || store.refresh() != refreshFor(0) {
		t.Fatalf("refreshes = %d, stored %q", n, store.refresh())
	}
}

// lateLimitSource reports a rate limit only after the source timeout.
type lateLimitSource struct{ reset time.Time }

func (lateLimitSource) Name() string { return "upcitemdb" }

func (s lateLimitSource) Products(context.Context, string) ([]Product, error) {
	time.Sleep(sourceTimeout + 200*time.Millisecond)
	return nil, &RateLimitError{Reset: s.reset}
}

// TestLateRateLimitStillPausesTheSource: a 429 that arrives after the
// lookup stopped waiting still pauses the source until its reset.
func TestLateRateLimitStillPausesTheSource(t *testing.T) {
	reset := time.Now().Add(time.Hour)
	p := &Provider{Sources: []NameSource{lateLimitSource{reset: reset}}, TMDB: &fakeSearcher{}}
	if _, err := p.Fetch(context.Background(), "5906190326195"); err == nil {
		t.Fatal("want the timed-out source to fail the fetch")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if until, ok := p.pausedUntil("upcitemdb"); ok {
			if !until.Equal(reset) {
				t.Fatalf("paused until %s, want %s", until, reset)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("source not paused after its late 429")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestAllegroRefreshOutlivesTheClientTimeout: the shared source client's
// timeout does not cut a refresh short; only refreshTimeout bounds it.
func TestAllegroRefreshOutlivesTheClientTimeout(t *testing.T) {
	a, srv, store := slowTokenAllegro(t, 1200*time.Millisecond)
	client := *a.Client
	client.Timeout = 500 * time.Millisecond
	a.Client = &client
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	waitAllegro(t, a)
	if got := store.refresh(); got != refreshFor(1) || srv.refreshCount() != 1 {
		t.Fatalf("stored %q after %d refreshes, want %q after 1", got, srv.refreshCount(), refreshFor(1))
	}
}

// TestAllegroRefreshesARejectedTokenOnce: when two lookups present the same
// access token and the catalog rejects it, the second, rejected after the
// first already refreshed, uses the new token instead of refreshing again.
func TestAllegroRefreshesARejectedTokenOnce(t *testing.T) {
	now := time.Now()
	store := &memStore{token: &Token{Access: "revoked", Refresh: refreshFor(0), Expiry: now.Add(time.Hour)}}
	srv := &allegroServer{}
	handler := srv.handler(t)
	var mu sync.Mutex
	rejected := 0
	s := serve(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/sale/products" && r.Header.Get("Authorization") == "Bearer revoked" {
			mu.Lock()
			rejected++
			late := rejected == 2
			mu.Unlock()
			if late {
				time.Sleep(500 * time.Millisecond)
			}
		}
		handler(w, r)
	})
	a := &Allegro{
		ClientID: "id", ClientSecret: "secret", Store: store, Client: s.Client(), APIBase: s.URL, AuthBase: s.URL,
		Now: func() time.Time { return now },
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Go(func() { _, errs[i] = a.Products(context.Background(), "5906190326195") })
	}
	wg.Wait()
	waitAllegro(t, a)
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if n := srv.refreshCount(); n != 1 {
		t.Fatalf("refreshes = %d, want 1", n)
	}
}

func waitAllegro(t *testing.T, a *Allegro) {
	t.Helper()
	if err := a.Wait(context.Background()); err != nil {
		t.Fatalf("wait: %v", err)
	}
}

// flakyStore fails the first failures writes, like a database that is
// briefly unreachable.
type flakyStore struct {
	memStore
	failures int
}

func (f *flakyStore) Save(ctx context.Context, provider string, t Token) error {
	f.mu.Lock()
	fail := f.failures > 0
	if fail {
		f.failures--
	}
	f.mu.Unlock()
	if fail {
		return errors.New("database unavailable")
	}
	return f.memStore.Save(ctx, provider, t)
}

// TestWaitStoresATokenWhoseWriteFailed: the store rejects the rotated pair
// once. Lookups keep using it from memory, and shutdown stores it, since
// Allegro no longer accepts the refresh token the store still holds.
func TestWaitStoresATokenWhoseWriteFailed(t *testing.T) {
	now := time.Now()
	store := &flakyStore{memStore: memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(-time.Minute)}}, failures: 1}
	srv := &allegroServer{}
	s := serve(t, srv.handler(t))
	a := &Allegro{
		ClientID: "id", ClientSecret: "secret", Store: store, Client: s.Client(), APIBase: s.URL, AuthBase: s.URL,
		Now: func() time.Time { return now },
	}
	if _, err := a.Products(context.Background(), "5906190326195"); err == nil || !strings.Contains(err.Error(), "save token") {
		t.Fatalf("err = %v, want the failed write", err)
	}
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil || srv.refreshCount() != 1 {
		t.Fatalf("next lookup: %v, refreshes %d", err, srv.refreshCount())
	}
	if got := store.refresh(); got != refreshFor(0) {
		t.Fatalf("stored %q before shutdown", got)
	}
	waitAllegro(t, a)
	if got := store.refresh(); got != refreshFor(1) {
		t.Fatalf("after Wait stored %q, want %q", got, refreshFor(1))
	}
}
