package scanqueue

import (
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

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
		httpx.WriteInternal(w, h.logger, "list scan queue", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, entries)
}

// AddInput is the POST /api/scan-queue body. Detail is a free-form note, such
// as the provider errors, cut to 500 characters.
type AddInput struct {
	Barcode string `json:"barcode"`
	Reason  string `json:"reason"`
	Detail  string `json:"detail"`
	// Restore puts back an entry with its history instead of recording a
	// new scan: the scanner sends it when it undoes an add whose edition
	// took the barcode out of the queue.
	Restore *RestoreInput `json:"restore,omitempty"`
}

// RestoreInput is the history of an entry being put back.
type RestoreInput struct {
	Scans         int       `json:"scans"`
	CreatedAt     time.Time `json:"created_at"`
	LastScannedAt time.Time `json:"last_scanned_at"`
}

// history checks a restored entry's history: at least one scan, the first
// scan no later than the last, and neither in the future.
func (in *RestoreInput) history(now time.Time) (History, *httpx.FieldError) {
	switch {
	case in.Scans < 1:
		return History{}, &httpx.FieldError{Field: "restore.scans", Msg: "must be at least 1"}
	case in.CreatedAt.IsZero() || in.CreatedAt.After(in.LastScannedAt):
		return History{}, &httpx.FieldError{Field: "restore.created_at", Msg: "must be set and not after last_scanned_at"}
	case in.LastScannedAt.After(now):
		return History{}, &httpx.FieldError{Field: "restore.last_scanned_at", Msg: "must not be in the future"}
	}
	return History{Scans: in.Scans, CreatedAt: in.CreatedAt, LastScannedAt: in.LastScannedAt}, nil
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
	detail := truncate(strings.TrimSpace(in.Detail), maxDetail)
	var entry Entry
	var created bool
	if in.Restore != nil {
		history, fe := in.Restore.history(time.Now())
		if fe != nil {
			httpx.WriteFieldError(w, h.logger, fe)
			return
		}
		entry, created, err = h.store.Restore(r.Context(), code, in.Reason, detail, history)
	} else {
		entry, created, err = h.store.Add(r.Context(), code, in.Reason, detail)
	}
	switch {
	case errors.Is(err, ErrInCollection):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "barcode"})
	case err != nil:
		httpx.WriteInternal(w, h.logger, "add to scan queue", err)
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
		httpx.WriteInternal(w, h.logger, "delete scan queue entry", err)
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
