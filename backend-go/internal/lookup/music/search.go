package music

import (
	"log/slog"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/lookup"
)

// searchProvider names the source of search candidates.
const searchProvider = "discogs"

// maxQuery bounds the search text, in characters.
const maxQuery = 200

// searchFormats maps an edition format to the Discogs format filter.
var searchFormats = map[string]string{"": "", "cd": "CD", "vinyl": "Vinyl", "cassette": "Cassette"}

// SearchHandler serves GET /api/music/search: a Discogs text search for an
// album typed by hand, answered as lookup candidates for the barcode being
// added, so picking one fills the edition from Discogs.
type SearchHandler struct {
	Discogs DiscogsSearcher
	Logger  *slog.Logger
}

// SearchResult is the GET /api/music/search body.
type SearchResult struct {
	Candidates []lookup.Candidate `json:"candidates"`
}

// Mount registers the route under the /api router.
func (h *SearchHandler) Mount(r chi.Router) {
	r.Get("/music/search", h.search)
}

func (h *SearchHandler) search(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	if logger == nil {
		logger = slog.Default()
	}
	q := r.URL.Query()
	text := strings.Join(strings.Fields(q.Get("q")), " ")
	if text == "" || utf8.RuneCountInString(text) > maxQuery {
		httpx.WriteFieldError(w, logger, &httpx.FieldError{Field: "q", Msg: "Wpisz wykonawcę lub tytuł płyty, do 200 znaków."})
		return
	}
	format, ok := searchFormats[q.Get("format")]
	if !ok {
		httpx.WriteFieldError(w, logger, &httpx.FieldError{Field: "format", Msg: "Nieznany nośnik."})
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
	if h.Discogs == nil {
		httpx.WriteError(w, logger, http.StatusServiceUnavailable, "Wyszukiwanie w Discogs jest wyłączone: brak tokenu.")
		return
	}
	releases, err := h.Discogs.SearchText(r.Context(), text, format)
	if err != nil {
		logger.WarnContext(r.Context(), "discogs search failed", "err", err)
		httpx.WriteError(w, logger, http.StatusBadGateway, "Discogs nie odpowiada, spróbuj za chwilę.")
		return
	}
	cands := make([]lookup.Candidate, 0, len(releases))
	for i := range releases {
		c := discogsCandidate(code, &releases[i])
		c.Provider, c.Sources = searchProvider, []string{searchProvider}
		cands = append(cands, c)
	}
	httpx.WriteJSON(w, logger, http.StatusOK, SearchResult{Candidates: cands})
}
