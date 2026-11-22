package wantlist

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

// Discogs is the part of the Discogs API a sync uses; *discogs.Client
// implements it.
type Discogs interface {
	Identity(ctx context.Context) (string, error)
	Wants(ctx context.Context, username string) ([]discogs.Want, error)
	AddWant(ctx context.Context, username string, id int64) error
	RemoveWant(ctx context.Context, username string, id int64) error
}

// Result is what one sync did. Added counts the wished releases it put on
// the wantlist, Already those that were there, Removed the wants Ohara had
// added whose wish is gone, and Skipped the album wishes whose edition has
// no Discogs release. Failure is why the sync stopped early; the counts
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
func NewSyncer(store *Store, client Discogs, logger *slog.Logger) *Syncer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Syncer{
		store: store, discogs: client, logger: logger, base: context.Background(),
		status: Status{Enabled: client != nil},
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

// clockSkew is how far the date_added Discogs gives a want may lie outside
// Ohara's own times of its add and still identify it: the two clocks
// differ a little. Kept small, since a want the owner re-adds by hand
// within it, before the sync reads the wantlist again, would pass as
// Ohara's.
const clockSkew = 10 * time.Second

// Sync reads the whole wantlist of the token's owner, then puts every
// wished release that is not on it there and takes off the wants Ohara
// added whose wish is gone. Nothing is written when the wantlist already
// matches.
//
// A want counts as Ohara's only while it is provably the one Ohara put
// there. An add is recorded before it is sent and confirmed once Discogs
// accepts it. Right after its adds, the same sync reads the wantlist again
// and keeps each confirmed want's date_added, when it falls between the
// sending and the confirmation (give or take clockSkew); no later sync
// does, so a want the owner removes and re-adds by hand between syncs
// never passes as Ohara's. A want is taken off only when the listed one
// still has that date_added. A want the owner had already, re-added by
// hand, or whose add has no confirmation (canceled, a lost answer, a
// server error) is never removed: its record is forgotten instead. A
// removal is forgotten only once Discogs confirms it, so a failed one is
// retried by the next sync. The first failure stops the sync with the
// counts so far.
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
	wants, err := s.discogs.Wants(ctx, username)
	if err != nil {
		return res, err
	}
	owned, err := s.store.Owned(ctx, username)
	if err != nil {
		return res, err
	}
	on := listing(wants)
	wanted := set(wished.Releases)

	var adds []added
	for _, id := range wished.Releases {
		if _, ok := on[id]; ok {
			res.Already++
			continue
		}
		a, err := s.add(ctx, username, id)
		if err != nil {
			return res, errors.Join(err, s.identify(ctx, username, adds))
		}
		adds = append(adds, a)
		res.Added++
	}
	if err := s.identify(ctx, username, adds); err != nil {
		return res, err
	}
	for _, o := range owned {
		mine := provable(o, on)
		_, onList := on[o.Release]
		switch {
		case wanted[o.Release] && (mine || !onList):
			// Still Ohara's, or just put back above.
		case wanted[o.Release] || !mine:
			if err := s.store.Forget(ctx, username, o.Release); err != nil {
				return res, err
			}
		default:
			if err := s.discogs.RemoveWant(ctx, username, o.Release); err != nil {
				return res, err
			}
			if err := s.store.Forget(ctx, username, o.Release); err != nil {
				return res, err
			}
			res.Removed++
		}
	}
	return res, nil
}

// added is an add Discogs accepted: the release and Ohara's clock around
// the request.
type added struct {
	release    int64
	sent, done time.Time
}

func listing(wants []discogs.Want) map[int64]time.Time {
	on := make(map[int64]time.Time, len(wants))
	for _, w := range wants {
		on[w.ID] = w.DateAdded
	}
	return on
}

// identify reads the wantlist once more and records the date_added of each
// of this sync's adds whose listed want falls within its request. An add
// left unidentified, because this read fails or the date does not fit,
// can never lead to a removal.
func (s *Syncer) identify(ctx context.Context, username string, adds []added) error {
	if len(adds) == 0 {
		return nil
	}
	wants, err := s.discogs.Wants(ctx, username)
	if err != nil {
		return err
	}
	on := listing(wants)
	for _, a := range adds {
		date, listed := on[a.release]
		if !listed || date.IsZero() || date.Before(a.sent.Add(-clockSkew)) || date.After(a.done.Add(clockSkew)) {
			continue
		}
		if err := s.store.Identify(ctx, username, a.release, date); err != nil {
			return err
		}
	}
	return nil
}

// provable says the listed want is the one Ohara added: the record holds
// its date_added.
func provable(o Owned, on map[int64]time.Time) bool {
	date, listed := on[o.Release]
	return listed && o.DiscogsAddedAt != nil && o.DiscogsAddedAt.Equal(date)
}

// add records release id as pending, puts it on the wantlist and confirms
// it once Discogs accepts. A refusal drops the record; an unknown outcome
// leaves it unconfirmed, so it can never lead to a removal.
func (s *Syncer) add(ctx context.Context, username string, id int64) (added, error) {
	a := added{release: id}
	if err := s.store.Pending(ctx, username, id); err != nil {
		return a, err
	}
	a.sent = time.Now()
	if err := s.discogs.AddWant(ctx, username, id); err != nil {
		if refused(err) {
			if ferr := s.store.Forget(ctx, username, id); ferr != nil {
				return a, errors.Join(err, ferr)
			}
		}
		return a, err
	}
	a.done = time.Now()
	return a, s.store.Confirm(ctx, username, id)
}

// refused says Discogs answered with a status that leaves the wantlist as
// it was: a redirect or a client error, 429 included. A server error or a
// lost answer can follow a write Discogs made.
func refused(err error) bool {
	se, ok := errors.AsType[discogs.StatusError](err)
	return ok && se.Code < http.StatusInternalServerError
}

func set(ids []int64) map[int64]bool {
	m := make(map[int64]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return m
}
