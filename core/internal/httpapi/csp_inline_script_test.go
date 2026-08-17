package httpapi

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// inlineScript matches an inline `<script>` — one with NO attributes, so vite's injected
// `<script type="module" src=…>` tags are not candidates. A CSP hash covers the element's exact
// text content, which is what the capture is.
var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// THE CSP MUST ADMIT EVERY INLINE SCRIPT THE UI SHIPS, AND THIS IS THE TEST THAT MAKES A MISMATCH
// LOUD.
//
// The failure it guards against is invisible from inside this repo's other gates: vite's dev server
// sends no CSP and neither does the vitest environment, so a blocked inline script runs everywhere a
// test looks and is refused only by the real daemon in front of a real browser. quince#1074's
// first-paint theme script shipped that way — the page still rendered, every suite stayed green, and
// the only evidence was a console message.
//
// IT RECOMPUTES RATHER THAN COMPARING TO A SECOND COPY of the hash. A constant checked against a
// constant proves nothing; this reads the file the browser gets and derives the value the browser
// will derive.
//
// `ui/index.html` IS THE ARTIFACT. Vite copies it through and injects bundle tags — it does not
// reformat or minify the inline script — so its bytes are the bytes served. Verified against a
// running daemon: the hash computed here equalled the one Chrome named in its refusal.
func TestTheCSPAdmitsEveryInlineScriptTheUIShips(t *testing.T) {
	// FOUR LEVELS UP FROM core/internal/httpapi. A test that cannot find the file must FAIL rather
	// than skip: skipping is how this check would quietly stop running.
	path := filepath.Join("..", "..", "..", "ui", "index.html")
	html, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — this test needs the shipped index.html", path, err)
	}

	rec := httptest.NewRecorder()
	securityHeaders(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy header")
	}

	found := inlineScript.FindAllSubmatch(html, -1)
	if len(found) == 0 {
		// NOT A PASS. If the inline script is gone the hash in the CSP is dead weight and the
		// comment above it describes something that no longer exists.
		t.Fatal("no inline <script> in ui/index.html — remove the hash from the CSP and this test")
	}

	for _, m := range found {
		sum := sha256.Sum256(m[1])
		want := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		if !regexp.MustCompile(regexp.QuoteMeta(want)).MatchString(csp) {
			t.Errorf("the CSP does not admit an inline script in ui/index.html.\n"+
				"  put this in script-src: %s\n  csp is: %s", want, csp)
		}
	}

	// AND 'unsafe-inline' IS NEVER THE FIX. It admits every inline script including one an injection
	// plants, which is the whole reason a hash is worth the bookkeeping this test exists to do.
	if regexp.MustCompile(`script-src[^;]*'unsafe-inline'`).MatchString(csp) {
		t.Error("script-src carries 'unsafe-inline' — a hash admits the one script quince ships")
	}
}
