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
	"net"
	"net/http"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/webui"
	"github.com/novkostya/quince/core/internal/wire"
	"github.com/novkostya/quince/core/internal/ws"
)

// HealthResponse is the body of GET /api/health (contracts.md — {status, version}).
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// NewRouter assembles the full handler: security middleware wraps a root mux that mounts
// the (separately self-guarding) WebSocket, the chained JSON API, and the UI fallback.
func NewRouter(deps Deps) http.Handler {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/health", deps.handleHealth())
	apiMux.HandleFunc("GET /api/auth/status", deps.handleAuthStatus())
	apiMux.HandleFunc("POST /api/auth/setup", deps.handleAuthSetup())
	apiMux.HandleFunc("POST /api/auth/login", deps.handleAuthLogin())
	apiMux.HandleFunc("POST /api/auth/logout", deps.handleAuthLogout())
	apiMux.HandleFunc("GET /api/config", deps.handleConfigGet())
	apiMux.HandleFunc("PUT /api/config", deps.handleConfigPut())
	apiMux.HandleFunc("GET /api/devices", deps.handleDevices())
	apiMux.HandleFunc("GET /api/devices/{udid}", deps.handleDevice())
	apiMux.HandleFunc("GET /api/jobs", deps.handleJobs())
	apiMux.HandleFunc("GET /api/jobs/{id}", deps.handleJob())
	apiMux.HandleFunc("GET /api/jobs/{id}/log", deps.handleJobLog())
	apiMux.HandleFunc("GET /api/versions", deps.handleVersions())
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
		writeJSON(w, d.Log, http.StatusOK, HealthResponse{Status: "ok", Version: d.Version})
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

func clientIP(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func sessionCookieValue(r *http.Request) string { return cookieValue(r, auth.SessionCookieName) }
