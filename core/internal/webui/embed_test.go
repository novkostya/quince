package webui

import (
	"net/http"
	"net/http/httptest"
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
