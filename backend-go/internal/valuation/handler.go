package valuation

import (
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
	r.Get("/valuation/copies", h.titleCopies)
	r.Get("/valuation/copies/history", h.copyHistory)
	r.Get("/valuation/market", h.market)
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
		httpx.WriteInternal(w, r, h.logger, "valuation", err)
		return
	}
	history, err := h.store.History(r.Context(), historyDays)
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "valuation history", err)
		return
	}
	latest, ok, err := h.store.Latest(r.Context())
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "valuation latest", err)
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

// TitleCopies is the GET /api/valuation/copies?title_id= body: the
// title's owned copies valued as the full view values them, without the
// rest of the collection or its history.
type TitleCopies struct {
	Copies []CopyValue `json:"copies"`
}

func (h *Handler) titleCopies(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("title_id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "title_id", Msg: "must be a positive integer"})
		return
	}
	copies, err := h.store.TitleValued(r.Context(), h.runner.Window(), id)
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "title valuation", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, TitleCopies{Copies: copies})
}

// copyHistoryDays is how much of each copy's value history a title page
// gets.
const copyHistoryDays = 90

// CopyHistory is the GET /api/valuation/copies/history?title_id= body:
// the stored daily values of the title's copies, owned or not, over the
// last Days days, by copy and oldest first.
type CopyHistory struct {
	Days    int       `json:"days"`
	History []CopyDay `json:"history"`
}

func (h *Handler) copyHistory(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("title_id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "title_id", Msg: "must be a positive integer"})
		return
	}
	history, err := h.store.CopyHistory(r.Context(), id, copyHistoryDays)
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "copy value history", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, CopyHistory{Days: copyHistoryDays, History: history})
}

// MarketView is the GET /api/valuation/market body: the used price of
// every wished title and format, or with ?title_id= of every format of
// that title, from the prices of the last WindowDays days.
type MarketView struct {
	WindowDays int      `json:"window_days"`
	Markets    []Market `json:"markets"`
}

func (h *Handler) market(w http.ResponseWriter, r *http.Request) {
	id, ok := marketTitle(r.URL.RawQuery)
	if !ok {
		httpx.WriteFieldError(w, h.logger, &httpx.FieldError{Field: "title_id", Msg: "must be a positive integer"})
		return
	}
	window := h.runner.Window()
	markets, err := h.store.Markets(r.Context(), window, id)
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "market prices", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, MarketView{
		WindowDays: int(window.Hours() / 24),
		Markets:    markets,
	})
}

// marketTitle reads the market route's optional title_id: 0 without one,
// the title when it is given once as a positive integer. A blank,
// malformed or repeated title_id, or a query that does not parse where it
// names title_id, is refused rather than read as no filter.
func marketTitle(rawQuery string) (int64, bool) {
	q, err := url.ParseQuery(rawQuery)
	if err != nil && strings.Contains(rawQuery, "title_id") {
		return 0, false
	}
	values, given := q["title_id"]
	if !given {
		return 0, true
	}
	if len(values) != 1 {
		return 0, false
	}
	id, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func (h *Handler) run(w http.ResponseWriter, _ *http.Request) {
	if !h.runner.Trigger() {
		httpx.WriteError(w, h.logger, http.StatusConflict, "a valuation is already running")
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusAccepted, h.runner.Status())
}
