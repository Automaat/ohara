package collection

import (
	"context"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// filmSeriesTimeout bounds the series lookup of a film being saved, as
// coverLookupTimeout bounds its cover.
const filmSeriesTimeout = 3 * time.Second

type filmSeries struct {
	finder  lookup.SeriesFinder
	timeout time.Duration
}

// WithFilmSeries makes a film created with a TMDB ID and without series
// fields join the series finder names for it.
func (h *Handler) WithFilmSeries(finder lookup.SeriesFinder) *Handler {
	h.filmSeries = &filmSeries{finder: finder, timeout: filmSeriesTimeout}
	return h
}

// wantsFilmSeries says whether a create may fill title's series: a film
// with a TMDB ID whose body named no series, not even null.
func (h *Handler) wantsFilmSeries(title *TitleInput) bool {
	return h.filmSeries != nil && title.Kind == KindMovie && title.TMDBID != nil && title.KeepSeries
}

// fillFilmSeries puts title in its film series, numbered and with the
// series' total, and reports whether it did. A failed or slow lookup, or a
// name the API would refuse, leaves the title without a series.
func (h *Handler) fillFilmSeries(ctx context.Context, title *TitleInput) bool {
	if title.TMDBID == nil {
		return false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, h.filmSeries.timeout)
	defer cancel()
	series, ok, err := h.filmSeries.finder.FilmSeries(lookupCtx, *title.TMDBID)
	if err != nil {
		h.logger.WarnContext(ctx, "film series lookup failed", "tmdb_id", *title.TMDBID, "err", err)
		return false
	}
	if !ok {
		return false
	}
	name := series.Name
	filled := *title
	filled.SeriesName = &name
	filled.SeriesNumber = nil
	if series.Number != nil && checkSeriesNumber("series_number", series.Number) == nil {
		n := *series.Number
		filled.SeriesNumber = &n
	}
	if total := series.Total; total > 0 && total <= MaxSeriesNumber {
		filled.SeriesTotal = &total
	}
	if fe := filled.checkSeries(); fe != nil {
		h.logger.WarnContext(ctx, "film series name refused", "tmdb_id", *title.TMDBID, "err", fe)
		return false
	}
	*title = filled
	return true
}
