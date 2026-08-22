package httpapi

import (
	"errors"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// The scope an authenticated client is told about itself — qn.13 slice 8d, spec D8, ruled on
// quince#1443.
//
// ONE RESOLVER FOR FIVE EMITTERS. `wire.AuthStatus` is written at five places — the status read, the
// password login, the passkey login, the first-run registration and the enrolment finish — and every
// one of them must answer this the same way. Five copies of the ordering below is five chances to
// get it wrong once, in a field whose wrong value hides a household member's confinement from them.

// scopeWireFor resolves a principal's scope into the wire shape: nil for an ADMIN principal, and a
// named device for a scoped one.
//
// THE ERROR IS CHECKED BEFORE THE VALUE, AND THAT ORDER IS THE WHOLE OF IT. `ScopeOf` returns
// `("", nil)` for an admin and `("", ErrCredentialRevoked)` for a credential that has been removed —
// the same empty string. Reading the value first turns a revocation into an ADMIN answer, which is
// the shape quince#1441's review caught on the transport route and named as converting a revocation
// into a privilege grant. Here it would tell a revoked household member they administer quince.
func (d Deps) scopeWireFor(p auth.Principal) (*wire.PasskeyScope, error) {
	udid, err := d.Auth.ScopeOf(p)
	if err != nil {
		return nil, err
	}
	if udid == "" {
		return nil, nil
	}
	return &wire.PasskeyScope{UDID: udid}, nil
}

// authStatusFor builds the whole payload for a session that has just authenticated, or for one
// presented at the status read.
//
// A CREDENTIAL THAT NO LONGER EXISTS IS NOT AUTHENTICATED, and this is the case the ruling did not
// cover because the field is what raises it. quince#1001 ends a credential's sessions when it is
// removed, so reaching here means the narrow window between the two — or a bug. Either way quince
// cannot say whose session this is.
//
// SO IT REPORTS `needs_login` RATHER THAN GUESSING, and the alternatives are both worse. Reporting
// `authenticated` with a nil scope says ADMIN, which is the privilege grant above. Answering `500`
// on the one endpoint the shell reads to boot would strand a user whose credential was revoked
// behind a broken app instead of a login form — and a login form is exactly where they should be,
// since the way back is to be issued another credential.
//
// IT IS NOT A NEW REFUSAL. The session is already dead by quince#1001's rule; this only stops the
// status endpoint describing it as alive.
func (d Deps) authStatusFor(sessionID, csrf string) (wire.AuthStatus, error) {
	state, err := d.Auth.Status(sessionID)
	if err != nil {
		return wire.AuthStatus{}, err
	}
	out := wire.AuthStatus{State: state, CSRFToken: csrf}
	if state != auth.StateAuthenticated {
		// NO SCOPE ON A STATE THAT CANNOT CARRY ONE. `needs_setup` and `needs_login` have no
		// principal to describe, and `state` is what tells a client that — see the field's comment.
		return out, nil
	}
	sess, err := d.Auth.Authenticate(sessionID)
	if err != nil {
		// `Status` just said authenticated, so this is a race with a session ending underneath the
		// read. The narrow answer, for the same reason as the revoked case below.
		out.State = auth.StateNeedsLogin
		return out, nil
	}
	scope, err := d.scopeWireFor(auth.PrincipalOf(sess))
	if err != nil {
		if errors.Is(err, auth.ErrCredentialRevoked) {
			out.State = auth.StateNeedsLogin
			return out, nil
		}
		return wire.AuthStatus{}, err
	}
	out.Scope = scope
	return out, nil
}

// mintedStatus is the payload for a session THIS request just created — the four login paths, as
// opposed to the status read above.
//
// NO `Authenticate` ROUND TRIP AND NO REVOKED CASE. The credential was used to mint this session a
// few statements ago, so it exists; the only way this errors is the store failing under it.
//
// CALL IT BEFORE SETTING THE COOKIES. A failure has to be answerable with a refusal, and once the
// session cookie is on the wire the client is logged in and the only remaining options are a 500
// over a live session or a payload that says ADMIN because the scope could not be read. The second
// is the privilege-shaped answer this whole file exists to avoid, so the ordering is load-bearing
// rather than tidy.
//
// THE CALLER IS NOT STRANDED BY THAT. The session row and, on the enrolment path, the credential
// both exist — so the way forward is to sign in with the credential that was just created, which is
// the ordinary path rather than a recovery one.
func (d Deps) mintedStatus(p auth.Principal, csrf string) (wire.AuthStatus, error) {
	scope, err := d.scopeWireFor(p)
	if err != nil {
		return wire.AuthStatus{}, err
	}
	return wire.AuthStatus{State: auth.StateAuthenticated, CSRFToken: csrf, Scope: scope}, nil
}
