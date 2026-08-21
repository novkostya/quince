package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// THE ENROLMENT ROUTES — qn.13 slice 9b-3, spec D4.
//
// PRE-AUTH, and the exemption is only half of the decision. Every other entry in `authExempt` pairs
// its exemption with a bound in the handler — `Configured()` for the first-run pair, one-shot for
// the certificate trial. This pair's bound is the ENROLMENT SECRET: single-use, minutes-long,
// revocable, and carrying its own scope (slice 9a). Being exempt is what makes it reachable; being
// authorized by a secret quince minted is what makes it safe.
//
// THE SECRET TRAVELS IN A QUERY PARAMETER, matching `setup/passkey/finish`'s `?ceremony=`, and the
// honest reason is CONSISTENCY rather than necessity.
//
// A HEADER OR A BODY WOULD WORK HERE AND WOULD BE TIGHTER. The QR is a URL by construction, so the
// secret arrives in the page address whatever these routes do — but by the time the page calls
// them it already holds the value and is issuing a POST, so it could put it anywhere. Sending it
// in the query keeps it out of nothing: access logs, proxy logs and referer headers all see it
// again.
//
// WHAT BOUNDS THE LEAK IS SINGLE-USE, NOT PLACEMENT. D4 assumes the URL leaks and answers that
// with one-shot plus minutes, which holds wherever the value rides. So the API mirrors the QR's
// shape and the mitigation is the secret's lifecycle — and if a later change wants it in a header,
// nothing in D4 is standing in the way.

// enrolmentSecret pulls the secret out of the request, or writes the refusal.
//
// ONE PLACE, so begin and finish cannot disagree about what a missing secret is.
func enrolmentSecret(d Deps, w http.ResponseWriter, r *http.Request) (string, bool) {
	token := strings.TrimSpace(r.URL.Query().Get("secret"))
	if token == "" {
		writeError(w, d.Log, http.StatusBadRequest, "bad_request",
			"this enrolment link is missing its code — scan the QR code again, or ask for a new one")
		return "", false
	}
	return token, true
}

// writeEnrolmentError maps the secret's four refusals to statuses, keeping them distinct.
//
// FOUR CAUSES, FOUR SENTENCES, and that is the whole reason `Enrolments` declines to collapse them.
// The person reading these is an UNAUTHENTICATED household member holding a link somebody handed
// them, and what they should do next differs in every case — including one that is not a retry at
// all: *already used* says somebody else enrolled with this link, which is worth knowing rather
// than being told to ask for another.
//
// 410 GONE FOR THE THREE THAT WERE REAL, 404 FOR THE ONE THAT NEVER WAS. `Gone` is the honest code
// for a resource that existed and does not now, and it distinguishes *this link is finished* from
// *this link was never ours* — which is the difference between "ask the admin" and "check the
// address you typed".
func (d Deps) writeEnrolmentError(w http.ResponseWriter, err error) bool {
	switch {
	case errors.Is(err, auth.ErrEnrolmentUnknown):
		writeError(w, d.Log, http.StatusNotFound, "enrolment_unknown",
			"this enrolment link is not one this quince issued — check the address, or ask for a new QR code")
	case errors.Is(err, auth.ErrEnrolmentExpired):
		writeError(w, d.Log, http.StatusGone, "enrolment_expired",
			"this enrolment link has expired — ask for a new QR code")
	case errors.Is(err, auth.ErrEnrolmentSpent):
		writeError(w, d.Log, http.StatusGone, "enrolment_spent",
			"this enrolment link has already been used to add a passkey — if that was not you, tell whoever runs this quince")
	case errors.Is(err, auth.ErrEnrolmentRevoked):
		writeError(w, d.Log, http.StatusGone, "enrolment_revoked",
			"this enrolment link was cancelled — ask for a new QR code")
	default:
		return false
	}
	return true
}

// POST /api/enrol/passkey/begin?secret=<secret> → 200 {ceremony, options}
func (d Deps) handleEnrolPasskeyBegin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// THE SAME INSECURE-ORIGIN REFUSAL the first-run pair carries, and for the same stronger
		// reason: WebAuthn does not run outside a secure context at all, so a browser here would
		// fail after the round trip rather than before it.
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		token, ok := enrolmentSecret(d, w, r)
		if !ok {
			return
		}
		options, ceremony, err := d.Auth.BeginEnrolment(d.Passkeys, d.Enrolments,
			auth.RPIDFromRequest(r), token, d.Proxies.ClientIP(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case d.writeEnrolmentError(w, err):
				// already written — the four secret refusals
			case d.writePasskeyError(w, err):
				// already written — covers passkeys_unsupported_here
			default:
				d.Log.Error("enrolment begin failed", "error", err)
				writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not start enrolment")
			}
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.PasskeyRegisterBegin{Ceremony: ceremony, Options: options})
	}
}

// POST /api/enrol/passkey/finish?secret=<secret>&ceremony=<key>&name=<label> → 200 {state, csrf_token} + cookies
//
// ISSUES A SCOPED SESSION, for `setup/passkey/finish`'s reason — the caller has just proved
// possession of a credential this install now holds. The response shape is `AuthStatus` and not the
// 201 {passkey} that authenticated registration returns, because this call's outcome is *you are
// signed in*, not *here is a row you can manage*: a scoped holder reaches no passkey list.
func (d Deps) handleEnrolPasskeyFinish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		token, ok := enrolmentSecret(d, w, r)
		if !ok {
			return
		}
		ceremony := strings.TrimSpace(r.URL.Query().Get("ceremony"))
		if ceremony == "" {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "ceremony is required")
			return
		}
		// NO name_required HERE, unlike the first-run pair. The stored label is DERIVED from the
		// enrolment's scope (quince#1431 review) — a household member naming a credential the ADMIN
		// has to identify is the wrong source of truth, so any `?name=` is ignored rather than
		// validated.
		_, sess, csrf, err := d.Auth.FinishEnrolment(d.Passkeys, d.Enrolments, ceremony,
			auth.RPIDFromRequest(r), token, r, time.Now().UTC(), sessionCookieValue(r), d.Proxies.ClientIP(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				// REACHABLE SINCE quince#1426 METERED THIS DOOR, and falling through to the
				// default told a throttled household member their passkey was invalid and to
				// START AGAIN — false, and the worst available advice, since starting again is
				// more requests at the limiter. A client that would back off on a 429 got a 400,
				// and the daemon logged an Error for an ordinary throttle.
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case d.writeEnrolmentError(w, err):
				// THE SECRET IS RE-READ AT FINISH, so these are reachable here and not only at
				// begin: the admin can revoke a QR while the phone is on the unlock sheet.
			case d.writePasskeyError(w, err):
				// already written
			default:
				d.Log.Error("enrolment finish failed verification", "error", err)
				writeError(w, d.Log, http.StatusBadRequest, "passkey_rejected",
					"this passkey could not be verified — start again")
			}
			return
		}
		secure := d.Auth.Secure(r)
		http.SetCookie(w, auth.SessionCookie(sess, secure))
		http.SetCookie(w, auth.CSRFCookie(csrf, secure))
		writeJSON(w, d.Log, http.StatusOK, wire.AuthStatus{State: auth.StateAuthenticated, CSRFToken: csrf})
	}
}
