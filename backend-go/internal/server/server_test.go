package server_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Automaat/ohara/backend-go/internal/server"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newServer(t *testing.T, deps server.Deps) *httptest.Server {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	srv := httptest.NewServer(server.New(server.Config{Version: "v0.0.0-test"}, logger, deps))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, url string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return resp.StatusCode, string(body)
}

func TestHealthAndStatus(t *testing.T) {
	tests := []struct {
		name         string
		db           server.Pinger
		healthCode   int
		statusCode   int
		wantStatus   string
		wantDatabase string
	}{
		{"database ok", fakeDB{}, http.StatusOK, http.StatusOK, "ok", "ok"},
		{"database down", fakeDB{err: errors.New("down")}, http.StatusServiceUnavailable, http.StatusServiceUnavailable, "degraded", "unavailable"},
		{"no database", nil, http.StatusOK, http.StatusOK, "ok", "unconfigured"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := newServer(t, server.Deps{DB: tt.db})

			code, _ := get(t, srv.URL+"/health")
			if code != tt.healthCode {
				t.Fatalf("/health = %d, want %d", code, tt.healthCode)
			}

			code, body := get(t, srv.URL+"/api/status")
			if code != tt.statusCode {
				t.Fatalf("/api/status = %d, want %d", code, tt.statusCode)
			}
			var st server.Status
			if err := json.Unmarshal([]byte(body), &st); err != nil {
				t.Fatalf("decode %q: %v", body, err)
			}
			if st.Status != tt.wantStatus || st.Database != tt.wantDatabase || st.Version != "v0.0.0-test" {
				t.Fatalf("status = %+v", st)
			}
		})
	}
}

func TestMetricsExposed(t *testing.T) {
	srv := newServer(t, server.Deps{})
	get(t, srv.URL+"/health")
	code, body := get(t, srv.URL+"/metrics")
	if code != http.StatusOK {
		t.Fatalf("/metrics = %d", code)
	}
	if !strings.Contains(body, `ohara_http_requests_total{method="GET",route="/health",status="2xx"}`) {
		t.Fatalf("/metrics lacks the /health request counter:\n%s", body)
	}
}

func TestFrontendAndUnknownAPI(t *testing.T) {
	webFS := fstest.MapFS{"index.html": {Data: []byte("<title>Ohara</title>")}}
	srv := newServer(t, server.Deps{Web: webFS})

	code, body := get(t, srv.URL+"/collection")
	if code != http.StatusOK || !strings.Contains(body, "<title>Ohara</title>") {
		t.Fatalf("client route = %d %q, want index.html", code, body)
	}

	code, body = get(t, srv.URL+"/api/nope")
	if code != http.StatusNotFound || !strings.Contains(body, `"error"`) {
		t.Fatalf("unknown api = %d %q, want JSON 404", code, body)
	}
}

func TestNoFrontendConfigured(t *testing.T) {
	srv := newServer(t, server.Deps{})
	if code, _ := get(t, srv.URL+"/"); code != http.StatusNotFound {
		t.Fatalf("/ without frontend = %d, want 404", code)
	}
}
