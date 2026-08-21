package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
)

// A SCOPED PRINCIPAL MUST NOT BE ABLE TO DOWNGRADE THE TRANSPORT (quince#1441).
//
// FOUND ON HARDWARE, 2026-08-21, by a scoped holder enabling it from Settings. The banner it raises
// says what it costs: *anyone who can see the traffic can sign in as you* — including as the ADMIN.
// So the one principal type this rung exists to confine could hand the whole install's credentials
// to the network.
//
// WHY THE ROUTE'S `adminOnly` ENTRY DID NOT STOP IT. The route is in `authExempt`, which is what
// makes it reachable by a first-run user stranded on plain http — and `authGuard` is the only place
// a principal is bound. No principal, no scope guard, and the `routeScope` entry was a comment
// rather than a control. The handler asked *is there a session*, never *whose*.

// THE STARTUP GATE IS THE GENERAL FORM. Every authExempt route whose class is not openToAll must
// declare what bounds it instead, and `NewRouter` panics if one drifts out.
func TestEveryExemptRouteWithAScopeClassDeclaresItsOwnGuard(t *testing.T) {
	// The control: an empty map would make this pass against anything.
	if len(enforcesItsOwnPrincipal) == 0 {
		t.Fatal("enforcesItsOwnPrincipal is empty — this assertion would pass vacuously")
	}
	if enforcesItsOwnPrincipal["POST /api/config/insecure-transport"] == "" {
		t.Fatal("the transport downgrade is not declared as enforcing its own principal check")
	}
	// Building the router IS the assertion — the gate runs at construction.
	srv := httptest.NewServer(NewRouter(testDeps(t)))
	t.Cleanup(srv.Close)
}

// scopedSession mints a session whose credential is device-scoped, and returns a request carrying
// it. Built by hand rather than through a login, because there is no scoped sign-in path in this
// package's helpers and the point is what the HANDLER does with such a session.
func scopedRequest(t *testing.T, d Deps, srv *httptest.Server, body string) *http.Request {
	t.Helper()
	if err := d.Store.InsertPasskey(store.Passkey{
		CredentialID: "scoped-cred", PublicKey: []byte("k"), RPID: "quince.example",
		Name: "household iPhone", CreatedAt: time.Now().UTC(),
	}, store.DeviceScope("DEVICE-A")); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	cred := "scoped-cred"
	now := time.Now().UTC()
	sess := store.AuthSession{
		ID: "scoped-session", CreatedAt: now, LastSeenAt: now,
		ExpiresAt: now.Add(time.Hour), CredentialID: &cred,
	}
	if err := d.Store.CreateAuthSession(sess); err != nil {
		t.Fatalf("create session: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/config/insecure-transport",
		strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: sess.ID})
	// Double-submit: the cookie and the header only have to AGREE, so a value the test chooses is
	// as valid as one the server minted. This request must fail on SCOPE, not on CSRF.
	req.AddCookie(&http.Cookie{Name: auth.CSRFCookieName, Value: "t0ken"})
	req.Header.Set(auth.CSRFHeaderName, "t0ken")
	return req
}

func TestAScopedSessionCannotEnableInsecureTransport(t *testing.T) {
	d := testDeps(t)
	srv := httptest.NewServer(NewRouter(d))
	t.Cleanup(srv.Close)

	// The install must be CONFIGURED, or the route is in its first-run mode and refuses nobody.
	if err := d.Auth.SetPassword("test", ""); err != nil {
		t.Fatalf("configure: %v", err)
	}

	resp, err := srv.Client().Do(scopedRequest(t, d, srv, `{"allow":true}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 — a scoped holder downgraded the transport for the whole install",
			resp.StatusCode)
	}
	// AND IT DID NOT TAKE EFFECT. A refusal that wrote anyway would be the worst of both.
	if d.Config.Current().Sessions.AllowInsecureTransport {
		t.Fatal("the setting was written despite the refusal")
	}
}
