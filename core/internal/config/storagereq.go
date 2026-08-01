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
	// Missing is true when `storage.storages:` is absent from config.yml entirely.
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
}

// OK reports whether the process may serve.
func (r StorageRequirement) OK() bool { return !r.Missing && !r.Empty }

// CheckStorages evaluates the requirement. environ is os.Environ()-style; it is read ONLY to
// detect a retired variable for the explanation, never to resolve a path.
func CheckStorages(c Config, environ []string) StorageRequirement {
	r := StorageRequirement{}
	switch {
	case c.Storage.Storages == nil:
		r.Missing = true
	case len(*c.Storage.Storages) == 0:
		r.Empty = true
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

	if r.Missing {
		p("no `storage.storages:` key in %s — quince does not know where to keep backups.", configPath)
	} else {
		p("`storage.storages:` in %s declares no storages — quince does not know where to keep backups.", configPath)
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
	p("      storages:")
	p("        - name: local")
	p("          path: %s", suggested)
	p("          default: true")
	p("")
	p("The path must already exist and hold your backups if you have any; quince will adopt")
	p("what it finds there. `default: true` marks the storage a backup goes to when none is named.")
	p("")
	p("REFUSING to start. A quince that comes up with nowhere to put backups looks healthy and")
	p("silently protects nothing, which is worse than one that did not start.")

	if r.Missing {
		return fmt.Errorf("no storage declared: %s has no storage.storages key", configPath)
	}
	return fmt.Errorf("no storage declared: storage.storages in %s is empty", configPath)
}

// CheckStorageBackends refuses configurations where two storages would write to the same place
// (qn.6c, quince#458).
//
// zfs is the case that needs it: a zfs backend creates `<parent_dataset>/<udid>`, so two storages
// inheriting one global `storage.zfs` block would create THE SAME dataset for a device and each
// believe it owned it. That is not a degraded mode to surface — it is two storages that are one
// storage, and every guarantee this rung added about per-storage attribution is void beneath it.
//
// Namespace backends need no equivalent: they are rooted at their own paths, which
// `CheckStorages` already requires to be distinct.
//
// Returns one message per collision, empty when the config is coherent.
func CheckStorageBackends(c StorageConfig) []string {
	if c.Storages == nil {
		return nil
	}
	seen := map[string]string{} // parent dataset → the storage that claimed it
	var out []string
	for _, e := range *c.Storages {
		z := c.ZFSFor(e)
		isZFS := c.BackendFor(e) == "zfs" ||
			(c.BackendFor(e) == "auto" && (z.ParentDataset != "" || z.HookCmd != ""))
		if isZFS && z.ParentDataset == "" {
			// A ZFS BACKEND WITH NO PARENT DATASET is not a degraded mode, it is an incoherent
			// declaration: `Select` would build a zfs backend with nothing to create datasets
			// under. It is reachable the obvious way — an entry writes `zfs: {}` to say "I am not
			// zfs" while the GLOBAL `backend: zfs` still applies to it, which is the first thing
			// anyone tries and what my own first guidance on quince#458 told the Operator to do.
			// wrote `zfs: {}` to opt OUT with one whose OWN block is incoherent — a hook and no
			// parent — and sent the second to `backend: auto`, which would not fix it.
			remedy := "set `storage.zfs.parent_dataset`"
			switch {
			case e.ZFS != nil && *e.ZFS != (ZFSConfig{}):
				remedy = "this storage has its own `zfs:` block with no `parent_dataset` — set one " +
					"there, or use `zfs: {}` with `backend: auto` to make it a non-zfs storage"
			case e.ZFS != nil:
				remedy = "an entry that sets `zfs: {}` to opt OUT must also set `backend: auto` " +
					"(or a namespace backend), because the global `backend: zfs` still applies to it"
			}
			out = append(out, fmt.Sprintf(
				"storage %q resolves to the zfs backend but has no `zfs.parent_dataset` — %s",
				e.Name, remedy))
			continue
		}
		if !isZFS {
			continue
		}
		if first, dup := seen[z.ParentDataset]; dup {
			out = append(out, fmt.Sprintf(
				"storages %q and %q are both zfs on parent dataset %q — they would create the same "+
					"dataset per device and each believe it owned it. Give one of them its own "+
					"`zfs.parent_dataset`, or `zfs: {}` to make it a non-zfs storage",
				first, e.Name, z.ParentDataset))
			continue
		}
		seen[z.ParentDataset] = e.Name
	}
	return out
}
