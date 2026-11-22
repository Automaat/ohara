package deals

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

// Sleep and Jitter are sleepCtx and jitter for the valuation, which paces
// its requests and schedules its runs as the hunter does.
var (
	Sleep  = sleepCtx
	Jitter = jitter
)

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// jitter spreads d uniformly over d*(1-spread) to d*(1+spread).
func jitter(d time.Duration, spread float64) time.Duration {
	var b [8]byte
	_, _ = rand.Read(b[:])
	u := float64(binary.LittleEndian.Uint64(b[:])>>11) / (1 << 53)
	return time.Duration(float64(d) * (1 - spread + 2*spread*u))
}

// TaskRun is a task's last run: when it started and finished, and what it
// returned. A field is nil until the run reached it.
type TaskRun[R any] struct {
	StartedAt  *time.Time
	FinishedAt *time.Time
	Result     *R
}

// Task runs a background job, a deal hunt or a valuation, one run at a
// time: on a schedule (Schedule) or by hand (Trigger). The job records its
// own start and outcome with Started and Finished, which the job's status
// view reads with State. The zero value has no job yet, which Init sets;
// until then State, Restore and Wait work and nothing runs.
type Task[R any] struct {
	job     func(context.Context)
	running atomic.Bool
	wg      sync.WaitGroup
	base    context.Context

	mu   sync.Mutex
	last TaskRun[R]
}

// Init makes job the task's job. Until Bind, a manual run gets
// context.Background().
func (t *Task[R]) Init(job func(context.Context)) { t.job = job }

// Bind makes ctx the context of every later manual run.
func (t *Task[R]) Bind(ctx context.Context) { t.base = ctx }

// Go runs f in the background; Wait waits for it too.
func (t *Task[R]) Go(f func()) { t.wg.Go(f) }

// Repeat runs f in the background after first, then again after interval
// jittered by ±10%, or after retry jittered by ±10% when f reports false,
// each wait starting when f returned, until sleep fails, which it does once
// ctx ends. Wait waits for it too. f does not hold the task's one run.
func (t *Task[R]) Repeat(ctx context.Context, first, interval, retry time.Duration, sleep func(context.Context, time.Duration) error, f func(context.Context) bool) {
	t.wg.Go(func() {
		for wait := first; sleep(ctx, wait) == nil; {
			wait = jitter(interval, 0.1)
			if !f(ctx) {
				wait = jitter(retry, 0.1)
			}
		}
	})
}

// Schedule runs the job after first and then every interval jittered by
// ±10%, each wait starting when the last run ended, until sleep fails,
// which it does once ctx ends. A scheduled run that finds a manual one
// running is skipped.
func (t *Task[R]) Schedule(ctx context.Context, first, interval time.Duration, sleep func(context.Context, time.Duration) error) {
	t.Repeat(ctx, first, interval, interval, sleep, func(ctx context.Context) bool {
		if t.running.CompareAndSwap(false, true) {
			t.execute(ctx)
		}
		return true
	})
}

// Wait blocks until the schedule, any manual run and everything started
// with Go have stopped.
func (t *Task[R]) Wait() { t.wg.Wait() }

// Trigger starts a run in the background and reports false when one is
// already running.
func (t *Task[R]) Trigger() bool {
	if !t.running.CompareAndSwap(false, true) {
		return false
	}
	ctx := t.base
	if ctx == nil {
		ctx = context.Background()
	}
	t.wg.Go(func() { t.execute(ctx) })
	return true
}

// execute runs the job once; the caller holds running.
func (t *Task[R]) execute(ctx context.Context) {
	defer t.running.Store(false)
	t.job(ctx)
}

// State returns a copy of the last run and whether a run is going on.
func (t *Task[R]) State() (TaskRun[R], bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.last, t.running.Load()
}

// Restore makes last the last run unless a run has started since.
func (t *Task[R]) Restore(last TaskRun[R]) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last.StartedAt == nil {
		t.last = last
	}
}

// Started records the start time of the run going on.
func (t *Task[R]) Started(at time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last.StartedAt = &at
}

// Finished records when the run that started last finished, and its
// result res.
func (t *Task[R]) Finished(at time.Time, res *R) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.last.FinishedAt = &at
	t.last.Result = res
}
