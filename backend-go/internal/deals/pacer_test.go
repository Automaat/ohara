package deals

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestPacerSpacesOneSite(t *testing.T) {
	const gap = 50 * time.Millisecond
	p := NewPacer(gap)
	ctx := context.Background()
	var mu sync.Mutex
	var starts []time.Time
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := p.Wait(ctx, "olx"); err != nil {
				t.Errorf("Wait: %v", err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
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

func TestPacerLeavesOtherSitesAlone(t *testing.T) {
	p := NewPacer(time.Hour)
	ctx := context.Background()
	for _, site := range []string{"olx", "pepper", "skupszop"} {
		start := time.Now()
		if err := p.Wait(ctx, site); err != nil {
			t.Fatalf("Wait(%s): %v", site, err)
		}
		if d := time.Since(start); d > time.Second {
			t.Fatalf("first request to %s waited %v", site, d)
		}
	}
}

func TestPacerWaitStopsWithTheContext(t *testing.T) {
	p := NewPacer(time.Hour)
	if err := p.Wait(context.Background(), "olx"); err != nil {
		t.Fatalf("first Wait: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := p.Wait(ctx, "olx"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Wait = %v, want the context's deadline", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if err := p.Wait(canceled, "olx"); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait on a canceled context = %v, want context.Canceled", err)
	}
}

func TestPacedSourceKeepsTheSource(t *testing.T) {
	src := &fakeSource{name: "olx", perQuery: true, listings: []Listing{{Title: "Diuna DVD"}}}
	paced := NewPacer(time.Hour).Source(src)
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
