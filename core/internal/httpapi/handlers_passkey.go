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
