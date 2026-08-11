package auth

import (
	"errors"
	"testing"
)

// qn.6m D4 — the password becomes mutable. Three operations, three different guards, and the two
// added here are the ones that did not exist before:
//
//	set     pre-auth, one-shot, 409 once configured   (unchanged; D3 widened its 409)
//	change  session + the CURRENT password
//	remove  session + a passkey FOR THIS rpId

const (
	here      = "example.com"
	elsewhere = "example.net"
	ip        = "203.0.113.1"
)

func configured(t *testing.T, pw string) (*Service, func()) {
	t.Helper()
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword(pw, ip); err != nil {
		t.Fatalf("seed password: %v", err)
	}
	return svc, func() { _ = st }
}

// THE CURRENT PASSWORD IS REQUIRED EVEN THOUGH THE CALLER HOLDS A SESSION, and this is the test that
// says why it is worth the field. A session is proof of a PAST authentication; the one irreversible
// thing an attacker with a stolen cookie can do is change the password and keep the owner out.
func TestChangeRequiresTheCurrentPassword(t *testing.T) {
	svc, _ := configured(t, "old-one")

	if err := svc.ChangePassword("not-it", "new-one", ip); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong current = %v, want ErrBadPassword (401)", err)
	}
	// AND IT DID NOT WRITE. A refusal that had already replaced the hash would be the worst of both.
	if _, _, err := svc.Login("old-one", ip, ""); err != nil {
		t.Fatalf("the old password stopped working after a REFUSED change: %v", err)
	}
}

func TestChangeReplacesThePasswordAndRetiresTheOld(t *testing.T) {
	svc, _ := configured(t, "old-one")

	if err := svc.ChangePassword("old-one", "new-one", ip); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, _, err := svc.Login("new-one", ip, ""); err != nil {
		t.Fatalf("the new password does not work: %v", err)
	}
	if _, _, err := svc.Login("old-one", ip, ""); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("the OLD password still works after a change (%v) — the hash was not replaced", err)
	}
}

// ON A PASSWORDLESS INSTALL "CHANGE" IS "SET", served by the same endpoint with `current` absent. A
// separate add-a-password endpoint would be a fourth spelling of one idea, and the state that
// decides which spelling applies is server-side anyway.
func TestChangeOnAPasswordlessInstallNeedsNoCurrent(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here) // configured by credential, no password row

	if err := svc.ChangePassword("", "brand-new", ip); err != nil {
		t.Fatalf("ChangePassword on a passwordless install: %v", err)
	}
	if _, _, err := svc.Login("brand-new", ip, ""); err != nil {
		t.Fatalf("the password just set does not work: %v", err)
	}
}

// The empty-string case must NOT become a way past the check once a password exists.
func TestChangeRejectsAnEmptyCurrentWhenAPasswordExists(t *testing.T) {
	svc, _ := configured(t, "old-one")

	if err := svc.ChangePassword("", "new-one", ip); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("empty current against a set password = %v, want ErrBadPassword", err)
	}
}

// SHARED BUCKET WITH LOGIN AND SETUP — it verifies a password, so it is a credential endpoint, and
// holding a session must not buy a fresh budget to guess the current password in.
func TestChangeIsRateLimitedOnTheLoginBucket(t *testing.T) {
	svc, _ := configured(t, "old-one")

	var lastErr error
	for i := 0; i < 40; i++ {
		lastErr = svc.ChangePassword("wrong", "new-one", ip)
		if errors.Is(lastErr, ErrRateLimited) {
			break
		}
	}
	if !errors.Is(lastErr, ErrRateLimited) {
		t.Fatalf("never rate limited after 40 wrong attempts (last=%v)", lastErr)
	}
	// The bucket is SHARED: login from the same client is now refused too, which is the point.
	if _, _, err := svc.Login("old-one", ip, ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("login = %v, want ErrRateLimited — change and login must share one bucket", err)
	}
}

// ── remove ────────────────────────────────────────────────────────────────────────────────────

// THE LOCKOUT GUARD. Removing the last way in is refused, and the refusal says what is missing
// rather than failing generically.
func TestRemoveIsRefusedWithNoPasskeyAtAll(t *testing.T) {
	svc, _ := configured(t, "old-one")

	err := svc.RemovePassword(here, ip)
	var last ErrLastCredential
	if !errors.As(err, &last) {
		t.Fatalf("RemovePassword with no passkey = %v, want ErrLastCredential", err)
	}
	if _, _, err := svc.Login("old-one", ip, ""); err != nil {
		t.Fatalf("the password was removed despite the refusal: %v", err)
	}
}

func TestRemoveSucceedsWithAPasskeyForThisAddress(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, st, "cred-1", here)

	if err := svc.RemovePassword(here, ip); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}
	if _, _, err := svc.Login("old-one", ip, ""); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("login after removal = %v, want ErrNoPassword", err)
	}
	// STILL CONFIGURED — D3. The install is passwordless, not unclaimed, so first run stays closed.
	if ok, err := svc.Configured(); err != nil || !ok {
		t.Fatalf("Configured = %v (err=%v) after going passwordless — first run must stay closed", ok, err)
	}
	if err := svc.SetPassword("stranger", ip); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("SetPassword on a passwordless install = %v, want ErrAlreadyConfigured", err)
	}
}

// THE rpId FILTER, AND IT IS THE OPPOSITE OF Configured()'s. A credential bound to another domain
// cannot sign in HERE, so counting it would let somebody remove their password and lock themselves
// out — with the phone still listing a passkey that cannot help.
func TestRemoveRefusesWhenEveryPasskeyBelongsElsewhere(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, st, "cred-elsewhere", elsewhere)

	err := svc.RemovePassword(here, ip)
	var last ErrLastCredential
	if !errors.As(err, &last) {
		t.Fatalf("RemovePassword = %v, want ErrLastCredential", err)
	}
	// THE MESSAGE IS THE WHOLE VALUE, same as ErrRPIDMismatch: "no passkeys" at a box that visibly
	// has one reads as quince being broken.
	if last.Presented != here {
		t.Errorf("Presented = %q, want %q", last.Presented, here)
	}
	if len(last.Elsewhere) != 1 || last.Elsewhere[0] != elsewhere {
		t.Errorf("Elsewhere = %v, want [%s]", last.Elsewhere, elsewhere)
	}
	if _, _, err := svc.Login("old-one", ip, ""); err != nil {
		t.Fatalf("the password went despite the refusal: %v", err)
	}
}

// Configured() must NOT filter and RemovePassword() must — asserted together, on ONE store, because
// the bug is using the same rule for both and neither test alone catches it.
func TestTheTwoRPIDRulesAreOpposite(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, st, "cred-elsewhere", elsewhere)

	// Claimed — unfiltered, so an off-domain credential counts and first run stays closed.
	if ok, err := svc.Configured(); err != nil || !ok {
		t.Fatalf("Configured = %v (err=%v), want true — claiming does NOT filter by rpId", ok, err)
	}
	// Can sign in here — filtered, so the same credential does NOT permit removal.
	if err := svc.RemovePassword(here, ip); !errors.As(err, &ErrLastCredential{}) {
		t.Fatalf("RemovePassword = %v, want ErrLastCredential — signing in DOES filter by rpId", err)
	}
}
