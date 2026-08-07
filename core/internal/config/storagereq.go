package config

import (
	"fmt"
	"io"
	"strings"
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
}

// OK reports whether the process may serve.
func (r StorageRequirement) OK() bool { return !r.Missing && !r.Empty && !r.Malformed }

// CheckStorages evaluates the requirement. environ is os.Environ()-style; it is read ONLY to
// detect a retired variable for the explanation, never to resolve a path.
//
// `warnings` are the load's own, and they are how a PARSE FAILURE reaches this decision
// (quince#508). It cannot be inferred from the Config: a failed parse yields `Default()`, whose
// nil `Storage` is indistinguishable from a file that genuinely declares nothing. Passing nil is
// correct for a caller that did not load from disk — `Service.Replace` validates a document that
// already parsed, so there is no parse failure it could be hiding.
func CheckStorages(c Config, environ []string, warnings []Warning) StorageRequirement {
	r := StorageRequirement{}
	// THE PARSE FAILURE OUTRANKS EVERYTHING BELOW, because everything below is read off a Config
	// that parsing did not produce. Reporting "no storage key" about `Default()` is reporting on a
	// document nobody wrote.
	for _, w := range warnings {
		if detail, found := strings.CutPrefix(w.Message, "invalid YAML: "); found {
			r.Malformed, r.MalformedDetail = true, detail
			break
		}
	}
	if !r.Malformed {
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
	case r.Malformed:
		// THE PARSER'S OWN SENTENCE, because it names the line and the type and nothing this
		// function knows can improve on it (quince#508). Saying "no storage key" here — which is
		// what nil-as-absent produced — tells the operator to add a key they can see in the file.
		p("%s could not be parsed — quince cannot tell what storage you declared.", configPath)
		p("")
		p("    %s", r.MalformedDetail)
		p("")
		p("`storage:` CHANGED SHAPE in qn.6c: it IS the list now, with no `storages:` wrapper and")
		p("no global `backend`, `zfs` or `retention`. If this file predates that, it parses as the")
		p("old shape and fails here. deploy/upgrading.md has the before/after.")
	case r.Missing:
		p("no `storage:` key in %s — quince does not know where to keep backups.", configPath)
	default:
		p("`storage:` in %s declares no storages — quince does not know where to keep backups.", configPath)
	}

	suggested := "/backups"
	if r.LegacyEnv {
		p("")
		p("QUINCE_BACKUPS is set to %q and is NO LONGER READ. It was retired in qn.6c: every", r.LegacyEnvValue)
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
func CheckStorageBackends(storages *[]StorageEntry) []string {
	if storages == nil {
		return nil
	}
	seen := map[string]string{} // parent dataset → the storage that claimed it
	var out []string
	for _, e := range *storages {
		// `auto` still resolves to zfs when zfs intent is declared — interface fact 4: zfs intent
		// is config-side and never probed. Keeping that here is what makes `auto` legal without
		// making it a hole. `auto` is ABSORBED rather than removed (quince#502, Operator ruling
		// 2026-08-07): the loader is unchanged and the add flow writes a concrete backend, so
		// quince never writes `auto` and a human still may.
		//
		// THIS PREDICATE IS DUPLICATED, and semantically rather than literally: storage.WantZFS is
		// the same rule spelled with BackendZFS where this spells "zfs", so a grep for either
		// spelling finds only one of them. That is worse than a literal copy, not better. It is not
		// collapsed here because the two packages deliberately do not import each other — storage's
		// Options exists precisely so that package need not import config — and reversing that edge
		// is a layering decision, not a tidy-up. qn.6e PR 2 factored the storage side and stopped
		// at the boundary on purpose.
		isZFS := e.Backend == "zfs" ||
			(e.Backend == "auto" && (e.ZFS.ParentDataset != "" || e.ZFS.HookCmd != ""))
		if isZFS && e.ZFS.ParentDataset == "" {
			// A ZFS BACKEND WITH NO PARENT DATASET is not a degraded mode, it is an incoherent
			// declaration: `Select` would build a zfs backend with nothing to create datasets
			// under. ONE remedy now, because there is one key it could be.
			out = append(out, fmt.Sprintf(
				"storage %q resolves to the zfs backend but has no `zfs.parent_dataset` — set it in "+
					"that storage's own `zfs:` block, or give the storage a namespace backend "+
					"(`backend: reflink`, `hardlink` or `copy`)",
				e.Name))
			continue
		}
		if !isZFS {
			continue
		}
		if first, dup := seen[e.ZFS.ParentDataset]; dup {
			out = append(out, fmt.Sprintf(
				"storages %q and %q are both zfs on parent dataset %q — they would create the same "+
					"dataset per device and each believe it owned it. Give one of them its own "+
					"`zfs.parent_dataset`, or a namespace backend",
				first, e.Name, e.ZFS.ParentDataset))
			continue
		}
		seen[e.ZFS.ParentDataset] = e.Name
	}
	return out
}
