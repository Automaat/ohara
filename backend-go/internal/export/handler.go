// Package export serves the whole collection as a file: a CSV that opens as
// columns in Polish Excel, or JSON nested as titles, editions and copies.
package export

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/collection"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/valuation"
)

// Formats GET /api/export answers in.
const (
	FormatCSV  = "csv"
	FormatJSON = "json"
)

// Copy is an exported copy: the copy as the API sends it and, for an owned
// one, the value bounds GET /api/valuation shows for it now; null without
// prices.
type Copy struct {
	collection.Copy
	Floor   *valuation.Estimate `json:"floor"`
	Mid     *valuation.Estimate `json:"mid"`
	Ceiling *valuation.Estimate `json:"ceiling"`
}

// Edition is an exported edition with its copies.
type Edition struct {
	collection.Edition
	Copies []Copy `json:"copies"`
}

// Title is an exported title with its editions.
type Title struct {
	collection.Title
	Editions []Edition `json:"editions"`
}

// Handler is the HTTP boundary of the export.
type Handler struct {
	collection *collection.Store
	values     *valuation.Store
	window     time.Duration
	logger     *slog.Logger
}

// NewHandler reads the collection from titles and the value bounds from
// values, over the valuation's price window.
func NewHandler(titles *collection.Store, values *valuation.Store, window time.Duration, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{collection: titles, values: values, window: window, logger: logger}
}

// Mount registers the routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/export", h.export)
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format != FormatCSV && format != FormatJSON {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "format", Msg: "must be one of: csv, json"})
		return
	}
	titles, day, err := h.read(r.Context())
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "export", err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="ohara-`+day+`.`+format+`"`)
	if format == FormatJSON {
		httpx.WriteJSON(w, h.logger, http.StatusOK, titles)
		return
	}
	var body bytes.Buffer
	if err := writeCSV(&body, titles); err != nil {
		httpx.WriteInternal(w, r, h.logger, "export csv", err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(body.Bytes()); err != nil {
		h.logger.ErrorContext(r.Context(), "write export", "err", err)
	}
}

// read joins the collection with the value bounds of its owned copies.
func (h *Handler) read(ctx context.Context) ([]Title, string, error) {
	details, day, err := h.collection.Export(ctx)
	if err != nil {
		return nil, "", err
	}
	valued, _, err := h.values.Valued(ctx, h.window)
	if err != nil {
		return nil, "", err
	}
	values := make(map[int64]*valuation.CopyValue, len(valued))
	for i := range valued {
		values[valued[i].CopyID] = &valued[i]
	}
	titles := make([]Title, 0, len(details))
	for i := range details {
		d := &details[i]
		t := Title{Title: d.Title, Editions: make([]Edition, 0, len(d.Editions))}
		for j := range d.Editions {
			e := &d.Editions[j]
			edition := Edition{Edition: e.Edition, Copies: make([]Copy, 0, len(e.Copies))}
			for k := range e.Copies {
				c := Copy{Copy: e.Copies[k]}
				if v := values[c.ID]; v != nil && c.Status == collection.StatusOwned {
					c.Floor, c.Mid, c.Ceiling = v.Floor, v.Mid, v.Ceiling
				}
				edition.Copies = append(edition.Copies, c)
			}
			t.Editions = append(t.Editions, edition)
		}
		titles = append(titles, t)
	}
	return titles, day, nil
}
