package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Per-operation proof — qn.6n slice 2, the primitive. Operator ruling on quince#888 item 3.
//
// A SESSION PROVES A PAST AUTHENTICATION. Changing the set of things that can authenticate requires
// a PRESENT one. The ruling chose this over a sudo window because a window's grant is ambient: a
// stolen session acting inside it inherits exactly the authority being defended against. What makes
// this mechanism the other thing is that each proof is bound to ONE operation and dies when spent.
//
// NOTHING IN THE SHIPPED BINARY CALLS THIS YET, ON PURPOSE. Slice 2 lands the primitive alone and
// reviewable before any endpoint can depend on it — `qn.6m` slice 5a's ordering, and `qn.6k`'s
// before it, where `quince auth reset` shipped ahead of anything that could create a credential.

// ProofOperation names what a proof permits. The set is closed: these are the four mutations the
// ruling's three rules cover, and `rename` is deliberately absent (spec D6 — a label on a row
// changes nothing about who can get in).
type ProofOperation string

const (
	OpAddPasskey     ProofOperation = "add_passkey"
	OpRemovePasskey  ProofOperation = "remove_passkey"
	OpRemovePassword ProofOperation = "remove_password"
	OpSetPassword    ProofOperation = "set_password"
)

func (o ProofOperation) valid() bool {
	switch o {
	case OpAddPasskey, OpRemovePasskey, OpRemovePassword, OpSetPassword:
		return true
	}
	return false
}

// needsTarget reports whether this operation names a specific credential.
//
// ONLY `remove_passkey` DOES, and the asymmetry is rule 2's. "Other than the one being removed"
// needs to know which one that is; every other operation is about the credential SET rather than
// about a member of it.
func (o ProofOperation) needsTarget() bool { return o == OpRemovePasskey }

// ProofSubject is WHAT was presented — the single most important field in this rung.
//
// Rule 2 is *"removing a credential requires presenting an existing credential OTHER THAN THE ONE
// BEING REMOVED"*, which is a COMPARISON. There is nothing to compare against unless the server
// records what proved the request, so a proof that omitted this would satisfy rules 1 and 3 and be
// structurally unable to express rule 2 — and adding it later is a contract change, not a patch.
//
// Exactly one of the two is set. A password subject has no credential id because the password is
// not a row; a passkey subject names the credential that asserted.
type ProofSubject struct {
	// Password is true when the admin password was presented.
	Password bool
	// CredentialID is the passkey that asserted, empty when Password is true.
	CredentialID string
}

func (s ProofSubject) valid() bool {
	if s.Password {
		return s.CredentialID == ""
	}
	return s.CredentialID != ""
}

// IsCredential reports whether this proof was made by the passkey with the given credential id.
//
// THE WHOLE OF RULE 2 AT THE CALL SITE: a removal is refused when its subject IS its target. Given
// as a method rather than left to callers comparing strings, because the comparison is the guard and
// a second spelling of it somewhere else is the way the two drift apart.
func (s ProofSubject) IsCredential(credentialID string) bool {
	return !s.Password && credentialID != "" &&
		subtle.ConstantTimeCompare([]byte(s.CredentialID), []byte(credentialID)) == 1
}

// proofTTL bounds how long a minted proof may sit unspent.
//
// SEPARATE FROM challengeTTL THOUGH THE VALUE MATCHES. A ceremony's clock is a human looking at a
// Face ID sheet; a proof's is the round trip from `reauth/finish` to the mutating call, which is one
// request. They are the same number today for different reasons, and one constant would mean tuning
// either lifetime silently retunes the other.
const proofTTL = 2 * time.Minute

var (
	// ErrNoProof — no such proof, it expired, or it was already spent. The three are deliberately
	// one error: a caller holding a token learns nothing useful from being told which, and the UI
	// remedy is identical in all three cases (start the ceremony again).
	ErrNoProof = errors.New("auth: no proof for this operation — authenticate again")

	// ErrProofNotForThis — a live proof, presented for something it does not permit. DISTINCT from
	// ErrNoProof because the causes are different in kind: this one is a client bug, not an expiry,
	// and collapsing them would send a user to re-authenticate over a mismatch that re-authenticating
	// cannot fix.
	ErrProofNotForThis = errors.New("auth: this proof was issued for a different operation")
)

// Proofs holds minted, unspent proofs.
//
// IN MEMORY, FOLLOWING PasskeyCeremonies, AND THE COST IS THE SAME ONE RETRY. A proof is single-use
// and lives two minutes; persisting it would buy surviving a restart in the gap between proving and
// mutating, which the user resolves by tapping the button again. What it would cost is a table whose
// rows are credential-equivalents — worse than the challenge table that reasoning already rejected,
// because a proof AUTHORISES rather than merely continues.
type Proofs struct {
	mu  sync.Mutex
	now func() time.Time
	in  map[string]mintedProof
}

type mintedProof struct {
	op        ProofOperation
	target    string
	subject   ProofSubject
	sessionID string
	expires   time.Time
}

// NewProofs builds the store.
func NewProofs() *Proofs {
	return &Proofs{now: time.Now, in: map[string]mintedProof{}}
}

// Mint records a proof and returns its opaque token.
//
// FOUR BINDINGS, AND THE SPEC ENUMERATES THEM BECAUSE AN ENUMERATION IS READ AS EXHAUSTIVE (D4):
// single-use, operation, subject, and the SESSION that minted it. The last was added at spec review
// — a proof is a credential-equivalent for one operation, so a client that did not earn it must not
// be able to spend it, and the binding costs one comparison.
//
// The sweep is here rather than on a timer, for `PasskeyCeremonies`' reason: this map is bounded by
// how often a human confirms a credential change.
func (p *Proofs) Mint(op ProofOperation, target string, subject ProofSubject, sessionID string) (string, error) {
	if !op.valid() {
		return "", fmt.Errorf("auth: unknown proof operation %q", op)
	}
	// A TARGET IS REQUIRED EXACTLY WHERE IT IS MEANINGFUL, refused where it is not. A target on
	// `remove_password` would be a field nothing reads, which is how a binding becomes decorative.
	if op.needsTarget() && target == "" {
		return "", fmt.Errorf("auth: %s needs a target credential", op)
	}
	if !op.needsTarget() && target != "" {
		return "", fmt.Errorf("auth: %s takes no target, got %q", op, target)
	}
	if !subject.valid() {
		return "", errors.New("auth: a proof subject is either the password or one credential id")
	}
	// THE SESSION BINDING IS NOT OPTIONAL. Minting without one would produce a bearer token for a
	// privileged operation, which is the shape this whole mechanism exists to avoid.
	if sessionID == "" {
		return "", errors.New("auth: a proof must be bound to the session that minted it")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)

	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for k, v := range p.in {
		if now.After(v.expires) {
			delete(p.in, k)
		}
	}
	p.in[token] = mintedProof{
		op:        op,
		target:    target,
		subject:   subject,
		sessionID: sessionID,
		expires:   now.Add(proofTTL),
	}
	return token, nil
}

// Consume spends a proof, returning WHAT was presented so rule 2 can compare it against the target.
//
// SINGLE USE, AND THE ENTRY GOES WHATEVER HAPPENS NEXT — the same rule `PasskeyCeremonies.take`
// states, and for the same reason: a proof that survives a failed attempt is a proof that can be
// replayed against a second one. The cost is that a client which loses the response must
// re-authenticate, which is the cost the ceremony already pays.
//
// **IT IS SPENT EVEN ON MISMATCH.** That looks harsh and is the safer of the two: leaving a
// mismatched proof alive turns this into an oracle a holder can probe against all four operations,
// and the legitimate cost is one re-prompt after a client bug.
func (p *Proofs) Consume(token string, op ProofOperation, target, sessionID string) (ProofSubject, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	found, ok := p.in[token]
	delete(p.in, token)
	if !ok || p.now().After(found.expires) {
		return ProofSubject{}, ErrNoProof
	}
	if found.op != op || found.target != target {
		return ProofSubject{}, ErrProofNotForThis
	}
	// THE SESSION MUST BE THE MINTING ONE. Compared in constant time beside the others; it is a
	// secret in the same sense the token is.
	//
	// ErrNoProof, NOT ErrProofNotForThis — spec-review finding on quince#920, and what it fixes is a
	// sentence that was FALSE. *"This proof was issued for a different operation"* is untrue here: it
	// was issued for a different SESSION.
	//
	// WHAT DECIDES THE ERROR IS THE REMEDY, which is ErrNoProof's own stated rule. A session mismatch
	// shares that remedy exactly — the legitimate way to reach it is logging out and back in between
	// proving and mutating, where *start again* is precisely right. ErrProofNotForThis' cause is "a
	// client bug, not an expiry", which would send a user to fix something that is not broken.
	//
	// Non-disclosure agrees rather than pulling the other way: ErrNoProof is the LESS informative of
	// the two, so filing a mismatch there tells a holder less rather than more.
	if subtle.ConstantTimeCompare([]byte(found.sessionID), []byte(sessionID)) != 1 {
		return ProofSubject{}, ErrNoProof
	}
	return found.subject, nil
}

// Presented is what a credential-mutating call offers as its PRESENT authentication — qn.6n slice 4,
// rules 1 and 3.
//
// EXACTLY ONE OF THE TWO, and they are not interchangeable in cost: a password is verified here and
// now, where a proof was earned by an assertion at `reauth/finish` and is single-use. Both are real
// presentations, which is the ruling's point — *"the password remains the lighter alternative, since
// either factor is accepted"*.
type Presented struct {
	// Password is the admin password, typed into this request.
	Password string
	// Proof is a token from POST /api/auth/reauth/finish.
	Proof string
}

// RequirePresent enforces the ruling's rules 1 and 3: changing the credential set demands a PRESENT
// credential, not merely a session.
//
// IT RETURNS THE SUBJECT, which rules 1 and 3 do not need and rule 2 cannot work without. Slice 5
// compares it against the credential being removed; returning it here rather than adding it later
// keeps one function answering "what proved this request" for every caller.
//
// THE ONLY EXCEPTION IS AN INSTALL WITH NO CREDENTIALS AT ALL — first launch, or after
// `quince auth reset`. It is `Configured()`, the same predicate that decides `needs_setup`, so the
// exemption cannot drift from the state that makes first run legal. Nothing else is exempt: the
// ruling considered a carve-out for *"a credential exists but cannot be presented here"* and
// REJECTED it, because an attacker holding a stolen session controls the `Host` header and could
// manufacture that state with one crafted request — the waiver would hand them their own trigger.
//
// RATE-LIMITED ON THE LOGIN BUCKET when a password is presented, for `ChangePassword`'s reason:
// holding a session must not buy a fresh budget to guess in.
func (s *Service) RequirePresent(proofs *Proofs, pres Presented, op ProofOperation,
	target, sessionID, clientIP string) (ProofSubject, error) {
	configured, err := s.Configured()
	if err != nil {
		return ProofSubject{}, err
	}
	if !configured {
		// NOTHING TO PRESENT AND NOTHING TO PROTECT. The install is unclaimed, which is the state
		// `POST /api/auth/setup` is already open in; demanding proof here would make an install with
		// no credentials unrecoverable rather than merely empty.
		return ProofSubject{}, nil
	}

	if pres.Proof != "" {
		return proofs.Consume(pres.Proof, op, target, sessionID)
	}

	// PRESENTING NOTHING IS NOT PRESENTING A WRONG PASSWORD, and collapsing the two broke a shipping
	// flow — Operator-measured on the staging stand, 2026-08-14.
	//
	// This used to fall through to `verifyPassword("")` on an install that HAS a password, which is
	// false and returns `ErrBadPassword`. So a client that presented nothing at all was told **"current
	// password is incorrect"** about a field it never rendered, and — worse — could not tell that
	// refusal apart from a genuinely wrong password. `reauth_required` is the code every client retries
	// on; `bad_password` is the one it must NOT retry on, because re-authenticating cannot fix a typo.
	// So the retry that would have run the passkey ceremony never fired, on any surface that presents
	// nothing and expects to be told what to present.
	//
	// THE DISTINCTION IS ABSENT-VERSUS-WRONG, and it is the same one `decodeOptionalJSON` draws one
	// layer out: an empty body is a credential refusal, not a malformed request. A caller who typed a
	// wrong password still gets `ErrBadPassword` below, because they presented something.
	if pres.Password == "" {
		return ProofSubject{}, ErrNoProof
	}

	hash, hasPassword, err := s.store.GetSetting(settingPasswordHash)
	if err != nil {
		return ProofSubject{}, err
	}
	if !hasPassword {
		// PASSWORDLESS: there is no password to present, so a proof is the only thing that can
		// satisfy the rule — and this is the branch that used to accept an empty `current_password`
		// and no proof at all, which is what quince#888 item 3's table called "nothing".
		return ProofSubject{}, ErrNoProof
	}
	if !s.limiter.allow(clientIP, s.now()) {
		return ProofSubject{}, ErrRateLimited
	}
	ok, err := verifyPassword(pres.Password, hash)
	if err != nil {
		return ProofSubject{}, err
	}
	if !ok {
		s.audit("present_failed", clientIP)
		return ProofSubject{}, ErrBadPassword
	}
	return ProofSubject{Password: true}, nil
}
