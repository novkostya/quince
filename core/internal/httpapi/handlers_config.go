package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
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

		// THE VERSIONS REFUSAL, AND IT RUNS BEFORE `ForgetStorage` RATHER THAN INSIDE IT
		// (quince#1525). Two reasons, and the first is why this is not the mistake `story8` caught.
		//
		// `story8`'s lesson was that a TRANSIENT refusal must not preempt a permanent one: a backup
		// running on the default storage made quince say *wait for the backup* when waiting could
		// never help, because the storage was undeletable anyway. This refusal is PERMANENT — a
		// committed backup does not stop referencing a storage by itself — so ordering it ahead of
		// the declaration refusals costs a user nothing: both sentences are actionable, and neither
		// expires.
		//
		// And `config` must keep knowing nothing about the storage subsystem, which is the property
		// the liveness callback exists to preserve — *config gets a sentence, never a job*. A
		// versions count is that same kind of fact, one table over.
		if why := d.versionsBlockingForget(name); why != "" {
			writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
				Errors []wire.ConfigError `json:"errors"`
			}{Errors: []wire.ConfigError{{Path: "storage", Message: why}}})
			return
		}

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
		// THE ROW GOES TOO, AND THIS IS THE HALF THAT WAS MISSING (quince#1525). `ForgetStorage`
		// splices the entry out of `config.yml`; without this the `storages` row survives, and
		// `ResolveStorage` asks the DB first on every path — so a reachable path with no marker and
		// a known row is MISSING MEDIUM, and the path stays claimed by a storage nobody declares.
		//
		// AFTER the write, not before, and the ordering is load-bearing. The row is only ever
		// consulted for a name the config declares, so between the two statements it is already
		// unreachable. Deleting first would leave a declared entry with no row, which is the state
		// that PERMITS a creation moment — quince would silently mint a new identity for a storage
		// the user has not finished removing.
		//
		// A FAILURE HERE IS SURFACED, NEVER SWALLOWED. The forget itself has succeeded and the
		// config is written, so this cannot fail the request without lying about what happened —
		// but a silent miss puts the user back in exactly the state quince#1525 reports, with no
		// sign of it. It rides the `warnings` channel the response already carries.
		if err := d.Store.DeleteStorage(name); err != nil {
			d.Log.Error("storage forgotten but its row survived", "error", err, "storage", name)
			warns = append(warns, config.Warning{Message: fmt.Sprintf(
				"storage %q was removed from config.yml, but quince could not release its database "+
					"row: %v. The path stays claimed, so re-adding a storage under this name will "+
					"be refused as a missing medium. Retry the forget, or declare it under a "+
					"different name.", name, err)})
		}
		d.Log.Info("storage forgotten", "storage", name, "applier_warnings", len(applied))
		writeJSON(w, d.Log, http.StatusOK, d.configResponse(cfg2, warns, src))
	}
}

// versionsBlockingForget reports why a storage cannot be forgotten, or "" when nothing blocks it.
//
// `versions.storage_id` joins against a storage's marker UUID, so removing the row while backups
// reference it would orphan that join — silently detaching a real backup history from the disk
// holding it. A committed backup is data that cannot be regenerated, which is why this refuses
// rather than cascading.
//
// IT NAMES WHAT BLOCKS IT, which is the difference between a refusal a user can act on and a bare
// no (Operator, quince#940). Counts rather than version ids: a user with two hundred backups
// cannot act on two hundred identifiers, and *3 backups across 2 devices* is the fact that decides
// what they do next.
//
// A STORAGE WITH NO ID CANNOT HAVE VERSIONS, and that is a fact rather than an assumption — the id
// is written at the creation moment, so a nil one means quince has never reached the storage and
// nothing can have been committed to it. That is also the state a user most wants to forget, which
// is why it must not be blocked by a lookup that finds nothing.
//
// A READ THAT FAILS REFUSES, AND THIS FUNCTION FAILED OPEN UNTIL quince#1534's REVIEW. The comment
// here claimed the row deletion was "guarded by the same join in the other direction" — it is not.
// `versions.storage_id` is a plain `TEXT` column added by `ALTER TABLE` (`0006_storage.sql:43`)
// with NO `FOREIGN KEY`, so nothing downstream would have caught a delete that should not have
// happened, and the detachment would have been silent and permanent.
//
// The reasoning that produced it — *a database read that errors is not evidence that backups
// exist* — is true and is not the question: it is not evidence they do NOT exist either, and the
// two mistakes cost differently. Refusing wrongly costs a retry. Allowing wrongly detaches a real
// backup history from the disk holding it, unrecoverably as far as quince's record goes. Over an
// irreversible action on data that cannot be regenerated, "I could not check" must never read as
// "there is nothing to check".
//
// AND THE OBJECTION IS ANSWERED BY THE MESSAGE, NOT BY THE DECISION. *Refusing makes a transient
// failure look permanent* is a property of what the sentence says; one that names the failure as a
// failed check and asks for a retry is transient-shaped, honest about not knowing, and tells the
// user more than proceeding silently ever could.
func (d Deps) versionsBlockingForget(name string) string {
	// Named once: three call sites must not drift into three different explanations of one state.
	cannotCheck := fmt.Sprintf(
		"quince could not check whether any backups reference storage %q, so it has not forgotten "+
			"it — that check is the only thing standing between a forget and a backup history "+
			"detached from the disk holding it. Try again. If it keeps failing, quince cannot read "+
			"its own database and that is the problem to fix first; nothing on the disk is affected "+
			"either way.", name)

	// A nil Store cannot answer, and the delete below would panic on it besides.
	if d.Store == nil {
		d.Log.Error("cannot check versions before forget: no store", "storage", name)
		return cannotCheck
	}
	row, ok, err := d.Store.GetStorage(name)
	if err != nil {
		d.Log.Error("could not read storage row before forget", "error", err, "storage", name)
		return cannotCheck
	}
	// UNKNOWN and KNOWN-EMPTY are different, and only the second may proceed. No row means nothing
	// to delete; a nil id means quince never reached the storage, so nothing can reference it.
	if !ok || row.StorageID == nil {
		return ""
	}
	counts, err := d.Store.CountVersionsByStorage()
	if err != nil {
		d.Log.Error("could not count versions before forget", "error", err, "storage", name)
		return cannotCheck
	}
	c, ok := counts[*row.StorageID]
	if !ok || c.Backups == 0 {
		return ""
	}
	devices := "1 device"
	if c.Devices != 1 {
		devices = fmt.Sprintf("%d devices", c.Devices)
	}
	backups := "1 backup"
	if c.Backups != 1 {
		backups = fmt.Sprintf("%d backups", c.Backups)
	}
	return fmt.Sprintf(
		"storage %q still holds %s across %s, and forgetting it would detach them from the disk "+
			"they are on — quince would no longer know where those backups live. Restore or delete "+
			"them first, or leave the storage declared. Nothing on the disk is deleted either way.",
		name, backups, devices)
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
		// THE ENTRY, PLUS ONE SIBLING KEY. Embedding keeps the body a `StorageEntry` on the wire —
		// every existing field stays exactly where it was — while carrying the fingerprint of the ssh
		// key this screen showed, which is the only thing that can prove the save is committing the
		// key whose line the operator actually pasted (quince#1038).
		var body struct {
			config.StorageEntry
			ZFSKeyFingerprint string `json:"zfs_key_fingerprint"`
		}
		if err := decodeJSON(r, &body); err != nil {
			writeError(w, d.Log, http.StatusBadRequest, "bad_request", "invalid request body: "+err.Error())
			return
		}
		entry := body.StorageEntry

		// THE KEY MOVES BEFORE THE CONFIG IS WRITTEN, and the order is deliberate: a config naming a
		// storage whose key never landed is a storage that fails at its first backup, whereas a
		// failed move with nothing written is a screen the operator is still looking at.
		//
		// ONLY WHERE QUINCE MANAGES THE KEY. An explicit `ssh_key` is the operator's own, placed by
		// them, and no part of this has ever touched it.
		if entry.Backend == "zfs" && entry.ZFS.SSHKey == "" && entry.ZFS.ParentDataset != "" {
			if err := storage.CommitZFSKey(d.zfsKeyDir(), entry.ZFS.ParentDataset, body.ZFSKeyFingerprint); err != nil {
				if errors.Is(err, storage.ErrPendingKeyChanged) {
					// A 422 NAMING A FIELD, because the operator can act on it: the line they pasted
					// on the host is for a key quince no longer holds, so it has to be re-read and
					// re-pasted. The two-tab case, arriving honestly instead of as a storage that
					// fails at its first backup.
					writeJSON(w, d.Log, http.StatusUnprocessableEntity, struct {
						Errors []wire.ConfigError `json:"errors"`
					}{Errors: []wire.ConfigError{{Path: "zfs.parent_dataset", Message: "the ssh key this screen " +
						"showed is not the one quince holds now — another tab may have added a storage since. " +
						"Read the key again and paste the new line on the ZFS host."}}})
					return
				}
				d.Log.Error("committing the zfs key", "error", err)
				writeError(w, d.Log, http.StatusInternalServerError, "internal", "could not place the ssh key")
				return
			}
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
