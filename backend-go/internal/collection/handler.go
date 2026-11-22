package collection

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/barcode"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Handler is the HTTP boundary for titles, editions, copies and the
// collection view.
type Handler struct {
	store  *Store
	logger *slog.Logger
	covers *catalogCovers
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
	r.Get("/collection", h.collection)

	r.Get("/titles", h.listTitles)
	r.Post("/titles", h.createTitle)
	r.Get("/titles/{id}", h.getTitle)
	r.Put("/titles/{id}", h.updateTitle)
	r.Put("/titles/{id}/finished", h.setFinished)
	r.Put("/titles/{id}/rating", h.rateTitle)
	r.Delete("/titles/{id}", h.deleteTitle)

	r.Get("/tags", h.listTags)

	r.Get("/series", h.listSeries)
	r.Get("/series/{id}", h.getSeries)
	r.Put("/series/{id}", h.setSeriesTotal)

	r.Get("/editions", h.listEditions)
	r.Post("/editions", h.createEdition)
	r.Get("/editions/{id}", h.getEdition)
	r.Put("/editions/{id}", h.updateEdition)
	r.Delete("/editions/{id}", h.deleteEdition)
	r.Get("/editions/{id}/cover", h.getEditionCover)
	r.Put("/editions/{id}/cover", h.putEditionCover)
	r.Delete("/editions/{id}/cover", h.deleteEditionCover)

	r.Post("/copies", h.createCopy)
	r.Get("/copies/locations", h.locations)
	r.Get("/copies/{id}", h.getCopy)
	r.Put("/copies/{id}", h.updateCopy)
	r.Delete("/copies/{id}", h.deleteCopy)
	r.Post("/copies/{id}/loans", h.lendCopy)
	r.Post("/copies/{id}/receipt", h.receiveCopy)
	r.Put("/loans/{id}", h.returnLoan)

	r.Delete("/requests/{key}", h.discardRequest)
}

// writeErr maps store errors to responses. A missing parent named in the
// body is a 422 on that field; a missing resource in the path is a 404.
func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, op string, err error) {
	var fe *httpx.FieldError
	switch {
	case errors.As(err, &fe):
		httpx.WriteFieldError(w, h.logger, fe)
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, h.logger, http.StatusNotFound, "not found")
	case errors.Is(err, ErrDuplicateBarcode):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "barcode"})
	case errors.Is(err, ErrDuplicateTMDB):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "tmdb_id"})
	case errors.Is(err, ErrRequestDiscarded):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "request_key"})
	case errors.Is(err, ErrDuplicateSeriesNumber):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "series_number"})
	case errors.Is(err, ErrKindLocked):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "kind"})
	case errors.Is(err, ErrNotOrdered):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "status"})
	case errors.Is(err, ErrLentCopyStays):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "status"})
	case errors.Is(err, ErrHasEditions), errors.Is(err, ErrHasCopies), errors.Is(err, ErrNotOwned),
		errors.Is(err, ErrAlreadyLent), errors.Is(err, ErrReturned), errors.Is(err, ErrInBoxSet):
		httpx.WriteError(w, h.logger, http.StatusConflict, err.Error())
	default:
		httpx.WriteInternal(w, r, h.logger, op, err)
	}
}

// parentErr treats a missing parent referenced by the body as a 422 on
// field, and a missing resource from the path as a 404.
func parentErr(err, parent error, field string) error {
	if errors.Is(err, parent) {
		return fieldErr(field, "does not exist")
	}
	if errors.Is(err, ErrTitleNotFound) || errors.Is(err, ErrEditionNotFound) {
		return ErrNotFound
	}
	return err
}

func notFound(err error) error {
	if errors.Is(err, ErrTitleNotFound) || errors.Is(err, ErrEditionNotFound) {
		return ErrNotFound
	}
	return err
}

// Totals sums the copies in a collection view. Copies counts sold ones too;
// owned and priced count owned copies only, ordered the copies on their way
// (left out when none) and spent what owned and ordered copies cost.
type Totals struct {
	Copies     int   `json:"copies"`
	Owned      int   `json:"owned"`
	Ordered    int   `json:"ordered,omitempty"`
	Wishlist   int   `json:"wishlist"`
	Priced     int   `json:"priced"`
	SpentCents int64 `json:"spent_cents"`
}

// CollectionView is the GET /api/collection body.
type CollectionView struct {
	Items  []Item `json:"items"`
	Totals Totals `json:"totals"`
}

func totals(items []Item) Totals {
	var t Totals
	for i := range items {
		it := &items[i]
		t.Copies++
		switch it.Status {
		case StatusWishlist:
			t.Wishlist++
			continue
		case StatusSold:
			continue
		case StatusOrdered:
			t.Ordered++
			if it.PriceCents != nil {
				t.SpentCents += *it.PriceCents
			}
			continue
		}
		t.Owned++
		if it.PriceCents != nil {
			t.Priced++
			t.SpentCents += *it.PriceCents
		}
	}
	return t
}

func queryEnum(r *http.Request, name string, allowed []string) (string, *httpx.FieldError) {
	v := r.URL.Query().Get(name)
	if v != "" && !slices.Contains(allowed, v) {
		return "", fieldErr(name, "unknown value")
	}
	return v, nil
}

func (h *Handler) collection(w http.ResponseWriter, r *http.Request) {
	var f Filter
	var fe *httpx.FieldError
	if f.Kind, fe = queryEnum(r, "kind", kinds); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	if f.Format, fe = queryEnum(r, "format", formats); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	if f.Status, fe = queryEnum(r, "status", statuses); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	loaned, fe := queryEnum(r, "loaned", []string{"1"})
	if fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	f.Loaned = loaned == "1"
	unfinished, fe := queryEnum(r, "unfinished", []string{"1"})
	if fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	f.Unfinished = unfinished == "1"
	f.Location = r.URL.Query().Get("location")
	f.Query = r.URL.Query().Get("q")
	f.Typed = f.Query
	f.Tag = r.URL.Query().Get("tag")
	if code, err := barcode.Normalize(f.Query); err == nil {
		f.Query = code
	}
	items, err := h.store.Collection(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, "collection", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, CollectionView{Items: items, Totals: totals(items)})
}

func (h *Handler) listTitles(w http.ResponseWriter, r *http.Request) {
	kind, fe := queryEnum(r, "kind", kinds)
	if fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	f := TitleFilter{Kind: kind, Query: r.URL.Query().Get("q"), Tag: r.URL.Query().Get("tag")}
	if raw := r.URL.Query().Get("tmdb_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpx.WriteFieldError(w, h.logger, fieldErr("tmdb_id", "must be a positive integer"))
			return
		}
		f.TMDBID = id
	}
	titles, err := h.store.ListTitles(r.Context(), f)
	if err != nil {
		h.writeErr(w, r, "list titles", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, titles)
}

// listTags answers the tags titles carry, with how many titles carry each.
func (h *Handler) listTags(w http.ResponseWriter, r *http.Request) {
	tags, err := h.store.ListTags(r.Context())
	if err != nil {
		h.writeErr(w, r, "list tags", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, tags)
}

// EditionWithCopies is an edition in a title's detail view.
type EditionWithCopies struct {
	Edition
	Copies []Copy `json:"copies"`
}

// TitleDetail is the GET /api/titles/{id} body. BoxSets are the box set
// editions that hold the title, absent when none does.
type TitleDetail struct {
	Title
	Editions []EditionWithCopies `json:"editions"`
	BoxSets  []BoxSet            `json:"box_sets,omitempty"`
}

func (h *Handler) titleDetail(ctx context.Context, id int64) (TitleDetail, error) {
	t, err := h.store.GetTitle(ctx, id)
	if err != nil {
		return TitleDetail{}, notFound(err)
	}
	editions, err := h.store.ListEditions(ctx, id, "")
	if err != nil {
		return TitleDetail{}, err
	}
	boxSets, err := h.store.BoxSets(ctx, id)
	if err != nil {
		return TitleDetail{}, err
	}
	out := TitleDetail{Title: t, Editions: make([]EditionWithCopies, 0, len(editions)), BoxSets: boxSets}
	for i := range editions {
		copies, err := h.store.ListCopies(ctx, editions[i].ID)
		if err != nil {
			return TitleDetail{}, err
		}
		out.Editions = append(out.Editions, EditionWithCopies{Edition: editions[i], Copies: copies})
	}
	return out, nil
}

func (h *Handler) getTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	detail, err := h.titleDetail(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, "get title", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, detail)
}

// titleBody reads a title's rating and series as they were sent, since a
// body without them keeps the stored ones and a null clears them.
type titleBody struct {
	TitleInput
	Rating       json.RawMessage `json:"rating"`
	SeriesName   json.RawMessage `json:"series_name"`
	SeriesNumber json.RawMessage `json:"series_number"`
}

func (h *Handler) decodeTitle(w http.ResponseWriter, r *http.Request) (*TitleInput, bool) {
	var body titleBody
	if !httpx.DecodeJSONFields(w, r, h.logger, &body, "tags") {
		return nil, false
	}
	in := body.TitleInput
	in.KeepRating = body.Rating == nil
	if !in.KeepRating && json.Unmarshal(body.Rating, &in.Rating) != nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("rating", ratingMsg))
		return nil, false
	}
	in.KeepSeries = body.SeriesName == nil && body.SeriesNumber == nil
	if body.SeriesName != nil && json.Unmarshal(body.SeriesName, &in.SeriesName) != nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("series_name", "must be text or null"))
		return nil, false
	}
	if body.SeriesNumber != nil && json.Unmarshal(body.SeriesNumber, &in.SeriesNumber) != nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("series_number", seriesNumberMsg))
		return nil, false
	}
	if fe := in.Normalize(); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return nil, false
	}
	return &in, true
}

// createdStatus answers a create: 201 for a new row, 200 for the row an
// earlier request with the same request key created.
func createdStatus(created bool) int {
	if created {
		return http.StatusCreated
	}
	return http.StatusOK
}

func (h *Handler) createTitle(w http.ResponseWriter, r *http.Request) {
	in, ok := h.decodeTitle(w, r)
	if !ok {
		return
	}
	if h.covers != nil && emptyCover(in.CoverURL) {
		in.CoverURL = h.titleCover(r.Context(), in)
	}
	t, created, err := h.store.CreateTitle(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, "create title", err)
		return
	}
	httpx.WriteJSON(w, h.logger, createdStatus(created), t)
}

func (h *Handler) updateTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	in, ok := h.decodeTitle(w, r)
	if !ok {
		return
	}
	t, err := h.store.UpdateTitle(r.Context(), id, in)
	if err != nil {
		h.writeErr(w, r, "update title", notFound(err))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, t)
}

// setFinished serves PUT /api/titles/{id}/finished: the title's finished
// state and day alone, so a page holding an older copy of the title
// never writes its other fields back.
func (h *Handler) setFinished(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	var in FinishInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return
	}
	if fe := in.Normalize(); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	t, err := h.store.SetFinished(r.Context(), id, &in)
	if err != nil {
		h.writeErr(w, r, "set finished", notFound(err))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, t)
}

// rateTitle sets only the rating, so a page that loaded the title earlier
// never writes back the rest of it over another edit.
func (h *Handler) rateTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	var body struct {
		Rating json.RawMessage `json:"rating"`
	}
	if !httpx.DecodeJSON(w, r, h.logger, &body) {
		return
	}
	var in RatingInput
	if body.Rating == nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("rating", "is required: "+ratingMsg+", or null"))
		return
	}
	if json.Unmarshal(body.Rating, &in.Rating) != nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("rating", ratingMsg))
		return
	}
	if fe := checkRating(in.Rating); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	t, err := h.store.RateTitle(r.Context(), id, in.Rating)
	if err != nil {
		h.writeErr(w, r, "rate title", notFound(err))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, t)
}

func (h *Handler) deleteTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	if err := h.store.DeleteTitle(r.Context(), id); err != nil {
		h.writeErr(w, r, "delete title", notFound(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listEditions(w http.ResponseWriter, r *http.Request) {
	var titleID int64
	if raw := r.URL.Query().Get("title_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpx.WriteFieldError(w, h.logger, fieldErr("title_id", "must be a positive integer"))
			return
		}
		titleID = id
	}
	var code string
	if raw := r.URL.Query().Get("barcode"); raw != "" {
		normalized, err := barcode.Normalize(raw)
		if err != nil {
			httpx.WriteFieldError(w, h.logger, fieldErr("barcode", err.Error()))
			return
		}
		code = normalized
	}
	editions, err := h.store.ListEditions(r.Context(), titleID, code)
	if err != nil {
		h.writeErr(w, r, "list editions", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, editions)
}

// EditionDetail is the GET /api/editions/{id} body.
type EditionDetail struct {
	Edition
	Title  Title  `json:"title"`
	Copies []Copy `json:"copies"`
}

func (h *Handler) getEdition(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	e, err := h.store.GetEdition(ctx, id)
	if err != nil {
		h.writeErr(w, r, "get edition", notFound(err))
		return
	}
	t, err := h.store.GetTitle(ctx, e.TitleID)
	if err != nil {
		h.writeErr(w, r, "get edition title", err)
		return
	}
	copies, err := h.store.ListCopies(ctx, id)
	if err != nil {
		h.writeErr(w, r, "get edition copies", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, EditionDetail{Edition: e, Title: t, Copies: copies})
}

func (h *Handler) decodeEdition(w http.ResponseWriter, r *http.Request) (*EditionInput, bool) {
	var in EditionInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return nil, false
	}
	if fe := in.Normalize(); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return nil, false
	}
	return &in, true
}

func (h *Handler) createEdition(w http.ResponseWriter, r *http.Request) {
	in, ok := h.decodeEdition(w, r)
	if !ok {
		return
	}
	e, created, err := h.store.CreateEdition(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, "create edition", parentErr(err, ErrTitleNotFound, "title_id"))
		return
	}
	httpx.WriteJSON(w, h.logger, createdStatus(created), e)
}

func (h *Handler) updateEdition(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	in, ok := h.decodeEdition(w, r)
	if !ok {
		return
	}
	e, err := h.store.UpdateEdition(r.Context(), id, in)
	if err != nil {
		h.writeErr(w, r, "update edition", parentErr(err, ErrTitleNotFound, "title_id"))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, e)
}

func (h *Handler) deleteEdition(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	requeue := r.URL.Query().Get("requeue") == "1"
	if err := h.store.DeleteEdition(r.Context(), id, requeue); err != nil {
		h.writeErr(w, r, "delete edition", notFound(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CopyDetail is the GET /api/copies/{id} body. Loans are newest first,
// returned ones included.
type CopyDetail struct {
	Copy
	Edition Edition `json:"edition"`
	Title   Title   `json:"title"`
	Loans   []Loan  `json:"loans"`
}

func (h *Handler) getCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := h.store.GetCopy(ctx, id)
	if err != nil {
		h.writeErr(w, r, "get copy", err)
		return
	}
	e, err := h.store.GetEdition(ctx, c.EditionID)
	if err != nil {
		h.writeErr(w, r, "get copy edition", err)
		return
	}
	t, err := h.store.GetTitle(ctx, e.TitleID)
	if err != nil {
		h.writeErr(w, r, "get copy title", err)
		return
	}
	loans, err := h.store.ListLoans(ctx, id)
	if err != nil {
		h.writeErr(w, r, "get copy loans", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, CopyDetail{Copy: c, Edition: e, Title: t, Loans: loans})
}

// copyBody reads a copy's condition as it was sent, since an update
// without one keeps the stored condition and a null clears it.
type copyBody struct {
	CopyInput
	Condition sentCondition `json:"condition"`
}

// sentCondition is a condition as a body carries it: whether the key was
// there and its value, null or a string. Each occurrence of the key is
// checked as it is read, so a repeated key cannot hide a wrong type.
type sentCondition struct {
	sent  bool
	value *string
}

// UnmarshalJSON takes null or a string; anything else is a type error
// naming the field.
func (c *sentCondition) UnmarshalJSON(b []byte) error {
	c.sent = true
	if string(b) == "null" {
		c.value = nil
		return nil
	}
	var value string
	if err := json.Unmarshal(b, &value); err != nil {
		return &json.UnmarshalTypeError{Value: "value", Type: reflect.TypeFor[string](), Field: "condition"}
	}
	c.value = &value
	return nil
}

func (h *Handler) decodeCopy(w http.ResponseWriter, r *http.Request) (*CopyInput, bool) {
	var body copyBody
	fields := []string{"sale_price_cents", "media_grade", "sleeve_grade", "condition"}
	if !httpx.DecodeJSONFields(w, r, h.logger, &body, fields...) {
		return nil, false
	}
	in := body.CopyInput
	in.KeepCondition = !body.Condition.sent
	in.Condition = body.Condition.value
	if fe := in.Normalize(); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return nil, false
	}
	return &in, true
}

func (h *Handler) createCopy(w http.ResponseWriter, r *http.Request) {
	in, ok := h.decodeCopy(w, r)
	if !ok {
		return
	}
	c, created, err := h.store.CreateCopy(r.Context(), in)
	if err != nil {
		h.writeErr(w, r, "create copy", parentErr(err, ErrEditionNotFound, "edition_id"))
		return
	}
	if h.covers != nil && r.URL.Query().Get("cover") == "auto" {
		h.fillScannedCover(r.Context(), c.EditionID)
	}
	httpx.WriteJSON(w, h.logger, createdStatus(created), c)
}

func (h *Handler) updateCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	in, ok := h.decodeCopy(w, r)
	if !ok {
		return
	}
	c, err := h.store.UpdateCopy(r.Context(), id, in)
	if err != nil {
		h.writeErr(w, r, "update copy", parentErr(err, ErrEditionNotFound, "edition_id"))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, c)
}

// locations serves GET /api/copies/locations: the places copies are stored
// at, the most used first, for the copy form's suggestions.
func (h *Handler) locations(w http.ResponseWriter, r *http.Request) {
	locations, err := h.store.Locations(r.Context())
	if err != nil {
		h.writeErr(w, r, "locations", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, locations)
}

func (h *Handler) deleteCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	if err := h.store.DeleteCopy(r.Context(), id); err != nil {
		h.writeErr(w, r, "delete copy", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) lendCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	var in LoanInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return
	}
	if fe := in.Normalize(); fe != nil {
		httpx.WriteFieldError(w, h.logger, fe)
		return
	}
	l, err := h.store.LendCopy(r.Context(), id, &in)
	if err != nil {
		h.writeErr(w, r, "lend copy", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusCreated, l)
}

// receiveCopy serves POST /api/copies/{id}/receipt: an ordered copy arrived
// and goes on the shelf as it is stored, without its expected delivery.
func (h *Handler) receiveCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	c, err := h.store.ReceiveCopy(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, "receive copy", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, c)
}

func (h *Handler) returnLoan(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	var in ReturnInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return
	}
	if in.ReturnedOn == nil {
		httpx.WriteFieldError(w, h.logger, fieldErr("returned_on", "is required"))
		return
	}
	l, err := h.store.ReturnLoan(r.Context(), id, in.ReturnedOn.Time)
	if err != nil {
		h.writeErr(w, r, "return loan", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, l)
}

// discardRequest serves DELETE /api/requests/{key}: what a failed add with
// that request key made goes, unless someone else uses or changed it.
func (h *Handler) discardRequest(w http.ResponseWriter, r *http.Request) {
	// chi matches on the escaped path when the request needed one, as for a key with a slash,
	// and hands its segment over still escaped; otherwise the segment is already decoded.
	raw := chi.URLParam(r, "key")
	var err error
	if r.URL.RawPath != "" {
		raw, err = url.PathUnescape(raw)
	}
	key, fe := normalizeRequestKey(&raw)
	if err != nil || fe != nil || key == nil {
		httpx.WriteError(w, h.logger, http.StatusNotFound, "Nie ma takiego klucza.")
		return
	}
	if err := h.store.DiscardRequest(r.Context(), *key); err != nil {
		h.writeErr(w, r, "discard request", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
