// Package scanqueue keeps the barcodes the scanner could not resolve, so an
// unknown disc or book is never lost during bulk entry. An entry leaves the
// queue when an edition with its barcode is created (a trigger on edition) or
// when it is deleted.
package scanqueue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Reasons an entry is queued: no provider knew the barcode, or every
// provider failed.
const (
	ReasonNotFound     = "not_found"
	ReasonLookupFailed = "lookup_failed"
)

var reasons = []string{ReasonNotFound, ReasonLookupFailed}

// maxDetail caps the stored provider error summary, in runes.
const maxDetail = 500

var (
	ErrNotFound     = errors.New("scan queue entry not found")
	ErrInCollection = errors.New("an edition with this barcode already exists")
)

// Entry is one queued barcode.
type Entry struct {
	ID            int64     `json:"id"`
	Barcode       string    `json:"barcode"`
	Reason        string    `json:"reason"`
	Detail        string    `json:"detail"`
	Scans         int       `json:"scans"`
	CreatedAt     time.Time `json:"created_at"`
	LastScannedAt time.Time `json:"last_scanned_at"`
}

// Store is the Postgres access layer for the queue.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

const columns = `id, barcode, reason, detail, scans, created_at, last_scanned_at`

func scan(row pgx.Row) (Entry, error) {
	var e Entry
	err := row.Scan(&e.ID, &e.Barcode, &e.Reason, &e.Detail, &e.Scans, &e.CreatedAt, &e.LastScannedAt)
	return e, err
}

// List returns the queue, most recently scanned first.
func (s *Store) List(ctx context.Context) ([]Entry, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+columns+` FROM scan_queue
		ORDER BY last_scanned_at DESC, id DESC`)
	if err != nil {
		return nil, fmt.Errorf("list scan queue: %w", err)
	}
	defer rows.Close()
	out := []Entry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("list scan queue: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list scan queue: %w", err)
	}
	return out, nil
}

// Add queues a normalized barcode, or records another scan of one already
// queued with the latest reason and detail. The bool reports a new entry. A
// barcode an edition already carries is ErrInCollection.
func (s *Store) Add(ctx context.Context, code, reason, detail string) (Entry, bool, error) {
	row := s.pool.QueryRow(ctx, `INSERT INTO scan_queue AS q (barcode, reason, detail)
		SELECT $1, $2, $3
		WHERE NOT EXISTS (SELECT 1 FROM edition WHERE barcode = $1)
		ON CONFLICT (barcode) DO UPDATE SET
			reason = EXCLUDED.reason,
			detail = EXCLUDED.detail,
			scans = q.scans + 1,
			last_scanned_at = now()
		RETURNING `+columns+`, (xmax = 0)`, code, reason, detail)
	var entry Entry
	var created bool
	err := row.Scan(&entry.ID, &entry.Barcode, &entry.Reason, &entry.Detail, &entry.Scans,
		&entry.CreatedAt, &entry.LastScannedAt, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return Entry{}, false, ErrInCollection
	}
	if err != nil {
		return Entry{}, false, fmt.Errorf("add to scan queue: %w", err)
	}
	return entry, created, nil
}

// Delete removes one entry.
func (s *Store) Delete(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM scan_queue WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete scan queue entry: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
