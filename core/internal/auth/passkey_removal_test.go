package auth

import (
	"errors"
	"testing"
)

// quince#888 item 1 — the mirror `RemovePassword` never had.
//
// The asymmetry was the whole bug: one removal path asked "will anything be left?" and the other
// asked nothing at all, because DELETE /api/auth/passkeys/{id} went straight to the store. The
// sequence test at the bottom of this file is the finding itself, and it is the one to keep if any
// of these ever have to go.

// ── the takeover, as a sequence ────────────────────────────────────────────────────────────────

// THE FINDING, END TO END, IN THE ORDER THE OPERATOR REACHED IT ON A STAND. Two clicks from
// Settings, both individually allowed, together emptying the install:
//
//	remove the password  → allowed, a passkey exists ✔
//	remove that passkey  → allowed, NOTHING was checked ✘
//
// After which Configured() is false, GET /api/auth/status answers `needs_setup`, and
// POST /api/auth/setup is pre-auth by exact path — so whoever reaches the address next owns the
// backups. Asserted as a SEQUENCE rather than as two unit cases because neither step is wrong on
// its own; the defect only exists in their composition, which is exactly why review did not see it.
func TestTheTwoClickTakeoverIsRefusedAtTheSecondClick(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, st, "cred-1", here)

	// Click one — allowed then and now. A passkey for this address remains, so this is not a lockout.
	if err := svc.RemovePassword(here, ip); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}

	// Click two — the one that used to go through.
	_, err := svc.RemovePasskey("cred-1", here)
	if !errors.As(err, &ErrLastPasskey{}) {
		t.Fatalf("removing the last credential = %v, want ErrLastPasskey", err)
	}

	// AND THE STATE IS THE ONE THAT MATTERS, not merely the error. A refusal that had deleted the
	// row anyway would leave the takeover open while reporting that it had been prevented.
	if ok, err := svc.Configured(); err != nil || !ok {
		t.Fatalf("Configured = %v (err=%v) — the install is unclaimed, so first run is open to a stranger", ok, err)
	}
	if err := svc.SetPassword("stranger", ip); !errors.Is(err, ErrAlreadyConfigured) {
		t.Fatalf("SetPassword by a stranger = %v, want ErrAlreadyConfigured — this IS the takeover", err)
	}
}

// ── what it does NOT refuse ───────────────────────────────────────────────────────────────────

// A PASSWORD IS A WAY IN, so removing any passkey is fine on an install that has one. The guard
// exists to prevent a lockout, not to make credentials permanent — a user must be able to remove a
// passkey for a phone they no longer own.
func TestRemovingAPasskeyIsAllowedWhileAPasswordExists(t *testing.T) {
	svc, st := newConfiguredService(t)
	if err := svc.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, st, "cred-1", here)

	deleted, err := svc.RemovePasskey("cred-1", here)
	if err != nil {
		t.Fatalf("RemovePasskey with a password present: %v", err)
	}
	if !deleted {
		t.Fatal("reported no row deleted")
	}
	if n, err := st.CountPasskeys(); err != nil || n != 0 {
		t.Fatalf("CountPasskeys = %d (err=%v), want 0", n, err)
	}
}

// PASSWORDLESS WITH TWO PASSKEYS: removing one is allowed, because one still works here. This is
// the case a guard written as "refuse when passwordless" would break, and it is the ordinary one —
// retiring an old phone.
func TestPasswordlessCanStillRemoveAPasskeyWhenAnotherWorksHere(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here)
	seedPasskey(t, st, "cred-2", here)

	if _, err := svc.RemovePasskey("cred-1", here); err != nil {
		t.Fatalf("RemovePasskey with a second usable credential: %v", err)
	}
	if n, err := st.CountPasskeys(); err != nil || n != 1 {
		t.Fatalf("CountPasskeys = %d (err=%v), want 1", n, err)
	}
	// And the last one is now refused — the boundary is where it should be, not one row early.
	if _, err := svc.RemovePasskey("cred-2", here); !errors.As(err, &ErrLastPasskey{}) {
		t.Fatalf("removing the now-last credential = %v, want ErrLastPasskey", err)
	}
}

// ── the rpId filter, which is what makes this stronger than "do not empty the set" ────────────

// THE GUARD IS ABOUT SIGNING IN HERE, NOT ABOUT THE SET BEING NON-EMPTY, and this is the test that
// separates the two. A credential bound to another address survives the removal, so the set is not
// empty and the takeover is not open — and the user is still locked out of this address, which is
// precisely what RemovePassword has filtered by rpId for since qn.6m.
//
// A mirror written against Configured()'s unfiltered rule would pass every other test in this file
// and let this one through.
func TestRemovalIsRefusedWhenTheSURVIVORSOnlyWorkElsewhere(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-here", here)
	seedPasskey(t, st, "cred-elsewhere", elsewhere)

	_, err := svc.RemovePasskey("cred-here", here)
	var last ErrLastPasskey
	if !errors.As(err, &last) {
		t.Fatalf("RemovePasskey = %v, want ErrLastPasskey — the survivor cannot sign in here", err)
	}
	// The message names the addresses that WOULD REMAIN, so the refusal is an instruction. "No
	// passkeys" at a box that visibly has one reads as quince being broken (ErrRPIDMismatch's
	// reasoning, and ErrLastCredential's).
	if last.Presented != here {
		t.Errorf("Presented = %q, want %q", last.Presented, here)
	}
	if len(last.Elsewhere) != 1 || last.Elsewhere[0] != elsewhere {
		t.Errorf("Elsewhere = %v, want [%s]", last.Elsewhere, elsewhere)
	}
	if n, err := st.CountPasskeys(); err != nil || n != 2 {
		t.Fatalf("CountPasskeys = %d (err=%v), want 2 — the row went despite the refusal", n, err)
	}
	// AND THIS REMOVAL WAS REFUSED FOR LOCKOUT, NOT FOR TAKEOVER — the two reasons are different and
	// this is where they are visibly so. Had it gone through, the surviving off-domain row would have
	// kept `needs_setup` shut, so a guard aimed only at the takeover would have permitted it.
	if ok, err := svc.Configured(); err != nil || !ok {
		t.Fatalf("Configured = %v (err=%v) — first run was never the exposure in this case", ok, err)
	}
}

// ── the 204-whether-or-not-a-row-went rule, which the guard must not break ────────────────────

// DELETING AN ID THAT MATCHES NOTHING IS STILL A NO-OP AND STILL SUCCEEDS. The endpoint is
// deliberately indifferent to whether a row existed — a retry, or a second tab, must not look like a
// failure — and quince#888 asked whether that survives a refusal landing on the same handler.
//
// It survives because the guard is a claim about the RESULTING state: an id matching no row leaves
// the state unchanged, so it cannot be the last credential. No special case implements this; if the
// guard were ever rewritten as "is the row being deleted the last one", this test goes red.
func TestRemovingAnUnknownIDIsANoOpEvenOnAOneCredentialInstall(t *testing.T) {
	svc, st := newConfiguredService(t)
	seedPasskey(t, st, "cred-1", here)

	deleted, err := svc.RemovePasskey("no-such-credential", here)
	if err != nil {
		t.Fatalf("RemovePasskey on an unknown id = %v, want nil — the endpoint is indifferent to whether a row went", err)
	}
	if deleted {
		t.Fatal("reported a row deleted for an id that does not exist")
	}
	if n, err := st.CountPasskeys(); err != nil || n != 1 {
		t.Fatalf("CountPasskeys = %d (err=%v), want 1", n, err)
	}
}
