package scanqueue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Automaat/ohara/backend-go/internal/dbtest"
	"github.com/Automaat/ohara/backend-go/internal/scanqueue"
)

const raceBarcode = "5901234123457"

// blockedFor reports whether done stays open for d, which means the
// operation behind it waits on the per-barcode lock.
func blockedFor(done <-chan struct{}, d time.Duration) bool {
	select {
	case <-done:
		return false
	case <-time.After(d):
		return true
	}
}

func queueSize(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM scan_queue`).Scan(&n); err != nil {
		t.Fatalf("count queue: %v", err)
	}
	return n
}

func TestQueueWaitsForUncommittedEdition(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)
	store := scanqueue.NewStore(pool)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `WITH t AS (INSERT INTO title (kind, name) VALUES ('movie', 'Rejs') RETURNING id)
		INSERT INTO edition (title_id, barcode, format) SELECT id, $1, 'dvd' FROM t`, raceBarcode); err != nil {
		t.Fatalf("insert edition: %v", err)
	}

	done := make(chan struct{})
	var addErr error
	go func() {
		defer close(done)
		_, _, addErr = store.Add(ctx, raceBarcode, scanqueue.ReasonNotFound, "")
	}()
	if !blockedFor(done, 300*time.Millisecond) {
		t.Fatalf("Add finished before the edition committed: %v", addErr)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	<-done
	if !errors.Is(addErr, scanqueue.ErrInCollection) {
		t.Fatalf("Add after edition commit = %v, want ErrInCollection", addErr)
	}
	if n := queueSize(t, pool); n != 0 {
		t.Fatalf("queue has %d entries, want 0", n)
	}
}

func TestEditionWaitsForUncommittedQueueEntry(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.Migrated(t)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT scan_queue_lock($1)`, raceBarcode); err != nil {
		t.Fatalf("lock: %v", err)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO scan_queue (barcode, reason) VALUES ($1, 'not_found')`, raceBarcode); err != nil {
		t.Fatalf("queue: %v", err)
	}

	done := make(chan struct{})
	var editionErr error
	go func() {
		defer close(done)
		_, editionErr = pool.Exec(ctx, `WITH t AS (INSERT INTO title (kind, name) VALUES ('movie', 'Rejs') RETURNING id)
			INSERT INTO edition (title_id, barcode, format) SELECT id, $1, 'dvd' FROM t`, raceBarcode)
	}()
	if !blockedFor(done, 300*time.Millisecond) {
		t.Fatalf("edition insert finished before the queue entry committed: %v", editionErr)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	<-done
	if editionErr != nil {
		t.Fatalf("insert edition: %v", editionErr)
	}
	if n := queueSize(t, pool); n != 0 {
		t.Fatalf("queue has %d entries after the edition committed, want 0", n)
	}
}
