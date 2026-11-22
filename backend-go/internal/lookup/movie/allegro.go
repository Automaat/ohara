package movie

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/fetch"
	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/reqlog"
)

// AllegroUserAgent identifies Ohara to Allegro. The Allegro REST API terms
// require every request to carry the User-Agent registered for the app.
const AllegroUserAgent = "Ohara/1.0.0 (+https://github.com/Automaat/ohara)"

// Allegro searches the Allegro product catalog by GTIN. The catalog needs a
// user token, which a one-time OAuth device flow (Login) obtains; the
// refresh token is kept in a TokenStore and rotated on every refresh.
type Allegro struct {
	ClientID     string
	ClientSecret string
	Store        TokenStore
	Client       *http.Client
	APIBase      string
	AuthBase     string
	// UserAgent overrides AllegroUserAgent.
	UserAgent string
	Now       func() time.Time

	tokenTimeout time.Duration

	mu      sync.Mutex
	token   *Token
	unsaved bool
	chain   []string
	op      *tokenOp
}

// tokenOp is a token refresh, a retried save of the current pair, or the
// first load of the stored pair, that runs on a context detached from the
// lookup that started it: Allegro retires the old refresh token as soon as
// it answers, so the new pair has to be kept even when the lookup gave up,
// and a lookup waiting for another's load still stops at its own deadline.
// One runs at a time, so the store ends with the latest pair. chain lists the refresh tokens of the
// pairs this process loaded or issued since the store last confirmed one:
// a stored pair outside it came from another process, such as
// `api allegro-login`.
type tokenOp struct {
	done    chan struct{}
	refresh bool
	chain   []string
	access  string
	err     error
}

// Token is an OAuth token pair.
type Token struct {
	Access  string
	Refresh string
	Expiry  time.Time
}

// TokenStore persists one provider's token pair across restarts.
type TokenStore interface {
	Load(ctx context.Context, provider string) (Token, bool, error)
	Save(ctx context.Context, provider string, t Token) error
}

// TokenSwapper is a TokenStore that can save a pair conditionally. Swap
// stores t and returns it with true when nothing is stored or the stored
// pair's refresh token is in expected, in one step; otherwise it leaves
// the store alone and returns the stored pair with false. A refresh saves
// through Swap when the store has it, so a pair `api allegro-login` stores
// at the same moment is never overwritten; with Load and Save alone a login
// landing between the two is lost.
type TokenSwapper interface {
	Swap(ctx context.Context, provider string, expected []string, t Token) (Token, bool, error)
}

// Allegro errors. ErrAllegroRefused is the issue's stop-and-flag condition:
// the app is not allowed to read the catalog.
var (
	ErrAllegroNotAuthorized = errors.New("allegro not authorized: run `api allegro-login`")
	ErrAllegroRefused       = errors.New("allegro refused catalog access")
)

// tokenLeeway refreshes a token this long before it expires.
const tokenLeeway = time.Minute

// defaultTokenTimeout bounds one token load, refresh and save.
const defaultTokenTimeout = 20 * time.Second

// Name is "allegro".
func (*Allegro) Name() string { return "allegro" }

type allegroProducts struct {
	Products []struct {
		Name     string `json:"name"`
		Category struct {
			ID string `json:"id"`
		} `json:"category"`
	} `json:"products"`
}

// Products runs GET /sale/products?mode=GTIN for barcode. A rejected access
// token is refreshed once.
func (a *Allegro) Products(ctx context.Context, code string) ([]Product, error) {
	for attempt := 0; ; attempt++ {
		tok, err := a.accessToken(ctx, attempt > 0)
		if err != nil {
			return nil, err
		}
		q := url.Values{"phrase": {code}, "mode": {"GTIN"}, "language": {"pl-PL"}}
		resp, err := get(ctx, a.client(), a.Name(), fetch.Request{
			URL: a.apiBase() + "/sale/products?" + q.Encode(),
			Header: http.Header{
				"Accept":          {"application/vnd.allegro.public.v1+json"},
				"Accept-Language": {"pl-PL"},
				"Authorization":   {"Bearer " + tok},
				"User-Agent":      {a.userAgent()},
			},
		})
		switch {
		case isStatus(err, http.StatusUnauthorized) && attempt == 0:
			continue
		case isStatus(err, http.StatusForbidden):
			return nil, fmt.Errorf("%w: %w", ErrAllegroRefused, err)
		case err != nil:
			return nil, err
		}
		var r allegroProducts
		if err := json.Unmarshal(resp.Body, &r); err != nil {
			return nil, parseFailed(a.Name(), err)
		}
		products := []Product{}
		for _, p := range r.Products {
			if n := strings.TrimSpace(p.Name); n != "" {
				products = append(products, Product{Source: "allegro", Name: n, Category: p.Category.ID})
			}
		}
		found(a.Name(), len(products))
		return products, nil
	}
}

// accessToken returns an access token, refreshed when it is about to expire
// or force is set. A lookup that stops waiting leaves the refresh running.
func (a *Allegro) accessToken(ctx context.Context, force bool) (string, error) {
	for {
		access, op, err := a.currentToken(ctx, force)
		if op == nil {
			return access, err
		}
		select {
		case <-op.done:
		case <-ctx.Done():
			return "", fmt.Errorf("allegro: wait for token refresh: %w", ctx.Err())
		}
		if op.refresh || op.err != nil {
			return op.access, op.err
		}
	}
}

// currentToken returns a usable access token, or the operation to wait for
// before asking again. The first call loads the stored pair in the
// background, and a pair whose save failed is saved again there.
func (a *Allegro) currentToken(ctx context.Context, force bool) (string, *tokenOp, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.token == nil {
		if a.op == nil {
			if err := ctx.Err(); err != nil {
				return "", nil, fmt.Errorf("allegro: load token: %w", err)
			}
			op := &tokenOp{done: make(chan struct{})}
			a.op = op
			go a.runLoad(reqlog.Detach(context.WithoutCancel(ctx)), op)
		}
		return "", a.op, nil
	}
	if !force && a.now().Add(tokenLeeway).Before(a.token.Expiry) {
		if a.unsaved && a.op == nil {
			a.startOp(ctx, *a.token, false)
		}
		return a.token.Access, nil, nil
	}
	if a.op == nil {
		if err := ctx.Err(); err != nil {
			return "", nil, fmt.Errorf("allegro: token refresh: %w", err)
		}
		a.startOp(ctx, *a.token, true)
	}
	return "", a.op, nil
}

// startOp runs a refresh of t, or a save of t, in its own goroutine. The
// caller holds a.mu. The op may outlive the lookup that started it, so its
// context drops the request's log info.
func (a *Allegro) startOp(ctx context.Context, t Token, refresh bool) {
	op := &tokenOp{done: make(chan struct{}), refresh: refresh, chain: append(slices.Clone(a.chain), t.Refresh)}
	a.op = op
	go a.runOp(reqlog.Detach(context.WithoutCancel(ctx)), op, t)
}

// runLoad loads the stored pair, bounded by the token timeout. A failed
// load leaves no pair, so the next lookup loads again.
func (a *Allegro) runLoad(ctx context.Context, op *tokenOp) {
	defer close(op.done)
	ctx, cancel := context.WithTimeout(ctx, a.timeout())
	defer cancel()
	t, ok, err := a.Store.Load(ctx, a.Name())
	a.mu.Lock()
	defer a.mu.Unlock()
	a.op = nil
	switch {
	case err != nil:
		op.err = fmt.Errorf("allegro: load token: %w", err)
	case !ok:
		op.err = ErrAllegroNotAuthorized
	case a.token == nil:
		a.token = &t
		a.chain = []string{t.Refresh}
	}
}

// runOp drops a refresh token Allegro rejects, so the next lookup loads
// the pair `api allegro-login` stores.
func (a *Allegro) runOp(ctx context.Context, op *tokenOp, t Token) {
	defer close(op.done)
	if op.refresh {
		next, err := a.refresh(ctx, t.Refresh)
		if err != nil {
			op.err = err
			a.mu.Lock()
			defer a.mu.Unlock()
			a.op = nil
			if errors.Is(err, ErrAllegroNotAuthorized) {
				a.token, a.chain, a.unsaved = nil, nil, false
			}
			return
		}
		t = next
		op.chain = append(op.chain, t.Refresh)
	}
	t, saved := a.persist(ctx, t, op.chain)
	op.access = t.Access
	a.mu.Lock()
	defer a.mu.Unlock()
	a.op = nil
	a.token = &t
	a.unsaved = !saved
	a.chain = []string{t.Refresh}
	if !saved {
		a.chain = op.chain
	}
}

// persist stores t and returns the pair to use and whether the store holds
// it. A stored pair outside chain, stored meanwhile by `api allegro-login`
// for example, wins over t.
func (a *Allegro) persist(ctx context.Context, t Token, chain []string) (Token, bool) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout())
	defer cancel()
	stored, kept, err := a.save(ctx, t, chain)
	if err != nil {
		slog.WarnContext(ctx, "allegro: save token, retrying on the next lookup", "err", err)
		return t, false
	}
	if kept {
		return stored, true
	}
	return t, true
}

// save stores t unless the store holds a pair outside chain, which it
// returns with true.
func (a *Allegro) save(ctx context.Context, t Token, chain []string) (Token, bool, error) {
	if s, ok := a.Store.(TokenSwapper); ok {
		stored, swapped, err := s.Swap(ctx, a.Name(), chain, t)
		return stored, !swapped && err == nil, err
	}
	stored, ok, err := a.Store.Load(ctx, a.Name())
	if err != nil {
		return Token{}, false, err
	}
	if ok && !slices.Contains(chain, stored.Refresh) {
		return stored, true, nil
	}
	return Token{}, false, a.Store.Save(ctx, a.Name(), t)
}

// refresh trades refreshToken for a new pair.
func (a *Allegro) refresh(ctx context.Context, refreshToken string) (Token, error) {
	ctx, cancel := context.WithTimeout(ctx, a.timeout())
	defer cancel()
	t, err := a.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}})
	if oe, ok := errors.AsType[*oauthError](err); ok && oe.Code == "invalid_grant" {
		return Token{}, fmt.Errorf("%w: refresh token rejected", ErrAllegroNotAuthorized)
	}
	return t, err
}

func (a *Allegro) timeout() time.Duration {
	if a.tokenTimeout > 0 {
		return a.tokenTimeout
	}
	return defaultTokenTimeout
}

type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string {
	if e.Description == "" {
		return "oauth: " + e.Code
	}
	return "oauth: " + e.Code + ": " + e.Description
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// tokenRequest posts to /auth/oauth/token with the parameters in the query,
// as Allegro documents it.
func (a *Allegro) tokenRequest(ctx context.Context, q url.Values) (Token, error) {
	var tr tokenResponse
	if err := a.authPost(ctx, "/auth/oauth/token", q, &tr); err != nil {
		return Token{}, err
	}
	if tr.AccessToken == "" || tr.RefreshToken == "" {
		return Token{}, parseFailed("allegro", errors.New("token response without tokens"))
	}
	succeeded("allegro")
	return Token{
		Access:  tr.AccessToken,
		Refresh: tr.RefreshToken,
		Expiry:  a.now().Add(time.Duration(tr.ExpiresIn) * time.Second),
	}, nil
}

// authPost sends an authenticated POST to the OAuth server and decodes a 200
// answer into dst, or an OAuth error answer into *oauthError.
func (a *Allegro) authPost(ctx context.Context, path string, q url.Values, dst any) error {
	resp, err := get(ctx, a.client(), "allegro", fetch.Request{
		Method: http.MethodPost,
		URL:    a.authBase() + path + "?" + q.Encode(),
		Header: http.Header{
			"Authorization": {"Basic " + base64.StdEncoding.EncodeToString([]byte(a.ClientID+":"+a.ClientSecret))},
			"Accept":        {"application/json"},
			"User-Agent":    {a.userAgent()},
		},
		OK: []int{http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized},
	})
	if err != nil {
		return err
	}
	if resp.Status != http.StatusOK {
		metrics.ObserveSource("allegro", "oauth_error")
		oe := &oauthError{}
		if json.Unmarshal(resp.Body, oe) != nil || oe.Code == "" {
			oe.Code = fmt.Sprintf("status %d", resp.Status)
		}
		return oe
	}
	if err := json.Unmarshal(resp.Body, dst); err != nil {
		return parseFailed("allegro", err)
	}
	return nil
}

type deviceResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

// Login runs the OAuth device flow: it prints the page the user opens to
// approve the app, waits for the approval and stores the token pair.
func (a *Allegro) Login(ctx context.Context, out io.Writer) error {
	var d deviceResponse
	if err := a.authPost(ctx, "/auth/oauth/device", url.Values{"client_id": {a.ClientID}}, &d); err != nil {
		return fmt.Errorf("start device flow: %w", err)
	}
	if d.DeviceCode == "" {
		return errors.New("start device flow: no device code")
	}
	link := d.VerificationURIComplete
	if link == "" {
		link = d.VerificationURI
	}
	if _, err := fmt.Fprintf(out, "Open %s and confirm code %s\n", link, d.UserCode); err != nil {
		return fmt.Errorf("print device code: %w", err)
	}
	interval := time.Duration(max(d.Interval, 1)) * time.Second
	ctx, cancel := context.WithTimeout(ctx, time.Duration(max(d.ExpiresIn, 1))*time.Second)
	defer cancel()
	q := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {d.DeviceCode}}
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("device flow: %w", ctx.Err())
		case <-time.After(interval):
		}
		t, err := a.tokenRequest(ctx, q)
		var oe *oauthError
		switch {
		case errors.As(err, &oe) && oe.Code == "authorization_pending":
			continue
		case errors.As(err, &oe) && oe.Code == "slow_down":
			interval += 5 * time.Second
			continue
		case err != nil:
			return fmt.Errorf("device flow: %w", err)
		}
		if err := a.Store.Save(ctx, a.Name(), t); err != nil {
			return fmt.Errorf("save token: %w", err)
		}
		a.mu.Lock()
		a.token = &t
		a.mu.Unlock()
		_, err = fmt.Fprintln(out, "Allegro authorized.")
		return err
	}
}

func (a *Allegro) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now()
}

func (a *Allegro) client() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}

func (a *Allegro) userAgent() string {
	if a.UserAgent != "" {
		return a.UserAgent
	}
	return AllegroUserAgent
}

func (a *Allegro) apiBase() string {
	if a.APIBase != "" {
		return a.APIBase
	}
	return "https://api.allegro.pl"
}

func (a *Allegro) authBase() string {
	if a.AuthBase != "" {
		return a.AuthBase
	}
	return "https://allegro.pl"
}
