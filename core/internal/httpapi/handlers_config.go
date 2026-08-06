package httpapi

import (
	"fmt"
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
		errs, applied, err := d.Config.Replace(cfg)
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
		// APPENDED to the load's own warnings (qn.6g). An applier warning says "saved, but not
		// applied" — a fact about THIS response rather than about the file, so it rides here and is
		// never stored.
		//
		// This cited `ForgetRestartWarning` as the precedent, and that function is DELETED in the
		// same diff. The precedent outlives it: `config/forget.go` carries the note where it stood.
		warns = append(append([]config.Warning{}, warns...), applied...)
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

		// LIVENESS REFUSAL, BEFORE THE WRITE (qn.6g, Operator ruling 2026-08-06 — quince#577,
		// option (b)). A forget while a backup runs on that storage would leave `CommitJob` unable to
		// resolve its slot between verify passing and commit completing, and restart-time recovery
		// fails identically — which is what *"a commit failure must not destroy a multi-hour Wi-Fi
		// transfer"* forbids. `storage.Manager.JobsOn` carries the full reasoning.
		//
		// HERE RATHER THAN IN `config.Service`: putting it there would make the config package depend
		// on the storage subsystem, which is backwards — qn.6g's seam runs storage-subscribes-to-
		// config. `Deps` already holds both side by side, and HTTP semantics live in the handler.
		//
		// THE FIRST 422 ON THIS ENDPOINT ABOUT LIVENESS rather than about coherence of the
		// declaration: every other refusal here answers "is this a valid set of storages?", this one
		// answers "is quince busy?". Written into contracts §1 as a decision so the next reader does
		// not meet it as an inconsistency.
		if busy := d.jobsRunningOn(name); len(busy) > 0 {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: []wire.ConfigError{{
				Path: "storage",
				Message: fmt.Sprintf(
					"a backup is running on %q (job %s) — wait for it to finish, or cancel it, and "+
						"then forget the storage. Forgetting it now would leave that backup unable "+
						"to finish writing and unable to clean up.", name, busy[0]),
			}}})
			return
		}

		outcome, errs, applied, err := d.Config.ForgetStorage(name)
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
		//
		// `config.ForgetRestartWarning` USED TO BE APPENDED HERE and is deleted in this diff (qn.6g
		// PR 4). It told the user to restart quince to stop serving the storage; the applier below
		// has already stopped serving it by the time this line runs, so the notice had become the
		// thing `no silent caps or fallbacks` forbids in its other direction — a remedy prescribed
		// for a problem that no longer exists.
		warns = append(warns, applied...) // qn.6g: anything an applier could not take
		d.Log.Info("storage forgotten", "storage", name, "applier_warnings", len(applied))
		writeJSON(w, d.Log, http.StatusOK, configGetResponse{Config: cfg2, Warnings: warns, Source: src})
	}
}

// jobsRunningOn resolves a storage NAME to its id and asks which jobs are bound to it.
//
// The route is keyed by name (quince#570 — an unreachable storage has no marker and therefore no
// id), while the binding map is keyed by storage_id, so the two have to be joined somewhere. Here,
// through the storage list, which is the only place both keys appear together.
//
// A STORAGE WITH NO ID CANNOT HAVE A RUNNING JOB, and that is a fact rather than an assumption: an
// empty id means quince has never reached the storage, so no backup has ever been bound to it.
// Returning nil there is correct, and `ForgetStorage` still answers 404 for a name it does not know.
func (d Deps) jobsRunningOn(name string) []string {
	for _, s := range d.Storages.Storages("") {
		if s.Name == name {
			return d.Storages.JobsOn(s.ID)
		}
	}
	return nil
}
