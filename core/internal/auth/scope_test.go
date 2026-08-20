package auth

import (
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// seedScoped inserts one credential. `scopeUDID` nil means an ADMIN credential.
func seedScoped(t *testing.T, st *store.Store, id string, scopeUDID *string) {
	t.Helper()
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: id,
		PublicKey:    []byte("k"),
		RPID:         rpHome,
		Name:         id,
		CreatedAt:    time.Now().UTC(),
		ScopeUDID:    scopeUDID,
	}); err != nil {
		t.Fatalf("seed %s: %v", id, err)
	}
}

// G1 — THE LOCKOUT. A scoped-only install must not be able to reach passwordless.
//
// Operator, quince#1342: "to go passwordless you have to have admin passkey, scoped passkey is not
// enough." With one scoped credential and no admin passkey, an install that counted every row would
// permit its admin password to be removed — after which the admin cannot get in, the scoped holder
// cannot administer anything by construction, and only `quince auth reset` (which destroys every
// credential) is a way back.
func TestScopedOnlyInstallCannotReachPasswordless(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	udid := "DEVICE-A"
	seedScoped(t, svc.store, "scoped-1", &udid)

	factors, err := svc.Accepts(OpRemovePassword, rpHome, "")
	if err != nil {
		t.Fatalf("accepts: %v", err)
	}
	for _, f := range factors {
		if f == FactorPasskey {
			t.Fatal("a scoped-only install offered a passkey as proof for removing the password — " +
				"this is the lockout D6 exists to prevent")
		}
	}
}

// The control. The SAME assertion with an ADMIN credential must offer the passkey factor — without
// this, a predicate that always answered "no" would pass the test above while breaking passwordless
// for everyone.
func TestAdminCredentialStillReachesPasswordless(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	seedScoped(t, svc.store, "admin-1", nil)

	factors, err := svc.Accepts(OpRemovePassword, rpHome, "")
	if err != nil {
		t.Fatalf("accepts: %v", err)
	}
	found := false
	for _, f := range factors {
		if f == FactorPasskey {
			found = true
		}
	}
	if !found {
		t.Fatal("an admin credential did not reach passwordless — the predicate is refusing everything")
	}
}

// A scoped credential BESIDE an admin one must not change the answer, in either direction.
func TestScopedCredentialDoesNotMaskAnAdminOne(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	udid := "DEVICE-A"
	seedScoped(t, svc.store, "admin-1", nil)
	seedScoped(t, svc.store, "scoped-1", &udid)

	factors, err := svc.Accepts(OpRemovePassword, rpHome, "")
	if err != nil {
		t.Fatalf("accepts: %v", err)
	}
	found := false
	for _, f := range factors {
		if f == FactorPasskey {
			found = true
		}
	}
	if !found {
		t.Fatal("an admin credential stopped counting once a scoped one existed")
	}
}

// The store-level split, asserted directly so a wrong SQL predicate fails HERE with a clear name
// rather than as a puzzling behaviour further up.
func TestListAdminPasskeysExcludesScopedRows(t *testing.T) {
	svc, _ := newTestAuth(t)
	udid := "DEVICE-A"
	seedScoped(t, svc.store, "admin-1", nil)
	seedScoped(t, svc.store, "scoped-1", &udid)

	all, err := svc.store.ListPasskeys()
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListPasskeys should still return every row: got %d want 2", len(all))
	}

	admin, err := svc.store.ListAdminPasskeys()
	if err != nil {
		t.Fatalf("list admin: %v", err)
	}
	if len(admin) != 1 {
		t.Fatalf("ListAdminPasskeys: got %d want 1", len(admin))
	}
	if admin[0].CredentialID != "admin-1" {
		t.Fatalf("wrong row survived: %s", admin[0].CredentialID)
	}
	if admin[0].ScopeUDID != nil {
		t.Fatalf("an admin row carries a scope: %q", *admin[0].ScopeUDID)
	}
}

// The scope must SURVIVE a round trip. A column that reads back nil would make every scoped
// credential an admin one, silently, which is the failure this whole slice is about.
func TestScopeRoundTrips(t *testing.T) {
	svc, _ := newTestAuth(t)
	udid := "DEVICE-A"
	seedScoped(t, svc.store, "scoped-1", &udid)

	got, ok, err := svc.store.GetPasskey("scoped-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ScopeUDID == nil {
		t.Fatal("the scope was dropped on the round trip — every scoped credential is now an admin one")
	}
	if *got.ScopeUDID != udid {
		t.Fatalf("scope changed: got %q want %q", *got.ScopeUDID, udid)
	}
}

// A credential registered before 0015 existed must read as ADMIN, which is what makes the migration
// additive: every row that exists today genuinely is one.
func TestPreScopeCredentialReadsAsAdmin(t *testing.T) {
	svc, _ := newTestAuth(t)
	seedScoped(t, svc.store, "legacy-1", nil)

	got, ok, err := svc.store.GetPasskey("legacy-1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.ScopeUDID != nil {
		t.Fatalf("a pre-scope credential gained a scope: %q", *got.ScopeUDID)
	}
}
