package auth

import (
	"errors"
	"testing"
	"time"
)

// qn.6n slice 2 — the proof primitive, gates G2, G3, G4 and G4b's session half.
//
// Nothing in the shipped binary calls this yet, so these tests are the only thing that exercises it.
// That is the point of landing it alone: the piece most likely to be got wrong is D4's SUBJECT
// field, and it is easier to see when nothing else is moving.

const (
	sess  = "session-abc"
	cred1 = "credential-one"
	cred2 = "credential-two"
)

func newProofs(t *testing.T) (*Proofs, *time.Time) {
	t.Helper()
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	p := NewProofs()
	p.now = func() time.Time { return now }
	return p, &now
}

func mustMint(t *testing.T, p *Proofs, op ProofOperation, target string, subj ProofSubject) string {
	t.Helper()
	tok, err := p.Mint(op, target, subj, sess)
	if err != nil {
		t.Fatalf("Mint(%s): %v", op, err)
	}
	return tok
}

// ── the happy path, and the field the rung turns on ───────────────────────────────────────────

// CONSUME RETURNS THE SUBJECT. Not a boolean — rule 2 is a comparison, and a primitive that answered
// only "valid" would satisfy rules 1 and 3 and be structurally unable to express rule 2.
func TestConsumeReturnsWhatWasPresented(t *testing.T) {
	p, _ := newProofs(t)

	tok := mustMint(t, p, OpRemovePasskey, cred1, ProofSubject{CredentialID: cred2})
	subj, err := p.Consume(tok, OpRemovePasskey, cred1, sess)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if subj.Password {
		t.Error("subject reports the password; a passkey asserted")
	}
	if !subj.IsCredential(cred2) {
		t.Errorf("subject is not %s", cred2)
	}
	// AND IT IS NOT THE TARGET, which is the whole of rule 2 at the call site.
	if subj.IsCredential(cred1) {
		t.Error("subject matches the credential being removed — rule 2 cannot be enforced")
	}
}

func TestAPasswordSubjectIsNotAnyCredential(t *testing.T) {
	p, _ := newProofs(t)

	tok := mustMint(t, p, OpRemovePasskey, cred1, ProofSubject{Password: true})
	subj, err := p.Consume(tok, OpRemovePasskey, cred1, sess)
	if err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !subj.Password {
		t.Fatal("subject lost the password flag")
	}
	// THE ARITHMETIC RULE 2 FALLS OUT OF: the password is a subject like any other, so removing a
	// passkey may be proven by it and removing the PASSWORD may not — with no special case anywhere.
	if subj.IsCredential(cred1) || subj.IsCredential(cred2) {
		t.Error("a password subject matched a credential id")
	}
}

// ── G3: single use ────────────────────────────────────────────────────────────────────────────

func TestAProofIsSpentOnce(t *testing.T) {
	p, _ := newProofs(t)

	tok := mustMint(t, p, OpSetPassword, "", ProofSubject{CredentialID: cred1})
	if _, err := p.Consume(tok, OpSetPassword, "", sess); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	if _, err := p.Consume(tok, OpSetPassword, "", sess); !errors.Is(err, ErrNoProof) {
		t.Fatalf("second Consume = %v, want ErrNoProof — a proof that survives can be replayed", err)
	}
}

// SPENT EVEN ON MISMATCH. Leaving a mismatched proof alive turns this into an oracle a holder can
// probe against all four operations; the legitimate cost is one re-prompt after a client bug.
func TestAMismatchAlsoSpendsIt(t *testing.T) {
	p, _ := newProofs(t)

	tok := mustMint(t, p, OpAddPasskey, "", ProofSubject{Password: true})
	if _, err := p.Consume(tok, OpRemovePassword, "", sess); !errors.Is(err, ErrProofNotForThis) {
		t.Fatalf("wrong-operation Consume = %v, want ErrProofNotForThis", err)
	}
	if _, err := p.Consume(tok, OpAddPasskey, "", sess); !errors.Is(err, ErrNoProof) {
		t.Fatalf("Consume after a mismatch = %v, want ErrNoProof — the mismatch must spend it", err)
	}
}

// ── G2: the operation and target bindings ─────────────────────────────────────────────────────

// TABLE-DRIVEN OVER ALL FOUR OPERATIONS, because the failure this guards is a proof minted for the
// cheapest operation and spent on the most expensive one.
func TestAProofIsBoundToItsOperation(t *testing.T) {
	ops := []struct {
		op     ProofOperation
		target string
	}{
		{OpAddPasskey, ""},
		{OpRemovePasskey, cred1},
		{OpRemovePassword, ""},
		{OpSetPassword, ""},
	}

	for _, minted := range ops {
		for _, presented := range ops {
			if minted.op == presented.op {
				continue
			}
			p, _ := newProofs(t)
			tok := mustMint(t, p, minted.op, minted.target, ProofSubject{Password: true})
			_, err := p.Consume(tok, presented.op, presented.target, sess)
			if !errors.Is(err, ErrProofNotForThis) {
				t.Errorf("minted %s, spent as %s = %v, want ErrProofNotForThis", minted.op, presented.op, err)
			}
		}
	}
}

// THE TARGET IS PART OF THE BINDING, NOT DECORATION. Without this, a proof to remove a phone you no
// longer own removes the one you are holding.
func TestARemovalProofIsBoundToITSCredential(t *testing.T) {
	p, _ := newProofs(t)

	tok := mustMint(t, p, OpRemovePasskey, cred1, ProofSubject{Password: true})
	if _, err := p.Consume(tok, OpRemovePasskey, cred2, sess); !errors.Is(err, ErrProofNotForThis) {
		t.Fatalf("Consume against another credential = %v, want ErrProofNotForThis", err)
	}
}

// ── G4b: the session binding ──────────────────────────────────────────────────────────────────

// ADDED AT SPEC REVIEW. A proof is a credential-equivalent for one operation, so a client that did
// not earn it must not be able to spend it. The ceremony key goes only to the minting client today,
// which makes the gap narrow rather than absent — and narrow is not worth relying on.
func TestAProofCannotBeSpentByAnotherSession(t *testing.T) {
	p, _ := newProofs(t)

	tok := mustMint(t, p, OpRemovePassword, "", ProofSubject{CredentialID: cred1})
	if _, err := p.Consume(tok, OpRemovePassword, "", "another-session"); !errors.Is(err, ErrProofNotForThis) {
		t.Fatalf("Consume under another session = %v, want ErrProofNotForThis", err)
	}
}

func TestMintRefusesWithoutASession(t *testing.T) {
	p, _ := newProofs(t)

	if _, err := p.Mint(OpAddPasskey, "", ProofSubject{Password: true}, ""); err == nil {
		t.Fatal("minted a proof bound to no session — that is a bearer token for a privileged operation")
	}
}

// ── G4: expiry ────────────────────────────────────────────────────────────────────────────────

func TestAProofExpires(t *testing.T) {
	p, now := newProofs(t)

	tok := mustMint(t, p, OpAddPasskey, "", ProofSubject{Password: true})
	*now = now.Add(proofTTL + time.Second)

	if _, err := p.Consume(tok, OpAddPasskey, "", sess); !errors.Is(err, ErrNoProof) {
		t.Fatalf("Consume past the TTL = %v, want ErrNoProof", err)
	}
}

// AND AN EXPIRED PROOF IS SWEPT RATHER THAN ACCUMULATING. The sweep runs on Mint, following
// `PasskeyCeremonies`: this map is bounded by how often a human confirms a credential change, so a
// collector goroutine would be more machinery than the thing it manages.
func TestExpiredProofsAreSweptOnTheNextMint(t *testing.T) {
	p, now := newProofs(t)

	mustMint(t, p, OpAddPasskey, "", ProofSubject{Password: true})
	mustMint(t, p, OpSetPassword, "", ProofSubject{Password: true})
	*now = now.Add(proofTTL + time.Second)
	mustMint(t, p, OpRemovePassword, "", ProofSubject{CredentialID: cred1})

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.in) != 1 {
		t.Fatalf("store holds %d proofs after the sweep, want 1", len(p.in))
	}
}

// ── what Mint refuses ─────────────────────────────────────────────────────────────────────────

// A TARGET EXACTLY WHERE IT IS MEANINGFUL. A target on an operation nothing reads it for is how a
// binding becomes decorative — and a decorative binding is one a later reader deletes as unused.
func TestMintValidatesTheTarget(t *testing.T) {
	p, _ := newProofs(t)

	if _, err := p.Mint(OpRemovePasskey, "", ProofSubject{Password: true}, sess); err == nil {
		t.Error("remove_passkey minted with no target — rule 2 would have nothing to compare")
	}
	if _, err := p.Mint(OpSetPassword, cred1, ProofSubject{Password: true}, sess); err == nil {
		t.Error("set_password minted WITH a target — a field nothing reads")
	}
}

func TestMintValidatesTheSubject(t *testing.T) {
	p, _ := newProofs(t)

	// Neither: proved by nothing.
	if _, err := p.Mint(OpAddPasskey, "", ProofSubject{}, sess); err == nil {
		t.Error("minted a proof with no subject")
	}
	// Both: the password AND a credential, which no single presentation produces.
	if _, err := p.Mint(OpAddPasskey, "", ProofSubject{Password: true, CredentialID: cred1}, sess); err == nil {
		t.Error("minted a proof claiming two subjects")
	}
}

func TestMintRefusesAnUnknownOperation(t *testing.T) {
	p, _ := newProofs(t)

	if _, err := p.Mint(ProofOperation("rename_passkey"), "", ProofSubject{Password: true}, sess); err == nil {
		t.Fatal("minted a proof for an operation outside the closed set — D6 keeps rename session-only")
	}
}

// ── the token itself ──────────────────────────────────────────────────────────────────────────

// TWO MINTS, TWO TOKENS. Obvious, and the failure it guards is not: a deterministic token would make
// every proof predictable from any other, which no other test here would notice.
func TestTokensAreUnique(t *testing.T) {
	p, _ := newProofs(t)

	a := mustMint(t, p, OpAddPasskey, "", ProofSubject{Password: true})
	b := mustMint(t, p, OpAddPasskey, "", ProofSubject{Password: true})
	if a == b {
		t.Fatal("two mints produced the same token")
	}
	if len(a) < 32 {
		t.Fatalf("token is %d characters, which is not 32 bytes of entropy", len(a))
	}
}
