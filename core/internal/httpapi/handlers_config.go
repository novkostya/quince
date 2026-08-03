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

// DELETE /api/config/storage/{name}: forget one storage (contracts §2, gap B ruling 2026-08-03).
// → 200 {config, warnings, source} | 404 | 422.
//
// A CONFIG MUTATION, NOT A RESOURCE-DELETE, and the shape follows from that: it returns the
// config-endpoint body rather than a 204, because what changed is the document. The client
// re-renders from the same payload GET and PUT hand it, and the restart notice arrives in the
// `warnings` channel it already displays.
//
// Addressed by the config `name`, never the marker UUID (quince#570): an unreachable storage has
// no UUID, and the storage a user most wants to forget is the one that never came up.
func (d Deps) handleConfigStorageDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		outcome, errs, err := d.Config.ForgetStorage(name)
		switch {
		case err != nil:
			d.Log.Error("config write failed", "error", err, "forgetting", name)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not write config")
			return
		case outcome == config.ForgetNoSuchStorage:
			writeError(w, d.Log, http.StatusNotFound, "no_such_storage",
				"no storage with that name is declared")
			return
		case outcome == config.ForgetRefused:
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: errs})
			return
		}

		cfg2, warns, src := d.Config.Snapshot()
		// APPENDED to the load's own warnings rather than replacing them: a config.yml that
		// already had, say, an unknown-key warning still has it, and dropping that to make room
		// for this one would trade one silent thing for another.
		warns = append(append([]config.Warning{}, warns...), config.ForgetRestartWarning(name))
		d.Log.Info("storage forgotten", "storage", name, "restart_required", true)
		writeJSON(w, d.Log, http.StatusOK, configGetResponse{Config: cfg2, Warnings: warns, Source: src})
	}
}
