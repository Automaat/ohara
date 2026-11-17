package lookup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PGCache keeps raw provider responses in the lookup_cache table.
type PGCache struct {
	pool *pgxpool.Pool
}

// NewPGCache wraps pool.
func NewPGCache(pool *pgxpool.Pool) *PGCache {
	return &PGCache{pool: pool}
}

// Get returns the response stored for provider and barcode if it is younger
// than maxAge.
func (c *PGCache) Get(ctx context.Context, provider, code string, maxAge time.Duration) (json.RawMessage, bool, error) {
	var raw json.RawMessage
	err := c.pool.QueryRow(ctx, `SELECT response FROM lookup_cache
		WHERE provider = $1 AND barcode = $2 AND fetched_at > now() - make_interval(secs => $3)`,
		provider, code, maxAge.Seconds()).Scan(&raw)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read lookup cache: %w", err)
	}
	return raw, true, nil
}

// Put stores or replaces the response for provider and barcode.
func (c *PGCache) Put(ctx context.Context, provider, code string, raw json.RawMessage) error {
	_, err := c.pool.Exec(ctx, `INSERT INTO lookup_cache (provider, barcode, response)
		VALUES ($1, $2, $3)
		ON CONFLICT (provider, barcode) DO UPDATE SET response = EXCLUDED.response, fetched_at = now()`,
		provider, code, raw)
	if err != nil {
		return fmt.Errorf("write lookup cache: %w", err)
	}
	return nil
}
