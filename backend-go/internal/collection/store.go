package collection

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	pgUniqueViolation     = "23505"
	pgForeignKeyViolation = "23503"
	pgRestrictViolation   = "23001"
)

// Store is the Postgres access layer for the collection.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// isFKViolation reports a broken reference through constraint. Deleting a
// referenced row under ON DELETE RESTRICT raises 23001, not 23503.
func isFKViolation(err error, constraint string) bool {
	code, name := pgCode(err)
	return (code == pgForeignKeyViolation || code == pgRestrictViolation) && name == constraint
}

// pgCode returns the SQLSTATE and constraint name of a Postgres error.
func pgCode(err error) (string, string) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr != nil {
		return pgErr.Code, pgErr.ConstraintName
	}
	return "", ""
}

// mapErr turns a missing row into notFound and unique violations into their
// sentinels. Foreign-key violations mean different things per call site, so
// callers check them with isFKViolation.
func mapErr(err, notFound error, op string) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound
	}
	switch code, constraint := pgCode(err); {
	case code == pgUniqueViolation && constraint == "edition_barcode_key":
		return ErrDuplicateBarcode
	case code == pgUniqueViolation && constraint == "title_tmdb_id_key":
		return ErrDuplicateTMDB
	case code == pgUniqueViolation && constraint == "discarded_request_key":
		return ErrRequestDiscarded
	}
	return fmt.Errorf("%s: %w", op, err)
}

const titleColumns = `t.id, t.kind, t.name, t.original_title, t.polish_title, t.year,
	t.tmdb_id, t.authors, t.bn_id, t.openlibrary_id, t.notes, t.cover_url, t.created_at, t.updated_at,
	(SELECT count(*) FROM edition e WHERE e.title_id = t.id),
	(SELECT count(*) FROM copy c JOIN edition e ON e.id = c.edition_id WHERE e.title_id = t.id)`

func scanTitle(row pgx.Row) (Title, error) {
	var t Title
	err := row.Scan(&t.ID, &t.Kind, &t.Name, &t.OriginalTitle, &t.PolishTitle, &t.Year,
		&t.TMDBID, &t.Authors, &t.BNID, &t.OpenLibraryID, &t.Notes, &t.CoverURL, &t.CreatedAt, &t.UpdatedAt,
		&t.EditionCount, &t.CopyCount)
	return t, err
}

const editionCoverOverrideURL = `CASE WHEN e.cover_photo IS NOT NULL
	THEN '/api/editions/' || e.id || '/cover?v=' || extract(epoch FROM e.updated_at) END`

// CoverURL selects the cover a copy shows from edition e and title t: the
// edition's own photo, then the edition's cover, then the title's.
const CoverURL = `COALESCE(` + editionCoverOverrideURL + `, e.cover_url, t.cover_url)`

const editionColumns = `e.id, e.title_id, e.barcode, e.label, e.publisher, e.year, e.format,
	e.pl_subtitles, e.pl_voiceover, e.packaging, e.binding, e.discogs_release_id, e.notes, e.cover_url,
	e.created_at, e.updated_at, (SELECT count(*) FROM copy c WHERE c.edition_id = e.id), ` + editionCoverOverrideURL

func scanEdition(row pgx.Row) (Edition, error) {
	var e Edition
	err := row.Scan(&e.ID, &e.TitleID, &e.Barcode, &e.Label, &e.Publisher, &e.Year, &e.Format,
		&e.PLSubtitles, &e.PLVoiceover, &e.Packaging, &e.Binding, &e.DiscogsReleaseID, &e.Notes, &e.CoverURL,
		&e.CreatedAt, &e.UpdatedAt, &e.CopyCount, &e.CoverOverrideURL)
	return e, err
}

const copyColumns = `c.id, c.edition_id, c.status, c.price_cents, c.purchased_on, c.shop,
	c.acquired_as, c.condition, c.notes, c.max_price_cents, c.max_market_percent, c.sold_on,
	c.sale_price_cents, c.location, c.expected_on, c.created_at, c.updated_at`

func scanCopy(row pgx.Row) (Copy, error) {
	var c Copy
	var purchased, sold, expected *time.Time
	err := row.Scan(&c.ID, &c.EditionID, &c.Status, &c.PriceCents, &purchased, &c.Shop,
		&c.AcquiredAs, &c.Condition, &c.Notes, &c.MaxPriceCents, &c.MaxMarketPercent, &sold,
		&c.SalePriceCents, &c.Location, &expected, &c.CreatedAt, &c.UpdatedAt)
	c.PurchasedOn = dateOf(purchased)
	c.SoldOn = dateOf(sold)
	c.ExpectedOn = dateOf(expected)
	return c, err
}

// dateOf carries a nullable date column as a Date.

func collect[T any](rows pgx.Rows, scan func(pgx.Row) (T, error), op string) ([]T, error) {
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("%s: scan: %w", op, err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", op, err)
	}
	return out, nil
}

// TitleFilter narrows ListTitles; zero fields match every title.
type TitleFilter struct {
	Kind   string
	Query  string
	TMDBID int64
}

// ListTitles returns titles of f.Kind whose names or authors contain f.Query
// and, when set, with TMDB ID f.TMDBID, by name.
func (s *Store) ListTitles(ctx context.Context, f TitleFilter) ([]Title, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+titleColumns+` FROM title t
		WHERE ($1 = '' OR t.kind = $1)
		  AND ($2 = '' OR strpos(lower(concat_ws(' ', t.name, t.original_title, t.polish_title,
		       array_to_string(t.authors, ' '))), lower($2)) > 0)
		  AND ($3 = 0 OR t.tmdb_id = $3)
		ORDER BY lower(t.name), t.id`, f.Kind, strings.TrimSpace(f.Query), f.TMDBID)
	if err != nil {
		return nil, fmt.Errorf("list titles: %w", err)
	}
	return collect(rows, scanTitle, "list titles")
}

// rowQuerier reads rows from the pool or from a transaction.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// GetTitle returns one title or ErrTitleNotFound.
func (s *Store) GetTitle(ctx context.Context, id int64) (Title, error) {
	return getTitle(ctx, s.pool, id)
}

func getTitle(ctx context.Context, q rowQuerier, id int64) (Title, error) {
	t, err := scanTitle(q.QueryRow(ctx, `SELECT `+titleColumns+` FROM title t WHERE t.id = $1`, id))
	return t, mapErr(err, ErrTitleNotFound, "get title")
}

// CreateTitle inserts a normalized title. With a request key already used,
// it returns that key's title and false instead of inserting another.
func (s *Store) CreateTitle(ctx context.Context, in *TitleInput) (Title, bool, error) {
	var t Title
	created := true
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := claimRequestKey(ctx, tx, in.RequestKey); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO title
			(kind, name, original_title, polish_title, year, tmdb_id, authors, bn_id, openlibrary_id, notes,
			cover_url, request_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, ''), $12)
			ON CONFLICT (request_key) DO NOTHING RETURNING id`,
			in.Kind, in.Name, in.OriginalTitle, in.PolishTitle, in.Year, in.TMDBID, in.Authors,
			in.BNID, in.OpenLibraryID, in.Notes, in.CoverURL, in.RequestKey).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) && in.RequestKey != nil {
			created = false
			err = tx.QueryRow(ctx, `SELECT id FROM title WHERE request_key = $1`, *in.RequestKey).Scan(&id)
			if err != nil {
				return mapErr(err, ErrTitleNotFound, "replay title")
			}
		} else if err != nil {
			return mapErr(err, ErrTitleNotFound, "create title")
		}
		t, err = getTitle(ctx, tx, id)
		return err
	})
	if err != nil {
		return Title{}, false, err
	}
	return t, created, nil
}

// UpdateTitle replaces a title. The kind cannot change while editions exist,
// since their kind-specific fields would no longer apply.
func (s *Store) UpdateTitle(ctx context.Context, id int64, in *TitleInput) (Title, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var kind string
		err := tx.QueryRow(ctx, `SELECT kind FROM title WHERE id = $1 FOR UPDATE`, id).Scan(&kind)
		if err != nil {
			return mapErr(err, ErrTitleNotFound, "lock title")
		}
		if kind != in.Kind {
			if err := checkNoEditions(ctx, tx, id); err != nil {
				return err
			}
		}
		_, err = tx.Exec(ctx, `UPDATE title SET kind = $2, name = $3, original_title = $4,
			polish_title = $5, year = $6, tmdb_id = $7, authors = $8, bn_id = $9,
			openlibrary_id = $10, notes = $11, cover_url = NULLIF(COALESCE($12, cover_url), ''),
			updated_at = now(), edited_at = now() WHERE id = $1`,
			id, in.Kind, in.Name, in.OriginalTitle, in.PolishTitle, in.Year, in.TMDBID, in.Authors,
			in.BNID, in.OpenLibraryID, in.Notes, in.CoverURL)
		return mapErr(err, ErrTitleNotFound, "update title")
	})
	if err != nil {
		return Title{}, err
	}
	return s.GetTitle(ctx, id)
}

// checkNoEditions runs after the title row is locked, as its own statement:
// under READ COMMITTED a count taken in the locking statement keeps the
// snapshot from before the wait and misses an edition committed meanwhile.
func checkNoEditions(ctx context.Context, tx pgx.Tx, titleID int64) error {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM edition WHERE title_id = $1)`, titleID).Scan(&exists)
	if err != nil {
		return fmt.Errorf("count editions: %w", err)
	}
	if exists {
		return ErrKindLocked
	}
	return nil
}

// DeleteTitle removes a title without editions; ErrHasEditions otherwise.
func (s *Store) DeleteTitle(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM title WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err, "edition_title_id_fkey") {
			return ErrHasEditions
		}
		return mapErr(err, ErrTitleNotFound, "delete title")
	}
	if tag.RowsAffected() == 0 {
		return ErrTitleNotFound
	}
	return nil
}

// ListEditions returns editions of titleID (all when 0) with barcode (any
// when empty).
func (s *Store) ListEditions(ctx context.Context, titleID int64, code string) ([]Edition, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+editionColumns+` FROM edition e
		WHERE ($1 = 0 OR e.title_id = $1) AND ($2 = '' OR e.barcode = $2)
		ORDER BY e.year NULLS LAST, e.id`, titleID, code)
	if err != nil {
		return nil, fmt.Errorf("list editions: %w", err)
	}
	return collect(rows, scanEdition, "list editions")
}

// GetEdition returns one edition or ErrEditionNotFound.
func (s *Store) GetEdition(ctx context.Context, id int64) (Edition, error) {
	return getEdition(ctx, s.pool, id)
}

func getEdition(ctx context.Context, q rowQuerier, id int64) (Edition, error) {
	e, err := scanEdition(q.QueryRow(ctx, `SELECT `+editionColumns+` FROM edition e WHERE e.id = $1`, id))
	return e, mapErr(err, ErrEditionNotFound, "get edition")
}

// lockTitleKind share-locks the title so its kind cannot change until the
// transaction ends, checks the edition against it and returns the kind.
func lockTitleKind(ctx context.Context, tx pgx.Tx, in *EditionInput) (string, error) {
	var kind string
	err := tx.QueryRow(ctx, `SELECT kind FROM title WHERE id = $1 FOR SHARE`, in.TitleID).Scan(&kind)
	if err != nil {
		return "", mapErr(err, ErrTitleNotFound, "lock title")
	}
	if fe := in.CheckKind(kind); fe != nil {
		return "", fe
	}
	return kind, nil
}

// CreateEdition inserts a normalized edition. Errors: ErrTitleNotFound,
// ErrDuplicateBarcode or a *httpx.FieldError for kind-specific fields. With a
// request key already used, it returns that key's edition and false instead
// of inserting another.
func (s *Store) CreateEdition(ctx context.Context, in *EditionInput) (Edition, bool, error) {
	var e Edition
	created := true
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		// The request key's lock comes first, as in DiscardRequest, which then locks the title:
		// taking the title first would deadlock against it. A discarded key is refused before
		// the title is checked, which the discard may have removed.
		if err := claimRequestKey(ctx, tx, in.RequestKey); err != nil {
			return err
		}
		if _, err := lockTitleKind(ctx, tx, in); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO edition (title_id, barcode, label, publisher, year,
			format, pl_subtitles, pl_voiceover, packaging, binding, discogs_release_id, notes, cover_url,
			request_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, ''), $14)
			ON CONFLICT (request_key) DO NOTHING RETURNING id`,
			in.TitleID, in.Barcode, in.Label, in.Publisher, in.Year, in.Format, in.PLSubtitles,
			in.PLVoiceover, in.Packaging, in.Binding, in.DiscogsReleaseID, in.Notes, in.CoverURL,
			in.RequestKey).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) && in.RequestKey != nil {
			created = false
			err = tx.QueryRow(ctx, `SELECT id FROM edition WHERE request_key = $1`, *in.RequestKey).Scan(&id)
			if err != nil {
				return mapErr(err, ErrEditionNotFound, "replay edition")
			}
		} else if err != nil {
			return mapErr(err, ErrEditionNotFound, "create edition")
		}
		e, err = getEdition(ctx, tx, id)
		return err
	})
	if err != nil {
		return Edition{}, false, err
	}
	return e, created, nil
}

// UpdateEdition replaces an edition, which may move to another title of the
// same kind only: its copies would otherwise change kind with it.
func (s *Store) UpdateEdition(ctx context.Context, id int64, in *EditionInput) (Edition, error) {
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var current string
		err := tx.QueryRow(ctx, `SELECT t.kind FROM edition e JOIN title t ON t.id = e.title_id
			WHERE e.id = $1 FOR SHARE`, id).Scan(&current)
		if err != nil {
			return mapErr(err, ErrEditionNotFound, "lock edition")
		}
		target, err := lockTitleKind(ctx, tx, in)
		if err != nil {
			return err
		}
		if target != current {
			return fieldErr("title_id", "must be a title of the same kind")
		}
		tag, err := tx.Exec(ctx, `UPDATE edition SET title_id = $2, barcode = $3, label = $4,
			publisher = $5, year = $6, format = $7, pl_subtitles = $8, pl_voiceover = $9,
			packaging = $10, binding = $11, discogs_release_id = $12, notes = $13,
			cover_url = NULLIF(COALESCE($14, cover_url), ''), updated_at = now(), edited_at = now()
			WHERE id = $1`,
			id, in.TitleID, in.Barcode, in.Label, in.Publisher, in.Year, in.Format, in.PLSubtitles,
			in.PLVoiceover, in.Packaging, in.Binding, in.DiscogsReleaseID, in.Notes, in.CoverURL)
		if err != nil {
			return mapErr(err, ErrEditionNotFound, "update edition")
		}
		if tag.RowsAffected() == 0 {
			return ErrEditionNotFound
		}
		return nil
	})
	if err != nil {
		return Edition{}, err
	}
	return s.GetEdition(ctx, id)
}

func (s *Store) fillEditionCover(ctx context.Context, edition *Edition, url string) error {
	_, err := s.pool.Exec(ctx, `UPDATE edition SET cover_url = $2, updated_at = now()
		WHERE id = $1 AND updated_at = $3 AND cover_url IS NULL AND cover_photo IS NULL`,
		edition.ID, url, edition.UpdatedAt)
	if err != nil {
		return fmt.Errorf("fill edition cover: %w", err)
	}
	return nil
}

// DeleteEdition removes an edition without copies; ErrHasCopies otherwise.
// With requeue, the scan queue entry the edition took out of the queue goes
// back in the same transaction, with its scans and times, as when the add
// that created the edition is undone. A repeat finds the edition gone, so the
// entry never goes back twice.
func (s *Store) DeleteEdition(ctx context.Context, id int64, requeue bool) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		var entry resolvedEntry
		queued := false
		if requeue {
			var err error
			entry, queued, err = resolvedQueueEntry(ctx, tx, id)
			if err != nil {
				return err
			}
		}
		tag, err := tx.Exec(ctx, `DELETE FROM edition WHERE id = $1`, id)
		if err != nil {
			if isFKViolation(err, "copy_edition_id_fkey") {
				return ErrHasCopies
			}
			return mapErr(err, ErrEditionNotFound, "delete edition")
		}
		if tag.RowsAffected() == 0 {
			return ErrEditionNotFound
		}
		if !queued {
			return nil
		}
		_, err = tx.Exec(ctx, `INSERT INTO scan_queue
			(barcode, reason, detail, scans, created_at, last_scanned_at)
			VALUES ($1, $2, $3, $4, $5, $6) ON CONFLICT (barcode) DO NOTHING`,
			entry.Barcode, entry.Reason, entry.Detail, entry.Scans, entry.CreatedAt, entry.LastScannedAt)
		if err != nil {
			return fmt.Errorf("requeue %s: %w", entry.Barcode, err)
		}
		return nil
	})
}

// resolvedEntry is a scan queue entry as an edition took it out of the queue.
type resolvedEntry struct {
	Barcode, Reason, Detail  string
	Scans                    int
	CreatedAt, LastScannedAt time.Time
}

// resolvedQueueEntry is the entry the edition took out of the queue under
// its current barcode, locked against a concurrent scan of that barcode; the
// bool is false when there is none.
func resolvedQueueEntry(ctx context.Context, tx pgx.Tx, editionID int64) (resolvedEntry, bool, error) {
	var e resolvedEntry
	err := tx.QueryRow(ctx, `SELECT r.barcode, r.reason, r.detail, r.scans, r.created_at,
		r.last_scanned_at FROM scan_queue_resolved r
		JOIN edition e ON e.id = r.edition_id AND e.barcode = r.barcode
		WHERE r.edition_id = $1`, editionID).Scan(
		&e.Barcode, &e.Reason, &e.Detail, &e.Scans, &e.CreatedAt, &e.LastScannedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return resolvedEntry{}, false, nil
	}
	if err != nil {
		return resolvedEntry{}, false, fmt.Errorf("read resolved queue entry: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT scan_queue_lock($1)`, e.Barcode); err != nil {
		return resolvedEntry{}, false, fmt.Errorf("lock queued barcode: %w", err)
	}
	return e, true, nil
}

// editionCover returns edition id's photo and its version, the `v` that
// editionCoverOverrideURL puts in the photo's URL.
func (s *Store) editionCover(ctx context.Context, id int64) ([]byte, string, error) {
	var photo []byte
	var version string
	err := s.pool.QueryRow(ctx, `SELECT cover_photo, extract(epoch FROM updated_at)::text
		FROM edition WHERE id = $1 AND cover_photo IS NOT NULL`, id).
		Scan(&photo, &version)
	return photo, version, mapErr(err, ErrEditionNotFound, "get edition cover")
}

func (s *Store) setEditionCover(ctx context.Context, id int64, photo []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE edition SET cover_photo = $2, updated_at = clock_timestamp(),
		edited_at = clock_timestamp() WHERE id = $1`,
		id, photo)
	if err != nil {
		return fmt.Errorf("set edition cover: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEditionNotFound
	}
	return nil
}

// ListCopies returns the copies of editionID.
func (s *Store) ListCopies(ctx context.Context, editionID int64) ([]Copy, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+copyColumns+` FROM copy c
		WHERE c.edition_id = $1 ORDER BY c.id`, editionID)
	if err != nil {
		return nil, fmt.Errorf("list copies: %w", err)
	}
	return collect(rows, scanCopy, "list copies")
}

// GetCopy returns one copy or ErrNotFound.
func (s *Store) GetCopy(ctx context.Context, id int64) (Copy, error) {
	return getCopy(ctx, s.pool, id)
}

func getCopy(ctx context.Context, q rowQuerier, id int64) (Copy, error) {
	c, err := scanCopy(q.QueryRow(ctx, `SELECT `+copyColumns+` FROM copy c WHERE c.id = $1`, id))
	return c, mapErr(err, ErrNotFound, "get copy")
}

// timeOf is the column value of an optional date.

// CreateCopy inserts a normalized copy; ErrEditionNotFound when its edition
// does not exist. With a request key already used, it returns that key's copy
// and false instead of inserting another.
func (s *Store) CreateCopy(ctx context.Context, in *CopyInput) (Copy, bool, error) {
	var c Copy
	created := true
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if err := claimRequestKey(ctx, tx, in.RequestKey); err != nil {
			return err
		}
		var id int64
		err := tx.QueryRow(ctx, `INSERT INTO copy (edition_id, status, price_cents, purchased_on,
			shop, acquired_as, condition, notes, max_price_cents, max_market_percent, sold_on,
			sale_price_cents, location, expected_on, request_key)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
			ON CONFLICT (request_key) DO NOTHING RETURNING id`,
			in.EditionID, in.Status, in.PriceCents, timeOf(in.PurchasedOn), in.Shop, in.AcquiredAs,
			in.Condition, in.Notes, in.MaxPriceCents, in.MaxMarketPercent, timeOf(in.SoldOn),
			in.SalePriceCents, in.Location, timeOf(in.ExpectedOn), in.RequestKey).Scan(&id)
		switch {
		case errors.Is(err, pgx.ErrNoRows) && in.RequestKey != nil:
			created = false
			err = tx.QueryRow(ctx, `SELECT id FROM copy WHERE request_key = $1`, *in.RequestKey).Scan(&id)
			if err != nil {
				return mapErr(err, ErrNotFound, "replay copy")
			}
		case isFKViolation(err, "copy_edition_id_fkey"):
			return ErrEditionNotFound
		case err != nil:
			return mapErr(err, ErrNotFound, "create copy")
		}
		c, err = getCopy(ctx, tx, id)
		return err
	})
	if err != nil {
		return Copy{}, false, err
	}
	return c, created, nil
}

// UpdateCopy replaces a copy and returns it as this update left it, so an
// answer never shows what a concurrent update wrote. A lent copy stays owned
// (ErrLentCopyStays) until it comes back: only owned copies are lent.
func (s *Store) UpdateCopy(ctx context.Context, id int64, in *CopyInput) (Copy, error) {
	var c Copy
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if in.Status != StatusOwned {
			if err := checkNotLent(ctx, tx, id); err != nil {
				return err
			}
		}
		var err error
		c, err = scanCopy(tx.QueryRow(ctx, `UPDATE copy c SET edition_id = $2, status = $3, price_cents = $4,
			purchased_on = $5, shop = $6, acquired_as = $7, condition = $8, notes = $9,
			max_price_cents = $10, max_market_percent = $11, sold_on = $12, sale_price_cents = $13,
			location = $14, expected_on = $15, updated_at = now() WHERE c.id = $1 RETURNING `+copyColumns,
			id, in.EditionID, in.Status, in.PriceCents, timeOf(in.PurchasedOn), in.Shop, in.AcquiredAs,
			in.Condition, in.Notes, in.MaxPriceCents, in.MaxMarketPercent, timeOf(in.SoldOn),
			in.SalePriceCents, in.Location, timeOf(in.ExpectedOn)))
		if isFKViolation(err, "copy_edition_id_fkey") {
			return ErrEditionNotFound
		}
		if err != nil {
			return mapErr(err, ErrNotFound, "update copy")
		}
		return nil
	})
	if err != nil {
		return Copy{}, err
	}
	return c, nil
}

// lockCopy locks copy id against a concurrent loan or status change and
// returns its status; ErrNotFound when there is none.
func lockCopy(ctx context.Context, tx pgx.Tx, id int64) (string, error) {
	var status string
	err := tx.QueryRow(ctx, `SELECT status FROM copy WHERE id = $1 FOR UPDATE`, id).Scan(&status)
	return status, mapErr(err, ErrNotFound, "lock copy")
}

// checkNotLent locks the copy, then looks for its open loan as its own
// statement, as checkNoEditions does, so it sees a loan committed while the
// lock waited.
func checkNotLent(ctx context.Context, tx pgx.Tx, id int64) error {
	if _, err := lockCopy(ctx, tx, id); err != nil {
		return err
	}
	var lent bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM loan WHERE copy_id = $1 AND returned_on IS NULL)`, id).
		Scan(&lent)
	if err != nil {
		return fmt.Errorf("check open loan: %w", err)
	}
	if lent {
		return ErrLentCopyStays
	}
	return nil
}

const loanColumns = `l.id, l.copy_id, l.borrower, l.lent_on, l.due_on, l.returned_on, l.created_at, l.updated_at`

func scanLoan(row pgx.Row) (Loan, error) {
	var l Loan
	var due, returned *time.Time
	err := row.Scan(&l.ID, &l.CopyID, &l.Borrower, &l.LentOn.Time, &due, &returned, &l.CreatedAt, &l.UpdatedAt)
	l.DueOn = dateOf(due)
	l.ReturnedOn = dateOf(returned)
	return l, err
}

func dateOf(t *time.Time) *Date {
	if t == nil {
		return nil
	}
	return &Date{*t}
}

func timeOf(d *Date) *time.Time {
	if d == nil {
		return nil
	}
	return &d.Time
}

func getLoan(ctx context.Context, q rowQuerier, id int64) (Loan, error) {
	l, err := scanLoan(q.QueryRow(ctx, `SELECT `+loanColumns+` FROM loan l WHERE l.id = $1`, id))
	return l, mapErr(err, ErrNotFound, "get loan")
}

// ListLoans returns the loans of copyID, newest first, returned ones included.
func (s *Store) ListLoans(ctx context.Context, copyID int64) ([]Loan, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+loanColumns+` FROM loan l
		WHERE l.copy_id = $1 ORDER BY l.lent_on DESC, l.id DESC`, copyID)
	if err != nil {
		return nil, fmt.Errorf("list loans: %w", err)
	}
	return collect(rows, scanLoan, "list loans")
}

// LendCopy opens a loan of owned copy copyID, lent today in Poland unless
// in names the day. Errors: ErrNotFound, ErrNotOwned, ErrAlreadyLent or a
// *httpx.FieldError for a due date before that day.
func (s *Store) LendCopy(ctx context.Context, copyID int64, in *LoanInput) (Loan, error) {
	var l Loan
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		status, err := lockCopy(ctx, tx, copyID)
		if err != nil {
			return err
		}
		if status != StatusOwned {
			return ErrNotOwned
		}
		var lentOn time.Time
		err = tx.QueryRow(ctx, `SELECT COALESCE($1::date, (now() AT TIME ZONE 'Europe/Warsaw')::date)`,
			timeOf(in.LentOn)).Scan(&lentOn)
		if err != nil {
			return fmt.Errorf("lent on: %w", err)
		}
		if fe := checkDue(in.DueOn, lentOn); fe != nil {
			return fe
		}
		var id int64
		err = tx.QueryRow(ctx, `INSERT INTO loan (copy_id, borrower, lent_on, due_on)
			VALUES ($1, $2, $3, $4) RETURNING id`, copyID, in.Borrower, lentOn, timeOf(in.DueOn)).Scan(&id)
		if code, constraint := pgCode(err); code == pgUniqueViolation && constraint == "loan_open_key" {
			return ErrAlreadyLent
		}
		if err != nil {
			return fmt.Errorf("lend copy: %w", err)
		}
		l, err = getLoan(ctx, tx, id)
		return err
	})
	if err != nil {
		return Loan{}, err
	}
	return l, nil
}

// ReturnLoan records the day loan id came back. A loan already back keeps
// its day: the same day again answers it unchanged, another one is
// ErrReturned, so a stale "Zwrócono" cannot move it. Errors: ErrNotFound,
// ErrReturned or a *httpx.FieldError for a day before the loan began,
// checked first, whether the loan is out or back.
func (s *Store) ReturnLoan(ctx context.Context, id int64, returnedOn time.Time) (Loan, error) {
	var l Loan
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		var lentOn time.Time
		var returned *time.Time
		err := tx.QueryRow(ctx, `SELECT lent_on, returned_on FROM loan WHERE id = $1 FOR UPDATE`, id).
			Scan(&lentOn, &returned)
		if err != nil {
			return mapErr(err, ErrNotFound, "lock loan")
		}
		if returnedOn.Before(lentOn) {
			return fieldErr("returned_on", "cannot be before lent_on")
		}
		if returned != nil {
			if !returned.Equal(returnedOn) {
				return ErrReturned
			}
			l, err = getLoan(ctx, tx, id)
			return err
		}
		_, err = tx.Exec(ctx, `UPDATE loan SET returned_on = $2, updated_at = now() WHERE id = $1`, id, returnedOn)
		if err != nil {
			return fmt.Errorf("return loan: %w", err)
		}
		l, err = getLoan(ctx, tx, id)
		return err
	})
	if err != nil {
		return Loan{}, err
	}
	return l, nil
}

// DeleteCopy removes a copy.
func (s *Store) DeleteCopy(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM copy WHERE id = $1`, id)
	if err != nil {
		return mapErr(err, ErrNotFound, "delete copy")
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DiscardRequest removes what the creates sent with key made, for an add
// that failed: its copy unless someone edited it, then its edition while it has no barcode (the
// scanner's undo puts those back in the queue), no other copy and no change
// by a person, then its title while it has no edition and no change by a
// person. Rows someone else uses or changed stay. The key is recorded, so a
// create that arrives with it later is refused (ErrRequestDiscarded).
func (s *Store) DiscardRequest(ctx context.Context, key string) error {
	return s.inTx(ctx, func(tx pgx.Tx) error {
		// The same lock the creates take: a create with this key either committed
		// before, and its rows are seen below, or waits and finds the key discarded.
		if err := lockRequestKey(ctx, tx, &key); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO discarded_request (request_key) VALUES ($1)
			ON CONFLICT DO NOTHING`, key); err != nil {
			return fmt.Errorf("record discarded key: %w", err)
		}
		// A copy someone edited or lent since the add made it is theirs now. Its row is locked
		// before the loan check, as LendCopy locks it, so a loan committed meanwhile is seen.
		err := discardUnused(ctx, tx, key,
			`SELECT id FROM copy WHERE request_key = $1 AND updated_at = created_at FOR UPDATE`,
			`SELECT EXISTS (SELECT 1 FROM loan WHERE copy_id = $1)`,
			`DELETE FROM copy WHERE id = $1`)
		if err != nil {
			return fmt.Errorf("discard copy: %w", err)
		}
		err = discardUnused(ctx, tx, key,
			`SELECT id FROM edition WHERE request_key = $1 AND barcode IS NULL AND edited_at IS NULL FOR UPDATE`,
			`SELECT EXISTS (SELECT 1 FROM copy WHERE edition_id = $1)`,
			`DELETE FROM edition WHERE id = $1`)
		if err != nil {
			return fmt.Errorf("discard edition: %w", err)
		}
		err = discardUnused(ctx, tx, key,
			`SELECT id FROM title WHERE request_key = $1 AND edited_at IS NULL FOR UPDATE`,
			`SELECT EXISTS (SELECT 1 FROM edition WHERE title_id = $1)`,
			`DELETE FROM title WHERE id = $1`)
		if err != nil {
			return fmt.Errorf("discard title: %w", err)
		}
		return nil
	})
}

// claimRequestKey takes key's lock for a create and refuses a key a discard
// gave up (ErrRequestDiscarded) before the create checks anything else. The
// create then reads its row back under the lock, so a discard cannot remove
// it between the insert and the answer. No key, no lock.
func claimRequestKey(ctx context.Context, tx pgx.Tx, key *string) error {
	if key == nil {
		return nil
	}
	if err := lockRequestKey(ctx, tx, key); err != nil {
		return err
	}
	var discarded bool
	err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM discarded_request WHERE request_key = $1)`, *key).
		Scan(&discarded)
	if err != nil {
		return fmt.Errorf("check discarded key: %w", err)
	}
	if discarded {
		return ErrRequestDiscarded
	}
	return nil
}

// lockRequestKey takes the transaction lock the request_key_guard trigger
// takes for key; none for a create without one.
func lockRequestKey(ctx context.Context, tx pgx.Tx, key *string) error {
	if key == nil {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('request:' || $1, 0))`, *key); err != nil {
		return fmt.Errorf("lock request key: %w", err)
	}
	return nil
}

// discardUnused deletes the row lock finds unless used says it is in use.
// The check runs after the lock as its own statement, as checkNoEditions
// does, so it sees a child committed while the lock waited.
func discardUnused(ctx context.Context, tx pgx.Tx, key, lock, used, remove string) error {
	var id int64
	err := tx.QueryRow(ctx, lock, key).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	var inUse bool
	if err := tx.QueryRow(ctx, used, id).Scan(&inUse); err != nil {
		return fmt.Errorf("check use: %w", err)
	}
	if inUse {
		return nil
	}
	if _, err := tx.Exec(ctx, remove, id); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	return nil
}

// Filter narrows the collection view; empty fields match everything.
// Location matches a copy's whole location, case aside. Query is the search
// as barcode.Normalize leaves it, Typed as the reader typed it: a location
// holding a code is found by the code as typed.
type Filter struct {
	Kind     string
	Format   string
	Status   string
	Location string
	Query    string
	Typed    string
	// Loaned keeps only copies with an open loan.
	Loaned bool
}

// foldCase lower-cases text one character at a time, the key locations
// match and merge by, with a final sigma as σ, since ΟΣ and ος name one
// place. The browser's foldLocation folds the same way, so the filter it
// applies to the loaded collection lists what this one does; Postgres's
// lower follows the database's older Unicode tables.
func foldCase(text string) string {
	return strings.Map(func(r rune) rune {
		if r = unicode.ToLower(r); r == 'ς' {
			return 'σ'
		}
		return r
	}, text)
}

// Item is one copy in the collection view with its edition and title.
type Item struct {
	CopyID        int64   `json:"copy_id"`
	CreatedAtUS   int64   `json:"created_at_us"`
	Status        string  `json:"status" ts:"CopyStatus"`
	PriceCents    *int64  `json:"price_cents"`
	MaxPriceCents *int64  `json:"max_price_cents"`
	PurchasedOn   *Date   `json:"purchased_on"`
	Shop          string  `json:"shop"`
	AcquiredAs    *string `json:"acquired_as" ts:"AcquiredAs"`
	Condition     *string `json:"condition" ts:"Condition"`
	// SoldOn and SalePriceCents are a sold copy's sale, ExpectedOn the day an
	// ordered copy should arrive.
	SoldOn         *Date    `json:"sold_on,omitempty" ts:",null"`
	SalePriceCents *int64   `json:"sale_price_cents,omitempty" ts:",null"`
	ExpectedOn     *Date    `json:"expected_on,omitempty" ts:",null"`
	Location       *string  `json:"location,omitempty" ts:",null"`
	EditionID      int64    `json:"edition_id"`
	Barcode        *string  `json:"barcode"`
	Label          string   `json:"label"`
	Format         *string  `json:"format" ts:"Format"`
	Binding        *string  `json:"binding" ts:"Binding"`
	TitleID        int64    `json:"title_id"`
	Kind           string   `json:"kind" ts:"Kind"`
	Name           string   `json:"name"`
	OriginalTitle  *string  `json:"original_title"`
	PolishTitle    *string  `json:"polish_title"`
	Year           *int     `json:"year"`
	CoverURL       *string  `json:"cover_url,omitempty"`
	Authors        []string `json:"authors,omitempty"`
	// LoanBorrower and LoanDueOn come from the copy's open loan, if any.
	LoanBorrower *string `json:"loan_borrower,omitempty"`
	LoanDueOn    *Date   `json:"loan_due_on,omitempty"`
}

func scanItem(row pgx.Row) (Item, error) {
	var it Item
	var createdAt time.Time
	var purchased, sold, due, expected *time.Time
	err := row.Scan(&it.CopyID, &createdAt, &it.Status, &it.PriceCents, &it.MaxPriceCents, &purchased, &it.Shop,
		&it.AcquiredAs, &it.Condition, &sold, &it.SalePriceCents, &expected, &it.Location, &it.EditionID, &it.Barcode, &it.Label, &it.Format, &it.Binding,
		&it.TitleID, &it.Kind, &it.Name, &it.OriginalTitle, &it.PolishTitle, &it.Year, &it.CoverURL, &it.Authors,
		&it.LoanBorrower, &due)
	it.CreatedAtUS = createdAt.UnixMicro()
	it.PurchasedOn = dateOf(purchased)
	it.SoldOn = dateOf(sold)
	it.LoanDueOn = dateOf(due)
	it.ExpectedOn = dateOf(expected)
	return it, err
}

// Collection returns every copy matching f, by title name. Without a status
// it lists sold copies too, so a client holding the whole collection can
// filter them itself.
func (s *Store) Collection(ctx context.Context, f Filter) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.created_at, c.status, c.price_cents, c.max_price_cents,
			c.purchased_on, c.shop, c.acquired_as, c.condition, c.sold_on, c.sale_price_cents, c.expected_on,
			c.location, e.id, e.barcode, e.label, e.format, e.binding,
			t.id, t.kind, t.name, t.original_title, t.polish_title, t.year,
			`+CoverURL+`, t.authors, l.borrower, l.due_on
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		LEFT JOIN loan l ON l.copy_id = c.id AND l.returned_on IS NULL
		WHERE ($1 = '' OR t.kind = $1) AND ($2 = '' OR e.format = $2) AND ($3 = '' OR c.status = $3)
		  AND ($4 = '' OR e.barcode = $4 OR strpos(lower(concat_ws(' ', t.name, t.original_title,
		       t.polish_title, array_to_string(t.authors, ' '), e.label, c.shop, c.location)), lower($4)) > 0
		       OR strpos(lower(c.location), lower($5)) > 0)
		  AND (NOT $6 OR l.id IS NOT NULL)
		ORDER BY lower(t.name), t.id, e.id, c.id`,
		f.Kind, f.Format, f.Status, strings.TrimSpace(f.Query), strings.TrimSpace(f.Typed), f.Loaned)
	if err != nil {
		return nil, fmt.Errorf("collection: %w", err)
	}
	items, err := collect(rows, scanItem, "collection")
	if err != nil {
		return nil, err
	}
	if place := foldCase(strings.TrimSpace(f.Location)); place != "" {
		items = slices.DeleteFunc(items, func(it Item) bool {
			return it.Location == nil || foldCase(*it.Location) != place
		})
	}
	return items, nil
}

// Locations returns the locations copies are stored at, each once whatever
// its letter case (foldCase), the most used first. A place spelled several
// ways goes by its most used spelling. Ties go in code point order, as the
// browser's locationsInUse orders them.
func (s *Store) Locations(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `SELECT location, count(*) FROM copy
		WHERE location IS NOT NULL GROUP BY location`)
	if err != nil {
		return nil, fmt.Errorf("locations: %w", err)
	}
	type spelling struct {
		text   string
		copies int
	}
	spellings, err := collect(rows, func(row pgx.Row) (spelling, error) {
		var sp spelling
		err := row.Scan(&sp.text, &sp.copies)
		return sp, err
	}, "locations")
	if err != nil {
		return nil, err
	}
	type place struct {
		key, name   string
		copies, top int
	}
	byKey := map[string]*place{}
	for _, sp := range spellings {
		key := foldCase(sp.text)
		p := byKey[key]
		if p == nil {
			p = &place{key: key}
			byKey[key] = p
		}
		p.copies += sp.copies
		if sp.copies > p.top || (sp.copies == p.top && sp.text < p.name) {
			p.name, p.top = sp.text, sp.copies
		}
	}
	places := make([]*place, 0, len(byKey))
	for _, p := range byKey {
		places = append(places, p)
	}
	slices.SortFunc(places, func(a, b *place) int {
		return cmp.Or(cmp.Compare(b.copies, a.copies), cmp.Compare(a.key, b.key))
	})
	names := make([]string, len(places))
	for i, p := range places {
		names[i] = p.name
	}
	return names, nil
}

func (s *Store) inTx(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
