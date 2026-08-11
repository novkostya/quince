package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.6m D6 — the demo carve-out, at the wire. The service-level guards are tested in
// `internal/auth`; what is asserted here is the thing that can only be got wrong in the WIRING:
// that leaving `PasswordAdmin` nil produces a surface which REFUSES WITH A REASON rather than one
// that 404s, 500s, or panics.

// PasswordAdmin LEFT NIL — which is exactly what `--demo` does. `NewRouter` installs
// `UnavailablePasswordAdmin`, and these two tests are the whole of the demo carve-out's evidence.
//
// THROUGH THE ROUTER RATHER THAN CALLING THE HANDLER, and it costs a real session to do it. That
// cost is the point: `NewRouter` takes `Deps` BY VALUE and installs the stand-in on its own copy, so
// the substitution is unobservable from outside and a handler-level test would prove the mapping
// while proving nothing about the wiring — which is the only half that can be got wrong here.
func demoRouter(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	d := testDeps(t)
	d.PasswordAdmin = nil
	return NewRouter(d), d.Store
}

// authed attaches a live session and a matching double-submit pair.
//
// The routes under test are in NEITHER `authExempt` NOR `csrfExempt` — asserted by exact path in
// `passkey_allowlist_test.go` — so a request without both is refused before it reaches the stand-in,
// which is 401/403 rather than the 503 these tests are about. Building the session by hand rather
// than logging in keeps the test about the carve-out instead of about the login path.
func authed(t *testing.T, st *store.Store, req *http.Request) *http.Request {
	t.Helper()
	now := time.Now().UTC()
	sess := store.AuthSession{
		ID: "sess-demo", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}
	if err := st.CreateAuthSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	const csrf = "csrf-token-for-the-test"
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: csrf})
	req.Header.Set(auth.CSRFHeaderName, csrf)
	return req
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) (code, message string) {
	t.Helper()
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return body.Error.Code, body.Error.Message
}

// THE ROUTES STILL EXIST IN DEMO MODE, and that is the design rather than an accident. A 404 would
// be the surface quietly vanishing; `no silent caps or fallbacks` wants it present and explaining
// itself, so a demo visitor learns what the real thing does instead of learning nothing.
func TestDemoRefusesAPasswordChangeWithAStatedReason(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("PUT", "/api/auth/password",
		strings.NewReader(`{"current_password":"demo","new_password":"something-else"}`))
	h, st := demoRouter(t)
	h.ServeHTTP(rec, authed(t, st, req))

	// 401 would mean "no session", which is the ordinary guard and NOT what is being tested; if this
	// ever starts failing with 401 the test harness lost its session, not the carve-out.
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodeErr(t, rec)
	if code != "unavailable" {
		t.Errorf("code = %q, want %q", code, "unavailable")
	}
	// THE SENTENCE IS THE POINT. A bare 503 tells a demo visitor the server is broken; this one
	// tells them why the control cannot work here, which is the difference the rule is about.
	if !strings.Contains(msg, "shared with every visitor") {
		t.Errorf("message does not say why: %q", msg)
	}
}

// RULING B SAYS "NEVER ON THE DEMO" AND MEANS BOTH HALVES. Removing the password is the more
// dangerous one: a visitor could make the shared install passwordless against a credential only
// they hold, and every other visitor would be locked out until the periodic reset.
func TestDemoRefusesRemovingThePasswordToo(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/auth/password", nil)
	h, st := demoRouter(t)
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 (body: %s)", rec.Code, rec.Body.String())
	}
	if code, _ := decodeErr(t, rec); code != "unavailable" {
		t.Errorf("code = %q, want %q", code, "unavailable")
	}
}
