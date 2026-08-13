package httpapi

import (
	"errors"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

type passwordBody struct {
	Password string `json:"password"`
}

// GET /api/auth/status → {state, csrf_token} (rung-ruled contract addition). The CSRF
// token comes from the cookie the authGuard just ensured (via request context).
func (d Deps) handleAuthStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, err := d.Auth.Status(sessionCookieValue(r))
		if err != nil {
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "auth status failed")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.AuthStatus{State: state, CSRFToken: csrfFromContext(r)})
	}
}

// refuseInsecureOrigin answers 426 when the session cookie this request is about to earn
// would be marked Secure and then discarded by the browser, and reports whether it did.
// That is the quince#497 login loop: the server returns 200 with two cookies, the browser
// keeps neither, the next request is unauthenticated, and nothing anywhere says why.
//
// It runs BEFORE the credential is examined — before Login, and before SetPassword — for
// three reasons. It leaves no session row and spends no rate-limit budget on an attempt
// that cannot work. It cannot become a password oracle over the one channel that is not
// encrypted, because the answer is identical for a right and a wrong password. And on the
// setup path it is the difference between refusing and refusing-having-already-set-the-
// password: SetPassword is not idempotent, so a refusal after it leaves a first-run user
// looking at an error with their chosen password silently in force and 409 on the retry.
//
// 426 with an Upgrade header is RFC 9110 §15.5.22, where the header is a MUST and means
// "the required protocol(s)" — so this states what quince requires rather than offering an
// in-band upgrade it would not perform (RFC 2817 §6 is the wire form). No browser acts on
// it; it is here because the status cannot be sent correctly without it. The status is the
// point: a 4xx about the client's transport, not about its credential.
func (d Deps) refuseInsecureOrigin(w http.ResponseWriter, r *http.Request) bool {
	if !d.Auth.CookieWillBeDiscarded(r) {
		return false
	}
	w.Header().Set("Upgrade", "TLS/1.3, HTTP/1.1")
	writeError(w, d.Log, http.StatusUpgradeRequired, "insecure_origin",
		"this connection is not encrypted, so your browser would discard the session cookie and you would land back here — reach quince over https, or over localhost")
	return true
}

// POST /api/auth/setup {password} → sets the first-run password and logs in. 409 if a
// password already exists (Operator ruling: setup succeeds exactly once).
func (d Deps) handleAuthSetup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		var body passwordBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if err := d.Auth.SetPassword(body.Password, d.Proxies.ClientIP(r)); err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case errors.Is(err, auth.ErrAlreadyConfigured):
				writeError(w, d.Log, http.StatusConflict, "already_configured", "admin password is already set")
			case errors.Is(err, auth.ErrNoProof), errors.Is(err, auth.ErrProofNotForThis):
				// 401 — NOTHING USABLE WAS PRESENTED, which is the same class of refusal as a wrong
				// password and takes the same status. The server's own sentence carries the remedy:
				// re-authenticate, or present the right proof.
				writeError(w, d.Log, http.StatusUnauthorized, "reauth_required", err.Error())
			case errors.Is(err, auth.ErrWeakPassword):
				writeError(w, d.Log, http.StatusUnprocessableEntity, "weak_password", "password does not meet requirements")
			default:
				d.Log.Error("set password failed", "error", err)
				writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not set password")
			}
			return
		}
		d.issueSessionResponse(w, r, body.Password)
	}
}

// POST /api/auth/login {password} → sets the session + CSRF cookies.
func (d Deps) handleAuthLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		var body passwordBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		d.issueSessionResponse(w, r, body.Password)
	}
}

// issueSessionResponse logs in with password and, on success, sets cookies and returns the
// authenticated status. Shared by setup (post-set) and login.
func (d Deps) issueSessionResponse(w http.ResponseWriter, r *http.Request, password string) {
	// The caller's own session cookie, so login supersedes THIS client's prior session and no
	// other device's (quince#373). Empty on a first login, which is the ordinary case.
	d.Proxies.WarnUnconfiguredProxy(d.Log, r)
	sess, csrf, err := d.Auth.Login(password, d.Proxies.ClientIP(r), sessionCookieValue(r))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		case errors.Is(err, auth.ErrBadPassword), errors.Is(err, auth.ErrNoPassword):
			writeError(w, d.Log, http.StatusUnauthorized, "bad_password", "incorrect password")
		default:
			d.Log.Error("login failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "login failed")
		}
		return
	}
	secure := d.Auth.Secure(r)
	http.SetCookie(w, auth.SessionCookie(sess, secure))
	http.SetCookie(w, auth.CSRFCookie(csrf, secure))
	writeJSON(w, d.Log, http.StatusOK, wire.AuthStatus{State: auth.StateAuthenticated, CSRFToken: csrf})
}

// POST /api/auth/logout → clears the session.
//
// Deliberately NOT guarded by refuseInsecureOrigin, and the reason is that the case cannot
// arise. The clearing cookie carries the same Secure flag, so on an insecure origin the
// browser would discard it too — but a Secure session cookie is never SENT over plain http
// in the first place, so a logout arriving on such an origin has no session to clear and
// the discarded clear-cookie erases a cookie that was not there. Refusing would turn a
// harmless no-op into an error on the one action that should always appear to succeed. The
// remaining callers of Auth.Secure are all in this file (quince#497 asked for this glance).
func (d Deps) handleAuthLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.Auth.Logout(sessionCookieValue(r)); err != nil {
			d.Log.Error("logout failed", "error", err)
		}
		http.SetCookie(w, auth.ClearSessionCookie(d.Auth.Secure(r)))
		w.WriteHeader(http.StatusNoContent)
	}
}

// changePasswordBody is PUT /api/auth/password (qn.6m D4).
//
// `current_password` IS OMITTED, NOT EMPTY, on a passwordless install — but the two arrive
// identically as JSON and the server decides which case applies from its own state, so there is no
// client-supplied flag to get wrong here. Both fields travel in the BODY, never the query: they are
// credentials, and the secrets rule keeps them out of argv, env and any URL that could be logged.
type changePasswordBody struct {
	CurrentPassword string `json:"current_password"`
	// Proof is a token from POST /api/auth/reauth/finish — qn.6n rules 1 and 3. EITHER field
	// satisfies the rule; a passkey is the alternative for an install that has no password to type,
	// which is the case that used to require nothing at all.
	Proof       string `json:"proof"`
	NewPassword string `json:"new_password"`
}

// PUT /api/auth/password — change the admin password. SESSION REQUIRED (authGuard).
func (d Deps) handleChangePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body changePasswordBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		err := d.PasswordAdmin.ChangePassword(d.Proofs,
			auth.Presented{Password: body.CurrentPassword, Proof: body.Proof},
			body.NewPassword, sessionCookieValue(r), d.Proxies.ClientIP(r))
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrPasswordAdminUnavailable):
			// 503 AND THE REASON, not a hidden control — the demo carve-out (qn.6m D6).
			writeError(w, d.Log, http.StatusServiceUnavailable, "unavailable", err.Error())
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		case errors.Is(err, auth.ErrBadPassword):
			// 401 — the CURRENT password was wrong. Deliberately the same code the login form uses
			// for the same mistake, so a client need not learn a second spelling of it.
			writeError(w, d.Log, http.StatusUnauthorized, "bad_password", "current password is incorrect")
		case errors.Is(err, auth.ErrNoProof), errors.Is(err, auth.ErrProofNotForThis):
			// 401 — NOTHING USABLE WAS PRESENTED, which is the same class of refusal as a wrong
			// password and takes the same status. The server's own sentence carries the remedy:
			// re-authenticate, or present the right proof.
			writeError(w, d.Log, http.StatusUnauthorized, "reauth_required", err.Error())
		case errors.Is(err, auth.ErrWeakPassword):
			writeError(w, d.Log, http.StatusUnprocessableEntity, "weak_password", "password does not meet requirements")
		default:
			d.Log.Error("change password failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not change password")
		}
	}
}

// DELETE /api/auth/password — go passwordless. SESSION REQUIRED (authGuard).
//
// The rpId comes from the REQUEST rather than from the client, exactly as every other passkey
// surface derives it: what matters is the address this call actually arrived on, and a client-named
// domain would let a caller talk itself past the lockout guard.
func (d Deps) handleRemovePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := d.PasswordAdmin.RemovePassword(auth.RPIDFromRequest(r), d.Proxies.ClientIP(r))
		var lastCred auth.ErrLastCredential
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrPasswordAdminUnavailable):
			writeError(w, d.Log, http.StatusServiceUnavailable, "unavailable", err.Error())
		case errors.As(err, &lastCred):
			// 409 AND THE ERROR'S OWN SENTENCE. It names the address this request arrived on and the
			// addresses the credentials it found belong to, which is the difference between a
			// mystery and an instruction — the same reasoning as passkey_rp_mismatch.
			writeError(w, d.Log, http.StatusConflict, "last_credential", lastCred.Error())
		default:
			d.Log.Error("remove password failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not remove password")
		}
	}
}
