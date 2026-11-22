package server

import (
	"compress/gzip"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
)

// compressibleTypes are the response types the router compresses: the SPA's
// HTML, scripts, styles, SVG icons, web app manifest and barcode decoder
// WebAssembly, and the API's JSON. Images other than SVG are compressed
// already, so covers and PNG icons go out as they are.
var compressibleTypes = []string{
	"text/html",
	"text/css",
	"text/plain",
	"text/javascript",
	"application/javascript",
	"application/json",
	"application/manifest+json",
	"application/wasm",
	"image/svg+xml",
}

// compress gzips (or deflates) a response of a compressible type for a client
// that accepts it, adding Vary: Accept-Encoding; a response that already has a
// Content-Encoding, like /metrics' own gzip, is left alone. The best level
// keeps the 946 KB decoder .wasm near 412 KB; the level 6 default leaves it at
// 429 KB. A request with a Range header gets the plain body, so a 206 answer's
// Content-Range keeps describing the bytes it carries.
func compress() func(http.Handler) http.Handler {
	compressor := middleware.NewCompressor(gzip.BestCompression, compressibleTypes...)
	return func(next http.Handler) http.Handler {
		compressed := compressor.Handler(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Range") != "" {
				next.ServeHTTP(w, r)
				return
			}
			compressed.ServeHTTP(w, r)
		})
	}
}
