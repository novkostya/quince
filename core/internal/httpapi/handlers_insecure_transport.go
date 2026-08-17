package httpapi

import (
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/wire"
)

// insecureTransportBody is the whole of this route's input: one boolean, named for the setting it
// writes rather than for the act, so the request reads the same way the config file does.
type insecureTransportBody struct {
	Allow bool `json:"allow"`
}

// POST /api/config/insecure-transport {allow} → 200 {config, warnings, source} | 409 | 422 | 500.
//
// THE ONE PRE-AUTH MUTATING ROUTE IN THIS PRODUCT THAT IS NOT ABOUT OBTAINING A CREDENTIAL —
// Operator ruling 2026-08-14 on quince#908, recorded in contracts §1. It exists because first run
// over plain http at a LAN address is a dead end with no unauthenticated exit: `refuseInsecureOrigin`
// answers `426` to `POST /api/auth/setup` BEFORE looking at the password, so the install cannot be
// claimed at all, and the only remedy was a shell on the box. quince#912 settled the principle — *a
// remedy the user cannot follow is the same defect as a silent failure.*
//
// `Configured()` IS THE WHOLE SECURITY BOUND, AND IT IS THE SERVER'S TO ENFORCE. An attacker does
// not use the UI, so gating the button is not a control. On a CONFIGURED install this route without
// its guard is an unauthenticated *turn off the transport requirement* primitive: flip it, wait for
// the admin to sign in over plain http, read the cookie. The ruling names that failure explicitly
// because it arrives by implementing the ruling carelessly rather than by disagreeing with it.
//
// THE SAME CALL `POST /api/auth/setup` MAKES, not a second predicate — ruled, so the two cannot
// drift and the window closes at the instant the install is claimed rather than at some nearby
// instant. It is deliberately NOT rpId-filtered, for the reason `Configured` itself documents: the
// question is *has this install been claimed*, and a passkey bound to another address still claims
// it.
//
// WHY IT IS SAFE IN THAT WINDOW, restated because a reviewer should not have to fetch the ruling:
// before a credential exists `POST /api/auth/setup` is itself authExempt and one-shot, so anyone who
// reaches the port can claim the whole install outright. Writing one boolean grants strictly less
// than that, so there is nothing here to protect that is not already on offer.
//
// UNDER `/api/config/`, NOT `/api/onboarding/`. The ruling excluded the latter: that prefix means
// *step 1, pre-auth, read-only* today, and the product's first pre-auth WRITE beneath it would
// invite a reader to treat the prefix as the exemption. This route's prefix means AUTHENTICATED, so
// one exempt path here reads as the deliberate exception it is. All three exemption lists are exact
// paths with no prefix support, which qn.6f calls a constraint rather than a style.
//
// IT WRITES ONE KEY. Not `PUT /api/config` exempted, which would put every setting behind an
// unauthenticated door — see `config.SetAllowInsecureTransport`, which is narrow for this reason.
//
// AND IT ACCEPTS `false`. A control that only turns the relaxation ON would be a second dead end,
// and quince#900 made the setting live in both directions. The guard is unchanged: once the install
// is claimed this route is closed in both directions, and Settings is where an authenticated admin
// turns it off.
func (d Deps) handleInsecureTransportSet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// BEFORE THE BODY IS READ, so a malformed request on a configured install cannot be told
		// apart from a well-formed one. The 409 is the only thing this route says to a caller who
		// should not be here.
		configured, err := d.Auth.Configured()
		if err != nil {
			d.Log.Error("could not determine whether the install is configured", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not read auth state")
			return
		}
		// A CLAIMED INSTALL IS NOT CLOSED TO ITS OWN ADMIN — Operator direction, 2026-08-16
		// (quince#1069). This route is the only writer of the key, and `ConfigEditor` does not render
		// it: refuse every caller on a claimed install and the setting can be turned ON from the UI
		// and OFF from nowhere, which is stack D12 broken.
		//
		// `PUT /api/config` IS THE WRONG ALTERNATIVE, and not for style: it is a full-document
		// replace, so a client that does not model every Go key drops the ones it does not know
		// (quince#493). `SetAllowInsecureTransport` writes one key by name.
		//
		// THE AUTHENTICATED PATH MUST NOT INHERIT THE `csrfExempt` ENTRY. That entry exists for the
		// pre-auth caller, who has no session and therefore no cookie to double-submit. Extended to a
		// caller who HAS one it makes this write forgeable cross-site: any page could POST
		// `{allow:true}` with the admin's cookies and obtain the downgrade primitive quince#908 §3
		// refuses. So the session AND the token are checked here, explicitly, rather than by trusting
		// an exempt list to mean for one caller what it means for the other.
		if configured {
			if _, err := d.Auth.Authenticate(sessionCookieValue(r)); err != nil {
				// THE SAME CODE AND THE SAME SHAPE AS `POST /api/auth/setup`'s one-shot refusal, for
				// an anonymous caller: the install has been claimed. A 404 would hide the route, which
				// is worth less than it looks — the UI is public and the path is in the docs — and it
				// would cost the first-run user a comprehensible answer if they ever reached it late.
				writeError(w, d.Log, http.StatusConflict, "already_configured",
					"quince is already set up — sign in to change this")
				return
			}
			if !auth.CheckCSRF(r) {
				// THE SAME SENTENCE THE MIDDLEWARE GIVES, from the same function. This branch is
				// reached only by a caller that HAS a session, so the plain-http arm cannot fire —
				// but sharing the source is what keeps the product saying one thing in one way if
				// either half moves.
				writeError(w, d.Log, http.StatusForbidden, "csrf", d.csrfRefusal(r))
				return
			}
		}

		var body insecureTransportBody
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}

		errs, warns, err := d.Config.SetAllowInsecureTransport(body.Allow)
		switch {
		case err != nil:
			d.Log.Error("config write failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not write config")
			return
		case len(errs) > 0:
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: errs})
			return
		}

		// LOGGED, because the startup line is the only other place this would ever appear. `state
		// honesty` and `no silent caps or fallbacks` both land here: the operator reading the journal
		// afterwards should find the moment somebody changed the transport, not infer it from the
		// file's mtime.
		//
		// IT SAYS WHICH CALLER, because the two are different events. A pre-auth write is a stranded
		// first-run user taking the escape hatch; an authenticated one is an admin using the control in
		// the UI. This line is the audit trail for a security setting, so it must not report one as
		// the other.
		d.Log.Warn("sessions.allow_insecure_transport written",
			"allow", body.Allow, "caller", map[bool]string{true: "authenticated", false: "pre-auth"}[configured],
			"route", "POST /api/config/insecure-transport")

		cfg, loadWarns, src := d.Config.Snapshot()
		// APPENDED to the load's own warnings, as every other config route does: an applier warning
		// is a fact about THIS response rather than about the file.
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg, append(loadWarns, warns...), src))
	}
}
