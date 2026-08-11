package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Passkey registration — qn.6k slice 3b, contracts §1.
//
// BOTH ENDPOINTS REQUIRE A SESSION, which is why this file touches neither exact-path allowlist.
// Registration is by definition something an already-authenticated admin does; the pre-auth pair is
// assertion, and it arrives in 3c with the allowlist entries and the rate limiter it needs.

// writePasskeyError maps the ceremony's failures to statuses.
//
// THE rpId MISMATCH AND THE UNSUPPORTED TIER BOTH CARRY THEIR OWN MESSAGE THROUGH, and that is the
// point of them being typed errors: each names a domain or an address, and a generic string here
// would throw away the only thing that makes them actionable (spec D2).
func (d Deps) writePasskeyError(w http.ResponseWriter, err error) bool {
	var unsupported auth.ErrUnsupportedRPID
	var mismatch auth.ErrRPIDMismatch
	switch {
	case errors.As(err, &unsupported):
		writeError(w, d.Log, http.StatusConflict, "passkeys_unsupported_here", unsupported.Error())
	case errors.As(err, &mismatch):
		writeError(w, d.Log, http.StatusConflict, "passkey_rp_mismatch", mismatch.Error())
	case errors.Is(err, auth.ErrNoChallenge):
		writeError(w, d.Log, http.StatusBadRequest, "no_ceremony",
			"this passkey setup expired or was already completed — start again")
	default:
		return false
	}
	return true
}

// POST /api/auth/passkeys/register/begin → {ceremony, options}
func (d Deps) handlePasskeyRegisterBegin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rpID := auth.RPIDFromRequest(r)
		options, ceremony, err := auth.BeginPasskeyRegistration(d.Store, d.Passkeys, rpID)
		if err != nil {
			if d.writePasskeyError(w, err) {
				return
			}
			d.Log.Error("passkey registration begin failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not start passkey setup")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.PasskeyRegisterBegin{Ceremony: ceremony, Options: options})
	}
}

// POST /api/auth/passkeys/register/finish {ceremony, name} → 201 {passkey}
//
// The authenticator's response is read from the REQUEST BODY by the library rather than decoded
// here, which is why the ceremony key and the name arrive as QUERY PARAMETERS and this handler hands `r`
// on untouched. Putting them in the body would mean re-serialising a structure whose exact bytes are
// what the signature covers — so the body belongs to the authenticator and nothing else.
func (d Deps) handlePasskeyRegisterFinish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ceremony := strings.TrimSpace(r.URL.Query().Get("ceremony"))
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if ceremony == "" {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "ceremony is required")
			return
		}
		if name == "" {
			// A NAME IS REQUIRED RATHER THAN DEFAULTED. Several credentials per admin, and the list
			// exists so the user can remove the phone they no longer own — "Passkey 2" would make
			// that list unreadable exactly when it matters.
			writeError(w, d.Log, http.StatusUnprocessableEntity, "name_required",
				"give this passkey a name you will recognise later")
			return
		}

		pk, err := auth.FinishPasskeyRegistration(d.Store, d.Passkeys, ceremony, name,
			auth.RPIDFromRequest(r), r, time.Now().UTC())
		if err != nil {
			if d.writePasskeyError(w, err) {
				return
			}
			// A VERIFICATION FAILURE IS A 400, NOT A 500. The authenticator's response did not
			// check out — malformed, wrong origin, wrong challenge — and every one of those is the
			// client's to fix. Logging it at Error with the reason keeps the detail out of the
			// response, where it would only help someone probing.
			d.Log.Error("passkey registration failed verification", "error", err)
			writeError(w, d.Log, http.StatusBadRequest, "passkey_rejected",
				"this passkey could not be verified — start again")
			return
		}
		writeJSON(w, d.Log, http.StatusCreated, passkeyToWire(pk))
	}
}

// passkeyToWire renders a stored credential for the API. See wire.Passkey for what is deliberately
// left out.
func passkeyToWire(p store.Passkey) wire.Passkey {
	out := wire.Passkey{
		ID:        p.CredentialID,
		Name:      p.Name,
		RPID:      p.RPID,
		CreatedAt: p.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !p.LastUsedAt.IsZero() {
		s := p.LastUsedAt.UTC().Format(time.RFC3339)
		out.LastUsedAt = &s
	}
	return out
}

// POST /api/auth/passkeys/login/begin → 200 {ceremony, options}
//
// PRE-AUTH, and therefore in all THREE exact-path lists: `authExempt` (no session yet),
// `setupAllowed` (a storageless install is exactly where onboarding offers a passkey), and
// `csrfExempt` (no CSRF cookie exists before login). The spec named two; there are three.
func (d Deps) handlePasskeyLoginBegin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		options, ceremony, err := d.Auth.BeginPasskeyAssertion(d.Passkeys,
			auth.RPIDFromRequest(r), d.Proxies.ClientIP(r))
		if err != nil {
			if errors.Is(err, auth.ErrRateLimited) {
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
				return
			}
			if d.writePasskeyError(w, err) {
				return
			}
			d.Log.Error("passkey login begin failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not start passkey sign-in")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.PasskeyRegisterBegin{Ceremony: ceremony, Options: options})
	}
}

// POST /api/auth/passkeys/login/finish?ceremony=<key> → 200 {state, csrf_token} + session cookie
//
// The response is the SAME SHAPE POST /api/auth/login returns, because a passkey login IS a login:
// the session layer is untouched by this rung and the client has one path to follow afterwards.
func (d Deps) handlePasskeyLoginFinish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		ceremony := strings.TrimSpace(r.URL.Query().Get("ceremony"))
		if ceremony == "" {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "ceremony is required")
			return
		}
		sess, csrf, err := d.Auth.FinishPasskeyAssertion(d.Passkeys, ceremony,
			auth.RPIDFromRequest(r), d.Proxies.ClientIP(r), r, sessionCookieValue(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case d.writePasskeyError(w, err):
				// already written
			case errors.Is(err, auth.ErrNoCredential):
				// DELIBERATELY THE SAME 401 A WRONG PASSWORD GETS. An unknown credential must not be
				// distinguishable from a rejected one, or the endpoint answers "does this quince
				// know this passkey" to anybody who asks.
				writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "this passkey was not accepted")
			default:
				d.Log.Error("passkey login failed verification", "error", err)
				writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "this passkey was not accepted")
			}
			return
		}
		secure := d.Auth.Secure(r)
		http.SetCookie(w, auth.SessionCookie(sess, secure))
		http.SetCookie(w, auth.CSRFCookie(csrf, secure))
		writeJSON(w, d.Log, http.StatusOK, wire.AuthStatus{State: auth.StateAuthenticated, CSRFToken: csrf})
	}
}
