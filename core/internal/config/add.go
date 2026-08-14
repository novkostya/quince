package config

import (
	"fmt"
	"path/filepath"

	"github.com/novkostya/quince/core/internal/wire"
)

// AddOutcome mirrors ForgetOutcome. Two states rather than three: there is no "no such storage" to
// report when the whole point is that it does not exist yet.
type AddOutcome int

const (
	// AddDone — the entry is in config.yml and the appliers have run.
	AddDone AddOutcome = iota
	// AddRefused — the document would be invalid or incoherent; nothing was written.
	AddRefused
)

// AddStorage splices ONE storage into config.yml, server-side.
//
// IT IS FORGET'S MIRROR, and it is a narrow endpoint rather than a full-document PUT for the
// identical reason (contracts §1, qn.6d gap B): splicing server-side CANNOT drop a sibling entry's
// `zfs:` or `retention:` keys, and a client that reconstructs the list rather than splicing a
// fetched one silently resets every key it did not render. No UI surface renders `zfs:` or
// `retention:`, so a full-document PUT from the add form would quietly erase them.
//
// WHAT IT REFUSES, and the order is inherited rather than re-decided:
//
//  1. The DECLARATION rules, here — a name or path that already exists, and a path that is not
//     absolute. These are refused before anything else because they are about the entry the caller
//     just typed, and naming the offending field is the only actionable answer.
//  2. Everything `replaceLocked` already enforces — `Validate`, `CheckStorages`, and (quince#683)
//     `CheckStorageBackendErrors`. THE ADD PATH ADDS NO COPY OF THOSE. It writes through
//     `replaceLocked`, so it inherits them; two call sites for one invariant is how they diverge.
//
// A CONCRETE BACKEND IS THE CALLER'S JOB, not this function's. `auto` is ABSORBED rather than
// removed (quince#502, Operator ruling 2026-08-07): the loader still defaults it, so `auto` remains
// legal in a hand-written file and the one-line declaration the startup refusal teaches still works.
// What changed is that quince never WRITES it — the form sends the concrete backend it just showed,
// and `validateAddition` refuses an empty one so an omission cannot become an `auto` by the back
// door.
func (s *Service) AddStorage(entry StorageEntry) (AddOutcome, []wire.ConfigError, []Warning, error) {
	// THE WHOLE READ-MODIFY-WRITE IS UNDER writeMu, exactly as ForgetStorage explains: reading the
	// list, appending and writing are three steps, and without the lock two concurrent adds both
	// read the same list and the second write silently drops the first entry while both callers get
	// a 200. replaceLocked below, never Replace, which would deadlock on this same mutex.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	// REFUSED BEFORE ANYTHING ELSE WHEN THE FILE ON DISK COULD NOT BE READ (Operator ruling
	// 2026-08-12, quince#852). This is the only refusal here that is about the SERVER'S state rather
	// than the caller's entry, which is why it runs first: nothing the caller could type makes it
	// right, and reporting a field error for it would name the wrong thing.
	if errs := s.refuseIfConfigDiscarded(); len(errs) > 0 {
		return AddRefused, errs, nil, nil
	}

	cur := s.Current()

	if errs := validateAddition(entry, cur.Storage); len(errs) > 0 {
		return AddRefused, errs, nil, nil
	}

	// A NEW SLICE, never an append into the live one: Current() hands back a Config whose Storage
	// pointer aliases the snapshot other readers hold, and append may write through it in place.
	var existing []StorageEntry
	if cur.Storage != nil {
		existing = *cur.Storage
	}
	next := cur
	list := make([]StorageEntry, 0, len(existing)+1)
	list = append(list, existing...)
	list = append(list, entry)

	// RESOLVED AS A LIST, NOT ENTRY BY ENTRY, and that is the whole of the first-storage fix.
	//
	// This appended `entry.Resolved()`, which fills a single entry's own defaults — name, backend,
	// zfs mode, retention. It does NOT apply the one rule that is a property of the LIST: exactly
	// one storage is default, and it is IMPLIED when there is exactly one. That lives in
	// ResolveStorages, which runs at PARSE time, so a list assembled in memory never received it.
	//
	// THE CONSEQUENCE WAS THAT THE FIRST STORAGE COULD NEVER BE ADDED. Against an empty config the
	// new list is a single entry with `Default: false`, and validateStorages refuses it: "exactly
	// one storage must be marked `default: true`". Every existing test seeded a storage first, so
	// all of them added a SECOND one — where an existing default already satisfies the rule — and
	// none exercised the case the whole first-run path is made of.
	//
	// Found by running a real storageless container rather than by reading, which is the only
	// reason it did not ship (qn.6e).
	next.Storage = ResolveStorages(&list)

	// THE ROUTE NAMES ITSELF, and that holds because this method has exactly ONE caller
	// (quince#967). A second caller would make this constant a lie; it should take a `source`
	// parameter at that point rather than keep a name that used to be true.
	errs, warns, err := s.replaceLocked(next, SourceAddStorage)
	switch {
	case err != nil:
		return AddRefused, nil, nil, err
	case len(errs) > 0:
		return AddRefused, errs, nil, nil
	}
	return AddDone, nil, warns, nil
}

// refuseIfConfigDiscarded is the ruled guard on quince#852: an operation must not silently destroy
// the operator's declaration.
//
// WHAT IT PREVENTS, measured rather than reasoned (2026-08-12). With a `config.yml` carrying the
// retired `storage.zfs.mode: exec`, `Load` discards the document and `Current()` is `Default()` with
// no storage. One `POST /api/config/storage` then returned `200` and left the file holding ONLY the
// new entry — the zfs storage, its parent dataset and its hook command gone, no undo, and
// `warnings: []` in the response so every surface afterwards reported health.
//
// IT REFUSES RATHER THAN PRESERVING, and that was ruled against the two alternatives. A UI
// confirmation is a guard on the browser and `curl` walks around it, leaving the destructive path
// reachable by everything that is not the UI. Preserving the unparseable entries through the splice
// would have quince write back keys its own loader could not validate — a larger promise than this
// endpoint should make, and it produces a file that can be rewritten while containing something the
// daemon refuses to run.
//
// THE REFUSAL NAMES THE LINE, and that is a requirement rather than a courtesy: `qn.6g` ruled that a
// remedy the user cannot follow is the same defect as a silent failure, so "config invalid" with no
// path would be this bug in a politer form. Everything needed is in hand — `Load` reported exactly
// which key it choked on, and `GET /api/config` already serves the operator's own `file_text`
// beside it.
//
// THE REMEDY NAMES A RESTART because there is no reload path: `Load` runs at construction and
// nothing re-reads the file (quince#727). Editing `config.yml` is therefore not enough on its own,
// and a remedy that leaves the operator pressing the same button again would be the wrong half of
// the answer.
func (s *Service) refuseIfConfigDiscarded() []wire.ConfigError {
	s.mu.RLock()
	discarded := s.discarded
	errs := s.loadErrs
	warns := s.warnings
	path := s.path
	s.mu.RUnlock()

	// THE CONDITION IS THE DISCARD, NOT THE ERROR LIST. This read `len(errs) == 0` when quince#852
	// first shipped, and that covers ONE of `Load`'s three discard paths: an unreadable file and
	// invalid YAML both return `OK: false` with `Errors` EMPTY. Measured on a real container — an
	// unreadable `config.yml` serves with `errors=0`, and the add went straight through this guard
	// to the write.
	if !discarded {
		return nil
	}

	// THE DETAIL FALLS BACK TO `warnings`, WHICH IS WHERE THE CAUSE ALWAYS IS. The validation path
	// copies every error into `Warnings` in an explicit loop; the read-failure and invalid-YAML
	// paths write their own sentence there and set no errors at all. `Errors` is preferred where it
	// exists so the validator's own wording survives, and this fallback is what lets the refusal
	// name a line on the two paths that have none.
	if len(errs) == 0 {
		for _, w := range warns {
			errs = append(errs, wire.ConfigError{Path: w.Path, Message: w.Message})
		}
	}
	// A DISCARD WITH NOTHING TO SAY IS STILL A REFUSAL. Unreachable through `Load` as written —
	// every discard path records its cause — but returning nil here would silently restore the
	// destructive write, so the floor is a message rather than a hole.
	if len(errs) == 0 {
		return []wire.ConfigError{{
			Path: path,
			Message: fmt.Sprintf("quince could not read %s, so it is running on defaults — adding a "+
				"storage now would REPLACE that file and lose what it declares. Fix %s and restart "+
				"quince, then add the storage.", path, path),
		}}
	}

	// THE FIRST ERROR IS THE ONE NAMED, and the count carries the rest. A caller adding one storage
	// renders one sentence; listing every fault of a file they are about to open in an editor is
	// less useful than telling them where to start and that there is more.
	first := errs[0]
	more := ""
	if len(errs) > 1 {
		more = fmt.Sprintf(" (and %d other problem(s) in the same file)", len(errs)-1)
	}
	return []wire.ConfigError{{
		// KEYED ON THE OFFENDING CONFIG PATH, not on a form field, because no form field is wrong.
		// A client that highlights by path will match nothing and fall back to showing the message,
		// which is the correct behaviour here.
		Path: first.Path,
		Message: fmt.Sprintf(
			"quince could not read %s, so it is running on defaults — adding a storage now would "+
				"REPLACE that file and lose what it declares. The problem is %s: %s%s. Fix that line "+
				"in %s and restart quince, then add the storage.",
			path, first.Path, first.Message, more, path),
	}}
}

// validateAddition reports what is wrong with THE ENTRY BEING ADDED, keyed on the field a form
// would highlight.
//
// It deliberately re-asks two questions `Validate` also asks — duplicate name and duplicate path.
// That is NOT the duplication the comment above warns against: `Validate` reports them against the
// whole document as `storage[i].name`, where `i` is the index in the merged list, and the caller
// adding one entry has no way to know which index is theirs. Here the path is `name` or `path`,
// naming the field of the thing they just typed. Same rule, addressed to a different reader — and
// `replaceLocked` still runs the document-wide version underneath, so nothing rests on this being
// complete.
func validateAddition(e StorageEntry, existing *[]StorageEntry) []wire.ConfigError {
	var errs []wire.ConfigError
	add := func(path, msg string) { errs = append(errs, wire.ConfigError{Path: path, Message: msg}) }

	switch {
	case e.Path == "":
		add("path", "must not be empty")
	case !filepath.IsAbs(e.Path):
		add("path", fmt.Sprintf("must be an absolute path, got %q — and it must be the path INSIDE "+
			"the container, which is what quince can reach", e.Path))
	}

	// EMPTY IS REFUSED RATHER THAN DEFAULTED. Resolved() would turn it into `auto`, and the whole
	// point of the add flow is that quince writes the concrete backend it just showed the user
	// (quince#502). An omission here is a client bug, and defaulting it would hide one.
	switch e.Backend {
	case "zfs", "reflink", "hardlink", "copy":
	case "":
		add("backend", "must be set to the backend quince showed you — an added storage records a "+
			"concrete backend, never `auto`")
	default:
		add("backend", fmt.Sprintf("invalid value %q; must be one of [zfs reflink hardlink copy]", e.Backend))
	}

	if existing != nil {
		// The name defaults to the path (schema.go), so compare on the RESOLVED name — otherwise an
		// entry with no name collides with an existing one named after the same path and nothing
		// here notices, leaving `Validate` to report it against an index the caller cannot map.
		name := e.Resolved().Name
		clean := filepath.Clean(e.Path)
		for _, x := range *existing {
			if x.Name == name {
				add("name", fmt.Sprintf("a storage named %q is already declared — names key a "+
					"storage's DB row, so they must be unique", name))
				break
			}
		}
		for _, x := range *existing {
			if e.Path != "" && filepath.Clean(x.Path) == clean {
				add("path", fmt.Sprintf("storage %q is already declared at %q — two storages at one "+
					"path would each claim the other's identity marker", x.Name, x.Path))
				break
			}
		}
	}

	// DEFAULT IS NOT ASKED FOR AND NOT ACCEPTED HERE. The first storage is default by implication
	// (ResolveStorages marks a lone entry), and a LATER one must not steal it — so an add that set
	// `default: true` would silently re-point every backup that names no storage. Re-designation is
	// a separate act on an existing storage, and this rung does not build it.
	if e.Default {
		add("default", "an added storage cannot claim `default` — the first storage is default "+
			"implicitly, and changing which storage is default is a separate edit")
	}
	return errs
}
