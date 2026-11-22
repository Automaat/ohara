package wishshare

import (
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/Automaat/ohara/backend-go/internal/httpx"
)

// Prefix is the path every public share answer sits under, the only one
// served without Authelia's Remote-User header besides the probes.
const Prefix = "/share"

// ShareLink is the body of GET and POST /api/wishlist/share: the link's path
// on this site, /share/{token}, and when it was made.
type ShareLink struct {
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// SharedWishlist is the body of GET /share/{token}/wishes.
type SharedWishlist struct {
	Wishes []Wish `json:"wishes"`
}

// Handler is the HTTP boundary for the share link and its public pages.
type Handler struct {
	store  *Store
	logger *slog.Logger
}

// NewHandler wires the store and logger.
func NewHandler(store *Store, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{store: store, logger: logger}
}

// Mount registers the owner's routes under the /api router.
func (h *Handler) Mount(r chi.Router) {
	r.Get("/wishlist/share", h.current)
	r.Post("/wishlist/share", h.create)
	r.Delete("/wishlist/share", h.delete)
}

func linkBody(l Link) ShareLink {
	return ShareLink{URL: Prefix + "/" + l.Token, CreatedAt: l.CreatedAt}
}

func (h *Handler) current(w http.ResponseWriter, r *http.Request) {
	l, err := h.store.Current(r.Context())
	if errors.Is(err, ErrNoLink) {
		httpx.WriteError(w, h.logger, http.StatusNotFound, "no share link")
		return
	}
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "share link", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, linkBody(l))
}

func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	l, err := h.store.Create(r.Context())
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "create share link", err)
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusCreated, linkBody(l))
}

func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	if err := h.store.Delete(r.Context()); err != nil {
		httpx.WriteInternal(w, r, h.logger, "delete share link", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// Public returns the handler for the paths under Prefix, which anyone with
// the link reaches: the page at /share/{token} and its data at
// /share/{token}/wishes, both GET only. An unknown or turned-off token, any
// other path and any other method answer the API's own 404, so the answer
// tells nothing about which tokens exist. No answer is cached, indexed or
// passes the link on as a referrer.
func (h *Handler) Public() http.Handler {
	r := chi.NewRouter()
	r.Use(publicHeaders)
	r.Get("/{token}", h.page)
	r.Get("/{token}/wishes", h.wishes)
	r.NotFound(h.notFound)
	r.MethodNotAllowed(h.notFound)
	return r
}

func publicHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("Cache-Control", "no-store")
		hd.Set("Referrer-Policy", "no-referrer")
		hd.Set("X-Robots-Tag", "noindex, nofollow")
		next.ServeHTTP(w, r)
	})
}

func (h *Handler) notFound(w http.ResponseWriter, _ *http.Request) {
	httpx.WriteError(w, h.logger, http.StatusNotFound, "not found")
}

// sharedWishes answers the wishes when the request's token is the active
// one; otherwise it writes the 404 or 500 itself and reports false. A cover
// on this site's own host is left out, so the page asks nothing of the app
// outside the share paths.
func (h *Handler) sharedWishes(w http.ResponseWriter, r *http.Request) ([]Wish, bool) {
	wishes, ok, err := h.store.SharedWishes(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "shared wishes", err)
		return nil, false
	}
	if !ok {
		h.notFound(w, r)
		return nil, false
	}
	for i := range wishes {
		if !outsideCover(wishes[i].CoverURL, r.Host) {
			wishes[i].CoverURL = nil
		}
	}
	return wishes, true
}

// coverHost matches a host a cover may name: a DNS name with an optional
// port, nothing a header or a CSP source could misread.
var coverHost = regexp.MustCompile(`^[a-z0-9-]+(\.[a-z0-9-]+)+(:\d{1,5})?$`)

// outsideCover reports a cover URL the guest's browser may load: HTTP or
// HTTPS on a DNS name other than host, the one the request named. An IP
// address in any spelling a browser accepts (127.1, 0x7f.1, 2130706433),
// a name without a dot such as localhost, and the request's own host are
// left out, since each can reach the app outside the share paths.
func outsideCover(cover *string, host string) bool {
	if cover == nil {
		return false
	}
	u, err := url.Parse(*cover)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return false
	}
	h := strings.ToLower(u.Host)
	if !coverHost.MatchString(h) {
		return false
	}
	name := strings.ToLower(u.Hostname())
	labels := strings.Split(name, ".")
	if endsInNumber(labels[len(labels)-1]) {
		return false
	}
	own := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		own = hh
	}
	return !strings.EqualFold(name, own)
}

// endsInNumber reports a last label that makes a browser read the whole
// host as an IPv4 address: decimal digits, or 0x and hex digits.
func endsInNumber(label string) bool {
	if hex, ok := strings.CutPrefix(label, "0x"); ok {
		return strings.Trim(hex, "0123456789abcdef") == ""
	}
	return label != "" && strings.Trim(label, "0123456789") == ""
}

func (h *Handler) wishes(w http.ResponseWriter, r *http.Request) {
	wishes, ok := h.sharedWishes(w, r)
	if !ok {
		return
	}
	httpx.WriteJSON(w, h.logger, http.StatusOK, SharedWishlist{Wishes: wishes})
}

func (h *Handler) page(w http.ResponseWriter, r *http.Request) {
	wishes, ok := h.sharedWishes(w, r)
	if !ok {
		return
	}
	body, csp, err := renderPage(wishes)
	if err != nil {
		httpx.WriteInternal(w, r, h.logger, "shared wishlist page", err)
		return
	}
	hd := w.Header()
	hd.Set("Content-Type", "text/html; charset=utf-8")
	hd.Set("Content-Security-Policy", csp)
	w.WriteHeader(http.StatusOK)
	if _, err := body.WriteTo(w); err != nil {
		h.logger.DebugContext(r.Context(), "write shared wishlist page", "err", err)
	}
}
