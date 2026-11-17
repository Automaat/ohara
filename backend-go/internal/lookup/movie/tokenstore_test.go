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
