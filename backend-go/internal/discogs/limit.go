package discogs

import (
	"context"
	"sync"
	"time"
)

// Discogs allows a token 60 authenticated requests in a moving 60-second
// window. The limiter lets limitBurst requests through at once and then
// one every limitInterval, so no 60 seconds hold more than 55: a margin
// for Discogs measuring its window when the requests arrive.
const (
	limitBurst    = 5
	limitInterval = 1200 * time.Millisecond
)

// Limiter is the request budget of one Discogs token, shared by every
// Client that sends that token. Callers wait their turn in arrival order,
// so a busy caller, such as a valuation run, delays another by a request or
// two, never by a whole window. A nil Limiter lets every request through.
//
// It is the generic cell rate algorithm: next is when the budget would be
// full again given every request already admitted, and a request's turn
// comes burst-1 intervals before it.
type Limiter struct {
	burst    int
	interval time.Duration

	mu   sync.Mutex
	next time.Time
}

// NewLimiter returns the budget of one token: 60 requests a minute with a
// margin.
func NewLimiter() *Limiter {
	return &Limiter{burst: limitBurst, interval: limitInterval}
}

// Wait blocks until the budget allows one more request and spends it.
// When ctx ends first, or its deadline comes before the request's turn,
// Wait returns ctx's error at once and spends nothing, so a caller with
// little time left falls back to another source instead of waiting.
func (l *Limiter) Wait(ctx context.Context) error {
	if l == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	now := time.Now()
	l.mu.Lock()
	prev := l.next
	base := l.next
	if base.Before(now) {
		base = now
	}
	turn := base.Add(-time.Duration(l.burst-1) * l.interval)
	l.next = base.Add(l.interval)
	mine := l.next
	l.mu.Unlock()
	if !turn.After(now) {
		return nil
	}
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(turn) {
		l.release(prev, mine)
		return context.DeadlineExceeded
	}
	t := time.NewTimer(turn.Sub(now))
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		l.release(prev, mine)
		return ctx.Err()
	}
}

// release gives back a turn nobody used. Only the latest turn can go back:
// an earlier one would let a new request take a later caller's place.
func (l *Limiter) release(prev, mine time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.next.Equal(mine) {
		l.next = prev
	}
}
