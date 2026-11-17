package movie

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGTokenStore keeps token pairs in the oauth_token table.
type PGTokenStore struct {
	Pool *pgxpool.Pool
}

// Load returns the stored pair for provider.
func (s PGTokenStore) Load(ctx context.Context, provider string) (Token, bool, error) {
	var t Token
	err := s.Pool.QueryRow(ctx, `SELECT access_token, refresh_token, expires_at
		FROM oauth_token WHERE provider = $1`, provider).Scan(&t.Access, &t.Refresh, &t.Expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return Token{}, false, nil
	}
	if err != nil {
		return Token{}, false, fmt.Errorf("read oauth token: %w", err)
	}
	return t, true, nil
}

// Save stores or replaces the pair for provider.
func (s PGTokenStore) Save(ctx context.Context, provider string, t Token) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO oauth_token (provider, access_token, refresh_token, expires_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider) DO UPDATE SET access_token = EXCLUDED.access_token,
			refresh_token = EXCLUDED.refresh_token, expires_at = EXCLUDED.expires_at, updated_at = now()`,
		provider, t.Access, t.Refresh, t.Expiry)
	if err != nil {
		return fmt.Errorf("write oauth token: %w", err)
	}
	return nil
}
