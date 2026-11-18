package collection

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

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
	c.acquired_as, c.condition, c.notes, c.max_price_cents, c.created_at, c.updated_at`

func scanCopy(row pgx.Row) (Copy, error) {
	var c Copy
	var purchased *time.Time
	err := row.Scan(&c.ID, &c.EditionID, &c.Status, &c.PriceCents, &purchased, &c.Shop,
		&c.AcquiredAs, &c.Condition, &c.Notes, &c.MaxPriceCents, &c.CreatedAt, &c.UpdatedAt)
	if purchased != nil {
		c.PurchasedOn = &Date{*purchased}
	}
	return c, err
}

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

// GetTitle returns one title or ErrTitleNotFound.
func (s *Store) GetTitle(ctx context.Context, id int64) (Title, error) {
	t, err := scanTitle(s.pool.QueryRow(ctx, `SELECT `+titleColumns+` FROM title t WHERE t.id = $1`, id))
	return t, mapErr(err, ErrTitleNotFound, "get title")
}

// CreateTitle inserts a normalized title.
func (s *Store) CreateTitle(ctx context.Context, in *TitleInput) (Title, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO title
		(kind, name, original_title, polish_title, year, tmdb_id, authors, bn_id, openlibrary_id, notes, cover_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NULLIF($11, '')) RETURNING id`,
		in.Kind, in.Name, in.OriginalTitle, in.PolishTitle, in.Year, in.TMDBID, in.Authors,
		in.BNID, in.OpenLibraryID, in.Notes, in.CoverURL).Scan(&id)
	if err != nil {
		return Title{}, mapErr(err, ErrTitleNotFound, "create title")
	}
	return s.GetTitle(ctx, id)
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
			updated_at = now() WHERE id = $1`,
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
	e, err := scanEdition(s.pool.QueryRow(ctx, `SELECT `+editionColumns+` FROM edition e WHERE e.id = $1`, id))
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
// ErrDuplicateBarcode or a *httpx.FieldError for kind-specific fields.
func (s *Store) CreateEdition(ctx context.Context, in *EditionInput) (Edition, error) {
	var id int64
	err := s.inTx(ctx, func(tx pgx.Tx) error {
		if _, err := lockTitleKind(ctx, tx, in); err != nil {
			return err
		}
		err := tx.QueryRow(ctx, `INSERT INTO edition (title_id, barcode, label, publisher, year,
			format, pl_subtitles, pl_voiceover, packaging, binding, discogs_release_id, notes, cover_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NULLIF($13, '')) RETURNING id`,
			in.TitleID, in.Barcode, in.Label, in.Publisher, in.Year, in.Format, in.PLSubtitles,
			in.PLVoiceover, in.Packaging, in.Binding, in.DiscogsReleaseID, in.Notes, in.CoverURL).Scan(&id)
		return mapErr(err, ErrEditionNotFound, "create edition")
	})
	if err != nil {
		return Edition{}, err
	}
	return s.GetEdition(ctx, id)
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
			cover_url = NULLIF(COALESCE($14, cover_url), ''), updated_at = now() WHERE id = $1`,
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
func (s *Store) DeleteEdition(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM edition WHERE id = $1`, id)
	if err != nil {
		if isFKViolation(err, "copy_edition_id_fkey") {
			return ErrHasCopies
		}
		return mapErr(err, ErrEditionNotFound, "delete edition")
	}
	if tag.RowsAffected() == 0 {
		return ErrEditionNotFound
	}
	return nil
}

func (s *Store) editionCover(ctx context.Context, id int64) ([]byte, error) {
	var photo []byte
	err := s.pool.QueryRow(ctx, `SELECT cover_photo FROM edition WHERE id = $1 AND cover_photo IS NOT NULL`, id).
		Scan(&photo)
	return photo, mapErr(err, ErrEditionNotFound, "get edition cover")
}

func (s *Store) setEditionCover(ctx context.Context, id int64, photo []byte) error {
	tag, err := s.pool.Exec(ctx, `UPDATE edition SET cover_photo = $2, updated_at = clock_timestamp() WHERE id = $1`,
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
	c, err := scanCopy(s.pool.QueryRow(ctx, `SELECT `+copyColumns+` FROM copy c WHERE c.id = $1`, id))
	return c, mapErr(err, ErrNotFound, "get copy")
}

func purchasedOn(in *CopyInput) *time.Time {
	if in.PurchasedOn == nil {
		return nil
	}
	return &in.PurchasedOn.Time
}

// CreateCopy inserts a normalized copy; ErrEditionNotFound when its edition
// does not exist. With a request key already used, it returns that key's copy
// and false instead of inserting another.
func (s *Store) CreateCopy(ctx context.Context, in *CopyInput) (Copy, bool, error) {
	var id int64
	err := s.pool.QueryRow(ctx, `INSERT INTO copy (edition_id, status, price_cents, purchased_on,
		shop, acquired_as, condition, notes, max_price_cents, request_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (request_key) DO NOTHING RETURNING id`,
		in.EditionID, in.Status, in.PriceCents, purchasedOn(in), in.Shop, in.AcquiredAs,
		in.Condition, in.Notes, in.MaxPriceCents, in.RequestKey).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) && in.RequestKey != nil {
		err = s.pool.QueryRow(ctx, `SELECT id FROM copy WHERE request_key = $1`, *in.RequestKey).Scan(&id)
		if err != nil {
			return Copy{}, false, mapErr(err, ErrNotFound, "replay copy")
		}
		c, err := s.GetCopy(ctx, id)
		return c, false, err
	}
	if err != nil {
		if isFKViolation(err, "copy_edition_id_fkey") {
			return Copy{}, false, ErrEditionNotFound
		}
		return Copy{}, false, mapErr(err, ErrNotFound, "create copy")
	}
	c, err := s.GetCopy(ctx, id)
	return c, true, err
}

// UpdateCopy replaces a copy.
func (s *Store) UpdateCopy(ctx context.Context, id int64, in *CopyInput) (Copy, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE copy SET edition_id = $2, status = $3, price_cents = $4,
		purchased_on = $5, shop = $6, acquired_as = $7, condition = $8, notes = $9,
		max_price_cents = $10, updated_at = now() WHERE id = $1`,
		id, in.EditionID, in.Status, in.PriceCents, purchasedOn(in), in.Shop, in.AcquiredAs,
		in.Condition, in.Notes, in.MaxPriceCents)
	if err != nil {
		if isFKViolation(err, "copy_edition_id_fkey") {
			return Copy{}, ErrEditionNotFound
		}
		return Copy{}, mapErr(err, ErrNotFound, "update copy")
	}
	if tag.RowsAffected() == 0 {
		return Copy{}, ErrNotFound
	}
	return s.GetCopy(ctx, id)
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

// Filter narrows the collection view; empty fields match everything.
type Filter struct {
	Kind   string
	Format string
	Status string
	Query  string
}

// Item is one copy in the collection view with its edition and title.
type Item struct {
	CopyID        int64    `json:"copy_id"`
	CreatedAtUS   int64    `json:"created_at_us"`
	Status        string   `json:"status"`
	PriceCents    *int64   `json:"price_cents"`
	PurchasedOn   *Date    `json:"purchased_on"`
	Shop          string   `json:"shop"`
	AcquiredAs    *string  `json:"acquired_as"`
	Condition     *string  `json:"condition"`
	EditionID     int64    `json:"edition_id"`
	Barcode       *string  `json:"barcode"`
	Label         string   `json:"label"`
	Format        *string  `json:"format"`
	Binding       *string  `json:"binding"`
	TitleID       int64    `json:"title_id"`
	Kind          string   `json:"kind"`
	Name          string   `json:"name"`
	OriginalTitle *string  `json:"original_title"`
	PolishTitle   *string  `json:"polish_title"`
	Year          *int     `json:"year"`
	CoverURL      *string  `json:"cover_url,omitempty"`
	Authors       []string `json:"authors,omitempty"`
}

func scanItem(row pgx.Row) (Item, error) {
	var it Item
	var createdAt time.Time
	var purchased *time.Time
	err := row.Scan(&it.CopyID, &createdAt, &it.Status, &it.PriceCents, &purchased, &it.Shop, &it.AcquiredAs,
		&it.Condition, &it.EditionID, &it.Barcode, &it.Label, &it.Format, &it.Binding,
		&it.TitleID, &it.Kind, &it.Name, &it.OriginalTitle, &it.PolishTitle, &it.Year, &it.CoverURL, &it.Authors)
	it.CreatedAtUS = createdAt.UnixMicro()
	if purchased != nil {
		it.PurchasedOn = &Date{*purchased}
	}
	return it, err
}

// Collection returns every copy matching f, by title name.
func (s *Store) Collection(ctx context.Context, f Filter) ([]Item, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, c.created_at, c.status, c.price_cents, c.purchased_on, c.shop,
			c.acquired_as, c.condition, e.id, e.barcode, e.label, e.format, e.binding,
			t.id, t.kind, t.name, t.original_title, t.polish_title, t.year,
			COALESCE(`+editionCoverOverrideURL+`, e.cover_url, t.cover_url), t.authors
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE ($1 = '' OR t.kind = $1) AND ($2 = '' OR e.format = $2) AND ($3 = '' OR c.status = $3)
		  AND ($4 = '' OR e.barcode = $4 OR strpos(lower(concat_ws(' ', t.name, t.original_title,
		       t.polish_title, array_to_string(t.authors, ' '), e.label, c.shop)), lower($4)) > 0)
		ORDER BY lower(t.name), t.id, e.id, c.id`,
		f.Kind, f.Format, f.Status, strings.TrimSpace(f.Query))
	if err != nil {
		return nil, fmt.Errorf("collection: %w", err)
	}
	return collect(rows, scanItem, "collection")
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
