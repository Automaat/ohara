package lookup_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
// when the client gives up on the lookup.
type canceledProvider struct{ fakeProvider }

func (p *canceledProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	raw, err := p.fakeProvider.Fetch(ctx, code)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return raw, err
}

func TestCanceledLookupsDoNotCountAsFailures(t *testing.T) {
	p := &canceledProvider{fakeProvider{name: "upc", confidence: 0.9}}
	svc, _ := serviceWith(t, p)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for range lookup.FailureRequests + 1 {
		svc.Lookup(canceled, recheckCode)
	}
	if res := svc.Lookup(context.Background(), recheckCode); len(res.Candidates) != 1 || len(res.Errors) != 0 {
		t.Fatalf("lookup after canceled ones = %+v, want the source's answer", res)
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

// answerProvider answers every Fetch with err, after counting it.
type answerProvider struct {
	fakeProvider
	err error
}

func (p *answerProvider) Fetch(ctx context.Context, code string) (json.RawMessage, error) {
	if _, err := p.fakeProvider.Fetch(ctx, code); err != nil {
		return nil, err
	}
	return nil, p.err
}

func TestPausesDoNotCountAsFailures(t *testing.T) {
	pause := &lookup.PausedError{Err: errors.New("upcitemdb: rate limit exceeded, skipped until 2026-11-21T13:00:00Z")}
	tests := []struct {
		name        string
		err         error
		wantFetches int
	}{
		{name: "pause", err: fmt.Errorf("upcitemdb: %w", pause), wantFetches: lookup.FailureRequests + 2},
		{name: "partial answer while paused", err: &lookup.PartialError{Raw: json.RawMessage(missAnswer), Err: errors.Join(pause)}, wantFetches: lookup.FailureRequests + 2},
		{name: "pause and an outage", err: errors.Join(pause, errUnavailable), wantFetches: lookup.FailureRequests},
		{name: "partial answer while paused and down", err: &lookup.PartialError{Raw: json.RawMessage(missAnswer), Err: errors.Join(pause, errUnavailable)}, wantFetches: lookup.FailureRequests},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &answerProvider{fakeProvider: fakeProvider{name: "movie"}, err: tt.err}
			svc, _ := serviceWith(t, p)
			for range lookup.FailureRequests + 2 {
				if res := svc.Lookup(context.Background(), recheckCode); len(res.Errors) != 1 {
					t.Fatalf("lookup = %+v, want the failure", res)
				}
			}
			if got := p.fetchCount(); got != tt.wantFetches {
				t.Fatalf("fetches = %d, want %d", got, tt.wantFetches)
			}
		})
	}
}
