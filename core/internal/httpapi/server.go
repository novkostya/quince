// Package httpapi wires the quince HTTP surface: the JSON REST API under /api and the
// embedded UI on everything else. qn.0 ships only the health probe; later rungs add the
// device/job/version endpoints frozen in docs/contracts.md.
package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/novkostya/quince/core/internal/version"
	"github.com/novkostya/quince/core/internal/webui"
)

// HealthResponse is the body of GET /api/health (contracts.md — {status, version}).
type HealthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version"`
}

// NewRouter returns the top-level handler: /api/* first, UI fallback last.
func NewRouter(log *slog.Logger) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", handleHealth(log))
	// Any other /api/* path is a real 404 (JSON), never the SPA fallback. More specific
	// than "/", so it wins for the /api/ prefix only.
	mux.HandleFunc("/api/", handleAPINotFound(log))
	mux.Handle("/", webui.Handler())
	return mux
}

func handleAPINotFound(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, log, http.StatusNotFound, errorBody{Error: errorDetail{
			Code:    "not_found",
			Message: "no such endpoint: " + r.URL.Path,
		}})
	}
}

// errorBody matches the contracts.md error envelope: {error: {code, message}}.
type errorBody struct {
	Error errorDetail `json:"error"`
}

type errorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func handleHealth(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, log, http.StatusOK, HealthResponse{
			Status:  "ok",
			Version: version.String(),
		})
	}
}

func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		log.Error("failed to encode response", "error", err)
	}
}
