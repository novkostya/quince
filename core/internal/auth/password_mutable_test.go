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

	if err := svc.ChangePassword(NewProofs(), Presented{Password: "not-it"}, "new-one", sess, ip); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("wrong current = %v, want ErrBadPassword (401)", err)
	}
	// AND IT DID NOT WRITE. A refusal that had already replaced the hash would be the worst of both.
	if _, _, err := svc.Login("old-one", ip, ""); err != nil {
		t.Fatalf("the old password stopped working after a REFUSED change: %v", err)
	}
}

func TestChangeReplacesThePasswordAndRetiresTheOld(t *testing.T) {
	svc, _ := configured(t, "old-one")

	if err := svc.ChangePassword(NewProofs(), Presented{Password: "old-one"}, "new-one", sess, ip); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	if _, _, err := svc.Login("new-one", ip, ""); err != nil {
		t.Fatalf("the new password does not work: %v", err)
	}
	if _, _, err := svc.Login("old-one", ip, ""); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("the OLD password still works after a change (%v) — the hash was not replaced", err)
	}
}

// THIS TEST HAS INVERTED — qn.6n rule 1, gate G6. It was
// `TestChangeOnAPasswordlessInstallNeedsNoCurrent`, and it asserted that a passwordless install
// could set a password with an empty `current`. That WAS the design (qn.6m D4, *"change IS set"*),
// and quince#888 item 3's table named it as the row proved by NOTHING:
//
//	set a password on a passwordless install | nothing — an empty current_password is accepted by
//	design | mints a credential the owner cannot revoke without console access
//
// The simplification is still right and is untouched: one endpoint, and the server decides which
// spelling applies from its own state. What changed is that the spelling with no password to type
// now needs the PASSKEY instead of needing nothing.
//
// Inverted in place rather than deleted and replaced, because a reader who knew the old behaviour
// needs to see that it was removed on purpose.
func TestSettingAPasswordOnAPasswordlessInstallNowNeedsThePasskey(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here) // configured by credential, no password row

	if err := svc.ChangePassword(NewProofs(), Presented{}, "brand-new", sess, ip); !errors.Is(err, ErrNoProof) {
		t.Fatalf("setting a password with nothing presented = %v, want ErrNoProof", err)
	}
	// AND IT DID NOT WRITE. A refusal that had already set the hash would be the worst of both: the
	// credential minted, and the attempt reported as prevented.
	if _, _, err := svc.Login("brand-new", ip, ""); !errors.Is(err, ErrNoPassword) {
		t.Fatalf("a password was set despite the refusal: %v", err)
	}
}

// AND THE PASSKEY IS WHAT LETS IT THROUGH — the other half of the pair above. Rule 1 is a
// requirement, not a prohibition: a passwordless install must still be able to ADD a password, and
// that is the remedy `/settings/auth` points at.
func TestAProofSetsThePasswordOnAPasswordlessInstall(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here)

	proofs := NewProofs()
	tok, err := proofs.Mint(OpSetPassword, "", ProofSubject{CredentialID: "cred-1"}, sess)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if err := svc.ChangePassword(proofs, Presented{Proof: tok}, "brand-new", sess, ip); err != nil {
		t.Fatalf("ChangePassword with a proof: %v", err)
	}
	if _, _, err := svc.Login("brand-new", ip, ""); err != nil {
		t.Fatalf("the password just set does not work: %v", err)
	}
}

// G5 — THE EXEMPTION AND THE RULE ASSERTED TOGETHER, ON TWO STORES DIFFERING BY ONE ROW.
//
// The exception and the rule are one decision, and a test proving only the exemption would pass on a
// build that exempted everything. `Configured()` is the predicate, so the exemption cannot drift
// from the state that makes first run legal in the first place.
func TestOnlyAnUnclaimedInstallIsExemptFromPresentingSomething(t *testing.T) {
	// No password, no passkeys — unclaimed. Nothing to present, and nothing yet to protect.
	virgin, _ := newConfiguredService(t)
	if err := virgin.ChangePassword(NewProofs(), Presented{}, "first-one", sess, ip); err != nil {
		t.Fatalf("an unclaimed install refused a password: %v — first run would be unrecoverable", err)
	}

	// One passkey, no password — CLAIMED, and therefore not exempt. One row is the whole difference.
	claimed, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here)
	if err := claimed.ChangePassword(NewProofs(), Presented{}, "first-one", sess, ip); !errors.Is(err, ErrNoProof) {
		t.Fatalf("a claimed install was exempt = %v, want ErrNoProof", err)
	}
}

// The empty-string case must NOT become a way past the check once a password exists.
func TestChangeRejectsAnEmptyCurrentWhenAPasswordExists(t *testing.T) {
	svc, _ := configured(t, "old-one")

	if err := svc.ChangePassword(NewProofs(), Presented{}, "new-one", sess, ip); !errors.Is(err, ErrBadPassword) {
		t.Fatalf("empty current against a set password = %v, want ErrBadPassword", err)
	}
}

// SHARED BUCKET WITH LOGIN AND SETUP — it verifies a password, so it is a credential endpoint, and
// holding a session must not buy a fresh budget to guess the current password in.
func TestChangeIsRateLimitedOnTheLoginBucket(t *testing.T) {
	svc, _ := configured(t, "old-one")

	var lastErr error
	for i := 0; i < 40; i++ {
		lastErr = svc.ChangePassword(NewProofs(), Presented{Password: "wrong"}, "new-one", sess, ip)
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
