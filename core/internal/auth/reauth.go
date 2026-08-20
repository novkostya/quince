package auth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/novkostya/quince/core/internal/store"
)

// RE-AUTHENTICATION — qn.6n slice 3, D3. The assertion that proves a PRESENT credential without
// signing anybody in.
//
// A SEPARATE PAIR FROM `passkeys/login/*`, WHICH IS THE WHOLE OF D3. Those three routes are pre-auth
// by exact path in all three allowlists — `authExempt`, `setupAllowed` and `csrfExempt` — and giving
// the least-guarded routes in the system a second job whose purpose is gating privileged operations
// is the wrong trade. These two require a session and are in none of the lists.
//
// AND IT MINTS NO SESSION, which is the sharpest difference from the endpoint it is modelled on.
// `FinishPasskeyAssertion` exists to produce one; this produces a *proof*. Issuing a session here
// would make it a second login path reachable from an authenticated context, and would bind the
// proof to a session id that no longer exists by the time the mutating call arrives (spec D4).

// ReauthCeremonies holds in-flight re-authentication challenges.
//
// ITS OWN STORE RATHER THAN A FIELD ON PasskeyCeremonies, and the isolation is a guard rather than
// tidiness: a key minted for a LOGIN cannot be presented here, and a key minted here cannot be
// presented at login, because neither map contains the other's keys. Overloading one store would
// make that a comparison somebody has to remember to write.
//
// In memory for `PasskeyCeremonies`' reason, sharpened: what a reauth ceremony is worth is a proof,
// and a proof authorises rather than continues.
type ReauthCeremonies struct {
	mu  sync.Mutex
	now func() time.Time
	in  map[string]pendingReauth
}

type pendingReauth struct {
	session webauthn.SessionData
	rpID    string
	// THE OPERATION TRAVELS WITH THE CEREMONY, NOT WITH THE FINISH CALL. Naming it at `begin` and
	// carrying it is what makes the proof bound to one operation: a client that could choose the
	// operation at `finish` could assert once and pick the most valuable use afterwards, which is the
	// ambient grant the sudo window was rejected for.
	op     ProofOperation
	target string
	// sessionID is the session that BEGAN the ceremony. Carried so the proof can be bound to it
	// without trusting the finish call to name its own session honestly.
	sessionID string
	expires   time.Time
}

// NewReauthCeremonies builds the in-flight challenge store.
func NewReauthCeremonies() *ReauthCeremonies {
	return &ReauthCeremonies{now: time.Now, in: map[string]pendingReauth{}}
}

func (c *ReauthCeremonies) put(p pendingReauth) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	key := base64.RawURLEncoding.EncodeToString(raw)

	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for k, v := range c.in {
		if now.After(v.expires) {
			delete(c.in, k)
		}
	}
	p.expires = now.Add(challengeTTL)
	c.in[key] = p
	return key, nil
}

// take consumes a ceremony. SINGLE USE, whatever follows — `PasskeyCeremonies.take`'s rule, for its
// reason: a challenge that survives a failed attempt can be replayed against a second one.
func (c *ReauthCeremonies) take(key string) (pendingReauth, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p, ok := c.in[key]
	delete(c.in, key)
	if !ok || c.now().After(p.expires) {
		return pendingReauth{}, false
	}
	return p, true
}

// BeginReauth starts a discoverable assertion for one credential-changing operation.
//
// THE OPERATION IS VALIDATED HERE AND AGAIN AT MINT. Not belt-and-braces: refusing early means a
// user is never sent to a Face ID sheet that cannot produce a usable proof, and the check at mint is
// what makes `Proofs` safe for any caller rather than only this one.
//
// RATE LIMITED ON THE LOGIN BUCKET (spec D7), for the reason `ChangePassword` already states:
// somebody holding a session must not get a fresh budget to guess with. It verifies a credential,
// so it is the same resource as `passkeys/login/*`.
func (s *Service) BeginReauth(cer *ReauthCeremonies, rpID, clientIP string,
	op ProofOperation, target, sessionID string) (any, string, error) {
	if !op.valid() {
		return nil, "", fmt.Errorf("auth: unknown proof operation %q", op)
	}
	if op.needsTarget() && target == "" {
		return nil, "", fmt.Errorf("auth: %s needs a target credential", op)
	}
	if !op.needsTarget() && target != "" {
		return nil, "", fmt.Errorf("auth: %s takes no target, got %q", op, target)
	}
	if sessionID == "" {
		return nil, "", ErrNoSession
	}
	// REFUSE BEFORE THE SHEET when this address holds nothing that could produce a usable proof.
	//
	// FOR `remove_password` THIS IS COPY, NOT A GUARD, and the distinction is D2's: the guard is the
	// assertion itself, which no row can fake, and what this buys is that a user who cannot do the
	// thing is told why instead of meeting an empty passkey sheet.
	//
	// FOR `remove_passkey` IT IS ALSO WHAT KEEPS THE ALLOW-LIST BELOW NON-EMPTY, which is load-bearing:
	// an empty `allowCredentials` means ANY credential, so a ceremony begun with nothing to allow
	// would offer the very passkey being removed. Refusing here is what makes that state unreachable.
	if err := s.provable(op, rpID, target); err != nil {
		return nil, "", err
	}
	if !s.limiter.allow(clientIP, s.now()) {
		return nil, "", ErrRateLimited
	}

	wa, err := relyingParty(rpID)
	if err != nil {
		return nil, "", err
	}

	// THE TARGET IS EXCLUDED FROM THE CEREMONY ITSELF for a removal — rule 2, one layer earlier than
	// the subject comparison in `RemovePasskey`. Two independent refusals of a self-proof: the
	// browser is not offered the credential, and `go-webauthn` checks the asserted id against
	// `session.AllowedCredentialIDs` at finish when that list is non-empty (`login.go:292-301`).
	//
	// EVERY OTHER OPERATION STAYS DISCOVERABLE, with no allow-list at all. `add_passkey` and
	// `set_password` are about the credential SET rather than a member of it, so restricting them
	// would exclude nothing and cost the usernameless flow qn.6k exists for.
	// EVERY REAUTH OPERATION IS AN ADMIN OPERATION, so every ceremony admits only admin
	// credentials as proof (spec D6). This paragraph used to read "every other operation stays
	// discoverable, with no allow-list at all", on the grounds that `add_passkey` and
	// `set_password` concern the credential SET rather than a member of it. Scope breaks that:
	// there is no longer ONE set, and those operate on the ADMIN's — so admitting any
	// credential would let a scoped holder prove an admin operation.
	//
	// IT COSTS THE USERNAMELESS FLOW NOTHING. An allow-list names credentials; it does not ask
	// for a username, and the platform still chooses among what is offered. And it is a NO-OP
	// until a scoped credential exists, which is why the invariant lands before the rows do.
	var opts []webauthn.LoginOption
	if op == OpRemovePasskey {
		allowed, err := allowedForRemoval(s.store, rpID, target)
		if err != nil {
			return nil, "", err
		}
		opts = append(opts, webauthn.WithAllowedCredentials(allowed))
	} else {
		admin, err := adminCredentials(s.store, rpID)
		if err != nil {
			return nil, "", err
		}
		opts = append(opts, webauthn.WithAllowedCredentials(credentialDescriptors(admin)))
	}
	assertion, session, err := wa.BeginDiscoverableLogin(opts...)
	if err != nil {
		return nil, "", err
	}
	key, err := cer.put(pendingReauth{
		session:   *session,
		rpID:      rpID,
		op:        op,
		target:    target,
		sessionID: sessionID,
	})
	if err != nil {
		return nil, "", err
	}
	return assertion, key, nil
}

// provable reports whether a ceremony for this operation could produce a proof this install would
// accept, refusing with the sentence that names what to do about it.
//
// ONLY THE TWO REMOVALS ARE CHECKED HERE. `set_password` and `add_passkey` take the password as
// their lighter factor, so a caller with no passkey at this address still has a route and refusing
// would close it — that would be D5's rejected carve-out wearing a helpful face. An operation
// NOTHING can prove is a dead end worth naming; an operation this particular client cannot prove
// is not.
func (s *Service) provable(op ProofOperation, rpID, target string) error {
	switch op {
	case OpRemovePasskey:
		// RULE 2's OWN CASE: the target cannot prove its own removal, so the question is whether
		// anything ELSE at this address can. The message distinguishes a dead end from a
		// redirection to the password — see ErrLastPasskey.
		return s.passkeyRemovalRefusal(target, rpID)
	case OpRemovePassword:
		// falls through to the scan below
	default:
		return nil
	}
	// ADMIN CREDENTIALS ONLY (spec D6). This is the `OpRemovePassword` scan, and removing the
	// password is only safe if an ADMIN credential survives it. A scoped credential is not a
	// way back in for the admin — its holder can reach one device and nothing that administers
	// quince — so counting one here would report a route that does not exist and produce the
	// lockout ErrLastCredential is the last guard against.
	rows, err := s.store.ListAdminPasskeys()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(rows))
	elsewhere := make([]string, 0, len(rows))
	for _, p := range rows {
		if p.RPID == rpID {
			return nil
		}
		if !seen[p.RPID] {
			seen[p.RPID] = true
			elsewhere = append(elsewhere, p.RPID)
		}
	}
	return ErrLastCredential{Presented: rpID, Elsewhere: elsewhere}
}

// FinishReauth verifies the assertion and mints a proof for the operation the ceremony named.
//
// IT RETURNS A TOKEN AND NOTHING ELSE. No session, no CSRF token, no cookie — see the file header.
// The proof's subject is the credential that actually asserted, which is what rule 2 compares
// against the credential being removed.
func (s *Service) FinishReauth(cer *ReauthCeremonies, proofs *Proofs, key, rpID, clientIP string,
	r *http.Request, sessionID string) (string, error) {
	now := s.now()
	if !s.limiter.allow(clientIP, now) {
		return "", ErrRateLimited
	}
	pending, ok := cer.take(key)
	if !ok {
		return "", ErrNoChallenge
	}
	// THE CEREMONY'S OWN SESSION WINS. A ceremony begun by one client and finished by another is not
	// a re-authentication of the second one — and binding the proof to the finisher's session would
	// let a stolen session inherit a proof the owner earned.
	if pending.sessionID != sessionID {
		return "", ErrNoChallenge
	}
	// The ceremony's own rpId wins, for `FinishPasskeyAssertion`'s reason: the authenticator signed
	// for the domain the challenge was issued on.
	if pending.rpID != rpID {
		return "", ErrRPIDMismatch{Registered: pending.rpID, Presented: rpID}
	}

	wa, err := relyingParty(pending.rpID)
	if err != nil {
		return "", err
	}
	handle, err := userHandle(s.store)
	if err != nil {
		return "", err
	}

	// THE STORED rp_id COMPARISON CARRIES THIS PATH TOO. `RPOrigins` derives from the same header as
	// the rpId, so the library is checking the request against itself and is not an independent
	// control (architect, quince#830). `ResolveCredential` comparing the credential's STORED domain
	// is the check that means anything — do not remove it because the library validates the origin.
	var resolved store.Passkey
	lookup := func(rawID, _ []byte) (webauthn.User, error) {
		pk, err := ResolveCredential(s.store, base64.RawURLEncoding.EncodeToString(rawID), pending.rpID)
		if err != nil {
			return nil, err
		}
		resolved = pk
		creds, err := existingCredentials(s.store, pending.rpID)
		if err != nil {
			return nil, err
		}
		return passkeyUser{handle: handle, creds: creds}, nil
	}

	_, cred, err := wa.FinishPasskeyLogin(lookup, pending.session, r)
	if err != nil {
		return "", err
	}

	// The counter is recorded after verification, exactly as login does: clone detection is the
	// library's and has already run. A re-authentication is a real use of the credential, so
	// skipping this would leave the counter behind and make the NEXT login look like a regression.
	if err := s.store.TouchPasskey(resolved.CredentialID, cred.Authenticator.SignCount,
		&cred.Flags.BackupState, now); err != nil {
		return "", err
	}

	token, err := proofs.Mint(pending.op, pending.target,
		ProofSubject{CredentialID: resolved.CredentialID}, pending.sessionID)
	if err != nil {
		return "", err
	}
	s.limiter.reset(clientIP)
	// A DISTINCT AUDIT EVENT FROM `login_passkey`. The same credential was presented, and what it
	// bought is different: an audit trail that could not tell a sign-in from a credential change
	// would be missing the entries that matter most.
	s.audit("reauth_passkey", clientIP)
	return token, nil
}

// allowedForRemoval lists the credentials at this address that MAY prove a `remove_passkey` — every
// one except the target.
//
// AN EMPTY LIST MUST NEVER REACH THE CEREMONY: to WebAuthn, and to the library's finish-time check,
// an empty `allowCredentials` means ANY credential. A ceremony begun with nothing to allow would
// therefore offer the exact passkey being removed, which is the opposite of what this function is
// for. `provable` refuses that state before this runs, so the emptiness is unreachable rather than
// merely unlikely — stated here because the failure would be silent and would look like a working
// prompt.
func allowedForRemoval(st *store.Store, rpID, target string) ([]protocol.CredentialDescriptor, error) {
	// ADMIN CREDENTIALS ONLY (spec D6, and the ceremony half of it). Removing a credential is
	// an admin operation, so the set that may PROVE it is the admin set — offering a scoped
	// credential here would let a household member prove an admin removal.
	creds, err := adminCredentials(st, rpID)
	if err != nil {
		return nil, err
	}
	out := make([]protocol.CredentialDescriptor, 0, len(creds))
	for _, c := range creds {
		// The stored id is base64url; `webauthn.Credential.ID` is the raw bytes.
		if base64.RawURLEncoding.EncodeToString(c.ID) == target {
			continue
		}
		out = append(out, c.Descriptor())
	}
	return out, nil
}
