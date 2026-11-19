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
// prices in the window now, the stored daily totals of the last year and
// the day of the newest one, null before the first valuation.
type View struct {
	Status          Status         `json:"status"`
	WindowDays      int            `json:"window_days"`
	MinObservations int            `json:"min_observations"`
	Summary         Summary        `json:"summary"`
	Copies          []CopyValue    `json:"copies"`
	History         []HistoryPoint `json:"history"`
	LastValuation   *string        `json:"last_valuation"`
}

func (h *Handler) view(w http.ResponseWriter, r *http.Request) {
	// The status comes first: a run that ends while the rows below are read
	// then still shows as running, so the page asks again for its result.
	status := h.runner.Status()
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
	latest, ok, err := h.store.Latest(r.Context())
	if err != nil {
		httpx.WriteInternal(w, h.logger, "valuation latest", err)
		return
	}
	var last *string
	if ok {
		last = &latest.Day
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, View{
		Status:          status,
		WindowDays:      int(window.Hours() / 24),
		MinObservations: MinObservations,
		Summary:         sum,
		Copies:          copies,
		History:         history,
		LastValuation:   last,
	})
}

func (h *Handler) run(w http.ResponseWriter, _ *http.Request) {
	if !h.runner.Trigger() {
		httpx.WriteError(w, h.logger, http.StatusConflict, "a valuation is already running")
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusAccepted, h.runner.Status())
}
