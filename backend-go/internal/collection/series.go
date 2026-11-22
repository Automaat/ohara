package collection

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// ErrSeriesNotFound is a series no title is in any more, or never was.
var ErrSeriesNotFound = errors.New("series not found")

// Series is a run of titles of one kind, such as the Wiedźmin books, with
// how many titles it holds and how many of its numbers have no owned copy.
type Series struct {
	ID   int64  `json:"id"`
	Kind string `json:"kind" ts:"Kind"`
	Name string `json:"name"`
	// TotalVolumes is how many volumes the series has, null when unknown.
	TotalVolumes *int `json:"total_volumes"`
	TitleCount   int  `json:"title_count"`
	MissingCount int  `json:"missing_count"`
}

// SeriesVolume is a title of a series and what of it is on the shelf.
type SeriesVolume struct {
	TitleID     int64    `json:"title_id"`
	Name        string   `json:"name"`
	PolishTitle *string  `json:"polish_title"`
	Year        *int     `json:"year"`
	Authors     []string `json:"authors"`
	CoverURL    *string  `json:"cover_url"`
	// Number is the title's number in the series, null when it has none.
	Number *int   `json:"number"`
	State  string `json:"state" ts:"VolumeState"`
}

// MissingVolume is a number of a series no title with an owned copy has:
// none of its titles carries it, or the one that does is only wished for,
// on order or without a copy.
type MissingVolume struct {
	Number  int    `json:"number"`
	TitleID *int64 `json:"title_id"`
	State   string `json:"state" ts:"VolumeState"`
}

// SeriesDetail is the GET /api/series/{id} body: the series, its titles by
// number (those without one last, by name) and its missing numbers.
type SeriesDetail struct {
	Series
	Volumes []SeriesVolume  `json:"volumes"`
	Missing []MissingVolume `json:"missing"`
}

// SeriesTotalInput is the body of PUT /api/series/{id}: the series' total of
// volumes, or null when unknown.
type SeriesTotalInput struct {
	TotalVolumes *int `json:"total_volumes"`
}

// volumeSlot is what the missing numbers need of a volume.
type volumeSlot struct {
	titleID int64
	number  *int
	state   string
}

// missingVolumes lists every number from 1 to the larger of total and the
// highest number the slots hold that no slot with an owned copy has.
func missingVolumes(total *int, slots []volumeSlot) []MissingVolume {
	last := 0
	if total != nil {
		last = *total
	}
	byNumber := make(map[int]volumeSlot, len(slots))
	for _, slot := range slots {
		if slot.number != nil {
			byNumber[*slot.number] = slot
			last = max(last, *slot.number)
		}
	}
	missing := []MissingVolume{}
	for n := 1; n <= last; n++ {
		slot, ok := byNumber[n]
		switch {
		case !ok:
			missing = append(missing, MissingVolume{Number: n, State: VolumeNone})
		case slot.state != VolumeOwned:
			id := slot.titleID
			missing = append(missing, MissingVolume{Number: n, TitleID: &id, State: slot.state})
		}
	}
	return missing
}

// volumeState selects what title t has on the shelf: an owned copy, its own
// or one of a box set holding it, else one on order, else a wish, else
// nothing. Sold copies count as nothing.
const volumeState = `(SELECT CASE
		WHEN bool_or(c.status = 'owned') OR EXISTS (SELECT 1 FROM edition_content ec
			JOIN copy bc ON bc.edition_id = ec.edition_id
			WHERE ec.title_id = t.id AND bc.status = 'owned') THEN 'owned'
		WHEN bool_or(c.status = 'ordered') THEN 'ordered'
		WHEN bool_or(c.status = 'wishlist') THEN 'wishlist'
		ELSE 'none' END
	FROM copy c JOIN edition e ON e.id = c.edition_id WHERE e.title_id = t.id)`

// volumeCover selects title t's cover, else the first of its editions' own
// photos or covers.
const volumeCover = `COALESCE(t.cover_url, (SELECT COALESCE(` + editionCoverOverrideURL + `, e.cover_url)
	FROM edition e WHERE e.title_id = t.id AND (e.cover_photo IS NOT NULL OR e.cover_url IS NOT NULL)
	ORDER BY e.id LIMIT 1))`

// storedSeries is the series a title is in as stored, all nil without one.
type storedSeries struct {
	id     *int64
	name   *string
	number *int
}

// joinSeries returns the ID of the series of kind that name names, making
// it when no series of that kind has the name's key. The series row stays
// locked until the transaction ends. A title moving from series left to
// another locks both rows, in key order, so saves that move titles between
// two series in opposite directions do not wait for each other; the
// title_series_drop_empty trigger then deletes left once no title is in it.
func joinSeries(ctx context.Context, tx pgx.Tx, kind, name string, left *int64) (int64, error) {
	key := foldCase(name)
	lockLeft := func() error {
		if left == nil {
			return nil
		}
		if _, err := tx.Exec(ctx, `SELECT 1 FROM series WHERE id = $1 FOR UPDATE`, *left); err != nil {
			return fmt.Errorf("lock series: %w", err)
		}
		return nil
	}
	leftFirst := false
	if left != nil {
		var leftKind, leftKey string
		err := tx.QueryRow(ctx, `SELECT kind, key FROM series WHERE id = $1`, *left).Scan(&leftKind, &leftKey)
		if err != nil {
			return 0, fmt.Errorf("read series: %w", err)
		}
		leftFirst = cmp.Or(cmp.Compare(leftKind, kind), cmp.Compare(leftKey, key)) < 0
	}
	if leftFirst {
		if err := lockLeft(); err != nil {
			return 0, err
		}
	}
	var id int64
	err := tx.QueryRow(ctx, `INSERT INTO series (kind, name, key) VALUES ($1, $2, $3)
		ON CONFLICT (kind, key) DO UPDATE SET name = series.name RETURNING id`,
		kind, name, key).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("join series: %w", err)
	}
	if !leftFirst && left != nil && *left != id {
		if err := lockLeft(); err != nil {
			return 0, err
		}
	}
	return id, nil
}

// ListSeries returns every series with its title and missing counts, by name.
func (s *Store) ListSeries(ctx context.Context) ([]Series, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.id, s.kind, s.name, s.total_volumes, t.id, t.series_number,
		`+volumeState+`
		FROM series s JOIN title t ON t.series_id = s.id
		ORDER BY s.key, s.kind, s.id`)
	if err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	defer rows.Close()
	out := []Series{}
	var slots []volumeSlot
	finish := func() {
		if len(out) > 0 {
			last := &out[len(out)-1]
			last.MissingCount = len(missingVolumes(last.TotalVolumes, slots))
		}
		slots = nil
	}
	for rows.Next() {
		var row Series
		var slot volumeSlot
		if err := rows.Scan(&row.ID, &row.Kind, &row.Name, &row.TotalVolumes,
			&slot.titleID, &slot.number, &slot.state); err != nil {
			return nil, fmt.Errorf("list series: scan: %w", err)
		}
		if len(out) == 0 || out[len(out)-1].ID != row.ID {
			finish()
			out = append(out, row)
		}
		out[len(out)-1].TitleCount++
		slots = append(slots, slot)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list series: %w", err)
	}
	finish()
	return out, nil
}

// GetSeries returns a series with its volumes and missing numbers, or
// ErrSeriesNotFound. Both reads share one snapshot, so the answer never
// pairs a series with the titles of a later moment, such as none once its
// last title left and the series went with it.
func (s *Store) GetSeries(ctx context.Context, id int64) (SeriesDetail, error) {
	var d SeriesDetail
	err := s.readSnapshot(ctx, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT id, kind, name, total_volumes FROM series WHERE id = $1`, id).
			Scan(&d.ID, &d.Kind, &d.Name, &d.TotalVolumes)
		if err != nil {
			return mapErr(err, ErrSeriesNotFound, "get series")
		}
		rows, err := tx.Query(ctx, `SELECT t.id, t.name, t.polish_title, t.year, t.authors, `+volumeCover+`,
			t.series_number, `+volumeState+`
			FROM title t WHERE t.series_id = $1
			ORDER BY t.series_number NULLS LAST, lower(t.name), t.id`, id)
		if err != nil {
			return fmt.Errorf("series volumes: %w", err)
		}
		d.Volumes, err = collect(rows, func(row pgx.Row) (SeriesVolume, error) {
			var v SeriesVolume
			err := row.Scan(&v.TitleID, &v.Name, &v.PolishTitle, &v.Year, &v.Authors, &v.CoverURL,
				&v.Number, &v.State)
			return v, err
		}, "series volumes")
		return err
	})
	if err != nil {
		return SeriesDetail{}, err
	}
	slots := make([]volumeSlot, len(d.Volumes))
	for i, v := range d.Volumes {
		slots[i] = volumeSlot{titleID: v.TitleID, number: v.Number, state: v.State}
	}
	d.TitleCount = len(d.Volumes)
	d.Missing = missingVolumes(d.TotalVolumes, slots)
	d.MissingCount = len(d.Missing)
	return d, nil
}

// readSnapshot runs fn in a read-only transaction whose statements all see
// one snapshot.
func (s *Store) readSnapshot(ctx context.Context, fn func(pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
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

// SetSeriesTotal sets a series' total of volumes, nil when unknown.
func (s *Store) SetSeriesTotal(ctx context.Context, id int64, total *int) (SeriesDetail, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE series SET total_volumes = $2, updated_at = now() WHERE id = $1`, id, total)
	if err != nil {
		return SeriesDetail{}, fmt.Errorf("set series total: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return SeriesDetail{}, ErrSeriesNotFound
	}
	return s.GetSeries(ctx, id)
}

func (h *Handler) listSeries(w http.ResponseWriter, r *http.Request) {
	series, err := h.store.ListSeries(r.Context())
	if err != nil {
		h.writeErr(w, r, "list series", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, series)
}

func (h *Handler) getSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	d, err := h.store.GetSeries(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, "get series", seriesNotFound(err))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, d)
}

// setSeriesTotal stores a series' total of volumes; the key is required, and
// null clears the total.
func (h *Handler) setSeriesTotal(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	var body struct {
		TotalVolumes json.RawMessage `json:"total_volumes"`
	}
	if !httpx.DecodeJSON(w, r, h.logger, &body) {
		return
	}
	if body.TotalVolumes == nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("total_volumes", "is required: "+seriesNumberMsg+", or null"))
		return
	}
	var in SeriesTotalInput
	if json.Unmarshal(body.TotalVolumes, &in.TotalVolumes) != nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("total_volumes", seriesNumberMsg))
		return
	}
	if fe := checkSeriesNumber("total_volumes", in.TotalVolumes); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	d, err := h.store.SetSeriesTotal(r.Context(), id, in.TotalVolumes)
	if err != nil {
		h.writeErr(w, r, "set series total", seriesNotFound(err))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, d)
}

func seriesNotFound(err error) error {
	if errors.Is(err, ErrSeriesNotFound) {
		return ErrNotFound
	}
	return err
}
