// Package seed fills an empty database with demo data for local work: a
// movie, book and music collection with prices, a personal cover photo,
// wishlist deals from more than one source, a valuation history and scan
// queue entries. It sends no request anywhere; listing links point at
// example.com. A database that already holds data is refused, so a run
// against production cannot mix demo rows into a real collection. It
// seeds no deal run: a running hunter reads that only at startup.
package seed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotEmpty means the database already holds titles, listings, value
// history, scan queue entries or a deal run, so the seed changed nothing.
var ErrNotEmpty = errors.New("database already holds data; the seed only fills an empty one")

// Counts is what a seed added.
type Counts struct {
	Titles       int
	Editions     int
	Copies       int
	Observations int
	Alerts       int
	HistoryDays  int
	ScanQueue    int
}

// historyDays is how many past days of collection value the seed stores,
// ending yesterday, so the next valuation run writes today's row.
const historyDays = 45

// maxAttempts matches deals.MaxAttempts: a failed alert has used them all.
const maxAttempts = 5

type title struct {
	kind, name    string
	originalTitle *string
	year          int
	tmdbID        *int64
	authors       []string
	editions      []edition
}

// edition is one demo release. valuationPrices are what the valuation
// sources saw for an owned edition; dealListings are what the deal hunter
// found for a wish.
type edition struct {
	barcode, label, publisher string
	year                      int
	format, binding           *string
	plSubtitles, plVoiceover  string
	coverPhoto                bool
	owned                     *owned
	wish                      *wish
	valuationPrices           []listing
	dealListings              []listing
}

type owned struct {
	priceCents       int64
	purchasedDaysAgo int
	shop             string
	acquiredAs       string
	condition        string
}

type wish struct {
	maxPriceCents int64
}

// listing is one observation, first seen firstSeenHoursAgo and last seen
// lastSeenHoursAgo. alert is the deal alert's state for a listing under
// the wish's limit, "sent" or "failed", or empty for none. Pending alerts
// are never seeded, so a configured Telegram bot does not deliver demo
// alerts. A SkupSzop price of 0 is a book the shop does not buy.
type listing struct {
	source                              string
	title                               string
	priceCents                          int64
	acquiredAs                          *string
	firstSeenHoursAgo, lastSeenHoursAgo int
	alert                               string
}

type queued struct {
	barcode, reason, detail string
	scans, hoursAgo         int
}

var used = new("used")

// demo is the seeded collection. Barcodes carry valid check digits and are
// in their normalized form.
var demo = []title{
	{
		kind: "movie", name: "Łowca androidów", originalTitle: new("Blade Runner"), year: 1982, tmdbID: new(int64(78)),
		editions: []edition{{
			barcode: "5903567700015", label: "Final Cut", publisher: "Galapagos", year: 2017,
			format: new("bluray"), plSubtitles: "yes", plVoiceover: "yes", coverPhoto: true,
			owned: &owned{priceCents: 3999, purchasedDaysAgo: 400, shop: "Empik", acquiredAs: "new", condition: "mint"},
			valuationPrices: []listing{
				{source: "olx", title: "Łowca androidów Blu-ray Final Cut", priceCents: 3500, acquiredAs: used, firstSeenHoursAgo: 24 * 40, lastSeenHoursAgo: 30},
				{source: "olx", title: "Blade Runner Łowca androidów BD PL", priceCents: 3900, acquiredAs: used, firstSeenHoursAgo: 24 * 25, lastSeenHoursAgo: 30},
				{source: "olx", title: "Łowca androidów [Blu-ray] lektor", priceCents: 4200, acquiredAs: used, firstSeenHoursAgo: 24 * 12, lastSeenHoursAgo: 30},
				{source: "olx", title: "Łowca androidów Blu-ray kolekcjonerskie", priceCents: 5500, acquiredAs: used, firstSeenHoursAgo: 24 * 5, lastSeenHoursAgo: 30},
			},
		}},
	},
	{
		kind: "movie", name: "Diuna", originalTitle: new("Dune"), year: 2021, tmdbID: new(int64(438631)),
		editions: []edition{
			{
				barcode: "5903567700022", publisher: "Warner Bros.", year: 2022,
				format: new("uhd"), plSubtitles: "yes", plVoiceover: "yes",
				owned: &owned{priceCents: 8999, purchasedDaysAgo: 300, shop: "Media Expert", acquiredAs: "new", condition: "mint"},
				valuationPrices: []listing{
					{source: "olx", title: "Diuna 4K UHD + Blu-ray", priceCents: 7900, acquiredAs: used, firstSeenHoursAgo: 24 * 20, lastSeenHoursAgo: 30},
					{source: "olx", title: "Diuna 4K Ultra HD PL", priceCents: 8500, acquiredAs: used, firstSeenHoursAgo: 24 * 8, lastSeenHoursAgo: 30},
				},
			},
			{
				barcode: "5903567700039", publisher: "Warner Bros.", year: 2022,
				format: new("bluray"), plSubtitles: "yes", plVoiceover: "yes",
				wish: &wish{maxPriceCents: 3000},
				dealListings: []listing{
					{source: "olx", title: "Diuna Blu-ray PL lektor", priceCents: 2799, acquiredAs: used, firstSeenHoursAgo: 3, lastSeenHoursAgo: 3, alert: "sent"},
					{source: "pepper", title: "Diuna [Blu-ray] w promocji", priceCents: 2900, firstSeenHoursAgo: 26, lastSeenHoursAgo: 3, alert: "sent"},
					{source: "olx", title: "Diuna Blu-ray steelbook", priceCents: 3500, acquiredAs: used, firstSeenHoursAgo: 50, lastSeenHoursAgo: 3},
				},
			},
		},
	},
	{
		kind: "movie", name: "Siedem", originalTitle: new("Se7en"), year: 1995, tmdbID: new(int64(807)),
		editions: []edition{{
			barcode: "5903567700046", publisher: "Galapagos", year: 2010,
			format: new("dvd"), plSubtitles: "yes", plVoiceover: "unknown",
			wish: &wish{maxPriceCents: 2500},
			dealListings: []listing{
				{source: "olx", title: "Siedem DVD lektor PL", priceCents: 1999, acquiredAs: used, firstSeenHoursAgo: 5, lastSeenHoursAgo: 3, alert: "sent"},
				{source: "pepper", title: "Siedem (DVD)", priceCents: 2700, firstSeenHoursAgo: 30, lastSeenHoursAgo: 3},
			},
		}},
	},
	{
		kind: "book", name: "Ostatnie życzenie", authors: []string{"Andrzej Sapkowski"}, year: 1993,
		editions: []edition{{
			barcode: "9788300000012", publisher: "SuperNowa", year: 2014, binding: new("paperback"),
			plSubtitles: "unknown", plVoiceover: "unknown",
			owned: &owned{priceCents: 3490, purchasedDaysAgo: 200, shop: "OLX", acquiredAs: "used", condition: "good"},
			valuationPrices: []listing{
				{source: "olx", title: "Sapkowski Ostatnie życzenie", priceCents: 2200, acquiredAs: used, firstSeenHoursAgo: 24 * 30, lastSeenHoursAgo: 30},
				{source: "olx", title: "Ostatnie życzenie - Andrzej Sapkowski", priceCents: 2500, acquiredAs: used, firstSeenHoursAgo: 24 * 15, lastSeenHoursAgo: 30},
				{source: "olx", title: "Wiedźmin Ostatnie życzenie Sapkowski", priceCents: 2800, acquiredAs: used, firstSeenHoursAgo: 24 * 6, lastSeenHoursAgo: 30},
				{source: "skupszop", title: "Ostatnie życzenie", priceCents: 640, firstSeenHoursAgo: 30, lastSeenHoursAgo: 30},
			},
		}},
	},
	{
		kind: "book", name: "Solaris", authors: []string{"Stanisław Lem"}, year: 1961,
		editions: []edition{{
			barcode: "9788300000029", publisher: "Wydawnictwo Literackie", year: 2012, binding: new("hardcover"),
			plSubtitles: "unknown", plVoiceover: "unknown",
			owned: &owned{priceCents: 4500, purchasedDaysAgo: 150, shop: "Empik", acquiredAs: "new", condition: "very_good"},
			valuationPrices: []listing{
				{source: "olx", title: "Lem Solaris twarda okładka", priceCents: 3000, acquiredAs: used, firstSeenHoursAgo: 24 * 35, lastSeenHoursAgo: 30},
				{source: "olx", title: "Stanisław Lem - Solaris", priceCents: 3600, acquiredAs: used, firstSeenHoursAgo: 24 * 18, lastSeenHoursAgo: 30},
				{source: "olx", title: "Solaris Lem Wydawnictwo Literackie", priceCents: 4100, acquiredAs: used, firstSeenHoursAgo: 24 * 4, lastSeenHoursAgo: 30},
				{source: "skupszop", title: "Solaris", priceCents: 0, firstSeenHoursAgo: 30, lastSeenHoursAgo: 30},
			},
		}},
	},
	{
		kind: "book", name: "Cyberiada", authors: []string{"Stanisław Lem"}, year: 1965,
		editions: []edition{{
			barcode: "9788300000036", publisher: "Wydawnictwo Literackie", year: 2013, binding: new("paperback"),
			plSubtitles: "unknown", plVoiceover: "unknown",
			wish: &wish{maxPriceCents: 3000},
			dealListings: []listing{
				{source: "olx", title: "Cyberiada Stanisław Lem", priceCents: 2500, acquiredAs: used, firstSeenHoursAgo: 8, lastSeenHoursAgo: 3, alert: "failed"},
			},
		}},
	},
	{
		kind: "music", name: "Kind of Blue", authors: []string{"Miles Davis"}, year: 1959,
		editions: []edition{{
			barcode: "5900000001012", label: "Legacy Edition", publisher: "Columbia", year: 2009,
			format: new("cd"), plSubtitles: "unknown", plVoiceover: "unknown",
			owned: &owned{priceCents: 2999, purchasedDaysAgo: 100, shop: "Discogs", acquiredAs: "used", condition: "very_good"},
			valuationPrices: []listing{
				{source: "olx", title: "Miles Davis Kind of Blue CD", priceCents: 2500, acquiredAs: used, firstSeenHoursAgo: 24 * 22, lastSeenHoursAgo: 30},
				{source: "olx", title: "Kind of Blue - Miles Davis (CD)", priceCents: 2900, acquiredAs: used, firstSeenHoursAgo: 24 * 11, lastSeenHoursAgo: 30},
				{source: "olx", title: "Miles Davis – Kind Of Blue płyta CD", priceCents: 3200, acquiredAs: used, firstSeenHoursAgo: 24 * 3, lastSeenHoursAgo: 30},
			},
		}},
	},
	{
		kind: "music", name: "Nevermind", authors: []string{"Nirvana"}, year: 1991,
		editions: []edition{{
			barcode: "5900000001029", publisher: "DGC", year: 1991,
			format: new("cassette"), plSubtitles: "unknown", plVoiceover: "unknown",
			owned: &owned{priceCents: 1500, purchasedDaysAgo: 20, shop: "Giełda staroci", acquiredAs: "used", condition: "good"},
		}},
	},
	{
		kind: "music", name: "OK Computer", authors: []string{"Radiohead"}, year: 1997,
		editions: []edition{{
			barcode: "5900000001036", publisher: "XL Recordings", year: 2017,
			format: new("vinyl"), plSubtitles: "unknown", plVoiceover: "unknown",
			wish: &wish{maxPriceCents: 9000},
			dealListings: []listing{
				{source: "pepper", title: "Radiohead – OK Computer OKNOTOK winyl 3LP", priceCents: 8500, firstSeenHoursAgo: 1, lastSeenHoursAgo: 1, alert: "sent"},
				{source: "olx", title: "Radiohead OK Computer LP", priceCents: 11900, acquiredAs: used, firstSeenHoursAgo: 72, lastSeenHoursAgo: 3},
			},
		}},
	},
}

var demoQueue = []queued{
	{barcode: "5909990000012", reason: "not_found", scans: 2, hoursAgo: 4},
	{barcode: "5909990000029", reason: "lookup_failed", detail: "movie: context deadline exceeded", scans: 1, hoursAgo: 28},
	{barcode: "5909990000036", reason: "not_found", scans: 1, hoursAgo: 75},
	{barcode: "59012344", reason: "not_found", scans: 3, hoursAgo: 150},
}

// dataTables are the tables whose rows make a database not empty.
var dataTables = []string{"title", "price_observation", "valuation_history", "scan_queue", "deal_run"}

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// CheckEmpty returns ErrNotEmpty when any data table holds a row. A table
// that does not exist yet, as before the migrations ran, holds none, so a
// caller can check a database before migrating it and refuse it unchanged.
func CheckEmpty(ctx context.Context, q querier) error {
	for _, table := range dataTables {
		var exists bool
		if err := q.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table).Scan(&exists); err != nil {
			return fmt.Errorf("seed: check %s: %w", table, err)
		}
		if !exists {
			continue
		}
		var taken bool
		sql := `SELECT EXISTS (SELECT 1 FROM ` + pgx.Identifier{table}.Sanitize() + `)`
		if err := q.QueryRow(ctx, sql).Scan(&taken); err != nil {
			return fmt.Errorf("seed: check %s: %w", table, err)
		}
		if taken {
			return ErrNotEmpty
		}
	}
	return nil
}

// Run fills an empty, migrated database with the demo collection in one
// transaction. It returns ErrNotEmpty, having changed nothing, when the
// database already holds titles, listings, value history, scan queue
// entries or a deal run. It locks those tables before it checks them, so
// a second seed, or a write from a running app such as a queued scan,
// waits until this one commits and cannot slip in after the check.
func Run(ctx context.Context, pool *pgxpool.Pool) (Counts, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return Counts{}, fmt.Errorf("seed: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	_, err = tx.Exec(ctx, `LOCK TABLE title, price_observation, valuation_history, scan_queue, deal_run
		IN EXCLUSIVE MODE`)
	if err != nil {
		return Counts{}, fmt.Errorf("seed: lock: %w", err)
	}
	if err := CheckEmpty(ctx, tx); err != nil {
		return Counts{}, err
	}
	photo, err := coverPhoto()
	if err != nil {
		return Counts{}, err
	}
	s := seeder{tx: tx, photo: photo}
	for i := range demo {
		if err := s.title(ctx, &demo[i]); err != nil {
			return Counts{}, err
		}
	}
	if err := s.queue(ctx); err != nil {
		return Counts{}, err
	}
	if err := s.history(ctx); err != nil {
		return Counts{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Counts{}, fmt.Errorf("seed: commit: %w", err)
	}
	return s.counts, nil
}

// seeder writes the demo rows in one transaction and counts them;
// listingSeq numbers the listings for their external IDs and URLs.
type seeder struct {
	tx         pgx.Tx
	photo      []byte
	counts     Counts
	listingSeq int
}

func (s *seeder) title(ctx context.Context, t *title) error {
	var id int64
	err := s.tx.QueryRow(ctx, `INSERT INTO title (kind, name, original_title, year, tmdb_id, authors)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		t.kind, t.name, t.originalTitle, t.year, t.tmdbID, append([]string{}, t.authors...)).Scan(&id)
	if err != nil {
		return fmt.Errorf("seed title %q: %w", t.name, err)
	}
	s.counts.Titles++
	for i := range t.editions {
		if err := s.edition(ctx, id, &t.editions[i]); err != nil {
			return err
		}
	}
	return nil
}

// edition stores e with its copies and listings. The personal cover photo
// sets edited_at, as an upload does.
func (s *seeder) edition(ctx context.Context, titleID int64, e *edition) error {
	var photo []byte
	if e.coverPhoto {
		photo = s.photo
	}
	var id int64
	err := s.tx.QueryRow(ctx, `INSERT INTO edition (title_id, barcode, label, publisher, year, format,
			pl_subtitles, pl_voiceover, binding, cover_photo, edited_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
			CASE WHEN $10::bytea IS NOT NULL THEN now() END)
		RETURNING id`,
		titleID, e.barcode, e.label, e.publisher, e.year, e.format, e.plSubtitles, e.plVoiceover,
		e.binding, photo).Scan(&id)
	if err != nil {
		return fmt.Errorf("seed edition %s: %w", e.barcode, err)
	}
	s.counts.Editions++
	if o := e.owned; o != nil {
		_, err := s.tx.Exec(ctx, `INSERT INTO copy (edition_id, status, price_cents, purchased_on, shop,
				acquired_as, condition)
			VALUES ($1, 'owned', $2, current_date - $3::int, $4, $5, $6)`,
			id, o.priceCents, o.purchasedDaysAgo, o.shop, o.acquiredAs, o.condition)
		if err != nil {
			return fmt.Errorf("seed owned copy of %s: %w", e.barcode, err)
		}
		s.counts.Copies++
	}
	for i := range e.valuationPrices {
		if _, err := s.listing(ctx, &e.valuationPrices[i], e.barcode, titleID, id, 0); err != nil {
			return err
		}
	}
	if e.wish == nil {
		return nil
	}
	var copyID int64
	err = s.tx.QueryRow(ctx, `INSERT INTO copy (edition_id, status, max_price_cents)
		VALUES ($1, 'wishlist', $2) RETURNING id`, id, e.wish.maxPriceCents).Scan(&copyID)
	if err != nil {
		return fmt.Errorf("seed wish for %s: %w", e.barcode, err)
	}
	s.counts.Copies++
	for i := range e.dealListings {
		l := &e.dealListings[i]
		obsID, err := s.listing(ctx, l, e.barcode, titleID, id, copyID)
		if err != nil {
			return err
		}
		if l.alert == "" {
			continue
		}
		if err := s.alert(ctx, l, obsID, copyID); err != nil {
			return fmt.Errorf("seed alert for %q: %w", l.title, err)
		}
	}
	return nil
}

// listing stores one observation the way the deal hunter and the
// valuation record theirs; wishID is the wishlist copy a deal listing was
// found for, 0 for a valuation price. A SkupSzop quote is keyed by the
// book's barcode, as the valuation keys it.
func (s *seeder) listing(ctx context.Context, l *listing, barcode string, titleID, editionID, wishID int64) (int64, error) {
	s.listingSeq++
	externalID := fmt.Sprintf("demo-%d", s.listingSeq)
	url := fmt.Sprintf("https://example.com/ohara-demo/%s/%d", l.source, s.listingSeq)
	if l.source == "skupszop" {
		externalID = barcode
	}
	raw := map[string]any{"title": l.title, "photo_url": ""}
	if wishID != 0 {
		raw["wishlist_copy_id"] = wishID
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		return 0, fmt.Errorf("seed listing %q: %w", l.title, err)
	}
	var id int64
	err = s.tx.QueryRow(ctx, `INSERT INTO price_observation (source, external_id, observed_at, last_seen_at,
			price_cents, acquired_as, url, edition_id, title_id, raw)
		VALUES ($1, $2, now() - make_interval(hours => $3), now() - make_interval(hours => $4),
			$5, $6, $7, $8, $9, $10)
		RETURNING id`,
		l.source, externalID, l.firstSeenHoursAgo, l.lastSeenHoursAgo, l.priceCents, l.acquiredAs, url,
		editionID, titleID, rawJSON).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("seed listing %q: %w", l.title, err)
	}
	s.counts.Observations++
	return id, nil
}

func (s *seeder) alert(ctx context.Context, l *listing, obsID, copyID int64) error {
	var err error
	switch l.alert {
	case "sent":
		_, err = s.tx.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id, created_at, sent_at, attempts)
			VALUES ($1, $2, now() - make_interval(hours => $3), now() - make_interval(hours => $3), 1)`,
			obsID, copyID, l.firstSeenHoursAgo)
	case "failed":
		_, err = s.tx.Exec(ctx, `INSERT INTO deal_alert (observation_id, copy_id, created_at, attempts, last_error)
			VALUES ($1, $2, now() - make_interval(hours => $3), $4, 'telegram: status 400')`,
			obsID, copyID, l.firstSeenHoursAgo, maxAttempts)
	default:
		return fmt.Errorf("unknown alert state %q", l.alert)
	}
	if err != nil {
		return err
	}
	s.counts.Alerts++
	return nil
}

// queue stores the scan queue entries, each first scanned a day before
// its last scan.
func (s *seeder) queue(ctx context.Context) error {
	for _, q := range demoQueue {
		_, err := s.tx.Exec(ctx, `INSERT INTO scan_queue (barcode, reason, detail, scans, created_at, last_scanned_at)
			VALUES ($1, $2, $3, $4, now() - make_interval(hours => $5 + 24), now() - make_interval(hours => $5))`,
			q.barcode, q.reason, q.detail, q.scans, q.hoursAgo)
		if err != nil {
			return fmt.Errorf("seed scan queue %s: %w", q.barcode, err)
		}
		s.counts.ScanQueue++
	}
	return nil
}

// history stores one value per day for the historyDays before today: the
// owned copies and what was paid for them as the seed holds them (the
// cassette bought 20 days ago counts from then on) and a middle that
// drifts up towards today's.
func (s *seeder) history(ctx context.Context) error {
	tag, err := s.tx.Exec(ctx, `INSERT INTO valuation_history (day, copies, spent_cents, floor_cents,
			floor_copies, mid_cents, mid_copies, ceiling_cents, ceiling_copies, low_confidence, computed_at)
		SELECT current_date - d,
			(SELECT count(*) FROM copy WHERE status = 'owned' AND purchased_on <= current_date - d),
			(SELECT coalesce(sum(price_cents), 0) FROM copy WHERE status = 'owned' AND purchased_on <= current_date - d),
			640, 2,
			21000 - d * 40 + (d % 7) * 90, 5,
			0, 0, 2,
			(current_date - d) + time '03:00'
		FROM generate_series(1, $1::int) AS d`, historyDays)
	if err != nil {
		return fmt.Errorf("seed valuation history: %w", err)
	}
	s.counts.HistoryDays = int(tag.RowsAffected())
	return nil
}

// coverPhoto draws the personal cover photo: a 400×600 PNG made here, so
// the seed needs no file or download.
func coverPhoto() ([]byte, error) {
	const w, h = 400, 600
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	stripe := color.RGBA{R: 240, G: 120, B: 40, A: 255}
	for y := range h {
		for x := range w {
			c := color.RGBA{R: uint8(20 + y*60/h), G: uint8(30 + x*40/w), B: uint8(90 + y*120/h), A: 255}
			if y > h*3/4 && y < h*3/4+12 {
				c = stripe
			}
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("seed cover photo: %w", err)
	}
	return buf.Bytes(), nil
}
