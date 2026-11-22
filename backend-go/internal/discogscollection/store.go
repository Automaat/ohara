// Package discogscollection adds an owned album copy to the Discogs
// collection of the OHARA_DISCOGS_TOKEN owner, with its Goldmine grades,
// and keeps the instance it made so that a later push updates that one.
// It only pushes: nothing is read back from Discogs into Ohara, and no
// instance Ohara did not make is edited or removed.
package discogscollection

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is a copy that does not exist.
var ErrNotFound = errors.New("not found")

// Store reads and records the Discogs instances of copies.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Target is what a push needs of a copy: its status, its title's kind,
// its edition's Discogs release, its grades, the instance it was pushed
// to with that instance's release and whether its conditions were set,
// and an add whose answer never came: the release and when it was sent.
type Target struct {
	CopyID            int64
	Status            string
	Kind              string
	ReleaseID         *int64
	MediaGrade        *string
	SleeveGrade       *string
	InstanceID        *int64
	InstanceReleaseID *int64
	Synced            bool
	AddReleaseID      *int64
	AddSince          *time.Time
}

// Instance is the instance the copy made of its edition's current
// release, nil when it made none or made it of another release.
func (t *Target) Instance() *int64 {
	if t.InstanceID == nil || t.ReleaseID == nil || t.InstanceReleaseID == nil || *t.InstanceReleaseID != *t.ReleaseID {
		return nil
	}
	return t.InstanceID
}

const targetQuery = `SELECT c.status, ti.kind, e.discogs_release_id, c.media_grade, c.sleeve_grade,
		c.discogs_instance_id, c.discogs_instance_release_id, c.discogs_instance_synced,
		c.discogs_add_release_id, c.discogs_add_since
	FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title ti ON ti.id = e.title_id
	WHERE c.id = $1`

func scanTarget(row pgx.Row, id int64) (Target, error) {
	t := Target{CopyID: id}
	err := row.Scan(&t.Status, &t.Kind, &t.ReleaseID, &t.MediaGrade, &t.SleeveGrade,
		&t.InstanceID, &t.InstanceReleaseID, &t.Synced, &t.AddReleaseID, &t.AddSince)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, fmt.Errorf("discogs target: %w", err)
	}
	return t, nil
}

// Target returns the push target of copy id, or ErrNotFound.
func (s *Store) Target(ctx context.Context, id int64) (Target, error) {
	return scanTarget(s.pool.QueryRow(ctx, targetQuery, id), id)
}

// Locked is a copy held for one push: no other push, update or delete of
// it runs until Commit or Rollback, so a copy deleted mid-push cannot
// leave an instance on Discogs that Ohara no longer knows of.
type Locked struct {
	tx pgx.Tx
	id int64
}

// Lock begins a push of copy id and returns its target; ErrNotFound when
// the copy does not exist.
func (s *Store) Lock(ctx context.Context, id int64) (*Locked, Target, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, Target{}, fmt.Errorf("discogs lock: %w", err)
	}
	t, err := scanTarget(tx.QueryRow(ctx, targetQuery+` FOR NO KEY UPDATE OF c`, id), id)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, Target{}, err
	}
	return &Locked{tx: tx, id: id}, t, nil
}

func (l *Locked) exec(ctx context.Context, op, sql string, args ...any) error {
	if _, err := l.tx.Exec(ctx, sql, append([]any{l.id}, args...)...); err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return nil
}

// Link records that the copy made instance of release, its conditions not
// yet set, and drops any pending add.
func (l *Locked) Link(ctx context.Context, release, instance int64) error {
	return l.exec(ctx, "link discogs instance", `UPDATE copy SET discogs_instance_id = $2,
		discogs_instance_release_id = $3, discogs_instance_synced = false,
		discogs_add_release_id = NULL, discogs_add_since = NULL WHERE id = $1`, instance, release)
}

// Synced records that the linked instance's conditions are set.
func (l *Locked) Synced(ctx context.Context) error {
	return l.exec(ctx, "sync discogs instance", `UPDATE copy SET discogs_instance_synced = true WHERE id = $1`)
}

// Forget drops the link: Discogs no longer has that instance.
func (l *Locked) Forget(ctx context.Context) error {
	return l.exec(ctx, "forget discogs instance", `UPDATE copy SET discogs_instance_id = NULL,
		discogs_instance_release_id = NULL, discogs_instance_synced = false WHERE id = $1`)
}

// MarkAdd records that an add of release is about to be sent and returns
// when; an instance of it added since then and alone is the copy's.
func (l *Locked) MarkAdd(ctx context.Context, release int64) (time.Time, error) {
	var since time.Time
	err := l.tx.QueryRow(ctx, `UPDATE copy SET discogs_add_release_id = $2, discogs_add_since = now()
		WHERE id = $1 RETURNING discogs_add_since`, l.id, release).Scan(&since)
	if err != nil {
		return time.Time{}, fmt.Errorf("mark discogs add: %w", err)
	}
	return since, nil
}

// ClearAdd drops a pending add.
func (l *Locked) ClearAdd(ctx context.Context) error {
	return l.exec(ctx, "clear discogs add", `UPDATE copy SET discogs_add_release_id = NULL,
		discogs_add_since = NULL WHERE id = $1`)
}

// Commit keeps what the push recorded and lets the copy go.
func (l *Locked) Commit(ctx context.Context) error {
	if err := l.tx.Commit(ctx); err != nil {
		return fmt.Errorf("discogs push commit: %w", err)
	}
	return nil
}

// Rollback lets the copy go without what the push recorded; after Commit
// it does nothing.
func (l *Locked) Rollback(ctx context.Context) {
	_ = l.tx.Rollback(ctx)
}
