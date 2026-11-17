package valuation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/deals"
)

// Store is the Postgres access layer for valuation.
type Store struct {
	pool  *pgxpool.Pool
	deals *deals.Store
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, deals: deals.NewStore(pool)}
}

// Holding is an edition with at least one owned copy, in the shape the
// deal matcher reads.
type Holding struct {
	Want    deals.Want
	Barcode *string
}

// Holdings returns every edition the collection owns, oldest first.
func (s *Store) Holdings(ctx context.Context) ([]Holding, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, t.id, t.kind, t.name, t.original_title,
			t.polish_title, t.authors, e.format, e.barcode
		FROM edition e JOIN title t ON t.id = e.title_id
		WHERE EXISTS (SELECT 1 FROM copy c WHERE c.edition_id = e.id AND c.status = 'owned')
		ORDER BY e.id`)
	if err != nil {
		return nil, fmt.Errorf("holdings: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Holding, error) {
		var h Holding
		w := &h.Want
		err := row.Scan(&w.EditionID, &w.TitleID, &w.Kind, &w.Name, &w.OriginalTitle,
			&w.PolishTitle, &w.Authors, &w.Format, &h.Barcode)
		return h, err
	})
}

// Rules returns the deal matcher's editable phrases.
func (s *Store) Rules(ctx context.Context) (deals.Rules, error) {
	return s.deals.Rules(ctx)
}

// Record stores l as a price of h's edition, through the deal hunter's
// store so both keep one row per listing. It reports whether the listing
// was new.
func (s *Store) Record(ctx context.Context, l *deals.Listing, h *Holding) (bool, error) {
	isNew, _, err := s.deals.Record(ctx, l, &h.Want, false)
	return isNew, err
}

// Prices returns the valuation sources' observations of owned editions
// last seen within window.
func (s *Store) Prices(ctx context.Context, window time.Duration) ([]Price, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.edition_id, e.title_id, e.format, o.source,
			o.price_cents, o.acquired_as, o.last_seen_at
		FROM price_observation o JOIN edition e ON e.id = o.edition_id
		WHERE o.source = ANY ($1)
			AND o.last_seen_at > now() - make_interval(secs => $2)
			AND o.edition_id IN (SELECT edition_id FROM copy WHERE status = 'owned')`,
		valuationSources, window.Seconds())
	if err != nil {
		return nil, fmt.Errorf("prices: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Price])
}

// Copies returns every owned copy with its edition and title, by title.
func (s *Store) Copies(ctx context.Context) ([]CopyValue, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, e.id, t.id, t.name, t.kind, e.format, e.barcode,
			c.price_cents
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE c.status = 'owned'
		ORDER BY lower(t.name), c.id`)
	if err != nil {
		return nil, fmt.Errorf("copies: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CopyValue, error) {
		var c CopyValue
		err := row.Scan(&c.CopyID, &c.EditionID, &c.TitleID, &c.TitleName, &c.Kind, &c.Format,
			&c.Barcode, &c.PriceCents)
		return c, err
	})
}

// Valued returns every owned copy with its range and the collection
// summary, from the prices last seen within window.
func (s *Store) Valued(ctx context.Context, window time.Duration) ([]CopyValue, Summary, error) {
	copies, err := s.Copies(ctx)
	if err != nil {
		return nil, Summary{}, err
	}
	prices, err := s.Prices(ctx, window)
	if err != nil {
		return nil, Summary{}, err
	}
	Value(copies, NewRanges(prices))
	return copies, Summarize(copies), nil
}

// HistoryPoint is one day of the collection's value.
type HistoryPoint struct {
	Day        string    `json:"day"`
	ComputedAt time.Time `json:"computed_at"`
	Summary
}

const historyColumns = `to_char(day, 'YYYY-MM-DD'), computed_at, copies, spent_cents,
	floor_cents, floor_copies, mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence`

func scanHistory(row pgx.CollectableRow) (HistoryPoint, error) {
	var p HistoryPoint
	err := row.Scan(&p.Day, &p.ComputedAt, &p.Copies, &p.SpentCents,
		&p.Floor.Cents, &p.Floor.Copies, &p.Mid.Cents, &p.Mid.Copies,
		&p.Ceiling.Cents, &p.Ceiling.Copies, &p.LowConfidence)
	return p, err
}

// SaveDay stores s as today's value, replacing an earlier run's.
func (s *Store) SaveDay(ctx context.Context, sum *Summary) (HistoryPoint, error) {
	rows, err := s.pool.Query(ctx, `INSERT INTO valuation_history (day, copies, spent_cents,
			floor_cents, floor_copies, mid_cents, mid_copies, ceiling_cents, ceiling_copies,
			low_confidence)
		VALUES (current_date, $1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (day) DO UPDATE SET copies = EXCLUDED.copies, spent_cents = EXCLUDED.spent_cents,
			floor_cents = EXCLUDED.floor_cents, floor_copies = EXCLUDED.floor_copies,
			mid_cents = EXCLUDED.mid_cents, mid_copies = EXCLUDED.mid_copies,
			ceiling_cents = EXCLUDED.ceiling_cents, ceiling_copies = EXCLUDED.ceiling_copies,
			low_confidence = EXCLUDED.low_confidence, computed_at = now()
		RETURNING `+historyColumns,
		sum.Copies, sum.SpentCents, sum.Floor.Cents, sum.Floor.Copies, sum.Mid.Cents, sum.Mid.Copies,
		sum.Ceiling.Cents, sum.Ceiling.Copies, sum.LowConfidence)
	if err != nil {
		return HistoryPoint{}, fmt.Errorf("save day: %w", err)
	}
	p, err := pgx.CollectExactlyOneRow(rows, scanHistory)
	if err != nil {
		return HistoryPoint{}, fmt.Errorf("save day: %w", err)
	}
	return p, nil
}

// History returns the value of each of the last n days, oldest first.
func (s *Store) History(ctx context.Context, n int) ([]HistoryPoint, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+historyColumns+` FROM valuation_history
		WHERE day > current_date - $1::int ORDER BY day`, n)
	if err != nil {
		return nil, fmt.Errorf("history: %w", err)
	}
	return pgx.CollectRows(rows, scanHistory)
}

// Latest returns the newest day of value; false before the first run.
func (s *Store) Latest(ctx context.Context) (HistoryPoint, bool, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+historyColumns+` FROM valuation_history
		ORDER BY day DESC LIMIT 1`)
	if err != nil {
		return HistoryPoint{}, false, fmt.Errorf("latest value: %w", err)
	}
	p, err := pgx.CollectExactlyOneRow(rows, scanHistory)
	if errors.Is(err, pgx.ErrNoRows) {
		return HistoryPoint{}, false, nil
	}
	if err != nil {
		return HistoryPoint{}, false, fmt.Errorf("latest value: %w", err)
	}
	return p, true, nil
}
