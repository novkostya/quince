package httpapi

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// A PLAIN-HTTP PAGE CAN COMPLETE A DOUBLE SUBMIT — Operator ruling 2026-08-17, quince#1156.
//
// THIS DRIVES THE ROUTER RATHER THAN ASKING A PREDICATE, which is what the ruling requires and what
// the bug demanded: `Secure(r)` returning the right boolean proves nothing about whether a browser
// can complete the ceremony. The sequence below is the browser's — a GET that mints the cookie, then
// a mutation carrying that cookie and the matching header.
//
// THE FAILURE IT PINS is not "the cookie is missing". Before the ruling the cookie arrived stamped
// `Secure` on a plain-http origin, which a browser never sends back and never exposes to
// `document.cookie` — so the page had nothing to echo and every CSRF-guarded route answered 403.
// Measured in Chrome, Firefox and WebKit before it was fixed.
func TestAPlainHTTPPageCanCompleteTheDoubleSubmit(t *testing.T) {
	router := NewRouter(testDeps(t))

	// A NON-LOOPBACK HOST, deliberately. Plain-http loopback has never carried the flag at all, so it
	// would pass this test without the fix and prove nothing.
	const origin = "http://192.0.2.10:8968"

	warm := httptest.NewRecorder()
	router.ServeHTTP(warm, httptest.NewRequest(http.MethodGet, origin+"/api/auth/status", nil))

	var csrf *http.Cookie
	for _, c := range warm.Result().Cookies() {
		if c.Name == auth.CSRFCookieName {
			csrf = c
		}
	}
	if csrf == nil {
		t.Fatal("no CSRF cookie was minted on a plain-http origin")
	}
	// THE ASSERTION THE WHOLE RULING TURNS ON. A real browser drops this silently; httptest keeps it,
	// so the flag has to be read directly or the test would pass on a cookie no client could use.
	if csrf.Secure {
		t.Error("the CSRF cookie is Secure on a plain-http origin — a browser will not send it back, " +
			"and every CSRF-guarded mutation from that page is refused")
	}

	// THE CONSEQUENCE, END TO END: the mutation the certificate step performs, with exactly what a
	// browser would have to work with.
	req := httptest.NewRequest(http.MethodPost, origin+certProbePath,
		strings.NewReader(`{"cert_file":"/tls/c.pem","key_file":"/tls/c.key"}`))
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(csrf)
	req.Header.Set(auth.CSRFHeaderName, csrf.Value)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden {
		t.Fatalf("the double submit was refused on plain http: %s", rec.Body.String())
	}
}

// THE SESSION COOKIE KEEPS ITS FLAG, WHICH IS THE BOUNDARY OF THE SAME RULING. quince#497's login
// loop is a defect of the SESSION cookie, and relaxing that one is a different decision the Operator
// has already ruled belongs behind the explicit opt-in. A future edit that "makes the two
// consistent" would undo it, so the difference is asserted rather than left as prose.
func TestTheSessionCookieStillCarriesSecureOnAnInsecureOrigin(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodGet, "http://192.0.2.10:8968/api/auth/status", nil)

	if !deps.Auth.Secure(req) {
		t.Error("Secure(r) is false on a plain-http non-loopback origin — the session cookie's rule moved")
	}
	if deps.Auth.CSRFSecure(req) {
		t.Error("CSRFSecure(r) is true on a plain-http non-loopback origin — quince#1156 is undone")
	}
	if !deps.Auth.CookieWillBeDiscarded(req) {
		t.Error("CookieWillBeDiscarded is false here — the predicate both of the above rest on has moved")
	}
}

// LOOPBACK IS UNTOUCHED BY THIS RULING, AND NOT IN THE WAY THE RULING'S OWN NOTE EXPECTED.
//
// That note reads *"the flag survives on `http://127.0.0.1`"*. It does not, and it did not before
// this change either: `secureCookie` ends in `!isLoopbackHost(r.Host)`, so plain-http loopback has
// always produced a non-Secure cookie — for BOTH cookies, which is what lets `--demo` and the e2e
// suite work over http at all.
//
// So `CSRFSecure` narrows exactly one case, plain http to a NON-loopback host, and leaves every
// other answer identical to `Secure`. Asserted rather than written down, because the note invites a
// future reader to "restore" a flag on loopback that was never set.
func TestLoopbackIsUnchangedAndCarriesNoSecureFlagEitherWay(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8968/api/auth/status", nil)

	if deps.Auth.Secure(req) {
		t.Error("Secure(r) is true on plain-http loopback — the pre-existing relaxation moved")
	}
	if deps.Auth.CSRFSecure(req) {
		t.Error("CSRFSecure(r) is true on plain-http loopback — it must agree with Secure here")
	}
	// AND THE TWO AGREE EVERYWHERE THE COOKIE WOULD SURVIVE. `CookieWillBeDiscarded` is false on
	// loopback, so `CSRFSecure` reduces to `Secure` — the narrowing is real but it is narrow.
	if deps.Auth.CookieWillBeDiscarded(req) {
		t.Error("CookieWillBeDiscarded is true on loopback — the predicate this rests on moved")
	}
}

// AND ON A GENUINELY SECURE ORIGIN NOTHING CHANGES: both cookies carry the flag, as they always did.
func TestATLSOriginKeepsTheSecureFlagOnBothCookies(t *testing.T) {
	deps := testDeps(t)
	req := httptest.NewRequest(http.MethodGet, "https://quince.example:8968/api/auth/status", nil)
	req.TLS = &tls.ConnectionState{}

	if !deps.Auth.Secure(req) {
		t.Error("Secure(r) is false on an https origin")
	}
	if !deps.Auth.CSRFSecure(req) {
		t.Error("CSRFSecure(r) is false on an https origin — the CSRF cookie lost a flag it should keep")
	}
}
