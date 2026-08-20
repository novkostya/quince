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

// THE PRE-AUTH TRANSPORT OPT-IN — quince#908 slice 6, Operator ruling 2026-08-14, contracts §1.
//
// The route's whole reason to exist is that it is reachable WITHOUT a credential, and its whole
// safety argument is that it stops being reachable the moment one exists. Both halves are asserted
// here, and the second is the one that matters: without it this route is an unauthenticated *turn
// off the transport requirement* primitive on a configured install.

const route = "/api/config/insecure-transport"

func postTransport(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://quince.example:8968"+route, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// NO SESSION, NO CSRF TOKEN, NO COOKIE OF ANY KIND on this request — which is the point. A 200 here
// proves all three exact-path lists admit it: `authExempt`, `csrfExempt` and `setupAllowed`. Miss
// any one and this is a 401, a 403 or a 503, and the dead end quince#908 is about stays shut.
func TestPreAuthCallerCanAllowInsecureTransport(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	if rec := postTransport(t, router, `{"allow":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — body %s", rec.Code, rec.Body.String())
	}

	if !deps.Config.Current().Sessions.AllowInsecureTransport {
		t.Errorf("the setting was not written")
	}
	// THE ROUTE'S JOB ENDS AT THE WRITE, AND THIS TEST STOPS THERE DELIBERATELY.
	//
	// It also asserted `deps.Auth.AllowsInsecureTransport()` — *written AND applied* — and that is not
	// this package's to prove: the subscription carrying a config change into the auth service is
	// wired by `main.go` (`subscribeInsecureTransport`), so a router built by `testDeps` has none, and
	// the assertion failed against correct code.
	//
	// SUBSCRIBING INSIDE THE TEST WOULD HAVE MADE IT PASS AND BEEN WORSE: a second copy of the applier
	// is the duplicate-predicate hazard `RequireStorage`, `CheckStorages` and `AllowsInsecureTransport`
	// each carry a paragraph about, and a green test proving a copy of the mechanism rather than the
	// mechanism is what that shape produces.
	//
	// The live half is covered where it is wired — `main_test.go` drives `subscribeInsecureTransport`
	// directly — and end to end on the container walk in this PR, which is the only place the whole
	// chain runs: route → file → applier → cookie.
}

// THE GUARD IS ABOUT THE CALLER, NOT ABOUT THE INSTALL (quince#1069). Every case below sends NO
// COOKIE OF ANY KIND: once somebody owns the install, an ANONYMOUS caller gets nothing here. An
// authenticated admin may write it — `TestAnAuthenticatedAdminCanTurnItOff` — which is what keeps the
// setting from being turn-on-only from the UI.
//
// Asserted on the STATE as well as on the status: a 409 that had already written the setting would
// be worse than no guard, because it would look refused.
func TestAClaimedInstallRefusesAnAnonymousCaller(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"turning it on", `{"allow":true}`},
		// BOTH DIRECTIONS ARE CLOSED. `false` looks harmless — it re-tightens transport — but an
		// unauthenticated caller who can toggle a setting either way can flap it, and the ruling
		// scopes the window to the unclaimed install rather than to a direction.
		{"turning it off", `{"allow":false}`},
		// THE BODY IS NEVER READ ON A CLAIMED INSTALL. If the 409 moved below the decode, this
		// would be a 400 — which tells a caller the route exists and reached its parser, and
		// distinguishes a claimed install from an unclaimed one by the shape of the refusal.
		{"a body that would not parse", `{"allow":`},
		{"no body at all", ``},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := testDeps(t)
			if err := deps.Auth.SetPassword("test", "127.0.0.1"); err != nil {
				t.Fatalf("SetPassword: %v", err)
			}
			before := deps.Config.Current().Sessions.AllowInsecureTransport

			rec := postTransport(t, NewRouter(deps), tc.body)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status %d, want 409 — body %s", rec.Code, rec.Body.String())
			}
			if got := deps.Config.Current().Sessions.AllowInsecureTransport; got != before {
				t.Errorf("the setting moved to %v on a REFUSED request — the guard runs too late", got)
			}
		})
	}
}

// `Configured()` IS NOT `HasPassword()`, and a passkey-only install is the case that separates them.
// It is claimed — somebody owns it — so the window is closed even though no password exists. Using
// the narrower predicate here would leave the downgrade primitive open on every passkey-only
// deployment, which is the quietest possible way to get this wrong.
func TestAPasskeyOnlyInstallIsClaimedToo(t *testing.T) {
	deps := testDeps(t)
	if err := deps.Store.InsertPasskey(store.Passkey{
		CredentialID: "cred-1",
		PublicKey:    []byte("cose"),
		// A DIFFERENT rpId FROM THE REQUEST'S HOST, deliberately. `Configured` does not filter by
		// rpId — the question is *has this install been claimed*, not *can you sign in here* — so a
		// passkey bound elsewhere still closes this window. Filtering would reopen the downgrade
		// through a second address, which is contracts §1's table in this route's terms.
		RPID:      "somewhere.else",
		Name:      "phone",
		CreatedAt: time.Now().UTC(),
	}, store.AdminScope()); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	if rec := postTransport(t, NewRouter(deps), `{"allow":true}`); rec.Code != http.StatusConflict {
		t.Errorf("status %d, want 409 — a passkey claims the install just as a password does", rec.Code)
	}
	if deps.Config.Current().Sessions.AllowInsecureTransport {
		t.Errorf("the setting was written on a claimed install")
	}
}

// IT WRITES ONE KEY AND LEAVES THE REST OF THE DOCUMENT ALONE. The route is pre-auth, so this is a
// security property rather than a tidiness one: a full-document replace behind an unauthenticated
// door would let a stranger blank storage declarations, retention and muxer settings while flipping
// the boolean. `config.SetAllowInsecureTransport` splices server-side for exactly this reason.
func TestTheWriteTouchesNothingElse(t *testing.T) {
	deps := testDeps(t)
	before := deps.Config.Current()

	if rec := postTransport(t, NewRouter(deps), `{"allow":true}`); rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — body %s", rec.Code, rec.Body.String())
	}

	after := deps.Config.Current()
	// Compare everything except the one key this route owns, by putting it back and diffing the
	// documents whole — so a future field added anywhere in the config is covered without this test
	// being updated to know about it.
	after.Sessions.AllowInsecureTransport = before.Sessions.AllowInsecureTransport
	gotJSON, _ := json.Marshal(after)
	wantJSON, _ := json.Marshal(before)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("the document changed outside sessions.allow_insecure_transport\n got %s\nwant %s",
			gotJSON, wantJSON)
	}
}

// IT GOES BOTH WAYS while the install is unclaimed. A control that can only be turned ON is a second
// dead end — relax the transport to finish setup, then need a shell on the box to put it back, which
// is the defect quince#912 named. quince#900 made the setting live in both directions.
func TestThePreAuthWriteAcceptsFalse(t *testing.T) {
	deps := testDeps(t)
	router := NewRouter(deps)

	if rec := postTransport(t, router, `{"allow":true}`); rec.Code != http.StatusOK {
		t.Fatalf("on: status %d", rec.Code)
	}
	if rec := postTransport(t, router, `{"allow":false}`); rec.Code != http.StatusOK {
		t.Fatalf("off: status %d — body %s", rec.Code, rec.Body.String())
	}
	if deps.Config.Current().Sessions.AllowInsecureTransport {
		t.Errorf("the setting is still on after a false")
	}
	if deps.Auth.AllowsInsecureTransport() {
		t.Errorf("saved off but still applied on — the applier did not run for the false")
	}
}

// A MALFORMED BODY ON AN UNCLAIMED INSTALL IS A 400, not a silent default. `{"allow":false}` and
// "you sent nonsense" must not be the same outcome: defaulting a security setting from an
// unparseable request is how a caller gets a state nobody asked for.
func TestAMalformedBodyIsRefusedRatherThanDefaulted(t *testing.T) {
	deps := testDeps(t)
	if rec := postTransport(t, NewRouter(deps), `{"allow":`); rec.Code != http.StatusBadRequest {
		t.Errorf("status %d, want 400", rec.Code)
	}
	if deps.Config.Current().Sessions.AllowInsecureTransport {
		t.Errorf("a setting was written from a request that did not parse")
	}
}

// THE ADMIN'S OWN DOOR — quince#1069. The banner tells a reader to turn this off, and this route is
// the only writer that can: `ConfigEditor` does not render the key, and the first-run confirm writes
// `true` and only `true`. Without this case the setting is turn-on-only from the UI, which is stack
// D12 broken.
//
// THE NARROW WRITE IS THE POINT. `PUT /api/config` would also do it and is a full-document replace:
// a client that does not model every Go key drops the ones it does not know (quince#493). This route
// writes one key by name, which is why the fix belongs here rather than in a second writer.
func TestAnAuthenticatedAdminCanTurnItOff(t *testing.T) {
	deps := testDeps(t)
	if err := deps.Auth.SetPassword("test", "127.0.0.1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if _, _, err := deps.Config.SetAllowInsecureTransport(true); err != nil {
		t.Fatalf("arrange: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://quince.example:8968"+route, strings.NewReader(`{"allow":false}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, authed(t, deps.Store, req))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200 — body %s", rec.Code, rec.Body.String())
	}
	if deps.Config.Current().Sessions.AllowInsecureTransport {
		t.Error("the setting is still on after an authenticated caller turned it off")
	}
}

// AND IT STILL NEEDS ITS TOKEN. This route sits in `csrfExempt` because a first-run caller has no
// cookie to double-submit, and the comment there names `Configured()` as the whole of what protects
// it — true while a claimed install refused everyone. Opening the door to a session without checking
// the token would hand a cross-site page `{"allow":true}` with the admin's cookies: the downgrade
// primitive quince#908 §3 warns about, arriving through the door added to remove one.
//
// SO THE HANDLER CHECKS IT ITSELF rather than relying on either list, and this is the test that says
// so. It fails if somebody "simplifies" that check away on the grounds that the path is exempt.
func TestAnAuthenticatedCallerStillNeedsItsCSRFToken(t *testing.T) {
	deps := testDeps(t)
	if err := deps.Auth.SetPassword("test", "127.0.0.1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "http://quince.example:8968"+route, strings.NewReader(`{"allow":true}`))
	req.Header.Set("Content-Type", "application/json")
	req = authed(t, deps.Store, req)
	req.Header.Del(auth.CSRFHeaderName)

	rec := httptest.NewRecorder()
	NewRouter(deps).ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status %d, want 403 — body %s", rec.Code, rec.Body.String())
	}
	if deps.Config.Current().Sessions.AllowInsecureTransport {
		t.Error("the setting was written by a request with no CSRF token")
	}
}
