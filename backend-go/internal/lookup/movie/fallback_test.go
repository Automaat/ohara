package movie

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// slowSource answers only after release is closed, or fails once ctx ends.
type slowSource struct {
	products []Product
	release  <-chan struct{}
}

func (slowSource) Name() string { return "slow" }

func (s slowSource) Products(ctx context.Context, _ string) ([]Product, error) {
	select {
	case <-s.release:
		return s.products, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// signalSource records its calls and closes asked on the first one.
type signalSource struct {
	products []Product
	err      error
	once     *sync.Once
	asked    chan struct{}
	mu       *sync.Mutex
	calls    *int
}

func newSignalSource(products []Product, err error) signalSource {
	return signalSource{products: products, err: err, once: &sync.Once{}, asked: make(chan struct{}), mu: &sync.Mutex{}, calls: new(int)}
}

func (signalSource) Name() string { return "fallback" }

func (s signalSource) Products(context.Context, string) ([]Product, error) {
	s.mu.Lock()
	*s.calls++
	s.mu.Unlock()
	s.once.Do(func() { close(s.asked) })
	return s.products, s.err
}

func (s signalSource) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.calls
}

func TestFallbackAskedWhileASourceIsSlow(t *testing.T) {
	kursk := []Product{{Name: "Kursk [Blu-ray]"}}
	tests := []struct {
		name         string
		slow         []Product
		fallback     []Product
		fallbackErr  error
		wantProducts int
		wantErr      bool
	}{
		{name: "slow miss, fallback names it", fallback: kursk, wantProducts: 1},
		{name: "slow hit, fallback rate limited", slow: kursk, fallbackErr: ErrQuotaExceeded, wantProducts: 1},
		{name: "slow miss, fallback rate limited", fallbackErr: ErrQuotaExceeded, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := newSignalSource(tc.fallback, tc.fallbackErr)
			p := &Provider{
				Sources:       []NameSource{slowSource{products: tc.slow, release: fb.asked}},
				Fallbacks:     []NameSource{fb},
				TMDB:          &fakeSearcher{},
				fallbackAfter: 10 * time.Millisecond,
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			raw, err := p.Fetch(ctx, "5906190326195")
			if ctx.Err() != nil {
				t.Fatal("the slow source was never released: the fallback was not asked while it waited")
			}
			if fb.count() != 1 {
				t.Fatalf("fallback calls = %d, want 1", fb.count())
			}
			if tc.wantErr {
				if err == nil || !errors.Is(err, ErrQuotaExceeded) {
					t.Fatalf("err = %v, want the fallback's rate limit", err)
				}
				return
			}
			if _, partial := errors.AsType[*lookup.PartialError](err); err != nil || partial {
				t.Fatalf("err = %v, want a complete answer", err)
			}
			var r Raw
			if err := json.Unmarshal(raw, &r); err != nil || len(r.Products) != tc.wantProducts {
				t.Fatalf("raw = %s, %v", raw, err)
			}
		})
	}
}

func TestFallbackNotAskedWhenASourceAnswersInTime(t *testing.T) {
	fb := newSignalSource(nil, nil)
	p := &Provider{
		Sources:       []NameSource{fakeSource{name: "a", products: []Product{{Name: "Kursk [Blu-ray]"}}}},
		Fallbacks:     []NameSource{fb},
		TMDB:          &fakeSearcher{},
		fallbackAfter: time.Hour,
	}
	if _, err := p.Fetch(context.Background(), "5906190326195"); err != nil {
		t.Fatal(err)
	}
	if fb.count() != 0 {
		t.Fatalf("fallback calls = %d, want 0", fb.count())
	}
}
