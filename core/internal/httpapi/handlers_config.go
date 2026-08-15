package httpapi

import (
	"fmt"
	"net/http"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/wire"
)

// configGetResponse is GET /api/config: {config, warnings, source} (contracts §1).
// configResponse builds it with a NON-NIL warnings list, always.
//
// A NIL SLICE MARSHALS AS `null`, NOT `[]`, and the wire type says array — so a client doing
// the obvious thing crashes. `ConfigView` reads `data.warnings.length` and TypeScript offered
// no protection, because `ConfigResponse.warnings` is declared non-nullable and was telling
// the truth about the CONTRACT while the server broke it.
//
// IT IS REACHED BY THE ORDINARY PATH, not an edge: `Service.Snapshot` returns
// `append([]Warning(nil), s.warnings...)`, which is nil when there are none, and `Replace`
// CLEARS the warnings on every successful write. So the first save on a clean config hands
// the next reader a `null` — Operator-reported as an Unexpected Application Error on Settings
// that went away on refresh, because a refetch of an unwritten config had warnings again.
//
// The same treatment `handleStorages` already gives its list, for the same reason.
// A METHOD ON Deps, NOT A FREE FUNCTION, so every response carries the file text through ONE door.
// Four handlers build this body; a free function taking the text as an argument would be four places
// to remember, which is how one of them ends up shipping a preview the others do not have.
func (d Deps) configResponse(cfg config.Config, warns []config.Warning, src config.Source) configGetResponse {
	if warns == nil {
		warns = []config.Warning{}
	}
	// READ AT REQUEST TIME, never cached — see config.Service.FileText for why the alternative
	// contradicts the subtitle this panel sits under.
	return configGetResponse{
		Config: cfg, Warnings: warns, Source: src,
		FileText: d.Config.FileText(), Discarded: d.Config.Discarded(),
	}
}

type configGetResponse struct {
	Config   config.Config    `json:"config"`
	Warnings []config.Warning `json:"warnings"`
	Source   config.Source    `json:"source"`
	// Discarded — the file on disk was REFUSED at load, so quince is running on defaults and
	// nothing the file declares is in effect (Operator ruling 2026-08-12, quince#849).
	//
	// IT CARRIES THE FATALITY, NOT THE CAUSE. The cause is in `warnings` and always is; what no
	// client could infer is whether those warnings were fatal. `warnings` is non-empty in two states
	// that want opposite headlines — a discarded config, where the declared storage is not running,
	// and one that parsed with an ignored unknown key, where it is fine — and `config.storage: null`
	// does not separate them, since a fresh install with a typo has that too.
	//
	// A BOOLEAN RATHER THAN AN `errors: []`, and that is evidence rather than taste: only ONE of
	// `Load`'s three discard paths fills `Errors`, so a client keying off it would tell somebody
	// whose config cannot be read that their storage is fine.
	//
	// Same shape as `has_password` on `GET /api/auth/passkeys` (quince#855): a fact the client
	// cannot derive, on an endpoint that already requires a session, so no disclosure question.
	Discarded bool `json:"discarded"`
	// FileText is config.yml AS IT IS ON DISK (contracts §6, Operator ruling 2026-08-09 on
	// quince#728). The panel is titled "Current configuration" and its subtitle invites a hand-edit,
	// so it must show the FILE rather than a re-rendering of the parsed document — and after qn.6j
	// those are genuinely different documents: `config` is the RESOLVED configuration, every key
	// filled; this is only what was set. "" when there is no file yet.
	FileText string `json:"file_text"`
}

func (d Deps) handleConfigGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, warns, src := d.Config.Snapshot()
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg, warns, src))
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
		errs, applied, err := d.Config.Replace(cfg, config.SourcePutConfig)
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
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg2, warns, src))
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

		// THE LIVENESS REFUSAL IS HANDED IN, NOT RUN HERE FIRST (qn.6g, Operator ruling 2026-08-06 —
		// quince#577, option (b)). A forget while a backup runs on that storage would leave
		// `CommitJob` unable to resolve its slot between verify passing and commit completing, and
		// restart-time recovery fails identically — which is what *"a commit failure must not destroy
		// a multi-hour Wi-Fi transfer"* forbids. `storage.Manager.JobsOn` carries the full reasoning.
		//
		// THIS WAS AN `if` ABOVE THE CALL AND IT WAS WRONG, caught by `story8` on the first CI run
		// that dispatched. `--demo` keeps a backup running on `internal`, which is also the default —
		// so the transient refusal fired first and a user was told to *wait for the backup* when
		// waiting could never help. `ForgetStorage` now runs the declaration refusals first and calls
		// this only if the forget would otherwise succeed; its doc comment carries the reasoning.
		//
		// THE MESSAGE STAYS HERE. `config` gets a sentence, never a job — so it still knows nothing
		// about the storage subsystem, which is what kept the check out of that package to begin with.
		outcome, errs, applied, err := d.Config.ForgetStorage(name, func(storageName string) string {
			busy := d.jobsRunningOn(storageName)
			if len(busy) == 0 {
				return ""
			}
			return fmt.Sprintf(
				"a backup is running on %q (job %s) — wait for it to finish, or cancel it, and "+
					"then forget the storage. Forgetting it now would leave that backup unable "+
					"to finish writing and unable to clean up.", storageName, busy[0])
		})
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
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg2, warns, src))
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

// POST /api/config/storage: add one storage (contracts §1, qn.6e).
// → 200 {config, warnings, source} | 422.
//
// FORGET'S MIRROR, deliberately and in every respect that matters: a config mutation rather than a
// resource-create, returning the config-endpoint body rather than a 201, because what changed is the
// document. The client re-renders from the same payload GET, PUT and DELETE hand it.
//
// A NARROW ROUTE RATHER THAN `PUT /api/config`, for the identical reason gap B gave for the delete:
// it splices SERVER-SIDE, so it cannot drop a sibling entry's `zfs:` or `retention:` keys. A
// full-document PUT decodes into a zero-valued config.Config, so a client that reconstructs the list
// rather than splicing a fetched one silently resets every key it did not render — and no UI surface
// renders `zfs:` or `retention:`.
//
// NO PER-ROUTE `CheckStorageBackends` CALL, and its absence is the design. quince#683's ruling put
// that check in `replaceLocked`, which `config.AddStorage` writes through, so this path inherits it
// along with `Validate` and `CheckStorages`. Two call sites for one invariant is how they diverge.
//
// THE 422 CARRIES THE FIELD THE CALLER TYPED — `path`, `name`, `backend`, `default` — not
// `storage[i].…`. A caller adding ONE entry cannot map an index in the merged list back to its own
// input. `replaceLocked`'s document-wide errors still arrive in the indexed form underneath, so the
// shape a client renders is unchanged; only the addressing differs, and it differs toward the
// question that was asked.
func (d Deps) handleConfigStorageAdd() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var entry config.StorageEntry
		if err := decodeJSON(r, &entry); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}

		outcome, errs, warns, err := d.Config.AddStorage(entry)
		switch {
		case err != nil:
			d.Log.Error("config write failed", "error", err)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not write config")
			return
		case outcome == config.AddRefused:
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: errs})
			return
		}

		cfg2, loadWarns, src := d.Config.Snapshot()
		// APPENDED to the load's own warnings, exactly as the delete does: an applier warning says
		// "saved, but not applied" — a fact about THIS response rather than about the file.
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg2, append(loadWarns, warns...), src))
	}
}

// POST /api/config/storage/{name}/default: make one declared storage the default (contracts §1,
// Operator ruling 2026-08-11, quince#722).
// → 200 {config, warnings, source} | 404 | 422.
//
// THE THIRD CASE NOBODY BUILT. Adding a storage and forgetting one both exist and both point at
// this one: `POST /api/config/storage` refuses a newcomer that claims `default`, and `DELETE`
// refuses the default with *"Make another storage the default first."* Until this route, that
// remedy named a control the product did not have — which is `qn.6g`'s own named defect, *a remedy
// that was never going to work is the same defect as a silent failure*, sitting in shipped code.
// The add's refusal is reworded in the same change to name this route's surface.
//
// ADD'S AND FORGET'S MIRROR in every respect that matters: a config mutation, so it returns the
// config-endpoint body rather than a 204, and the client re-renders from the same payload GET, PUT,
// POST and DELETE all hand it.
//
// ADDRESSED BY THE CONFIG `name`, never the marker UUID, for `DELETE`'s reason (quince#570): an
// unreachable storage has no UUID, and a disk that is currently unplugged is one somebody may well
// be designating for later.
//
// THE FLAG MOVES AND THE FILE ORDER DOES NOT — that is the ruling, and `config.SetDefaultStorage`
// carries the full reasoning along with why there is no busy refusal here and why an unreachable
// storage is allowed.
//
// ALREADY-DEFAULT IS A 200. The route asserts a state rather than issuing a command, so asking for
// the state it is already in has been satisfied; a refusal there would have "do nothing" as its
// remedy. Delegated to the implementer by the ruling, and stated here because the absence of a
// fourth status code is otherwise indistinguishable from a case nobody thought about.
func (d Deps) handleConfigStorageSetDefault() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")

		outcome, errs, warns, err := d.Config.SetDefaultStorage(name)
		switch {
		case err != nil:
			d.Log.Error("config write failed", "error", err, "making default", name)
			writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not write config")
			return
		case outcome == config.SetDefaultNoSuchStorage:
			writeError(w, d.Log, http.StatusNotFound, "no_such_storage",
				"no storage with that name is declared")
			return
		case outcome == config.SetDefaultRefused:
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: errs})
			return
		}

		cfg2, loadWarns, src := d.Config.Snapshot()
		d.Log.Info("storage made default", "storage", name, "applier_warnings", len(warns))
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg2, append(loadWarns, warns...), src))
	}
}
