package valuation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeShop mimics the SkupSzop sell flow: the sell page sets a session and
// a CSRF token, POST adds an ISBN to that session's cart, and the sell page
// then shows the cart.
type fakeShop struct {
	t       *testing.T
	reply   string
	cart    []byte
	empty   []byte
	status  int
	noToken bool

	mu       sync.Mutex
	added    map[string]bool
	posts    int
	sessions int
}

func newFakeShop(t *testing.T) *fakeShop {
	t.Helper()
	read := func(name string) []byte {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	return &fakeShop{
		t: t, reply: `{"response":null}`, cart: read("skupszop-cart.html"),
		empty: read("skupszop-empty.html"), added: map[string]bool{},
	}
}

func (f *fakeShop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/sprzedaj" {
		http.NotFound(w, r)
		return
	}
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	session, err := r.Cookie("skupszop_session")
	switch r.Method {
	case http.MethodGet:
		if err != nil {
			f.sessions++
			http.SetCookie(w, &http.Cookie{Name: "skupszop_session", Value: strconv.Itoa(f.sessions), Path: "/"})
			_, _ = w.Write(f.page(f.empty))
			return
		}
		if f.added[session.Value] {
			_, _ = w.Write(f.page(f.cart))
			return
		}
		_, _ = w.Write(f.page(f.empty))
	case http.MethodPost:
		f.posts++
		if err != nil || r.Header.Get("X-CSRF-TOKEN") != "test-csrf-token" ||
			r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
			w.WriteHeader(419)
			return
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("isbn") == "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		if f.reply == `{"response":null}` {
			f.added[session.Value] = true
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.reply))
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakeShop) page(b []byte) []byte {
	if f.noToken {
		return []byte(strings.ReplaceAll(string(b), `name="csrf-token"`, `name="other"`))
	}
	return b
}

func TestSkupSzopQuote(t *testing.T) {
	notInterested, err := os.ReadFile("testdata/skupszop-not-interested.json")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		isbn    string
		setup   func(*fakeShop)
		want    Quote
		wantErr string
	}{
		{
			name: "offer from the cart",
			isbn: "9788308074770",
			want: Quote{Offered: true, Cents: 732, Title: "Droga do siebie. O poczuciu wartości"},
		},
		{
			name: "second cart row",
			isbn: "9788308073209",
			want: Quote{Offered: true, Cents: 537, Title: "Katedra"},
		},
		{
			name:  "shop does not buy the book",
			isbn:  "9788375780635",
			setup: func(f *fakeShop) { f.reply = string(notInterested) },
			want:  Quote{},
		},
		{
			name:    "other shop message is an error",
			isbn:    "9788375780635",
			setup:   func(f *fakeShop) { f.reply = `{"response":"<div>Niepoprawny numer ISBN</div>"}` },
			wantErr: "skupszop: add 9788375780635: Niepoprawny numer ISBN",
		},
		{
			name:    "added ISBN missing from the cart",
			isbn:    "9780000000002",
			wantErr: "skupszop: cart has no offer for 9780000000002",
		},
		{
			name:    "sell page without a token",
			isbn:    "9788308074770",
			setup:   func(f *fakeShop) { f.noToken = true },
			wantErr: "skupszop: sell page has no CSRF token",
		},
		{
			name:    "shop down",
			isbn:    "9788308074770",
			setup:   func(f *fakeShop) { f.status = http.StatusServiceUnavailable },
			wantErr: "skupszop: sell page: status 503",
		},
		{
			name:    "undecodable reply",
			isbn:    "9788308074770",
			setup:   func(f *fakeShop) { f.reply = `<html>` },
			wantErr: "skupszop: add 9788308074770: decode:",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			shop := newFakeShop(t)
			if tt.setup != nil {
				tt.setup(shop)
			}
			srv := httptest.NewServer(shop)
			t.Cleanup(srv.Close)
			s := SkupSzop{Client: srv.Client(), BaseURL: srv.URL + "/"}
			got, err := s.Quote(context.Background(), tt.isbn)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Quote err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Quote: %v", err)
			}
			tt.want.URL = srv.URL + "/sprzedaj"
			if got != tt.want {
				t.Fatalf("Quote = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSkupSzopSessionsAreFresh(t *testing.T) {
	shop := newFakeShop(t)
	srv := httptest.NewServer(shop)
	t.Cleanup(srv.Close)
	s := SkupSzop{Client: srv.Client(), BaseURL: srv.URL}
	for range 2 {
		if _, err := s.Quote(context.Background(), "9788308074770"); err != nil {
			t.Fatalf("Quote: %v", err)
		}
	}
	if shop.posts != 2 || shop.sessions != 2 {
		t.Fatalf("posts = %d, sessions = %d, want 2 each", shop.posts, shop.sessions)
	}
	if s.Client.Jar != nil {
		t.Fatal("Quote must not give the shared client a cookie jar")
	}
}

func TestParseCartIgnoresRowWithoutPrice(t *testing.T) {
	page := []byte(`<div class="buy-cart-item" data-isbn="9788308074770"><p class="text__heading">X</p></div>`)
	if _, ok := parseCart(page, "9788308074770"); ok {
		t.Fatal("a row without a price must not quote")
	}
	page = []byte(`<div class="buy-cart-item" data-isbn="9788308074770"><div class="price"><span class="value">abc</span></div>`)
	if _, ok := parseCart(page, "9788308074770"); ok {
		t.Fatal("an unreadable price must not quote")
	}
}
