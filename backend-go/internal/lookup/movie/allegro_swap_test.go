package movie

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
)

// swapStore is a memStore with the conditional save PGTokenStore has.
type swapStore struct{ *memStore }

func (s swapStore) Swap(ctx context.Context, _ string, expected []string, t Token) (Token, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Token{}, false, fmt.Errorf("write oauth token: %w", err)
	}
	if s.failSaves > 0 {
		s.failSaves--
		return Token{}, false, errors.New("write oauth token: connection reset")
	}
	if s.token != nil && !slices.Contains(expected, s.token.Refresh) {
		return *s.token, false, nil
	}
	s.token = &t
	s.saves++
	return t, true, nil
}

type swappingStore interface {
	TokenStore
	TokenSwapper
}

// loginBeforeSwap runs login once before the first Swap: the login lands
// after the refresh last read the store and before it writes.
type loginBeforeSwap struct {
	swappingStore
	once  sync.Once
	login func()
}

func (s *loginBeforeSwap) Swap(ctx context.Context, provider string, expected []string, t Token) (Token, bool, error) {
	s.once.Do(s.login)
	return s.swappingStore.Swap(ctx, provider, expected, t)
}

func TestAllegroRefreshKeepsAConcurrentLogin(t *testing.T) {
	stores := map[string]func(t *testing.T, initial Token) swappingStore{
		"memory": func(_ *testing.T, initial Token) swappingStore {
			return swapStore{&memStore{token: &initial}}
		},
		"postgres": func(t *testing.T, initial Token) swappingStore {
			t.Helper()
			s := PGTokenStore{Pool: dbtest.Migrated(t)}
			if err := s.Save(context.Background(), "allegro", initial); err != nil {
				t.Fatal(err)
			}
			return s
		},
	}
	for name, newStore := range stores {
		t.Run(name, func(t *testing.T) {
			now := time.Now().Truncate(time.Microsecond)
			inner := newStore(t, Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(30 * time.Second)})
			srv := &allegroServer{}
			a := newAllegro(t, srv, &memStore{}, now)
			store := &loginBeforeSwap{swappingStore: inner}
			store.login = func() {
				if err := otherProcess(a).Login(context.Background(), &bytes.Buffer{}); err != nil {
					t.Error(err)
				}
			}
			a.Store = store
			for i := range 2 {
				if _, err := a.Products(context.Background(), "5906190326195"); err != nil {
					t.Fatalf("lookup %d: %v", i, err)
				}
			}
			stored, _, err := inner.Load(context.Background(), "allegro")
			if err != nil || stored.Refresh != refreshFor(2) {
				t.Fatalf("stored %+v, %v; want the login's pair", stored, err)
			}
			if n := srv.refreshCount(); n != 2 {
				t.Fatalf("token grants = %d, want the refresh and the login", n)
			}
		})
	}
}

func TestAllegroSwapRetryKeepsAPairALoginStored(t *testing.T) {
	now := time.Now()
	mem := &memStore{token: &Token{Access: accessFor(0), Refresh: refreshFor(0), Expiry: now.Add(30 * time.Second)}, failSaves: 1}
	srv := &allegroServer{}
	a := newAllegro(t, srv, mem, now)
	a.Store = swapStore{mem}
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil {
		t.Fatal(err)
	}
	if err := otherProcess(a).Login(context.Background(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Products(context.Background(), "5906190326195"); err != nil {
		t.Fatal(err)
	}
	if got := mem.refresh(); got != refreshFor(3) {
		t.Fatalf("stored refresh token %q, want the login's pair refreshed", got)
	}
}
