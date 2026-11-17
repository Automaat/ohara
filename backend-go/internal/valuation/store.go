package valuation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/deals"
)

// Store is the Postgres access layer for valuation.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
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
	return deals.NewStore(s.pool).Rules(ctx)
}

type observationRaw struct {
	Title string          `json:"title"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Record stores l as a price of h's edition. A listing the source already
// reported keeps its row, edition and first observation time; its price
// and last_seen_at move to now. It reports whether the listing was new.
func (s *Store) Record(ctx context.Context, l *deals.Listing, h *Holding) (bool, error) {
	raw, err := json.Marshal(observationRaw{Title: l.Title, Data: l.Raw})
	if err != nil {
		return false, fmt.Errorf("record: encode raw: %w", err)
	}
	var inserted bool
	err = s.pool.QueryRow(ctx, `INSERT INTO price_observation
			(source, external_id, price_cents, acquired_as, url, edition_id, title_id, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL DO UPDATE SET
			price_cents = EXCLUDED.price_cents,
			last_seen_at = now(),
			url = EXCLUDED.url,
			acquired_as = coalesce(EXCLUDED.acquired_as, price_observation.acquired_as),
			edition_id = coalesce(price_observation.edition_id, EXCLUDED.edition_id),
			title_id = coalesce(price_observation.title_id, EXCLUDED.title_id)
		RETURNING xmax = 0`,
		l.Source, l.ExternalID, l.PriceCents, l.AcquiredAs, l.URL, h.Want.EditionID, h.Want.TitleID, raw,
	).Scan(&inserted)
	if err != nil {
		return false, fmt.Errorf("record observation: %w", err)
	}
	return inserted, nil
}

// Prices returns the valuation sources' observations of owned editions
// last seen within window.
func (s *Store) Prices(ctx context.Context, window time.Duration) ([]Price, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.edition_id, o.source, o.price_cents, o.acquired_as,
			o.last_seen_at
		FROM price_observation o
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
	Value(copies, Ranges(prices))
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
