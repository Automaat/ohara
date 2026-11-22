package deals

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// blockingJob is a task job that counts its runs, reports each start on
// started and blocks every run until release is closed.
type blockingJob struct {
	task    *Task[int]
	runs    atomic.Int32
	started chan struct{}
	release chan struct{}
}

func newBlockingJob() *blockingJob {
	j := &blockingJob{started: make(chan struct{}, 4), release: make(chan struct{})}
	j.task = NewTask[int](j.run)
	return j
}

func (j *blockingJob) run(context.Context) {
	n := int(j.runs.Add(1))
	j.task.Started(time.Unix(int64(n), 0))
	j.started <- struct{}{}
	<-j.release
	j.task.Finished(time.Unix(int64(n)+1, 0), &n)
}

func TestTaskTriggerRunsOneAtATime(t *testing.T) {
	j := newBlockingJob()
	if !j.task.Trigger() {
		t.Fatal("first Trigger = false")
	}
	<-j.started
	if j.task.Trigger() {
		t.Fatal("Trigger during a run = true, want false")
	}
	last, running := j.task.State()
	if !running || last.StartedAt == nil || last.FinishedAt != nil || last.Result != nil {
		t.Fatalf("state during the run = %+v, running %t", last, running)
	}
	close(j.release)
	j.task.Wait()
	last, running = j.task.State()
	if running || last.Result == nil || *last.Result != 1 || !last.FinishedAt.Equal(time.Unix(2, 0)) {
		t.Fatalf("state after the run = %+v, running %t", last, running)
	}
	if !j.task.Trigger() {
		t.Fatal("Trigger after the run = false")
	}
	j.task.Wait()
	if got := j.runs.Load(); got != 2 {
		t.Fatalf("runs = %d, want 2", got)
	}
}

func TestTaskManualRunUsesTheBoundContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var got atomic.Value
	task := NewTask[int](func(ctx context.Context) { got.Store(ctx.Err()) })
	task.Bind(ctx)
	cancel()
	task.Trigger()
	task.Wait()
	if err, _ := got.Load().(error); err == nil {
		t.Fatal("manual run got a live context, want the bound one, canceled")
	}
}

func TestTaskScheduleWaitsFirstThenTheInterval(t *testing.T) {
	var runs atomic.Int32
	task := NewTask[int](func(context.Context) { runs.Add(1) })
	var waits []time.Duration
	task.Schedule(t.Context(), 7*time.Minute, time.Hour, func(_ context.Context, d time.Duration) error {
		waits = append(waits, d)
		if len(waits) > 3 {
			return context.Canceled
		}
		return nil
	})
	task.Wait()
	if got := runs.Load(); got != 3 {
		t.Fatalf("scheduled runs = %d, want 3", got)
	}
	if len(waits) != 4 || waits[0] != 7*time.Minute {
		t.Fatalf("waits = %v, want the first one 7m", waits)
	}
	for _, d := range waits[1:] {
		if d < 54*time.Minute || d > 66*time.Minute {
			t.Fatalf("waits = %v, want the interval jittered by 10%%", waits)
		}
	}
}

func TestTaskScheduleSkipsARunningRun(t *testing.T) {
	j := newBlockingJob()
	j.task.Trigger()
	<-j.started
	// The second wait begins after the scheduled run was due.
	var sleeps atomic.Int32
	due := make(chan struct{})
	j.task.Schedule(t.Context(), 0, time.Hour, func(context.Context, time.Duration) error {
		if sleeps.Add(1) == 1 {
			return nil
		}
		close(due)
		return context.Canceled
	})
	<-due
	close(j.release)
	j.task.Wait()
	if got := j.runs.Load(); got != 1 {
		t.Fatalf("runs = %d, want the scheduled one skipped while the manual one ran", got)
	}
}

func TestTaskRestoreKeepsANewerRun(t *testing.T) {
	task := NewTask[int](func(context.Context) {})
	stored, n := time.Unix(10, 0), 5
	task.Restore(TaskRun[int]{StartedAt: &stored, FinishedAt: &stored, Result: &n})
	last, _ := task.State()
	if last.StartedAt == nil || !last.StartedAt.Equal(stored) || *last.Result != 5 {
		t.Fatalf("restored = %+v", last)
	}

	task = NewTask[int](func(context.Context) {})
	task.Started(time.Unix(20, 0))
	task.Restore(TaskRun[int]{StartedAt: &stored, FinishedAt: &stored, Result: &n})
	last, _ = task.State()
	if !last.StartedAt.Equal(time.Unix(20, 0)) || last.Result != nil {
		t.Fatalf("restore over a started run = %+v, want the started run kept", last)
	}
}

func TestTaskWaitWaitsForGo(t *testing.T) {
	task := NewTask[int](func(context.Context) {})
	var done atomic.Bool
	task.Go(func() {
		time.Sleep(10 * time.Millisecond)
		done.Store(true)
	})
	task.Wait()
	if !done.Load() {
		t.Fatal("Wait returned before the Go func ended")
	}
}
