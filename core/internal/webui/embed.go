// Package webui embeds the built React UI (ui/dist, copied here at build time) and
// serves it with SPA-style fallback. During plain `go build`/`go test` the dist tree
// may hold only .gitkeep; the handler degrades honestly rather than failing to compile.
package webui

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

func init() {
	// .webmanifest isn't in Go's default MIME table; register it so the PWA manifest is served as
	// application/manifest+json rather than content-sniffed to text/plain (qn.6a icons).
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// dist is populated by the build (ui/dist → core/internal/webui/dist). `all:` so that
// the embed still compiles when only the tracked .gitkeep placeholder is present.
//
//go:embed all:dist
var dist embed.FS

// assets is the dist subtree rooted so paths look like "index.html", "assets/...".
func assets() (fs.FS, error) { return fs.Sub(dist, "dist") }

// Built reports whether a real UI (index.html) was embedded at build time.
func Built() bool {
	sub, err := assets()
	if err != nil {
		return false
	}
	_, err = fs.Stat(sub, "index.html")
	return err == nil
}

// Handler serves the embedded UI. Unknown non-API paths fall back to index.html so the
// SPA router can handle them. When no UI was embedded it returns a plain-text notice
// (a build wired the placeholder only) instead of pretending to serve an app.
func Handler() http.Handler {
	sub, err := assets()
	if err != nil || !Built() {
		return notBuilt()
	}
	return handlerFor(sub)
}

// handlerFor is the testable core (Handler wires in the real embed.FS). Its cache policy is the
// standard SPA one, and it is load-bearing for the soak: index.html references content-HASHED
// assets, so index.html must NEVER be cached without revalidation (a stale one keeps pointing at old
// asset hashes → a deploy is invisible until the user clears their cache), while the hashed assets
// under assets/ are immutable and cached hard. Everything else revalidates.
func handlerFor(sub fs.FS) http.Handler {
	fileServer := http.FileServer(http.FS(sub))
	index, _ := fs.ReadFile(sub, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clean := strings.TrimPrefix(r.URL.Path, "/")
		if clean == "" {
			serveIndex(w, index)
			return
		}
		if _, statErr := fs.Stat(sub, clean); statErr == nil {
			if strings.HasPrefix(clean, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache") // favicon/manifest/etc. change on deploy
			}
			fileServer.ServeHTTP(w, r)
			return
		}
		serveIndex(w, index)
	})
}

func serveIndex(w http.ResponseWriter, index []byte) {
	// no-cache = the browser may store it but MUST revalidate before use, so a redeploy is picked up
	// on the next load instead of hiding behind a stale entry point (qn.6a soak fix).
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(index)
}

func notBuilt() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("quince: UI not embedded in this build (placeholder only). The API is live at /api/health.\n"))
	})
}
