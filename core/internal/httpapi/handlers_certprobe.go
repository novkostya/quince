package httpapi

import (
	"log/slog"
	"net/http"
	"path/filepath"
	"time"

	"github.com/novkostya/quince/core/internal/tlsx"
	"github.com/novkostya/quince/core/internal/wire"
)

// POST /api/onboarding/certificate {cert_file, key_file, hostname} → the OFFLINE half of the
// certificate probe (quince#908 §5, slice 4). → 200 | 400 | 409 | 422.
//
// It answers *what is this pair I am about to declare?* — the pair loads, the key belongs to the
// certificate, it is in date, and it covers the name. **Nothing else in quince performs that check
// before the pair goes live**: `validateTLS` says at its own definition that it checks
// "well-formedness and nothing else", and `CheckTLS` runs at STARTUP against the pair already in
// `config.yml`.
//
// EVERY REFUSAL IS CARRIED IN THE BODY, NOT AS A STATUS — `StorageProbe`'s rule, for its reason:
// "that certificate expired last week" is the ANSWER to the question asked, not a failure to answer
// it. Only a malformed QUESTION — a missing or relative path — is a 422.
//
// # It reads caller-supplied paths before authentication, and that is RULED rather than assumed
//
// This is a pre-auth endpoint that opens a filesystem path the caller names, so it can tell a
// stranger whether `/etc/shadow` exists and whether it parses as PEM. That is a real capability and
// it is worth stating plainly rather than burying.
//
// IT IS THE SAME BOUND, AND THE SAME ARGUMENT, AS `POST /api/config/insecure-transport` (Operator
// ruling 2026-08-14 on quince#908 §3): before a credential exists, `POST /api/auth/setup` is itself
// authExempt and one-shot, so **anyone who reaches the port can claim the install outright** — and an
// admin can point `tls.cert_file` at any path and read the same load error out of the startup
// refusal. So this grants strictly less than what is already on offer in that window, which is the
// test §3 sets. `Configured()` closes it at the instant the install is claimed.
//
// AND IT IS NOT THE OPEN QUESTION quince#940 §1 ASKS. That one is about a CONFIGURED install — an
// admin changed the pair at runtime and the page says nothing — where this window has shut and the
// argument above does not apply. Both touch "a path and an OpenSSL error reach a client"; only that
// one is unruled. Do not read this handler as having settled it.
func (d Deps) handleCertificateProbe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// FIRST, BEFORE THE BODY IS READ, exactly as the transport route does it: on a claimed
		// install the 409 is the only thing this endpoint says, so a caller who should not be here
		// cannot tell a malformed request from a well-formed one.
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

		var body wire.CertificateProbeRequest
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}

		// A MALFORMED QUESTION IS A 422, and a relative path is one: quince's working directory is
		// not the operator's, so `./cert.pem` names a file neither of them can agree on. The startup
		// refusal teaches absolute paths, and accepting a relative one here would check a different
		// file from the one that would be served.
		for _, f := range []struct{ field, value string }{
			{"cert_file", body.CertFile},
			{"key_file", body.KeyFile},
		} {
			if f.value == "" {
				writeConfigErrors(w, d.Log, f.field, "required — a certificate cannot be checked without both files")
				return
			}
			if !filepath.IsAbs(f.value) {
				writeConfigErrors(w, d.Log, f.field, "must be an absolute path — quince's working directory is not yours")
				return
			}
		}

		rep := tlsx.Inspect(body.CertFile, body.KeyFile, body.Hostname, time.Now())

		// `names` IS AN ARRAY ON THE WIRE, NEVER `null`, AND THE FAILURE PATHS ARE WHY IT NEEDS
		// SAYING. A nil Go slice marshals to `null`, and `Inspect` returns before it has a leaf to
		// read names from whenever the pair does not load or does not parse — so `unreadable`,
		// `mismatched` and `malformed` are exactly the outcomes that would emit a shape the contract
		// forbids. A client that trusts the declared type reads `.length` off it.
		names := rep.Names
		if names == nil {
			names = []string{}
		}

		writeJSON(w, d.Log, http.StatusOK, wire.CertificateProbe{
			CertFile:    body.CertFile,
			KeyFile:     body.KeyFile,
			Hostname:    body.Hostname,
			Outcome:     rep.Outcome,
			Reason:      rep.Reason,
			Names:       names,
			NotBefore:   rep.NotBefore,
			NotAfter:    rep.NotAfter,
			ChainLength: rep.ChainLength,
		})
	}
}

// writeConfigErrors emits the {errors:[{path,message}]} shape every config surface already uses, so
// a form highlights the offending field rather than showing a sentence above the whole thing.
func writeConfigErrors(w http.ResponseWriter, log *slog.Logger, path, msg string) {
	writeJSON(w, log, http.StatusUnprocessableEntity, struct {
		Errors []wire.ConfigError `json:"errors"`
	}{Errors: []wire.ConfigError{{Path: path, Message: msg}}})
}
