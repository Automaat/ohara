package movie

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/metrics"
	"github.com/Automaat/ohara/backend-go/internal/recovery"
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

	mu         sync.Mutex
	token      *Token
	refreshing *refreshCall
	refreshes  sync.WaitGroup
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

// Allegro errors. ErrAllegroRefused is the issue's stop-and-flag condition:
// the app is not allowed to read the catalog.
var (
	ErrAllegroNotAuthorized = errors.New("allegro not authorized: run `api allegro-login`")
	ErrAllegroRefused       = errors.New("allegro refused catalog access")
)

// tokenLeeway refreshes a token this long before it expires.
const tokenLeeway = time.Minute

// refreshTimeout bounds a token refresh and the write of its result.
const refreshTimeout = 30 * time.Second

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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.apiBase()+"/sale/products?"+q.Encode(), http.NoBody)
		if err != nil {
			return nil, fmt.Errorf("allegro: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.allegro.public.v1+json")
		req.Header.Set("Accept-Language", "pl-PL")
		req.Header.Set("Authorization", "Bearer "+tok)
		req.Header.Set("User-Agent", a.userAgent())
		_, body, err := get(a.client(), a.Name(), req, http.StatusOK)
		switch {
		case isStatus(err, http.StatusUnauthorized) && attempt == 0:
			continue
		case isStatus(err, http.StatusForbidden):
			return nil, fmt.Errorf("%w: %w", ErrAllegroRefused, err)
		case err != nil:
			return nil, err
		}
		var r allegroProducts
		if err := json.Unmarshal(body, &r); err != nil {
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

// accessToken returns a usable access token, refreshing it when it is about
// to expire or force is set. A refresh runs in its own goroutine apart from
// the caller's context: Allegro retires the old refresh token as soon as it
// answers, so a refresh cut short by a lookup's deadline would lose the new
// one and, after Allegro's grace period, the authorization. Concurrent
// callers share one refresh, and each stops waiting at its own deadline.
func (a *Allegro) accessToken(ctx context.Context, force bool) (string, error) {
	a.mu.Lock()
	if a.token == nil {
		t, ok, err := a.Store.Load(ctx, a.Name())
		if err != nil {
			a.mu.Unlock()
			return "", fmt.Errorf("allegro: load token: %w", err)
		}
		if !ok {
			a.mu.Unlock()
			return "", ErrAllegroNotAuthorized
		}
		a.token = &t
	}
	if a.refreshing == nil && !force && a.now().Add(tokenLeeway).Before(a.token.Expiry) {
		access := a.token.Access
		a.mu.Unlock()
		return access, nil
	}
	call := a.refreshing
	if call == nil {
		// A caller whose deadline passed while it waited for the lock
		// starts no refresh it cannot use.
		if err := ctx.Err(); err != nil {
			a.mu.Unlock()
			return "", fmt.Errorf("allegro: %w", err)
		}
		call = &refreshCall{done: make(chan struct{})}
		a.refreshing = call
		a.refreshes.Add(1)
		go a.refresh(context.WithoutCancel(ctx), call, a.token.Refresh)
	}
	a.mu.Unlock()
	select {
	case <-call.done:
		return call.access, call.err
	case <-ctx.Done():
		return "", fmt.Errorf("allegro: token refresh: %w", context.Cause(ctx))
	}
}

// refreshCall is one token refresh that concurrent callers share. access and
// err are set before done is closed.
type refreshCall struct {
	done   chan struct{}
	access string
	err    error
}

// refresh exchanges refreshToken for a new pair, bounded by refreshTimeout,
// and stores it. ctx carries no cancellation of the lookup that started it.
// The new pair replaces the one in memory even when the write fails, since
// Allegro no longer accepts the old refresh token.
func (a *Allegro) refresh(ctx context.Context, call *refreshCall, refreshToken string) {
	defer a.refreshes.Done()
	ctx, cancel := context.WithTimeout(ctx, refreshTimeout)
	defer cancel()
	access, err := recovery.Call(nil, "allegro token refresh panicked", func() (string, error) {
		q := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}
		t, err := a.tokenRequest(ctx, q)
		if err != nil {
			var oe *oauthError
			if errors.As(err, &oe) && oe.Code == "invalid_grant" {
				return "", fmt.Errorf("%w: refresh token rejected", ErrAllegroNotAuthorized)
			}
			return "", err
		}
		a.mu.Lock()
		a.token = &t
		a.mu.Unlock()
		if err := a.Store.Save(ctx, a.Name(), t); err != nil {
			return "", fmt.Errorf("allegro: save token: %w", err)
		}
		return t.Access, nil
	})
	a.mu.Lock()
	call.access, call.err = access, err
	a.refreshing = nil
	a.mu.Unlock()
	close(call.done)
}

// Wait blocks until every token refresh in flight has stored its result.
// Call it before closing the TokenStore's database, so a shutdown does not
// lose a token Allegro has already rotated. A nil Allegro has none.
func (a *Allegro) Wait() {
	if a == nil {
		return
	}
	a.refreshes.Wait()
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
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.authBase()+path+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return fmt.Errorf("allegro: %w", err)
	}
	req.SetBasicAuth(a.ClientID, a.ClientSecret)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", a.userAgent())
	status, body, err := get(a.client(), "allegro", req, http.StatusOK, http.StatusBadRequest, http.StatusUnauthorized)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		metrics.ObserveSource("allegro", "oauth_error")
		oe := &oauthError{}
		if json.Unmarshal(body, oe) != nil || oe.Code == "" {
			oe.Code = fmt.Sprintf("status %d", status)
		}
		return oe
	}
	if err := json.Unmarshal(body, dst); err != nil {
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
