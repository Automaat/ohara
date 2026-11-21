// Package recovery turns a panic into an error in goroutines Ohara starts
// outside a request handler, where the router's recoverer does not reach. A
// third-party answer that trips a parser then fails its provider, source
// or run instead of ending the process.
package recovery

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
)

// PanicError is a recovered panic.
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string { return fmt.Sprintf("panic: %v", e.Value) }

// Call runs fn and returns its results. A panic in fn is logged at error
// level under msg and attrs, with the panic value and its stack, and
// returned as a *PanicError next to T's zero value. fn counts as panicking
// whenever it does not return, so panic(nil) fails it even under
// GODEBUG=panicnil=1. A nil logger logs to slog.Default().
func Call[T any](logger *slog.Logger, msg string, fn func() (T, error), attrs ...any) (T, error) {
	var (
		v        T
		err      error
		returned bool
	)
	func() {
		defer func() {
			r := recover()
			if returned {
				return
			}
			if logger == nil {
				logger = slog.Default()
			}
			logger.Error(msg, slices.Concat(attrs, []any{"panic", r, "stack", string(debug.Stack())})...)
			var zero T
			v, err = zero, &PanicError{Value: r}
		}()
		v, err = fn()
		returned = true
	}()
	return v, err
}
