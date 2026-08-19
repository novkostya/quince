package config

import (
	"fmt"
	"io"
	"strings"

	"github.com/novkostya/quince/core/internal/wire"
)

// RequireStorages is the qn.6c startup gate: quince refuses to serve without at least one
// declared storage.
//
// WHY THIS IS NOT A Validate() ERROR, which is the whole point of the file. Load() DISCARDS a
// config that fails Validate and returns Default() with OK:false, and NewService logs
// "running on last-good defaults" and carries on — never fatal, by design and by its own doc
// comment. Routing "no storages" through that path would produce a daemon that starts, logs a
// warning nobody reads, serves a healthy-looking UI, and cannot back anything up. That is the
// silent zero-storage start gap 3's ruling exists to prevent, so the check has to be somewhere
// that can stop the process.
//
// It is separate from Validate for a second reason: a PUT to /api/config that omits `storages:`
// must be a 422, not a process exit. Validate answers "is this document well-formed"; this
// answers "may this process serve".
//
// The idiom is deploy/runner/preflight's, ported rather than invented: name what was OBSERVED,
// say what follows from it, and print the exact thing to do — an error message is a claim.
type StorageRequirement struct {
	// Missing is true when `storage:` is absent from config.yml entirely.
	Missing bool
	// Empty is true when the key is present and declares no entries.
	Empty bool
	// LegacyEnv is true when a retired QUINCE_BACKUPS is still set in the environment. It
	// never causes the refusal — it explains it, which is the difference between a user who
	// forgot to declare a storage and one who upgraded into this and needs to know why the
	// thing that used to work stopped.
	LegacyEnv bool
	// LegacyEnvValue is that variable's value, echoed back so the remedy can suggest it.
	LegacyEnvValue string

	// Malformed is true when config.yml FAILED TO PARSE, so `Missing` is an artifact rather than
	// an observation (quince#508).
	//
	// WITHOUT THIS, NIL DOES DOUBLE DUTY AND THE REFUSAL LIES. `Parse` returns `Default()` on a
	// YAML error, `Default()` leaves `Storage` nil, and `CheckStorages` reads nil as *absent* — so
	// a file whose `storage:` key is plainly there, in the wrong SHAPE, was told it had no such
	// key. Measured against the staging stand's real pre-flatten config while rehearsing
	// quince#506's upgrade.
	//
	// It matters because this IS the upgrade path. quince#473 retired the nested shape and
	// `deploy/upgrading.md` tells operators to rewrite it, so the people who meet this message are
	// exactly those who upgraded before editing — and it tells them to add a key they can see. The
	// obvious reactions are both wrong: add a second `storage:` (a YAML duplicate-key error), or
	// conclude the file is not being read.
	Malformed bool
	// MalformedDetail is the parser's own sentence, which names the line and the type. It is the
	// thing the old message threw away while the information sat one log line above it.
	MalformedDetail string

	// Unreadable is true when config.yml EXISTS AND COULD NOT BE READ, so `Missing` would be an
	// artifact for the second time (quince#544). It is the branch quince#508 did not walk: that fix
	// taught this type to recognise a PARSE failure, and a READ failure fell through the same nil to
	// the same false sentence.
	//
	// IT IS ARGUABLY THE MORE MISLEADING OF THE TWO. A parse failure at least means quince read the
	// file; this one can mean quince never saw the operator's config AT ALL, and the message sent
	// them to edit a file the daemon cannot see. `Load` stats before reading, so this is never the
	// ordinary no-file-yet first run — it is a permission error, an I/O error, or a `/data` bind that
	// did not mount. That last one is a plausible container mistake rather than an exotic state.
	Unreadable bool
	// UnreadableDetail is the OS's own sentence — it names the path and the errno.
	UnreadableDetail string
}

// OK reports whether the process may serve.
func (r StorageRequirement) OK() bool {
	return !r.Missing && !r.Empty && !r.Malformed && !r.Unreadable
}

// declaredStorage counts the storages a document declares. Nil and empty both count zero — the
// distinction `StorageRequirement` keeps as `Missing` versus `Empty` is worth two different
// sentences to a user and nothing at all to a comparison.
//
// IT EXISTS FOR THE WRITE PATH'S TRANSITION CHECK and has exactly one caller (Operator ruling
// 2026-08-14, quince#908). Unexported deliberately: anything outside this package asking "how many
// storages" should read the list, and a helper that looks like a general accessor invites the
// transition check to be re-derived somewhere it does not belong.
func declaredStorage(c Config) int {
	if c.Storage == nil {
		return 0
	}
	return len(*c.Storage)
}

// CheckStorages evaluates the requirement. environ is os.Environ()-style; it is read ONLY to
// detect a retired variable for the explanation, never to resolve a path.
//
// `failure` is the load's own TYPED cause, and it is how a document quince could not use reaches
// this decision (quince#508, quince#544). It cannot be inferred from the Config: every failing load
// yields `Default()`, whose nil `Storage` is indistinguishable from a file that genuinely declares
// nothing. Passing nil is correct for a caller that did not load from disk — `Service.Replace`
// validates a document that already parsed, so there is no load failure it could be hiding.
//
// IT USED TO BE `warnings []Warning`, AND THAT WAS THE DEFECT quince#544 NAMES AS THE SHARPER HALF.
// This function recognised a parse failure by matching the prose prefix `"invalid YAML: "` off a
// Warning that `Load` composes — a string contract between two functions in the same package that
// NOTHING ASSERTED STAYS IN STEP. Reword the Warning and detection stops silently: the old false
// "no storage: key" message returns, with every test green. A typed cause cannot drift, and it made
// the READ branch expressible at the same time, which prose-matching never did.
//
// IT IS A STATIC PREDICATE OVER ONE DOCUMENT, AND IT STAYS ONE. That is half of the Operator's
// ruling of 2026-08-14 (quince#908) rather than an accident of how it was written. Its two callers
// ask different questions:
//
//	main.go     may this daemon SERVE?   static. At startup there is no previous document, and
//	                                     `qn.6e`'s onboarding state depends on this answering about
//	                                     the document as it stands.
//	service.go  may this WRITE land?     a transition — and the comparison lives THERE, not here.
//
// SO DO NOT MAKE THIS FUNCTION A REGRESSION CHECK. *"The storage requirement becomes a regression
// check"* is a sentence that reads as licence to edit this predicate, and doing so would silently
// delete the startup refusal `qn.6e` and quince#508 both rest on — a daemon booting on defaults with
// no storage and no error, which is the exact failure gap 3's ruling forbade.
func CheckStorages(c Config, environ []string, failure *LoadFailure) StorageRequirement {
	r := StorageRequirement{}
	// A LOAD FAILURE OUTRANKS EVERYTHING BELOW, because everything below is read off a Config that
	// the file did not produce. Reporting "no storage key" about `Default()` is reporting on a
	// document nobody wrote.
	if failure != nil {
		switch failure.Kind {
		case LoadUnparsable:
			r.Malformed, r.MalformedDetail = true, failure.Detail
		case LoadUnreadable:
			r.Unreadable, r.UnreadableDetail = true, failure.Detail
		}
	}
	if !r.Malformed && !r.Unreadable {
		switch {
		case c.Storage == nil:
			r.Missing = true
		case len(*c.Storage) == 0:
			r.Empty = true
		}
	}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "QUINCE_BACKUPS" {
			r.LegacyEnv, r.LegacyEnvValue = true, v
		}
	}
	return r
}

// Explain writes the refusal to w. It returns the short error the caller should return so
// main() exits non-zero; the detail goes to w because a single-line error cannot carry a
// remedy, and a remedy the user cannot read is not a remedy.
func (r StorageRequirement) Explain(w io.Writer, configPath string) error {
	if r.OK() {
		return nil
	}
	// The write is deliberately unchecked: this runs on the way out of a refusal, and a failure
	// to write the explanation must not replace the exit code that is the actual refusal.
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(w, "quince: "+format+"\n", a...) }

	switch {
	case r.Unreadable:
		// NOT REACHED BY THE DAEMON TODAY, AND KEPT DELIBERATELY (quince#544). `main.go` calls Explain
		// only on `Malformed`, because `deploy/storageless-smoke`'s third arm requires an unreadable
		// config to SERVE — the add endpoint must be reachable so it can refuse with 422 (quince#852).
		// This branch printed at startup for exactly one CI run before that arm refuted it.
		//
		// IT IS A GUARD RATHER THAN DEAD CODE, and the distinction is which sentence a future caller
		// gets. `Unreadable` makes `OK()` false, so any NEW caller of Explain — a CLI, a preflight —
		// lands in this switch; without this arm it falls to `default:` and prints *"`storage:` …
		// declares no storages"*, which is the exact false claim quince#544 exists to remove, from a
		// path added later by somebody who never read the issue.
		//
		// THE OS'S OWN SENTENCE, for the same reason the parser's is kept below: it names the path
		// and the errno, and nothing this function knows improves on it.
		//
		// AND THE REMEDY BLOCK BELOW IS SKIPPED. "Add this to config.yml, then start again" is advice
		// quince cannot honour about a file it cannot open.
		p("%s could not be READ — quince cannot tell what storage you declared.", configPath)
		p("")
		p("    %s", r.UnreadableDetail)
		p("")
		p("The file EXISTS — quince found it and then failed to open it — so this is a permission")
		p("problem, an I/O error, or a mount that is not there. In a container the usual cause is a")
		p("`/data` bind that did not mount: check the volume before editing anything.")
		p("")
		p("quince cannot act on a config it cannot read, so nothing it declares is in effect.")
		return fmt.Errorf("config could not be read: %s: %s", configPath, r.UnreadableDetail)
	case r.Malformed:
		// THE PARSER'S OWN SENTENCE, because it names the line and the type and nothing this
		// function knows can improve on it (quince#508). Saying "no storage key" here — which is
		// what nil-as-absent produced — tells the operator to add a key they can see in the file.
		p("%s could not be parsed — quince cannot tell what storage you declared.", configPath)
		p("")
		p("    %s", r.MalformedDetail)
		p("")
		p("`storage:` IS THE LIST ITSELF — no `storages:` wrapper and no global `backend`, `zfs` or")
		p("`retention`. An older file written in the previous shape parses as that shape and fails")
		p("here. deploy/upgrading.md has the before/after.")
	case r.Missing:
		p("no `storage:` key in %s — quince does not know where to keep backups.", configPath)
	default:
		p("`storage:` in %s declares no storages — quince does not know where to keep backups.", configPath)
	}

	suggested := "/backups"
	if r.LegacyEnv {
		p("")
		p("QUINCE_BACKUPS is set to %q and is NO LONGER READ. Every", r.LegacyEnvValue)
		p("storage is now declared in config.yml, so nothing is implied by the environment.")
		p("That variable is almost certainly why this used to work and has now stopped.")
		if strings.TrimSpace(r.LegacyEnvValue) != "" {
			suggested = r.LegacyEnvValue
		}
	}

	p("")
	p("Add this to %s, then start again:", configPath)
	p("")
	p("    storage:")
	p("      - path: %s", suggested)
	p("")
	p("The path must already exist and hold your backups if you have any; quince will adopt")
	p("what it finds there. With ONE storage that is the whole of it — `name` defaults to the")
	p("path and `default: true` is implied. Declare both only when you add a second storage.")
	p("")
	p("REFUSING to start. A quince that comes up with nowhere to put backups looks healthy and")
	p("silently protects nothing, which is worse than one that did not start.")

	switch {
	case r.Malformed:
		// The short error main() exits on must not say "no storage declared" either — that is the
		// same false claim, one line shorter.
		return fmt.Errorf("config could not be parsed: %s: %s", configPath, r.MalformedDetail)
	case r.Missing:
		return fmt.Errorf("no storage declared: %s has no storage: key", configPath)
	}
	return fmt.Errorf("no storage declared: storage: in %s is empty", configPath)
}

// CheckStorageBackends refuses configurations where two storages would write to the same place
// (qn.6c, quince#458).
//
// zfs is the case that needs it: a zfs backend creates `<parent_dataset>/<udid>`, so two storages
// on one parent dataset would create THE SAME dataset for a device and each believe it owned it.
// That is not a degraded mode to surface — it is two storages that are one storage, and every
// guarantee this rung added about per-storage attribution is void beneath it.
//
// Namespace backends need no equivalent: they are rooted at their own paths, which
// `CheckStorages` already requires to be distinct.
//
// THIS FUNCTION SURVIVED THE FLATTENING, and quince#473's deletion list was wrong to include it.
// The collision above is NOT caused by inheritance: two fully-specified entries can each spell out
// the same `parent_dataset`. What the flattening removed is the REMEDY BRANCHING — quince#468 and
// quince#492 split the advice three ways because a zfs backend could arrive by inheritance, by a
// global, or by an entry's own block, and naming the wrong one sent an operator to a key they
// never wrote. With every entry self-contained there is exactly one key it can mean, so the
// three-way split is deleted and the refusal keeps its subject.
//
// Returns one message per collision, empty when the config is coherent.
//
// THIS IS THE STARTUP RENDERING. Its structured twin is CheckStorageBackendErrors, which the write
// path uses; both are built from checkStorageBackendProblems so the two refusals cannot describe the
// same config differently. main.go prints these to stderr, where a NAME is more use to a human than
// an index — which is why the string form survived rather than being replaced (quince#683).
func CheckStorageBackends(storages *[]StorageEntry) []string {
	probs := checkStorageBackendProblems(storages)
	if len(probs) == 0 {
		return nil
	}
	out := make([]string, 0, len(probs))
	for _, p := range probs {
		out = append(out, p.Message)
	}
	return out
}

// StorageBackendProblem is one incoherent storage declaration, located.
//
// The INDEX is what the string form could not carry, and it is what a form needs: `wire.ConfigError`
// is keyed on a path like `storage[1].zfs.parent_dataset`, and a client highlights the field it
// names. Both renderings below are built from this, so the startup refusal and the save refusal
// cannot drift into describing the same config differently.
type StorageBackendProblem struct {
	Index   int    // which storage[i]
	Field   string // the key within the entry, e.g. "zfs.parent_dataset"
	Message string
}

// checkStorageBackendProblems is the single source of truth behind CheckStorageBackends and
// CheckStorageBackendErrors. See CheckStorageBackends for the invariant and why it exists.
func checkStorageBackendProblems(storages *[]StorageEntry) []StorageBackendProblem {
	if storages == nil {
		return nil
	}
	type claim struct {
		name  string
		index int
	}
	seen := map[string]claim{} // parent dataset → the storage that claimed it
	var out []StorageBackendProblem
	for i, e := range *storages {
		// `SSHConfigured` rather than `HookCmd != ""` since quince#818: the transport is the four
		// `ssh_*` keys now, and `hook_cmd` is a REFUSED key rather than a signal of zfs intent — a
		// document carrying it never reaches here, because `Validate` discards it first.
		isZFS := e.Backend == "zfs" ||
			(e.Backend == "auto" && (e.ZFS.ParentDataset != "" || e.ZFS.SSHConfigured()))
		if isZFS && e.ZFS.ParentDataset == "" {
			out = append(out, StorageBackendProblem{Index: i, Field: "zfs.parent_dataset", Message: fmt.Sprintf(
				"storage %q resolves to the zfs backend but has no `zfs.parent_dataset` — set it in "+
					"that storage's own `zfs:` block, or give the storage a namespace backend "+
					"(`backend: reflink`, `hardlink` or `copy`)",
				e.Name)})
			continue
		}
		if !isZFS {
			continue
		}
		// A ZFS STORAGE WITH NO TRANSPORT CANNOT REACH ITS POOL (quince#818), and this sits beside
		// the parent-dataset check rather than in `Validate` for the reason that check already
		// gives: a `Validate` failure discards the document, where this returns the error and writes
		// nothing. Same class of problem, same place, same shape of message.
		//
		// USER AND HOST ONLY. `ssh_port` and `ssh_key` default, so their absence says nothing.
		if !e.ZFS.SSHConfigured() {
			// `Field` MOVES WITH `missing`, and hardcoding it was a real defect rather than a
			// tidiness one: this type's own comment says *"a client highlights the field it
			// names"*, so an operator who set `ssh_host` and omitted `ssh_user` got a message
			// about the user while the form highlighted the host they had filled in correctly —
			// `qn.6g`'s rule broken by the code that cites it two lines up.
			var missing, field string
			switch {
			case e.ZFS.SSHHost != "":
				missing, field = "`zfs.ssh_user`", "zfs.ssh_user"
			case e.ZFS.SSHUser != "":
				missing, field = "`zfs.ssh_host`", "zfs.ssh_host"
			default:
				// BOTH MISSING: point at the host. It is the first field on the form and the one
				// with no plausible default, so it is where the operator starts either way.
				missing, field = "`zfs.ssh_user` and `zfs.ssh_host`", "zfs.ssh_host"
			}
			out = append(out, StorageBackendProblem{Index: i, Field: field, Message: fmt.Sprintf(
				"storage %q resolves to the zfs backend but has no %s — quince reaches a ZFS pool "+
					"over ssh to the constrained helper, and the forced command in that user's "+
					"`authorized_keys` is what bounds what quince can do on the host",
				e.Name, missing)})
			continue
		}
		if first, dup := seen[e.ZFS.ParentDataset]; dup {
			out = append(out, StorageBackendProblem{Index: i, Field: "zfs.parent_dataset", Message: fmt.Sprintf(
				"storages %q and %q are both zfs on parent dataset %q (also storage[%d]) — they would "+
					"create the same dataset per device and each believe it owned it. Give one of "+
					"them its own `zfs.parent_dataset`, or a namespace backend",
				first.name, e.Name, e.ZFS.ParentDataset, first.index)})
			continue
		}
		seen[e.ZFS.ParentDataset] = claim{name: e.Name, index: i}
	}
	return out
}

// CheckStorageBackendErrors is CheckStorageBackends for a WRITE path — the same problems, located
// and shaped as the `{errors: [{path, message}]}` every config refusal already uses.
//
// RULED 2026-08-07 (quince#683): this check belongs in `replaceLocked`, beside `CheckStorages`, and
// NOT in `Validate`. `Load()` DISCARDS a config that fails `Validate` and falls back to `Default()`,
// so putting it there would turn a named, actionable refusal — this dataset, these two storages —
// into a daemon running on defaults, which is quince#508's defect in a new guise. `Replace` has the
// opposite property: it returns the errors and writes NOTHING, so the hazard that justifies the
// exclusion is absent from this path. That is the same argument, word for word, that `CheckStorages`
// already carries in `replaceLocked`.
//
// The startup call in main.go is untouched and keeps its own rendering: a config that was
// hand-edited into a collision still refuses to start, naming the dataset.
func CheckStorageBackendErrors(storages *[]StorageEntry) []wire.ConfigError {
	probs := checkStorageBackendProblems(storages)
	if len(probs) == 0 {
		return nil
	}
	out := make([]wire.ConfigError, 0, len(probs))
	for _, p := range probs {
		out = append(out, wire.ConfigError{
			Path:    fmt.Sprintf("storage[%d].%s", p.Index, p.Field),
			Message: p.Message,
		})
	}
	return out
}
