package lookup

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// TextSearcher finds candidates of one kind by the title or artist a user
// types, for a barcode no provider knows. format is the edition format
// already picked ("dvd", "cd"…) or empty; a searcher uses it to narrow
// the search or to fill the candidate's edition.
type TextSearcher interface {
	Name() string
	SearchText(ctx context.Context, query, format string) ([]Candidate, error)
}

// maxQuery bounds the search text, in characters; maxSearchResults the
// candidates one search answers, and maxSeasonResults those of a TV search,
// which lists every season of the shows it finds.
const (
	maxQuery         = 200
	maxSearchResults = 10
	maxSeasonResults = 40
)

// SearchHandler serves GET /api/search?kind=&q=&barcode=&format=. The
// candidates carry the barcode being added, so picking one fills the new
// edition as a lookup candidate would. Searchers are keyed by kind
// ("movie", "book", "music", "tv"); a kind without one is switched off, for
// lack of an API key, and answers 503.
type SearchHandler struct {
	Searchers map[string]TextSearcher
	Logger    *slog.Logger
}

// SearchResult is the GET /api/search body.
type SearchResult struct {
	Candidates []Candidate `json:"candidates"`
}

// Mount registers the route under the /api router.
func (h *SearchHandler) Mount(r chi.Router) {
	r.Get("/search", h.search)
}

func (h *SearchHandler) search(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}
	q := r.URL.Query()
	kind := q.Get("kind")
	if kind != "movie" && kind != "book" && kind != "music" && kind != "tv" {
		httpx.WriteFieldError(w, logger, &httpx.FieldError{Field: "kind", Msg: "Typ to film, książka, muzyka albo serial."})
		return
	}
	text := strings.Join(strings.Fields(q.Get("q")), " ")
	if text == "" || utf8.RuneCountInString(text) > maxQuery {
		httpx.WriteFieldError(w, logger, &httpx.FieldError{Field: "q", Msg: "Wpisz tytuł, do 200 znaków."})
		return
	}
	code := ""
	if raw := q.Get("barcode"); raw != "" {
		norm, err := barcode.Normalize(raw)
		if err != nil {
			httpx.WriteFieldError(w, logger, &httpx.FieldError{Field: "barcode", Msg: err.Error()})
			return
		}
		code = norm
	}
	s, ok := h.Searchers[kind]
	if !ok {
		httpx.WriteError(w, logger, http.StatusServiceUnavailable, "Wyszukiwanie dla tego typu jest wyłączone: brak klucza API.")
		return
	}
	cands, err := s.SearchText(r.Context(), text, q.Get("format"))
	if err != nil {
		logger.WarnContext(r.Context(), "title search failed", "source", s.Name(), "err", err)
		httpx.WriteError(w, logger, http.StatusBadGateway, "Źródło nie odpowiada, spróbuj za chwilę.")
		return
	}
	limit := maxSearchResults
	if kind == "tv" {
		limit = maxSeasonResults
	}
	cands = cands[:min(len(cands), limit)]
	for i := range cands {
		cands[i].Type = kind
		cands[i].Edition.Barcode = code
		cands[i].Provider, cands[i].Sources = s.Name(), []string{s.Name()}
	}
	if cands == nil {
		cands = []Candidate{}
	}
	httpx.WriteJSON(w, logger, http.StatusOK, SearchResult{Candidates: cands})
}
