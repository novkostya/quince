package auth

import (
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/store"
)

// FIRST-RUN PASSKEY REGISTRATION, PRE-AUTH AND ONE-SHOT — qn.6m D5, and the reason it needs its own
// pair of endpoints rather than reusing the ones in passkey.go.
//
// Registration is SESSION REQUIRED by design: it is something an authenticated admin does. First run
// has no session, because creating one is what first run is for. So a screen that offers "password
// AND a passkey" needs nothing new — setup returns a session and registration follows on the same
// click — but "a passkey INSTEAD of a password" has nowhere to register against.
//
// THREE WAYS TO CLOSE THAT, AND TWO ARE TRAPS:
//
//   - Exempt the existing registration pair conditionally. `authExempt` is exact-path AND
//     unconditional, and that is its whole value; a membership test that depends on `needs_setup`
//     puts the first state check into the one structure that has none.
//   - Generate a throwaway password, register, then delete it. Needs no new endpoint, and if
//     registration fails in that window the user is locked out of their own install by a password
//     they never saw. A lockout bug built on purpose.
//   - A DISTINCT PRE-AUTH PAIR, one-shot like `POST /api/auth/setup` itself. Taken.
//
// WHY THIS ADDS NO EXPOSURE. First run is already first-come-first-served for the PASSWORD: anyone
// who can reach an unconfigured quince can call `POST /api/auth/setup` and own it. This makes it
// first-come-first-served for a CREDENTIAL on exactly the same terms and behind exactly the same
// one-shot guard — `Configured()`, which since qn.6m D3 counts passkeys as well as passwords, so the
// second call finds the install claimed by the credential the first one created.

// BeginSetupPasskey starts a first-run registration ceremony. It refuses once this install is
// configured by anything at all — a password OR any passkey (D3).
//
// RATE-LIMITED ON THE SHARED LOGIN BUCKET, for the same reason SetPassword is: on an UNCONFIGURED
// instance this route is legitimately open to anybody, and a ceremony costs a challenge, a store
// read and a map entry. Cheaper than argon2id, which is why quince#463 bit setup and not this — but
// an unmetered pre-auth endpoint that allocates is still an amplifier, and giving it its own bucket
// would let one client spend both budgets.
func (s *Service) BeginSetupPasskey(cer *PasskeyCeremonies, rpID, clientIP string) (any, string, error) {
	if !s.limiter.allow(clientIP, s.now()) {
		return nil, "", ErrRateLimited
	}
	configured, err := s.Configured()
	if err != nil {
		return nil, "", err
	}
	if configured {
		return nil, "", ErrAlreadyConfigured
	}
	return BeginPasskeyRegistration(s.store, cer, rpID)
}

// FinishSetupPasskey completes a first-run registration and ISSUES A SESSION, exactly as
// `POST /api/auth/setup` does — the user has just proved possession of a credential this install now
// holds, and making them sign in again immediately would be ceremony for its own sake.
//
// THE ONE-SHOT GUARD IS CHECKED AGAIN HERE, not only in Begin. Two clients can both pass Begin on an
// unconfigured install; the credential write is what decides, and re-checking means the loser gets a
// 409 rather than silently adding a second admin credential to somebody else's box. This is the same
// shape SetPassword uses, where the cheap check comes first and SetSettingIfAbsent remains the
// authority.
//
// NOT ATOMIC ACROSS THE CHECK AND THE WRITE, and stated rather than implied: two ceremonies begun
// simultaneously on a virgin install can both finish, leaving two credentials. Both belong to
// whoever was physically at the machine during first run, which is the same trust window
// `POST /api/auth/setup` already has — and `quince auth reset` removes every credential, so the
// remedy is the one the rung already ships. Worth revisiting if first run ever stops being a
// first-come-first-served moment.
func (s *Service) FinishSetupPasskey(cer *PasskeyCeremonies, key, name, rpID string,
	r *http.Request, now time.Time, priorSessionID string,
) (store.Passkey, store.AuthSession, string, error) {
	configured, err := s.Configured()
	if err != nil {
		return store.Passkey{}, store.AuthSession{}, "", err
	}
	if configured {
		return store.Passkey{}, store.AuthSession{}, "", ErrAlreadyConfigured
	}

	pk, err := FinishPasskeyRegistration(s.store, cer, key, name, rpID, r, now)
	if err != nil {
		return store.Passkey{}, store.AuthSession{}, "", err
	}

	sess, csrf, err := s.mintSession(priorSessionID)
	if err != nil {
		// THE CREDENTIAL IS ALREADY STORED AND THAT IS THE RECOVERABLE SIDE OF THIS FAILURE. The
		// install is now configured, the passkey works, and the user simply lands on the login
		// screen and uses it. The reverse — a session with no credential — would be an admin
		// session on an install nobody can ever sign into again.
		return store.Passkey{}, store.AuthSession{}, "", err
	}
	s.audit("setup_passkey", "")
	return pk, sess, csrf, nil
}

// mintSession creates a fresh session and CSRF token, superseding the caller's own prior session and
// no other device's (quince#373). Extracted from Login so the three paths that issue a session —
// password login, passkey assertion, and first-run passkey setup — mint it identically rather than
// growing three subtly different copies.
func (s *Service) mintSession(priorSessionID string) (store.AuthSession, string, error) {
	now := s.now()
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
	}
	if err := s.store.CreateAuthSession(sess); err != nil {
		return store.AuthSession{}, "", err
	}
	return sess, NewCSRFToken(), nil
}
