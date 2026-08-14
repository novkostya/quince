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
	"github.com/novkostya/quince/core/internal/wire"
)

// qn.6o slice 2, AT THE WIRE — the half `internal/auth` cannot reach.
//
// `Service.Accepts` returning the right list proves nothing about what a client receives: five
// handlers write this refusal, and one that forgot to pass its operation would compute `accepts`
// for the wrong thing while every test in `internal/auth` stayed green. That is this rung's own
// recorded defect — a guard verified where it is written and never at the surface that calls it,
// three times in `qn.6n` and `qn.6o` between them.

// acceptsRouter is a PASSWORD-ONLY install: the row the regression is about, and the one shape in
// which adding a passkey is impossible today.
func acceptsRouter(t *testing.T) (http.Handler, *store.Store) {
	t.Helper()
	d := testDeps(t)
	d.PasswordAdmin = d.Auth
	d.Proofs = auth.NewProofs()
	// Without this the passkey routes are not registered at all and the assertions below would meet
	// a 404 rather than the refusal they are about — see `testDeps`.
	d.Passkeys = newPasskeyCeremoniesForTest()
	if err := d.Auth.SetPassword("old-one", "203.0.113.1"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	return NewRouter(d), d.Store
}

func decodeRefusal(t *testing.T, rec *httptest.ResponseRecorder) wire.ErrorDetail {
	t.Helper()
	var body wire.APIError
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode refusal: %v", err)
	}
	return body.Error
}

// THE REGRESSION'S OWN REQUEST. `POST /api/auth/passkeys/register/begin` with an empty body, on an
// install that holds only a password — which is what slice 4's page will send, and what
// `AddPasskeyDialog` sends today and cannot recover from.
func TestRegisterBeginTellsAPasswordOnlyInstallWhatWouldWork(t *testing.T) {
	h, st := acceptsRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/passkeys/register/begin", strings.NewReader(`{}`))
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeRefusal(t, rec)
	if got.Code != "reauth_required" {
		t.Fatalf("code = %q, want reauth_required — the code a client retries on", got.Code)
	}
	if len(got.Accepts) != 1 || got.Accepts[0] != auth.FactorPassword {
		t.Fatalf("accepts = %v, want [password] — the only factor this install holds", got.Accepts)
	}
}

// BOTH FACTORS WHEN THE INSTALL HOLDS BOTH, so the field tracks the install rather than the route.
func TestRegisterBeginListsBothWhenBothExist(t *testing.T) {
	h, st := acceptsRouter(t)
	// `httptest.NewRequest` sets Host to example.com, so this credential is at the address the
	// request arrives on — bound elsewhere it would not count, which G4 asserts in `internal/auth`.
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: "cre1", PublicKey: []byte("cose"), RPID: "example.com",
		Name: "phone", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/passkeys/register/begin", strings.NewReader(`{}`))
	h.ServeHTTP(rec, authed(t, st, req))

	got := decodeRefusal(t, rec)
	if len(got.Accepts) != 2 ||
		got.Accepts[0] != auth.FactorPassword || got.Accepts[1] != auth.FactorPasskey {
		t.Fatalf("accepts = %v, want [password passkey]", got.Accepts)
	}
}

// `remove_password` CARRIES ITS EXCLUSION ALL THE WAY TO THE CLIENT — rule 2 at the wire, on the
// second of the five emitters, so the operation really is per-call rather than per-file.
func TestRemovePasswordNeverOffersThePasswordOnTheWire(t *testing.T) {
	h, st := acceptsRouter(t)
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: "cre1", PublicKey: []byte("cose"), RPID: "example.com",
		Name: "phone", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}

	rec := httptest.NewRecorder()
	// No body at all — `decodeOptionalJSON`'s case, and the refusal it earns is a credential one.
	req := httptest.NewRequest("DELETE", "/api/auth/password", nil)
	h.ServeHTTP(rec, authed(t, st, req))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body: %s)", rec.Code, rec.Body.String())
	}
	got := decodeRefusal(t, rec)
	if len(got.Accepts) != 1 || got.Accepts[0] != auth.FactorPasskey {
		t.Fatalf("accepts = %v, want [passkey] — the password cannot authorise its own removal", got.Accepts)
	}
}

// D4 AT THE WIRE: A DEAD END OMITS THE FIELD ENTIRELY. Asserted on the RAW JSON rather than on the
// decoded struct, because `[]string(nil)` and `[]string{}` decode identically and the whole point is
// which one reached the client. `accepts: []` would put a prompt with nothing in it one bug away.
func TestADeadEndOmitsAcceptsFromTheJSON(t *testing.T) {
	d := testDeps(t)
	d.PasswordAdmin = d.Auth
	d.Proofs = auth.NewProofs()
	// Without this the passkey routes are not registered at all and the assertions below would meet
	// a 404 rather than the refusal they are about — see `testDeps`.
	d.Passkeys = newPasskeyCeremoniesForTest()
	// A passkey and NOTHING ELSE — so removing it has nothing to prove itself with.
	if err := d.Store.InsertPasskey(store.Passkey{
		CredentialID: "cre1", PublicKey: []byte("cose"), RPID: "example.com",
		Name: "phone", CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	h := NewRouter(d)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("DELETE", "/api/auth/passkeys/cre1", nil)
	h.ServeHTTP(rec, authed(t, d.Store, req))

	if body := rec.Body.String(); strings.Contains(body, "accepts") {
		t.Fatalf("a dead-end refusal carried the field: %s", body)
	}
}

// NO Go ERROR STRING REACHES A SCREEN — Operator, 2026-08-14, from a screenshot of the running
// stand showing `auth: no proof for this operation — authenticate again` in red on the passkey card.
//
// The call sites pass `err.Error()` as the wire `message`, and the UI renders it verbatim. Every
// other refusal on this surface already writes copy — `bad_password` says *"current password is
// incorrect"* — so this was the one exception and therefore the one that leaked.
//
// ASSERTED ON THE ABSENCE OF OUR VOCABULARY rather than on the exact sentence, so rewording the copy
// does not fail the test but reintroducing an error string does.
func TestReauthRefusalSaysSomethingAUserCanRead(t *testing.T) {
	h, st := acceptsRouter(t)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/auth/passkeys/register/begin", strings.NewReader(`{}`))
	h.ServeHTTP(rec, authed(t, st, req))

	got := decodeRefusal(t, rec)
	if got.Code != "reauth_required" {
		t.Fatalf("code = %q, want reauth_required", got.Code)
	}
	for _, ours := range []string{"auth:", "proof", "ErrNoProof"} {
		if strings.Contains(strings.ToLower(got.Message), strings.ToLower(ours)) {
			t.Fatalf("the message speaks our vocabulary, not the reader's: %q (contains %q)",
				got.Message, ours)
		}
	}
	// AND IT IS NOT EMPTY, because "say nothing" is the other way to fail this.
	if strings.TrimSpace(got.Message) == "" {
		t.Fatal("the refusal carries no message at all")
	}
}
