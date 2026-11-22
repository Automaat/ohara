package discogscollection

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/discogs"
	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Handler is the HTTP boundary of the Discogs collection push.
type Handler struct {
	pusher *Pusher
	logger *slog.Logger
}

// NewHandler serves pusher.
func NewHandler(pusher *Pusher, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{pusher: pusher, logger: logger}
}

// Mount registers the routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/copies/{id}/discogs", h.get)
	r.Post("/copies/{id}/discogs", h.push)
	r.Post("/copies/{id}/discogs/adopt", h.adopt)
	r.Delete("/copies/{id}/discogs/add", h.discard)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	s, err := h.pusher.State(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, id, err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, s)
}

func (h *Handler) push(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	s, err := h.pusher.Push(r.Context(), id, false)
	if err != nil {
		h.writeErr(w, r, id, err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, s)
}

// adopt is the owner's word that the release's only instance in the
// collection is the one an add of unknown outcome made.
func (h *Handler) adopt(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	s, err := h.pusher.Push(r.Context(), id, true)
	if err != nil {
		h.writeErr(w, r, id, err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, s)
}

// discard is the owner's word that an add of unknown outcome made nothing
// of theirs.
func (h *Handler) discard(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	s, err := h.pusher.Discard(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, id, err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, s)
}

// Field names the push in its error answers, so the page shows their
// Polish message as it is.
const Field = "discogs_collection"

func (h *Handler) writeErr(w http.ResponseWriter, r *http.Request, id int64, err error) {
	var refused *RefusedError
	var field *FieldError
	var up *UpstreamError
	switch {
	case errors.Is(err, ErrNotFound):
		httpx.WriteError(w, h.logger, http.StatusNotFound, "not found")
	case errors.Is(err, ErrDisabled):
		h.fail(w, http.StatusServiceUnavailable, "Kolekcja Discogs jest wyłączona: brak OHARA_DISCOGS_TOKEN.")
	case errors.As(err, &refused):
		h.fail(w, http.StatusConflict, refused.Reason)
	case errors.Is(err, ErrForeignInstance):
		h.fail(w, http.StatusConflict, ForeignInstanceMessage)
	case errors.Is(err, ErrLinkedElsewhere):
		h.fail(w, http.StatusConflict, LinkedElsewhereMessage)
	case errors.Is(err, ErrAddUnknown):
		h.fail(w, http.StatusConflict, AddUnknownMessage)
	case errors.Is(err, ErrAddUnsettled):
		h.fail(w, http.StatusConflict, AddUnsettledMessage)
	case errors.Is(err, ErrAdoptNotOne):
		h.fail(w, http.StatusConflict, AdoptNotOneMessage)
	case errors.Is(err, ErrSiblingSettling):
		h.fail(w, http.StatusConflict, SiblingSettlingMessage)
	case errors.Is(err, ErrSiblingAddUnknown):
		h.fail(w, http.StatusConflict, SiblingAddUnknownMessage)
	case errors.Is(err, ErrNoUnknownAdd):
		h.fail(w, http.StatusConflict, NoUnknownAddMessage)
	case errors.As(err, &field):
		h.logger.WarnContext(r.Context(), "discogs collection push", "copy", id, "err", err)
		h.fail(w, http.StatusBadGateway,
			fmt.Sprintf("Kolekcja Discogs nie ma pola „%s” z wartością „%s”.", field.Field, field.Option))
	case errors.As(err, &up):
		h.logger.WarnContext(r.Context(), "discogs collection push", "copy", id, "err", err)
		h.fail(w, http.StatusBadGateway, upstreamMessage(up))
	default:
		httpx.WriteInternal(w, r, h.logger, "discogs collection push", err)
	}
}

func (h *Handler) fail(w http.ResponseWriter, code int, msg string) {
	httpx.WriteJSON(w, h.logger, code, httpx.Error{Error: msg, Field: Field})
}

// upstreamMessage tells the page, in Polish, how Discogs failed.
func upstreamMessage(err *UpstreamError) string {
	se, ok := errors.AsType[discogs.StatusError](err)
	msg := statusMessage(se, ok)
	if err.Unknown {
		msg += " Nie wiadomo, czy Discogs dodał wydanie: Ohara nie doda go ponownie, dopóki to się nie wyjaśni."
	}
	return msg
}

func statusMessage(se discogs.StatusError, ok bool) string {
	switch {
	case !ok:
		return "Nie udało się połączyć z Discogs. Spróbuj ponownie."
	case se.Code == http.StatusUnauthorized || se.Code == http.StatusForbidden:
		return fmt.Sprintf("Discogs odrzucił token (HTTP %d).", se.Code)
	case se.Code == http.StatusTooManyRequests:
		return "Discogs ogranicza liczbę zapytań (HTTP 429). Spróbuj za minutę."
	}
	return fmt.Sprintf("Discogs odpowiedział błędem (HTTP %d). Spróbuj ponownie.", se.Code)
}
