package auth

import (
	"errors"
	"testing"
)

// quince#1259 — `ErrLastCredential` must be REACHABLE on the remove-password path.
//
// Before this, `RemovePassword` demanded a proof first. On an install with a password and no passkey
// that works at this address, no proof can exist, so the call always died at `ErrNoProof` —
// "authenticate again" — for an operation nothing could ever authorise. The refusal that CAN be
// satisfied was checked before the refusal that CANNOT.
func TestRemovePasswordReachesLastCredential(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}

	// A password and nothing else: removing it would leave no way in at all.
	err := svc.RemovePassword(NewProofs(), Presented{}, rpHome, "", "1.2.3.4")
	if err == nil {
		t.Fatal("removing the only credential succeeded")
	}
	var lastCred ErrLastCredential
	if !errors.As(err, &lastCred) {
		t.Fatalf("got %T (%v) — want ErrLastCredential, the refusal that names the remedy", err, err)
	}
	// AND NOT ErrNoProof, which is the specific substitution quince#1259 is about: a demand to
	// authenticate, for something no authentication could permit.
	if errors.Is(err, ErrNoProof) {
		t.Fatal("still the proof demand — the ordering did not change")
	}
}

// THE CONTROL. An install that CAN prove the removal must still be asked to, or the reorder would
// have turned a proof demand into a blanket refusal — which passes the test above for the wrong
// reason and would break removing a password on an install that has a working passkey.
func TestRemovePasswordStillDemandsAProofWhenOneIsPossible(t *testing.T) {
	svc, _ := newTestAuth(t)
	if err := svc.SetPassword("test", "1.2.3.4"); err != nil {
		t.Fatalf("set password: %v", err)
	}
	seedCredential(t, svc.store, "admin-1", rpHome)

	err := svc.RemovePassword(NewProofs(), Presented{}, rpHome, "", "1.2.3.4")
	if err == nil {
		t.Fatal("removed the password with no proof presented")
	}
	var lastCred ErrLastCredential
	if errors.As(err, &lastCred) {
		t.Fatal("refused as last-credential on an install with a usable passkey — the reorder " +
			"turned a proof demand into a blanket refusal")
	}
	if !errors.Is(err, ErrNoProof) {
		t.Fatalf("got %T (%v) — want ErrNoProof, since a proof IS possible here", err, err)
	}
}

// FIRST RUN MUST STAY REACHABLE. An unclaimed install holds no credentials to compare, and refusing
// there would make `quince auth reset` recoverable only by a credential the reset just destroyed.
func TestRemovePasswordOnAnUnconfiguredInstallIsNotRefusedAsLastCredential(t *testing.T) {
	svc, _ := newTestAuth(t)
	err := svc.RemovePassword(NewProofs(), Presented{}, rpHome, "", "1.2.3.4")
	var lastCred ErrLastCredential
	if errors.As(err, &lastCred) {
		t.Fatal("an unclaimed install was refused as last-credential — first run is broken")
	}
}
