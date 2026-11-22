// Package wantlist mirrors the album wishes to the Discogs wantlist of the
// token's owner: a manual sync adds every wished release and removes the
// wants Ohara added whose wish is gone. It never removes a want Ohara did
// not add and never creates wishes from the wantlist.
package wantlist

import (
	"context"
	"fmt"

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
// Discogs releases of their editions, and how many wished editions have
// none.
type Wished struct {
	Releases []int64
	Skipped  int
}

// Wished reads the editions of the album copies on the wishlist.
func (s *Store) Wished(ctx context.Context) (Wished, error) {
	rows, err := s.pool.Query(ctx, `SELECT DISTINCT e.id, e.discogs_release_id
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE c.status = 'wishlist' AND t.kind = 'music' ORDER BY e.id`)
	if err != nil {
		return Wished{}, fmt.Errorf("wished releases: %w", err)
	}
	editions, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (*int64, error) {
		var id int64
		var release *int64
		err := row.Scan(&id, &release)
		return release, err
	})
	if err != nil {
		return Wished{}, fmt.Errorf("wished releases: %w", err)
	}
	var w Wished
	seen := map[int64]bool{}
	for _, release := range editions {
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

// Added returns the releases Ohara put on the wantlist of username.
func (s *Store) Added(ctx context.Context, username string) ([]int64, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT release_id FROM discogs_want WHERE username = $1 ORDER BY release_id`, username)
	if err != nil {
		return nil, fmt.Errorf("added wants: %w", err)
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("added wants: %w", err)
	}
	return ids, nil
}

// RecordAdded records that Ohara put release on the wantlist of username.
func (s *Store) RecordAdded(ctx context.Context, username string, release int64) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO discogs_want (username, release_id) VALUES ($1, $2)
		ON CONFLICT DO NOTHING`, username, release)
	if err != nil {
		return fmt.Errorf("record want %d: %w", release, err)
	}
	return nil
}

// Forget drops the record of a want Ohara added, once it is off the
// wantlist.
func (s *Store) Forget(ctx context.Context, username string, release int64) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM discogs_want WHERE username = $1 AND release_id = $2`, username, release)
	if err != nil {
		return fmt.Errorf("forget want %d: %w", release, err)
	}
	return nil
}
