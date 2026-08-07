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
	list = append(list, entry.Resolved())
	next.Storage = &list

	errs, warns, err := s.replaceLocked(next)
	switch {
	case err != nil:
		return AddRefused, nil, nil, err
	case len(errs) > 0:
		return AddRefused, errs, nil, nil
	}
	return AddDone, nil, warns, nil
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
