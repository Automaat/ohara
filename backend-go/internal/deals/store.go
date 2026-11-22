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

// alertWindow is how long an unsent alert stays worth sending.
const alertWindow = `interval '7 days'`

// sources are the price_observation sources this package writes.
var sources = []string{"olx", "pepper"}

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

// Titles returns every title, wished, owned or neither, for Outranks.
func (s *Store) Titles(ctx context.Context) (Titles, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, kind, name, original_title, polish_title, authors
		FROM title ORDER BY id`)
	if err != nil {
		return Titles{}, fmt.Errorf("titles: %w", err)
	}
	titles, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (Want, error) {
		var w Want
		err := row.Scan(&w.TitleID, &w.Kind, &w.Name, &w.OriginalTitle, &w.PolishTitle, &w.Authors)
		return w, err
	})
	if err != nil {
		return Titles{}, fmt.Errorf("titles: %w", err)
	}
	return NewTitles(titles), nil
}

// Keyword is one editable matcher phrase; Kind is "reject" or "accept".
type Keyword struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind" ts:"KeywordKind"`
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

// listingTitle is the title a stored listing belongs to: its edition's,
// else the one it was stored with.
const listingTitle = `coalesce((SELECT e.title_id FROM edition e WHERE e.id = price_observation.edition_id),
	price_observation.title_id)`

// movedTitle holds in Record's upsert when the run matched the stored
// listing to another title than the one it belongs to.
const movedTitle = listingTitle + ` <> EXCLUDED.title_id`

// Record stores l as an observation of w and, with alert, queues its
// Telegram alert in the same transaction. A listing a source already
// reported keeps its row and first observation time; its price and
// last_seen_at move to now. It keeps its edition too, unless w belongs to
// another title: then it moves to w's title and edition, and a valuation
// match, which names no wish, drops the wish the hunter recorded it for. An unsent alert
// for a wish of another title than the listing's moves to w, or to no wish
// when w is not one, so it is never sent for the old wish; an alert
// already sent stays recorded. The alert is queued when the listing has none
// yet, so a listing the valuation run stored first, or one whose price
// dropped under the limit, still alerts. A listing whose alert was sent
// alerts again when its price is under the one that alert named, unless
// an alert for it is still waiting to be sent: that one goes out at the
// current price. An alert that gave up or expired unsent is queued again
// only under the lowest price it was queued or tried at. An unsent alert
// whose wish is gone or no longer wished for moves to w, keeping its
// attempts and age. It reports whether the listing was new and whether an
// alert was queued.
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
	var titleID *int64
	err = tx.QueryRow(ctx, `INSERT INTO price_observation
			(source, external_id, price_cents, acquired_as, url, edition_id, title_id, raw)
		VALUES ($1, $2, $3, $4, $5, nullif($6::bigint, 0), nullif($7::bigint, 0), $8)
		ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL DO UPDATE SET
			price_cents = EXCLUDED.price_cents,
			last_seen_at = now(),
			url = EXCLUDED.url,
			raw = CASE WHEN `+movedTitle+` THEN coalesce(price_observation.raw, '{}'::jsonb) - 'wishlist_copy_id'
				ELSE coalesce(price_observation.raw, '{}'::jsonb) END || EXCLUDED.raw,
			acquired_as = coalesce(EXCLUDED.acquired_as, price_observation.acquired_as),
			edition_id = CASE WHEN `+movedTitle+` THEN EXCLUDED.edition_id
				ELSE coalesce(price_observation.edition_id, EXCLUDED.edition_id) END,
			title_id = CASE WHEN `+movedTitle+` THEN EXCLUDED.title_id
				ELSE coalesce(price_observation.title_id, EXCLUDED.title_id) END
		RETURNING id, xmax = 0, `+listingTitle,
		l.Source, l.ExternalID, l.PriceCents, l.AcquiredAs, l.URL, w.EditionID, w.TitleID, raw).Scan(&id, &isNew, &titleID)
	if err != nil {
		return false, false, fmt.Errorf("record observation: %w", err)
	}
	if titleID != nil && *titleID == w.TitleID {
		if _, err := tx.Exec(ctx, `UPDATE deal_alert a SET copy_id = nullif($3::bigint, 0)
			FROM copy c JOIN edition e ON e.id = c.edition_id
			WHERE a.observation_id = $1 AND a.sent_at IS NULL AND c.id = a.copy_id AND e.title_id <> $2`,
			id, w.TitleID, w.CopyID); err != nil {
			return false, false, fmt.Errorf("record alert: move to title: %w", err)
		}
	}
	if alert {
		if alerted, err = queueAlert(ctx, tx, id, w.CopyID, l.PriceCents); err != nil {
			return false, false, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("record: commit: %w", err)
	}
	return isNew, alerted, nil
}

// queueAlert queues the alert for observation id at priceCents for wishlist
// copy copyID, as Record describes, and reports whether it did.
func queueAlert(ctx context.Context, tx pgx.Tx, id, copyID, priceCents int64) (bool, error) {
	if _, err := tx.Exec(ctx, `UPDATE deal_alert SET copy_id = $2
		WHERE observation_id = $1 AND sent_at IS NULL AND copy_id IS DISTINCT FROM $2
			AND NOT EXISTS (SELECT FROM copy c WHERE c.id = deal_alert.copy_id AND c.status = 'wishlist')`,
		id, copyID); err != nil {
		return false, fmt.Errorf("record alert: rebind: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id, queued_price_cents) VALUES ($1, $2, $3)
		ON CONFLICT (observation_id) DO UPDATE SET copy_id = EXCLUDED.copy_id, created_at = now(),
			sent_at = NULL, attempts = 0, last_error = '', queued_price_cents = EXCLUDED.queued_price_cents
		WHERE $3 < deal_alert.alerted_price_cents AND (deal_alert.sent_at IS NOT NULL
			OR $3 < deal_alert.queued_price_cents
				AND (deal_alert.attempts >= $4 OR deal_alert.created_at <= now() - `+alertWindow+`))`,
		id, copyID, priceCents, MaxAttempts)
	if err != nil {
		return false, fmt.Errorf("record alert: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Alert is a queued Telegram message with what it reports.
type Alert struct {
	ObservationID int64
	Source        string
	ExternalID    string
	URL           string
	ListingTitle  string
	PriceCents    int64
	AcquiredAs    *string
	TitleName     string
	Format        *string
	MaxPriceCents *int64
	// AlertedPriceCents is the price the listing's last sent alert named;
	// nil before its first alert is sent.
	AlertedPriceCents *int64
}

// PendingAlerts returns undelivered alerts with attempts left, oldest
// first. Alerts for copies no longer on the wishlist, alerts above the
// copy's current maximum price, alerts older than a week and repeat alerts
// whose listing is no longer under the price its last sent alert named are
// skipped.
func (s *Store) PendingAlerts(ctx context.Context) ([]Alert, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.source, coalesce(o.external_id, ''), o.url, coalesce(o.raw->>'title', ''),
			o.price_cents, o.acquired_as, t.name, e.format, c.max_price_cents, a.alerted_price_cents
		FROM deal_alert a
		JOIN price_observation o ON o.id = a.observation_id
		JOIN copy c ON c.id = a.copy_id AND c.status = 'wishlist'
		JOIN edition e ON e.id = c.edition_id
		JOIN title t ON t.id = e.title_id
		WHERE a.sent_at IS NULL AND a.attempts < $1 AND a.created_at > now() - `+alertWindow+`
			AND o.price_cents <= c.max_price_cents
			AND (a.alerted_price_cents IS NULL OR o.price_cents < a.alerted_price_cents)
		ORDER BY a.created_at, a.observation_id`, MaxAttempts)
	if err != nil {
		return nil, fmt.Errorf("pending alerts: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Alert, error) {
		var a Alert
		err := row.Scan(&a.ObservationID, &a.Source, &a.ExternalID, &a.URL, &a.ListingTitle, &a.PriceCents,
			&a.AcquiredAs, &a.TitleName, &a.Format, &a.MaxPriceCents, &a.AlertedPriceCents)
		return a, err
	})
}

// MarkSent records a delivered alert and the price its message named.
func (s *Store) MarkSent(ctx context.Context, observationID, priceCents int64) error {
	_, err := s.pool.Exec(ctx, `UPDATE deal_alert SET sent_at = now(), attempts = attempts + 1,
		last_error = '', alerted_price_cents = $2 WHERE observation_id = $1`, observationID, priceCents)
	if err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	return nil
}

// MarkFailed records a failed delivery attempt at priceCents.
func (s *Store) MarkFailed(ctx context.Context, observationID, priceCents int64, reason string) error {
	_, err := s.pool.Exec(ctx, `UPDATE deal_alert SET attempts = attempts + 1, last_error = $2,
		queued_price_cents = least(queued_price_cents, $3) WHERE observation_id = $1`, observationID, reason, priceCents)
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
	Source          string    `json:"source" ts:"DealSource"`
	ExternalID      string    `json:"external_id"`
	ObservedAt      time.Time `json:"observed_at"`
	PriceCents      int64     `json:"price_cents"`
	AcquiredAs      *string   `json:"acquired_as" ts:"AcquiredAs"`
	URL             string    `json:"url"`
	ListingTitle    string    `json:"listing_title"`
	TitleID         *int64    `json:"title_id"`
	TitleName       *string   `json:"title_name"`
	EditionID       *int64    `json:"edition_id"`
	Format          *string   `json:"format" ts:"Format"`
	ListingPhotoURL *string   `json:"listing_photo_url"`
	CoverURL        *string   `json:"cover_url"`
	MaxPriceCents   *int64    `json:"max_price_cents"`
	Alert           string    `json:"alert" ts:"AlertState"`
	AlertError      string    `json:"alert_error"`
	AlertAttempts   int       `json:"alert_attempts"`
}

// RecentFilter narrows Recent to at most Limit listings. A listing matches the wishlist copy the
// hunter recorded it for, or its alert's copy; a valuation listing of an edition that is also
// wished for is not matched. Matched keeps the matched listings, with or without a price limit;
// UnderLimit keeps those at or under the matched wish's limit; Sent keeps those whose alert was
// sent for a wish that still has a limit. Filters apply before the row limit, so newer listings
// that fail them, such as listings for owned editions, never push older ones out.
type RecentFilter struct {
	Limit      int
	Matched    bool
	UnderLimit bool
	Sent       bool
}

// Listing rows Recent pages through: each listing with its alert's sent time and the wishlist copy
// that matches it, the one the hunter recorded it for, else its alert's copy, while that copy is
// still on the wishlist. allListings is every stored listing; wishedListings only those with a
// matching copy, the only ones a filter keeps, found from the wishlist through
// price_observation_wish_idx and deal_alert without reading the rest.
const (
	allListings = `SELECT o.id, o.observed_at, o.price_cents, o.source, a.sent_at, w.id AS wish_id, w.max_price_cents
		FROM price_observation o
		LEFT JOIN deal_alert a ON a.observation_id = o.id
		LEFT JOIN copy w ON w.status = 'wishlist' AND w.id = coalesce(o.wishlist_copy_id, a.copy_id)`
	wishedListings = `SELECT o.id, o.observed_at, o.price_cents, o.source, a.sent_at, w.id AS wish_id, w.max_price_cents
		FROM copy w
		JOIN price_observation o ON o.wishlist_copy_id = w.id
		LEFT JOIN deal_alert a ON a.observation_id = o.id
		WHERE w.status = 'wishlist'
		UNION ALL
		SELECT o.id, o.observed_at, o.price_cents, o.source, a.sent_at, w.id, w.max_price_cents
		FROM copy w
		JOIN deal_alert a ON a.copy_id = w.id
		JOIN price_observation o ON o.id = a.observation_id AND o.wishlist_copy_id IS NULL
		WHERE w.status = 'wishlist'`
)

// Recent returns the newest deal observations that pass f. It picks the page from the listing
// rows first and reads raw and the joined edition and title only for that page.
func (s *Store) Recent(ctx context.Context, f RecentFilter) ([]Observation, error) {
	listings := allListings
	if f.Matched || f.UnderLimit || f.Sent {
		listings = wishedListings
	}
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
		FROM (
			SELECT l.id, l.wish_id FROM (`+listings+`) l
			WHERE l.source = ANY ($3)
				AND (NOT $4 OR l.price_cents <= l.max_price_cents)
				AND (NOT $5 OR l.wish_id IS NOT NULL)
				AND (NOT $6 OR l.sent_at IS NOT NULL AND l.max_price_cents IS NOT NULL)
			ORDER BY l.observed_at DESC, l.id DESC LIMIT $1
		) page
		JOIN price_observation o ON o.id = page.id
		LEFT JOIN deal_alert a ON a.observation_id = o.id
		LEFT JOIN copy w ON w.id = page.wish_id
		LEFT JOIN edition e ON e.id = coalesce(w.edition_id, o.edition_id)
		LEFT JOIN title t ON t.id = coalesce(e.title_id, o.title_id)
		ORDER BY o.observed_at DESC, o.id DESC`,
		f.Limit, MaxAttempts, sources, f.UnderLimit, f.Matched, f.Sent)
	if err != nil {
		return nil, fmt.Errorf("recent observations: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Observation])
}

// WishCount returns how many copies are on the wishlist.
func (s *Store) WishCount(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM copy WHERE status = 'wishlist'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("wish count: %w", err)
	}
	return n, nil
}

// LastRun is the hunter's last run as stored: when it started and, once it
// ended, when and with what result. FinishedAt and Result belong to an
// earlier run while a newer one has started but not finished.
type LastRun struct {
	StartedAt  time.Time
	FinishedAt *time.Time
	Result     *RunResult
}

// LastRun returns the stored last run; false before the first.
func (s *Store) LastRun(ctx context.Context) (LastRun, bool, error) {
	var (
		run    LastRun
		result []byte
	)
	err := s.pool.QueryRow(ctx, `SELECT started_at, finished_at, result FROM deal_run`).
		Scan(&run.StartedAt, &run.FinishedAt, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return LastRun{}, false, nil
	}
	if err != nil {
		return LastRun{}, false, fmt.Errorf("last run: %w", err)
	}
	if result != nil {
		var res RunResult
		if err := json.Unmarshal(result, &res); err != nil {
			return LastRun{}, false, fmt.Errorf("last run result: %w", err)
		}
		run.Result = &res
	}
	return run, true, nil
}

// RunStarted stores the start of a run and keeps the previous run's end.
func (s *Store) RunStarted(ctx context.Context, at time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO deal_run (started_at) VALUES ($1)
		ON CONFLICT (id) DO UPDATE SET started_at = excluded.started_at`, at)
	if err != nil {
		return fmt.Errorf("run started: %w", err)
	}
	return nil
}

// RunFinished stores a whole run: its start too, in case storing that
// failed.
func (s *Store) RunFinished(ctx context.Context, started, finished time.Time, res *RunResult) error {
	result, err := json.Marshal(res)
	if err != nil {
		return fmt.Errorf("run finished: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO deal_run (started_at, finished_at, result) VALUES ($1, $2, $3)
		ON CONFLICT (id) DO UPDATE SET started_at = excluded.started_at,
			finished_at = excluded.finished_at, result = excluded.result`, started, finished, result)
	if err != nil {
		return fmt.Errorf("run finished: %w", err)
	}
	return nil
}
