package scanqueue

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Handler serves /api/scan-queue.
type Handler struct {
	store  *Store
	logger *slog.Logger
}

// NewHandler wires the store and logger.
func NewHandler(store *Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, logger: logger}
}

// Mount registers the routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/scan-queue", h.list)
	r.Post("/scan-queue", h.add)
	r.Delete("/scan-queue/{id}", h.delete)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	entries, err := h.store.List(r.Context())
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "list scan queue", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, entries)
}

// AddInput is the POST /api/scan-queue body. Detail is a free-form note, such
// as the provider errors, cut to 500 characters.
type AddInput struct {
	Barcode string `json:"barcode"`
	Reason  string `json:"reason" ts:"QueueReason"`
	Detail  string `json:"detail"`
}

func (h *Handler) add(w http.ResponseWriter, r *http.Request) {
	var in AddInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return
	}
	code, err := barcode.Normalize(in.Barcode)
	if err != nil {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "barcode", Msg: err.Error()})
		return
	}
	if !slices.Contains(reasons, in.Reason) {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "reason", Msg: "unknown value"})
		return
	}
	entry, created, err := h.store.Add(r.Context(), code, in.Reason, truncate(strings.TrimSpace(in.Detail), maxDetail))
	switch {
	case errors.Is(err, ErrInCollection):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "barcode"})
	case err != nil:
		httpx.WriteInternal(w, r, h.logger, "add to scan queue", err)
	case created:
		httpx.WriteJSON(w, h.logger, http.StatusCreated, entry)
	default:
		httpx.WriteJSON(w, h.logger, http.StatusOK, entry)
	}
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	err := h.store.Delete(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, h.logger, http.StatusNotFound, "not found")
	case err != nil:
		httpx.WriteInternal(w, r, h.logger, "delete scan queue entry", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// truncate keeps at most limit runes of s.
func truncate(s string, limit int) string {
	runes := []rune(s)
	if len(runes) <= limit {
		return s
	}
	return string(runes[:limit])
}
