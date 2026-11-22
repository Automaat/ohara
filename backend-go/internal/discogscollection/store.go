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

// Pending is an add of a copy's release that has not finished: sent at
// Since, with the instance Discogs answered with, nil while the add's
// outcome is unknown.
type Pending struct {
	ReleaseID  int64
	Since      time.Time
	InstanceID *int64
}

// Target is what a push needs of a copy: its status, its title's kind,
// its edition's Discogs release, its grades, the instance it is linked
// to with that instance's release, and an add that has not finished.
type Target struct {
	CopyID            int64
	Status            string
	Kind              string
	ReleaseID         *int64
	MediaGrade        *string
	SleeveGrade       *string
	InstanceID        *int64
	InstanceReleaseID *int64
	Pending           *Pending
}

// Instance is the instance the copy is linked to of its edition's current
// release, nil when it is linked to none or to another release's.
func (t *Target) Instance() *int64 {
	if t.InstanceID == nil || t.ReleaseID == nil || t.InstanceReleaseID == nil || *t.InstanceReleaseID != *t.ReleaseID {
		return nil
	}
	return t.InstanceID
}

const targetQuery = `SELECT c.status, ti.kind, e.discogs_release_id, c.media_grade, c.sleeve_grade,
		c.discogs_instance_id, c.discogs_instance_release_id, a.release_id, a.sent_at, a.instance_id
	FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title ti ON ti.id = e.title_id
		LEFT JOIN discogs_add a ON a.copy_id = c.id
	WHERE c.id = $1`

func scanTarget(row pgx.Row, id int64) (Target, error) {
	t := Target{CopyID: id}
	var addRelease, addInstance *int64
	var addSince *time.Time
	err := row.Scan(&t.Status, &t.Kind, &t.ReleaseID, &t.MediaGrade, &t.SleeveGrade,
		&t.InstanceID, &t.InstanceReleaseID, &addRelease, &addSince, &addInstance)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, fmt.Errorf("discogs target: %w", err)
	}
	if addRelease != nil && addSince != nil {
		t.Pending = &Pending{ReleaseID: *addRelease, Since: *addSince, InstanceID: addInstance}
	}
	return t, nil
}

// Target returns the push target of copy id, or ErrNotFound.
func (s *Store) Target(ctx context.Context, id int64) (Target, error) {
	return scanTarget(s.pool.QueryRow(ctx, targetQuery, id), id)
}

// BeginAdd records, and commits at once, that an add of release for copy
// id is about to be sent; it outlives the push that sends it.
func (s *Store) BeginAdd(ctx context.Context, id, release int64) error {
	if _, err := s.pool.Exec(ctx, `INSERT INTO discogs_add (copy_id, release_id) VALUES ($1, $2)`, id, release); err != nil {
		return fmt.Errorf("begin discogs add: %w", err)
	}
	return nil
}

// AddMade records the instance Discogs answered copy id's add with.
func (s *Store) AddMade(ctx context.Context, id, instance int64) error {
	if _, err := s.pool.Exec(ctx, `UPDATE discogs_add SET instance_id = $2 WHERE copy_id = $1`, id, instance); err != nil {
		return fmt.Errorf("record discogs add: %w", err)
	}
	return nil
}

// EndAdd drops copy id's unfinished add: it is linked, refused or
// discarded.
func (s *Store) EndAdd(ctx context.Context, id int64) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM discogs_add WHERE copy_id = $1`, id); err != nil {
		return fmt.Errorf("end discogs add: %w", err)
	}
	return nil
}

// DiscardUnknownAdd drops copy id's add whose outcome is unknown, at the
// owner's word that it made nothing of theirs; false when there is none.
func (s *Store) DiscardUnknownAdd(ctx context.Context, id int64) (bool, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM discogs_add WHERE copy_id = $1 AND instance_id IS NULL`, id)
	if err != nil {
		return false, fmt.Errorf("discard discogs add: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

// Locked is a copy held for one push: no other push, update or delete of
// it runs until Commit or Rollback, so a copy deleted mid-push cannot
// leave an instance on Discogs that Ohara no longer knows of. Its
// unfinished add is written through the Store, outside the lock's
// transaction, so that it is kept whatever becomes of the push.
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

// Link records that the copy's instance is instance of release, its
// conditions set.
func (l *Locked) Link(ctx context.Context, release, instance int64) error {
	_, err := l.tx.Exec(ctx, `UPDATE copy SET discogs_instance_id = $2, discogs_instance_release_id = $3
		WHERE id = $1`, l.id, instance, release)
	if err != nil {
		return fmt.Errorf("link discogs instance: %w", err)
	}
	return nil
}

// Forget drops the link: Discogs no longer has that instance.
func (l *Locked) Forget(ctx context.Context) error {
	_, err := l.tx.Exec(ctx, `UPDATE copy SET discogs_instance_id = NULL, discogs_instance_release_id = NULL
		WHERE id = $1`, l.id)
	if err != nil {
		return fmt.Errorf("forget discogs instance: %w", err)
	}
	return nil
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
