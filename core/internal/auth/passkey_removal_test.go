package auth

import (
	"encoding/base64"
	"errors"
	"strings"
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

	// Click one — allowed then and now, and since qn.6n rule 2 it costs a passkey assertion. The
	// proof is minted directly rather than earned through a ceremony: what this test is about is the
	// SEQUENCE, and routing it through WebAuthn would be testing the ceremony a second time.
	proofs := NewProofs()
	tok := mustMint(t, proofs, OpRemovePassword, "", ProofSubject{CredentialID: "cred-1"})
	if err := svc.RemovePassword(proofs, Presented{Proof: tok}, here, sess, ip); err != nil {
		t.Fatalf("RemovePassword: %v", err)
	}

	// Click two — the one that used to go through, and the REASON it cannot has changed with qn.6n.
	// It is no longer a count of what would remain: the only credential this install still holds is
	// the one being removed, and rule 2 refuses a credential that vouches for itself.
	self := mustMint(t, proofs, OpRemovePasskey, "cred-1", ProofSubject{CredentialID: "cred-1"})
	_, err := svc.RemovePasskey(proofs, Presented{Proof: self}, "cred-1", here, sess, ip)
	if !errors.As(err, &ErrSelfRemoval{}) {
		t.Fatalf("removing the last credential with its own proof = %v, want ErrSelfRemoval", err)
	}
	// AND PRESENTING NOTHING IS REFUSED TOO — the other half of the same click. A passwordless
	// install has no lighter factor to fall back on, so there is nothing else this click could carry.
	if _, err := svc.RemovePasskey(NewProofs(), Presented{}, "cred-1", here, sess, ip); !errors.Is(err, ErrNoProof) {
		t.Fatalf("removing the last credential with nothing presented = %v, want ErrNoProof", err)
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

	// THE PASSWORD IS THE LIGHTER FACTOR AND IT PROVES THIS ONE — rule 2 is satisfied because the
	// password is not the credential being removed. No ceremony, no proof token.
	deleted, err := svc.RemovePasskey(NewProofs(), Presented{Password: "old-one"}, "cred-1", here, sess, ip)
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

	// PROVEN BY THE OTHER ONE, which is exactly what rule 2 asks for and what the old count only
	// approximated.
	proofs := NewProofs()
	byTwo := mustMint(t, proofs, OpRemovePasskey, "cred-1", ProofSubject{CredentialID: "cred-2"})
	if _, err := svc.RemovePasskey(proofs, Presented{Proof: byTwo}, "cred-1", here, sess, ip); err != nil {
		t.Fatalf("RemovePasskey with a second usable credential: %v", err)
	}
	if n, err := st.CountPasskeys(); err != nil || n != 1 {
		t.Fatalf("CountPasskeys = %d (err=%v), want 1", n, err)
	}
	// And the last one is now refused — the boundary is where it should be, not one row early. The
	// refusal is ErrSelfRemoval rather than a lockout count: cred-2 is all that is left, so the only
	// proof obtainable names cred-2, and a credential cannot authorise its own removal.
	self := mustMint(t, proofs, OpRemovePasskey, "cred-2", ProofSubject{CredentialID: "cred-2"})
	if _, err := svc.RemovePasskey(proofs, Presented{Proof: self}, "cred-2", here, sess, ip); !errors.As(err, &ErrSelfRemoval{}) {
		t.Fatalf("removing the now-last credential = %v, want ErrSelfRemoval", err)
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

	// ASSERTED AT THE CEREMONY GATE, WHICH IS WHERE THE rpId RULE MOVED IN qn.6n. `RemovePasskey`
	// itself no longer counts survivors — the guard is the subject comparison — so the filter that
	// used to live there now decides whether a ceremony can be BEGUN at all, and carries the same
	// message. Same rule, same sentence, one endpoint earlier.
	err := svc.provable(OpRemovePasskey, here, "cred-here")
	var last ErrLastPasskey
	if !errors.As(err, &last) {
		t.Fatalf("provable = %v, want ErrLastPasskey — the survivor cannot sign in here", err)
	}
	// The allow-list agrees, and it is asserted separately — see
	// TestTheAllowListExcludesTheTargetAndNothingElse, which needs decodable credential ids.
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

	// PROVEN BY THE REAL CREDENTIAL, which is not the id being removed — so rule 2 is satisfied and
	// the no-op still succeeds. This is what "no special case implements it" means under the new
	// guard too: `IsCredential` is false for a subject that is not the target, whether or not the
	// target exists.
	proofs := NewProofs()
	tok := mustMint(t, proofs, OpRemovePasskey, "no-such-credential", ProofSubject{CredentialID: "cred-1"})
	deleted, err := svc.RemovePasskey(proofs, Presented{Proof: tok}, "no-such-credential", here, sess, ip)
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

// ── the allow-list, which is rule 2 one layer before the subject comparison ────────────────────

// THE CEREMONY DOES NOT OFFER THE CREDENTIAL BEING REMOVED, and that is enforced rather than
// suggested: `go-webauthn` checks the asserted id against `session.AllowedCredentialIDs` at finish
// whenever that list is non-empty, so the exclusion is a second, independent refusal of a self-proof.
//
// THE IDS HERE ARE REAL base64url, unlike `seedPasskey`'s elsewhere in this file. `existingCredentials`
// DECODES them — a stored id is base64url in production — and the friendly fixture ids other tests
// use are not decodable, which is harmless until something actually reads the credential material.
// Written out rather than hidden in a helper, because the constraint belongs to this test.
func TestTheAllowListExcludesTheTargetAndNothingElse(t *testing.T) {
	const (
		target = "Y3JlZC10YXJnZXQ" // "cred-target"
		other  = "Y3JlZC1vdGhlcg"  // "cred-other"
	)
	_, st := newConfiguredService(t)
	seedPasskey(t, st, target, here)
	seedPasskey(t, st, other, here)

	allowed, err := allowedForRemoval(st, here, target)
	if err != nil {
		t.Fatalf("allowedForRemoval: %v", err)
	}
	if len(allowed) != 1 {
		t.Fatalf("allowed %d credential(s), want 1 — every one except the target", len(allowed))
	}
	if got := base64.RawURLEncoding.EncodeToString(allowed[0].CredentialID); got != other {
		t.Errorf("allowed %q, want %q — the wrong credential was excluded", got, other)
	}

	// AND THE CEREMONY IS REFUSED WHEN THAT LIST WOULD BE EMPTY, which is the state that must never
	// reach WebAuthn: empty means ANY credential, so the target would be offered after all.
	solo, stSolo := newConfiguredService(t)
	seedPasskey(t, stSolo, target, here)
	if err := solo.provable(OpRemovePasskey, here, target); !errors.As(err, &ErrLastPasskey{}) {
		t.Fatalf("provable with only the target = %v, want ErrLastPasskey", err)
	}
}

// THE MESSAGE TURNS ON WHETHER A PASSWORD EXISTS, because the two states are not both lockouts: with
// a password the removal is possible and the ceremony simply cannot help, and telling that user
// "this quince has no password" would be false AND the opposite of the instruction they need.
//
// The client falls back on this distinction — `Passkeys.tsx` drops to the password form on it — so
// it is load-bearing rather than a wording preference.
func TestTheRefusalDistinguishesADeadEndFromUseYourPassword(t *testing.T) {
	const target = "Y3JlZC10YXJnZXQ"

	locked, stLocked := newConfiguredService(t)
	seedPasskey(t, stLocked, target, here)
	var dead ErrLastPasskey
	if err := locked.provable(OpRemovePasskey, here, target); !errors.As(err, &dead) {
		t.Fatalf("provable passwordless = %v, want ErrLastPasskey", err)
	}
	if dead.HasPassword {
		t.Error("HasPassword is true on an install with no password")
	}
	if !strings.Contains(dead.Error(), "Set a password first") {
		t.Errorf("dead-end message does not name the remedy: %q", dead.Error())
	}

	withPw, stPw := newConfiguredService(t)
	if err := withPw.SetPassword("old-one", ip); err != nil {
		t.Fatalf("seed: %v", err)
	}
	seedPasskey(t, stPw, target, here)
	var redirect ErrLastPasskey
	if err := withPw.provable(OpRemovePasskey, here, target); !errors.As(err, &redirect) {
		t.Fatalf("provable with a password = %v, want ErrLastPasskey", err)
	}
	if !redirect.HasPassword {
		t.Fatal("HasPassword is false on an install that has one")
	}
	if !strings.Contains(redirect.Error(), "Confirm with your password") {
		t.Errorf("message does not redirect to the password: %q", redirect.Error())
	}
	// AND IT MUST NOT CLAIM A LOCKOUT, which is the sentence that would be flatly untrue here.
	if strings.Contains(redirect.Error(), "no way to sign in") {
		t.Errorf("message claims a lockout on an install with a password: %q", redirect.Error())
	}
}
