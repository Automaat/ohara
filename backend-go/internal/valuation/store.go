package valuation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/deals"
)

// Store is the Postgres access layer for valuation.
type Store struct {
	pool  *pgxpool.Pool
	deals *deals.Store
	// betweenReads runs in Valued after the copies are read and before
	// their prices are, so a test can change the database in between.
	betweenReads func()
}

// querier runs a query on the pool or inside a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool, deals: deals.NewStore(pool)}
}

// Holding is an edition with at least one owned copy, in the shape the
// deal matcher reads, with the Discogs release of an album edition.
type Holding struct {
	Want             deals.Want
	Barcode          *string
	DiscogsReleaseID *int64
}

// Holdings returns every edition the collection owns, oldest first.
func (s *Store) Holdings(ctx context.Context) ([]Holding, error) {
	rows, err := s.pool.Query(ctx, `SELECT e.id, t.id, t.kind, t.name, t.original_title,
			t.polish_title, t.authors, e.format, e.barcode, e.discogs_release_id
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
			&w.PolishTitle, &w.Authors, &w.Format, &h.Barcode, &h.DiscogsReleaseID)
		return h, err
	})
}

// RecordAllAsOf stores ls as prices of h's edition as RecordAsOf does, in
// one transaction, so they share their last seen time.
func (s *Store) RecordAllAsOf(ctx context.Context, ls []deals.Listing, h *Holding, asOf time.Time) error {
	return s.deals.RecordAllAsOf(ctx, ls, &h.Want, asOf)
}

// Rules returns the deal matcher's editable phrases.
func (s *Store) Rules(ctx context.Context) (deals.Rules, error) {
	return s.deals.Rules(ctx)
}

// Titles returns every title for the deal matcher's Outranks.
func (s *Store) Titles(ctx context.Context) (deals.Titles, error) {
	return s.deals.Titles(ctx)
}

// Record stores l as a price of h's edition, through the deal hunter's
// store so both keep one row per listing. It reports whether the listing
// was new.
func (s *Store) Record(ctx context.Context, l *deals.Listing, h *Holding) (bool, error) {
	isNew, _, err := s.deals.Record(ctx, l, &h.Want, false)
	return isNew, err
}

// RecordAsOf is Record for a run that read the holdings and titles it
// matched l with at asOf, as deals.Store.RecordAsOf describes.
func (s *Store) RecordAsOf(ctx context.Context, l *deals.Listing, h *Holding, asOf time.Time) (bool, error) {
	isNew, _, err := s.deals.RecordAsOf(ctx, l, &h.Want, false, asOf)
	return isNew, err
}

// UpdateGone brings the gone marks of source's stored listings up to a
// run, through the deal hunter's store (deals.Store.UpdateGone).
func (s *Store) UpdateGone(ctx context.Context, source string, complete, seen []string, since time.Time) (int64, error) {
	return s.deals.UpdateGone(ctx, source, complete, seen, since)
}

// HoldRun takes the run lock shared until the returned function runs, so
// no prune runs during a valuation (deals.Store.HoldRun).
func (s *Store) HoldRun(ctx context.Context) (func(), error) {
	return s.deals.HoldRun(ctx)
}

// MarkOffered records that an answer of source held the listings with
// externalIDs, through the deal hunter's store (deals.Store.MarkOffered).
func (s *Store) MarkOffered(ctx context.Context, source string, externalIDs []string) error {
	return s.deals.MarkOffered(ctx, source, externalIDs)
}

// Prune deletes the stored listings neither seen with a price nor offered
// within olderThan, through the deal hunter's store (deals.Store.Prune).
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return s.deals.Prune(ctx, olderThan)
}

// Now returns the database's current time, when a run reads its holdings.
func (s *Store) Now(ctx context.Context) (time.Time, error) {
	return s.deals.Now(ctx)
}

// Prices returns the valuation sources' observations last seen within
// window that value an owned edition. OLX prices of an edition without an
// owned copy count when an edition of the same title and format is owned,
// as middles pool that group and a listing keeps the edition of its title
// it was first stored against, often a wished one. Such a price counts only when first seen, or moved to the
// edition from another title, after the edition last changed: a listing
// matched before an edit, such as a new format, may not
// fit it, and a match for another edition later keeps it on this one.
// Floors and Discogs bounds are per edition, so those count only for an
// owned edition. A Discogs price counts only for the release the edition
// names now: its external ID is "<release>/<edition>", so a changed or
// cleared release stops valuing the edition at once.
func (s *Store) Prices(ctx context.Context, window time.Duration) ([]Price, error) {
	return prices(ctx, s.pool, window, 0)
}

// prices reads the prices Prices describes, only those of titleID's
// editions when it is not 0. Every bound of a copy comes from prices of
// its own title, so filtering by title leaves that title's ranges as
// they are. The ownership checks then read only that title's copies too,
// so the cost follows the title, not the collection.
func prices(ctx context.Context, q querier, window time.Duration, titleID int64) ([]Price, error) {
	args := []any{valuationSources, window.Seconds(), discogsSources, pooledSources}
	ownedEditions := `SELECT edition_id FROM copy WHERE status = 'owned'`
	var byTitle, ownedByTitle string
	if titleID != 0 {
		args = append(args, titleID)
		ownedEditions = `SELECT c.edition_id FROM copy c JOIN edition owned ON owned.id = c.edition_id
			WHERE c.status = 'owned' AND owned.title_id = $5`
		byTitle = ` AND e.title_id = $5`
		ownedByTitle = ` AND owned.title_id = $5`
	}
	rows, err := q.Query(ctx, `SELECT o.edition_id, e.title_id, e.format, o.source,
			o.price_cents, o.acquired_as, o.last_seen_at, o.observed_at, o.gone_at
		FROM price_observation o JOIN edition e ON e.id = o.edition_id
		WHERE o.source = ANY ($1)
			AND o.last_seen_at > now() - make_interval(secs => $2)
			AND (o.edition_id IN (`+ownedEditions+`)
				OR o.source = ANY ($4) AND coalesce(o.moved_at, o.observed_at) > e.updated_at
					AND (e.title_id, coalesce(e.format, '')) IN (
						SELECT owned.title_id, coalesce(owned.format, '')
						FROM copy c JOIN edition owned ON owned.id = c.edition_id
						WHERE c.status = 'owned'`+ownedByTitle+`))
			AND (o.source <> ALL ($3)
				OR o.external_id = e.discogs_release_id::text || '/' || e.id::text)`+byTitle,
		args...)
	if err != nil {
		return nil, fmt.Errorf("prices: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Price])
}

// Markets returns the used price of each title and format the valuation
// would give an owned copy of it: the middle of the OLX prices last seen
// within window, from every edition of that title and format, an
// unowned one's only when first seen after its last change. Without
// titleID it values every title and format with a wish; with it, every
// format of that title, wished, owned or neither. One with no used price
// is left out.
func (s *Store) Markets(ctx context.Context, window time.Duration, titleID int64) ([]Market, error) {
	args := []any{pooledSources, window.Seconds()}
	scope := ` AND (e.title_id, coalesce(e.format, '')) IN (
			SELECT wished.title_id, coalesce(wished.format, '')
			FROM copy c JOIN edition wished ON wished.id = c.edition_id
			WHERE c.status = 'wishlist')`
	if titleID != 0 {
		args = append(args, titleID)
		scope = ` AND e.title_id = $3`
	}
	rows, err := s.pool.Query(ctx, `SELECT o.edition_id, e.title_id, e.format, o.source,
			o.price_cents, o.acquired_as, o.last_seen_at, o.observed_at, o.gone_at
		FROM price_observation o JOIN edition e ON e.id = o.edition_id
		WHERE o.source = ANY ($1)
			AND o.last_seen_at > now() - make_interval(secs => $2)
			AND (o.observed_at > e.updated_at
				OR EXISTS (SELECT 1 FROM copy c WHERE c.edition_id = e.id AND c.status = 'owned'))`+scope,
		args...)
	if err != nil {
		return nil, fmt.Errorf("markets: %w", err)
	}
	seen, err := pgx.CollectRows(rows, pgx.RowToStructByPos[Price])
	if err != nil {
		return nil, fmt.Errorf("markets: %w", err)
	}
	return NewRanges(seen).Markets(), nil
}

// Copies returns every owned copy with its edition and title, by title.
func (s *Store) Copies(ctx context.Context) ([]CopyValue, error) {
	return copies(ctx, s.pool, 0)
}

// copies reads the owned copies Copies describes, only titleID's when it
// is not 0.
func copies(ctx context.Context, q querier, titleID int64) ([]CopyValue, error) {
	var args []any
	var byTitle string
	if titleID != 0 {
		args = append(args, titleID)
		byTitle = ` AND t.id = $1`
	}
	rows, err := q.Query(ctx, `SELECT c.id, e.id, t.id, t.name, t.kind, e.format, e.barcode,
			c.price_cents, `+collection.CoverURL+`, e.discogs_release_id, c.condition
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE c.status = 'owned'`+byTitle+`
		ORDER BY lower(t.name), c.id`, args...)
	if err != nil {
		return nil, fmt.Errorf("copies: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (CopyValue, error) {
		var c CopyValue
		err := row.Scan(&c.CopyID, &c.EditionID, &c.TitleID, &c.TitleName, &c.Kind, &c.Format,
			&c.Barcode, &c.PriceCents, &c.CoverURL, &c.DiscogsReleaseID, &c.Condition)
		return c, err
	})
}

// Valued returns every owned copy with its range and the collection
// summary, from the prices last seen within window, with the gain
// realized by the copies sold on or before today. Copies, prices and
// sales come from one snapshot, so a copy's Discogs release is the one
// its Discogs prices were read for and a copy sold meanwhile counts once.
func (s *Store) Valued(ctx context.Context, window time.Duration) ([]CopyValue, Summary, error) {
	owned, sales, err := s.valued(ctx, window, 0)
	if err != nil {
		return nil, Summary{}, err
	}
	sum := Summarize(owned)
	sum.Realized = sales
	return owned, sum, nil
}

// TitleValued returns titleID's owned copies with the ranges Valued gives
// them, reading only that title's copies and prices.
func (s *Store) TitleValued(ctx context.Context, window time.Duration, titleID int64) ([]CopyValue, error) {
	owned, _, err := s.valued(ctx, window, titleID)
	return owned, err
}

// valued values the owned copies of titleID, or of every title when it
// is 0, from one snapshot. For every title it also sums the sales to
// date; for one it reads none.
func (s *Store) valued(ctx context.Context, window time.Duration, titleID int64) ([]CopyValue, Realized, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, Realized{}, fmt.Errorf("valued: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	owned, err := copies(ctx, tx, titleID)
	if err != nil {
		return nil, Realized{}, err
	}
	if s.betweenReads != nil {
		s.betweenReads()
	}
	seen, err := prices(ctx, tx, window, titleID)
	if err != nil {
		return nil, Realized{}, err
	}
	Value(owned, NewRanges(seen))
	var sales Realized
	if titleID == 0 {
		if sales, err = realized(ctx, tx); err != nil {
			return nil, Realized{}, err
		}
	}
	return owned, sales, nil
}

// realizedSQL sums the sales of the copies sold on or before today, as
// Realized describes: its copies, gain and unpriced copies. A sale dated
// later counts from its day.
const realizedSQL = `SELECT
		count(*) FILTER (WHERE price_cents IS NOT NULL AND sale_price_cents IS NOT NULL) AS copies,
		coalesce(sum(sale_price_cents - price_cents), 0) AS gain_cents,
		count(*) FILTER (WHERE price_cents IS NULL OR sale_price_cents IS NULL) AS unpriced
	FROM copy WHERE status = 'sold' AND sold_on <= current_date`

// realized reads realizedSQL.
func realized(ctx context.Context, q querier) (Realized, error) {
	rows, err := q.Query(ctx, realizedSQL)
	if err != nil {
		return Realized{}, fmt.Errorf("realized: %w", err)
	}
	r, err := pgx.CollectExactlyOneRow(rows, func(row pgx.CollectableRow) (Realized, error) {
		var r Realized
		err := row.Scan(&r.Copies, &r.GainCents, &r.Unpriced)
		return r, err
	})
	if err != nil {
		return Realized{}, fmt.Errorf("realized: %w", err)
	}
	return r, nil
}

// HistoryPoint is one day of the collection's value.
type HistoryPoint struct {
	Day        string    `json:"day"`
	ComputedAt time.Time `json:"computed_at"`
	Totals
}

const historyColumns = `to_char(day, 'YYYY-MM-DD'), computed_at, copies, spent_cents,
	floor_cents, floor_copies, mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence,
	realized_gain_cents, realized_copies, realized_unpriced`

func scanHistory(row pgx.CollectableRow) (HistoryPoint, error) {
	var p HistoryPoint
	err := row.Scan(historyFields(&p)...)
	return p, err
}

// historyFields are the scan targets of p for historyColumns.
func historyFields(p *HistoryPoint) []any {
	return []any{
		&p.Day, &p.ComputedAt, &p.Copies, &p.SpentCents,
		&p.Floor.Cents, &p.Floor.Copies, &p.Mid.Cents, &p.Mid.Copies,
		&p.Ceiling.Cents, &p.Ceiling.Copies, &p.LowConfidence,
		&p.Realized.GainCents, &p.Realized.Copies, &p.Realized.Unpriced,
	}
}

// saveDaySQL upserts today's collection value. It sums the gain realized
// to date itself rather than taking the run's, so a run read before
// midnight and saved after it stores the sales of the day the row names.
const saveDaySQL = `INSERT INTO valuation_history (day, copies, spent_cents,
		floor_cents, floor_copies, mid_cents, mid_copies, ceiling_cents, ceiling_copies,
		low_confidence, realized_gain_cents, realized_copies, realized_unpriced)
	SELECT current_date, $1, $2, $3, $4, $5, $6, $7, $8, $9,
		sales.gain_cents, sales.copies, sales.unpriced
	FROM (` + realizedSQL + `) sales
	ON CONFLICT (day) DO UPDATE SET copies = EXCLUDED.copies, spent_cents = EXCLUDED.spent_cents,
		floor_cents = EXCLUDED.floor_cents, floor_copies = EXCLUDED.floor_copies,
		mid_cents = EXCLUDED.mid_cents, mid_copies = EXCLUDED.mid_copies,
		ceiling_cents = EXCLUDED.ceiling_cents, ceiling_copies = EXCLUDED.ceiling_copies,
		low_confidence = EXCLUDED.low_confidence,
		realized_gain_cents = EXCLUDED.realized_gain_cents,
		realized_copies = EXCLUDED.realized_copies,
		realized_unpriced = EXCLUDED.realized_unpriced, computed_at = now()
	RETURNING ` + historyColumns

// saveCopyDaysSQL upserts each copy's value of the day $1, today when
// null. Each copy is held (FOR KEY SHARE) until the commit, so a copy
// deleted since it was valued is left out instead of failing the foreign
// key.
const saveCopyDaysSQL = `INSERT INTO copy_value_history (copy_id, day, floor_cents, mid_cents, ceiling_cents)
	SELECT c.id, coalesce($1::date, current_date), v.floor_cents, v.mid_cents, v.ceiling_cents
	FROM unnest($2::bigint[], $3::bigint[], $4::bigint[], $5::bigint[])
		AS v (copy_id, floor_cents, mid_cents, ceiling_cents)
	JOIN copy c ON c.id = v.copy_id
	FOR KEY SHARE OF c
	ON CONFLICT (copy_id, day) DO UPDATE SET floor_cents = EXCLUDED.floor_cents,
		mid_cents = EXCLUDED.mid_cents, ceiling_cents = EXCLUDED.ceiling_cents,
		computed_at = now()`

// copyDayArgs are saveCopyDaysSQL's arguments for owned on day, nil for
// today.
func copyDayArgs(day *string, owned []CopyValue) []any {
	ids := make([]int64, len(owned))
	floors := make([]*int64, len(owned))
	mids := make([]*int64, len(owned))
	ceilings := make([]*int64, len(owned))
	for i := range owned {
		c := &owned[i]
		ids[i] = c.CopyID
		floors[i] = estimateCents(c.Floor)
		mids[i] = estimateCents(c.Mid)
		ceilings[i] = estimateCents(c.Ceiling)
	}
	return []any{day, ids, floors, mids, ceilings}
}

// SaveDay stores sum as today's value, with the gain realized by the
// copies sold on or before today in place of sum's, and each copy in
// owned as that copy's value today, replacing what an earlier run stored
// today for the collection and for those copies. A copy valued earlier
// today but not in owned, such as one sold since, keeps its row. Both are
// written in one transaction, so the day is stored with its copies or not
// at all; the two statements go in one batch to spare a round trip.
func (s *Store) SaveDay(ctx context.Context, sum *Summary, owned []CopyValue) (HistoryPoint, error) {
	var p HistoryPoint
	err := pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		batch.Queue(saveDaySQL, sum.Copies, sum.SpentCents, sum.Floor.Cents, sum.Floor.Copies,
			sum.Mid.Cents, sum.Mid.Copies, sum.Ceiling.Cents, sum.Ceiling.Copies, sum.LowConfidence).
			QueryRow(func(row pgx.Row) error { return row.Scan(historyFields(&p)...) })
		batch.Queue(saveCopyDaysSQL, copyDayArgs(nil, owned)...)
		return tx.SendBatch(ctx, batch).Close()
	})
	if err != nil {
		return HistoryPoint{}, fmt.Errorf("save day: %w", err)
	}
	return p, nil
}

// CopyDay is one day of a copy's value: its buy-back, used and new value
// in grosze, null for a bound without prices that day.
type CopyDay struct {
	CopyID       int64  `json:"copy_id"`
	Day          string `json:"day"`
	FloorCents   *int64 `json:"floor_cents"`
	MidCents     *int64 `json:"mid_cents"`
	CeilingCents *int64 `json:"ceiling_cents"`
}

// saveCopyDays stores each copy in owned as its value on day
// (YYYY-MM-DD), as SaveDay does for today.
func (s *Store) saveCopyDays(ctx context.Context, day string, owned []CopyValue) error {
	if _, err := s.pool.Exec(ctx, saveCopyDaysSQL, copyDayArgs(&day, owned)...); err != nil {
		return fmt.Errorf("save copy days: %w", err)
	}
	return nil
}

// estimateCents is e's amount, nil for a bound without prices.
func estimateCents(e *Estimate) *int64 {
	if e == nil {
		return nil
	}
	return &e.Cents
}

// CopyHistory returns the daily values of titleID's copies, owned or not,
// over the last n days up to today, by copy and oldest first. A row dated
// after today, stored before the database's date moved back (a time zone
// change), is left out.
func (s *Store) CopyHistory(ctx context.Context, titleID int64, n int) ([]CopyDay, error) {
	rows, err := s.pool.Query(ctx, `SELECT h.copy_id, to_char(h.day, 'YYYY-MM-DD'),
			h.floor_cents, h.mid_cents, h.ceiling_cents
		FROM copy_value_history h
			JOIN copy c ON c.id = h.copy_id
			JOIN edition e ON e.id = c.edition_id
		WHERE e.title_id = $1 AND h.day > current_date - $2::int AND h.day <= current_date
		ORDER BY h.copy_id, h.day`, titleID, n)
	if err != nil {
		return nil, fmt.Errorf("copy history: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[CopyDay])
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
