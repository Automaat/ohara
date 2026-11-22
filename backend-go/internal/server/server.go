// Package server wires the HTTP router and middleware.
package server

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"runtime/debug"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/reqlog"
	"github.com/Automaat/ohara/backend-go/internal/web"
)

const requestTimeout = 30 * time.Second

// Pinger is the part of *pgxpool.Pool the health probe needs.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Config holds the resolved runtime settings; cmd/api owns env loading.
// Version names the running build in /api/status. AccessLog adds one log line
// per request; metrics are recorded either way. RequireRemoteUser answers 401
// to every request without the Remote-User header Authelia's forward auth
// sets, except the probes on /health and /metrics.
type Config struct {
	Version           string
	AccessLog         bool
	RequireRemoteUser bool
}

// Mounter adds a domain's routes to the /api router.
type Mounter interface {
	Mount(r chi.Router)
}

// Deps are the runtime dependencies. A nil DB reports the database as
// unconfigured; a nil Web serves no frontend. API holds the domain handlers
// mounted under /api.
type Deps struct {
	DB  Pinger
	Web fs.FS
	API []Mounter
}

// New returns the router: probes, metrics, the JSON API and the frontend.
func New(cfg Config, logger *slog.Logger, deps Deps) http.Handler {
	return newRouter(cfg, logger, deps, requestTimeout)
}

// newRouter is New with the request timeout as a parameter, so tests can
// shorten it.
func newRouter(cfg Config, logger *slog.Logger, deps Deps, timeout time.Duration) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	r := chi.NewRouter()
	r.Use(securityHeaders)
	r.Use(requestID)
	r.Use(requestObserver(logger, cfg.AccessLog))
	r.Use(compress())
	r.Use(generalOptions(cfg.RequireRemoteUser))
	r.Use(recoverer(logger))
	r.Use(timeoutAfter(timeout, logger))
	if cfg.RequireRemoteUser {
		r.Use(remoteUserGuard(logger))
	}
	r.Use(crossOriginGuard(logger))
	r.Use(jsonMethodNotAllowed(r, logger))

	r.Get("/health", healthHandler(logger, deps.DB))
	r.Handle("/metrics", metrics.Handler())

	r.Route("/api", func(r chi.Router) {
		r.Get("/status", statusHandler(logger, cfg.Version, deps.DB))
		for _, m := range deps.API {
			m.Mount(r)
		}
		r.NotFound(func(w http.ResponseWriter, _ *http.Request) {
			httpx.WriteError(w, logger, http.StatusNotFound, "not found")
		})
	})

	if deps.Web != nil {
		r.Handle("/*", web.Handler(deps.Web))
	}
	return r
}

// jsonMethodNotAllowed gives chi's own 405 the API's JSON error body. Only
// chi's default handler knows which methods a path allows, including any
// added with chi.RegisterMethod, and it writes them as Allow headers with an
// empty body, so its answer is completed on the way out instead of replaced.
// Only a request no route matches gets the wrapped writer; a handler keeps the
// writer it had.
func jsonMethodNotAllowed(routes chi.Routes, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path := r.URL.RawPath
			if path == "" {
				path = r.URL.Path
			}
			if routes.Match(chi.NewRouteContext(), r.Method, path) {
				next.ServeHTTP(w, r)
				return
			}
			next.ServeHTTP(&methodNotAllowedWriter{ResponseWriter: w, logger: logger}, r)
		})
	}
}

// methodNotAllowedWriter turns a 405 without a body of its own into the JSON
// error and drops the empty body that follows.
type methodNotAllowedWriter struct {
	http.ResponseWriter
	logger   *slog.Logger
	wrote    bool
	replaced bool
}

func (m *methodNotAllowedWriter) WriteHeader(code int) {
	if !m.wrote && code == http.StatusMethodNotAllowed && m.Header().Get("Content-Type") == "" {
		m.wrote, m.replaced = true, true
		httpx.WriteError(m.ResponseWriter, m.logger, code, "method not allowed")
		return
	}
	m.wrote = true
	m.ResponseWriter.WriteHeader(code)
}

func (m *methodNotAllowedWriter) Write(b []byte) (int, error) {
	if m.replaced {
		return len(b), nil
	}
	m.wrote = true
	return m.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (m *methodNotAllowedWriter) Unwrap() http.ResponseWriter { return m.ResponseWriter }

// recoverer turns a handler panic into a logged error, with its stack, and a
// JSON 500 when nothing was written yet. Like chi's Recoverer it lets
// http.ErrAbortHandler itself through and sends no 500 on a protocol upgrade.
func recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				rvr := recover()
				if rvr == nil {
					return
				}
				if isAbortHandler(rvr) {
					panic(rvr)
				}
				logger.Error("handler panic",
					"request_id", middleware.GetReqID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"panic", rvr,
					"stack", string(debug.Stack()),
				)
				if ww.Status() == 0 && r.Header.Get("Connection") != "Upgrade" {
					writeFallback(ww, logger, http.StatusInternalServerError, "internal server error")
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// timeoutAfter cancels the request context after d. A handler that returns
// past the deadline without writing gets a JSON 504; one that already wrote
// keeps its answer.
func timeoutAfter(d time.Duration, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				cancel()
				if errors.Is(ctx.Err(), context.DeadlineExceeded) && ww.Status() == 0 {
					writeFallback(ww, logger, http.StatusGatewayTimeout, "gateway timeout")
				}
			}()
			next.ServeHTTP(ww, r.WithContext(ctx))
		})
	}
}

// isAbortHandler reports a panic with http.ErrAbortHandler itself, the value
// net/http and chi's Recoverer compare against; a wrapped one is an ordinary
// panic. Both sides are compared as panic values, so a value of another
// dynamic type, comparable or not, is simply unequal.
func isAbortHandler(rvr any) bool {
	return rvr == any(http.ErrAbortHandler)
}

// bodyHeaders describe a handler's own body; the fallback answer drops them
// so a length, encoding or validator set for a body never sent does not
// describe the JSON error instead.
var bodyHeaders = []string{
	"Content-Length", "Content-Encoding", "Content-Range", "Content-Disposition", "ETag", "Last-Modified",
}

// writeFallback sends the error answer for a handler that wrote nothing.
func writeFallback(w http.ResponseWriter, logger *slog.Logger, code int, msg string) {
	for _, h := range bodyHeaders {
		w.Header().Del(h)
	}
	httpx.WriteError(w, logger, code, msg)
}

// requestID gives every request a new ID from chi's RequestID, which would
// take the client's X-Request-Id as is; the client's header is dropped first,
// so logs hold only IDs the server made. The ID goes on the context, where
// middleware.GetReqID finds it for the access log, into the X-Request-Id
// response header and, with the method and path, into the reqlog request info
// that *Context log calls write. It runs right after securityHeaders, so every
// answer the router writes carries the header.
func requestID(next http.Handler) http.Handler {
	assign := middleware.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := middleware.GetReqID(r.Context())
		w.Header().Set(middleware.RequestIDHeader, id)
		next.ServeHTTP(w, r.WithContext(reqlog.WithRequest(r.Context(), id, r.Method, r.URL.Path)))
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(middleware.RequestIDHeader) != "" {
			r = r.Clone(r.Context())
			r.Header.Del(middleware.RequestIDHeader)
		}
		assign.ServeHTTP(w, r)
	})
}

// permissionsPolicy lets only Ohara's own pages open the camera, for the
// scanner, and denies the other features the SPA never uses.
const permissionsPolicy = "camera=(self), microphone=(), geolocation=()"

// securityHeaders adds the browser security headers to every response. It
// runs first, so the recoverer's 500, the timeout's 504 and the router's own
// 404 and 405 answers carry them too.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Permissions-Policy", permissionsPolicy)
		next.ServeHTTP(w, r)
	})
}

// generalOptions answers "OPTIONS *" with an empty 200, as net/http does when
// the server's DisableGeneralOptionsHandler is false. cmd/api sets it, so the
// request reaches the router and its answer carries the security headers and
// X-Request-Id; the request observer runs first, so it is counted and, with
// the access log on, logged. When the Remote-User check is on, one without
// the header goes on to remoteUserGuard, which answers 401.
func generalOptions(requireRemoteUser bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodOptions || r.RequestURI != "*" ||
				requireRemoteUser && r.Header.Get("Remote-User") == "" {
				next.ServeHTTP(w, r)
				return
			}
			w.Header().Set("Content-Length", "0")
			if r.ContentLength != 0 {
				_, _ = io.Copy(io.Discard, http.MaxBytesReader(w, r.Body, 4<<10))
			}
			w.WriteHeader(http.StatusOK)
		})
	}
}

// remoteUserGuard answers 401 to a request without a Remote-User header, or
// with an empty one, so a request that reaches the container without passing
// Authelia gets nothing. The guard checks only that the header is there, so it
// is worth as much as the proxy that sets it and the firewall that keeps
// everyone else off the port; the name in it is not used. The Docker
// healthcheck and Prometheus reach /health and /metrics directly and stay open.
func remoteUserGuard(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Remote-User") == "" && !probePath(r) {
				httpx.WriteError(w, logger, http.StatusUnauthorized, "authentication required")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// probePath reports a GET of /health or /metrics, the requests served without
// the Remote-User header. Only GET is exempt: chi has the health probe for GET
// alone and sends HEAD /health to the frontend. The path is read the way chi
// routes it, RawPath when set, so an escaped spelling such as /heal%74h, which
// chi also sends to the frontend, is not exempt.
func probePath(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	path := r.URL.RawPath
	if path == "" {
		path = r.URL.Path
	}
	return path == "/health" || path == "/metrics"
}

// crossOriginGuard answers 403 to a cross-origin write. Authelia's session
// cookie covers every sibling site of the parent domain, so their pages could
// otherwise write here.
func crossOriginGuard(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if crossOriginWrite(r) {
				httpx.WriteError(w, logger, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// crossOriginWrite reports a request other than GET, HEAD and OPTIONS that the
// browser marks as cross-origin: a Sec-Fetch-Site other than same-origin or
// none, or an Origin whose host is not Host. Each header decides on its own,
// unlike http.CrossOriginProtection, which ignores Origin once Sec-Fetch-Site
// is set. Clients that send neither header, like curl, pass.
func crossOriginWrite(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "", "same-origin", "none":
	default:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return true
	}
	return u.Host != r.Host
}

func requestObserver(logger *slog.Logger, accessLog bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			dur := time.Since(start)
			route := chi.RouteContext(r.Context()).RoutePattern()
			status := ww.Status()
			if status == 0 {
				status = http.StatusOK
			}
			metrics.ObserveRequest(r.Method, route, status, dur)
			if !accessLog {
				return
			}
			logger.Info("request",
				"request_id", middleware.GetReqID(r.Context()),
				"method", r.Method,
				"path", r.URL.Path,
				"route", route,
				"status", status,
				"bytes", ww.BytesWritten(),
				"latency_ms", float64(dur.Microseconds())/1000.0,
			)
		})
	}
}

// dbState is "ok", "unavailable" or "unconfigured".
func dbState(ctx context.Context, logger *slog.Logger, db Pinger) string {
	if db == nil {
		return "unconfigured"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := db.Ping(ctx); err != nil {
		logger.Error("database ping failed", "err", err)
		return "unavailable"
	}
	return "ok"
}

func healthHandler(logger *slog.Logger, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if dbState(r.Context(), logger, db) == "unavailable" {
			writeJSON(w, logger, http.StatusServiceUnavailable, map[string]string{"status": "unavailable"})
			return
		}
		writeJSON(w, logger, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// Status is the /api/status body the frontend shows on its home page.
type Status struct {
	Status   string `json:"status"`
	Version  string `json:"version"`
	Database string `json:"database"`
}

func statusHandler(logger *slog.Logger, version string, db Pinger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		st := Status{Status: "ok", Version: version, Database: dbState(r.Context(), logger, db)}
		code := http.StatusOK
		if st.Database == "unavailable" {
			st.Status = "degraded"
			code = http.StatusServiceUnavailable
		}
		writeJSON(w, logger, code, st)
	}
}

func writeJSON(w http.ResponseWriter, logger *slog.Logger, code int, body any) {
	httpx.WriteJSON(w, logger, code, body)
}
