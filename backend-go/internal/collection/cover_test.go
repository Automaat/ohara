package collection

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

type interruptedUpload struct{}

func (interruptedUpload) Read([]byte) (int, error) {
	return 0, os.ErrDeadlineExceeded
}

func TestInterruptedCoverUpload(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/editions/1/cover", interruptedUpload{})
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	NewHandler(nil, nil).Mount(router)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "try again") {
		t.Fatalf("interrupted upload returned %d: %s", rec.Code, rec.Body.String())
	}
}
