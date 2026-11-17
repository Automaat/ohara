package lookup

import (
	"log/slog"
	"net/http"

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
}

func (h *Handler) lookup(w http.ResponseWriter, r *http.Request) {
	code, err := barcode.Normalize(chi.URLParam(r, "barcode"))
	if err != nil {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "barcode", Msg: err.Error()})
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, h.service.Lookup(r.Context(), code))
}
