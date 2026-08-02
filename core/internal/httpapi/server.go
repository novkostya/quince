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
type HealthResponse struct {
	Status  string        `json:"status"`
	Version string        `json:"version"`
	Mode    string        `json:"mode"` // normal | demo | public_demo
	Muxers  []MuxerHealth `json:"muxers"`
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
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/health", deps.handleHealth())
	apiMux.HandleFunc("GET /api/auth/status", deps.handleAuthStatus())
	apiMux.HandleFunc("GET /api/onboarding/step1", deps.handleOnboardingStep1())
	apiMux.HandleFunc("POST /api/auth/setup", deps.handleAuthSetup())
	apiMux.HandleFunc("POST /api/auth/login", deps.handleAuthLogin())
	apiMux.HandleFunc("POST /api/auth/logout", deps.handleAuthLogout())
	apiMux.HandleFunc("GET /api/config", deps.handleConfigGet())
	apiMux.HandleFunc("PUT /api/config", deps.handleConfigPut())
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
	apiMux.HandleFunc("POST /api/storages/{id}/recheck", deps.handleStorageRecheck())
	apiMux.HandleFunc("GET /api/versions", deps.handleVersions())
	apiMux.HandleFunc("DELETE /api/versions/{id}", deps.handleVersionDelete())
	apiMux.HandleFunc("/api/", deps.handleAPINotFound())

	apiHandler := chain(apiMux, bodyLimit, deps.authGuard, deps.csrfGuard)

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
		writeJSON(w, d.Log, http.StatusOK, HealthResponse{
			Status:  "ok",
			Version: d.Version,
			Mode:    mode,
			Muxers:  muxers,
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
