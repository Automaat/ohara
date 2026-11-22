package collection

import "time"

// WithFilmSeriesTimeout shortens the film series lookup's timeout, so a
// test of a slow catalog does not wait the full three seconds.
func (h *Handler) WithFilmSeriesTimeout(d time.Duration) *Handler {
	h.filmSeries.timeout = d
	return h
}
