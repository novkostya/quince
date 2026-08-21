package auth

import (
	"encoding/base64"
	"net/http"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/store"
)

// Passkey ASSERTION — qn.6k slice 3c. The half that signs you in, and therefore the half that is
// pre-auth, rate limited, and reachable on a storageless install.
//
// DISCOVERABLE, which is what one-admin-no-accounts permits: no username field and no account
// picker. The browser offers the passkey in the same dropdown as saved passwords under conditional
// mediation, which is the login surface the Operator ruled for on quince#657.

// BeginPasskeyAssertion starts a login ceremony and returns the options plus a single-use ceremony
// key. `hint` is the credential id this browser remembers, or empty for the discoverable flow.
//
// RATE LIMITED ON THE SAME BUCKET AS PASSWORD LOGIN. They are the same resource — a pre-auth
// credential endpoint on the same box — and the same attacker. Giving passkeys their own bucket
// would let one client spend both budgets, which is the reasoning `SetPassword` already records for
// sharing the login limiter.
//
// THE HINT SELECTS WHAT IS OFFERED. IT NEVER GRANTS (spec D2.2, G8). The ceremony stays
// DISCOVERABLE either way — `BeginDiscoverableLogin` with an allow-list, exactly as `reauth.go`
// narrows a removal — so authority still resolves from `credential_id` after the assertion, and
// nothing about the offer decides who signed in.
//
// IT CAN ONLY NARROW, and that is a property of the library rather than of this code.
// `go-webauthn` v0.17.4 `webauthn/login.go:292-301`: with a NON-EMPTY `AllowedCredentialIDs` the
// asserted id must be in the list AND owned by the resolved user; an empty list means any. So a
// caller naming a credential id that is not theirs restricts themselves to a signature they cannot
// produce. There is no direction in which a hint widens what a caller may do.
//
// AND THE HINT IS ECHOED, NOT LOOKED UP — the decision worth knowing before changing this. Checking
// the id against the store, and quietly falling back when it is absent, would make this PRE-AUTH
// endpoint a credential-presence oracle: a populated allow-list versus an empty one is visible to
// whoever asked, so it would answer "does this quince know this passkey" to anybody who can reach
// the address. That is the property the FINISH handler already protects with its deliberately
// indistinguishable 401, and contracts.md states it as a rule for this pair.
//
// SO A REVOKED CREDENTIAL FALLS BACK IN THE BROWSER, which is what D2.2 describes: the platform
// reports no passkey available, and the client retries discoverable rather than dead-ending on a
// page that should have worked. Nothing here can tell a revoked id from a fabricated one, and it
// does not need to — the finish path refuses both.
//
// A MALFORMED HINT IS IGNORED, NOT REFUSED. It is a hint; the honest response to one that makes no
// sense is today's behaviour, not an error on a sign-in page nobody asked to see fail.
func (s *Service) BeginPasskeyAssertion(cer *PasskeyCeremonies, rpID, clientIP, hint string) (any, string, error) {
	if !s.limiter.allow(clientIP, s.now()) {
		return nil, "", ErrRateLimited
	}
	wa, err := relyingParty(rpID)
	if err != nil {
		return nil, "", err
	}
	assertion, session, err := wa.BeginDiscoverableLogin(allowedForHint(hint)...)
	if err != nil {
		return nil, "", err
	}
	// AN ASSERTION CARRIES NO SCOPE. Scope resolves from `credential_id` AFTER the assertion (D2),
	// so a login ceremony has nothing to record and nothing to compare — `AdminScope()` here would
	// be a claim, not a fact. The zero value says "not applicable", and only registration compares.
	// AN ASSERTION RECORDS NO HANDLE either: the user handle arrives IN the assertion, and scope
	// resolves from `credential_id` afterwards (D2). Nothing here has an identity to carry.
	key, err := cer.put(session, rpID, ceremonyAssert, store.Scope{}, nil)
	if err != nil {
		return nil, "", err
	}
	return assertion, key, nil
}

// FinishPasskeyAssertion verifies the authenticator's response and, on success, mints a session.
//
// It returns the same pair `Login` does — the session and a fresh CSRF token — because a passkey
// login IS a login: the session layer is untouched by this rung, and an assertion sets exactly the
// cookie a password would have.
func (s *Service) FinishPasskeyAssertion(cer *PasskeyCeremonies, key, rpID, clientIP string,
	r *http.Request, priorSessionID string) (store.AuthSession, string, error) {
	now := s.now()
	if !s.limiter.allow(clientIP, now) {
		return store.AuthSession{}, "", ErrRateLimited
	}
	pending, ok := cer.take(key, ceremonyAssert)
	if !ok {
		return store.AuthSession{}, "", ErrNoChallenge
	}
	// The ceremony's own rpId wins, for the reason registration's does: the authenticator signed
	// for the domain the challenge was issued on, and finishing against another would accept a
	// signature made for somewhere else.
	if pending.rpID != rpID {
		return store.AuthSession{}, "", ErrRPIDMismatch{Registered: pending.rpID, Presented: rpID}
	}
	wa, err := relyingParty(pending.rpID)
	if err != nil {
		return store.AuthSession{}, "", err
	}
	// THE HANDLE IS NOW THE CREDENTIALS OWN, resolved inside the lookup below once the
	// assertion has named which credential it is (quince#1393). It cannot be read before that:
	// a discoverable login knows nothing about the caller until the authenticator answers.

	// THE STORED rp_id COMPARISON IS WHAT CARRIES THIS PATH, and it is not redundant with the
	// library's origin check. Because the relying party is built per request, `RPOrigins` derives
	// from the SAME header as the rpId — so the library is checking the request against itself and
	// is not an independent control here (architect, quince#830). `ResolveCredential` comparing the
	// credential's STORED domain is the check that means anything. Do not remove it on the grounds
	// that the library validates the origin.
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
		handle, err := handleOf(s.store, pk)
		if err != nil {
			return nil, err
		}
		return passkeyUser{handle: handle, creds: creds}, nil
	}

	_, cred, err := wa.FinishPasskeyLogin(lookup, pending.session, r)
	if err != nil {
		return store.AuthSession{}, "", err
	}

	// The counter is recorded AFTER verification, which is where the library's clone detection has
	// already run. Storing it is not a policy decision; refusing a regression is, and that is the
	// library's (spec D4).
	if err := s.store.TouchPasskey(resolved.CredentialID, cred.Authenticator.SignCount,
		&cred.Flags.BackupState, now); err != nil {
		return store.AuthSession{}, "", err
	}

	// Same rotation policy as password login, for the same reason: this client's own prior session
	// is superseded and every OTHER device keeps its own (quince#373).
	if priorSessionID != "" {
		if err := s.store.DeleteAuthSession(priorSessionID); err != nil {
			return store.AuthSession{}, "", err
		}
	}
	sess := store.AuthSession{
		ID:         id.Token(32),
		CreatedAt:  now,
		LastSeenAt: now,
		ExpiresAt:  now.Add(s.absoluteTimeout),
		// THE CREDENTIAL THAT JUST PROVED ITSELF, which is what makes this session attributable.
		// `resolved` is the row the assertion was verified against, so this is the one identity
		// quince can state without inferring anything (spec D1).
		CredentialID: &resolved.CredentialID,
	}
	if err := s.store.CreateAuthSession(sess); err != nil {
		return store.AuthSession{}, "", err
	}
	s.limiter.reset(clientIP)
	s.audit("login_passkey", clientIP)
	return sess, NewCSRFToken(), nil
}

// allowedForHint turns a remembered credential id into the login options that offer it, and returns
// nothing at all when there is no usable hint — which is the discoverable flow, unchanged.
//
// NO STORE ACCESS, DELIBERATELY, and `BeginPasskeyAssertion`'s comment carries the reason: a lookup
// here would make a pre-auth endpoint answer whether a credential exists. This function cannot
// consult the store because it is not given one, which is a cheaper guarantee than a comment.
//
// THE DECODE IS A SHAPE CHECK, NOT A VALIDATION. `allowCredentials` carries raw bytes and the hint
// arrives base64url, so the encoding has to be undone; a string that is not base64url could not
// name any credential, so there is nothing to offer and the ceremony stays discoverable. That is
// the same "do not remember" degradation `passkeyHint.ts` already applies on the browser side.
//
// AN ALLOW-LIST NAMING NOTHING WOULD BE WORSE THAN NO ALLOW-LIST, which is what the `len(raw) == 0`
// arm is for: a descriptor with an empty id makes the list NON-empty, and the library then enforces
// it at finish and refuses every credential — a sign-in page that can never succeed.
//
// IT IS UNREACHABLE TODAY, AND IT IS KEPT ANYWAY. Measured against this encoder: the ONLY string
// that decodes to zero bytes without an error is `""`, and the `hint == ""` arm above already
// returns on that — a one-character input is `illegal base64 data`, and two characters decode to
// one byte. So nothing that reaches the decode can produce an empty result.
//
// SAID PLAINLY BECAUSE NO TEST COVERS IT AND NONE CAN: deleting this arm leaves every test green,
// which was measured by deleting it. It is here so that removing the empty-hint check above cannot
// silently turn a malformed memory into an unusable login page, and that is the whole of its claim.
func allowedForHint(hint string) []webauthn.LoginOption {
	if hint == "" {
		return nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(hint)
	if err != nil || len(raw) == 0 {
		return nil
	}
	return []webauthn.LoginOption{webauthn.WithAllowedCredentials([]protocol.CredentialDescriptor{{
		Type:         protocol.PublicKeyCredentialType,
		CredentialID: raw,
	}})}
}
