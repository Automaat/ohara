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
// its edition's Discogs release, its grades and the instance it was
// pushed to before with that instance's release.
type Target struct {
	CopyID            int64
	Status            string
	Kind              string
	ReleaseID         *int64
	MediaGrade        *string
	SleeveGrade       *string
	InstanceID        *int64
	InstanceReleaseID *int64
}

// Instance is the instance the copy made of its edition's current
// release, nil when it made none or made it of another release.
func (t *Target) Instance() *int64 {
	if t.InstanceID == nil || t.ReleaseID == nil || t.InstanceReleaseID == nil || *t.InstanceReleaseID != *t.ReleaseID {
		return nil
	}
	return t.InstanceID
}

// Target returns the push target of copy id, or ErrNotFound.
func (s *Store) Target(ctx context.Context, id int64) (Target, error) {
	t := Target{CopyID: id}
	err := s.pool.QueryRow(ctx, `SELECT c.status, ti.kind, e.discogs_release_id, c.media_grade,
			c.sleeve_grade, c.discogs_instance_id, c.discogs_instance_release_id
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title ti ON ti.id = e.title_id
		WHERE c.id = $1`, id).
		Scan(&t.Status, &t.Kind, &t.ReleaseID, &t.MediaGrade, &t.SleeveGrade, &t.InstanceID, &t.InstanceReleaseID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Target{}, ErrNotFound
	}
	if err != nil {
		return Target{}, fmt.Errorf("discogs target: %w", err)
	}
	return t, nil
}

// SaveInstance records that copy id made instance of release; ErrNotFound
// when the copy is gone.
func (s *Store) SaveInstance(ctx context.Context, id, release, instance int64) error {
	tag, err := s.pool.Exec(ctx, `UPDATE copy SET discogs_instance_id = $2, discogs_instance_release_id = $3
		WHERE id = $1`, id, instance, release)
	if err != nil {
		return fmt.Errorf("save discogs instance: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ForgetInstance drops instance from copy id when it is still the one
// stored: Discogs no longer has it.
func (s *Store) ForgetInstance(ctx context.Context, id, instance int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE copy SET discogs_instance_id = NULL, discogs_instance_release_id = NULL
		WHERE id = $1 AND discogs_instance_id = $2`, id, instance)
	if err != nil {
		return fmt.Errorf("forget discogs instance: %w", err)
	}
	return nil
}
