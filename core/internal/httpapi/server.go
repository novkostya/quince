// Package httpapi wires the quince HTTP surface: the JSON REST API under /api, the
// /api/ws event socket, and the embedded UI on everything else, all behind the
// non-negotiable web-security baseline (design §6). Wire shapes are frozen in
// docs/contracts.md and modeled in internal/wire.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/webui"
	"github.com/novkostya/quince/core/internal/wire"
	"github.com/novkostya/quince/core/internal/ws"
)

// HealthResponse is the body of GET /api/health. {status, version} since qn.1; qn.2b added muxer
// supervision state (design §10 — health surfaces muxer status honestly), which qn.4c turned into
// a per-daemon ARRAY: quince may supervise usbmuxd (USB) and netmuxd (Wi-Fi) at once, and a single
// aggregate object could not say which one was degraded. The singular `muxer` key is GONE (clean
// break ruled (bz)) — two overlapping representations rot, /api/health is not a frozen contract,
// and quince is its only consumer.
//
// qn.6 adds `mode`, which is how the LOGIN SCREEN learns it is a public demo and may print the
// password (public-demo spec story 5). Health rather than `/api/auth/status`, ruled 2026-08-02:
// auth/status is FROZEN in contracts §1 and health explicitly is not (above); health is already
// `authExempt`, and story 5 needs the mode BEFORE login; and health already reports how this daemon
// is DEPLOYED — muxer supervision, managed versus external — where auth/status answers who you are,
// which a mode is not. The cost is one extra request on the login page.
//
// A MODE STRING, not a boolean: a third mode later must not need a second field.
// qn.6 also adds `demo_reset_minutes` (spec story 6) — how often the DEPLOYMENT restarts a
// --public-demo instance. It rides here rather than anywhere else for the same reason `mode` does:
// the login screen is the one screen that must state it, and health is the only authExempt endpoint
// that is not frozen. `omitempty` is deliberate — an absent key and a zero are the same fact, "the
// deployment did not say", and one representation of one fact is fewer than two.
// qn.6i adds `reconciling`, and it is the half of blocker 2 that faces a client (Operator ruling
// 2026-08-08 on quince#731: SERVE, and report `reconciling`). Reconciliation no longer finishes
// before the listener binds, so quince can be serving a registry it knows is incomplete — and
// `state honesty` allows that only if the state is SURFACED.
//
// WHAT IT PROMISES, written here because a state nobody can act on is decoration:
//
//	true   a version list may be SHORT. Versions on disk that have not been adopted are absent from
//	       GET /api/versions and from Storage.backup_count; rows whose artifact vanished are not yet
//	       marked `missing`. THIS IS A DECLARED PROVISIONAL STATE, NOT AN EMPTY RESULT — a client
//	       must not conclude "this disk has no backups" while it holds.
//	false  the last triggered pass COMPLETED. It does not mean the disk was read a moment ago.
//
// A BOOLEAN, NOT A STRING — unlike `mode` above, which is a string precisely so a third mode needs
// no second field. Here there are two states and no candidate third; if one ever appears
// (`deferred`, say) that is a widening, and this sentence is the note saying so.
//
// DAEMON-WIDE, NOT PER STORAGE. One disk scanning while another is idle is a real distinction the
// daemon knows and this does not carry. Deliberately deferred rather than dropped: `Storage` already
// has three fields describing its condition (`reachable`, `unreachable_code`, `unreachable_reason`)
// and a fourth is a bigger contracts change than this rung needs (spec open question 1).
type HealthResponse struct {
	Status           string `json:"status"`
	Version          string `json:"version"`
	Mode             string `json:"mode"` // normal | demo | public_demo
	DemoResetMinutes int    `json:"demo_reset_minutes,omitempty"`
	Reconciling      bool   `json:"reconciling"`
	// InsecureOrigin says a session cookie earned on THIS connection would be marked Secure
	// and then discarded by the browser — so no credential can be established over it at all
	// (quince#908). It is `refuseInsecureOrigin`'s own predicate, surfaced rather than
	// re-derived: two implementations of one predicate is the hazard `RequireStorage` carries
	// a paragraph about, from having been bitten by it.
	//
	// PER REQUEST, UNLIKE EVERYTHING ELSE IN THIS PAYLOAD. `mode` and `demo_reset_minutes` are
	// properties of the daemon; this is a property of the CONNECTION, and the same daemon
	// answers differently to a browser on `https://name` and one on `http://ip`. That is what
	// makes it usable — a client learns it about the connection it is actually on.
	//
	// IT IS NOT `GET /api/onboarding/https`'s `complete`, AND THE DIFFERENCE IS LOOPBACK.
	// `detectHTTPS` reports `complete: false` on `http://localhost` deliberately, because that
	// step asks "can you reach quince from your phone" — but a cookie survives loopback fine,
	// so a client keying a redirect on THAT fact would send every developer on localhost to
	// the HTTPS onboarding page. Two questions that agree on most deployments and disagree on
	// the one a contributor runs.
	InsecureOrigin bool          `json:"insecure_origin"`
	Muxers         []MuxerHealth `json:"muxers"`
}

// Serving modes reported by GET /api/health (public-demo spec story 5).
const (
	ModeNormal     = "normal"      // the shipping product, real hardware
	ModeDemo       = "demo"        // --demo: fixtures, first-run setup, Secure forced off
	ModePublicDemo = "public_demo" // --public-demo: fixtures, password preset, Secure left alone
)

// MuxerHealth is one muxer daemon's slice of /api/health: which daemon, the transport it serves,
// whether quince manages it, its state (running | degraded | starting | stopped | external), a
// human detail (last exit reason / why degraded / why external), and whether
// POST /api/devices/rescan applies to it (USB only — restarting netmuxd would tear a live Wi-Fi
// backup).
type MuxerHealth struct {
	Name    string `json:"name"`
	Role    string `json:"role"` // usb | wifi
	Managed bool   `json:"managed"`
	State   string `json:"state"`
	Detail  string `json:"detail,omitempty"`
	Rescan  bool   `json:"rescan"`
}

// NewRouter assembles the full handler: security middleware wraps a root mux that mounts
// the (separately self-guarding) WebSocket, the chained JSON API, and the UI fallback.
func NewRouter(deps Deps) http.Handler {
	if deps.Muxer == nil { // external/--demo default: quince owns no muxer to restart
		deps.Muxer = UnmanagedMuxer{}
	}
	if deps.Ops == nil { // no device-ops subsystem wired → refuse honestly (503)
		deps.Ops = UnavailableDeviceOps{}
	}
	if deps.Storages == nil { // no storage subsystem wired → an empty list, not a 503 (qn.6c)
		deps.Storages = UnavailableStorages{}
	}
	if deps.VersionAdmin == nil { // no storage subsystem wired → refuse honestly (503)
		deps.VersionAdmin = UnavailableVersionAdmin{}
	}
	if deps.JobControl == nil { // no backup engine wired (--demo) → command surface 503
		deps.JobControl = UnavailableJobControl{}
	}
	if deps.WorkingReset == nil { // no backup engine wired → reset surface 503
		deps.WorkingReset = UnavailableWorkingReset{}
	}
	// --demo leaves this nil ON PURPOSE (qn.6m D6): the demo's password is shared with every
	// visitor, so one visitor changing or removing it would lock out the rest. The surface still
	// EXISTS and refuses with a stated reason — no `if demo` in any handler, which is quince#841's
	// instruction and keeps this the same shape as the four stand-ins above.
	if deps.PasswordAdmin == nil {
		deps.PasswordAdmin = UnavailablePasswordAdmin{}
	}
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/health", deps.handleHealth())
	apiMux.HandleFunc("GET /api/auth/status", deps.handleAuthStatus())
	apiMux.HandleFunc("GET /api/onboarding/https", deps.handleOnboardingHTTPS())
	apiMux.HandleFunc("POST /api/auth/setup", deps.handleAuthSetup())
	apiMux.HandleFunc("POST /api/auth/login", deps.handleAuthLogin())
	apiMux.HandleFunc("POST /api/auth/logout", deps.handleAuthLogout())
	// Changing and removing the admin password (qn.6m D4). BOTH NEED A SESSION, so they sit inside
	// authGuard with everything else and join NEITHER exact-path allowlist — which is the same
	// reasoning that keeps passkey REGISTRATION out of them, and the opposite of the assertion pair.
	//
	// Registered UNCONDITIONALLY, unlike the passkey routes below: a nil PasswordAdmin has already
	// become UnavailablePasswordAdmin above, and the demo must REFUSE with a reason rather than 404.
	apiMux.HandleFunc("PUT /api/auth/password", deps.handleChangePassword())
	apiMux.HandleFunc("DELETE /api/auth/password", deps.handleRemovePassword())
	// Registration needs a session, so these sit inside authGuard with everything else and touch
	// neither exact-path allowlist. Registered only when the ceremony store is wired (qn.6k).
	if deps.Passkeys != nil && deps.Store != nil {
		// FIRST-RUN passkey registration — PRE-AUTH and one-shot (qn.6m D5). Registered beside the
		// authenticated pair because both need the ceremony store, but it joins all three exact-path
		// lists where that pair joins none.
		apiMux.HandleFunc("POST /api/auth/setup/passkey/begin", deps.handleSetupPasskeyBegin())
		apiMux.HandleFunc("POST /api/auth/setup/passkey/finish", deps.handleSetupPasskeyFinish())
		apiMux.HandleFunc("POST /api/auth/passkeys/register/begin", deps.handlePasskeyRegisterBegin())
		apiMux.HandleFunc("POST /api/auth/passkeys/register/finish", deps.handlePasskeyRegisterFinish())
		apiMux.HandleFunc("POST /api/auth/passkeys/login/begin", deps.handlePasskeyLoginBegin())
		apiMux.HandleFunc("POST /api/auth/passkeys/login/finish", deps.handlePasskeyLoginFinish())
		// The reauth pair — qn.6n D3. In NONE of the three exact-path lists, unlike the login pair
		// directly above, which is in all three.
		apiMux.HandleFunc("POST /api/auth/reauth/begin", deps.handleReauthBegin())
		apiMux.HandleFunc("POST /api/auth/reauth/finish", deps.handleReauthFinish())
		apiMux.HandleFunc("GET /api/auth/passkeys", deps.handlePasskeyList())
		apiMux.HandleFunc("DELETE /api/auth/passkeys/{id}", deps.handlePasskeyDelete())
		apiMux.HandleFunc("PATCH /api/auth/passkeys/{id}", deps.handlePasskeyRename())
	}
	apiMux.HandleFunc("GET /api/config", deps.handleConfigGet())
	apiMux.HandleFunc("PUT /api/config", deps.handleConfigPut())
	apiMux.HandleFunc("POST /api/config/storage", deps.handleConfigStorageAdd())
	apiMux.HandleFunc("DELETE /api/config/storage/{name}", deps.handleConfigStorageDelete())
	apiMux.HandleFunc("GET /api/devices", deps.handleDevices())
	apiMux.HandleFunc("POST /api/devices/rescan", deps.handleRescan())
	apiMux.HandleFunc("GET /api/devices/{udid}", deps.handleDevice())
	apiMux.HandleFunc("POST /api/devices/{udid}/pair", deps.handlePair())
	apiMux.HandleFunc("POST /api/devices/{udid}/pair/validate", deps.handlePairValidate())
	apiMux.HandleFunc("POST /api/devices/{udid}/encryption", deps.handleEncryption())
	apiMux.HandleFunc("POST /api/devices/{udid}/wifi-sync", deps.handleWifiSync())
	apiMux.HandleFunc("POST /api/devices/{udid}/reset-working", deps.handleResetWorking())
	apiMux.HandleFunc("GET /api/ops/{op_id}", deps.handleOp())
	apiMux.HandleFunc("POST /api/jobs", deps.handleJobCreate())
	apiMux.HandleFunc("GET /api/jobs", deps.handleJobs())
	apiMux.HandleFunc("GET /api/jobs/{id}", deps.handleJob())
	apiMux.HandleFunc("POST /api/jobs/{id}/cancel", deps.handleJobCancel())
	apiMux.HandleFunc("GET /api/jobs/{id}/log", deps.handleJobLog())
	apiMux.HandleFunc("GET /api/storages", deps.handleStorages())
	apiMux.HandleFunc("POST /api/storages/{name}/recheck", deps.handleStorageRecheck())
	// NOT auth-exempt, and that is the whole of what has to be said about it: the exempt set is five
	// literal method+path strings above and this is not one of them, so the guard covers it by
	// default. GET /api/onboarding/https is pre-auth BY EXACT PATH because you cannot log in without
	// https; nothing about a storage probe is a prerequisite of logging in.
	apiMux.HandleFunc("POST /api/storages/probe", deps.handleStorageProbe())
	// Likewise not exempt — and it matters more here than for the probe, because this one EXECUTES A
	// REQUEST-SUPPLIED ARGV. See handleStorageHookCheck for why that adds no capability an
	// authenticated admin lacks, and what bounds it.
	apiMux.HandleFunc("POST /api/storages/probe/hook", deps.handleStorageHookCheck())
	// POST rather than GET because it CAN CREATE (quince#818): it returns the key at
	// `/data/keys/zfs`, generating one only if there is nothing there. Not exempt either, and this
	// one writes to quince's own volume — see handleStorageZFSKey for why it can only ever touch
	// that one path, and why it refuses rather than replacing whatever it finds.
	apiMux.HandleFunc("POST /api/storages/zfs/key", deps.handleStorageZFSKey())
	// The constrained helper, rendered with the caller's `parent_dataset` (quince#818 piece C). A
	// GET, unlike the key above, because it reads nothing and writes nothing: the answer is a pure
	// function of the embedded script and one query parameter.
	apiMux.HandleFunc("GET /api/storages/zfs/helper", deps.handleStorageZFSHelper())
	// The host-key ceremony (quince#912), two steps because the split IS the security property:
	// scan shows a fingerprint and writes nothing; trust records the line that was confirmed and
	// never re-reads the host. POST for both — one opens a connection, the other writes a file.
	apiMux.HandleFunc("POST /api/storages/zfs/hostkey", deps.handleStorageZFSHostKey())
	apiMux.HandleFunc("POST /api/storages/zfs/hostkey/trust", deps.handleStorageZFSHostKeyTrust())
	apiMux.HandleFunc("GET /api/versions", deps.handleVersions())
	apiMux.HandleFunc("DELETE /api/versions/{id}", deps.handleVersionDelete())
	apiMux.HandleFunc("/api/", deps.handleAPINotFound())

	// setupGuard runs AFTER authGuard and csrfGuard, not before, and the order is the point: an
	// unauthenticated caller must get a 401 rather than being told what state the daemon is in.
	// Setup mode is a fact about a configured-but-unfinished install, and it is the operator's to
	// see — leaking it to anyone who can reach the port would say "this quince is not set up yet",
	// which is precisely what a stranger should not learn.
	apiHandler := chain(apiMux, bodyLimit, deps.authGuard, deps.csrfGuard, deps.setupGuard)

	wsHandler := ws.Handler(deps.Bus,
		func(sessionID string) error { _, err := deps.Auth.Authenticate(sessionID); return err },
		deps.Version, deps.AllowedOrigins, deps.Log)

	root := http.NewServeMux()
	root.Handle("/api/ws", wsHandler) // self-guarding; bypasses the JSON API chain
	root.Handle("/api/", apiHandler)
	root.Handle("/", webui.Handler())

	return chain(root, recoverMW(deps.Log), securityHeaders)
}

func (d Deps) handleHealth() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// An unset Mode means nobody wired it — report `normal` rather than an empty string, so
		// the UI never has to treat "" as a fourth case (no silent fallbacks: this is the
		// SHIPPING mode, which is what an unwired router is).
		mode := d.Mode
		if mode == "" {
			mode = ModeNormal
		}
		muxers := d.Muxer.MuxersHealth()
		if muxers == nil {
			muxers = []MuxerHealth{}
		}
		// READ LIVE, PER REQUEST — never captured. The whole point of the field is that it changes
		// while the process runs, and a deploy check polls this endpoint precisely across that
		// transition. Nil when nothing wired a runner (`--demo`, the admin CLIs, any test router),
		// which reports `false`: no runner means no asynchronous pass, so nothing is provisional.
		reconciling := false
		if d.Reconcile != nil {
			reconciling = d.Reconcile.Reconciling()
		}
		// FROM THE AUTH SERVICE'S OWN PREDICATE, so this answer and the 426 that enforces it
		// cannot disagree.
		//
		// NO NIL GUARD, and that is measured rather than assumed. `Auth` is a pointer, so one
		// looks prudent — but `middleware.go`'s CSRF chain calls `d.Auth.Secure(r)` on EVERY
		// request including the authExempt ones, so a router with no auth service cannot serve
		// this endpoint or any other. A guard here would be unreachable code whose comment
		// described a router that cannot exist. Written after a test case for it returned 500
		// from the middleware rather than 200 from the guard.
		writeJSON(w, d.Log, http.StatusOK, HealthResponse{
			Status:           "ok",
			Version:          d.Version,
			Mode:             mode,
			DemoResetMinutes: d.DemoResetMinutes,
			Reconciling:      reconciling,
			InsecureOrigin:   d.Auth.CookieWillBeDiscarded(r),
			Muxers:           muxers,
		})
	}
}

func (d Deps) handleAPINotFound() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeError(w, d.Log, http.StatusNotFound, "not_found", "no such endpoint: "+r.URL.Path)
	}
}

// --- shared helpers ---

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Error("failed to encode response", "error", err)
	}
}

func writeError(w http.ResponseWriter, log *slog.Logger, status int, code, message string) {
	writeJSON(w, log, status, wire.APIError{Error: wire.ErrorDetail{Code: code, Message: message}})
}

// decodeJSON decodes a JSON request body into v, rejecting unknown fields and oversized or
// malformed input.
func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	// Reject trailing garbage after the JSON value.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("unexpected trailing data in request body")
	}
	return nil
}

func cookieValue(r *http.Request, name string) string {
	if c, err := r.Cookie(name); err == nil {
		return c.Value
	}
	return ""
}

func sessionCookieValue(r *http.Request) string { return cookieValue(r, auth.SessionCookieName) }
