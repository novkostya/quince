package auth

import (
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// newResetStore is a bare store on a temp DB — no Service, because three of the four tests below
// exercise Reset directly and only one needs the auth surface.
func newResetStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// seedReset puts a box into the state a reset is FOR: a password set, a live session, and a passkey
// registered. Every assertion below is about what survives that.
//
// `example.com` as the rpId is the spec's Rule check holding: a real domain is Operator-private, so
// fixtures never carry one.
func seedReset(t *testing.T) *store.Store {
	t.Helper()
	st := newResetStore(t)

	if err := st.SetSetting(settingPasswordHash, "not-a-real-hash"); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	now := time.Now().UTC()
	if err := st.CreateAuthSession(store.AuthSession{
		ID: "sess-1", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: "cred-1",
		PublicKey:    []byte("cose"),
		RPID:         "example.com",
		Name:         "phone",
		CreatedAt:    now,
	}); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
	return st
}

// THE WHOLE POINT OF SLICE 2. Ruled on quince#657: the escape hatch ships before any credential can
// be issued, and it clears the credential as well as the password — a credential list the
// locked-out user cannot reach is not recovery.
func TestResetClearsPasswordPasskeysAndSessions(t *testing.T) {
	st := seedReset(t)

	res, err := Reset(st)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}

	if !res.HadPassword {
		t.Error("HadPassword = false, want true — a password was seeded")
	}
	if res.Passkeys != 1 {
		t.Errorf("Passkeys = %d, want 1", res.Passkeys)
	}
	if res.Sessions != 1 {
		t.Errorf("Sessions = %d, want 1", res.Sessions)
	}

	if _, ok, err := st.GetSetting(settingPasswordHash); err != nil || ok {
		t.Errorf("password still set (ok=%v, err=%v)", ok, err)
	}
	if n, err := st.CountPasskeys(); err != nil || n != 0 {
		t.Errorf("passkeys remain: %d (err=%v)", n, err)
	}
	if _, ok, err := st.GetAuthSession("sess-1"); err != nil || ok {
		t.Errorf("session survived the reset (ok=%v, err=%v)", ok, err)
	}
}

// THE HOLE THAT MAKES THE SESSION CLEAR NON-OPTIONAL, pinned as behaviour rather than as a comment.
//
// `Authenticate` never consults the password — it checks the session row and its expiry, nothing
// else. So a reset that cleared only the password would leave a live cookie passing `authGuard` and
// reaching every protected route, while the UI reads `needs_setup` off `Status()`, which DOES
// short-circuit. The box would look reset and still be authenticated.
//
// This test asserts BOTH halves: that the hole is real if you only clear the password, and that
// Reset closes it. Delete the session clear from Reset and the second half fails.
//
// HALF 1 WAS REWRITTEN BY qn.6m D3, AND THE HOLE IT DOCUMENTS SURVIVED THE REWRITE. It used to
// assert that a password-only clear made `Status()` read `needs_setup` — the box LOOKING reset while
// `Authenticate` still passed. Under D3 `Status()` is computed from `Configured()`, which is a
// password OR a passkey, and this fixture seeds both — so after clearing the password the install is
// still claimed by its credential and `Status()` no longer claims to be reset at all.
//
// That is a BETTER outcome behind a WORSE-LOOKING symptom, which is why the assertion moved rather
// than being deleted: the box now reads `authenticated` outright instead of contradicting itself.
// What has not changed is the thing Reset's third step exists for — `Authenticate` never consults
// the password, so a live cookie still reaches every protected route.
func TestResetClosesTheStaleSessionHole(t *testing.T) {
	st := seedReset(t)
	svc := NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Half 1 — clearing ONLY the password leaves the session usable. If this ever stops holding,
	// `Authenticate` grew a password check and Reset's third step may be redundant.
	if _, err := st.DeleteSetting(settingPasswordHash); err != nil {
		t.Fatalf("clear password: %v", err)
	}
	if _, err := svc.Authenticate("sess-1"); err != nil {
		t.Fatalf("Authenticate rejected the session after a password-only clear (%v) — "+
			"the hole this test documents is gone; re-read auth.Reset's third step", err)
	}
	// D3's half: the passkey still claims the install, so first run stays CLOSED. A half-reset that
	// reopened `POST /api/auth/setup` would be the takeover reached by clearing one row.
	if ok, err := svc.Configured(); err != nil || !ok {
		t.Fatalf("Configured = %v (err=%v) after a password-only clear — the passkey still claims this install", ok, err)
	}
	if err := svc.SetPassword("stranger", "203.0.113.1"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("SetPassword after a password-only clear = %v, want ErrAlreadyConfigured", err)
	}

	// Half 2 — the real Reset closes it.
	if _, err := Reset(st); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if _, err := svc.Authenticate("sess-1"); err == nil {
		t.Error("session STILL authenticates after Reset — the box looks reset and is not")
	}
}

// An unconfigured box is a legitimate no-op, not a failure: the operator who runs this on a fresh
// install should get a clean report rather than an error to interpret.
func TestResetOnUnconfiguredBoxIsACleanNoOp(t *testing.T) {
	st := newResetStore(t)

	res, err := Reset(st)
	if err != nil {
		t.Fatalf("Reset on a fresh box: %v", err)
	}
	if res.HadPassword || res.Passkeys != 0 || res.Sessions != 0 {
		t.Errorf("got %+v, want a zero result on a fresh box", res)
	}
}

// Re-running must be safe, because the documented failure story is "it stopped partway, run it
// again" — which is only true if the second run cannot error on what the first already cleared.
func TestResetIsIdempotent(t *testing.T) {
	st := seedReset(t)

	if _, err := Reset(st); err != nil {
		t.Fatalf("first Reset: %v", err)
	}
	res, err := Reset(st)
	if err != nil {
		t.Fatalf("second Reset: %v", err)
	}
	if res.HadPassword || res.Passkeys != 0 || res.Sessions != 0 {
		t.Errorf("second run reported %+v, want all-zero", res)
	}
}
