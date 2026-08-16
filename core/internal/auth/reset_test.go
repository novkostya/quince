package auth

import (
	"database/sql"
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

// THE PARTIAL-FAILURE PATH — quince#1032, carried out of quince#841's ruling B.
//
// `Reset` is DELIBERATELY NOT ATOMIC, and its comment argues the case: three statements on one local
// file, run by hand at a console, where *"wrapping them in a transaction would make a partial failure
// invisible rather than impossible."* The argument is sound. Every clause of it was also a testable
// claim that nothing tested — the four tests above are all happy paths.
//
// THAT WAS FINE WHILE RESET WAS A BACKSTOP. quince#841 ruling B shipped passwordless and said what
// it cost: *"quince auth reset stops being a backstop and becomes THE recovery path … its
// partial-failure path is untested — acceptable for a backstop, less so for the only way back in."*
//
// THE SECOND STATEMENT IS BROKEN BY RENAMING ITS TABLE AWAY, on a second handle to the same file. The
// driver is already registered by `store`'s blank import, so this needs no new dependency — and it
// fails the way a real failure does, inside the store call, rather than through a seam added to the
// production type for the benefit of a test.
// RENAMED AWAY RATHER THAN DROPPED, so the break is REVERSIBLE — which is what makes it a model of
// the failure this path is about. The recoverability claim concerns a TRANSIENT failure: a lock, a
// full disk, something that is gone by the second run. A dropped table is a permanent break and
// would test the wrong thing.
//
// AND REOPENING DOES NOT REPAIR ONE, which is worth knowing and is how the second test first failed:
// `store.Open` runs migrations, but applied migrations are RECORDED, so a table dropped afterwards
// is not recreated by opening the file again. The repair has to be the reverse of the break.
func breakPasskeys(t *testing.T, path string) {
	t.Helper()
	execRaw(t, path, "ALTER TABLE passkeys RENAME TO passkeys_broken")
}

func repairPasskeys(t *testing.T, path string) {
	t.Helper()
	execRaw(t, path, "ALTER TABLE passkeys_broken RENAME TO passkeys")
}

func execRaw(t *testing.T, path, stmt string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("second handle: %v", err)
	}
	defer func() { _ = raw.Close() }()
	if _, err := raw.Exec(stmt); err != nil {
		t.Fatalf("%s: %v", stmt, err)
	}
}

// THE ORDERING CLAIM, WHICH IS THE ONE WITH A SECURITY CONSEQUENCE. `Reset` deletes the password
// FIRST, *"so that a failure in either step below still leaves the box unable to accept the old
// password."* That property was held in place by a comment: reorder the three statements and nothing
// would have failed.
func TestResetClearsThePasswordEvenWhenALaterStepFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	seedPasskey(t, st, "cre1", here)

	breakPasskeys(t, path)

	out, err := Reset(st)
	if err == nil {
		t.Fatal("Reset succeeded with the passkeys table gone — the failure was swallowed")
	}
	// IT STOPS AT THE FIRST ERROR rather than pressing on.
	if out.Passkeys != 0 || out.Sessions != 0 {
		t.Fatalf("kept going past the failure: %+v", out)
	}
	// AND IT REPORTS WHAT HAD ALREADY GONE, which is what makes the state recoverable rather than
	// unknown — the caller can say so at the console.
	if !out.HadPassword {
		t.Fatal("the result does not report the password that was already removed")
	}
	// THE PROPERTY THAT MATTERS: the old password no longer opens the box, despite the reset having
	// failed part-way.
	if _, _, err := svc.Login("old-one", ip, ""); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("the old password survived a partial reset: %v", err)
	}
}

// RE-RUNNING AFTER A PARTIAL FAILURE REACHES A CLEAN STATE — the other half of the comment's claim,
// *"because re-running is a no-op on what is already clear."* The idempotence test above covers a
// re-run after a CLEAN one, which is the easy case.
//
// THE FAILURE IS UNDONE, NOT WORKED AROUND — a transient fault clearing, which is the case the
// comment's *"re-running is a no-op on what is already clear"* is about. The operator's second run
// then has to finish the job rather than trip over what the first one already did.
func TestResetIsRecoverableByRunningItAgain(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "q.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	svc := NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	seedPasskey(t, st, "cre1", here)

	breakPasskeys(t, path)
	if _, err := Reset(st); err == nil {
		t.Fatal("precondition: the first reset was expected to fail")
	}
	repairPasskeys(t, path)

	out, err := Reset(st)
	if err != nil {
		t.Fatalf("the second run did not succeed: %v", err)
	}
	// THE SECOND RUN DID THE WORK THE FIRST ONE COULD NOT. Without this the test passes for the wrong
	// reason: a run that found nothing left to do satisfies every other assertion here, so *"it
	// completed"* and *"it completed the remaining work"* were indistinguishable.
	//
	// Added on the architect's retirement note against this PR — *a PR adding partial-failure
	// coverage is exactly where to ask whether the NEW assertions can pass for the reason they
	// state.* This one could not, and the passkey the failed run left behind is what tells them
	// apart.
	if out.Passkeys != 1 {
		t.Fatalf("the second run removed %d passkeys, want the 1 the failed run left behind: %+v",
			out.Passkeys, out)
	}
	// THE PASSWORD IS NOT REPORTED TWICE. It went in the first run, so a second `HadPassword` would
	// mean the first had lied about what it did.
	if out.HadPassword {
		t.Fatalf("reported removing a password that was already gone: %+v", out)
	}
	configured, err := svc.Configured()
	if err != nil {
		t.Fatalf("Configured: %v", err)
	}
	if configured {
		t.Fatal("the install is still claimed after a completed reset")
	}
}
