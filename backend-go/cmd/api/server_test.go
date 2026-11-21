package main

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/server"
)

// TestGeneralOptionsReachesRouter checks that the server leaves "OPTIONS *"
// to the router, so its answer carries the security headers.
func TestGeneralOptionsReachesRouter(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	srv := newHTTPServer("", server.New(server.Config{}, logger, server.Deps{}))
	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	t.Cleanup(func() {
		if err := srv.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodOptions, "http://"+ln.Addr().String(), http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.URL = &url.URL{Scheme: "http", Host: ln.Addr().String(), Opaque: "*"}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("OPTIONS *: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("OPTIONS * = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}
}
