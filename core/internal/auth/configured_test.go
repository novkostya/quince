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

// qn.6m D3 — "configured" means a password OR at least one passkey.
//
// WITHOUT THIS, PASSWORDLESS IS AN UNAUTHENTICATED ADMIN TAKEOVER. Nothing in the project can yet
// PRODUCE a passwordless install — removing the password is slice 5's endpoint and does not exist —
// so every test below seeds the state directly. That is the point of landing this first: the guard
// exists before the thing it guards against can happen, which is the same ordering ruling that put
// `quince auth reset` ahead of the first credential in `qn.6k`.

func newConfiguredService(t *testing.T) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "q.db"))
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewService(st, slog.New(slog.NewTextHandler(io.Discard, nil))), st
}

// `example.com` / `example.net` throughout — a real domain is Operator-private, so fixtures never
// carry one (the spec's Rule check).
func seedPasskey(t *testing.T, st *store.Store, credID, rpID string) {
	t.Helper()
	if err := st.InsertPasskey(store.Passkey{
		CredentialID: credID,
		PublicKey:    []byte("cose"),
		RPID:         rpID,
		Name:         "phone",
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed passkey: %v", err)
	}
}

func TestConfiguredIsFalseOnAVirginInstall(t *testing.T) {
	svc, _ := newConfiguredService(t)

	ok, err := svc.Configured()
	if err != nil {
		t.Fatalf("Configured: %v", err)
	}
	if ok {
		t.Fatal("a store with no password and no passkey must NOT be configured — first run has to be reachable")
	}
	state, err := svc.Status("")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state != StateNeedsSetup {
		t.Fatalf("Status = %q, want %q", state, StateNeedsSetup)
	}
}

// THE CORE OF D3. A passwordless install has been claimed, so an anonymous visitor is offered
// LOGIN — never first run.
func TestPasskeyOnlyInstallIsConfiguredAndAsksForLogin(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", "example.com")

	ok, err := svc.Configured()
	if err != nil {
		t.Fatalf("Configured: %v", err)
	}
	if !ok {
		t.Fatal("an install holding a passkey and no password IS configured")
	}
	state, err := svc.Status("")
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if state != StateNeedsLogin {
		t.Fatalf("Status = %q, want %q — a passwordless install must offer login, never first run", state, StateNeedsLogin)
	}
}

// THE TAKEOVER ITSELF, asserted as the refusal that prevents it. Before D3 this call SUCCEEDED and
// the caller was handed an admin session by `issueSessionResponse`.
func TestSetupIsRefusedOnAPasskeyOnlyInstall(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", "example.com")

	err := svc.SetPassword("hunter2", "203.0.113.1")
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("SetPassword on a passkey-only install = %v, want ErrAlreadyConfigured (409)", err)
	}

	// AND IT REALLY DID NOT WRITE. A 409 that had already stored the hash would be worse than no
	// guard at all: the stranger's password would be live and the refusal would hide it.
	if _, ok, _ := st.GetSetting(settingPasswordHash); ok {
		t.Fatal("refused setup must not have stored a password hash")
	}
}

// THE rpId SUBTLETY, AND IT IS THE ONE A REASONABLE PERSON GETS WRONG. `existingCredentials` filters
// by rpId because a credential bound elsewhere cannot SIGN IN here. This question is different —
// "has this install been claimed" — so it must NOT filter, or the takeover reopens through the
// second address of a quince reachable at two.
func TestClaimedCountIgnoresTheRPID(t *testing.T) {
	svc, st := newConfiguredService(t)
	// Registered for a DIFFERENT domain than the one being asked about.
	seedPasskey(t, st, "cred-elsewhere", "example.net")

	ok, err := svc.Configured()
	if err != nil {
		t.Fatalf("Configured: %v", err)
	}
	if !ok {
		t.Fatal("a credential bound to another domain still CLAIMS the install — filtering here reopens the takeover")
	}
	if err := svc.SetPassword("hunter2", "203.0.113.1"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("SetPassword = %v, want ErrAlreadyConfigured even for an off-domain credential", err)
	}
}

// First run must still WORK. A guard that closes the takeover by closing setup would be a denial of
// service on every fresh install, and this is the test that says it did not.
func TestFirstRunStillSucceedsWhenNothingHasClaimedTheInstall(t *testing.T) {
	svc, _ := newConfiguredService(t)

	if err := svc.SetPassword("hunter2", "203.0.113.1"); err != nil {
		t.Fatalf("first-run SetPassword: %v", err)
	}
	// And exactly once — the pre-existing one-shot rule is untouched by D3.
	if err := svc.SetPassword("again", "203.0.113.1"); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("second SetPassword = %v, want ErrAlreadyConfigured", err)
	}
}

// `quince auth reset` clears BOTH, so it must return the box to genuinely unclaimed — otherwise the
// recovery path that ruling B leans its whole cost on would leave an install nobody can set up
// again. Slice 5 makes this the primary way back in; it is worth asserting before then.
func TestResetReturnsTheInstallToUnclaimed(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("hunter2", "203.0.113.1"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	seedPasskey(t, st, "cred-1", "example.com")

	if _, err := Reset(st); err != nil {
		t.Fatalf("Reset: %v", err)
	}

	ok, err := svc.Configured()
	if err != nil {
		t.Fatalf("Configured: %v", err)
	}
	if ok {
		t.Fatal("after a reset the install must be unclaimed, or first run is unreachable and the box is bricked")
	}
	if err := svc.SetPassword("fresh", "203.0.113.1"); err != nil {
		t.Fatalf("SetPassword after reset: %v", err)
	}
}
