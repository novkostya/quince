package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
)

// qn.13 slice 8d / D8 — WHAT THE CLIENT IS TOLD ABOUT ITSELF (ruled on quince#1443).
//
// The shell hides what a principal cannot use, so this field is what decides whether a household
// member is shown the admin's chrome. The server refuses either way — that is D8's *unreachable is a
// server property* — so nothing here is an authorization test. It is a test about honesty.
//
// SYNTHETIC UDIDS. A real one is Operator-private and never enters a fixture.

func seedAdminCred(t *testing.T, st *store.Store, credID string) auth.Principal {
	t.Helper()
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: credID,
		PublicKey:    []byte("k"),
		RPID:         "quince.example",
		Name:         credID,
	}, store.AdminScope()); err != nil {
		t.Fatalf("seed admin credential: %v", err)
	}
	return auth.Principal{CredentialID: credID}
}

func TestAnAdminPrincipalIsToldNoScope(t *testing.T) {
	d, st := listDeps(t)
	p := seedAdminCred(t, st, "cred-admin")

	got, err := d.scopeWireFor(p)
	if err != nil {
		t.Fatalf("scopeWireFor: %v", err)
	}
	if got != nil {
		t.Fatalf("scope = %+v, want nil — nil is ADMIN, and a scope on the admin would confine the "+
			"one principal that must not be", got)
	}
}

func TestAPasswordLoginIsToldNoScope(t *testing.T) {
	d, _ := listDeps(t)

	// A password is the admin's and there is nothing else it could be (0014).
	got, err := d.scopeWireFor(auth.Principal{})
	if err != nil {
		t.Fatalf("scopeWireFor: %v", err)
	}
	if got != nil {
		t.Fatalf("scope = %+v, want nil for a password login", got)
	}
}

func TestAScopedPrincipalIsToldItsDevice(t *testing.T) {
	d, st := listDeps(t)
	p := scopedPrincipal(t, st, "cred-scoped", "udid-fixture-0001")

	got, err := d.scopeWireFor(p)
	if err != nil {
		t.Fatalf("scopeWireFor: %v", err)
	}
	if got == nil {
		t.Fatal("scope = nil, want the device — a nil scope reads as ADMIN, so this is the shell " +
			"being told a household member administers quince")
	}
	if got.UDID != "udid-fixture-0001" {
		t.Fatalf("scope.UDID = %q, want %q", got.UDID, "udid-fixture-0001")
	}
}

// THE ORDERING TRAP, AND IT IS THE REASON THIS FILE EXISTS.
//
// `ScopeOf` answers `("", nil)` for an admin and `("", ErrCredentialRevoked)` for a credential that
// has been removed — the SAME empty string. A resolver that reads the value before the error turns a
// revocation into an ADMIN answer. quince#1441's review caught exactly this shape on the transport
// route and named it: converting a revocation into a privilege grant.
func TestARevokedCredentialIsNotReportedAsAdmin(t *testing.T) {
	d, st := listDeps(t)
	p := scopedPrincipal(t, st, "cred-gone", "udid-fixture-0002")
	if _, err := st.DeletePasskey("cred-gone"); err != nil {
		t.Fatalf("remove credential: %v", err)
	}

	got, err := d.scopeWireFor(p)
	if err == nil {
		t.Fatalf("scopeWireFor returned %+v and NO error for a credential that no longer exists — "+
			"a nil scope here tells a revoked holder they administer quince", got)
	}
	if got != nil {
		t.Fatalf("scope = %+v alongside an error, want nil", got)
	}
}

// AND THE STATUS READ MUST NOT CALL THAT SESSION AUTHENTICATED.
//
// The case the ruling did not cover, because the field is what raises it. quince#1001 ends a
// credential's sessions when it is removed, so this is the window between the two. `authenticated`
// with a nil scope would say ADMIN; `500` on the endpoint the shell boots from would strand the user
// behind a broken app rather than in front of the login form they need.
//
// THE INSTALL MUST STAY CONFIGURED, AND THAT IS THE WHOLE FIXTURE. An earlier version of this test
// seeded only the scoped credential and removed it — which left the install with NO credentials, so
// `Status` answered `needs_setup`, `authStatusFor` returned at its early guard, and the revoked
// branch was never reached. It asserted `!= authenticated` and `needs_setup` satisfies that, so it
// passed for the wrong reason and a mutation making the branch report `authenticated` left the whole
// package green (quince#1465 review). Seeding an ADMIN credential alongside is what keeps
// `Configured()` true so the read gets as far as the branch under test.
func TestTheStatusReadDowngradesASessionWhoseCredentialIsGone(t *testing.T) {
	d, st := listDeps(t)
	seedAdminCred(t, st, "cred-admin") // keeps the install CONFIGURED after the removal
	scopedPrincipal(t, st, "cred-gone", "udid-fixture-0003")
	seedSessionFor(t, st, "sess-1", "cred-gone")
	if _, err := st.DeletePasskey("cred-gone"); err != nil {
		t.Fatalf("remove credential: %v", err)
	}

	out, err := d.authStatusFor("sess-1", "csrf")
	if err != nil {
		t.Fatalf("authStatusFor: %v — the status read must answer, not fail, in this window", err)
	}
	if out.State != auth.StateNeedsLogin {
		t.Fatalf("state = %q, want %q — a session quince cannot attribute must not be reported as "+
			"authenticated, and with a nil scope that says ADMIN", out.State, auth.StateNeedsLogin)
	}
	if out.Scope != nil {
		t.Fatalf("scope = %+v on a non-authenticated state, want nil", out.Scope)
	}
	// THE WIRE SHAPE, not just the struct: `scope` must be present-and-null rather than absent, so a
	// client reading it never has to tell "no key" from "null" (contracts §1, no `omitempty`).
	blob, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(blob), `"scope":null`) {
		t.Fatalf("payload = %s, want an explicit \"scope\":null", blob)
	}
}

// THE CONTROL, and without it the test above passes for any reason at all that produces a state
// other than `authenticated` — which is exactly how its first version passed.
//
// SAME FIXTURE, SAME READ, CREDENTIAL INTACT. The only difference is the removal, so a green here
// and a red there is evidence about the removal rather than about the harness.
func TestTheSameReadWithTheCredentialIntactIsAuthenticatedAndScoped(t *testing.T) {
	d, st := listDeps(t)
	seedAdminCred(t, st, "cred-admin")
	scopedPrincipal(t, st, "cred-live", "udid-fixture-0004")
	seedSessionFor(t, st, "sess-2", "cred-live")

	out, err := d.authStatusFor("sess-2", "csrf")
	if err != nil {
		t.Fatalf("authStatusFor: %v", err)
	}
	if out.State != auth.StateAuthenticated {
		t.Fatalf("state = %q, want %q — the control must reach the branch the test above is about",
			out.State, auth.StateAuthenticated)
	}
	if out.Scope == nil || out.Scope.UDID != "udid-fixture-0004" {
		t.Fatalf("scope = %+v, want the device — a nil scope here reads as ADMIN", out.Scope)
	}
}

// NO SCOPE ON A STATE THAT CANNOT CARRY ONE — the other half of *`state` is the disambiguator*.
func TestAnUnconfiguredInstallCarriesNoScope(t *testing.T) {
	d, _ := listDeps(t)

	out, err := d.authStatusFor("", "csrf")
	if err != nil {
		t.Fatalf("authStatusFor: %v", err)
	}
	if out.State != auth.StateNeedsSetup {
		t.Fatalf("state = %q, want %q", out.State, auth.StateNeedsSetup)
	}
	if out.Scope != nil {
		t.Fatalf("scope = %+v on %q, want nil", out.Scope, out.State)
	}
}

// seedSessionFor creates a live session attributed to one credential.
func seedSessionFor(t *testing.T, st *store.Store, sessID, credID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateAuthSession(store.AuthSession{
		ID: sessID, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		CredentialID: &credID,
	}); err != nil {
		t.Fatalf("seed session %s: %v", sessID, err)
	}
}
