package auth

import (
	"errors"
	"net/http/httptest"
	"testing"
)

// qn.6n slice 3 — the reauth pair's refusals.
//
// WHAT IS TESTABLE HERE AND WHAT IS NOT. Everything up to the authenticator is: the operation and
// target validation, the session requirement, the ceremony's single use, and the two bindings the
// ceremony carries. The assertion itself is not — verifying one needs a real authenticator, which is
// G7/G8's territory and is declared unrun. So these tests prove the ceremony cannot be MISUSED; they
// prove nothing about whether a genuine assertion is accepted.

const reauthRP = "quince.example.com"

func newReauth(t *testing.T) (*Service, *ReauthCeremonies, *Proofs) {
	t.Helper()
	svc, _ := newConfiguredService(t)
	return svc, NewReauthCeremonies(), NewProofs()
}

// ── begin: what it refuses before a sheet is ever shown ───────────────────────────────────────

// REFUSED EARLY SO THE USER IS NEVER SENT TO A FACE ID SHEET THAT CANNOT PRODUCE A USABLE PROOF.
// `Proofs.Mint` validates the same things, and that is not redundant: this check is for the user,
// and that one is what makes the primitive safe for any caller rather than only this one.
func TestBeginReauthValidatesTheOperation(t *testing.T) {
	svc, cer, _ := newReauth(t)

	cases := []struct {
		name   string
		op     ProofOperation
		target string
	}{
		{"an operation outside the closed set", ProofOperation("rename_passkey"), ""},
		{"remove_passkey with no target", OpRemovePasskey, ""},
		{"set_password WITH a target", OpSetPassword, cred1},
	}
	for _, c := range cases {
		if _, _, err := svc.BeginReauth(cer, reauthRP, ip, c.op, c.target, sess); err == nil {
			t.Errorf("%s was accepted", c.name)
		}
	}
}

// A CEREMONY WITH NO SESSION WOULD MINT A BEARER TOKEN for a privileged operation, which is the
// shape the whole mechanism exists to avoid. Refused at begin as well as at mint.
func TestBeginReauthRequiresASession(t *testing.T) {
	svc, cer, _ := newReauth(t)

	if _, _, err := svc.BeginReauth(cer, reauthRP, ip, OpAddPasskey, "", ""); !errors.Is(err, ErrNoSession) {
		t.Fatalf("BeginReauth with no session = %v, want ErrNoSession", err)
	}
}

func TestBeginReauthRefusesAnAddressThatCannotHoldAPasskey(t *testing.T) {
	svc, cer, _ := newReauth(t)

	// TEST-NET-1 (RFC 5737). An rpId must be a domain, so no certificate rescues a bare IP — the
	// same refusal registration makes, arriving one endpoint later.
	_, _, err := svc.BeginReauth(cer, "192.0.2.10", ip, OpAddPasskey, "", sess)
	if !errors.As(err, &ErrUnsupportedRPID{}) {
		t.Fatalf("BeginReauth at a bare IP = %v, want ErrUnsupportedRPID", err)
	}
}

// ── finish: the bindings the ceremony carries ─────────────────────────────────────────────────

func beginOK(t *testing.T, svc *Service, cer *ReauthCeremonies, op ProofOperation, target string) string {
	t.Helper()
	_, key, err := svc.BeginReauth(cer, reauthRP, ip, op, target, sess)
	if err != nil {
		t.Fatalf("BeginReauth: %v", err)
	}
	return key
}

// THE CEREMONY'S OWN SESSION WINS. A ceremony begun by one client and finished by another is not a
// re-authentication of the second one, and binding the proof to the finisher would let a stolen
// session inherit a proof the owner earned.
//
// ASSERTED BEFORE THE ASSERTION IS EVEN PARSED, which is why this is testable with an empty body:
// the check runs on the ceremony, not on the authenticator's response.
func TestFinishReauthRefusesAnotherSession(t *testing.T) {
	svc, cer, proofs := newReauth(t)
	key := beginOK(t, svc, cer, OpAddPasskey, "")

	r := httptest.NewRequest("POST", "/api/auth/reauth/finish", nil)
	_, err := svc.FinishReauth(cer, proofs, key, reauthRP, ip, r, "a-different-session")
	if !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("FinishReauth under another session = %v, want ErrNoChallenge", err)
	}
}

// THE CEREMONY IS SINGLE USE, whatever follows — `PasskeyCeremonies.take`'s rule. The first attempt
// here fails at the authenticator (there is no response body), and the ceremony is spent anyway.
func TestAReauthCeremonyIsSpentOnce(t *testing.T) {
	svc, cer, proofs := newReauth(t)
	key := beginOK(t, svc, cer, OpAddPasskey, "")

	r := httptest.NewRequest("POST", "/api/auth/reauth/finish", nil)
	if _, err := svc.FinishReauth(cer, proofs, key, reauthRP, ip, r, sess); err == nil {
		t.Fatal("an empty assertion body was accepted")
	}
	if _, err := svc.FinishReauth(cer, proofs, key, reauthRP, ip, r, sess); !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("second FinishReauth = %v, want ErrNoChallenge — the ceremony must not survive", err)
	}
}

// THE CEREMONY'S OWN rpId WINS, for `FinishPasskeyAssertion`'s reason: the authenticator signed for
// the domain the challenge was issued on, and finishing against another would accept a signature
// made for somewhere else.
func TestFinishReauthRefusesAnotherAddress(t *testing.T) {
	svc, cer, proofs := newReauth(t)
	key := beginOK(t, svc, cer, OpRemovePassword, "")

	r := httptest.NewRequest("POST", "/api/auth/reauth/finish", nil)
	_, err := svc.FinishReauth(cer, proofs, key, "quince.example.net", ip, r, sess)
	if !errors.As(err, &ErrRPIDMismatch{}) {
		t.Fatalf("FinishReauth at another address = %v, want ErrRPIDMismatch", err)
	}
}

func TestFinishReauthRefusesAnUnknownCeremony(t *testing.T) {
	svc, cer, proofs := newReauth(t)

	r := httptest.NewRequest("POST", "/api/auth/reauth/finish", nil)
	if _, err := svc.FinishReauth(cer, proofs, "no-such-ceremony", reauthRP, ip, r, sess); !errors.Is(err, ErrNoChallenge) {
		t.Fatalf("FinishReauth with an unknown key = %v, want ErrNoChallenge", err)
	}
}

// ── nothing is minted on a failure ────────────────────────────────────────────────────────────

// A PROOF IS MINTED ONLY AFTER THE ASSERTION VERIFIES. Obvious, and the failure it guards is the
// expensive one: a proof handed out on a refused ceremony is exactly the bypass this rung exists to
// close, and no test above would notice, because they all assert on the ERROR.
func TestNoProofSurvivesAFailedReauth(t *testing.T) {
	svc, cer, proofs := newReauth(t)
	key := beginOK(t, svc, cer, OpRemovePassword, "")

	r := httptest.NewRequest("POST", "/api/auth/reauth/finish", nil)
	if _, err := svc.FinishReauth(cer, proofs, key, reauthRP, ip, r, sess); err == nil {
		t.Fatal("an empty assertion body was accepted")
	}

	proofs.mu.Lock()
	defer proofs.mu.Unlock()
	if len(proofs.in) != 0 {
		t.Fatalf("the store holds %d proof(s) after a failed re-authentication, want 0", len(proofs.in))
	}
}
