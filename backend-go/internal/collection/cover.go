package collection

import (
	"bytes"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

const (
	maxCoverBytes  = 8 << 20
	maxCoverPixels = 16_000_000
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

func (h *Handler) getEditionCover(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	id, ok := httpx.PathID(w, r, h.logger, "id")
	if !ok {
		return
	}
	photo, err := h.store.editionCover(r.Context(), id)
	if err != nil {
		h.writeErr(w, "get edition cover", notFound(err))
		return
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
	if err != nil || !validCover(photo) {
		httpx.WriteFieldError(w, h.logger, fieldErr("cover_photo", "choose a valid JPEG or PNG up to 16 megapixels"))
		return
	}
	if err := h.store.setEditionCover(r.Context(), id, photo); err != nil {
		h.writeErr(w, "save edition cover", notFound(err))
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
		h.writeErr(w, "remove edition cover", notFound(err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
