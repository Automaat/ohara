package movie

import (
	"context"
	"sync"
	"testing"
	"time"
)

// delayedSource names its products after delay.
type delayedSource struct {
	products []Product
	delay    time.Duration
}

func (delayedSource) Name() string { return "slow" }

func (s delayedSource) Products(ctx context.Context, _ string) ([]Product, error) {
	select {
	case <-time.After(s.delay):
		return s.products, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// countingSource counts its calls.
type countingSource struct {
	mu    *sync.Mutex
	calls *int
}

func (countingSource) Name() string { return "fallback" }

func (s countingSource) Products(context.Context, string) ([]Product, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	*s.calls++
	return []Product{}, nil
}

func TestFallbackWaitsForASlowSource(t *testing.T) {
	tests := []struct {
		name     string
		products []Product
		calls    int
	}{
		{name: "slow hit", products: []Product{{Name: "Kursk [Blu-ray]"}}, calls: 0},
		{name: "slow miss", calls: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := countingSource{mu: &sync.Mutex{}, calls: new(int)}
			p := &Provider{
				Sources:   []NameSource{delayedSource{products: tc.products, delay: 200 * time.Millisecond}},
				Fallbacks: []NameSource{fb},
				TMDB:      &fakeSearcher{},
			}
			if _, err := p.Fetch(context.Background(), "5906190326195"); err != nil {
				t.Fatal(err)
			}
			if *fb.calls != tc.calls {
				t.Fatalf("fallback calls = %d, want %d", *fb.calls, tc.calls)
			}
		})
	}
}
