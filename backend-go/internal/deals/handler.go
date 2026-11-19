package deals

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Keyword kinds.
const (
	KeywordReject = "reject"
	KeywordAccept = "accept"
)

const (
	maxPhrase     = 100
	defaultRecent = 100
	maxRecent     = 500
)

// KeywordInput is the body of POST /api/deals/keywords.
type KeywordInput struct {
	Kind   string `json:"kind"`
	Phrase string `json:"phrase"`
}

// Normalize trims the phrase, collapses its inner spaces and checks the
// input.
func (in *KeywordInput) Normalize() *httpx.FieldError {
	in.Kind = strings.TrimSpace(in.Kind)
	in.Phrase = strings.Join(strings.Fields(in.Phrase), " ")
	if in.Kind != KeywordReject && in.Kind != KeywordAccept {
		return &httpx.FieldError{Field: "kind", Msg: "must be one of: reject, accept"}
	}
	if len(words(in.Phrase)) == 0 {
		return &httpx.FieldError{Field: "phrase", Msg: "must contain a letter or digit"}
	}
	if len(in.Phrase) > maxPhrase {
		return &httpx.FieldError{Field: "phrase", Msg: "is too long"}
	}
	return nil
}

// Handler is the HTTP boundary for the deals view, keywords and manual runs.
type Handler struct {
	store  *Store
	hunter *Hunter
	logger *slog.Logger
}

// NewHandler wires the store, hunter and logger.
func NewHandler(store *Store, hunter *Hunter, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, hunter: hunter, logger: logger}
}

// Mount registers the routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/deals", h.list)
	r.Post("/deals/run", h.run)
	r.Get("/deals/keywords", h.listKeywords)
	r.Post("/deals/keywords", h.addKeyword)
	r.Delete("/deals/keywords/{id}", h.deleteKeyword)
}

// View is the GET /api/deals body. Wishes counts the copies on the wishlist.
type View struct {
	Status       Status        `json:"status"`
	Wishes       int           `json:"wishes"`
	Observations []Observation `json:"observations"`
}

// list reads the hunter status before the listings, so a run that ends mid-request still reads
// as running and the page asks again for the listings it stored.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	filter, fe := recentFilter(r)
	if fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	status := h.hunter.Status()
	obs, err := h.store.Recent(r.Context(), filter)
	if err != nil {
		httpx.WriteInternal(w, h.logger, "recent deals", err)
		return
	}
	wishes, err := h.store.WishCount(r.Context())
	if err != nil {
		httpx.WriteInternal(w, h.logger, "wish count", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, View{Status: status, Wishes: wishes, Observations: obs})
}

func recentFilter(r *http.Request) (RecentFilter, *httpx.FieldError) {
	q := r.URL.Query()
	f := RecentFilter{Limit: defaultRecent}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > maxRecent {
			return f, &httpx.FieldError{Field: "limit", Msg: "must be between 1 and 500"}
		}
		f.Limit = n
	}
	for _, flag := range []struct {
		name string
		dst  *bool
	}{{"matched", &f.Matched}, {"under_limit", &f.UnderLimit}} {
		raw := q.Get(flag.name)
		if raw == "" {
			continue
		}
		v, err := strconv.ParseBool(raw)
		if err != nil {
			return f, &httpx.FieldError{Field: flag.name, Msg: "must be true or false"}
		}
		*flag.dst = v
	}
	return f, nil
}

func (h *Handler) run(w http.ResponseWriter, _ *http.Request) {
	if !h.hunter.Trigger() {
		httpx.WriteError(w, h.logger, http.StatusConflict, "a deal search is already running")
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusAccepted, h.hunter.Status())
}

func (h *Handler) listKeywords(w http.ResponseWriter, r *http.Request) {
	keywords, err := h.store.Keywords(r.Context())
	if err != nil {
		httpx.WriteInternal(w, h.logger, "list keywords", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, keywords)
}

func (h *Handler) addKeyword(w http.ResponseWriter, r *http.Request) {
	var in KeywordInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return
	}
	if fe := in.Normalize(); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	k, err := h.store.AddKeyword(r.Context(), &in)
	switch {
	case errors.Is(err, ErrDuplicateKeyword):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "phrase"})
	case err != nil:
		httpx.WriteInternal(w, h.logger, "add keyword", err)
	default:
		httpx.WriteJSON(w, h.logger, http.StatusCreated, k)
	}
}

func (h *Handler) deleteKeyword(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	err := h.store.DeleteKeyword(r.Context(), id)
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, h.logger, http.StatusNotFound, "not found")
	case err != nil:
		httpx.WriteInternal(w, h.logger, "delete keyword", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}
