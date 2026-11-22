package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

var errUnavailable = errors.New("503 Service Unavailable")

// flakyProvider answers like fakeProvider while down is false and fails
// with errUnavailable while it is true.
type flakyProvider struct {
	fakeProvider
	down atomic.Bool
}

func (p *flakyProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw, err := p.fakeProvider.Fetch(ctx, code)
	if p.down.Load() {
		return nil, errUnavailable
	}
	return raw, err
}

// countedProvider is a provider whose Fetch calls a test can count.
type countedProvider interface {
	lookup.Provider
	fetchCount() int
}

func TestFailingSourceIsAskedAtMostFailureRequestsPerWindow(t *testing.T) {
	tests := []struct {
		name      string
		p         countedProvider
		wantCands int
		wantErr   string
	}{
		{name: "fetch fails", p: &fakeProvider{name: "upc", fetchErr: errUnavailable}, wantErr: "503"},
		{name: "fetch answers in part", p: &partialProvider{fakeProvider{name: "movie", confidence: 0.9}}, wantCands: 1, wantErr: "allegro refused"},
		{name: "answer is unreadable", p: &fakeProvider{name: "bn", parseErr: errors.New("bad marc")}, wantErr: "bad marc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, cache := serviceWith(t, tt.p)
			ctx := context.Background()
			lookups := []func(context.Context, string) lookup.Result{svc.Lookup, svc.Recheck}
			for i := range 10 {
				res := lookups[i%2](ctx, recheckCode)
				if len(res.Candidates) != tt.wantCands || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error, tt.wantErr) {
					t.Fatalf("lookup %d = %+v, want %d candidates and the %q failure", i, res, tt.wantCands, tt.wantErr)
				}
				cache.advance(30 * time.Second)
			}
			if got := tt.p.fetchCount(); got != lookup.FailureRequests {
				t.Fatalf("ten lookups of a failing source made %d fetches, want %d", got, lookup.FailureRequests)
			}

			cache.advance(lookup.FailureWindow)
			lookups[0](ctx, recheckCode)
			if got := tt.p.fetchCount(); got != lookup.FailureRequests+1 {
				t.Fatalf("a lookup after the window made %d fetches in all, want %d", got, lookup.FailureRequests+1)
			}
		})
	}
}

func TestFailureLimitIsPerProviderAndCode(t *testing.T) {
	down := &fakeProvider{name: "upc", fetchErr: errUnavailable}
	up := &fakeProvider{name: "discogs", confidence: 0.9}
	r := &lookup.Registry{}
	for _, p := range []lookup.Provider{down, up} {
		if err := r.Register(p, weekTTL); err != nil {
			t.Fatal(err)
		}
	}
	svc := lookup.NewService(r, newClockCache(), discard())
	ctx := context.Background()
	for range lookup.FailureRequests + 2 {
		svc.Lookup(ctx, recheckCode)
	}
	const other = "5906190326195"
	res := svc.Lookup(ctx, other)
	if len(res.Candidates) != 1 || len(res.Errors) != 1 {
		t.Fatalf("lookup of another code = %+v", res)
	}
	if down.fetchCount() != lookup.FailureRequests+1 || up.fetchCount() != 2 {
		t.Fatalf("fetches down=%d up=%d, want %d and 2", down.fetchCount(), up.fetchCount(), lookup.FailureRequests+1)
	}
}

// TestRecoveredSourceAnswersAndClearsItsFailures lets the recovered
// answer expire within the window: the success cleared the failures, so
// the source gets its full count again.
func TestRecoveredSourceAnswersAndClearsItsFailures(t *testing.T) {
	p := &flakyProvider{fakeProvider: fakeProvider{name: "upc", confidence: 0.9}}
	r := &lookup.Registry{}
	if err := r.Register(p, time.Minute); err != nil {
		t.Fatal(err)
	}
	cache := newClockCache()
	svc := lookup.NewService(r, cache, discard())
	lookup.SetClock(svc, cache.clock)
	ctx := context.Background()

	p.down.Store(true)
	for range lookup.FailureRequests - 1 {
		if res := svc.Lookup(ctx, recheckCode); len(res.Errors) != 1 {
			t.Fatalf("lookup while down = %+v", res)
		}
	}
	p.down.Store(false)
	if res := svc.Lookup(ctx, recheckCode); len(res.Candidates) != 1 || len(res.Errors) != 0 {
		t.Fatalf("lookup after recovery = %+v, want the fresh answer", res)
	}

	cache.advance(2 * time.Minute)
	p.down.Store(true)
	for range lookup.FailureRequests + 1 {
		svc.Lookup(ctx, recheckCode)
	}
	if got := p.fetchCount(); got != 2*lookup.FailureRequests {
		t.Fatalf("fetches = %d, want %d", got, 2*lookup.FailureRequests)
	}
}

// blockedProvider fails every Fetch once release closes, reporting each on
// started first.
type blockedProvider struct {
	fakeProvider
	started chan struct{}
	release chan struct{}
}

func (p *blockedProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	if _, err := p.fakeProvider.Fetch(ctx, code); err != nil {
		return nil, err
	}
	p.started <- struct{}{}
	<-p.release
	return nil, errUnavailable
}

func TestFailureLimitCountsRequestsInFlight(t *testing.T) {
	p := &blockedProvider{
		fakeProvider: fakeProvider{name: "upc"},
		started:      make(chan struct{}, lookup.FailureRequests+1),
		release:      make(chan struct{}),
	}
	svc, _ := serviceWith(t, p)
	ctx := context.Background()

	var wg sync.WaitGroup
	for range lookup.FailureRequests {
		wg.Go(func() { svc.Lookup(ctx, recheckCode) })
	}
	for range lookup.FailureRequests {
		<-p.started
	}
	res := svc.Lookup(ctx, recheckCode)
	close(p.release)
	wg.Wait()
	if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error, "pending") {
		t.Fatalf("lookup while %d requests are in flight = %+v, want a pending error", lookup.FailureRequests, res)
	}
	if got := p.fetchCount(); got != lookup.FailureRequests {
		t.Fatalf("fetches = %d, want %d", got, lookup.FailureRequests)
	}
	if res := svc.Lookup(ctx, recheckCode); len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error, "503") {
		t.Fatalf("lookup after the requests failed = %+v, want their failure", res)
	}
}

// canceledProvider fails with its context's error, as an HTTP request does
// when the client gives up on the lookup after sending it. Fetch with
// cancel set cancels the lookup itself once the request went out.
type canceledProvider struct {
	fakeProvider
	cancel context.CancelFunc
}

func (p *canceledProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw, err := p.fakeProvider.Fetch(ctx, code)
	if p.cancel != nil {
		p.cancel()
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return raw, err
}

func TestLookupCanceledBeforeItsRequestAsksNothing(t *testing.T) {
	p := &canceledProvider{fakeProvider: fakeProvider{name: "upc", confidence: 0.9}}
	svc, _ := serviceWith(t, p)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for range lookup.FailureRequests + 1 {
		if res := svc.Lookup(canceled, recheckCode); len(res.Errors) != 1 {
			t.Fatalf("canceled lookup = %+v, want its error", res)
		}
	}
	if got := p.fetchCount(); got != 0 {
		t.Fatalf("canceled lookups made %d fetches, want 0", got)
	}
	if res := svc.Lookup(context.Background(), recheckCode); len(res.Candidates) != 1 || len(res.Errors) != 0 {
		t.Fatalf("lookup after canceled ones = %+v, want the source's answer", res)
	}
}

// TestCanceledLookupsBlockNothingOnTheirOwn cancels lookups while their
// requests wait, as one queued behind a source's pacing does: they count,
// but with no failure known a lookup past the limit still asks.
func TestCanceledLookupsBlockNothingOnTheirOwn(t *testing.T) {
	p := &canceledProvider{fakeProvider: fakeProvider{name: "discogs", confidence: 0.9}}
	svc, _ := serviceWith(t, p)
	for i := range lookup.FailureRequests {
		ctx, cancel := context.WithCancel(context.Background())
		p.cancel = cancel
		if res := svc.Lookup(ctx, recheckCode); len(res.Errors) != 1 {
			t.Fatalf("lookup %d canceled in flight = %+v, want its error", i, res)
		}
	}
	p.cancel = nil
	if res := svc.Lookup(context.Background(), recheckCode); len(res.Candidates) != 1 || len(res.Errors) != 0 {
		t.Fatalf("lookup after canceled ones = %+v, want the source's answer", res)
	}
	if got := p.fetchCount(); got != lookup.FailureRequests+1 {
		t.Fatalf("fetches = %d, want %d", got, lookup.FailureRequests+1)
	}
}

// failingCanceledProvider answers 503 to a lookup that its client then
// gave up on, or, while canceledOnly is set, fails with the cancellation.
type failingCanceledProvider struct {
	fakeProvider
	cancel       context.CancelFunc
	canceledOnly bool
}

func (p *failingCanceledProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	if _, err := p.fakeProvider.Fetch(ctx, code); err != nil {
		return nil, err
	}
	p.cancel()
	if p.canceledOnly {
		return nil, ctx.Err()
	}
	return nil, errUnavailable
}

func TestFailureAnsweredToCanceledLookupsCounts(t *testing.T) {
	tests := []struct {
		name     string
		canceled int
	}{
		{name: "every failure answered after the client gave up", canceled: 0},
		{name: "two canceled requests, then a failure", canceled: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &failingCanceledProvider{fakeProvider: fakeProvider{name: "upc"}}
			svc, _ := serviceWith(t, p)
			for i := range lookup.FailureRequests {
				ctx, cancel := context.WithCancel(context.Background())
				p.cancel = cancel
				p.canceledOnly = i < tt.canceled
				svc.Lookup(ctx, recheckCode)
			}
			p.cancel = func() {}
			res := svc.Lookup(context.Background(), recheckCode)
			if got := p.fetchCount(); got != lookup.FailureRequests {
				t.Fatalf("fetches = %d, want %d", got, lookup.FailureRequests)
			}
			if len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Error, "503") {
				t.Fatalf("lookup past the limit = %+v, want the 503", res)
			}
		})
	}
}

// TestFailureLimitHoldsOverAnyWindow spreads requests over a window
// boundary: each one frees its slot only a whole window after it was made.
func TestFailureLimitHoldsOverAnyWindow(t *testing.T) {
	p := &fakeProvider{name: "upc", fetchErr: errUnavailable}
	svc, cache := serviceWith(t, p)
	ctx := context.Background()
	svc.Lookup(ctx, recheckCode)
	cache.advance(lookup.FailureWindow - time.Second)
	for range 2 {
		svc.Lookup(ctx, recheckCode)
	}
	cache.advance(time.Second)
	for range 3 {
		svc.Lookup(ctx, recheckCode)
	}
	if got := p.fetchCount(); got != lookup.FailureRequests+1 {
		t.Fatalf("fetches = %d, want %d: one slot freed at the boundary", got, lookup.FailureRequests+1)
	}
}

func TestUncachedProviderHasNoFailureLimit(t *testing.T) {
	p := &fakeProvider{name: "stub", fetchErr: errUnavailable}
	r := &lookup.Registry{}
	if err := r.Register(p, 0); err != nil {
		t.Fatal(err)
	}
	svc := lookup.NewService(r, newClockCache(), discard())
	for range lookup.FailureRequests + 2 {
		svc.Lookup(context.Background(), recheckCode)
	}
	if got := p.fetchCount(); got != lookup.FailureRequests+2 {
		t.Fatalf("fetches = %d, want %d", got, lookup.FailureRequests+2)
	}
}

// answerProvider answers every Fetch with the next of errs, the last one
// once they run out, after counting it.
type answerProvider struct {
	fakeProvider
	errs []func() error
}

func (p *answerProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	if _, err := p.fakeProvider.Fetch(ctx, code); err != nil {
		return nil, err
	}
	n := min(p.fetchCount(), len(p.errs)) - 1
	return nil, p.errs[n]()
}

func TestPausedSourceIsAskedAgainAfterItsReset(t *testing.T) {
	const reset = time.Minute
	var cache *clockCache
	pause := func() error {
		until := cache.clock().Add(reset)
		return &lookup.PausedError{
			Err:     errors.New("upcitemdb: rate limit exceeded"),
			Resumed: func() bool { return !cache.clock().Before(until) },
		}
	}
	outage := func() error { return errUnavailable }
	partial := func() error {
		return &lookup.PartialError{Raw: json.RawMessage(missAnswer), Err: errors.Join(errUnavailable, pause())}
	}
	tests := []struct {
		name string
		errs []func() error
	}{
		{name: "paused from the first request", errs: []func() error{pause}},
		{name: "outages, then a rate limit", errs: []func() error{outage, outage, pause}},
		{name: "partial answer with a pause and an outage", errs: []func() error{partial}},
		{name: "a rate limit, then outages that were in flight", errs: []func() error{pause, outage}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &answerProvider{fakeProvider: fakeProvider{name: "movie"}, errs: tt.errs}
			var svc *lookup.Service
			svc, cache = serviceWith(t, p)
			ctx := context.Background()
			for range lookup.FailureRequests + 2 {
				if res := svc.Recheck(ctx, recheckCode); len(res.Errors) != 1 {
					t.Fatalf("lookup = %+v, want the failure", res)
				}
			}
			if got := p.fetchCount(); got != lookup.FailureRequests {
				t.Fatalf("fetches before the reset = %d, want %d", got, lookup.FailureRequests)
			}
			cache.advance(reset)
			for range 3 {
				svc.Lookup(ctx, recheckCode)
			}
			if got := p.fetchCount(); got != lookup.FailureRequests+1 {
				t.Fatalf("fetches after the reset = %d, want one more than %d", got, lookup.FailureRequests)
			}
		})
	}
}

func TestPauseEndedWhenItFailedLetsNothingPastTheLimit(t *testing.T) {
	p := &answerProvider{fakeProvider: fakeProvider{name: "movie"}, errs: []func() error{func() error {
		return &lookup.PausedError{Err: errors.New("upcitemdb: rate limit exceeded"), Resumed: func() bool { return true }}
	}}}
	svc, _ := serviceWith(t, p)
	for range lookup.FailureRequests + 3 {
		svc.Lookup(context.Background(), recheckCode)
	}
	if got := p.fetchCount(); got != lookup.FailureRequests {
		t.Fatalf("fetches = %d, want %d", got, lookup.FailureRequests)
	}
}
