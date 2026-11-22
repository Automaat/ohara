package wantlist

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// Discogs is the part of the Discogs API a sync uses; *discogs.Client
// implements it.
type Discogs interface {
	Identity(ctx context.Context) (string, error)
	Wants(ctx context.Context, username string) ([]int64, error)
	AddWant(ctx context.Context, username string, id int64) error
	RemoveWant(ctx context.Context, username string, id int64) error
}

// Result is what one sync did. Added counts the wished releases it put on
// the wantlist, Already those that were there, Removed the wants Ohara had
// added whose wish is gone, and Skipped the wished album editions without
// a Discogs release. Failure is why the sync stopped early; the counts
// then hold what it did before.
type Result struct {
	Added   int    `json:"added"`
	Already int    `json:"already"`
	Removed int    `json:"removed"`
	Skipped int    `json:"skipped"`
	Failure string `json:"failure,omitempty"`
}

// Status is the GET /api/discogs/wantlist body. Enabled says a Discogs
// token is set; the last sync's times and result live until the process
// restarts.
type Status struct {
	Enabled        bool       `json:"enabled"`
	Running        bool       `json:"running"`
	LastStartedAt  *time.Time `json:"last_started_at"`
	LastFinishedAt *time.Time `json:"last_finished_at"`
	LastResult     *Result    `json:"last_result"`
}

// Syncer runs one sync at a time, in the background: a sync waits for the
// Discogs rate budget between requests, which can outlast a request.
type Syncer struct {
	store   *Store
	discogs Discogs
	logger  *slog.Logger

	running atomic.Bool
	wg      sync.WaitGroup
	base    context.Context

	mu     sync.Mutex
	status Status
}

// NewSyncer wires the store and the Discogs client; a nil client leaves
// the sync disabled.
func NewSyncer(store *Store, discogs Discogs, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		store: store, discogs: discogs, logger: logger, base: context.Background(),
		status: Status{Enabled: discogs != nil},
	}
}

// Start sets the context every later sync runs in; canceling it stops a
// running sync. Call it before the first Trigger.
func (s *Syncer) Start(ctx context.Context) { s.base = ctx }

// Wait blocks until a running sync ends.
func (s *Syncer) Wait() { s.wg.Wait() }

// Enabled says a Discogs client is set.
func (s *Syncer) Enabled() bool { return s.discogs != nil }

// Trigger starts a sync unless one is running or the sync is disabled,
// and says whether it did.
func (s *Syncer) Trigger() bool {
	if s.discogs == nil || !s.running.CompareAndSwap(false, true) {
		return false
	}
	started := time.Now()
	s.mu.Lock()
	s.status.LastStartedAt = &started
	s.mu.Unlock()
	s.wg.Go(func() { s.execute(s.base) })
	return true
}

// Status returns a copy of the current state.
func (s *Syncer) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Running = s.running.Load()
	return st
}

func (s *Syncer) execute(ctx context.Context) {
	defer s.running.Store(false)
	res, err := recovery.Call(s.logger, "discogs wantlist sync panicked", func() (Result, error) {
		return s.Sync(ctx)
	})
	if err != nil {
		res.Failure = err.Error()
		s.logger.Error("discogs wantlist sync failed", "err", err,
			"added", res.Added, "removed", res.Removed)
	} else {
		s.logger.Info("discogs wantlist sync finished", "added", res.Added, "already", res.Already,
			"removed", res.Removed, "skipped", res.Skipped)
	}
	finished := time.Now()
	s.mu.Lock()
	s.status.LastFinishedAt = &finished
	s.status.LastResult = &res
	s.mu.Unlock()
}

// errDisabled is a Sync without a Discogs client.
var errDisabled = errors.New("discogs wantlist sync disabled: no Discogs token")

// Sync reads the whole wantlist of the token's owner, then puts every
// wished release that is not on it there and takes off every want Ohara
// added whose wish is gone. A want on the wantlist that Ohara did not add
// is never removed. Each write is recorded as soon as it succeeds; the
// first failure stops the sync with the counts so far, and the next sync
// retries what is left. Nothing is written when the wantlist already
// matches.
func (s *Syncer) Sync(ctx context.Context) (Result, error) {
	var res Result
	if s.discogs == nil {
		return res, errDisabled
	}
	wished, err := s.store.Wished(ctx)
	if err != nil {
		return res, err
	}
	res.Skipped = wished.Skipped
	username, err := s.discogs.Identity(ctx)
	if err != nil {
		return res, err
	}
	onList, err := s.discogs.Wants(ctx, username)
	if err != nil {
		return res, err
	}
	added, err := s.store.Added(ctx, username)
	if err != nil {
		return res, err
	}
	on := set(onList)
	wanted := set(wished.Releases)

	for _, id := range wished.Releases {
		if on[id] {
			res.Already++
			continue
		}
		if err := s.discogs.AddWant(ctx, username, id); err != nil {
			return res, err
		}
		if err := s.store.RecordAdded(ctx, username, id); err != nil {
			return res, err
		}
		res.Added++
	}
	for _, id := range added {
		if wanted[id] {
			continue
		}
		if on[id] {
			if err := s.discogs.RemoveWant(ctx, username, id); err != nil {
				return res, err
			}
			res.Removed++
		}
		if err := s.store.Forget(ctx, username, id); err != nil {
			return res, err
		}
	}
	return res, nil
}

func set(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
