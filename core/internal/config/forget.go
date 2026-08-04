package config

import (
	"fmt"

	"github.com/novkostya/quince/core/internal/wire"
)

// ForgetOutcome is which of the three answers DELETE /api/config/storage/{name} gives
// (contracts §2, gap B ruling 2026-08-03): 200, 404 or 422.
//
// An enum rather than a (bool, error) pair because the three cases are not degrees of failure:
// "no such storage" is a fact about the request, "this one is the default" is a refusal with a
// remedy, and a write failure is the server's problem. A caller that collapsed the first two
// would answer 404 to a user who asked to forget the only disk they have, which tells them the
// storage does not exist rather than why quince will not do it.
type ForgetOutcome int

const (
	// ForgetDone means the entry is gone from config.yml and the live snapshot.
	ForgetDone ForgetOutcome = iota
	// ForgetNoSuchStorage means no declared storage carries that name → 404.
	ForgetNoSuchStorage
	// ForgetRefused means the declaration would be incoherent → 422, with Errors saying why.
	ForgetRefused
)

// ForgetStorage removes one storage from the declaration, addressed by its config `name`.
//
// DETACH-AND-FORGET: this deletes one config line and NOTHING on the disk. The versions
// attributed to the storage keep their rows — `Version.storage_id` is nullable-with-meaning and
// nothing here touches it — and the tree under the storage's path is not opened, let alone
// written. G5 asserts that on the tree rather than on this function's return value.
//
// WHY THIS EXISTS RATHER THAN A PUT TO /api/config, which already works. `handleConfigPut`
// decodes into a ZERO-VALUED Config, so a client that reconstructs the storage list from what it
// rendered — rather than splicing a list it fetched — silently drops every surviving entry's
// `zfs:` and `retention:` keys. Those are exactly the keys no storage card renders, so the UI
// could not round-trip them even in principle. Splicing HERE, server-side, over the live parsed
// config, means the survivors are the same values that were loaded; G5b pins that.
//
// It is NOT a live deregistration. `storage.Manager`'s slot list is fixed at construction
// (only reachability moves), so the process keeps serving the disk until it restarts. That is the
// ruled behaviour, not a shortcut: the caller SURFACES the restart through the `warnings` channel,
// and `POST /api/storages/{name}/recheck` keeps answering for the slot still being served, which
// is runtime truth. Project-wide config→runtime propagation is its own rung (quince#577).
func (s *Service) ForgetStorage(name string) (ForgetOutcome, []wire.ConfigError, []Warning, error) {
	cur := s.Current()
	if cur.Storage == nil {
		return ForgetNoSuchStorage, nil, nil, nil
	}

	idx := -1
	for i, e := range *cur.Storage {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return ForgetNoSuchStorage, nil, nil, nil
	}

	// THE DEFAULT IS REFUSED, AND THIS ONE RULE SUBSUMES THE LAST-STORAGE CASE. A backup that
	// names no storage resolves to the default, so forgetting it would leave that resolution with
	// no answer — and ResolveStorages marks a LONE storage default implicitly, so on a
	// single-storage install the only storage IS the default and this catches it before
	// CheckStorages ever sees an empty list. Refusing here rather than relying on that floor is
	// deliberate: the floor's message is about having nowhere to keep backups, which is the wrong
	// explanation for a user who has two disks and picked the wrong one.
	if (*cur.Storage)[idx].Default {
		msg := fmt.Sprintf(
			"storage %q is the default — a backup that names no storage resolves to it, so quince "+
				"will not forget it. Make another storage the default first, then forget this one.",
			name)
		if len(*cur.Storage) == 1 {
			msg = fmt.Sprintf(
				"storage %q is the only storage declared, so it is the default implicitly — and "+
					"quince refuses to start with none declared. Declare another storage and make "+
					"it the default first, then forget this one. Nothing on the disk is deleted "+
					"either way.",
				name)
		}
		return ForgetRefused, []wire.ConfigError{{Path: "storage", Message: msg}}, nil, nil
	}

	// A NEW SLICE, never a splice in place: Current() hands back a Config whose Storage pointer
	// aliases the live one, so appending into it would mutate the snapshot other readers hold.
	kept := make([]StorageEntry, 0, len(*cur.Storage)-1)
	kept = append(kept, (*cur.Storage)[:idx]...)
	kept = append(kept, (*cur.Storage)[idx+1:]...)
	next := cur
	next.Storage = &kept

	// Replace re-validates the whole document and runs CheckStorages, so nothing here has to
	// re-derive what a coherent declaration is. Its errors reach the caller as the same 422.
	errs, warns, err := s.Replace(next)
	switch {
	case err != nil:
		return ForgetRefused, nil, nil, err
	case len(errs) > 0:
		return ForgetRefused, errs, nil, nil
	}
	return ForgetDone, nil, warns, nil
}

// ForgetRestartWarning is the notice a successful Forget carries back, in the `warnings` channel
// the config endpoints already return.
//
// A PROPERTY OF THE RESPONSE, not of the stored state, and that is why it is built here rather
// than pushed into Service.warnings: `Replace` clears those on a valid write, because they
// describe the FILE as parsed. This describes the gap between the file and the running process,
// which is true from the moment of the write until the restart and belongs to nobody's snapshot.
//
// It exists at all because `no silent caps or fallbacks` applies squarely: the storage is gone
// from the declaration and still being served, and a 200 with nothing said would leave a user
// watching a card that should have disappeared with no explanation for why it has not.
func ForgetRestartWarning(name string) Warning {
	return Warning{
		Path: "storage",
		Message: fmt.Sprintf(
			"storage %q is no longer declared, but this process is still serving it — restart "+
				"quince to apply. Nothing on the disk was deleted; the backups that were there "+
				"are still there.",
			name),
	}
}
