package lookup

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Handler serves GET /api/lookup/{barcode}.
type Handler struct {
	service *Service
	logger  *slog.Logger
}

// NewHandler wires the service.
func NewHandler(service *Service, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{service: service, logger: logger}
}

// Mount registers the route under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/lookup/{barcode}", h.lookup)
	r.Put("/lookup/{barcode}/selection", h.selectCandidate)
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) {
	code, err := barcode.Normalize(chi.URLParam(r, "barcode"))
	if err != nil {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "barcode", Msg: err.Error()})
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, h.service.Lookup(r.Context(), code))
}

// selectCandidate stores the candidate the user picked, so the next scan of
// the barcode needs no confirm step. The body is a Candidate; its barcode,
// provider and confidence are set by the server.
func (h *Handler) selectCandidate(w http.ResponseWriter, r *http.Request) {
	code, err := barcode.Normalize(chi.URLParam(r, "barcode"))
	if err != nil {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "barcode", Msg: err.Error()})
		return
	}
	var c Candidate
	if !httpx.DecodeJSON(w, r, h.logger, &c) {
		return
	}
	if fe := validateSelection(code, &c); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	sel, err := h.service.Select(r.Context(), code, c)
	if err != nil {
		httpx.WriteInternal(w, h.logger, "store lookup selection", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, sel)
}

func validateSelection(code string, c *Candidate) *httpx.FieldError {
	c.Title.Name = strings.TrimSpace(c.Title.Name)
	switch {
	case c.Type != "movie" && c.Type != "book" && c.Type != "music":
		return &httpx.FieldError{Field: "type", Msg: "must be movie, book or music"}
	case c.Title.Name == "":
		return &httpx.FieldError{Field: "title.name", Msg: "is required"}
	case c.Edition.Barcode != "":
		got, err := barcode.Normalize(c.Edition.Barcode)
		if err != nil || got != code {
			return &httpx.FieldError{Field: "edition.barcode", Msg: "must match the path barcode"}
		}
	}
	return nil
}
