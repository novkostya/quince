package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/auth"
)

// postAuth sends a credential POST with an explicit Host, driving the router directly
// rather than through httptest.NewServer — the server forces Host to the loopback address
// it listens on, which is the one origin the refusal must NOT fire on, so it cannot
// express these cases at all. Returns the recorder: there is no body to close.
func postAuth(t *testing.T, deps Deps, path, host, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Host = host
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope from %q: %v", rec.Body.String(), err)
	}
	return env.Error.Code
}

// setCookieNames lists the cookies a response sets, read off the raw header so no
// *http.Response is constructed just to ask.
func setCookieNames(rec *httptest.ResponseRecorder) []string {
	var names []string
	for _, sc := range rec.Header().Values("Set-Cookie") {
		if name, _, ok := strings.Cut(sc, "="); ok {
			names = append(names, strings.TrimSpace(name))
		}
	}
	return names
}

// A phone reaching quince at a LAN address over plain http is the whole of quince#497: the
// session cookie would be marked Secure and dropped, so both credential endpoints refuse
// rather than answering 200 with cookies that evaporate on arrival.
func TestCredentialPostRefusedOnInsecureOrigin(t *testing.T) {
	for _, path := range []string{"/api/auth/login", "/api/auth/setup"} {
		t.Run(path, func(t *testing.T) {
			rec := postAuth(t, testDeps(t), path, "quince.example:8080", `{"password":"test"}`, nil)
			if rec.Code != http.StatusUpgradeRequired {
				t.Fatalf("status = %d, want 426", rec.Code)
			}
			if got := errorCode(t, rec); got != "insecure_origin" {
				t.Fatalf("code = %q, want insecure_origin", got)
			}
			// RFC 9110 §15.5.22 makes the Upgrade header a MUST on a 426.
			if rec.Header().Get("Upgrade") == "" {
				t.Error("426 sent with no Upgrade header")
			}
			// No SESSION cookie: that is the one the refusal exists to not issue, and its
			// absence is what proves Login/SetPassword were never reached.
			//
			// A quince_csrf cookie IS present and is not a second defect. authGuard runs
			// ensureCSRF on every request including the exempt ones, so it is set before
			// this handler is entered, by middleware that predates the refusal and does not
			// know about it. It is stateless — a minted token in a cookie, nothing stored —
			// so the refusal still leaves nothing behind. It will be discarded by the same
			// browser for the same reason, which costs nothing: a CSRF token is only ever
			// used to accompany a session that this request did not create.
			for _, name := range setCookieNames(rec) {
				if name == auth.SessionCookieName {
					t.Error("the refusal set a session cookie, which is the cookie it exists to not issue")
				}
			}
		})
	}
}

// The refusal must precede SetPassword, not follow it. Were it after, this second setup —
// over an origin that works — would answer 409 already_configured, locking a first-run user
// out of their own setup screen with an error that said nothing had happened.
func TestRefusalOnSetupLeavesNoPasswordBehind(t *testing.T) {
	deps := testDeps(t)

	if rec := postAuth(t, deps, "/api/auth/setup", "quince.example:8080", `{"password":"test"}`, nil); rec.Code != http.StatusUpgradeRequired {
		t.Fatalf("status = %d, want 426", rec.Code)
	}

	if has, err := deps.Auth.HasPassword(); err != nil {
		t.Fatalf("HasPassword: %v", err)
	} else if has {
		t.Fatal("the refused setup set the password anyway")
	}

	if rec := postAuth(t, deps, "/api/auth/setup", "127.0.0.1:8080", `{"password":"test"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup over loopback after a refusal: status = %d, want 200", rec.Code)
	}
}

// A wrong password and a right one must be indistinguishable on an insecure origin, or the
// refusal becomes a password oracle reachable over the exact channel that is not encrypted.
func TestRefusalDoesNotRevealWhetherThePasswordWasRight(t *testing.T) {
	deps := testDeps(t)
	if rec := postAuth(t, deps, "/api/auth/setup", "127.0.0.1:8080", `{"password":"test"}`, nil); rec.Code != http.StatusOK {
		t.Fatalf("setup fixture: status = %d, want 200", rec.Code)
	}

	right := postAuth(t, deps, "/api/auth/login", "quince.example:8080", `{"password":"test"}`, nil)
	wrong := postAuth(t, deps, "/api/auth/login", "quince.example:8080", `{"password":"nope"}`, nil)

	if right.Code != wrong.Code {
		t.Fatalf("right password → %d, wrong → %d; the refusal distinguishes them", right.Code, wrong.Code)
	}
	if a, b := errorCode(t, right), errorCode(t, wrong); a != b {
		t.Fatalf("right password → %q, wrong → %q; the refusal distinguishes them", a, b)
	}
	if right.Body.String() != wrong.Body.String() {
		t.Error("the two refusals differ in their message body")
	}
}

// The origins where the cookie survives, so the refusal must not fire. Every one of these
// reaches the Secure decision by a different route, and a refusal here is a login that
// used to work and now does not.
func TestCredentialPostAllowedWhereTheCookieSurvives(t *testing.T) {
	tests := []struct {
		name    string
		host    string
		headers map[string]string
		demo    bool
	}{
		{name: "loopback plain http", host: "127.0.0.1:8080"},
		{name: "localhost by name", host: "localhost:8080"},
		{name: "ipv6 loopback", host: "[::1]:8080"},
		{name: "proxy terminated tls", host: "quince.example", headers: map[string]string{"X-Forwarded-Proto": "https"}},
		{name: "demo mode on a lan address", host: "quince.example:8080", demo: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			if tc.demo {
				deps.Auth.SetInsecureCookies(true)
			}
			rec := postAuth(t, deps, "/api/auth/setup", tc.host, `{"password":"test"}`, tc.headers)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200 — the refusal fired where the cookie survives", rec.Code)
			}
			var sawSession bool
			for _, name := range setCookieNames(rec) {
				if name == auth.SessionCookieName {
					sawSession = true
				}
			}
			if !sawSession {
				t.Error("no session cookie on a successful setup")
			}
		})
	}
}

// Logout is deliberately not guarded by the refusal, and this pins the reason rather than
// leaving it as prose: logout is not in authExempt, so a request from an insecure origin —
// which by definition cannot be presenting a Secure session cookie — is turned away by the
// auth guard before the handler runs at all. There is no state for a refusal to protect,
// and adding one would replace a 401 that is true with a 426 about a cookie nobody sent.
func TestLogoutOnInsecureOriginIsStoppedByTheAuthGuardNotTheRefusal(t *testing.T) {
	rec := postAuth(t, testDeps(t), "/api/auth/logout", "quince.example:8080", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if got := errorCode(t, rec); got != "unauthorized" {
		t.Fatalf("code = %q, want unauthorized", got)
	}
}
