package collection

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"slices"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

const (
	maxCoverBytes     = 8 << 20
	maxCoverPixels    = 16_000_000
	coverCacheControl = "private, max-age=31536000, immutable"
)

func validCover(photo []byte) bool {
	config, format, err := image.DecodeConfig(bytes.NewReader(photo))
	if err != nil || (format != "jpeg" && format != "png") || config.Width <= 0 ||
		config.Height <= 0 || config.Width > maxCoverPixels/config.Height {
		return false
	}
	_, _, err = image.Decode(bytes.NewReader(photo))
	return err == nil
}

// getEditionCover answers the edition's photo. A URL whose one `v` names the
// photo's current version, as the API hands it out, never changes content:
// a new photo or any other edit of the edition gets a new `v`. The browser
// may keep that answer; any other request (a query that does not parse, a
// second `v`), and every error, is no-store.
func (h *Handler) getEditionCover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	photo, version, err := h.store.editionCover(r.Context(), id)
	if err != nil {
		h.writeErr(w, r, "get edition cover", notFound(err))
		return
	}
	if query, err := url.ParseQuery(r.URL.RawQuery); err == nil && slices.Equal(query["v"], []string{version}) {
		w.Header().Set("Cache-Control", coverCacheControl)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Type", http.DetectContentType(photo))
	if _, err := w.Write(photo); err != nil {
		h.logger.Debug("write edition cover", "error", err)
	}
}

func (h *Handler) putEditionCover(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	photo, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCoverBytes))
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		httpx.WriteJSON(w, h.logger, http.StatusRequestEntityTooLarge,
			httpx.Error{Error: "choose a file up to 8 MiB", Field: "cover_photo"})
		return
	}
	if err != nil {
		httpx.WriteJSON(w, h.logger, http.StatusBadRequest,
			httpx.Error{Error: "photo upload interrupted; try again", Field: "cover_photo"})
		return
	}
	if !validCover(photo) {
		httpx.WriteFieldError(w, h.logger, fieldErr("cover_photo", "choose a valid JPEG or PNG up to 16 megapixels"))
		return
	}
	if err := h.store.setEditionCover(r.Context(), id, photo); err != nil {
		h.writeErr(w, r, "save edition cover", notFound(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) deleteEditionCover(w http.ResponseWriter, r *http.Request) {
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	if err := h.store.setEditionCover(r.Context(), id, nil); err != nil {
		h.writeErr(w, r, "remove edition cover", notFound(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
