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

// POST /api/auth/passkeys/register/begin {current_password?, proof?} → {ceremony, options}
//
// RULE 1 IS ENFORCED HERE, AT **BEGIN**, AND THAT IS THE OPPOSITE END FROM THE PASSWORD PATH — qn.6n
// slice 5b. `PUT /api/auth/password` checks the proof where the write happens, and the client can
// discover the requirement from a 401 and retry, because nothing was consumed by the first attempt.
//
// A WEBAUTHN CREATION CEREMONY CANNOT BE REPLAYED. Its challenge is single-use, so a client that
// learned at `finish` that a proof was needed would have to run `navigator.credentials.create()`
// again — a second Face ID sheet for a credential the user already made, which on some platforms
// also mints a second resident key. Refusing before the ceremony exists costs the caller one round
// trip and costs the user nothing.
//
// SO `finish` NEEDS NO CHECK OF ITS OWN, AND THAT IS A PROPERTY RATHER THAN AN OMISSION: a
// REGISTRATION ceremony key is only ever produced by a guarded begin, so a key in hand IS the
// evidence that a proof was presented.
//
// THERE ARE THREE PRODUCERS INTO THAT STORE, NOT TWO, AND THE THIRD IS PRE-AUTH — quince#930 review,
// and this comment claimed two until it. `setup/passkey/begin` is first-run-only and answers 409
// once `Configured()` is true, so it cannot mint one where this rule applies. **`passkeys/login/begin`
// is in all three exact-path allowlists** and can be called by anyone who reaches the address.
//
// WHAT MAKES THE SENTENCE TRUE IS THE CEREMONY KIND, not the count. `PasskeyCeremonies.take` compares
// what the ceremony was begun for against what the finisher expects, so a login key presented here
// is refused by this package. Before that tag the property held only because `go-webauthn` v0.17.4
// happens to refuse the cross-use on session shape — an upstream invariant nothing here tested, and
// a bump could have changed it silently.
func (d Deps) handlePasskeyRegisterBegin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A BODY IS OPTIONAL AND AN ABSENT ONE IS NOT AN ERROR. `RequirePresent` refuses an empty
		// `Presented` on a claimed install anyway, so decoding failures here would only turn one
		// refusal into a less useful one.
		var body struct {
			CurrentPassword string `json:"current_password"`
			Proof           string `json:"proof"`
		}
		_ = decodeJSON(r, &body)

		if _, err := d.Auth.RequirePresent(d.Proofs,
			auth.Presented{Password: body.CurrentPassword, Proof: body.Proof},
			auth.OpAddPasskey, "", sessionCookieValue(r), d.Proxies.ClientIP(r)); err != nil {
			d.writePresentError(w, err, "could not start passkey setup",
				auth.OpAddPasskey, auth.RPIDFromRequest(r), "")
			return
		}

		rpID := auth.RPIDFromRequest(r)
		options, ceremony, err := auth.BeginPasskeyRegistration(d.Store, d.Passkeys, rpID, store.AdminScope(), auth.ExcludeRegistered())
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

// writePresentError maps `RequirePresent`'s refusals — qn.6n. Shared so the password path and the
// registration path cannot answer the same refusal with different codes, which is what a client
// retrying on `reauth_required` would trip over.
//
// IT NOW TAKES THE OPERATION, so the refusal it writes can carry `accepts` (qn.6o D1). The three
// arguments are exactly what `Service.Accepts` needs, and requiring them here is what stops a sixth
// caller emitting this code with the field silently absent.
func (d Deps) writePresentError(w http.ResponseWriter, err error, fallback string,
	op auth.ProofOperation, rpID, target string) {
	switch {
	case errors.Is(err, auth.ErrRateLimited):
		writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
	case errors.Is(err, auth.ErrBadPassword):
		writeError(w, d.Log, http.StatusUnauthorized, "bad_password", "current password is incorrect")
	case errors.Is(err, auth.ErrNoProof), errors.Is(err, auth.ErrProofNotForThis):
		// THE CODE THE CLIENT RETRIES ON. It must match `PUT /api/auth/password`'s exactly — a second
		// spelling here would leave the retry working on one surface and silently not on the other.
		d.writeReauthRequired(w, err.Error(), op, rpID, target)
	default:
		d.Log.Error("re-authentication check failed", "error", err)
		writeError(w, d.Log, http.StatusInternalServerError, "internal", fallback)
	}
}

// writeReauthRequired computes `accepts` and writes the refusal — qn.6o slice 2, D1.
//
// A COMPUTATION FAILURE COSTS THE FIELD, NOT THE REFUSAL. `accepts` is advisory copy on a 401 that
// has already been decided, so turning a store error here into a 500 would replace a correct,
// actionable refusal with an opaque one — and the client's fallback for a missing field is exactly
// today's behaviour. Logged rather than swallowed, because a store that cannot answer this is a
// problem even when it is not this request's problem.
// `reason` IS THE LOG LINE, NOT THE MESSAGE, and the name is the whole of the fix — architect,
// reviewing quince#1003. It was called `message` after the copy moved inside, so a reader at a call
// site saw `writeReauthRequired(w, err.Error(), …)` and concluded the string was shown. The comment
// saying otherwise lived in here, five lines away from the place somebody will one day pass
// carefully-worded copy and never learn it was dropped — and nothing would fail.
func (d Deps) writeReauthRequired(w http.ResponseWriter, reason string,
	op auth.ProofOperation, rpID, target string) {
	accepts, err := d.Auth.Accepts(op, rpID, target)
	if err != nil {
		d.Log.Error("could not compute the acceptable factors", "error", err, "operation", op)
		accepts = nil
	}
	// THE CALLER PASSES A Go ERROR STRING AND IT IS NOT FIT TO SHOW ANYONE — Operator,
	// 2026-08-14, from a screenshot of the running stand:
	//
	//	auth: no proof for this operation — authenticate again
	//
	// Every word of that is ours rather than the reader's. `auth:` is a package prefix; a *proof* is
	// an internal mechanism nobody outside this codebase has heard of; *"authenticate again"* names
	// no button on the screen. It reached a user because the call sites pass `err.Error()` as the
	// wire message, and the UI rendered that verbatim.
	//
	// EVERY OTHER REFUSAL HERE ALREADY WRITES COPY — `bad_password` says *"current password is
	// incorrect"* rather than `ErrBadPassword.Error()`. This one was the exception, so it is the one
	// that leaked.
	//
	// `reason` IS IGNORED FOR THE WIRE RATHER THAN CLEANED UP AT EACH SITE, deliberately: a parameter every
	// caller must remember to make human is a parameter that drifts back to a Go string. There is
	// nothing per-site to say, because the answer is the same wherever it is asked.
	//
	// THE ERROR STILL REACHES THE LOG, where `auth: no proof for this operation` is exactly right.
	d.Log.Info("re-authentication required", "operation", op, "reason", reason)
	writeReauthRequired(w, d.Log, "Confirm it is you before changing how you sign in.", accepts)
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
			// The admin adding a passkey from Settings. Scoped credentials come from enrolment,
			// which is a different endpoint.
			auth.RPIDFromRequest(r), r, time.Now().UTC(), store.AdminScope())
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
	// NIL STAYS NIL, WHICH IS ADMIN (qn.13 D9). Copied through a fresh value rather than aliasing
	// the store's pointer: two rows holding one pointer is a shape where changing a scope through
	// one of them changes the other, and nothing in the type stops a future caller trying.
	if p.ScopeUDID != nil {
		out.Scope = &wire.PasskeyScope{UDID: *p.ScopeUDID}
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
		// A BODY IS OPTIONAL AND AN ABSENT ONE IS NOT AN ERROR — the same idiom
		// `handlePasskeyRegisterBegin` uses, and for a stronger reason here: the field is a HINT,
		// so the failure to read one costs the discoverable flow rather than the sign-in. A
		// malformed body on a PRE-AUTH page must not produce an error the visitor cannot act on.
		//
		// `DisallowUnknownFields` means qn.6k's browsers, which post `{}`, still decode cleanly.
		var body wire.PasskeyLoginBegin
		_ = decodeJSON(r, &body)

		options, ceremony, err := d.Auth.BeginPasskeyAssertion(d.Passkeys,
			auth.RPIDFromRequest(r), d.Proxies.ClientIP(r), strings.TrimSpace(body.CredentialID))
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
		// READ ALONGSIDE THE LIST, not derived from it: an install can hold credentials AND a
		// password, or either alone, and the surface renders differently in all four combinations.
		hasPassword, err := d.Auth.HasPassword()
		if err != nil {
			d.Log.Error("passkey list: password check failed", "error", err)
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
			Passkeys:    out,
			RPID:        auth.RPIDFromRequest(r),
			Supported:   auth.RPIDSupported(auth.RPIDFromRequest(r)),
			HasPassword: hasPassword,
		})
	}
}

// DELETE /api/auth/passkeys/{id} → 204, or 409 last_credential
//
// 204 WHETHER OR NOT A ROW WENT. Removing a credential that is already gone is the state the caller
// wanted, and a 404 there would make a second tab, or a retry after a dropped response, look like a
// failure the user must act on.
//
// THAT INDIFFERENCE SURVIVES THE LOCKOUT GUARD, and the two rules meeting on one handler is the
// question quince#888 raised about this endpoint. They do not collide: the 204 is about whether a
// ROW existed, and the refusal is about what the install would be LEFT WITH. An id matching no row
// leaves the state unchanged, so it cannot be the last credential, so it still gets its 204.
//
// IT GOES THROUGH Auth RATHER THAN STRAIGHT TO THE STORE, and that was the whole defect: this
// handler called d.Store.DeletePasskey directly, so there was no layer for the check to live in.
func (d Deps) handlePasskeyDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A BODY, OPTIONAL AND ABSENT-TOLERANT — the same shape and the same reasoning as
		// DELETE /api/auth/password (spec D9): a credential may not travel in the query, and
		// presenting nothing is a credential refusal rather than a malformed request.
		var body struct {
			CurrentPassword string `json:"current_password"`
			Proof           string `json:"proof"`
		}
		if err := decodeOptionalJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body")
			return
		}
		_, err := d.Auth.RemovePasskey(d.Proofs,
			auth.Presented{Password: body.CurrentPassword, Proof: body.Proof},
			r.PathValue("id"), auth.RPIDFromRequest(r), sessionCookieValue(r), d.Proxies.ClientIP(r))
		var lastKey auth.ErrLastPasskey
		var self auth.ErrSelfRemoval
		switch {
		case err == nil:
			w.WriteHeader(http.StatusNoContent)
		case errors.Is(err, auth.ErrRateLimited):
			writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
		case errors.As(err, &self):
			// 409 wrong_credential — RULE 2, the same code and the same distinction the password
			// path draws: retryable with something the caller has, versus nothing to retry with.
			writeError(w, d.Log, http.StatusConflict, "wrong_credential", self.Error())
		case errors.As(err, &lastKey):
			// 409 AND THE ERROR'S OWN SENTENCE, exactly as DELETE /api/auth/password does — it names
			// this address and the addresses the remaining credentials belong to, which is the
			// difference between a mystery and an instruction.
			writeError(w, d.Log, http.StatusConflict, "last_credential", lastKey.Error())
		case errors.Is(err, auth.ErrBadPassword):
			writeError(w, d.Log, http.StatusUnauthorized, "bad_password", "current password is incorrect")
		case errors.Is(err, auth.ErrNoProof), errors.Is(err, auth.ErrProofNotForThis):
			// THE TARGET IS PASSED, and it is what makes rule 2's second exclusion expressible: a
			// passkey does not count as a factor that could authorise its own removal, so `accepts`
			// lists `passkey` only when some OTHER credential can assert at this address.
			d.writeReauthRequired(w, err.Error(), auth.OpRemovePasskey,
				auth.RPIDFromRequest(r), r.PathValue("id"))
		default:
			d.Log.Error("passkey delete failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not remove the passkey")
		}
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

// POST /api/auth/setup/passkey/begin → 200 {ceremony, options}
//
// PRE-AUTH AND ONE-SHOT — qn.6m D5. This is `POST /api/auth/setup`'s sibling: reachable with no
// session because creating one is what it does, and refused with 409 the moment this install is
// configured by anything (a password OR any passkey, D3).
func (d Deps) handleSetupPasskeyBegin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// The SAME insecure-origin refusal `POST /api/auth/setup` carries, and for a stronger
		// reason: WebAuthn does not run outside a secure context at all, so a browser here would
		// fail after the round trip rather than before it.
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		options, ceremony, err := d.Auth.BeginSetupPasskey(d.Passkeys, auth.RPIDFromRequest(r), d.Proxies.ClientIP(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrRateLimited):
				writeError(w, d.Log, http.StatusTooManyRequests, "rate_limited", "too many attempts, try again later")
			case errors.Is(err, auth.ErrAlreadyConfigured):
				writeError(w, d.Log, http.StatusConflict, "already_configured", alreadySetUp)
			case d.writePasskeyError(w, err):
				// already written — covers passkeys_unsupported_here
			default:
				d.Log.Error("first-run passkey begin failed", "error", err)
				writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not start passkey setup")
			}
			return
		}
		writeJSON(w, d.Log, http.StatusOK, wire.PasskeyRegisterBegin{Ceremony: ceremony, Options: options})
	}
}

// POST /api/auth/setup/passkey/finish?ceremony=<key>&name=<label> → 200 {state, csrf_token} + cookies
//
// ISSUES A SESSION, exactly as `POST /api/auth/setup` does — the caller has just proved possession of
// a credential this install now holds. The response shape is `AuthStatus` and NOT the 201 {passkey}
// that authenticated registration returns, because this call's outcome is "you are signed in", not
// "here is a row you can now manage".
func (d Deps) handleSetupPasskeyFinish() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if d.refuseInsecureOrigin(w, r) {
			return
		}
		ceremony := strings.TrimSpace(r.URL.Query().Get("ceremony"))
		name := strings.TrimSpace(r.URL.Query().Get("name"))
		if ceremony == "" {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "ceremony is required")
			return
		}
		if name == "" {
			writeError(w, d.Log, http.StatusUnprocessableEntity, "name_required",
				"give this passkey a name you will recognise later")
			return
		}
		_, sess, csrf, err := d.Auth.FinishSetupPasskey(d.Passkeys, ceremony, name,
			auth.RPIDFromRequest(r), r, time.Now().UTC(), sessionCookieValue(r))
		if err != nil {
			switch {
			case errors.Is(err, auth.ErrAlreadyConfigured):
				// THE RACE'S LOSER GETS THIS. Two ceremonies can begin on a virgin install; the
				// credential write decides, and the second finisher is told the box is taken rather
				// than silently adding a second admin credential to it.
				writeError(w, d.Log, http.StatusConflict, "already_configured", alreadySetUp)
			case d.writePasskeyError(w, err):
				// already written
			default:
				d.Log.Error("first-run passkey finish failed verification", "error", err)
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
