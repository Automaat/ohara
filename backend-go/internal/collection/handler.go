package collection

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
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
	r.Delete("/titles/{id}", h.deleteTitle)

	r.Get("/editions", h.listEditions)
	r.Post("/editions", h.createEdition)
	r.Get("/editions/{id}", h.getEdition)
	r.Put("/editions/{id}", h.updateEdition)
	r.Delete("/editions/{id}", h.deleteEdition)
	r.Get("/editions/{id}/cover", h.getEditionCover)
	r.Put("/editions/{id}/cover", h.putEditionCover)
	r.Delete("/editions/{id}/cover", h.deleteEditionCover)

	r.Post("/copies", h.createCopy)
	r.Get("/copies/{id}", h.getCopy)
	r.Put("/copies/{id}", h.updateCopy)
	r.Delete("/copies/{id}", h.deleteCopy)
}

// writeErr maps store errors to responses. A missing parent named in the
// body is a 422 on that field; a missing resource in the path is a 404.
func (h *Handler) writeErr(w http.ResponseWriter, op string, err error) {
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
	case errors.Is(err, ErrKindLocked):
		httpx.WriteJSON(w, h.logger, http.StatusConflict, httpx.Error{Error: err.Error(), Field: "kind"})
	case errors.Is(err, ErrHasEditions), errors.Is(err, ErrHasCopies):
		httpx.WriteError(w, h.logger, http.StatusConflict, err.Error())
	default:
		httpx.WriteInternal(w, h.logger, op, err)
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

// Totals sums the copies in a collection view. Spent counts owned copies only.
type Totals struct {
	Copies     int   `json:"copies"`
	Owned      int   `json:"owned"`
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
		if it.Status == StatusWishlist {
			t.Wishlist++
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
	f.Query = r.URL.Query().Get("q")
	if code, err := barcode.Normalize(f.Query); err == nil {
		f.Query = code
	}
	items, err := h.store.Collection(r.Context(), f)
	if err != nil {
		h.writeErr(w, "collection", err)
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
	f := TitleFilter{Kind: kind, Query: r.URL.Query().Get("q")}
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
		h.writeErr(w, "list titles", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, titles)
}

// EditionWithCopies is an edition in a title's detail view.
type EditionWithCopies struct {
	Edition
	Copies []Copy `json:"copies"`
}

// TitleDetail is the GET /api/titles/{id} body.
type TitleDetail struct {
	Title
	Editions []EditionWithCopies `json:"editions"`
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
	out := TitleDetail{Title: t, Editions: make([]EditionWithCopies, 0, len(editions))}
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
		h.writeErr(w, "get title", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, detail)
}

func (h *Handler) decodeTitle(w http.ResponseWriter, r *http.Request) (*TitleInput, bool) {
	var in TitleInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
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
		h.writeErr(w, "create title", err)
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
		h.writeErr(w, "update title", notFound(err))
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
		h.writeErr(w, "delete title", notFound(err))
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
		h.writeErr(w, "list editions", err)
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
		h.writeErr(w, "get edition", notFound(err))
		return
	}
	t, err := h.store.GetTitle(ctx, e.TitleID)
	if err != nil {
		h.writeErr(w, "get edition title", err)
		return
	}
	copies, err := h.store.ListCopies(ctx, id)
	if err != nil {
		h.writeErr(w, "get edition copies", err)
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
		h.writeErr(w, "create edition", parentErr(err, ErrTitleNotFound, "title_id"))
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
		h.writeErr(w, "update edition", parentErr(err, ErrTitleNotFound, "title_id"))
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
		h.writeErr(w, "delete edition", notFound(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// CopyDetail is the GET /api/copies/{id} body.
type CopyDetail struct {
	Copy
	Edition Edition `json:"edition"`
	Title   Title   `json:"title"`
}

func (h *Handler) getCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	ctx := r.Context()
	c, err := h.store.GetCopy(ctx, id)
	if err != nil {
		h.writeErr(w, "get copy", err)
		return
	}
	e, err := h.store.GetEdition(ctx, c.EditionID)
	if err != nil {
		h.writeErr(w, "get copy edition", err)
		return
	}
	t, err := h.store.GetTitle(ctx, e.TitleID)
	if err != nil {
		h.writeErr(w, "get copy title", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, CopyDetail{Copy: c, Edition: e, Title: t})
}

func (h *Handler) decodeCopy(w http.ResponseWriter, r *http.Request) (*CopyInput, bool) {
	var in CopyInput
	if !httpx.DecodeJSON(w, r, h.logger, &in) {
		return nil, false
	}
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
		h.writeErr(w, "create copy", parentErr(err, ErrEditionNotFound, "edition_id"))
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
		h.writeErr(w, "update copy", parentErr(err, ErrEditionNotFound, "edition_id"))
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, c)
}

func (h *Handler) deleteCopy(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	if err := h.store.DeleteCopy(r.Context(), id); err != nil {
		h.writeErr(w, "delete copy", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
