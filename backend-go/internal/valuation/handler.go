package valuation

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// historyDays is how much of the value history the view returns.
const historyDays = 365

// Handler is the HTTP boundary for the valuation view and manual runs.
type Handler struct {
	store  *Store
	runner *Runner
	logger *slog.Logger
}

// NewHandler wires the store, runner and logger.
func NewHandler(store *Store, runner *Runner, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, runner: runner, logger: logger}
}

// Mount registers the routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/valuation", h.view)
	r.Post("/valuation/run", h.run)
}

// View is the GET /api/valuation body: every owned copy valued from the
// prices in the window now, and the stored daily totals.
type View struct {
	Status          Status         `json:"status"`
	WindowDays      int            `json:"window_days"`
	MinObservations int            `json:"min_observations"`
	Summary         Summary        `json:"summary"`
	Copies          []CopyValue    `json:"copies"`
	History         []HistoryPoint `json:"history"`
}

func (h *Handler) view(w http.ResponseWriter, r *http.Request) {
	window := h.runner.Window()
	copies, sum, err := h.store.Valued(r.Context(), window)
	if err != nil {
		httpx.WriteInternal(w, h.logger, "valuation", err)
		return
	}
	history, err := h.store.History(r.Context(), historyDays)
	if err != nil {
		httpx.WriteInternal(w, h.logger, "valuation history", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, View{
		Status:          h.runner.Status(),
		WindowDays:      int(window.Hours() / 24),
		MinObservations: MinObservations,
		Summary:         sum,
		Copies:          copies,
		History:         history,
	})
}

func (h *Handler) run(w http.ResponseWriter, _ *http.Request) {
	if !h.runner.Trigger() {
		httpx.WriteError(w, h.logger, http.StatusConflict, "a valuation is already running")
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusAccepted, h.runner.Status())
}
