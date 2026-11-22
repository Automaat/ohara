package deals

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// ErrPressReplayed reports a Telegram button press DismissPress already
// applied.
var ErrPressReplayed = errors.New("press already applied")

// ErrNotWished reports a "Kupione" press for a copy no longer on the
// wishlist, bought or deleted, that the press did not buy.
var ErrNotWished = errors.New("copy no longer on the wishlist")

// ErrAlreadyBought reports a "Kupione" press, sent again or pressed again,
// for a copy already owned whose listing is dismissed: the purchase it
// records is in place.
var ErrAlreadyBought = errors.New("copy already bought")

// ErrBoughtBefore reports a press of a "Kupione" button that already
// recorded its purchase, for a copy the collector has since changed back:
// the button buys once, so the press changes nothing.
var ErrBoughtBefore = errors.New("purchase already recorded")

// MaxAttempts bounds Telegram deliveries per alert; after that the alert is
// reported as failed and no longer retried.
const MaxAttempts = 5

// alertWindow is how long an unsent alert stays worth sending.
const alertWindow = `interval '7 days'`

// sources are the price_observation sources this package writes.
var sources = []string{"olx", "bluedvd", "winylownia", "swiatksiazki", "pepper", "tezeusz", DiscogsSource}

// expiringSources are the sources whose stored listings only a fresh
// answer lets the deals view show (freshListing) and which are deleted
// once no answer has offered them for discogsKeep (ForgetStale).
var expiringSources = []string{DiscogsSource}

// freshListing holds for a listing o the deals view may show and alert
// on: one of a source that does not expire, or one an answer still holds
// (not gone) and offered within discogsShown.
var freshListing = freshAt("now()")

// freshAt is freshListing judged at the instant the SQL at names.
func freshAt(at string) string {
	return fmt.Sprintf(`(o.source <> ALL ('{%s}'::text[])
	OR o.gone_at IS NULL AND o.last_offered_at > %s - make_interval(secs => %d))`,
		strings.Join(expiringSources, ","), at, int(discogsShown.Seconds()))
}

// mediumFits is SQL that holds when listing o can be what wish w's
// edition is now: a listing without a source-named medium
// (Listing.Medium, kept as raw->'data'->>'medium') leaves that to the
// matcher, one with a medium is an album's, so it fits a music wish of no
// format or of that format. The hunter matched the listing against the
// wish as it was, so a wish edited since (vinyl to CD, an album to a
// film) no longer holds a listing of another medium.
func mediumFits(w string) string {
	return `(coalesce(o.raw->'data'->>'medium', '') = '' OR EXISTS (SELECT FROM edition me
		JOIN title mt ON mt.id = me.title_id
		WHERE me.id = ` + w + `.edition_id AND mt.kind = 'music'
			AND (me.format IS NULL OR me.format = o.raw->'data'->>'medium')))`
}

// Store is the Postgres access layer for deal hunting.
type Store struct {
	pool    *pgxpool.Pool
	markets MarketPrices
}

// NewStore wraps pool. Until UseMarkets gives it market prices, no wish
// alerts on its market percent.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Market is the used market price of one wished title in one format that
// a wish's market percent applies to: one that rests on enough prices to
// be trusted. A nil Format is a title whose editions name none.
type Market struct {
	TitleID int64
	Format  *string
	Cents   int64
}

// MarketPrices reads the trusted market price of every wished title and
// format.
type MarketPrices func(ctx context.Context) ([]Market, error)

// UseMarkets sets where the store reads market prices; call it before the
// hunter or the handler use the store.
func (s *Store) UseMarkets(markets MarketPrices) {
	s.markets = markets
}

// marketTable is the market prices as the parallel arrays a query unnests
// into m(title_id, format, cents), with an empty format for none.
type marketTable struct {
	titles  []int64
	formats []string
	cents   []int64
}

// marketJoin joins m, the market price of the title and format of edition
// alias, from the arrays at $n, $n+1 and $n+2.
func marketJoin(alias string, n int) string {
	return fmt.Sprintf(`LEFT JOIN unnest($%d::bigint[], $%d::text[], $%d::bigint[]) AS m(title_id, format, cents)
			ON m.title_id = %[4]s.title_id AND m.format = coalesce(%[4]s.format, '')`, n, n+1, n+2, alias)
}

// marketTable reads the market prices; empty before UseMarkets.
func (s *Store) marketTable(ctx context.Context) (marketTable, error) {
	t := marketTable{titles: []int64{}, formats: []string{}, cents: []int64{}}
	if s.markets == nil {
		return t, nil
	}
	markets, err := s.markets(ctx)
	if err != nil {
		return t, fmt.Errorf("market prices: %w", err)
	}
	for _, m := range markets {
		format := ""
		if m.Format != nil {
			format = *m.Format
		}
		t.titles = append(t.titles, m.TitleID)
		t.formats = append(t.formats, format)
		t.cents = append(t.cents, m.Cents)
	}
	return t, nil
}

// RefreshMarkets sets the MarketCents of every want with a market percent
// to the current market price of its title and format, nil without one.
func (s *Store) RefreshMarkets(ctx context.Context, wants []Want) error {
	markets, err := s.marketTable(ctx)
	if err != nil {
		return fmt.Errorf("refresh markets: %w", err)
	}
	type key struct {
		title  int64
		format string
	}
	cents := make(map[key]int64, len(markets.titles))
	for i, title := range markets.titles {
		cents[key{title, markets.formats[i]}] = markets.cents[i]
	}
	for i := range wants {
		w := &wants[i]
		w.MarketCents = nil
		if w.MaxMarketPercent == nil {
			continue
		}
		format := ""
		if w.Format != nil {
			format = *w.Format
		}
		if c, ok := cents[key{w.TitleID, format}]; ok {
			w.MarketCents = &c
		}
	}
	return nil
}

// Wants returns every wishlist copy with its edition and title, oldest
// first, but those of TV series, which the hunter does not look for, and the market price of its title and format when it has a
// market percent.
func (s *Store) Wants(ctx context.Context) ([]Want, error) {
	markets, err := s.marketTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("wants: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id, e.id, t.id, t.kind, t.name, t.original_title,
			t.polish_title, t.authors, e.format, e.barcode, c.max_price_cents, c.max_market_percent,
			CASE WHEN c.max_market_percent IS NOT NULL THEN m.cents END, c.exact_edition,
			e.packaging, e.pl_voiceover, e.pl_subtitles, e.binding, e.discogs_release_id
		FROM copy c JOIN edition e ON e.id = c.edition_id JOIN title t ON t.id = e.title_id
		`+marketJoin("e", 1)+`
		WHERE c.status = 'wishlist' AND t.kind <> 'tv' ORDER BY c.id`, markets.titles, markets.formats, markets.cents)
	if err != nil {
		return nil, fmt.Errorf("wants: %w", err)
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (Want, error) {
		var w Want
		err := row.Scan(&w.CopyID, &w.EditionID, &w.TitleID, &w.Kind, &w.Name, &w.OriginalTitle,
			&w.PolishTitle, &w.Authors, &w.Format, &w.Barcode, &w.MaxPriceCents, &w.MaxMarketPercent,
			&w.MarketCents, &w.ExactEdition, &w.Packaging, &w.PLVoiceover, &w.PLSubtitles, &w.Binding, &w.DiscogsReleaseID)
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
	Query          string          `json:"query,omitempty"`
	Data           json.RawMessage `json:"data,omitempty"`
}

// listingSources are the sources whose stored listings keep their first
// price and are marked gone once no longer offered.
var listingSources = map[string]bool{"olx": true}

// listingTitle is the title a stored listing belongs to: its edition's,
// else the one it was stored with.
const listingTitle = `coalesce((SELECT e.title_id FROM edition e WHERE e.id = price_observation.edition_id),
	price_observation.title_id)`

// otherTitle holds in Record's upsert when the run matched the stored
// listing to another title than the one it belongs to.
const otherTitle = listingTitle + ` <> EXCLUDED.title_id`

// matchedAsOf is when the recording run read the wishlist and titles it
// matched with: $9, or now for Record.
const matchedAsOf = `coalesce($9::timestamptz, now())`

// assignedFromNewer holds in Record's upsert when a run that read its data
// after the recording run gave the listing its current title; never for a
// listing stored before assigned_as_of.
const assignedFromNewer = `price_observation.assigned_as_of > ` + matchedAsOf

// movedTitle holds in Record's upsert when the listing moves to the
// matched title: another title, matched from data at least as new as the
// data its current title was matched from.
const movedTitle = `(` + otherTitle + ` AND NOT coalesce(` + assignedFromNewer + `, false))`

// Now returns the database's current time, the time a run reads its
// wishlist and titles at for RecordAsOf.
func (s *Store) Now(ctx context.Context) (time.Time, error) {
	var now time.Time
	if err := s.pool.QueryRow(ctx, `SELECT now()`).Scan(&now); err != nil {
		return time.Time{}, fmt.Errorf("now: %w", err)
	}
	return now, nil
}

// Record stores l as an observation of w and, with alert, queues its
// Telegram alert in the same transaction. A listing a source already
// reported keeps its row and first observation time; its price and
// last_seen_at move to now. An OLX listing keeps the price it was first
// stored at (unknown for rows stored before that was kept) and the query
// key that last found it, and a gone one seen again is no longer gone.
// It keeps its edition too, unless w belongs to
// another title: then it moves to w's title and edition, its moved_at
// becomes now, and a valuation match, which names no wish, drops the wish
// the hunter recorded it for. An unsent alert for a wish of another title
// than the listing's moves to w, or to no wish when w is not one, so it is
// never sent for the old wish; an alert already sent stays recorded. A
// listing that still belongs to another title than w's, as RecordAsOf
// allows, queues and moves no alert. The alert is queued when the listing
// has none yet, so a listing the valuation run stored first, or one whose price
// dropped under the limit, still alerts. A listing whose alert was sent
// alerts again when its price is under the one that alert named, unless
// an alert for it is still waiting to be sent: that one goes out at the
// current price. An alert that gave up or expired unsent is queued again
// only under the lowest price it was queued or tried at. An unsent alert
// whose wish is gone or no longer wished for moves to w, keeping its
// attempts and age. A dismissed listing stays dismissed and queues no
// alert. It reports whether the listing was new and whether an alert was
// queued.
func (s *Store) Record(ctx context.Context, l *Listing, w *Want, alert bool) (bool, bool, error) {
	return s.record(ctx, l, w, alert, nil)
}

// RecordAsOf is Record for a run that matched l to w from the wishlist and
// titles it read at asOf; Record counts as a match from data read now. The
// listing keeps, in assigned_as_of, the newest such time of a match to its
// current title. A listing whose current title was matched from data read
// after asOf keeps its title and edition when w belongs to another title,
// since that match is newer; it is then not recorded for w, and no alert
// is queued for w.
func (s *Store) RecordAsOf(ctx context.Context, l *Listing, w *Want, alert bool, asOf time.Time) (bool, bool, error) {
	return s.record(ctx, l, w, alert, &asOf)
}

// RecordAllAsOf stores ls as observations of w as RecordAsOf does, in one
// transaction and queuing no alert, so every one of them is last seen at
// the same instant.
func (s *Store) RecordAllAsOf(ctx context.Context, ls []Listing, w *Want, asOf time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("record: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for i := range ls {
		if _, _, err := record(ctx, tx, &ls[i], w, false, &asOf); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("record: commit: %w", err)
	}
	return nil
}

func (s *Store) record(ctx context.Context, l *Listing, w *Want, alert bool, asOf *time.Time) (bool, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, false, fmt.Errorf("record: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	isNew, alerted, err := record(ctx, tx, l, w, alert, asOf)
	if err != nil {
		return false, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, false, fmt.Errorf("record: commit: %w", err)
	}
	return isNew, alerted, nil
}

// record is Store.record inside tx, which the caller commits.
func record(ctx context.Context, tx pgx.Tx, l *Listing, w *Want, alert bool, asOf *time.Time) (bool, bool, error) {
	raw, err := json.Marshal(observationRaw{
		Title: l.Title, Data: l.Raw, PhotoURL: l.PhotoURL, WishlistCopyID: w.CopyID, Query: QueryKey(l.Query),
	})
	if err != nil {
		return false, false, fmt.Errorf("record: encode raw: %w", err)
	}
	var firstPrice *int64
	if listingSources[l.Source] {
		firstPrice = &l.PriceCents
	}
	var id int64
	var isNew, dismissed, alerted bool
	var titleID *int64
	err = tx.QueryRow(ctx, `INSERT INTO price_observation
			(source, external_id, price_cents, acquired_as, url, edition_id, title_id, raw, assigned_as_of,
				first_price_cents, last_offered_at, edition_details)
		VALUES ($1, $2, $3, $4, $5, nullif($6::bigint, 0), nullif($7::bigint, 0), $8, `+matchedAsOf+`, $10, now(), $11)
		ON CONFLICT (source, external_id) WHERE external_id IS NOT NULL DO UPDATE SET
			price_cents = EXCLUDED.price_cents,
			last_seen_at = now(),
			last_offered_at = now(),
			gone_at = NULL,
			url = EXCLUDED.url,
			edition_details = EXCLUDED.edition_details,
			raw = CASE WHEN `+movedTitle+` THEN coalesce(price_observation.raw, '{}'::jsonb) - 'wishlist_copy_id' || EXCLUDED.raw
				WHEN `+otherTitle+` THEN coalesce(price_observation.raw, '{}'::jsonb) || (EXCLUDED.raw - 'wishlist_copy_id')
				ELSE coalesce(price_observation.raw, '{}'::jsonb) || EXCLUDED.raw END,
			acquired_as = CASE WHEN price_observation.source = ANY ($12) THEN EXCLUDED.acquired_as
				ELSE coalesce(EXCLUDED.acquired_as, price_observation.acquired_as) END,
			edition_id = CASE WHEN `+movedTitle+` THEN EXCLUDED.edition_id
				ELSE coalesce(price_observation.edition_id, EXCLUDED.edition_id) END,
			title_id = CASE WHEN `+movedTitle+` THEN EXCLUDED.title_id
				ELSE coalesce(price_observation.title_id, EXCLUDED.title_id) END,
			moved_at = CASE WHEN `+movedTitle+` THEN now() ELSE price_observation.moved_at END,
			assigned_as_of = CASE WHEN `+movedTitle+` THEN `+matchedAsOf+`
				WHEN `+otherTitle+` THEN price_observation.assigned_as_of
				ELSE greatest(price_observation.assigned_as_of, `+matchedAsOf+`) END
		RETURNING id, xmax = 0, dismissed_at IS NOT NULL, `+listingTitle,
		l.Source, l.ExternalID, l.PriceCents, l.AcquiredAs, l.URL, w.EditionID, w.TitleID, raw, asOf,
		firstPrice, namedDetails(words(l.Title)), expiringSources).Scan(&id, &isNew, &dismissed, &titleID)
	if err != nil {
		return false, false, fmt.Errorf("record observation: %w", err)
	}
	if titleID == nil || *titleID != w.TitleID {
		return isNew, false, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE deal_alert a SET copy_id = nullif($3::bigint, 0)
		FROM copy c JOIN edition e ON e.id = c.edition_id
		WHERE a.observation_id = $1 AND a.sent_at IS NULL AND c.id = a.copy_id AND e.title_id <> $2`,
		id, w.TitleID, w.CopyID); err != nil {
		return false, false, fmt.Errorf("record alert: move to title: %w", err)
	}
	if alert && !dismissed {
		if alerted, err = queueAlert(ctx, tx, alertCandidate{id: id, copyID: w.CopyID, priceCents: l.PriceCents}, false); err != nil {
			return false, false, err
		}
	}
	return isNew, alerted, nil
}

// UpdateGone brings the gone marks of source's stored listings up to a
// run that answered seen (the external ID of every offer any answer held)
// and whose answers to the query keys complete held every offer the site
// has. A listing seen is offered, so it is no longer gone, whether or not
// it still matches what it was stored for. A listing last found by one of
// complete, not seen, and neither seen with a price nor offered at all
// after since (the run's start on the database clock, so a listing another
// run recorded or saw meanwhile stays) is gone. gone_at is set once;
// last_seen_at keeps the last time the listing was offered with a price
// and last_offered_at the last time it was offered at all (MarkOffered).
// It reports how many listings it marked gone.
func (s *Store) UpdateGone(ctx context.Context, source string, complete, seen []string, since time.Time) (int64, error) {
	seen = append([]string{}, seen...)
	if err := s.MarkOffered(ctx, source, seen); err != nil {
		return 0, err
	}
	if len(complete) == 0 {
		return 0, nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE price_observation SET gone_at = now()
		WHERE source = $1 AND gone_at IS NULL AND external_id IS NOT NULL
			AND raw->>'query' = ANY ($2) AND external_id <> ALL ($3)
			AND greatest(last_seen_at, last_offered_at) < $4`,
		source, complete, seen, since)
	if err != nil {
		return 0, fmt.Errorf("mark gone: %w", err)
	}
	return tag.RowsAffected(), nil
}

// MarkOffered records that an answer of source held the listings with
// externalIDs, priced or not, matching or not: their last_offered_at moves
// to now and a gone one is no longer gone. last_seen_at and the price stay,
// so the valuation window still counts only priced sightings. IDs of
// listings never stored are ignored.
func (s *Store) MarkOffered(ctx context.Context, source string, externalIDs []string) error {
	if len(externalIDs) == 0 {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `UPDATE price_observation SET gone_at = NULL, last_offered_at = now()
		WHERE source = $1 AND external_id = ANY ($2)`, source, externalIDs); err != nil {
		return fmt.Errorf("mark offered: %w", err)
	}
	return nil
}

// Unmatch takes the unsent alerts of source's stored listings with
// externalIDs, which an answer held under a title that matches no want,
// off their wishes. Like the alert of a listing moved to a title no one
// wishes for, each goes to none, so the price it was queued at, which
// the answer contradicts, is never sent. Its attempts, age and prices
// stay: a later answer that matches the listing within a wish's limit
// queues it for that wish again (queueAlert). Sent alerts and IDs of
// listings never stored are left alone. It takes the listings' rows as
// SendAlert does, so a message already on its way goes out first and its
// sent alert stays.
func (s *Store) Unmatch(ctx context.Context, source string, externalIDs []string) error {
	if len(externalIDs) == 0 {
		return nil
	}
	if _, err := s.pool.Exec(ctx, `WITH locked AS (
			SELECT o.id FROM price_observation o
			WHERE o.source = $1 AND o.external_id = ANY ($2) AND EXISTS (SELECT FROM deal_alert d
				WHERE d.observation_id = o.id AND d.sent_at IS NULL AND d.copy_id IS NOT NULL)
			ORDER BY o.id FOR UPDATE OF o)
		UPDATE deal_alert a SET copy_id = NULL
		FROM locked
		WHERE a.observation_id = locked.id AND a.sent_at IS NULL AND a.copy_id IS NOT NULL`,
		source, externalIDs); err != nil {
		return fmt.Errorf("unmatch alerts: %w", err)
	}
	return nil
}

// ForgetStale deletes the listings of expiring sources that no answer has
// offered for discogsKeep, with their alerts and purchases, dismissed or
// not: the source's terms let them be kept only while they serve. It
// reports how many it deleted.
func (s *Store) ForgetStale(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM price_observation
		WHERE source = ANY ($1) AND greatest(last_seen_at, last_offered_at) < now() - make_interval(secs => $2)`,
		expiringSources, discogsKeep.Seconds())
	if err != nil {
		return 0, fmt.Errorf("forget stale listings: %w", err)
	}
	return tag.RowsAffected(), nil
}

// pruneBatch bounds the listings one delete of Prune removes, so no
// statement holds many rows or runs long.
const pruneBatch = 1000

// runLock is the advisory lock key a hunt or valuation holds shared for
// its whole run (HoldRun) and Prune holds exclusive, so a listing a run
// has seen but not yet recorded is never pruned under it. The key names
// the schema, as advisory locks span the database.
const runLock = `hashtextextended('ohara.run:' || current_schema(), 0)`

// ErrRunning reports that Prune found a hunt or valuation running and
// deleted nothing.
var ErrRunning = errors.New("a hunt or valuation is running")

// running counts the hunts and valuations this process runs against each
// pool, so Prune defers to them even when a run's lock session ended under
// it (a server timeout or a dropped connection releases the advisory lock
// while the run goes on).
var running = struct {
	sync.Mutex
	runs map[*pgxpool.Pool]int
}{runs: map[*pgxpool.Pool]int{}}

func addRunning(pool *pgxpool.Pool, n int) {
	running.Lock()
	defer running.Unlock()
	running.runs[pool] += n
	if running.runs[pool] == 0 {
		delete(running.runs, pool)
	}
}

func isRunning(pool *pgxpool.Pool) bool {
	running.Lock()
	defer running.Unlock()
	return running.runs[pool] > 0
}

// HoldRun marks a hunt or valuation running in this process and takes the
// run lock shared on a connection of its own, waiting for a prune in
// progress. It returns the function that gives both back. A connection
// whose unlock fails is closed, which drops its lock.
func (s *Store) HoldRun(ctx context.Context) (func(), error) {
	addRunning(s.pool, 1)
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		addRunning(s.pool, -1)
		return nil, fmt.Errorf("run lock: %w", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock_shared(`+runLock+`)`); err != nil {
		closeConn(ctx, conn)
		addRunning(s.pool, -1)
		return nil, fmt.Errorf("run lock: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			defer addRunning(s.pool, -1)
			if _, err := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock_shared(`+runLock+`)`); err != nil {
				closeConn(ctx, conn)
				return
			}
			conn.Release()
		})
	}, nil
}

// closeConn takes conn out of the pool and closes it, ending its session
// and with it every advisory lock it holds.
func closeConn(ctx context.Context, conn *pgxpool.Conn) {
	_ = conn.Hijack().Close(context.WithoutCancel(ctx))
}

// Prune deletes the listings neither seen with a price (last_seen_at) nor
// offered at all (last_offered_at) within olderThan, in batches of
// pruneBatch, and reports how many it deleted. A dismissed listing and one
// with an alert, sent or not, stay whatever their age: a listing still
// offered past every answer would otherwise come back as a new row, not
// dismissed and alerting again, and a "Kupione" press needs its listing
// and alert (telegram_press and telegram_purchase stay with them). A
// listing another transaction holds (a run recording it, an alert on its
// way) is left for the next prune. Every answer that
// holds a stored listing moves one of the two as it arrives, and Prune
// takes the run lock exclusive without waiting: while a hunt or valuation
// runs, in this process or holding the lock from another, it deletes
// nothing and returns ErrRunning, so a listing the runs still see is never
// deleted.
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	if isRunning(s.pool) {
		return 0, ErrRunning
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("prune listings: %w", err)
	}
	var locked bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(`+runLock+`)`).Scan(&locked); err != nil {
		closeConn(ctx, conn)
		return 0, fmt.Errorf("prune listings: lock: %w", err)
	}
	if !locked {
		conn.Release()
		return 0, ErrRunning
	}
	total, err := prune(ctx, conn, olderThan)
	if _, unlockErr := conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(`+runLock+`)`); unlockErr != nil {
		closeConn(ctx, conn)
		return total, err
	}
	conn.Release()
	return total, err
}

// prune is Prune's deletes, on conn, which holds the run lock.
func prune(ctx context.Context, conn *pgxpool.Conn, olderThan time.Duration) (int64, error) {
	var total int64
	for {
		tag, err := conn.Exec(ctx, `DELETE FROM price_observation WHERE id IN (
			SELECT o.id FROM price_observation o
			WHERE greatest(o.last_seen_at, o.last_offered_at) < now() - make_interval(secs => $1)
				AND o.dismissed_at IS NULL
				AND NOT EXISTS (SELECT FROM deal_alert a WHERE a.observation_id = o.id)
			ORDER BY o.id LIMIT $2 FOR UPDATE SKIP LOCKED)`,
			olderThan.Seconds(), pruneBatch)
		if err != nil {
			return total, fmt.Errorf("prune listings: %w", err)
		}
		total += tag.RowsAffected()
		if tag.RowsAffected() < pruneBatch {
			return total, nil
		}
	}
}

type alertCandidate struct {
	id, copyID, priceCents int64
}

// queueAlert queues an alert as Record describes, and reports whether it did.
func queueAlert(ctx context.Context, tx pgx.Tx, candidate alertCandidate, automatic bool) (bool, error) {
	if _, err := tx.Exec(ctx, `UPDATE deal_alert SET copy_id = $2, automatic = automatic OR $3
		WHERE observation_id = $1 AND sent_at IS NULL
			AND (copy_id IS DISTINCT FROM $2
				AND NOT EXISTS (SELECT FROM copy c WHERE c.id = deal_alert.copy_id AND c.status = 'wishlist')
				OR $3 AND NOT automatic AND copy_id = $2)`,
		candidate.id, candidate.copyID, automatic); err != nil {
		return false, fmt.Errorf("record alert: rebind: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id, queued_price_cents, automatic) VALUES ($1, $2, $3, $5)
		ON CONFLICT (observation_id) DO UPDATE SET copy_id = EXCLUDED.copy_id, created_at = now(),
			sent_at = NULL, attempts = 0, last_error = '', queued_price_cents = EXCLUDED.queued_price_cents,
			automatic = EXCLUDED.automatic
		WHERE $3 < deal_alert.alerted_price_cents AND (deal_alert.sent_at IS NOT NULL
			OR $3 < deal_alert.queued_price_cents
				AND (deal_alert.attempts >= $4 OR deal_alert.created_at <= now() - `+alertWindow+`))`,
		candidate.id, candidate.copyID, candidate.priceCents, MaxAttempts, automatic)
	if err != nil {
		return false, fmt.Errorf("record alert: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

// Alert is a queued Telegram message with what it reports.
type Alert struct {
	ObservationID int64
	// CopyID is the wishlist copy the alert is for.
	CopyID        int64
	Source        string
	ExternalID    string
	URL           string
	ListingTitle  string
	PriceCents    int64
	AcquiredAs    *string
	TitleName     string
	Format        *string
	MaxPriceCents *int64
	// MaxMarketPercent is the wish's share of the market price and
	// MarketCents that price, nil without a percent or a trusted price.
	MaxMarketPercent *int
	MarketCents      *int64
	// AlertedPriceCents is the price the listing's last sent alert named;
	// nil before its first alert is sent.
	AlertedPriceCents *int64
	automatic         bool
}

// PendingAlerts returns undelivered alerts with attempts left, oldest
// first. Alerts for copies no longer on the wishlist, alerts that meet
// neither the copy's current maximum price nor its share of the current
// market price, alerts for dismissed listings or ones not fresh
// (freshListing), alerts older than a week and repeat alerts whose listing
// is no longer under the price its last sent alert named are skipped.
func (s *Store) PendingAlerts(ctx context.Context) ([]Alert, error) {
	return s.pendingAlerts(ctx, false)
}

func (s *Store) pendingAlerts(ctx context.Context, automatic bool) ([]Alert, error) {
	markets, err := s.marketTable(ctx)
	if err != nil {
		return nil, fmt.Errorf("pending alerts: %w", err)
	}
	return pendingAlerts(ctx, s.pool, markets, nil, automatic)
}

// SendAlert runs send for the alert of observationID while PendingAlerts
// would still return it, and stores the outcome: sent, or a failed attempt
// with send's error, at the price send was given. It holds the listing's
// row from that check until the outcome is stored, so a dismissal or a
// price change either lands before the check and stops or reprices the
// alert, or waits until the message is out. The market prices are read once
// the row is held, so a market price that moved while the check waited for it
// counts. The outcome is stored even when ctx ends during send, so a message
// that went out is not sent again. It reports whether send ran and succeeded.
func (s *Store) SendAlert(ctx context.Context, observationID int64, send func(*Alert) error) (bool, error) {
	return s.sendAlert(ctx, observationID, false, send)
}

func (s *Store) sendAlert(ctx context.Context, observationID int64, automatic bool, send func(*Alert) error) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("send alert: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT 1 FROM price_observation WHERE id = $1 FOR UPDATE`, observationID); err != nil {
		return false, fmt.Errorf("send alert: lock listing: %w", err)
	}
	markets, err := s.marketTable(ctx)
	if err != nil {
		return false, fmt.Errorf("send alert: %w", err)
	}
	due, err := pendingAlerts(ctx, tx, markets, &observationID, automatic)
	if err != nil {
		return false, err
	}
	if len(due) == 0 {
		return false, nil
	}
	a := &due[0]
	sendErr := send(a)
	storeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), markSentTimeout)
	defer cancel()
	if sendErr != nil {
		err = markFailed(storeCtx, tx, observationID, a.PriceCents, sendErr.Error())
	} else {
		err = markSent(storeCtx, tx, observationID, a.PriceCents, a.automatic)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(storeCtx); err != nil {
		return false, fmt.Errorf("send alert: commit: %w", err)
	}
	return sendErr == nil, nil
}

// querier is what the alert queries need from a pool or a transaction.
type querier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// pendingAlerts is PendingAlerts with the market prices read, limited to
// one listing when only is set.
func pendingAlerts(ctx context.Context, q querier, markets marketTable, only *int64, automatic bool) ([]Alert, error) {
	rows, err := q.Query(ctx, `SELECT o.id, c.id, o.source, coalesce(o.external_id, ''), o.url, coalesce(o.raw->>'title', ''),
			o.price_cents, o.acquired_as, t.name, e.format, c.max_price_cents, c.max_market_percent,
			CASE WHEN c.max_market_percent IS NOT NULL THEN m.cents END, a.alerted_price_cents,
			c.max_price_cents IS NULL AND c.max_market_percent IS NULL, `+storedAlbumFields+`
		FROM deal_alert a
		JOIN price_observation o ON o.id = a.observation_id
		JOIN copy c ON c.id = a.copy_id AND c.status = 'wishlist'
		JOIN edition e ON e.id = c.edition_id
		JOIN title t ON t.id = e.title_id
		`+marketJoin("e", 3)+`
		WHERE a.sent_at IS NULL AND a.attempts < $1 AND a.created_at > now() - `+alertWindow+`
			AND (o.price_cents <= c.max_price_cents OR o.price_cents * 100 <= m.cents * c.max_market_percent
				OR $6 AND a.automatic AND c.max_price_cents IS NULL AND c.max_market_percent IS NULL
					AND o.gone_at IS NULL AND c.id = coalesce(o.wishlist_copy_id, a.copy_id) AND `+wishOfListingTitle("c")+`)
			AND o.dismissed_at IS NULL AND `+freshListing+` AND `+mediumFits("c")+`
			AND `+wishNamedBy("c")+` AND `+huntedWish("c")+`
			AND (a.alerted_price_cents IS NULL OR o.price_cents < a.alerted_price_cents)
			AND ($2::bigint IS NULL OR a.observation_id = $2)
		ORDER BY a.created_at, a.observation_id`, MaxAttempts, only, markets.titles, markets.formats, markets.cents, automatic)
	if err != nil {
		return nil, fmt.Errorf("pending alerts: %w", err)
	}
	defer rows.Close()
	var alerts []Alert
	var candidates []int64
	for rows.Next() {
		var a Alert
		var album storedAlbum
		fields := []any{
			&a.ObservationID, &a.CopyID, &a.Source, &a.ExternalID, &a.URL, &a.ListingTitle, &a.PriceCents,
			&a.AcquiredAs, &a.TitleName, &a.Format, &a.MaxPriceCents, &a.MaxMarketPercent, &a.MarketCents,
			&a.AlertedPriceCents, &a.automatic,
		}
		if err := rows.Scan(append(fields, album.destinations()...)...); err != nil {
			return nil, fmt.Errorf("pending alerts: %w", err)
		}
		if album.want.VerifiesAlbum(&album.listing) {
			alerts = append(alerts, a)
			if a.automatic {
				candidates = append(candidates, a.ObservationID)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return alerts, nil
	}
	qualified, err := qualifyingAskingPrices(ctx, q, candidates)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(alerts, func(a Alert) bool { return a.automatic && !qualified[a.ObservationID] }), nil
}

// MarkSent records a delivered alert and the price its message named.
func (s *Store) MarkSent(ctx context.Context, observationID, priceCents int64) error {
	return markSent(ctx, s.pool, observationID, priceCents, false)
}

func markSent(ctx context.Context, q querier, observationID, priceCents int64, automatic bool) error {
	_, err := q.Exec(ctx, `UPDATE deal_alert SET sent_at = now(), attempts = attempts + 1,
		last_error = '', alerted_price_cents = $2, automatic = $3 WHERE observation_id = $1`, observationID, priceCents, automatic)
	if err != nil {
		return fmt.Errorf("mark sent: %w", err)
	}
	return nil
}

// MarkFailed records a failed delivery attempt at priceCents.
func (s *Store) MarkFailed(ctx context.Context, observationID, priceCents int64, reason string) error {
	return markFailed(ctx, s.pool, observationID, priceCents, reason)
}

func markFailed(ctx context.Context, q querier, observationID, priceCents int64, reason string) error {
	_, err := q.Exec(ctx, `UPDATE deal_alert SET attempts = attempts + 1, last_error = $2,
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
	// MaxMarketPercent is the matched wish's share of the market price and
	// MarketCents that price, null without a percent or a trusted price.
	MaxMarketPercent *int   `json:"max_market_percent,omitempty" ts:",null"`
	MarketCents      *int64 `json:"market_cents,omitempty" ts:",null"`
	Alert            string `json:"alert" ts:"AlertState"`
	AlertError       string `json:"alert_error"`
	AlertAttempts    int    `json:"alert_attempts"`
}

// RecentFilter narrows Recent to at most Limit listings. A listing matches the wishlist copy the
// hunter recorded it for, or its alert's copy; a valuation listing of an edition that is also
// wished for is not matched. Matched keeps the matched listings, with or without a price limit;
// UnderLimit keeps those at or under the matched wish's limit or its share of the market price;
// Sent keeps those whose alert was sent automatically or for a wish that still has a limit or market percent.
// Dismissed listings and those not fresh (freshListing) are always left out. Filters apply before the row limit, so newer listings
// that fail them, such as listings for owned editions, never push older ones out.
type RecentFilter struct {
	Limit      int
	Matched    bool
	UnderLimit bool
	Sent       bool
}

// wishOfListingTitle holds when wish w is for listing o's title, its edition's or else the one
// it was stored with. A listing moved to another title keeps its sent alert for the old title's
// wish, which no longer matches it.
func wishOfListingTitle(w string) string {
	return `EXISTS (SELECT FROM edition we WHERE we.id = ` + w + `.edition_id
		AND we.title_id = coalesce((SELECT le.title_id FROM edition le WHERE le.id = o.edition_id), o.title_id))`
}

// allListings is SQL for the newest $5 stored listings from the deal sources $6, each with the
// wishlist copy that matches it: the one the hunter recorded it for, else its alert's copy when
// that copy is for the listing's title, while that copy is still on the wishlist and the listing
// names every edition detail it needs (wishNamedBy).
var allListings = `SELECT o.id, w.id AS wish_id
		FROM price_observation o
		LEFT JOIN deal_alert a ON a.observation_id = o.id
		LEFT JOIN copy w ON w.status = 'wishlist' AND w.id = coalesce(o.wishlist_copy_id, a.copy_id)
			AND (o.wishlist_copy_id IS NOT NULL OR ` + wishOfListingTitle("w") + `) AND ` + wishNamedBy("w") + `
			AND ` + huntedWish("w") + ` AND ` + mediumFits("w") + ` AND o.id <> ALL ($7)
		WHERE o.dismissed_at IS NULL AND o.source = ANY ($6) AND ` + freshListing + `
		ORDER BY o.observed_at DESC, o.id DESC LIMIT $5`

// albumListings are the listings with a copy among the album wishes $2 that matches them, as
// Recent's filters match them: those the hunter recorded for the copy, found through
// price_observation_wish_idx, and those an alert alone names the copy for. unverifiedAlbums
// rechecks them.
var albumListings = `SELECT o.id, w.edition_id AS wish_edition_id
		FROM copy w
		JOIN price_observation o ON o.wishlist_copy_id = w.id AND o.dismissed_at IS NULL AND ` + freshListing + `
		WHERE w.id = ANY ($2) AND ` + wishNamedBy("w") + ` AND ` + mediumFits("w") + `
		UNION ALL
		SELECT o.id, w.edition_id
		FROM copy w
		JOIN deal_alert a ON a.copy_id = w.id
		JOIN price_observation o ON o.id = a.observation_id AND o.wishlist_copy_id IS NULL
			AND o.dismissed_at IS NULL AND ` + freshListing + `
		WHERE w.id = ANY ($2) AND ` + wishOfListingTitle("w") + ` AND ` + wishNamedBy("w") + ` AND ` + mediumFits("w")

// Compare the stored Discogs release as JSON, since old observations
// need not have a numeric release_id.
const storedAlbumFields = `t.name, t.original_title, t.polish_title, t.authors, t.kind, e.format, e.discogs_release_id,
	coalesce(o.raw->>'title', ''), o.source, coalesce(o.raw->'data'->>'medium', ''),
	CASE WHEN o.raw->'data'->'release_id' = to_jsonb(e.discogs_release_id) THEN e.discogs_release_id ELSE 0 END`

type storedAlbum struct {
	want    Want
	listing Listing
}

func (a *storedAlbum) destinations() []any {
	w, l := &a.want, &a.listing
	return []any{
		&w.Name, &w.OriginalTitle, &w.PolishTitle, &w.Authors, &w.Kind, &w.Format, &w.DiscogsReleaseID,
		&l.Title, &l.Source, &l.Medium, &l.DiscogsReleaseID,
	}
}

// unverifiedAlbums returns the listings matched to an album wish that the wish no longer
// verifies (Want.VerifiesAlbum). It reads the album wishes first and, with none, no listing.
func unverifiedAlbums(ctx context.Context, q pgx.Tx) ([]int64, error) {
	var albums []int64
	if err := q.QueryRow(ctx, `SELECT coalesce(array_agg(w.id), '{}') FROM copy w
		WHERE w.status = 'wishlist' AND `+huntedWish("w")+` AND EXISTS (SELECT FROM edition ae
			JOIN title at ON at.id = ae.title_id WHERE ae.id = w.edition_id AND at.kind = 'music')`).Scan(&albums); err != nil {
		return nil, err
	}
	if len(albums) == 0 {
		return []int64{}, nil
	}
	rows, err := q.Query(ctx, `SELECT o.id, `+storedAlbumFields+`
		FROM (`+albumListings+`) l
		JOIN price_observation o ON o.id = l.id
		JOIN edition e ON e.id = l.wish_edition_id
		JOIN title t ON t.id = e.title_id
		WHERE o.source = ANY ($1)`, sources, albums)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	unverified := []int64{}
	for rows.Next() {
		var id int64
		var album storedAlbum
		if err := rows.Scan(append([]any{&id}, album.destinations()...)...); err != nil {
			return nil, err
		}
		if !album.want.VerifiesAlbum(&album.listing) {
			unverified = append(unverified, id)
		}
	}
	return unverified, rows.Err()
}

// pageListings is SQL for the listings of a page Recent picked before: $5 holds their IDs and
// $6 the ID of the wishlist copy each matches.
const pageListings = `SELECT * FROM unnest($5::bigint[], $6::bigint[]) AS page(id, wish_id)`

// Recent returns the newest deal observations that pass f. It rechecks album
// associations before paging so an ambiguous match cannot displace a valid one.
func (s *Store) Recent(ctx context.Context, f RecentFilter) ([]Observation, error) {
	observations, _, err := s.recent(ctx, f, false)
	return observations, err
}

// recent is Recent, with the automatic deals of the page when automatic is set, read in one
// snapshot. It picks the page of listings first and reads raw and the joined edition and title
// only for that page.
func (s *Store) recent(ctx context.Context, f RecentFilter, automatic bool) ([]Observation, []AutomaticDeal, error) {
	markets, err := s.marketTable(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("recent observations: %w", err)
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, fmt.Errorf("recent observations: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	filtered := f.Matched || f.UnderLimit || f.Sent
	var wishes wishedCopies
	if filtered {
		if wishes, err = readWishedCopies(ctx, tx); err != nil {
			return nil, nil, fmt.Errorf("recent observations: %w", err)
		}
	}
	unverified, err := unverifiedAlbums(ctx, tx)
	if err != nil {
		return nil, nil, fmt.Errorf("verify stored album matches: %w", err)
	}
	var observations []Observation
	if filtered {
		observations, err = recentWished(ctx, tx, f, markets, wishes, unverified)
	} else {
		observations, err = recentPage(ctx, tx, markets, allListings, f.Limit, sources, unverified)
	}
	if err != nil {
		return nil, nil, fmt.Errorf("recent observations: %w", err)
	}
	if !automatic {
		return observations, nil, nil
	}
	deals, err := automaticDeals(ctx, tx, observations)
	return observations, deals, err
}

// recentWished reads the observations of a filtered page.
func recentWished(ctx context.Context, tx pgx.Tx, f RecentFilter, markets marketTable, wishes wishedCopies,
	unverified []int64,
) ([]Observation, error) {
	page, err := wishedPage(ctx, tx, f, markets, wishes, unverified)
	if err != nil {
		return nil, err
	}
	ids, wishIDs := make([]int64, len(page)), make([]int64, len(page))
	for i, l := range page {
		ids[i], wishIDs[i] = l.id, l.wishID
	}
	return recentPage(ctx, tx, markets, pageListings, ids, wishIDs)
}

// wishedCopies are the wishlist copies the hunter hunts and the instant a filtered page judges
// listings fresh at.
type wishedCopies struct {
	ids    []int64
	cutoff time.Time
}

// readWishedCopies reads the wishlist copies the hunter hunts. It is the first query of a page's
// snapshot, and listings are fresh as of its clock, so a listing that expired before a change
// the snapshot holds, such as its alert's send, is never shown as fresh.
func readWishedCopies(ctx context.Context, tx pgx.Tx) (wishedCopies, error) {
	var w wishedCopies
	if err := tx.QueryRow(ctx, `SELECT clock_timestamp(), coalesce(array_agg(w.id), '{}')
		FROM copy w WHERE w.status = 'wishlist' AND `+huntedWish("w")).Scan(&w.cutoff, &w.ids); err != nil {
		return w, fmt.Errorf("wished copies: %w", err)
	}
	return w, nil
}

// recentPage reads, newest first, the observations of the page that listings picks: SQL that
// takes args as its parameters from $5 on and returns each listing's ID and the ID of the
// wishlist copy that matches it.
func recentPage(ctx context.Context, q querier, markets marketTable, listings string, args ...any) ([]Observation, error) {
	rows, err := q.Query(ctx, `SELECT o.id, o.source, coalesce(o.external_id, ''), o.observed_at,
			o.price_cents, o.acquired_as, o.url, coalesce(o.raw->>'title', ''), coalesce(e.title_id, o.title_id), t.name,
			e.id, e.format, nullif(o.raw->>'photo_url', ''),
            coalesce(CASE WHEN e.cover_photo IS NOT NULL
                THEN '/api/editions/' || e.id || '/cover?v=' || extract(epoch FROM e.updated_at) END,
                nullif(e.cover_url, ''), t.cover_url), w.max_price_cents, w.max_market_percent,
			CASE WHEN w.max_market_percent IS NOT NULL THEN m.cents END,
			CASE WHEN a.observation_id IS NULL OR a.sent_at IS NULL AND a.copy_id IS NULL THEN 'none'
			     WHEN a.sent_at IS NOT NULL THEN 'sent'
			     WHEN a.attempts >= $1 THEN 'failed'
			     ELSE 'pending' END,
			coalesce(a.last_error, ''), coalesce(a.attempts, 0)
		FROM (`+listings+`) page
		JOIN price_observation o ON o.id = page.id
		LEFT JOIN deal_alert a ON a.observation_id = o.id
		LEFT JOIN copy w ON w.id = page.wish_id
		LEFT JOIN edition e ON e.id = coalesce(w.edition_id, o.edition_id)
		LEFT JOIN title t ON t.id = coalesce(e.title_id, o.title_id)
		`+marketJoin("e", 2)+`
		ORDER BY o.observed_at DESC, o.id DESC`,
		append([]any{MaxAttempts, markets.titles, markets.formats, markets.cents}, args...)...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToStructByPos[Observation])
}

// pageRow is a listing of a Recent page and the wishlist copy that matches it.
type pageRow struct {
	id         int64
	observedAt time.Time
	wishID     int64
}

// minWalk is how many of the hunter's listings wishedPage reads at least in its first step.
const minWalk = 100

// wishedPage picks the newest f.Limit listings with a matching copy, the only ones a filter
// keeps, that pass f's filters. It takes them apart from the listings an alert names the copy
// for and from those the hunter recorded for the copy, then merges the two. The hunter's
// listings, nearly all of them when a long hunt with alerts has run, it reads newest first
// through price_observation_wish_recent_idx, in steps of twice as many as the last one, and
// stops once the page is full, so it never sorts or filters all of them.
func wishedPage(ctx context.Context, tx pgx.Tx, f RecentFilter, markets marketTable, wishes wishedCopies,
	unverified []int64,
) ([]pageRow, error) {
	cond := "true"
	if f.UnderLimit {
		cond += ` AND (o.price_cents <= w.max_price_cents OR o.price_cents * 100 <= m.cents * w.max_market_percent)`
	}
	if f.Sent {
		cond += ` AND a.sent_at IS NOT NULL AND (a.automatic OR w.max_price_cents IS NOT NULL OR w.max_market_percent IS NOT NULL)`
	}
	if len(wishes.ids) == 0 {
		return nil, nil
	}
	fresh := freshAt("$6::timestamptz")
	// The planner counts the alerts of copies on the wishlist from the materialized copies, so
	// it reads the few listings an alert alone ties to a copy first when most alerts are the
	// hunter's own, instead of looking up the listing of every alert.
	page, err := collectPage(tx.Query(ctx, `WITH wished AS MATERIALIZED (
			SELECT w.id FROM copy w WHERE w.status = 'wishlist' AND `+huntedWish("w")+`)
		SELECT o.id, o.observed_at, w.id
		FROM deal_alert a
		JOIN wished ON wished.id = a.copy_id
		JOIN copy w ON w.id = a.copy_id
		JOIN price_observation o ON o.id = a.observation_id
		LEFT JOIN edition ed ON ed.id = w.edition_id
		`+marketJoin("ed", 3)+`
		WHERE o.wishlist_copy_id IS NULL AND o.dismissed_at IS NULL AND o.source = ANY ($2) AND o.id <> ALL ($7)
			AND `+fresh+` AND `+wishOfListingTitle("w")+` AND `+wishNamedBy("w")+`
			AND `+mediumFits("w")+` AND `+cond+`
		ORDER BY o.observed_at DESC, o.id DESC LIMIT $1`,
		f.Limit, sources, markets.titles, markets.formats, markets.cents, wishes.cutoff, unverified))
	if err != nil {
		return nil, fmt.Errorf("alerted listings: %w", err)
	}
	hunted := 0
	var last *pageRow
	for step := max(f.Limit, minWalk); hunted < f.Limit; step *= 2 {
		walked, err := walkHunted(ctx, tx, wishes.ids, last, step)
		if err != nil {
			return nil, err
		}
		if len(walked) == 0 {
			break
		}
		last = &walked[len(walked)-1]
		ids := make([]int64, len(walked))
		for i, l := range walked {
			ids[i] = l.id
		}
		// mediumFits reads each listing's raw, so it runs last, on the listings the rest keeps.
		matched, err := collectPage(tx.Query(ctx, `WITH kept AS MATERIALIZED (
				SELECT o.id, w.id AS wish_id
				FROM price_observation o
				JOIN copy w ON w.id = o.wishlist_copy_id
				LEFT JOIN deal_alert a ON a.observation_id = o.id
				LEFT JOIN edition ed ON ed.id = w.edition_id
				`+marketJoin("ed", 3)+`
				WHERE o.id = ANY ($1) AND o.id <> ALL ($7) AND o.dismissed_at IS NULL AND o.source = ANY ($2)
					AND `+fresh+`
					AND w.status = 'wishlist' AND `+huntedWish("w")+` AND `+wishNamedBy("w")+` AND `+cond+`)
			SELECT o.id, o.observed_at, w.id
			FROM kept
			JOIN price_observation o ON o.id = kept.id
			JOIN copy w ON w.id = kept.wish_id
			WHERE `+mediumFits("w"),
			ids, sources, markets.titles, markets.formats, markets.cents, wishes.cutoff, unverified))
		if err != nil {
			return nil, fmt.Errorf("hunted listings: %w", err)
		}
		page = append(page, matched...)
		hunted += len(matched)
		if len(walked) < step {
			break
		}
	}
	slices.SortFunc(page, func(x, y pageRow) int {
		if c := y.observedAt.Compare(x.observedAt); c != 0 {
			return c
		}
		return cmp.Compare(y.id, x.id)
	})
	return page[:min(len(page), f.Limit)], nil
}

// walkHunted returns the next n listings the hunter recorded for one of the wishlist copies
// wished, newest first, after last, or from the newest when last is nil. Each query is planned
// for its own wished copies (QueryExecModeDescribeExec), so the planner reads the newest of
// them through price_observation_wish_recent_idx while most listings belong to those copies,
// and through price_observation_wish_idx when few do, as when the copies the hunter recorded
// most listings for have since been bought.
func walkHunted(ctx context.Context, tx pgx.Tx, wished []int64, last *pageRow, n int) ([]pageRow, error) {
	var rows pgx.Rows
	var err error
	if last == nil {
		rows, err = tx.Query(ctx, `SELECT id, observed_at FROM price_observation
			WHERE wishlist_copy_id = ANY ($2)
			ORDER BY observed_at DESC, id DESC LIMIT $1`, pgx.QueryExecModeDescribeExec, n, wished)
	} else {
		rows, err = tx.Query(ctx, `SELECT id, observed_at FROM price_observation
			WHERE wishlist_copy_id = ANY ($2) AND (observed_at, id) < ($3, $4)
			ORDER BY observed_at DESC, id DESC LIMIT $1`, pgx.QueryExecModeDescribeExec, n, wished, last.observedAt, last.id)
	}
	if err != nil {
		return nil, fmt.Errorf("hunted listings: %w", err)
	}
	walked, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (pageRow, error) {
		var l pageRow
		err := row.Scan(&l.id, &l.observedAt)
		return l, err
	})
	if err != nil {
		return nil, fmt.Errorf("hunted listings: %w", err)
	}
	return walked, nil
}

// collectPage reads the listing ID, observation time and wishlist copy ID of each row.
func collectPage(rows pgx.Rows, err error) ([]pageRow, error) {
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, func(row pgx.CollectableRow) (pageRow, error) {
		var l pageRow
		err := row.Scan(&l.id, &l.observedAt, &l.wishID)
		return l, err
	})
}

// Dismiss hides the deal listing id from the deals view and keeps its
// unsent alert from going out, for every wish it matches; the observation,
// its alert and its price for the valuation stay. A repeat keeps the first
// dismissal. ErrNotFound when no deal listing has the id.
func (s *Store) Dismiss(ctx context.Context, id int64) error {
	return s.setDismissed(ctx, id, true)
}

// Undismiss brings a dismissed deal listing back; a listing that is not
// dismissed stays as it is. ErrNotFound when no deal listing has the id.
func (s *Store) Undismiss(ctx context.Context, id int64) error {
	return s.setDismissed(ctx, id, false)
}

// DismissPress is Dismiss for the press callbackQueryID of a Telegram
// alert's "Ignoruj" button, applied once: ErrPressReplayed, with nothing
// changed, when that press already dismissed a listing, so a press
// Telegram sends again after a restart does not undo an Okazje "Cofnij"
// made in between.
func (s *Store) DismissPress(ctx context.Context, callbackQueryID string, id int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("dismiss press: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	fresh, err := recordPress(ctx, tx, callbackQueryID)
	if err != nil {
		return err
	}
	if !fresh {
		return ErrPressReplayed
	}
	if err := setDismissed(ctx, tx, id, true); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("dismiss press: commit: %w", err)
	}
	return nil
}

// BuyPress applies the press callbackQueryID of a Telegram alert's
// "Kupione" button for the listing id and the wishlist copy copyID the
// alert named: the copy becomes an owned copy bought today (in Poland) at
// the listing's price, in the source's shop, new or used as the listing
// says (the copy's own value when it says nothing), with its limits
// cleared; it keeps its id, edition, condition and notes, and the listing
// is dismissed.
//
// A button buys once, and only on a press's first delivery. Every press
// is recorded, refused ones too, and every purchase in telegram_purchase,
// so a press Telegram sends again, or a later press of the same button,
// only reports the state: a copy owned with the listing dismissed is
// ErrAlreadyBought, a copy the button bought and the collector changed
// back is ErrBoughtBefore, anything else ErrNotWished, as for a fresh
// press of a copy no longer on the wishlist. ErrNotFound when no alerted
// deal listing has the id. None of these errors changes the copy or the
// listing.
func (s *Store) BuyPress(ctx context.Context, callbackQueryID string, id, copyID int64) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("buy press: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	outcome := buyInTx(ctx, tx, callbackQueryID, id, copyID)
	if outcome != nil && !errors.Is(outcome, ErrNotFound) && !errors.Is(outcome, ErrNotWished) &&
		!errors.Is(outcome, ErrAlreadyBought) && !errors.Is(outcome, ErrBoughtBefore) {
		return outcome
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("buy press: commit: %w", err)
	}
	return outcome
}

// buyInTx is BuyPress inside its transaction; the press's record commits
// with the purchase or a refusal.
func buyInTx(ctx context.Context, tx pgx.Tx, callbackQueryID string, id, copyID int64) error {
	fresh, err := recordPress(ctx, tx, callbackQueryID)
	if err != nil {
		return err
	}
	var (
		priceCents int64
		acquiredAs *string
		source     string
		dismissed  bool
	)
	err = tx.QueryRow(ctx, `SELECT o.price_cents, o.acquired_as, o.source, o.dismissed_at IS NOT NULL
		FROM price_observation o
		WHERE o.id = $1 AND o.source = ANY ($2)
			AND EXISTS (SELECT 1 FROM deal_alert a WHERE a.observation_id = o.id)
		FOR UPDATE`, id, sources).Scan(&priceCents, &acquiredAs, &source, &dismissed)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("buy press: listing: %w", err)
	}
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM copy WHERE id = $1 FOR UPDATE`, copyID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotWished
	}
	if err != nil {
		return fmt.Errorf("buy press: copy: %w", err)
	}
	var boughtBefore bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM telegram_purchase
		WHERE observation_id = $1 AND copy_id = $2)`, id, copyID).Scan(&boughtBefore); err != nil {
		return fmt.Errorf("buy press: purchase: %w", err)
	}
	if !fresh || boughtBefore || status != "wishlist" {
		switch {
		case status == "owned" && dismissed:
			return ErrAlreadyBought
		case boughtBefore:
			return ErrBoughtBefore
		}
		return ErrNotWished
	}
	shop, ok := sourceLabels[source]
	if !ok {
		shop = source
	}
	if _, err := tx.Exec(ctx, `UPDATE copy SET status = 'owned', price_cents = $2,
			purchased_on = (now() AT TIME ZONE 'Europe/Warsaw')::date, shop = $3,
			acquired_as = coalesce($4, acquired_as), max_price_cents = NULL, max_market_percent = NULL,
			exact_edition = false, updated_at = now()
		WHERE id = $1`, copyID, priceCents, shop, acquiredAs); err != nil {
		return fmt.Errorf("buy press: copy: %w", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO telegram_purchase (observation_id, copy_id) VALUES ($1, $2)`,
		id, copyID); err != nil {
		return fmt.Errorf("buy press: record purchase: %w", err)
	}
	return setDismissed(ctx, tx, id, true)
}

// recordPress stores the Telegram press callbackQueryID and reports
// whether it is new, so a press Telegram sends again is applied once.
// Records older than two days, long after Telegram dropped the press,
// are pruned.
func recordPress(ctx context.Context, tx pgx.Tx, callbackQueryID string) (bool, error) {
	if _, err := tx.Exec(ctx, `DELETE FROM telegram_press WHERE pressed_at < now() - interval '2 days'`); err != nil {
		return false, fmt.Errorf("press: prune: %w", err)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO telegram_press (callback_query_id) VALUES ($1)
		ON CONFLICT DO NOTHING`, callbackQueryID)
	if err != nil {
		return false, fmt.Errorf("press: record: %w", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *Store) setDismissed(ctx context.Context, id int64, dismissed bool) error {
	return setDismissed(ctx, s.pool, id, dismissed)
}

func setDismissed(ctx context.Context, q querier, id int64, dismissed bool) error {
	tag, err := q.Exec(ctx, `UPDATE price_observation
		SET dismissed_at = CASE WHEN $3 THEN coalesce(dismissed_at, now()) END
		WHERE id = $1 AND source = ANY ($2)`, id, sources, dismissed)
	if err != nil {
		return fmt.Errorf("set dismissed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SearchedWishCount returns how many wishlist copies the hunter looks for:
// every wish but those of TV series, as Wants reads them.
func (s *Store) SearchedWishCount(ctx context.Context) (int, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM copy c JOIN edition e ON e.id = c.edition_id
		JOIN title t ON t.id = e.title_id
		WHERE c.status = 'wishlist' AND t.kind <> 'tv'`).Scan(&n); err != nil {
		return 0, fmt.Errorf("searched wish count: %w", err)
	}
	return n, nil
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

type storedRunResult struct {
	RunResult
	Coverage *runCoverage `json:"coverage,omitempty"`
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
		var res storedRunResult
		if err := json.Unmarshal(result, &res); err != nil {
			return LastRun{}, false, fmt.Errorf("last run result: %w", err)
		}
		res.coverage = res.Coverage
		run.Result = &res.RunResult
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
	result, err := json.Marshal(storedRunResult{RunResult: *res, Coverage: res.coverage})
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
