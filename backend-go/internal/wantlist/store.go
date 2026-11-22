// Package wantlist mirrors the album wishes to the Discogs wantlist of the
// token's owner: a manual sync adds every wished release and removes the
// wants Ohara added whose wish is gone. It never removes a want Ohara did
// not add and never creates wishes from the wantlist.
package wantlist

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store reads the album wishes and records the wants Ohara added.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Wished is what the album wishes ask of the wantlist: the distinct
// Discogs releases of their editions, and how many album wishes (wishlist
// copies) have an edition without one.
type Wished struct {
	Releases []int64
	Skipped  int
}

// Wished reads the editions of the album copies on the wishlist.
func (s *Store) Wished(ctx context.Context) (Wished, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.discogs_release_id
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE c.status = 'wishlist' AND t.kind = 'music' ORDER BY c.id`)
	if err != nil {
		return Wished{}, fmt.Errorf("wished releases: %w", err)
	}
	releases, err := pgx.CollectRows(rows, pgx.RowTo[*int64])
	if err != nil {
		return Wished{}, fmt.Errorf("wished releases: %w", err)
	}
	var w Wished
	seen := map[int64]bool{}
	for _, release := range releases {
		switch {
		case release == nil:
			w.Skipped++
		case !seen[*release]:
			seen[*release] = true
			w.Releases = append(w.Releases, *release)
		}
	}
	return w, nil
}

// Owned is the record of a want Ohara put on the wantlist. ConfirmedAt is
// when Discogs accepted the add, nil while its outcome is unknown;
// DiscogsAddedAt is the date_added Discogs keeps for that want, nil until
// a sync reads it.
type Owned struct {
	Release        int64
	AddedAt        time.Time
	ConfirmedAt    *time.Time
	DiscogsAddedAt *time.Time
}

// Owned returns the wants Ohara put on the wantlist of username.
func (s *Store) Owned(ctx context.Context, username string) ([]Owned, error) {
	rows, err := s.pool.Query(ctx, `SELECT release_id, added_at, confirmed_at, discogs_added_at
		FROM discogs_want WHERE username = $1 ORDER BY release_id`, username)
	if err != nil {
		return nil, fmt.Errorf("owned wants: %w", err)
	}
	owned, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Owned, error) {
		var o Owned
		err := row.Scan(&o.Release, &o.AddedAt, &o.ConfirmedAt, &o.DiscogsAddedAt)
		return o, err
	})
	if err != nil {
		return nil, fmt.Errorf("owned wants: %w", err)
	}
	return owned, nil
}

// Pending records, before the add is sent, that Ohara is putting release
// on the wantlist of username; an earlier record of it starts over.
func (s *Store) Pending(ctx context.Context, username string, release int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO discogs_want (username, release_id) VALUES ($1, $2)
		ON CONFLICT (username, release_id) DO UPDATE
		SET added_at = now(), confirmed_at = NULL, discogs_added_at = NULL`, username, release)
	if err != nil {
		return fmt.Errorf("record want %d: %w", release, err)
	}
	return nil
}

// Confirm records that Discogs accepted the add of release.
func (s *Store) Confirm(ctx context.Context, username string, release int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE discogs_want SET confirmed_at = now()
		WHERE username = $1 AND release_id = $2`, username, release)
	if err != nil {
		return fmt.Errorf("confirm want %d: %w", release, err)
	}
	return nil
}

// Identify records the date_added Discogs keeps for the confirmed want
// release.
func (s *Store) Identify(ctx context.Context, username string, release int64, added time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE discogs_want SET discogs_added_at = $3
		WHERE username = $1 AND release_id = $2 AND confirmed_at IS NOT NULL`, username, release, added)
	if err != nil {
		return fmt.Errorf("identify want %d: %w", release, err)
	}
	return nil
}

// Forget drops the record of a want: it is off the wantlist, or no longer
// provably Ohara's.
func (s *Store) Forget(ctx context.Context, username string, release int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM discogs_want WHERE username = $1 AND release_id = $2`, username, release)
	if err != nil {
		return fmt.Errorf("forget want %d: %w", release, err)
	}
	return nil
}
