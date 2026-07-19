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

// POST /api/auth/setup {password} → sets the first-run password and logs in. 409 if a
// password already exists (Operator ruling: setup succeeds exactly once).
func (d Deps) handleAuthSetup() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body passwordBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		if err := d.Auth.SetPassword(body.Password); err != nil {
			switch {
			case errors.Is(err, auth.ErrAlreadyConfigured):
				writeError(w, d.Log, http.StatusConflict, "already_configured", "admin password is already set")
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
	sess, csrf, err := d.Auth.Login(password, clientIP(r))
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
func (d Deps) handleAuthLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := d.Auth.Logout(sessionCookieValue(r)); err != nil {
			d.Log.Error("logout failed", "error", err)
		}
		http.SetCookie(w, auth.ClearSessionCookie(d.Auth.Secure(r)))
		w.WriteHeader(http.StatusNoContent)
	}
}
