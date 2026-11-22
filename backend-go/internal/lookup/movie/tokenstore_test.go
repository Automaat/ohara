package movie_test

import (
	"context"
	"testing"
	"time"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/lookup/movie"
)

func TestPGTokenStore(t *testing.T) {
	store := movie.PGTokenStore{Pool: dbtest.Migrated(t)}
	ctx := context.Background()
	if _, ok, err := store.Load(ctx, "allegro"); ok || err != nil {
		t.Fatalf("empty load = %v, %v", ok, err)
	}
	exp := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	for _, tok := range []movie.Token{{Access: "a1", Refresh: "r1", Expiry: exp}, {Access: "a2", Refresh: "r2", Expiry: exp}} {
		if err := store.Save(ctx, "allegro", tok); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := store.Load(ctx, "allegro")
	if err != nil || !ok || got.Access != "a2" || got.Refresh != "r2" || !got.Expiry.Equal(exp) {
		t.Fatalf("load = %+v, %v, %v", got, ok, err)
	}
	if _, ok, _ := store.Load(ctx, "other"); ok {
		t.Fatal("token leaked across providers")
	}
}

func TestPGTokenStoreSwap(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := movie.PGTokenStore{Pool: pool}
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	pair := func(n string) movie.Token { return movie.Token{Access: "a" + n, Refresh: "r" + n, Expiry: exp} }
	steps := []struct {
		name     string
		expected []string
		swap     movie.Token
		want     movie.Token
		swapped  bool
	}{
		{"empty store", []string{"r0"}, pair("1"), pair("1"), true},
		{"stored pair expected", []string{"r0", "r1"}, pair("2"), pair("2"), true},
		{"stored pair from another process", []string{"r1"}, pair("3"), pair("2"), false},
	}
	for _, s := range steps {
		got, swapped, err := store.Swap(ctx, "allegro", s.expected, s.swap)
		if err != nil || swapped != s.swapped || got != s.want {
			t.Fatalf("%s: swap = %+v, %v, %v", s.name, got, swapped, err)
		}
		stored, _, err := store.Load(ctx, "allegro")
		if err != nil || stored.Access != s.want.Access || stored.Refresh != s.want.Refresh || !stored.Expiry.Equal(exp) {
			t.Fatalf("%s: stored %+v, %v", s.name, stored, err)
		}
	}
	if _, ok, _ := store.Load(ctx, "other"); ok {
		t.Fatal("token leaked across providers")
	}
}

// A login that holds the row when the refresh writes wins once it commits.
func TestPGTokenStoreSwapWaitsForAConcurrentLogin(t *testing.T) {
	pool := dbtest.Migrated(t)
	store := movie.PGTokenStore{Pool: pool}
	ctx := context.Background()
	exp := time.Now().Add(time.Hour).Truncate(time.Microsecond)
	if err := store.Save(ctx, "allegro", movie.Token{Access: "a0", Refresh: "r0", Expiry: exp}); err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE oauth_token SET access_token = 'login', refresh_token = 'login' WHERE provider = 'allegro'`); err != nil {
		t.Fatal(err)
	}
	type result struct {
		tok     movie.Token
		swapped bool
		err     error
	}
	done := make(chan result, 1)
	go func() {
		tok, swapped, err := store.Swap(ctx, "allegro", []string{"r0"}, movie.Token{Access: "a1", Refresh: "r1", Expiry: exp})
		done <- result{tok, swapped, err}
	}()
	select {
	case r := <-done:
		t.Fatalf("swap did not wait for the login: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	r := <-done
	if r.err != nil || r.swapped || r.tok.Refresh != "login" {
		t.Fatalf("swap = %+v", r)
	}
	if got, _, _ := store.Load(ctx, "allegro"); got.Refresh != "login" {
		t.Fatalf("stored %+v, want the login's pair", got)
	}
}
