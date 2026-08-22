package httpapi

import (
	"errors"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
)

type passwordBody struct {
	Password string `json:"password"`
}

// GET /api/auth/status → {state, csrf_token, scope} (rung-ruled contract addition). The CSRF
// token comes from the cookie the authGuard just ensured (via request context).
//
// `scope` SINCE qn.13 SLICE 8d — null for an admin, the device for a scoped holder. This is the
// read the shell boots from, so it is the one that decides whether a household member is shown the
// admin's chrome (D8, ruled on quince#1443).
func (d Deps) handleAuthStatus() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out, err := d.authStatusFor(sessionCookieValue(r), csrfFromContext(r))
		if err != nil {
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "auth status failed")
			return
		}
		writeJSON(w, d.Log, http.StatusOK, out)
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
	// THE THIRD REMEDY IS NEW, AND IT IS THE ONLY ONE THE STRANDED USER CAN TAKE (quince#940 §3,
	// Operator sweep 2026-08-14: *if something doesn't work the user should understand what exactly,
	// and how it can be fixed*).
	//
	// This offered two — *reach quince over https, or over localhost* — and for a first-run user on a
	// LAN address neither is available: they have no certificate yet, and localhost is not where their
	// phone is. That is quince#908's dead end, and the message was describing an exit nobody in it
	// could use.
	//
	// IT NAMES THE SETTING RATHER THAN THE BUTTON, deliberately. This string is served to `POST
	// /api/auth/login` as well as to setup, and on a CONFIGURED install the pre-auth route is closed
	// (409) — so pointing at a control that answers 409 would be a second wrong remedy. The setting is
	// true in both cases: it is the thing to change, whether the user reaches it through the HTTPS
	// page's confirm on first run or by editing `config.yml`.
	//
	// It arrives in the SAME PR as the route that makes it followable — quince#940's own point is that
	// a message a rung behind its affordance is the defect, not a step toward fixing it.
	writeError(w, d.Log, http.StatusUpgradeRequired, "insecure_origin",
		"this connection is not encrypted, so your browser would discard the session cookie and you "+
			"would land back here — reach quince over https, or over localhost, or allow plain http "+
			"on a network you trust by turning on sessions.allow_insecure_transport")
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
				writeError(w, d.Log, http.StatusConflict, "already_configured", alreadySetUp)
			case errors.Is(err, auth.ErrNoProof), errors.Is(err, auth.ErrProofNotForThis):
				// 401 — NOTHING USABLE WAS PRESENTED, which is the same class of refusal as a wrong
				// password and takes the same status. The server's own sentence carries the remedy:
				// re-authenticate, or present the right proof.
				//
				// THIS BRANCH CANNOT FIRE TODAY, and it is carried rather than deleted because
				// deleting it is a different claim from this PR's. `SetPassword` takes no `*Proofs`
				// and never calls `RequirePresent` — setup is exempt by `Configured()`, which is the
				// one exemption qn.6n allows — so neither error can reach here. It is wired for
				// `accepts` regardless, so that "every `reauth_required` carries the field" stays a
				// property you can check by grepping for the code rather than by reasoning about
				// which emitters are live.
				d.writeReauthRequired(w, err.Error(), auth.OpSetPassword, auth.RPIDFromRequest(r), "")
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
	// THE SCOPE IS RESOLVED BEFORE THE COOKIES (qn.13 slice 8d) — a failure must be
	// answerable with a refusal, not with a payload that says ADMIN because it could
	// not be read. See `mintedStatus`.
	status, err := d.mintedStatus(auth.PrincipalOf(sess), csrf)
	if err != nil {
		d.Log.Error("could not resolve the session scope", "error", err)
		writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not complete sign-in")
		return
	}
	secure := d.Auth.Secure(r)
	http.SetCookie(w, auth.SessionCookie(sess, secure))
	http.SetCookie(w, auth.CSRFCookie(csrf, secure))
	writeJSON(w, d.Log, http.StatusOK, status)
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
			d.writeReauthRequired(w, err.Error(), auth.OpSetPassword, auth.RPIDFromRequest(r), "")
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
// domain would let a caller talk itself past the rule.
//
// IT TAKES A BODY, WHICH IS UNUSUAL FOR A DELETE AND IS THE ONLY PLACE A CREDENTIAL MAY TRAVEL. The
// alternatives were a query parameter and a header; the query is closed by the secrets rule, since a
// `proof` is a credential-equivalent for one operation and would land in every access log between
// here and the browser, and a header is the same objection one step weaker — proxies log those far
// more readily than bodies. A body on DELETE is legal (RFC 9110 leaves it undefined, not forbidden),
// `fetch` sends one, and a proxy that strips it produces a stated refusal rather than a silent
// success, which is the failure direction the no-silent-fallbacks rule asks for.
func (d Deps) handleRemovePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body removePasswordBody
		if err := decodeOptionalJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		err := d.PasswordAdmin.RemovePassword(d.Proofs,
			auth.Presented{Password: body.CurrentPassword, Proof: body.Proof},
			auth.RPIDFromRequest(r), sessionCookieValue(r), d.Proxies.ClientIP(r))
		var lastCred auth.ErrLastCredential
		var self auth.ErrSelfRemoval
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, ErrPasswordAdminUnavailable):
			writeError(w, d.Log, http.StatusServiceUnavailable, "unavailable", err.Error())
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		case errors.As(err, &self):
			// 409 wrong_credential — RULE 2. A DIFFERENT CODE FROM last_credential BECAUSE THE
			// REMEDIES DIFFER: this one is retryable with something the user already has, and that
			// one is not retryable at all until they make a new credential.
			writeError(w, d.Log, http.StatusConflict, "wrong_credential", self.Error())
		case errors.As(err, &lastCred):
			// 409 AND THE ERROR'S OWN SENTENCE. It names the address this request arrived on and the
			// addresses the credentials it found belong to, which is the difference between a
			// mystery and an instruction — the same reasoning as passkey_rp_mismatch.
			writeError(w, d.Log, http.StatusConflict, "last_credential", lastCred.Error())
		case errors.Is(err, auth.ErrBadPassword):
			writeError(w, d.Log, http.StatusUnauthorized, "bad_password", "current password is incorrect")
		case errors.Is(err, auth.ErrNoProof), errors.Is(err, auth.ErrProofNotForThis):
			// `accepts` HERE NEVER LISTS `password` — rule 2's first exclusion, applied in
			// `Service.Accepts`. The password cannot authorise its own removal, so offering it would
			// send a user to type a value the subject comparison is guaranteed to reject.
			d.writeReauthRequired(w, err.Error(), auth.OpRemovePassword, auth.RPIDFromRequest(r), "")
		default:
			d.Log.Error("remove password failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not remove password")
		}
	}
}

// removePasswordBody is the optional body of DELETE /api/auth/password.
//
// BOTH FIELDS ARE OPTIONAL AT THE WIRE AND NEITHER IS OPTIONAL IN EFFECT: rule 2 refuses the
// password, so `proof` is the field that works and `current_password` exists to be REFUSED with a
// sentence naming the remedy. Accepting it and answering properly is better than rejecting it as an
// unknown field, which is what `DisallowUnknownFields` would otherwise do — a 400 about JSON where
// the user needs to be told to use their passkey.
type removePasswordBody struct {
	CurrentPassword string `json:"current_password"`
	Proof           string `json:"proof"`
}

// alreadySetUp is what a FIRST-RUN route says when the install has been claimed underneath it.
//
// THREE SITES SHARE THIS AND SIX SHARE THE CODE, which is why it is a constant rather than a string
// typed three times. `already_configured` is also answered by the certificate and transport routes,
// where it is mundane — those keep their own words. What is shared here is not the code, it is the
// MEANING: a setup screen only renders while the install is unclaimed, so a 409 on one of these
// three means something claimed it between the page loading and the button being pressed.
//
// IT NAMES THE STALE-PAGE CASE FIRST, because that is the likely one and it has a remedy the reader
// can act on immediately — Operator, 2026-08-15.
//
// AND IT NAMES THE OTHER ONE, which is the reason a redirect to sign-in was proposed and WITHDRAWN:
//
//	> that case might mean something really bad has just happened
//
// On a network-reachable first run this is not necessarily a second tab; it can be somebody else
// taking the install, which quince#888 already names as a live shape in this product. Sending the
// user to a login form would answer the event that most deserves a stop with the most reassuring
// thing the app can say — and land them at a sign-in for an account they never created.
const alreadySetUp = "this quince has already been set up. If you set it up yourself, this page is " +
	"stale — refresh it. If you did not, someone else has claimed this install."
