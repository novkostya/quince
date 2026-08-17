package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// The SPA cache policy is load-bearing for the soak (qn.6a): index.html must revalidate every load so
// a redeploy is picked up, while content-hashed assets are immutable and cached hard. A stale
// index.html would keep pointing at old asset hashes and hide a deploy until a manual cache clear.
func TestHandlerCachePolicy(t *testing.T) {
	sub := fstest.MapFS{
		"index.html":             {Data: []byte("<!doctype html><html></html>")},
		"assets/index-abc123.js": {Data: []byte("console.log(1)")},
		"favicon.svg":            {Data: []byte("<svg/>")},
	}
	h := handlerFor(sub)

	cases := []struct {
		path      string
		wantCache string
		wantHTML  bool
	}{
		{"/", "no-cache", true},            // index.html
		{"/devices/123", "no-cache", true}, // SPA fallback → index.html
		{"/assets/index-abc123.js", "public, max-age=31536000, immutable", false}, // hashed asset
		{"/favicon.svg", "no-cache", false},                                       // unhashed static file
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if got := rec.Header().Get("Cache-Control"); got != c.wantCache {
			t.Errorf("%s Cache-Control = %q, want %q", c.path, got, c.wantCache)
		}
		if c.wantHTML && rec.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s Content-Type = %q, want text/html", c.path, rec.Header().Get("Content-Type"))
		}
	}
}

// qn.12 G4 — THE SERVICE WORKER IS REALLY THERE, AND IS REALLY JAVASCRIPT.
//
// `handlerFor` falls through to `serveIndex` for any path that does not `fs.Stat`, with
// `Content-Type: text/html`. So a build that failed to copy `sw.js` into dist does not 404 — it
// serves the SPA's index.html, and registration fails with a MIME error that names neither the
// missing file nor the fallback. Every test in this package would still pass, and notifications
// would silently never arrive.
//
// TWO ASSERTIONS, because either alone is satisfiable by the bug: that the response is not the SPA
// shell, and that its content type is JavaScript.
func TestTheServiceWorkerIsServedAsJavaScript(t *testing.T) {
	fsys := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html>the SPA</html>")},
		"sw.js":      &fstest.MapFile{Data: []byte("self.addEventListener('push', () => {})")},
	}
	rec := httptest.NewRecorder()
	handlerFor(fsys).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sw.js", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /sw.js = %d, want 200", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "the SPA") {
		t.Fatalf("GET /sw.js served index.html — the SPA fallback swallowed a missing worker")
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("GET /sw.js content type is %q; a service worker served as anything else fails to register", ct)
	}
}

// AND THE FALLBACK IS WHAT HAPPENS WHEN IT IS ABSENT, asserted so the test above is known to be
// testing something. If a missing sw.js 404'd, G4 would be unnecessary; it does not, which is why
// it is.
func TestAMissingServiceWorkerFallsThroughToTheSPA(t *testing.T) {
	fsys := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<html>the SPA</html>")}}
	rec := httptest.NewRecorder()
	handlerFor(fsys).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sw.js", nil))
	if !strings.Contains(rec.Body.String(), "the SPA") {
		t.Fatalf("a missing /sw.js no longer falls through to index.html — G4's premise has changed, "+
			"and the gate above should be re-read rather than trusted. got %d %q", rec.Code, rec.Body.String())
	}
}
