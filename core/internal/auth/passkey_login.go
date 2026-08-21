package auth

import (
	"encoding/base64"
	"net/http"

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

// BeginPasskeyAssertion starts a discoverable login and returns the options plus a single-use
// ceremony key.
//
// RATE LIMITED ON THE SAME BUCKET AS PASSWORD LOGIN. They are the same resource — a pre-auth
// credential endpoint on the same box — and the same attacker. Giving passkeys their own bucket
// would let one client spend both budgets, which is the reasoning `SetPassword` already records for
// sharing the login limiter.
func (s *Service) BeginPasskeyAssertion(cer *PasskeyCeremonies, rpID, clientIP string) (any, string, error) {
	if !s.limiter.allow(clientIP, s.now()) {
		return nil, "", ErrRateLimited
	}
	wa, err := relyingParty(rpID)
	if err != nil {
		return nil, "", err
	}
	assertion, session, err := wa.BeginDiscoverableLogin()
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
