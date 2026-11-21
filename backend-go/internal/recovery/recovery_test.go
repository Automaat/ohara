package recovery_test

import (
	"bytes"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/Automaat/ohara/backend-go/internal/recovery"
)

func TestCallReturnsResults(t *testing.T) {
	boom := errors.New("boom")
	v, err := recovery.Call(nil, "unused", func() (int, error) { return 7, boom })
	if v != 7 || !errors.Is(err, boom) {
		t.Fatalf("Call = %d, %v", v, err)
	}
}

func TestCallTurnsPanicIntoLoggedError(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	v, err := recovery.Call(logger, "source panicked", func() (int, error) {
		return len(third(strings.Fields(""))), nil
	}, "source", "olx")
	if v != 0 {
		t.Fatalf("v = %d, want zero", v)
	}
	pe, ok := errors.AsType[*recovery.PanicError](err)
	if !ok || !strings.Contains(err.Error(), "index out of range [2] with length 0") {
		t.Fatalf("err = %v, want a *PanicError for the index", err)
	}
	if pe.Value == nil {
		t.Fatal("PanicError.Value is nil")
	}
	logged := buf.String()
	for _, want := range []string{
		`"level":"ERROR"`, `"msg":"source panicked"`, `"source":"olx"`,
		`index out of range`, `"stack":"goroutine `, `recovery_test.TestCallTurnsPanicIntoLoggedError`,
	} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %s:\n%s", want, logged)
		}
	}
}

func TestCallRecoversInAGoroutine(t *testing.T) {
	var wg sync.WaitGroup
	var err error
	wg.Go(func() {
		_, err = recovery.Call(slog.New(slog.DiscardHandler), "panicked", func() (struct{}, error) {
			panic("bad answer")
		})
	})
	wg.Wait()
	if err == nil || err.Error() != "panic: bad answer" {
		t.Fatalf("err = %v", err)
	}
}

// third is a parser that expects at least three fields.
func third(fields []string) string { return fields[2] }
