package server

import (
	"compress/gzip"
	"io"
	"mime"
	"net/http"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
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

// gzipWriters reuses writers at the best level, which keeps the 946 KB
// decoder .wasm near 412 KB; the level 6 default leaves it at 429 KB.
var gzipWriters = sync.Pool{New: func() any {
	zw, err := gzip.NewWriterLevel(io.Discard, gzip.BestCompression)
	if err != nil {
		return nil
	}
	return zw
}}

// compress gzips a response of a compressible type for a client that accepts
// gzip. Every answer carries Vary: Accept-Encoding, the plain ones and 304s
// too, so no cache hands one encoding to a client that asked for the other.
// A response that already has a Content-Encoding, like /metrics' own gzip, is
// left alone. A client that accepts gzip gets the whole resource for a Range
// request on a path that may be compressed: a range of the plain bytes
// appended to a gzip body it already has would corrupt it, and a range of the
// gzip body would differ between two compressions. A path whose extension
// names a type that is never compressed, like a PNG, keeps its range. A
// compressed answer drops Accept-Ranges and weakens an ETag, since its bytes
// are not the plain resource's; a 304 that revalidates such a weak ETag keeps
// it weak. A client without gzip gets plain bodies and its ranges as before.
func compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if !acceptsGzip(r.Header.Values("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		if (r.Header.Get("Range") != "" || r.Header.Get("If-Range") != "") && mayCompress(r.URL.Path) {
			r = r.Clone(r.Context())
			r.Header.Del("Range")
			r.Header.Del("If-Range")
		}
		gw := &gzipResponseWriter{
			ResponseWriter: w,
			head:           r.Method == http.MethodHead,
			ifNoneMatch:    r.Header.Values("If-None-Match"),
		}
		defer gw.close()
		next.ServeHTTP(gw, r)
	})
}

// mayCompress reports whether the answer for a path may be compressed: false
// only when its extension names a known type outside compressibleTypes. SPA
// routes have no extension and get index.html, which is compressed.
func mayCompress(urlPath string) bool {
	ext := path.Ext(urlPath)
	if ext == "" {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(mime.TypeByExtension(ext))
	return err != nil || slices.Contains(compressibleTypes, mediaType)
}

// acceptsGzip reports whether the Accept-Encoding values allow gzip: gzip or
// x-gzip with a nonzero quality, or, when neither is named, * with a nonzero
// quality.
func acceptsGzip(values []string) bool {
	named, star := false, false
	for _, v := range values {
		for item := range strings.SplitSeq(v, ",") {
			coding, params, _ := strings.Cut(item, ";")
			coding = strings.ToLower(strings.TrimSpace(coding))
			ok := quality(params) > 0
			switch coding {
			case "gzip", "x-gzip":
				if ok {
					return true
				}
				named = true
			case "*":
				star = star || ok
			}
		}
	}
	return !named && star
}

// quality reads the q parameter of one Accept-Encoding item; without one, or
// with one that does not parse, the coding counts as acceptable.
func quality(params string) float64 {
	for p := range strings.SplitSeq(params, ";") {
		name, value, _ := strings.Cut(strings.TrimSpace(p), "=")
		if !strings.EqualFold(strings.TrimSpace(name), "q") {
			continue
		}
		q, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil {
			return 1
		}
		return q
	}
	return 1
}

// gzipResponseWriter decides on the first WriteHeader or Write whether the
// body is compressed, then sends it through a pooled gzip writer.
type gzipResponseWriter struct {
	http.ResponseWriter
	head        bool
	ifNoneMatch []string
	wroteHeader bool
	encoded     bool
	zw          *gzip.Writer
}

func (g *gzipResponseWriter) WriteHeader(code int) {
	if g.wroteHeader {
		g.ResponseWriter.WriteHeader(code)
		return
	}
	g.wroteHeader = true
	if g.compressible(code) {
		g.startGzip()
	} else if code == http.StatusNotModified {
		g.keepWeakETag()
	}
	g.ResponseWriter.WriteHeader(code)
}

// keepWeakETag weakens a 304's strong ETag when the client revalidates the
// weak one a compressed answer gave it, so its stored validator stays the same.
func (g *gzipResponseWriter) keepWeakETag() {
	h := g.Header()
	etag := h.Get("ETag")
	if etag == "" || strings.HasPrefix(etag, "W/") {
		return
	}
	weak := "W/" + etag
	for _, v := range g.ifNoneMatch {
		for tag := range strings.SplitSeq(v, ",") {
			if strings.TrimSpace(tag) == weak {
				h.Set("ETag", weak)
				return
			}
		}
	}
}

// startGzip marks the response as gzip and, unless it answers a HEAD, takes
// the writer its body goes through. A HEAD answer's body is dropped, so
// net/http does not size the header from the plain bytes.
func (g *gzipResponseWriter) startGzip() {
	if !g.head {
		zw, ok := gzipWriters.Get().(*gzip.Writer)
		if !ok || zw == nil {
			return
		}
		zw.Reset(g.ResponseWriter)
		g.zw = zw
	}
	g.encoded = true
	h := g.Header()
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	h.Del("Accept-Ranges")
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("ETag", "W/"+etag)
	}
}

// compressible reports a response with a body of a compressible type and no
// encoding of its own.
func (g *gzipResponseWriter) compressible(code int) bool {
	if code < http.StatusOK || code == http.StatusNoContent || code == http.StatusNotModified ||
		code == http.StatusPartialContent {
		return false
	}
	h := g.Header()
	if h.Get("Content-Encoding") != "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && slices.Contains(compressibleTypes, mediaType)
}

func (g *gzipResponseWriter) Write(b []byte) (int, error) {
	if !g.wroteHeader {
		g.WriteHeader(http.StatusOK)
	}
	if g.zw != nil {
		return g.zw.Write(b)
	}
	if g.encoded {
		return len(b), nil
	}
	return g.ResponseWriter.Write(b)
}

// Flush sends what the gzip writer holds, then flushes the connection.
func (g *gzipResponseWriter) Flush() {
	if g.zw != nil {
		_ = g.zw.Flush()
	}
	_ = http.NewResponseController(g.ResponseWriter).Flush()
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (g *gzipResponseWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

// close ends the gzip stream and returns its writer to the pool.
func (g *gzipResponseWriter) close() {
	if g.zw == nil {
		return
	}
	_ = g.zw.Close()
	g.zw.Reset(io.Discard)
	gzipWriters.Put(g.zw)
	g.zw = nil
}
