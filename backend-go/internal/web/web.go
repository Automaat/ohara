// Package web serves the built single-page frontend.
package web

import (
	"errors"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const indexFile = "index.html"

// Handler serves files from fsys and answers every other GET or HEAD with
// index.html, so client-side routes survive a reload. Hashed build assets
// under _app/immutable/ are cached for a year; everything else revalidates,
// so a new deploy reaches the reader on the next load.
func Handler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" || !isFile(fsys, name) {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFileFS(w, r, fsys, indexFile)
			return
		}
		if strings.HasPrefix(name, "_app/immutable/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

func isFile(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// ErrNoIndex reports a web directory without index.html, which would make
// every page a 404.
var ErrNoIndex = errors.New("web directory has no index.html")

// Check verifies that fsys holds the frontend entry point.
func Check(fsys fs.FS) error {
	if !isFile(fsys, indexFile) {
		return ErrNoIndex
	}
	return nil
}
