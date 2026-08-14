package httpapi

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"time"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/tlsx"
	"github.com/novkostya/quince/core/internal/wire"
)

// certificateApplied answers POST /api/onboarding/certificate/apply.
//
// IN httpapi RATHER THAN wire, for `configGetResponse`'s reason: it carries `config.Warning`, and
// `wire` cannot import `config` — `config` imports `wire`.
type certificateApplied struct {
	// ConfirmOrigin is where the confirmation must come FROM: scheme `https`, the certificate's own
	// hostname when one was given, and — always explicitly — the port this request arrived on.
	//
	// AN ORIGIN RATHER THAN A URL, so the path belongs to whoever renders the page.
	ConfirmOrigin string `json:"confirm_origin"`
	// ConfirmToken names THIS trial. Not a credential — see certTrial.confirm.
	ConfirmToken string `json:"confirm_token"`
	// ExpiresAt is when the trial ends and the previous certificate comes back, RFC3339 UTC.
	// ABSOLUTE rather than a countdown, so a client that was backgrounded does not resume from a
	// stale remainder.
	ExpiresAt string `json:"expires_at"`
	// ExpiresSeconds is the same window as a length, for prose ("you have ten minutes"). A client
	// needs one of these to render a clock and the other to write a sentence.
	ExpiresSeconds int `json:"expires_seconds"`
	// ConfigWritten is FALSE here, always, and it is sent rather than implied. This is the whole
	// design in one field: the certificate is being SERVED and `config.yml` has not been touched.
	// A client that cannot see this distinction would tell the user their certificate was saved.
	ConfigWritten bool `json:"config_written"`
}

// POST /api/onboarding/certificate/apply {cert_file, key_file, hostname} → 200 | 400 | 409 | 422 |
// 500 | 503 (quince#908 §5, slice 5).
//
// It hands the pair to the running daemon — which starts serving TLS immediately — and schedules a
// return to the pair `config.yml` names unless somebody confirms over https within
// `certTrialWindow`.
//
// # IT WRITES NO CONFIGURATION. THE CONFIRM DOES.
//
// Operator, 2026-08-14: *"we're not going to actually write tls setting entry to config.yml for that
// 30 seconds and only write config once probe has succeeded?"* An earlier shape wrote the pair here
// and wrote the file a second time to undo it, leaving a certificate that never worked visible in a
// hand-edited file for ten minutes. D12 says `config.yml` holds only what the user set, and a
// certificate somebody tried and abandoned was never something they set.
//
// SO THE RULED PRE-AUTH WRITE IS SPENT ON THE CONFIRM ROUTE, NOT THIS ONE. The ruling of 2026-08-14
// extended the pre-auth write to `tls.cert_file` and `tls.key_file`; deferring it satisfies that
// ruling more narrowly rather than departing from it, because the write still happens and still
// happens without a session — only after the certificate has proved itself.
//
// `Configured()` IS STILL THE BOUND HERE. Serving a trial certificate is not a config write, but it
// IS a change to how the daemon is reached, and on a claimed install this route would be an
// unauthenticated *point quince's TLS at any file* primitive. The guard is the first statement, and
// the 409 is decided before the body is read.
//
// # IT REFUSES ANYTHING THE OFFLINE CHECK DOES NOT CALL `usable`
//
// The same `tlsx.Inspect` that backs `POST /api/onboarding/certificate`, run again here rather than
// trusted from the client: a pair can be replaced between the check and the apply, and *"the UI
// checked it"* is not a fact this handler has. THE ESCAPE HATCH FOR THAT REFUSAL IS D12 ITSELF —
// `config.yml` is hand-editable, and a user who knows better can write the pair in directly.
//
// # IT DOES NOT TOUCH `sessions.allow_insecure_transport`, IN EITHER DIRECTION
//
// Ruled, and both halves matter. Turning it OFF here would couple the plain-http escape hatch to the
// riskiest operation in the product. REFUSING while it is on would be a remedy the user may not be
// able to follow, since a new session over plain http gets cookies without `Secure` — quince#912's
// defect exactly.
//
// WHAT IS GIVEN UP IS *"turning it on IS the test"*: with the opt-in on, `plainHalf` does not
// redirect, so the proof is a navigation the user performs rather than one they are carried through.
// The FACT ESTABLISHED IS IDENTICAL — a request arrived with `r.TLS != nil` carrying this trial's
// token — and only how the browser got there differs.
func (d Deps) handleCertificateApply() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// FIRST, BEFORE THE BODY IS READ. On a claimed install the 409 is the only thing this
		// endpoint says, so a caller who should not be here cannot tell a malformed request from a
		// well-formed one.
		configured, err := d.Auth.Configured()
		if err != nil {
			d.Log.Error("could not determine whether the install is configured", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not read auth state")
			return
		}
		if configured {
			writeError(w, d.Log, http.StatusConflict, "already_configured",
				"quince is already set up — sign in and configure TLS from Settings")
			return
		}

		// THE PROBE'S REQUEST TYPE, REUSED. The apply must check exactly the pair the offline probe
		// checked, so a second type that could drift from it would be a defect waiting to happen.
		var body wire.CertificateProbeRequest
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}

		// THE SAME MALFORMED-QUESTION RULE AS THE PROBE: quince's working directory is not the
		// operator's, so a relative path names a file neither of them can agree on.
		for _, f := range []struct{ field, value string }{
			{"cert_file", body.CertFile},
			{"key_file", body.KeyFile},
		} {
			if f.value == "" {
				writeConfigErrors(w, d.Log, f.field, "required — a certificate cannot be applied without both files")
				return
			}
			if !filepath.IsAbs(f.value) {
				writeConfigErrors(w, d.Log, f.field, "must be an absolute path — quince's working directory is not yours")
				return
			}
		}

		// RE-CHECKED HERE RATHER THAN TAKEN FROM THE CLIENT — see the doc comment.
		if rep := tlsx.Inspect(body.CertFile, body.KeyFile, body.Hostname, time.Now()); rep.Outcome != tlsx.OutcomeUsable {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors  []wire.ConfigError `json:"errors"`
				Outcome string             `json:"outcome"`
			}{
				Errors:  []wire.ConfigError{{Path: "tls.cert_file", Message: rep.Reason}},
				Outcome: rep.Outcome,
			})
			return
		}

		// THE PAIR TO COME BACK TO IS THE ONE THE FILE NAMES, and it is read HERE rather than at
		// expiry. The file does not change during the trial — that is the whole design — but an
		// authenticated admin could edit it, and "the previous pair" must mean what it meant when
		// the user pressed Apply.
		prev := d.Config.Current().TLS

		token, err := newConfirmToken()
		if err != nil {
			// BEFORE THE KEEPER MOVES, so a trial nobody could ever confirm is never started. The
			// write-first shape had to undo a live change here; this one simply has not begun.
			d.Log.Error("could not mint a confirm token", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not start the trial")
			return
		}

		deadline, err := d.CertTrial.begin(token, body.CertFile, body.KeyFile, prev.CertFile, prev.KeyFile)
		if err != nil {
			if errors.Is(err, errNoKeeper) {
				// HONEST 503 RATHER THAN A PRETENDED SUCCESS. A router with no TLS keeper — `--demo`,
				// a test router — cannot serve a certificate, and saying so is `no silent caps or
				// fallbacks` rather than an apology.
				writeError(w, d.Log, http.StatusServiceUnavailable, "tls_unavailable",
					"this quince has no TLS listener, so a certificate cannot be applied here")
				return
			}
			// THE OFFLINE CHECK PASSED AND THE LOAD STILL FAILED, so the pair moved underneath us
			// between the two. The daemon is unchanged — `begin` puts the Keeper back before
			// returning — and the reason is the loader's own, which names the file.
			writeConfigErrors(w, d.Log, "tls.cert_file", "checked out a moment ago and would not load now: "+err.Error())
			return
		}

		writeJSON(w, d.Log, http.StatusOK, certificateApplied{
			ConfirmOrigin:  httpsOrigin(r, body.Hostname),
			ConfirmToken:   token,
			ExpiresAt:      deadline.UTC().Format(time.RFC3339),
			ExpiresSeconds: int(certTrialWindow / time.Second),
			ConfigWritten:  false,
		})
	}
}

// POST /api/onboarding/certificate/confirm {token} → 200 | 400 | 409 | 422 | 426 | 500.
//
// IT PROVES TWO THINGS AT ONCE and needs both: `r.TLS != nil` says quince's own TLS half completed a
// handshake with this client, and the token says which trial that is evidence for. Only then is
// `config.yml` written.
//
// `auth.SecureOrigin` WOULD BE WRONG HERE, and it is the obvious wrong choice because every other
// *is this connection secure* question in this product uses it. It believes `X-Forwarded-Proto` from
// a trusted proxy — which proves SOMETHING terminated TLS, not that the trial pair works. A user
// behind a terminating proxy confirming on that evidence would write a certificate into their config
// that has never completed a handshake with anything.
func (d Deps) handleCertificateConfirm() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		configured, err := d.Auth.Configured()
		if err != nil {
			d.Log.Error("could not determine whether the install is configured", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not read auth state")
			return
		}
		if configured {
			writeError(w, d.Log, http.StatusConflict, "already_configured",
				"quince is already set up — sign in and configure TLS from Settings")
			return
		}

		// BEFORE THE BODY, because it is a property of the connection and no body can change it.
		// 426 is the code `refuseInsecureOrigin` already uses for "this request must be encrypted",
		// so the product says one thing in one way.
		if r.TLS == nil {
			writeError(w, d.Log, http.StatusUpgradeRequired, "insecure_transport",
				"a certificate is confirmed over https and only over https — open the confirm_origin "+
					"this apply returned and try again there")
			return
		}

		var body wire.CertificateConfirmRequest
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}

		certFile, keyFile, ok := d.CertTrial.confirm(body.Token)
		if !ok {
			// ONE ANSWER FOR THREE CAUSES — no trial is running, the window closed, or this token
			// names a superseded trial — because from the client's side the remedy is identical:
			// apply again. The message names both likely causes, so nobody is left stuck.
			//
			// AND QUINCE CAN TELL TWO OF THE THREE APART (quince#979 review). This comment said
			// distinguishing them "would mean holding spent tokens", which is true only of
			// SUPERSEDED: *nothing running* is `live == nil` and *the window closed* is
			// `live != nil && expired()`, both known at the check with no retained state. The
			// collapse is a choice about wording, not a limit on what quince knows — and
			// `CLAUDE.md`'s troubleshooting rule says *"quince cannot tell"* is legitimate only
			// when it genuinely cannot, which is a claim to CHECK rather than assume.
			writeError(w, d.Log, http.StatusConflict, "not_armed",
				"nothing is waiting to be confirmed — the window may have closed and the previous "+
					"certificate come back, or a later apply replaced this one. Apply again.")
			return
		}

		// THE WRITE, AND IT IS THE FIRST ONE IN THE WHOLE CEREMONY. The trial is already cancelled at
		// this point, so a failure here leaves the pair SERVING with no timer — which is why the
		// error says so rather than reporting a bare 500.
		errs, warns, err := d.Config.SetTLSFiles(certFile, keyFile, config.SourceApplyCertificate)
		switch {
		case err != nil:
			d.Log.Error("the confirmed certificate could not be written to config.yml — it is being "+
				"served but will not survive a restart", "cert_file", certFile, "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal",
				"quince is serving this certificate but could not save it — it will not survive a restart")
			return
		case len(errs) > 0:
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: errs})
			return
		}

		// LOGGED, because this is the unauthenticated write the ruling permits, and the startup line
		// is the only other place it would ever appear.
		d.Log.Warn("tls.cert_file/tls.key_file written by a PRE-AUTH caller after an https confirmation",
			"cert_file", certFile, "key_file", keyFile,
			"route", "POST /api/onboarding/certificate/confirm")

		if warns == nil {
			warns = []config.Warning{}
		}
		writeJSON(w, d.Log, http.StatusOK, struct {
			Confirmed     bool             `json:"confirmed"`
			ConfigWritten bool             `json:"config_written"`
			Warnings      []config.Warning `json:"warnings"`
		}{Confirmed: true, ConfigWritten: true, Warnings: warns})
	}
}

// httpsOrigin is where the confirmation must come from: `https://` + the certificate's hostname (or
// this request's, when none was given) + THE PORT THIS REQUEST ARRIVED ON.
//
// THE PORT IS THE WHOLE OF THE DIFFICULTY, and a bare scheme swap gets it wrong. quince serves both
// protocols on ONE listener (`tlsx.Mux` splits on the ClientHello), so the https half is on exactly
// the port the http half was reached at. `http://host` → `https://host` would send the browser to
// 443, where nothing is listening — so the port is always written out, including `:80`, which is
// legal in an https URL and is the truth on a default-port install.
//
// THE HOSTNAME IS THE CERTIFICATE'S, NOT THE REQUEST'S, when the caller gave one: the name they are
// about to use is the name the certificate covers, and sending them to the IP they are on now would
// produce a name mismatch in the browser for a certificate that is perfectly good.
func httpsOrigin(r *http.Request, hostname string) string {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		// No port in the Host header, so it is the scheme's default. This request reached the plain
		// half, so that is 80 — and 80 is where the TLS half is too.
		host, port = r.Host, "80"
	}
	if hostname != "" {
		host = hostname
	}
	return "https://" + net.JoinHostPort(host, port)
}

// newConfirmToken mints the opaque name of one trial. Same shape as a probe nonce: 32 random bytes,
// URL-safe, because it travels in a link a user may carry to another device.
func newConfirmToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
