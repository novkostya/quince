package auth

import (
	"errors"
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
	}, scopeArg(scopeUDID)); err != nil {
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

// THE REFUSAL THE MIGRATION'S ACCEPTANCE RESTS ON (quince#1361 review).
//
// 0015 accepts "NULL means admin" only because the Go layer will not guess for a new credential.
// Before this, `InsertPasskey` took a struct, a struct field is not a required argument, and a
// ceremony that omitted it wrote an ADMIN credential silently — the lockout from the write path
// instead of a predicate. Omission is now a compile error; these cover what a compile error cannot.
func TestInsertPasskeyRefusesAnUnstatedScope(t *testing.T) {
	svc, _ := newTestAuth(t)
	err := svc.store.InsertPasskey(store.Passkey{
		CredentialID: "no-scope",
		PublicKey:    []byte("k"),
		RPID:         rpHome,
		Name:         "x",
		CreatedAt:    time.Now().UTC(),
	}, store.Scope{}) // the zero value — what a forgetful caller would reach for
	if !errors.Is(err, store.ErrScopeUnset) {
		t.Fatalf("a zero Scope was accepted: err=%v", err)
	}
	// AND NOTHING WAS WRITTEN. A refusal that still inserted would be worse than no refusal.
	if _, ok, _ := svc.store.GetPasskey("no-scope"); ok {
		t.Fatal("the refused credential was written anyway")
	}
}

func TestInsertPasskeyRefusesScopeSetOnTheStruct(t *testing.T) {
	svc, _ := newTestAuth(t)
	udid := "DEVICE-A"
	err := svc.store.InsertPasskey(store.Passkey{
		CredentialID: "conflict",
		PublicKey:    []byte("k"),
		RPID:         rpHome,
		Name:         "x",
		CreatedAt:    time.Now().UTC(),
		ScopeUDID:    &udid, // the reasonable mistake: the field exists, for reads
	}, store.AdminScope())
	if !errors.Is(err, store.ErrScopeConflict) {
		t.Fatalf("a struct-set scope was silently ignored: err=%v", err)
	}
	// Silently ignoring it would have written an ADMIN credential where a scoped one was meant —
	// this rung's worst failure wearing the shape of a no-op.
	if _, ok, _ := svc.store.GetPasskey("conflict"); ok {
		t.Fatal("the refused credential was written anyway")
	}
}

// The constructors say what they mean, and the zero value says nothing.
func TestScopeConstructors(t *testing.T) {
	if !store.AdminScope().IsAdmin() {
		t.Fatal("AdminScope is not admin")
	}
	if store.DeviceScope("DEVICE-A").IsAdmin() {
		t.Fatal("a device scope reported as admin")
	}
	if (store.Scope{}).IsAdmin() {
		t.Fatal("the ZERO VALUE reported as admin — the default grants, which is the whole defect")
	}
}

// scopeArg turns this file's nil-means-admin test convention into a STATED Scope.
//
// The convention is the tests' own shorthand, not the store's — `InsertPasskey` will not accept a
// nil-means-admin argument, which is the point of the refusals above.
func scopeArg(scopeUDID *string) store.Scope {
	if scopeUDID == nil {
		return store.AdminScope()
	}
	return store.DeviceScope(*scopeUDID)
}

// ScopeOf is the join qn.13 slice 3 deliberately did NOT make a foreign key: a session points at a
// credential that may since have been removed. These assert the three answers.
func TestScopeOfAPasswordLoginIsAdmin(t *testing.T) {
	svc, _ := newTestAuth(t)
	scope, err := svc.ScopeOf(Principal{})
	if err != nil {
		t.Fatalf("ScopeOf: %v", err)
	}
	if scope != "" {
		t.Fatalf("a password login resolved to a scope: %q", scope)
	}
}

func TestScopeOfResolvesACredentialsScope(t *testing.T) {
	svc, _ := newTestAuth(t)
	udid := "DEVICE-A"
	seedScoped(t, svc.store, "scoped-1", &udid)
	seedScoped(t, svc.store, "admin-1", nil)

	scope, err := svc.ScopeOf(Principal{CredentialID: "scoped-1"})
	if err != nil || scope != udid {
		t.Fatalf("scoped credential: got %q err=%v want %q", scope, err, udid)
	}
	adminScope, err := svc.ScopeOf(Principal{CredentialID: "admin-1"})
	if err != nil || adminScope != "" {
		t.Fatalf("admin credential: got %q err=%v want empty", adminScope, err)
	}
}

// A REVOKED CREDENTIAL FAILS CLOSED, which is the direction that matters. quince#1001 ends such
// sessions at removal time, so this is the window between the two — and resolving it to the ADMIN
// would hand a revoked holder everything on their way out.
func TestScopeOfRefusesARevokedCredential(t *testing.T) {
	svc, _ := newTestAuth(t)
	_, err := svc.ScopeOf(Principal{CredentialID: "never-existed"})
	if !errors.Is(err, ErrCredentialRevoked) {
		t.Fatalf("got %v — want ErrCredentialRevoked rather than an admin scope", err)
	}
}
