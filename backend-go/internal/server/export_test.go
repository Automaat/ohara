package server

import (
	"log/slog"
	"net/http"
	"time"
)

// NewWithTimeout is New with a shorter request timeout, for tests.
func NewWithTimeout(cfg Config, logger *slog.Logger, deps Deps, timeout time.Duration) http.Handler {
	return newRouter(cfg, logger, deps, timeout)
}
