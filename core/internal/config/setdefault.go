package config

import (
	"github.com/novkostya/quince/core/internal/wire"
)

// SetDefaultOutcome is which of the three answers POST /api/config/storage/{name}/default gives
// (Operator ruling 2026-08-11, quince#722): 200, 404, or a write failure.
//
// AN ENUM WITH NO REFUSAL MEMBER, which is the shape of this route rather than an omission. Forget
// has `ForgetRefused` because forgetting can leave the declaration incoherent — no default, or no
// storage at all. Re-designation removes nothing: the entry stays, exactly one entry ends up
// flagged, and the document is coherent before and after. `replaceLocked` still re-validates and
// can still return errors, so the caller keeps a 422 path it should never reach; see below.
type SetDefaultOutcome int

const (
	// SetDefaultDone means the flag now sits on the named storage — including when it already did.
	SetDefaultDone SetDefaultOutcome = iota
	// SetDefaultNoSuchStorage means no declared storage carries that name → 404.
	SetDefaultNoSuchStorage
	// SetDefaultRefused means the resulting declaration failed validation → 422. Unreachable
	// through this function as written and kept because a floor that returns silently would be
	// worse than one that never fires; see the note at the write below.
	SetDefaultRefused
)

// SetDefaultStorage makes one declared storage the default, addressed by its config `name`.
//
// THE FLAG IS THE TRUTH AND THE ORDER IS LEFT ALONE (Operator ruling 2026-08-11, quince#722). This
// clears `default: true` from every entry and sets it on the named one, and it does NOT reorder the
// list. `declaredStorages` hoists the flagged entry when slots are built, so `slots[0]` follows the
// flag with no help from this function — which is exactly the ruling's point. Order in `config.yml`
// is cosmetic, so a hand-edit is one edit: move the flag.
//
// WHY IT EXISTS RATHER THAN A `PUT` TO /api/config, which can already set this. The reason is
// `ForgetStorage`'s, unchanged: `handleConfigPut` decodes into a ZERO-VALUED Config, so a client
// that rebuilds the storage list from what it rendered drops every surviving entry's `zfs:` and
// `retention:` keys — the keys no storage card shows, which the UI could not round-trip even in
// principle. Splicing HERE, over the live parsed config, means the survivors are the values that
// were loaded. `gap B` argued this once and the ruling declined to relitigate it.
//
// ALREADY-DEFAULT IS A 200 AND A NO-OP, and the choice was delegated to the implementer. It is a
// state assertion — *make this the default* — not a command, so asking for the state it is already
// in has been satisfied. A 422 there would be a refusal whose remedy is "do nothing", which is the
// unfollowable-remedy defect this whole issue was filed about. The write still happens, so the log
// records the door and any Applier still runs; nothing about the document changes.
//
// NO BUSY REFUSAL, and its absence is a decision rather than an oversight. `ForgetStorage` takes a
// `busyReason` callback because forgetting a storage mid-backup leaves `CommitJob` unable to
// resolve its slot between verify and commit — which is what *"a commit failure must not destroy a
// multi-hour Wi-Fi transfer"* forbids. Re-designation removes no slot and rebinds no job: contracts
// §6 already says a re-designation *"takes effect for the next unbound job"*, and a running job is
// bound. There is nothing for a running backup to lose, so there is nothing to refuse.
//
// AN UNREACHABLE STORAGE MAY BE MADE THE DEFAULT. A disk that is unplugged right now is precisely
// the one somebody designates for later, and refusing would be a refusal with no remedy — the user
// cannot make quince reach a disk from this screen. The consequence is already contracted: a job
// that resolves to an unreachable storage answers 409, *plug the disk in*, which is a state the
// user can act on. Named because its absence from the refusal set would otherwise read as untested.
func (s *Service) SetDefaultStorage(name string) (SetDefaultOutcome, []wire.ConfigError, []Warning, error) {
	// THE WHOLE READ-MODIFY-WRITE IS UNDER writeMu, for `ForgetStorage`'s reason: reading the list,
	// moving the flag and writing the result is three steps, and two concurrent calls that both read
	// the same list would have the second write silently undo the first while both callers got a
	// 200. It calls replaceLocked below rather than Replace, which would deadlock on this mutex.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	cur := s.Current()
	if cur.Storage == nil {
		return SetDefaultNoSuchStorage, nil, nil, nil
	}

	idx := -1
	for i, e := range *cur.Storage {
		if e.Name == name {
			idx = i
			break
		}
	}
	if idx < 0 {
		return SetDefaultNoSuchStorage, nil, nil, nil
	}

	// A NEW SLICE, never a mutation in place: `Current()` hands back a Config whose Storage pointer
	// aliases the live one, so setting `Default` through it would rewrite the snapshot other readers
	// are holding — and would do it BEFORE the write, so a failed write would leave the process
	// serving a declaration that is on no disk. `ForgetStorage` copies for the same reason.
	next := cur
	entries := make([]StorageEntry, len(*cur.Storage))
	copy(entries, *cur.Storage)
	for i := range entries {
		entries[i].Default = i == idx
	}
	next.Storage = &entries

	// EXACTLY ONE ENTRY IS FLAGGED BY CONSTRUCTION, which is what `Validate` requires — so the
	// error path below is unreachable as written. It is kept rather than dropped because
	// `replaceLocked` re-validates the WHOLE document, and this write can therefore surface a fault
	// that has nothing to do with the flag: a `config.yml` edited by hand between load and this call
	// is the ordinary route. Returning `SetDefaultDone` on a write that validation rejected would be
	// a silent failure, and there is no version of that worth the saved branch.
	errs, warns, err := s.replaceLocked(next, SourceSetDefaultStorage)
	switch {
	case err != nil:
		return SetDefaultRefused, nil, nil, err
	case len(errs) > 0:
		return SetDefaultRefused, errs, nil, nil
	}
	return SetDefaultDone, nil, warns, nil
}
