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

// qn.6n slice 6a, AT THE WIRE. The rule itself is tested in `internal/auth`; what only exists here
// is the plumbing rule 2 needed and this endpoint did not have — A BODY ON A `DELETE`.
//
// THAT IS THE HALF A SERVICE TEST CANNOT REACH. `RemovePassword` cannot tell whether the `Presented`
// it received came off the wire or was built by a test, so a handler that dropped the body would
// pass every test in `internal/auth` and refuse every real removal.

// removalRouter wires a REAL PasswordAdmin and proof store, unlike demoRouter's deliberate nil.
func removalRouter(t *testing.T) (http.Handler, *store.Store, *auth.Proofs) {
	t.Helper()
	d := testDeps(t)
	d.PasswordAdmin = d.Auth
	d.Proofs = auth.NewProofs()
	if err := d.Auth.SetPassword("old-one", "203.0.113.1"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	// `httptest.NewRequest` sets Host to example.com, so this is the rpId every request below
	// resolves to — the credential has to be bound there or it belongs to somewhere else.
	if err := d.Store.InsertPasskey(store.Passkey{
		CredentialID: "cred-1", PublicKey: []byte("cose"), RPID: "example.com",
		Name: "phone", CreatedAt: time.Now().UTC(),
	}, store.AdminScope()); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	return NewRouter(d), d.Store, d.Proofs
}

// THE PROOF IN THE BODY REACHES THE SERVICE. The one claim this file exists for.
func TestRemovePasswordReadsTheProofFromTheBody(t *testing.T) {
	h, st, proofs := removalRouter(t)
	// `authed` names the session "sess-demo", and a proof is bound to the session that minted it —
	// so this is not a detail the test may choose freely.
	tok, err := proofs.Mint(auth.OpRemovePassword, "", auth.ProofSubject{CredentialID: "cred-1"}, "sess-demo")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/auth/password",
		strings.NewReader(`{"proof":"`+tok+`"}`))
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (body: %s)", rec.Code, rec.Body.String())
	}
}

// RULE 2 AT THE WIRE: a correct password is refused, with its own code.
//
// `wrong_credential` RATHER THAN `last_credential`, because the remedies differ — this user has a
// passkey and needs to use it, where `last_credential` means there is nothing to use. A client that
// could not tell them apart would offer a retry that cannot work, or withhold one that can.
func TestRemovePasswordRefusesThePasswordWithItsOwnCode(t *testing.T) {
	h, st, _ := removalRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/auth/password",
		strings.NewReader(`{"current_password":"old-one"}`))
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (body: %s)", rec.Code, rec.Body.String())
	}
	code, msg := decodeErr(t, rec)
	if code != "wrong_credential" {
		t.Errorf("code = %q, want %q", code, "wrong_credential")
	}
	if !strings.Contains(msg, "passkey") {
		t.Errorf("message does not name the remedy: %q", msg)
	}
}

// AN ABSENT BODY IS NOT A BAD REQUEST — `decodeOptionalJSON`. A caller that presents nothing has
// broken a rule about credentials, not about JSON, and a 400 naming the body would send them to fix
// the wrong thing.
func TestRemovePasswordWithNoBodyIsACredentialRefusal(t *testing.T) {
	h, st, _ := removalRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/auth/password", nil)
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code == http.StatusBadRequest {
		t.Fatalf("an absent body was a 400 — it must be answered as a credential refusal (body: %s)",
			rec.Body.String())
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
}

// AND A MALFORMED BODY STILL IS ONE. The tolerance above is for an ABSENT body and nothing else.
func TestRemovePasswordRejectsAMalformedBody(t *testing.T) {
	h, st, _ := removalRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/auth/password", strings.NewReader(`{"proof":`))
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}
