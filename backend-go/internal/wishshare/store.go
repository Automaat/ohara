// Package wishshare serves the wishlist to anyone who has its share link:
// the owner's API under /api/wishlist/share creates, shows and turns off the
// one link, and the public pages under /share/{token} show the wishes with
// only what a gift giver needs.
package wishshare

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// tokenBytes is the token's random length: 256 bits, 43 base64url
// characters.
const tokenBytes = 32

// tokenLen is the length of an encoded token.
var tokenLen = base64.RawURLEncoding.EncodedLen(tokenBytes)

// ErrNoLink reports that no share link is on.
var ErrNoLink = errors.New("no share link")

// Link is the active share link's token and when it was made.
type Link struct {
	Token     string
	CreatedAt time.Time
}

// Wish is one wished copy as the public page shows it. Title carries a TV
// season's number; Cover is an outside cover URL, never the edition's own
// photo, which is served only to the owner.
type Wish struct {
	Title    string   `json:"title"`
	Creators []string `json:"creators"`
	Year     *int     `json:"year"`
	Kind     string   `json:"kind" ts:"Kind"`
	Format   *string  `json:"format" ts:"Format,null"`
	Binding  *string  `json:"binding" ts:"Binding,null"`
	CoverURL *string  `json:"cover_url"`
}

// Store reads and writes the share link and the wishes it shows.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps a pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// newToken returns 256 random bits as base64url without padding.
func newToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// Create turns the link on with a new token, replacing any earlier one, so
// the old link stops working.
func (s *Store) Create(ctx context.Context) (Link, error) {
	token, err := newToken()
	if err != nil {
		return Link{}, err
	}
	var l Link
	err = s.pool.QueryRow(ctx, `INSERT INTO wishlist_share (token) VALUES ($1)
		ON CONFLICT (singleton) DO UPDATE SET token = EXCLUDED.token, created_at = now()
		RETURNING token, created_at`, token).Scan(&l.Token, &l.CreatedAt)
	if err != nil {
		return Link{}, fmt.Errorf("create share link: %w", err)
	}
	return l, nil
}

// Current returns the active link, or ErrNoLink.
func (s *Store) Current(ctx context.Context) (Link, error) {
	var l Link
	err := s.pool.QueryRow(ctx, `SELECT token, created_at FROM wishlist_share`).Scan(&l.Token, &l.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Link{}, ErrNoLink
	}
	if err != nil {
		return Link{}, fmt.Errorf("share link: %w", err)
	}
	return l, nil
}

// Delete turns the link off; with no link on it does nothing.
func (s *Store) Delete(ctx context.Context) error {
	if _, err := s.pool.Exec(ctx, `DELETE FROM wishlist_share`); err != nil {
		return fmt.Errorf("delete share link: %w", err)
	}
	return nil
}

// SharedWishes returns the wishes when token is the active link's token,
// and false when it is not. The token and the wishes are read in one
// read-only snapshot, so a link turned off before the snapshot shows
// nothing, not even wishes added afterwards. The token comparison takes the
// same time wherever the two differ.
func (s *Store) SharedWishes(ctx context.Context, token string) ([]Wish, bool, error) {
	if len(token) != tokenLen {
		return nil, false, nil
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, false, fmt.Errorf("shared wishes: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var active string
	err = tx.QueryRow(ctx, `SELECT token FROM wishlist_share`).Scan(&active)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("share token: %w", err)
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(active)) != 1 {
		return nil, false, nil
	}
	wishes, err := wishes(ctx, tx)
	if err != nil {
		return nil, false, err
	}
	return wishes, true, nil
}

// wishes returns every copy on the wishlist, by title, with only the fields
// the public page shows.
func wishes(ctx context.Context, tx pgx.Tx) ([]Wish, error) {
	rows, err := tx.Query(ctx, `SELECT
			CASE WHEN t.kind = 'tv' THEN t.name || ', sezon ' || t.season ELSE t.name END,
			t.authors, t.year, t.kind, e.format, e.binding, COALESCE(e.cover_url, t.cover_url)
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE c.status = 'wishlist'
		ORDER BY lower(t.name), t.season NULLS FIRST, c.id`)
	if err != nil {
		return nil, fmt.Errorf("shared wishes: %w", err)
	}
	wishes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Wish, error) {
		var w Wish
		err := row.Scan(&w.Title, &w.Creators, &w.Year, &w.Kind, &w.Format, &w.Binding, &w.CoverURL)
		if w.Creators == nil {
			w.Creators = []string{}
		}
		return w, err
	})
	if err != nil {
		return nil, fmt.Errorf("shared wishes: %w", err)
	}
	return wishes, nil
}
