// Package httpx holds the JSON request and response helpers shared by the API
// handlers. Errors have one shape: {"error": "...", "field": "..."}, where
// field names the offending request field for validation failures.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// MaxBodyBytes caps JSON request bodies.
const MaxBodyBytes = 1 << 16

// Error is the body of every API error response.
type Error struct {
	Error string `json:"error"`
	Field string `json:"field,omitempty"`
}

// FieldError is a validation failure tied to one request field.
type FieldError struct {
	Field string
	Msg   string
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Msg }

// WriteJSON encodes body with status code. Responses are never cached: the
// data changes with every edit.
func WriteJSON(w http.ResponseWriter, logger *slog.Logger, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		logger.Error("encode response", "err", err)
	}
}

// WriteError sends {"error": msg} with status code.
func WriteError(w http.ResponseWriter, logger *slog.Logger, code int, msg string) {
	WriteJSON(w, logger, code, Error{Error: msg})
}

// WriteFieldError sends a 422 naming the invalid field.
func WriteFieldError(w http.ResponseWriter, logger *slog.Logger, fe *FieldError) {
	WriteJSON(w, logger, http.StatusUnprocessableEntity, Error{Error: fe.Msg, Field: fe.Field})
}

// WriteInternal logs err and sends a generic 500.
func WriteInternal(w http.ResponseWriter, logger *slog.Logger, op string, err error) {
	logger.Error(op, "err", err)
	WriteError(w, logger, http.StatusInternalServerError, "internal server error")
}

// DecodeJSON reads a size-limited JSON body into dst. On failure it writes a
// 422 and returns false. Trailing data after the object is rejected.
func DecodeJSON(w http.ResponseWriter, r *http.Request, logger *slog.Logger, dst any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	err := dec.Decode(dst)
	if err == nil {
		if _, extra := dec.Token(); !errors.Is(extra, io.EOF) {
			err = errors.New("unexpected data after the JSON object")
		}
	}
	if err != nil {
		WriteFieldError(w, logger, &FieldError{Field: "body", Msg: decodeMessage(err)})
		return false
	}
	return true
}

func decodeMessage(err error) string {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) && typeErr.Field != "" {
		return fmt.Sprintf("invalid JSON: %s must be %s", typeErr.Field, typeErr.Type)
	}
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return "request body too large"
	}
	return "invalid JSON: " + err.Error()
}

// PathID parses a positive integer path parameter. On failure it writes a 404,
// because no resource has that ID, and returns false.
func PathID(w http.ResponseWriter, r *http.Request, logger *slog.Logger, param string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, param), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, logger, http.StatusNotFound, "not found")
		return 0, false
	}
	return id, true
}
