package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// Re-authentication — qn.6n slice 3, contracts §1.
//
// BOTH ENDPOINTS REQUIRE A SESSION, which is why this file touches neither exact-path allowlist.
// That is D3's entire argument: `passkeys/login/*` is pre-auth in all three lists, and a pair whose
// purpose is gating privileged operations must not share routes with the least-guarded ones.
//
// NOTHING CONSUMES THE PROOF YET. Slices 4 and 5 make the mutating endpoints demand it; this slice
// lands the way to obtain one, so the two can be reviewed apart.

// POST /api/auth/reauth/begin {operation, target?} → 200 {ceremony, options}
func (d Deps) handleReauthBegin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Operation string `json:"operation"`
			Target    string `json:"target"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}

		options, ceremony, err := d.Auth.BeginReauth(d.Reauth, auth.RPIDFromRequest(r),
			d.Proxies.ClientIP(r), auth.ProofOperation(strings.TrimSpace(body.Operation)),
			strings.TrimSpace(body.Target), sessionCookieValue(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case d.writePasskeyError(w, err):
				// already written — the unsupported tier and the rpId mismatch carry their own message
			default:
				// A BAD OPERATION OR TARGET IS THE CALLER'S MISTAKE, NOT A 500. The set is closed and
				// documented, so a name outside it is a client bug; the message is the service's own,
				// which names what was wrong rather than saying "invalid request".
				writeError(w, d.Log, http.StatusUnprocessableEntity, "bad_operation", err.Error())
			}
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.PasskeyRegisterBegin{Ceremony: ceremony, Options: options})
	}
}

// POST /api/auth/reauth/finish?ceremony=<key> → 200 {proof}
//
// The authenticator's response is read from the REQUEST BODY by the library rather than decoded
// here, which is why the ceremony key arrives as a QUERY PARAMETER and this handler hands `r` on
// untouched — registration's reasoning, and for the same reason: the exact bytes are what the
// signature covers, so the body belongs to the authenticator and nothing else.
//
// NO COOKIE IS SET ON THIS RESPONSE, and that is a property worth stating where somebody might add
// one by analogy. `passkeys/login/finish` sets two; this endpoint mints no session at all.
func (d Deps) handleReauthFinish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ceremony := strings.TrimSpace(r.URL.Query().Get("ceremony"))
		if ceremony == "" {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "ceremony is required")
			return
		}
		proof, err := d.Auth.FinishReauth(d.Reauth, d.Proofs, ceremony, auth.RPIDFromRequest(r),
			d.Proxies.ClientIP(r), r, sessionCookieValue(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case d.writePasskeyError(w, err):
				// already written
			default:
				// THE SAME 401 A REJECTED ASSERTION GETS AT LOGIN, and for the same reason: an
				// unknown credential must not be distinguishable from a refused one, or the endpoint
				// answers "does this quince know this passkey" to anybody holding a session.
				d.Log.Error("re-authentication failed", "error", err)
				writeError(w, d.Log, http.StatusUnauthorized, "unauthorized", "this passkey was not accepted")
			}
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.ReauthFinish{Proof: proof})
	}
}
