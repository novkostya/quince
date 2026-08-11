package auth

import (
	"errors"
	"testing"
)

// qn.6m D5 — first-run passkey registration is PRE-AUTH, so the one-shot guard is the whole of its
// safety. These tests are about that guard; the ceremony itself needs a real authenticator and is
// covered by the library's own vectors in passkey_test.go.

// THE GUARD IS `Configured()`, NOT `HasPassword()`, which is what makes the pair close after it has
// been used once: D3 counts a passkey as claiming the install, so the credential the FIRST caller
// created is what refuses the second.
func TestFirstRunPasskeyRefusesOnceAPasskeyExists(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here)

	_, _, err := svc.BeginSetupPasskey(NewPasskeyCeremonies(), here, ip)
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("BeginSetupPasskey on a claimed install = %v, want ErrAlreadyConfigured", err)
	}
}

func TestFirstRunPasskeyRefusesOnceAPasswordExists(t *testing.T) {
	svc, _ := newConfiguredService(t)
	if err := svc.SetPassword("hunter2", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}

	_, _, err := svc.BeginSetupPasskey(NewPasskeyCeremonies(), here, ip)
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("BeginSetupPasskey with a password set = %v, want ErrAlreadyConfigured", err)
	}
}

// AND IT IS OPEN ON A VIRGIN INSTALL, or the rung's whole point is unreachable. A guard that closed
// both ways would be indistinguishable from the feature not existing.
func TestFirstRunPasskeyIsOpenOnAnUnclaimedInstall(t *testing.T) {
	svc, _ := newConfiguredService(t)

	_, ceremony, err := svc.BeginSetupPasskey(NewPasskeyCeremonies(), here, ip)
	if err != nil {
		t.Fatalf("BeginSetupPasskey on a virgin install: %v", err)
	}
	if ceremony == "" {
		t.Fatal("no ceremony key returned")
	}
}

// SHARED BUCKET WITH LOGIN AND SETUP. This is a pre-auth endpoint that allocates on an install
// anybody can reach, so an unmetered version is an amplifier — the shape quince#463 measured on
// setup, cheaper here but the same kind.
func TestFirstRunPasskeyIsRateLimited(t *testing.T) {
	svc, _ := newConfiguredService(t)

	var last error
	for i := 0; i < 40; i++ {
		_, _, last = svc.BeginSetupPasskey(NewPasskeyCeremonies(), here, ip)
		if errors.Is(last, ErrRateLimited) {
			break
		}
	}
	if !errors.Is(last, ErrRateLimited) {
		t.Fatalf("never rate limited after 40 attempts (last=%v)", last)
	}
}

// THE RACE'S LOSER IS REFUSED AT FINISH, not only at begin. Two clients can both pass Begin on a
// virgin install; the credential write is what decides, and re-checking at finish means the second
// gets a 409 rather than silently adding a second admin credential to somebody else's box.
func TestFirstRunPasskeyFinishRechecksTheGuard(t *testing.T) {
	svc, st := newConfiguredService(t)
	cer := NewPasskeyCeremonies()

	// Begin while unclaimed — this is the ceremony the loser is holding.
	if _, _, err := svc.BeginSetupPasskey(cer, here, ip); err != nil {
		t.Fatalf("BeginSetupPasskey: %v", err)
	}
	// Somebody else claims the install in the meantime.
	seedPasskey(t, st, "cred-winner", here)

	_, _, _, err := svc.FinishSetupPasskey(cer, "any-key", "phone", here, nil, svc.now(), "")
	if !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("FinishSetupPasskey after the install was claimed = %v, want ErrAlreadyConfigured", err)
	}
}
