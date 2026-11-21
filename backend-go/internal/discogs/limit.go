package discogs

import (
	"context"
	"slices"
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

// Priority orders the requests waiting for the budget. Interactive
// requests answer someone waiting, such as a scan or a title search;
// Background requests, such as a valuation run's, go after every waiting
// interactive one.
type Priority int

// The priorities, most urgent first.
const (
	Interactive Priority = iota
	Background
)

// Limiter is the request budget of one Discogs token, shared by every
// Client that sends that token. Waiting requests are served one at a time:
// interactive before background, each in arrival order, so a scan waits at
// most one interval behind a busy valuation run. A request that gives up
// leaves the queue at once and holds no place in it. A nil Limiter lets
// every request through.
//
// It is the generic cell rate algorithm: next is when the budget would be
// full again given every request already sent, and the next request's turn
// comes burst-1 intervals before it.
type Limiter struct {
	burst    int
	interval time.Duration

	mu      sync.Mutex
	next    time.Time
	waiting [2][]*waiter
}

// waiter is one request waiting for the budget; wake tells it that it may
// have become the first in line.
type waiter struct {
	priority Priority
	wake     chan struct{}
}

// NewLimiter returns the budget of one token: 60 requests a minute with a
// margin.
func NewLimiter() *Limiter {
	return &Limiter{burst: limitBurst, interval: limitInterval}
}

// Wait blocks until the budget allows one more request and spends it.
// When ctx ends first, or its deadline comes before the request's turn as
// the first in line, Wait returns ctx's error at once and spends nothing,
// so a caller with little time left falls back to another source instead
// of waiting.
func (l *Limiter) Wait(ctx context.Context, p Priority) error {
	if l == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p != Background {
		p = Interactive
	}
	w := &waiter{priority: p, wake: make(chan struct{}, 1)}
	l.mu.Lock()
	l.waiting[p] = append(l.waiting[p], w)
	for {
		now := time.Now()
		var timer *time.Timer
		var fire <-chan time.Time
		if l.first() == w {
			turn := l.turn(now)
			if !turn.After(now) {
				l.next = l.full(now).Add(l.interval)
				l.leave(w)
				l.mu.Unlock()
				return nil
			}
			if deadline, ok := ctx.Deadline(); ok && deadline.Before(turn) {
				l.leave(w)
				l.mu.Unlock()
				return context.DeadlineExceeded
			}
			timer = time.NewTimer(turn.Sub(now))
			fire = timer.C
		}
		l.mu.Unlock()
		var err error
		select {
		case <-fire:
		case <-w.wake:
		case <-ctx.Done():
			err = ctx.Err()
		}
		if timer != nil {
			timer.Stop()
		}
		l.mu.Lock()
		if err != nil {
			l.leave(w)
			l.mu.Unlock()
			return err
		}
	}
}

// full is when the budget would be full again, seen at now.
func (l *Limiter) full(now time.Time) time.Time {
	if l.next.Before(now) {
		return now
	}
	return l.next
}

// turn is when the budget next allows a request, seen at now.
func (l *Limiter) turn(now time.Time) time.Time {
	t := l.full(now).Add(-time.Duration(l.burst-1) * l.interval)
	if t.Before(now) {
		return now
	}
	return t
}

// first is the request served next: the oldest interactive one, else the
// oldest background one.
func (l *Limiter) first() *waiter {
	for _, q := range l.waiting {
		if len(q) > 0 {
			return q[0]
		}
	}
	return nil
}

// leave takes w out of the queue and wakes the request first in line after
// it, which may now be served or must time its own turn.
func (l *Limiter) leave(w *waiter) {
	l.waiting[w.priority] = slices.DeleteFunc(l.waiting[w.priority], func(o *waiter) bool { return o == w })
	if f := l.first(); f != nil {
		select {
		case f.wake <- struct{}{}:
		default:
		}
	}
}
