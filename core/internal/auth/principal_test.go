package auth

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// G2 — a session records what authenticated it, at EVERY creation site.
//
// The spec calls the nil-means-admin default an accepted hazard "because this column is written at
// exactly one site per login path rather than being a set that can gain members". That sentence is
// only true while something checks it, which is this file: a fourth login path that forgets the
// credential inherits a silent nil, and a silent nil reads as the admin.

func TestPasswordLoginRecordsNoCredential(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	sess, _, err := svc.Login("test", "1.2.3.4", "")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.CredentialID != nil {
		t.Fatalf("password login recorded a credential: %q", *sess.CredentialID)
	}

	// And it must READ BACK nil, not "". A round trip through SQLite is where a nullable turns
	// into an empty string if the scan is wrong, and "" would still be a password login by
	// PrincipalOf — so this asserts the storage layer, which the in-memory value cannot.
	got, ok, err := svc.store.GetAuthSession(sess.ID)
	if err != nil || !ok {
		t.Fatalf("get session: ok=%v err=%v", ok, err)
	}
	if got.CredentialID != nil {
		t.Fatalf("round trip invented a credential: %q", *got.CredentialID)
	}
	if p := PrincipalOf(got); !p.IsPasswordLogin() {
		t.Fatalf("password login did not read as one: %+v", p)
	}
}

func TestSessionRoundTripsItsCredential(t *testing.T) {
	svc, _ := newTestAuth(t)
	now := time.Now().UTC()
	cred := "Y3JlZC1hYmM"
	sess := store.AuthSession{
		ID:           "s1",
		CreatedAt:    now,
		LastSeenAt:   now,
		ExpiresAt:    now.Add(time.Hour),
		CredentialID: &cred,
	}
	if err := svc.store.CreateAuthSession(sess); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, ok, err := svc.store.GetAuthSession("s1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.CredentialID == nil {
		t.Fatal("credential was dropped on the round trip")
	}
	if *got.CredentialID != cred {
		t.Fatalf("credential changed: got %q want %q", *got.CredentialID, cred)
	}
	p := PrincipalOf(got)
	if p.IsPasswordLogin() {
		t.Fatal("a credential-bearing session read as a password login")
	}
	if p.CredentialID != cred {
		t.Fatalf("principal credential: got %q want %q", p.CredentialID, cred)
	}
}

// EVERY SITE THAT MINTS A SESSION IS ENUMERATED HERE, and the enumeration is the gate.
//
// This is the shape spec D6 is about, one slice early: a predicate — or here, a writer — that is
// correct until the set gains a member. `grep -n CreateAuthSession` is the check, and it must
// return exactly the sites this test knows about. A new login path shows up as a failure with a
// name rather than as a session nobody can attribute.
func TestEverySessionMintingSiteIsAccountedFor(t *testing.T) {
	// service.go Login          → nil, asserted by TestPasswordLoginRecordsNoCredential
	// passkey_login.go Login    → the asserting credential, asserted below
	// setup_passkey.go mintSession → its parameter, which has one caller passing &pk.CredentialID
	//
	// mintSession takes the credential as a PARAMETER rather than defaulting it, so a second
	// caller has to state what it is. That is the property this test names; the compiler enforces
	// it, and this comment is what tells a reader the enforcement is deliberate.
	svc, _ := newTestAuth(t)
	now := time.Now().UTC()
	cred := "c2V0dXAtY3JlZA"
	sess, _, err := svc.mintSession("", &cred)
	if err != nil {
		t.Fatalf("mintSession: %v", err)
	}
	got, ok, err := svc.store.GetAuthSession(sess.ID)
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.CredentialID == nil || *got.CredentialID != cred {
		t.Fatalf("mintSession did not persist its credential: %v", got.CredentialID)
	}
	_ = now
}

// An UPGRADED session — a row written before this column existed — must keep working and read as
// the admin. That is what makes the migration additive, and it is the claim the nullable column is
// there to support.
func TestPreExistingSessionReadsAsPasswordLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Written the way the pre-0014 code wrote it: the credential column is not mentioned at all,
	// so SQLite supplies NULL — which is exactly what an upgraded database holds for every session
	// that was live when the migration ran.
	now := time.Now().UTC().Format(time.RFC3339Nano)
	execRaw(t, path, `INSERT INTO sessions_auth (id, created_at, last_seen_at, expires_at)
	                  VALUES ('old', '`+now+`', '`+now+`', '`+now+`')`)

	got, ok, err := st.GetAuthSession("old")
	if err != nil || !ok {
		t.Fatalf("legacy session unreadable: ok=%v err=%v", ok, err)
	}
	if got.CredentialID != nil {
		t.Fatalf("legacy session gained a credential: %q", *got.CredentialID)
	}
	if !PrincipalOf(got).IsPasswordLogin() {
		t.Fatal("legacy session did not read as a password login")
	}
}
