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

// GET /api/auth/passkeys → 200 {passkeys: [...]}
//
// SESSION REQUIRED, like registration. The list is what the Settings surface reads to let an admin
// remove the phone they no longer own, so it carries the name, when it was created, when it was
// last used, and the domain it is bound to — and NOT the public key or anything else a compromised
// session could enumerate for no benefit (see wire.Passkey).
func (d Deps) handlePasskeyList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := d.Store.ListPasskeys()
		if err != nil {
			d.Log.Error("passkey list failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not list passkeys")
			return
		}
		out := make([]wire.Passkey, 0, len(rows))
		for _, p := range rows {
			out = append(out, passkeyToWire(p))
		}
		// THE CURRENT rpId TRAVELS WITH THE LIST so the UI can mark the rows that will not work
		// here without deriving the domain itself. A browser can read `location.hostname`, but it
		// cannot know what quince considered the relying party — behind a proxy those are the same
		// only if the proxy preserves Host, which is exactly the thing that can be misconfigured
		// (deploy/tls.md). Sending it makes the UI's warning agree with the server's behaviour
		// rather than with a guess.
		writeJSON(w, d.Log, http.StatusOK, wire.PasskeyList{
			Passkeys:  out,
			RPID:      auth.RPIDFromRequest(r),
			Supported: auth.RPIDSupported(auth.RPIDFromRequest(r)),
		})
	}
}

// DELETE /api/auth/passkeys/{id} → 204
//
// 204 WHETHER OR NOT A ROW WENT. Removing a credential that is already gone is the state the caller
// wanted, and a 404 there would make a second tab, or a retry after a dropped response, look like a
// failure the user must act on.
func (d Deps) handlePasskeyDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, err := d.Store.DeletePasskey(r.PathValue("id")); err != nil {
			d.Log.Error("passkey delete failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not remove the passkey")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// PATCH /api/auth/passkeys/{id} {name} → 200 {passkey}
func (d Deps) handlePasskeyRename() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		name := strings.TrimSpace(body.Name)
		if name == "" {
			writeError(w, d.Log, http.StatusUnprocessableEntity, "name_required",
				"give this passkey a name you will recognise later")
			return
		}
		id := r.PathValue("id")
		renamed, err := d.Store.RenamePasskey(id, name)
		if err != nil {
			d.Log.Error("passkey rename failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not rename the passkey")
			return
		}
		if !renamed {
			// UNLIKE DELETE, a 404 is right here: the caller asked for a specific end state — this
			// credential, that name — and it did not happen. Reporting 200 would tell the UI to
			// render a row that does not exist.
			writeError(w, d.Log, http.StatusNotFound, "not_found", "no such passkey")
			return
		}
		pk, _, err := d.Store.GetPasskey(id)
		if err != nil {
			d.Log.Error("passkey reread failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not read the passkey back")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, passkeyToWire(pk))
	}
}
