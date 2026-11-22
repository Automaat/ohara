package movie

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// slowLoadStore is a memStore whose Load takes delay, or hangs until its
// context ends when delay is zero.
type slowLoadStore struct {
	*memStore
	delay   time.Duration
	loads   atomic.Int32
	started chan struct{}
}

func newSlowLoadStore(m *memStore, delay time.Duration) *slowLoadStore {
	return &slowLoadStore{memStore: m, delay: delay, started: make(chan struct{}, 8)}
}

func (s *slowLoadStore) Load(ctx context.Context, provider string) (Token, bool, error) {
	s.loads.Add(1)
	s.started <- struct{}{}
	var wait <-chan time.Time
	if s.delay > 0 {
		wait = time.After(s.delay)
	}
	select {
	case <-wait:
	case <-ctx.Done():
		return Token{}, false, ctx.Err()
	}
	return s.memStore.Load(ctx, provider)
}

func coldAllegro(t *testing.T, delay time.Duration) (*Allegro, *slowLoadStore) {
	t.Helper()
	now := time.Now()
	mem := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(time.Hour)}}
	store := newSlowLoadStore(mem, delay)
	a := newAllegro(t, &allegroServer{}, mem, now)
	a.Store = store
	return a, store
}

// lookupByDeadline runs a lookup with a 20 ms context and checks it ends
// with the deadline, within 100 ms after it.
func lookupByDeadline(t *testing.T, a *Allegro) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := a.Products(ctx, "5906190326195"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if deadline, _ := ctx.Deadline(); time.Since(deadline) > 100*time.Millisecond {
		t.Fatalf("lookup returned %v after its deadline", time.Since(deadline))
	}
}

func TestAllegroWaitForAColdLoadStopsAtTheDeadline(t *testing.T) {
	a, store := coldAllegro(t, 300*time.Millisecond)
	first := make(chan error, 1)
	go func() {
		_, err := a.Products(context.Background(), "5906190326195")
		first <- err
	}()
	<-store.started
	lookupByDeadline(t, a)
	if err := <-first; err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if n := store.loads.Load(); n != 1 {
		t.Fatalf("loads = %d", n)
	}
}

func TestAllegroKeepsALoadTheLookupGaveUpOn(t *testing.T) {
	a, store := coldAllegro(t, 300*time.Millisecond)
	lookupByDeadline(t, a)
	eventually(t, func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return a.token != nil
	})
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil {
		t.Fatal(err)
	}
	if n := store.loads.Load(); n != 1 {
		t.Fatalf("loads = %d", n)
	}
}

func TestAllegroBoundsAHangingLoad(t *testing.T) {
	a, store := coldAllegro(t, 0)
	a.tokenTimeout = 100 * time.Millisecond
	for range 2 {
		start := time.Now()
		if _, err := a.Products(context.Background(), "5906190326195"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("err = %v", err)
		}
		if waited := time.Since(start); waited > time.Second {
			t.Fatalf("lookup waited %v", waited)
		}
	}
	if n := store.loads.Load(); n != 2 {
		t.Fatalf("loads = %d, want a new one after the first timed out", n)
	}
}
