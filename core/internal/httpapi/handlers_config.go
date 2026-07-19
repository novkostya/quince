package httpapi

import (
	"net/http"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// configGetResponse is GET /api/config: {config, warnings, source} (contracts §1).
type configGetResponse struct {
	Config   config.Config    `json:"config"`
	Warnings []config.Warning `json:"warnings"`
	Source   config.Source    `json:"source"`
}

func (d Deps) handleConfigGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, warns, src := d.Config.Snapshot()
		if warns == nil {
			warns = []config.Warning{}
		}
		writeJSON(w, d.Log, http.StatusOK, configGetResponse{Config: cfg, Warnings: warns, Source: src})
	}
}

// PUT /api/config: full-document replace. Body is the bare config object. On invalid
// config returns 422 {errors:[{path,message}]}; on success returns the new GET shape.
func (d Deps) handleConfigPut() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cfg config.Config
		if err := decodeJSON(r, &cfg); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}
		errs, err := d.Config.Replace(cfg)
		if err != nil {
			d.Log.Error("config write failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not write config")
			return
		}
		if len(errs) > 0 {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: errs})
			return
		}
		cfg2, warns, src := d.Config.Snapshot()
		if warns == nil {
			warns = []config.Warning{}
		}
		writeJSON(w, d.Log, http.StatusOK, configGetResponse{Config: cfg2, Warnings: warns, Source: src})
	}
}
