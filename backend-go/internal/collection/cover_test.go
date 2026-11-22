package collection

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
)

type interruptedUpload struct{}

func (interruptedUpload) Read([]byte) (int, error) {
	return 0, os.ErrDeadlineExceeded
}

func TestInterruptedCoverUpload(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/editions/1/cover", interruptedUpload{})
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	NewHandler(nil, nil).Mount(router)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "try again") {
		t.Fatalf("interrupted upload returned %d: %s", rec.Code, rec.Body.String())
	}
}

func smallPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// holdDecodeSlot takes the process's cover decode slot until the test ends.
func holdDecodeSlot(t *testing.T) {
	t.Helper()
	coverDecodeSlot <- struct{}{}
	t.Cleanup(func() { <-coverDecodeSlot })
}

func TestCoverDecodeWaitsForTheSlot(t *testing.T) {
	photo := smallPNG(t)
	holdDecodeSlot(t)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	valid, err := validCover(ctx, photo)
	if !errors.Is(err, context.Canceled) || valid {
		t.Fatalf("decode with the slot taken = %v, %v; want it to wait until the context ends", valid, err)
	}
}

func TestCoverDecodeRunsOnceTheSlotIsFree(t *testing.T) {
	photo := smallPNG(t)
	coverDecodeSlot <- struct{}{}
	done := make(chan error, 1)
	go func() {
		valid, err := validCover(t.Context(), photo)
		if err == nil && !valid {
			err = errors.New("valid PNG rejected")
		}
		done <- err
	}()
	<-coverDecodeSlot
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(coverDecodeSlot) != 0 {
		t.Fatal("decode kept the slot")
	}
}

// replaceDecode swaps decodeImage for the test's duration.
func replaceDecode(t *testing.T, decode func(io.Reader) (image.Image, string, error)) {
	t.Helper()
	original := decodeImage
	decodeImage = decode
	t.Cleanup(func() { decodeImage = original })
}

func TestCoverDecodeSkipsAnEndedRequestWithTheSlotFree(t *testing.T) {
	photo := smallPNG(t)
	decodes := 0
	replaceDecode(t, func(r io.Reader) (image.Image, string, error) {
		decodes++
		return image.Decode(r)
	})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for range 100 {
		valid, err := validCover(ctx, photo)
		if !errors.Is(err, context.Canceled) || valid {
			t.Fatalf("ended request = %v, %v; want context.Canceled", valid, err)
		}
	}
	if decodes != 0 || len(coverDecodeSlot) != 0 {
		t.Fatalf("ended requests decoded %d times, slot holds %d", decodes, len(coverDecodeSlot))
	}
}

func TestCoverDecodeKeepsTheSlotWhileItRuns(t *testing.T) {
	photo := smallPNG(t)
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	replaceDecode(t, func(r io.Reader) (image.Image, string, error) {
		entered <- struct{}{}
		<-release
		return image.Decode(r)
	})

	first := make(chan error, 1)
	go func() {
		valid, err := validCover(t.Context(), photo)
		if err == nil && !valid {
			err = errors.New("valid PNG rejected")
		}
		first <- err
	}()
	<-entered

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	second := make(chan error, 1)
	go func() {
		_, err := validCover(ctx, photo)
		second <- err
	}()
	select {
	case err := <-second:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("second decode during the first = %v; want it to wait until its deadline", err)
		}
	case <-entered:
		t.Error("second decode started while the first was running")
	case <-time.After(5 * time.Second):
		t.Error("second decode neither waited nor finished")
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

func TestCoverHeaderCheckSkipsTheQueue(t *testing.T) {
	holdDecodeSlot(t)
	for name, photo := range map[string][]byte{
		"not an image": []byte("GIF89a"),
		"truncated":    smallPNG(t)[:20],
		"too large":    pngHeader(t, 4001, 4000),
	} {
		valid, err := validCover(t.Context(), photo)
		if valid || err != nil {
			t.Errorf("%s = %v, %v; want rejected without waiting", name, valid, err)
		}
	}
}

func pngHeader(t *testing.T, width, height int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCoverUploadLeftWaitingWritesNothing(t *testing.T) {
	holdDecodeSlot(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequestWithContext(ctx, http.MethodPut, "/editions/1/cover", bytes.NewReader(smallPNG(t)))
	rec := httptest.NewRecorder()
	router := chi.NewRouter()
	NewHandler(nil, nil).Mount(router)
	router.ServeHTTP(rec, req)
	if rec.Body.Len() != 0 || len(rec.Header()) != 0 {
		t.Fatalf("upload whose client left wrote %v: %s", rec.Header(), rec.Body.String())
	}
}
