package auth

import (
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/store"
)

// THE ENROLMENT CEREMONY — qn.13 slice 9b-2, spec D4, D4.1 and D5.
//
// A household member scans a QR on their phone and registers a passkey confined to one device. The
// pair below is the pre-auth half of that: no session exists, and what authorizes the registration
// is the enrolment secret (slice 9a) rather than a cookie.
//
// SPECIFIED AGAINST fact 8's PRECEDENT, NOT INVENTED. `BeginSetupPasskey` / `FinishSetupPasskey` are
// already a pre-auth registration pair that issues a session; the difference is what authorizes it —
// there, that the install is unconfigured; here, the secret. Same shape, different gate, and the
// reasons the existing one gives for its choices apply here unchanged unless noted.
//
// THREE THINGS THIS PAIR DOES THAT THE SETUP PAIR DOES NOT:
//
//   - It mints a DEVICE-SCOPED credential, and the scope comes off the secret rather than off the
//     request. A token whose scope is chosen by the scanner is not a scoped token (D4).
//   - It sends NO exclusion list (D4.1, slice 9b-1) — the page is reached with no session, so an
//     exclusion list would hand every admin credential id to whoever scanned the QR.
//   - It consumes the secret at REGISTRATION, not at scan, so a phone that opens the page and then
//     fails Face ID can try again with the same QR.

// BeginEnrolment starts a registration authorized by an enrolment secret.
//
// THE SECRET IS CHECKED, NOT SPENT. `Check` is deliberately not `Spend` — see D4 and the split in
// `Enrolments`. A ceremony that burned the QR here would make one failed biometric prompt cost the
// household member a trip back to the admin.
//
// RATE-LIMITED ON THE SHARED LOGIN BUCKET, for the reason `BeginSetupPasskey` gives: this is a
// pre-auth endpoint that allocates a challenge and a map entry, so an unmetered one is an amplifier.
// It also bounds guessing at the secret itself, which that path has no equivalent of — there the
// gate is `Configured()`, a fact about the install; here it is a 256-bit value in a URL.
func (s *Service) BeginEnrolment(cer *PasskeyCeremonies, enr *Enrolments, rpID, token, clientIP string,
) (any, string, error) {
	if !s.limiter.allow(clientIP, s.now()) {
		return nil, "", ErrRateLimited
	}
	en, err := enr.Check(token)
	if err != nil {
		return nil, "", err
	}
	// THE SCOPE COMES OFF THE RECORD. Nothing in the request names a device, and there is no
	// parameter here that could.
	return BeginPasskeyRegistration(s.store, cer, rpID, store.DeviceScope(en.ScopeUDID), DiscloseNothing())
}

// FinishEnrolment completes the registration, spends the secret, and issues a SCOPED session.
//
// IT ISSUES A SESSION for `FinishSetupPasskey`'s reason — the caller has just proved possession of a
// credential this install now holds, and making them sign in again immediately would be ceremony for
// its own sake. The session is scoped because the CREDENTIAL is: `sessions_auth` points at the
// credential it was minted by (0014), and scope resolves from there (D2).
//
// THE ORDER IS REGISTER, THEN SPEND, AND IT IS NOT INTERCHANGEABLE. Spending first would burn the QR
// on a registration that then failed — the exact case `Check`/`Spend` exists to separate. So the
// secret is spent only once a credential actually exists.
//
// THE COST OF THAT ORDER, STATED RATHER THAN DISCOVERED: two ceremonies begun from one QR can both
// finish, leaving two credentials scoped to the same device. `FinishSetupPasskey` has the same
// non-atomicity and accepts it on the grounds that both belong to whoever was at the machine during
// first run. The bound here is different and worth naming: both belong to whoever held the QR inside
// its few live minutes. Both are removable from the admin's passkey list, which is where the remedy
// is, and neither can be an admin credential — `Mint` refuses an admin scope, so the escalation this
// would otherwise permit has no representation to travel in.
//
// A SECOND CHECK, NOT A CACHED ANSWER. The secret is re-read here rather than trusted from Begin,
// because minutes can pass between the two calls: the admin may have revoked it, or it may have
// expired, and a ceremony that consulted only its own start would honour a QR the admin cancelled
// while the phone sat on the unlock sheet.
// RATE-LIMITED, LIKE BEGIN AND FOR A SHARPER REASON (quince#1426 review). Begin's comment claimed
// the bucket bounds guessing at the secret; it did not, because this door takes the same secret and
// was unmetered — so an attacker would simply use this one. Both of Begin's reasons apply here
// verbatim, and this call allocates MORE: a full WebAuthn attestation verification runs before
// anything can refuse.
//
// THE REPOSITORY HAS BOTH PATTERNS AND THIS IS THE SECOND. `FinishSetupPasskey` is unmetered
// correctly — its gate is `Configured()`, a boolean with nothing to guess.
// `FinishPasskeyAssertion` IS metered, because it accepts a caller-supplied value that decides the
// outcome. So does this.
func (s *Service) FinishEnrolment(cer *PasskeyCeremonies, enr *Enrolments, key, name, rpID, token string,
	r *http.Request, now time.Time, priorSessionID, clientIP string,
) (store.Passkey, store.AuthSession, string, error) {
	if !s.limiter.allow(clientIP, s.now()) {
		return store.Passkey{}, store.AuthSession{}, "", ErrRateLimited
	}
	en, err := enr.Check(token)
	if err != nil {
		return store.Passkey{}, store.AuthSession{}, "", err
	}
	pk, err := FinishPasskeyRegistration(s.store, cer, key, name, rpID, r, now, store.DeviceScope(en.ScopeUDID))
	if err != nil {
		return store.Passkey{}, store.AuthSession{}, "", err
	}
	// SPENT ONLY NOW. If this fails the credential still exists and the QR stays live, which is the
	// recoverable side: a second scan lands on a device that already has its passkey, and the admin
	// can see both the credential and the outstanding secret. The reverse — spent, no credential —
	// would strand the household member with nothing and no way back except asking for a new QR.
	if _, err := enr.Spend(token); err != nil {
		return store.Passkey{}, store.AuthSession{}, "", err
	}
	sess, csrf, err := s.mintSession(priorSessionID, &pk.CredentialID)
	if err != nil {
		// THE CREDENTIAL IS STORED AND THE SECRET IS SPENT, and that is still the recoverable side:
		// the passkey works, so the holder lands on the sign-in screen and uses it. The reverse —
		// a session with no credential — would be a scoped session nothing can re-establish.
		return store.Passkey{}, store.AuthSession{}, "", err
	}
	// NO UDID IN THE AUDIT DETAIL. `audit` puts the client IP there and nothing else; a device
	// identifier in an audit row is exactly what the privacy rule keeps out of anything durable.
	s.audit("enrol_passkey", "")
	return pk, sess, csrf, nil
}
