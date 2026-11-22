package collection

import (
	"context"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// filmSeriesTimeout bounds the film lookup of a title being saved, as
// coverLookupTimeout bounds its cover search.
const filmSeriesTimeout = 3 * time.Second

type filmSeries struct {
	finder  lookup.FilmFinder
	timeout time.Duration
}

// WithFilmSeries makes a film created with a TMDB ID and without series
// fields join the series finder names for it, taking its cover from the
// same lookup instead of a title search.
func (h *Handler) WithFilmSeries(finder lookup.FilmFinder) *Handler {
	h.filmSeries = &filmSeries{finder: finder, timeout: filmSeriesTimeout}
	return h
}

// wantsFilmSeries says whether a create may fill title's series: a film
// with a TMDB ID whose body named no series, not even null.
func (h *Handler) wantsFilmSeries(title *TitleInput) bool {
	return h.filmSeries != nil && title.Kind == KindMovie && title.TMDBID != nil && title.KeepSeries
}

// fillFilm looks the film up once: a title without a cover takes the
// film's poster, and the title joins its series, numbered and with the
// series' total. It reports whether the series was filled. A failed or
// slow lookup, or a name the API would refuse, leaves the title without a
// series, keeping a poster read before the failure.
func (h *Handler) fillFilm(ctx context.Context, title *TitleInput) bool {
	if title.TMDBID == nil {
		return false
	}
	lookupCtx, cancel := context.WithTimeout(ctx, h.filmSeries.timeout)
	defer cancel()
	film, err := h.filmSeries.finder.Film(lookupCtx, *title.TMDBID)
	if cover := film.CoverURL; emptyCover(title.CoverURL) && cover != "" && normalizeCoverURL(&cover) == nil {
		title.CoverURL = &cover
	}
	if err != nil {
		h.logger.WarnContext(ctx, "film series lookup failed", "tmdb_id", *title.TMDBID, "err", err)
		return false
	}
	if !film.InSeries {
		return false
	}
	series := film.Series
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
