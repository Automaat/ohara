package wantlist

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Handler is the HTTP boundary of the wantlist sync.
type Handler struct {
	syncer *Syncer
	logger *slog.Logger
}

// NewHandler wires the syncer and logger.
func NewHandler(syncer *Syncer, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{syncer: syncer, logger: logger}
}

// Mount registers the routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/discogs/wantlist", h.status)
	r.Post("/discogs/wantlist/sync", h.sync)
}

func (h *Handler) status(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteJSON(w, h.logger, http.StatusOK, h.syncer.Status())
}

// sync starts a sync in the background: 202 with the status, 503 without
// a Discogs token and 409 while one runs.
func (h *Handler) sync(w http.ResponseWriter, _ *http.Request) {
	if !h.syncer.Enabled() {
		httpx.WriteError(w, h.logger, http.StatusServiceUnavailable,
			"Synchronizacja z Discogs jest wyłączona: brak tokenu Discogs.")
		return
	}
	if !h.syncer.Trigger() {
		httpx.WriteError(w, h.logger, http.StatusConflict, "a Discogs wantlist sync is already running")
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusAccepted, h.syncer.Status())
}
