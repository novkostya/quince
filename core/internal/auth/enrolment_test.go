package auth

import (
	"errors"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// G4 — the enrolment secret's lifecycle: single-use, expiry, pre-use revocation, and
// NOT-BURNED-ON-FAILURE, over an injected clock.
//
// EVERY REFUSAL HERE IS PAIRED WITH A CONTROL that shows the same call succeeding one step earlier.
// A test asserting only the refusal passes just as readily against a store that refuses everything,
// which is the shape that makes an absence read as thoroughness.

const enrolDevice = "DEVICE-A"

func newEnrolments(t *testing.T) (*Enrolments, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	e := NewEnrolments()
	e.now = func() time.Time { return now }
	return e, &now
}

func mustMintEnrolment(t *testing.T, e *Enrolments, udid string) (string, Enrolment) {
	t.Helper()
	tok, en, err := e.Mint(store.DeviceScope(udid))
	if err != nil {
		t.Fatalf("Mint(%s): %v", udid, err)
	}
	if tok == "" {
		t.Fatal("Mint returned an empty token")
	}
	return tok, en
}

func TestEnrolmentSingleUse(t *testing.T) {
	e, _ := newEnrolments(t)
	tok, _ := mustMintEnrolment(t, e, enrolDevice)

	if _, err := e.Spend(tok); err != nil {
		t.Fatalf("first Spend: got %v, want nil (the control — a fresh secret must be spendable)", err)
	}
	if _, err := e.Spend(tok); !errors.Is(err, ErrEnrolmentSpent) {
		t.Fatalf("second Spend: got %v, want ErrEnrolmentSpent", err)
	}
	// The refusal must also reach the BEGIN side, or a spent secret would still open a ceremony
	// that could never finish.
	if _, err := e.Check(tok); !errors.Is(err, ErrEnrolmentSpent) {
		t.Fatalf("Check after Spend: got %v, want ErrEnrolmentSpent", err)
	}
}

// A SCAN THAT FAILS MUST NOT BURN THE SECRET (D4). This is the property that makes Check and Spend
// two calls rather than one, so it is asserted directly rather than inferred from the API shape.
func TestEnrolmentCheckDoesNotConsume(t *testing.T) {
	e, _ := newEnrolments(t)
	tok, minted := mustMintEnrolment(t, e, enrolDevice)

	for i := range 3 {
		got, err := e.Check(tok)
		if err != nil {
			t.Fatalf("Check #%d: got %v, want nil", i+1, err)
		}
		if got.ID != minted.ID {
			t.Fatalf("Check #%d: id = %q, want %q", i+1, got.ID, minted.ID)
		}
	}
	if _, err := e.Spend(tok); err != nil {
		t.Fatalf("Spend after three Checks: got %v, want nil — the checks burned it", err)
	}
}

func TestEnrolmentExpiry(t *testing.T) {
	e, now := newEnrolments(t)
	tok, _ := mustMintEnrolment(t, e, enrolDevice)

	*now = now.Add(enrolmentTTL - time.Second)
	if _, err := e.Check(tok); err != nil {
		t.Fatalf("one second before expiry: got %v, want nil (the control)", err)
	}

	*now = now.Add(2 * time.Second)
	if _, err := e.Check(tok); !errors.Is(err, ErrEnrolmentExpired) {
		t.Fatalf("after expiry: got %v, want ErrEnrolmentExpired", err)
	}
	if _, err := e.Spend(tok); !errors.Is(err, ErrEnrolmentExpired) {
		t.Fatalf("Spend after expiry: got %v, want ErrEnrolmentExpired", err)
	}
}

// EXPIRED IS NOT UNKNOWN, UNTIL THE GRACE WINDOW CLOSES. The distinction is the whole reason
// enrolmentGrace exists, so both sides of it are asserted.
func TestEnrolmentExpiredIsNamedUntilGracePasses(t *testing.T) {
	e, now := newEnrolments(t)
	tok, _ := mustMintEnrolment(t, e, enrolDevice)

	*now = now.Add(enrolmentTTL + time.Minute)
	if _, err := e.Check(tok); !errors.Is(err, ErrEnrolmentExpired) {
		t.Fatalf("inside the grace window: got %v, want ErrEnrolmentExpired (the control)", err)
	}

	*now = now.Add(enrolmentGrace)
	if _, err := e.Check(tok); !errors.Is(err, ErrEnrolmentUnknown) {
		t.Fatalf("past the grace window: got %v, want ErrEnrolmentUnknown", err)
	}
}

func TestEnrolmentRevokeBeforeUse(t *testing.T) {
	e, _ := newEnrolments(t)
	tok, minted := mustMintEnrolment(t, e, enrolDevice)

	if _, err := e.Check(tok); err != nil {
		t.Fatalf("before revocation: got %v, want nil (the control)", err)
	}
	if err := e.Revoke(minted.ID); err != nil {
		t.Fatalf("Revoke: got %v, want nil", err)
	}
	if _, err := e.Check(tok); !errors.Is(err, ErrEnrolmentRevoked) {
		t.Fatalf("Check after revocation: got %v, want ErrEnrolmentRevoked", err)
	}
	if _, err := e.Spend(tok); !errors.Is(err, ErrEnrolmentRevoked) {
		t.Fatalf("Spend after revocation: got %v, want ErrEnrolmentRevoked", err)
	}
}

// REVOKING A SPENT SECRET IS NOT A NO-OP. It reports what actually happened, because a cancel that
// silently succeeds over an already-used link tells the admin the opposite of the truth.
func TestEnrolmentRevokeSpentReportsSpent(t *testing.T) {
	e, _ := newEnrolments(t)
	tok, minted := mustMintEnrolment(t, e, enrolDevice)

	if _, err := e.Spend(tok); err != nil {
		t.Fatalf("Spend: got %v, want nil (the control)", err)
	}
	if err := e.Revoke(minted.ID); !errors.Is(err, ErrEnrolmentSpent) {
		t.Fatalf("Revoke of a spent secret: got %v, want ErrEnrolmentSpent", err)
	}
}

func TestEnrolmentRevokeUnknownID(t *testing.T) {
	e, _ := newEnrolments(t)
	_, minted := mustMintEnrolment(t, e, enrolDevice)

	if err := e.Revoke(minted.ID); err != nil {
		t.Fatalf("Revoke of a live id: got %v, want nil (the control)", err)
	}
	if err := e.Revoke("01JNOTHINGATALL"); !errors.Is(err, ErrEnrolmentNotFound) {
		t.Fatalf("Revoke of an unknown id: got %v, want ErrEnrolmentNotFound", err)
	}
}

// THE ESCALATION HAS NO REPRESENTATION TO TRAVEL IN (D4). An admin-scoped enrolment cannot be
// minted, so no later code path can be handed one.
func TestEnrolmentRefusesAdminScope(t *testing.T) {
	e, _ := newEnrolments(t)

	if _, _, err := e.Mint(store.DeviceScope(enrolDevice)); err != nil {
		t.Fatalf("device scope: got %v, want nil (the control)", err)
	}
	if _, _, err := e.Mint(store.AdminScope()); !errors.Is(err, ErrEnrolmentAdminScope) {
		t.Fatalf("admin scope: got %v, want ErrEnrolmentAdminScope", err)
	}
	// An UNSET scope is the zero value, and it must not fall through to anything. store.Scope's
	// own contract is that its zero value is invalid; this asserts that contract holds here too.
	if _, _, err := e.Mint(store.Scope{}); !errors.Is(err, ErrEnrolmentAdminScope) {
		t.Fatalf("unset scope: got %v, want ErrEnrolmentAdminScope", err)
	}
}

func TestEnrolmentUnknownAndEmptyToken(t *testing.T) {
	e, _ := newEnrolments(t)
	tok, _ := mustMintEnrolment(t, e, enrolDevice)

	if _, err := e.Check(tok); err != nil {
		t.Fatalf("the real token: got %v, want nil (the control)", err)
	}
	if _, err := e.Check("not-a-real-token"); !errors.Is(err, ErrEnrolmentUnknown) {
		t.Fatalf("a wrong token: got %v, want ErrEnrolmentUnknown", err)
	}
	// An empty token must not match anything. ConstantTimeCompare("", "") is 1, so this is the
	// one input where the comparison alone would say yes.
	if _, err := e.Check(""); !errors.Is(err, ErrEnrolmentUnknown) {
		t.Fatalf("an empty token: got %v, want ErrEnrolmentUnknown", err)
	}
}

// THE SCOPE IS FIXED AT MINT AND THE SCANNER SENDS NONE. What Spend hands the ceremony is what the
// admin chose, which is the whole of *a token whose scope is chosen by the scanner is not a scoped
// token*.
func TestEnrolmentScopeSurvivesToSpend(t *testing.T) {
	e, _ := newEnrolments(t)
	tok, minted := mustMintEnrolment(t, e, enrolDevice)

	if minted.ScopeUDID != enrolDevice {
		t.Fatalf("minted scope = %q, want %q", minted.ScopeUDID, enrolDevice)
	}
	spent, err := e.Spend(tok)
	if err != nil {
		t.Fatalf("Spend: %v", err)
	}
	if spent.ScopeUDID != enrolDevice {
		t.Fatalf("spent scope = %q, want %q", spent.ScopeUDID, enrolDevice)
	}
}

func TestEnrolmentTokensDiffer(t *testing.T) {
	e, _ := newEnrolments(t)
	a, ea := mustMintEnrolment(t, e, enrolDevice)
	b, eb := mustMintEnrolment(t, e, enrolDevice)

	if a == b {
		t.Fatal("two mints produced the same token")
	}
	if ea.ID == eb.ID {
		t.Fatal("two mints produced the same id")
	}
	// Spending one must not touch the other — the records are independent, not a single slot.
	if _, err := e.Spend(a); err != nil {
		t.Fatalf("Spend(a): %v", err)
	}
	if _, err := e.Check(b); err != nil {
		t.Fatalf("Check(b) after spending a: got %v, want nil", err)
	}
}

func TestEnrolmentListShowsOnlyLiveOnesForThisDevice(t *testing.T) {
	e, now := newEnrolments(t)
	const other = "DEVICE-B"

	live, _ := mustMintEnrolment(t, e, enrolDevice)
	spentTok, _ := mustMintEnrolment(t, e, enrolDevice)
	_, revoked := mustMintEnrolment(t, e, enrolDevice)
	expiring, _ := mustMintEnrolment(t, e, enrolDevice)
	_, _ = mustMintEnrolment(t, e, other)

	if got := len(e.List(enrolDevice)); got != 4 {
		t.Fatalf("before anything happens: List = %d, want 4 (the control)", got)
	}

	if _, err := e.Spend(spentTok); err != nil {
		t.Fatalf("Spend: %v", err)
	}
	if err := e.Revoke(revoked.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	// Expire the fourth by moving the clock past every mint's TTL, then re-mint the one that is
	// meant to still be live so the list is not merely empty.
	*now = now.Add(enrolmentTTL + time.Second)
	if _, err := e.Check(expiring); !errors.Is(err, ErrEnrolmentExpired) {
		t.Fatalf("the expiring one: got %v, want ErrEnrolmentExpired", err)
	}
	if _, err := e.Check(live); !errors.Is(err, ErrEnrolmentExpired) {
		t.Fatalf("every mint shares the clock, so this one expired too: got %v", err)
	}
	fresh, freshEn := mustMintEnrolment(t, e, enrolDevice)

	got := e.List(enrolDevice)
	if len(got) != 1 {
		t.Fatalf("List = %d entries, want 1", len(got))
	}
	if got[0].ID != freshEn.ID {
		t.Fatalf("List returned %q, want the fresh one %q", got[0].ID, freshEn.ID)
	}
	if _, err := e.Check(fresh); err != nil {
		t.Fatalf("the listed one must still work: %v", err)
	}
	// The other device's secrets are not this device's business. Minted AFTER the clock move, so
	// this asserts isolation rather than re-asserting the expiry above.
	_, otherFresh := mustMintEnrolment(t, e, other)
	if got := e.List(other); len(got) != 1 || got[0].ID != otherFresh.ID {
		t.Fatalf("the other device's List = %v, want exactly its own fresh secret", got)
	}
	if got := e.List(enrolDevice); len(got) != 1 || got[0].ID != freshEn.ID {
		t.Fatalf("this device's List changed when another device minted one: %v", got)
	}
}
