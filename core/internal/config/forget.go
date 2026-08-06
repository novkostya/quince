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
	// THE WHOLE READ-MODIFY-WRITE IS UNDER writeMu (quince#665 review). Reading the list, splicing
	// one entry out and writing the result is three steps; without the lock two concurrent forgets
	// both read the same list, so the second write silently RESTORES the entry the first removed and
	// both callers get a 200. It calls replaceLocked below rather than Replace, which would deadlock
	// on this same mutex.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

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
	errs, warns, err := s.replaceLocked(next)
	switch {
	case err != nil:
		return ForgetRefused, nil, nil, err
	case len(errs) > 0:
		return ForgetRefused, errs, nil, nil
	}
	return ForgetDone, nil, warns, nil
}

// ForgetRestartWarning IS DELETED, and the deletion is the point of qn.6g PR 4 rather than tidying.
//
// It read: *"storage %q is no longer declared, but this process is still serving it — restart
// quince to apply. Nothing on the disk was deleted."* With the storage applier wired, the second
// clause is false: the storage stops being served at the moment of the write. A warning that names
// a restart nobody needs is exactly the silent-fallback inversion — it would send a user to reboot
// a daemon to complete something already complete.
//
// **Its SHAPE outlives it and is cited in three places** (`service.go`, `subscribe_test.go`, and
// qn.6g's own spec): an applier warning is a property of THIS RESPONSE, built at the call site and
// never pushed into `Service.warnings`, because `Replace` clears those on every valid write — they
// describe the FILE as parsed, and this describes the gap between the file and the running process.
// That precedent is why applier warnings are returned per-call. The rule survives; this instance of
// it does not.
//
// What replaces it for the case it really covered — an applier that could not take the change — is
// the applier's own returned warning, which says what actually failed instead of prescribing a
// restart that may not fix it.
