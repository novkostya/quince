package auth

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// quince#1001 — a credential change must end the sessions that credential created.
//
// People change a password because they think it leaked. The sessions a leaked password created are
// exactly the ones that must not survive it, and quince did the opposite — and said so on screen.

func liveSession(t *testing.T, svc *Service, id string, cred *string) {
	t.Helper()
	now := time.Now().UTC()
	if err := svc.store.CreateAuthSession(store.AuthSession{
		ID: id, CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
		CredentialID: cred,
	}); err != nil {
		t.Fatalf("seed session %s: %v", id, err)
	}
}

func alive(t *testing.T, svc *Service, id string) bool {
	t.Helper()
	_, ok, err := svc.store.GetAuthSession(id)
	if err != nil {
		t.Fatalf("get session %s: %v", id, err)
	}
	return ok
}

func TestChangingThePasswordEndsOtherPasswordSessions(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	cred := "passkey-1"
	liveSession(t, svc, "mine", nil)   // the tab doing the change
	liveSession(t, svc, "laptop", nil) // another password login
	liveSession(t, svc, "phone", &cred)

	if err := svc.ChangePassword(NewProofs(), Presented{Password: "test"}, "new-password",
		"mine", "1.2.3.4"); err != nil {
		t.Fatalf("change password: %v", err)
	}

	if !alive(t, svc, "mine") {
		t.Fatal("the session performing the change was signed out — hostile, and not what the norm means")
	}
	if alive(t, svc, "laptop") {
		t.Fatal("a session created by the OLD password survived the change (quince#1001)")
	}
	// THE SELECTIVITY, which is the half only 0014 made possible. quince#1001 offered "end every
	// session except the current one" as a fallback; a passkey session is not the changed
	// credential's and must be left alone.
	if !alive(t, svc, "phone") {
		t.Fatal("a passkey session was ended by a PASSWORD change — too wide")
	}
}

func TestRemovingAPasskeyEndsItsSessionsAndOnlyThose(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	// 16 chars: base64url rejects a length of 13 (mod 4 == 1), which is a test-fixture trap and
	// not a property of the code under test.
	target := "cred-target-aaaa"
	other := "cred-other-aaaaa"
	seedCredential(t, svc.store, target, rpHome)
	seedCredential(t, svc.store, other, rpHome)
	liveSession(t, svc, "mine", nil)
	liveSession(t, svc, "revoked-phone", &target)
	liveSession(t, svc, "kept-phone", &other)

	// Removed with the password as the proof, which is a different credential from the target.
	if _, err := svc.RemovePasskey(NewProofs(), Presented{Password: "test"}, target, rpHome,
		"mine", "1.2.3.4"); err != nil {
		t.Fatalf("remove passkey: %v", err)
	}

	if alive(t, svc, "revoked-phone") {
		t.Fatal("a REVOKED credential's session survived — the credential is not revoked (spec D9)")
	}
	if !alive(t, svc, "kept-phone") {
		t.Fatal("another passkey's session was ended — removal is not a global sign-out")
	}
	if !alive(t, svc, "mine") {
		t.Fatal("the acting session was signed out")
	}
}

// The store predicate, asserted directly. `credential_id = NULL` is never true in SQL, so a
// parameterised version of the password branch would silently delete nothing — a no-op wearing the
// shape of a success, which is the failure this function exists to prevent.
func TestDeleteAuthSessionsForNilMatchesPasswordSessions(t *testing.T) {
	svc, _ := newTestAuth(t)
	cred := "c1"
	liveSession(t, svc, "pw-1", nil)
	liveSession(t, svc, "pw-2", nil)
	liveSession(t, svc, "keep-me", nil)
	liveSession(t, svc, "passkey-1", &cred)

	n, err := svc.store.DeleteAuthSessionsFor(nil, "keep-me")
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if n != 2 {
		t.Fatalf("deleted %d password sessions, want 2 — IS NULL did not match", n)
	}
	if !alive(t, svc, "keep-me") || !alive(t, svc, "passkey-1") {
		t.Fatal("deleted too much")
	}
}
