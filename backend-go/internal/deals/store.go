package deals

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors the handler maps to HTTP status codes.
var (
	ErrNotFound         = errors.New("not found")
	ErrDuplicateKeyword = errors.New("this phrase is already on the list")
)

// MaxAttempts bounds Telegram deliveries per alert; after that the alert is
// reported as failed and no longer retried.
const MaxAttempts = 5

// sources are the price_observation sources this package writes.
var sources = []string{"olx", "vinted", "pepper", "ceneo"}

// Store is the Postgres access layer for deal hunting.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore wraps pool.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Wants returns every wishlist copy with its edition and title, oldest
// first.
func (s *Store) Wants(ctx context.Context) ([]Want, error) {
	rows, err := s.pool.Query(ctx, `SELECT c.id, e.id, t.id, t.kind, t.name, t.original_title,
			t.polish_title, t.authors, e.format, c.max_price_cents
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		WHERE c.status = 'wishlist' ORDER BY c.id`)
	if err != nil {
		return nil, fmt.Errorf("wants: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Want, error) {
		var w Want
		err := row.Scan(&w.CopyID, &w.EditionID, &w.TitleID, &w.Kind, &w.Name, &w.OriginalTitle,
			&w.PolishTitle, &w.Authors, &w.Format, &w.MaxPriceCents)
		return w, err
	})
}

// Keyword is one editable matcher phrase; Kind is "reject" or "accept".
type Keyword struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Phrase    string    `json:"phrase"`
	CreatedAt time.Time `json:"created_at"`
}

// Keywords returns every phrase by kind and phrase.
func (s *Store) Keywords(ctx context.Context) ([]Keyword, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, phrase, created_at FROM deal_keyword
		ORDER BY kind, lower(phrase), id`)
	if err != nil {
		return nil, fmt.Errorf("keywords: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Keyword])
}

// Rules returns the phrases as matcher rules.
func (s *Store) Rules(ctx context.Context) (Rules, error) {
	keywords, err := s.Keywords(ctx)
	if err != nil {
		return Rules{}, err
	}
	var r Rules
	for _, k := range keywords {
		if k.Kind == KeywordAccept {
			r.Accept = append(r.Accept, k.Phrase)
		} else {
			r.Reject = append(r.Reject, k.Phrase)
		}
	}
	return r, nil
}

// AddKeyword stores a normalized phrase; ErrDuplicateKeyword when the list
// already has it.
func (s *Store) AddKeyword(ctx context.Context, in *KeywordInput) (Keyword, error) {
	var k Keyword
	err := s.pool.QueryRow(ctx, `INSERT INTO deal_keyword (kind, phrase) VALUES ($1, $2)
		RETURNING id, kind, phrase, created_at`, in.Kind, in.Phrase).
		Scan(&k.ID, &k.Kind, &k.Phrase, &k.CreatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr != nil && pgErr.Code == "23505" {
		return Keyword{}, ErrDuplicateKeyword
	}
	if err != nil {
		return Keyword{}, fmt.Errorf("add keyword: %w", err)
	}
	return k, nil
}

// DeleteKeyword removes a phrase; ErrNotFound when it does not exist.
func (s *Store) DeleteKeyword(ctx context.Context, id int64) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM deal_keyword WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete keyword: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

type observationRaw struct {
	PhotoURL       string          `json:"photo_url"`
	WishlistCopyID int64           `json:"wishlist_copy_id,omitempty"`
	Title          string          `json:"title"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// Record stores l as an observation of w and, with alert, queues its
// Telegram alert in the same transaction. A listing a source already
// reported keeps its row and first observation time; its price and
// last_seen_at move to now. The alert is queued when the listing has none
// yet, so a listing the valuation run stored first, or one whose price
// dropped under the limit, still alerts once. It reports whether the
// listing was new and whether an alert was queued.
func (s *Store) Record(ctx context.Context, l *Listing, w *Want, alert bool) (bool, bool, error) {
	raw, err := json.Marshal(observationRaw{Title: l.Title, Data: l.Raw, PhotoURL: l.PhotoURL, WishlistCopyID: w.CopyID})
	if err != nil {
		return false, false, fmt.Errorf("record: encode raw: %w", err)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("record: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	var id int64
	var isNew, alerted bool
	err = tx.QueryRow(ctx, `INSERT INTO price_observation
			(source, external_id, price_cents, acquired_as, url, edition_id, title_id, raw)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL DO UPDATE SET
			price_cents = EXCLUDED.price_cents,
			last_seen_at = now(),
			url = EXCLUDED.url,
			raw = coalesce(price_observation.raw, '{}'::jsonb) || EXCLUDED.raw,
			acquired_as = coalesce(EXCLUDED.acquired_as, price_observation.acquired_as),
			edition_id = coalesce(price_observation.edition_id, EXCLUDED.edition_id),
			title_id = coalesce(price_observation.title_id, EXCLUDED.title_id)
		RETURNING id, xmax = 0`,
		l.Source, l.ExternalID, l.PriceCents, l.AcquiredAs, l.URL, w.EditionID, w.TitleID, raw).Scan(&id, &isNew)
	if err != nil {
		return false, false, fmt.Errorf("record observation: %w", err)
	}
	if alert {
		tag, err := tx.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id) VALUES ($1, $2)
			ON CONFLICT (observation_id) DO NOTHING`, id, w.CopyID)
		if err != nil {
			return false, false, fmt.Errorf("record alert: %w", err)
		}
		alerted = tag.RowsAffected() == 1
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("record: commit: %w", err)
	}
	return isNew, alerted, nil
}

// Alert is a queued Telegram message with what it reports.
type Alert struct {
	ObservationID int64
	Source        string
	URL           string
	ListingTitle  string
	PriceCents    int64
	AcquiredAs    *string
	TitleName     string
	Format        *string
	MaxPriceCents *int64
}

// PendingAlerts returns undelivered alerts with attempts left, oldest
// first. Alerts for copies no longer on the wishlist, alerts above the
// copy's current maximum price and alerts older than a week are skipped.
func (s *Store) PendingAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.source, o.url, coalesce(o.raw->>'title', ''),
			o.price_cents, o.acquired_as, t.name, e.format, c.max_price_cents
		FROM deal_alert a
		JOIN price_observation o ON o.id = a.observation_id
		JOIN copy c ON c.id = a.copy_id AND c.status = 'wishlist'
		JOIN edition e ON e.id = c.edition_id
		JOIN title t ON t.id = e.title_id
		WHERE a.sent_at IS NULL AND a.attempts < $1 AND a.created_at > now() - interval '7 days'
			AND o.price_cents <= c.max_price_cents
		ORDER BY a.created_at, a.observation_id`, MaxAttempts)
	if err != nil {
		return nil, fmt.Errorf("pending alerts: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Alert, error) {
		var a Alert
		err := row.Scan(&a.ObservationID, &a.Source, &a.URL, &a.ListingTitle, &a.PriceCents,
			&a.AcquiredAs, &a.TitleName, &a.Format, &a.MaxPriceCents)
		return a, err
	})
}

// MarkSent records a delivered alert.
func (s *Store) MarkSent(ctx context.Context, observationID int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE deal_alert SET sent_at = now(), attempts = attempts + 1,
		last_error = '' WHERE observation_id = $1`, observationID)
	if err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	return nil
}

// MarkFailed records a failed delivery attempt.
func (s *Store) MarkFailed(ctx context.Context, observationID int64, reason string) error {
	_, err := s.pool.Exec(ctx, `UPDATE deal_alert SET attempts = attempts + 1, last_error = $2
		WHERE observation_id = $1`, observationID, reason)
	if err != nil {
		return fmt.Errorf("mark failed: %w", err)
	}
	return nil
}

// Alert states in an Observation.
const (
	AlertNone    = "none"
	AlertPending = "pending"
	AlertSent    = "sent"
	AlertFailed  = "failed"
)

// Observation is one stored listing in the deals view.
type Observation struct {
	ID              int64     `json:"id"`
	Source          string    `json:"source"`
	ExternalID      string    `json:"external_id"`
	ObservedAt      time.Time `json:"observed_at"`
	PriceCents      int64     `json:"price_cents"`
	AcquiredAs      *string   `json:"acquired_as"`
	URL             string    `json:"url"`
	ListingTitle    string    `json:"listing_title"`
	TitleID         *int64    `json:"title_id"`
	TitleName       *string   `json:"title_name"`
	EditionID       *int64    `json:"edition_id"`
	Format          *string   `json:"format"`
	ListingPhotoURL *string   `json:"listing_photo_url"`
	CoverURL        *string   `json:"cover_url"`
	MaxPriceCents   *int64    `json:"max_price_cents"`
	Alert           string    `json:"alert"`
	AlertError      string    `json:"alert_error"`
	AlertAttempts   int       `json:"alert_attempts"`
}

// Recent returns the newest deal observations, at most limit.
func (s *Store) Recent(ctx context.Context, limit int) ([]Observation, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.source, coalesce(o.external_id, ''), o.observed_at,
			o.price_cents, o.acquired_as, o.url, coalesce(o.raw->>'title', ''), coalesce(e.title_id, o.title_id), t.name,
			e.id, e.format, nullif(o.raw->>'photo_url', ''),
            coalesce(CASE WHEN e.cover_photo IS NOT NULL
                THEN '/api/editions/' || e.id || '/cover?v=' || extract(epoch FROM e.updated_at) END,
                nullif(e.cover_url, ''), t.cover_url), w.max_price_cents,
			CASE WHEN a.observation_id IS NULL THEN 'none'
			     WHEN a.sent_at IS NOT NULL THEN 'sent'
			     WHEN a.attempts >= $2 THEN 'failed'
			     ELSE 'pending' END,
			coalesce(a.last_error, ''), coalesce(a.attempts, 0)
		FROM price_observation o
		LEFT JOIN deal_alert a ON a.observation_id = o.id
		LEFT JOIN LATERAL (
			SELECT coalesce((o.raw->>'wishlist_copy_id')::bigint, a.copy_id) AS copy_id
		) matched ON true
		LEFT JOIN LATERAL (
			SELECT c.edition_id, c.max_price_cents FROM copy c
			WHERE c.status = 'wishlist'
				AND (c.id = matched.copy_id OR (matched.copy_id IS NULL AND c.edition_id = o.edition_id))
			ORDER BY coalesce(c.max_price_cents >= o.price_cents, false) DESC, c.id
			LIMIT 1
		) w ON true
		LEFT JOIN edition e ON e.id = coalesce(w.edition_id, o.edition_id)
		LEFT JOIN title t ON t.id = coalesce(e.title_id, o.title_id)
		WHERE o.source = ANY ($3)
		ORDER BY o.observed_at DESC, o.id DESC LIMIT $1`, limit, MaxAttempts, sources)
	if err != nil {
		return nil, fmt.Errorf("recent observations: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Observation])
}
