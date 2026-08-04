package storage

import (
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Refresher re-resolves ONE declared storage and returns the Slot it is now.
//
// It is injected rather than owned because resolution needs the config entry and the storages
// table, which live outside this package — `buildStorage` supplies a closure over both. Nil when
// nothing wired one, in which case a recheck honestly reports that it cannot re-probe rather than
// silently returning the stale answer.
type Refresher func(name string) (Slot, bool)

// SetRefresher wires the re-probe used by POST /api/storages/{name}/recheck.
//
// WIRING TIME ONLY, and unsynchronised on purpose — the same constraint `config.Service.Subscribe`
// carries. It is written once during `buildStorage`, before the HTTP server exists, so no reader
// can be concurrent with it. Re-setting it later would be a data race on a func value, and there is
// no lock here to make that safe.
func (m *Manager) SetRefresher(f Refresher) { m.refresh = f }

// ApplyStorages replaces the whole slot list (qn.6g, quince#577). It is what makes a `storage:`
// change take effect without a restart.
//
// THE WHOLE LIST, never a splice. The caller rebuilds it from the declaration with the same
// `declaredStorages` + `resolveSlot` pair `buildStorage` uses at startup, so a live apply and a
// restart cannot disagree about what a storage IS — the property `SetRefresher`'s closure already
// argues for, applied to the list rather than to one entry.
//
// AN EMPTY LIST IS REFUSED. `config.CheckStorages` refuses an empty declaration before any write,
// so this should be unreachable; it returns a warning rather than panicking because by the time an
// applier runs, the file is already written and a panic would take down a daemon over a document
// that is already on disk. Every reader below is guarded anyway — that is the rest of this PR — so
// the refusal is about keeping the Manager's invariant, not about preventing a crash.
//
// It does NOT touch `jobStorage`: a job bound to a storage that has just been forgotten must keep
// its binding, so `jobSlot` can refuse by name rather than silently retargeting it. An in-flight job
// holds its own copied Slot and completes against the disk it started on.
func (m *Manager) ApplyStorages(next []Slot) []string {
	if len(next) == 0 {
		return []string{"the storage list would be empty, so it was not applied — quince kept the " +
			"storages it was already serving"}
	}
	m.mu.Lock()
	prev := m.slots
	m.slots = next
	m.mu.Unlock()

	var warns []string
	for _, p := range prev {
		if !slotNamed(next, p.Name) {
			m.log.Info("storage no longer declared — it is no longer served", "storage", p.Name)
		}
	}
	for _, n := range next {
		if !slotNamed(prev, n.Name) {
			m.log.Info("storage newly declared", "storage", n.Name, "path", n.Root,
				"reachable", n.Reachable)
		}
		if !n.Usable() {
			warns = append(warns, "storage "+n.Name+" is declared but not reachable ("+
				n.UnreachableCode+"): "+n.UnreachableReason)
		}
	}
	return warns
}

func slotNamed(list []Slot, name string) bool {
	for _, s := range list {
		if s.Name == name {
			return true
		}
	}
	return false
}

// Storages implements the contracts §1 GET /api/storages read.
//
// udid "" means the device-independent list. With a udid, each storage also answers "will the next
// backup here be full" — the (device, storage) pair fact that story 8 owes the user BEFORE tens of
// gigabytes move.
func (m *Manager) Storages(udid string) []wire.Storage {
	slots := m.slotsSnapshot()
	// ONE count query for the whole list, not one per slot (qn.6d gap A). A failure is not fatal:
	// counts are a card detail, where reachability and the full-transfer warning are the facts a
	// user acts on. A nil map yields zeroes; the alternative — failing the whole list because a
	// COUNT did not run — would hide the storages.
	counts, err := m.reg.CountVersionsByStorage()
	if err != nil {
		m.log.Warn("storage counts unavailable — cards will show zero", "err", err)
	}
	out := make([]wire.Storage, 0, len(slots))
	for i, s := range slots {
		out = append(out, m.storageToWire(s, i == 0, udid, counts))
	}
	return out
}

func (m *Manager) storageToWire(s Slot, isDefault bool, udid string,
	counts map[string]store.StorageCounts) wire.Storage {
	// A storage that was never reached has no backend, and "unknown" says so rather than guessing.
	// It is a declared value in the enum precisely so a client is not left reading an empty string
	// as either "none" or "not sent".
	backend := s.BackendName
	if backend == "" {
		backend = "unknown"
	}
	v := wire.Storage{
		ID: s.StorageID, Name: s.Name, Path: s.Root, Backend: backend,
		Default: isDefault, Reachable: s.Reachable,
	}
	if !s.Reachable {
		code, reason := s.UnreachableCode, s.UnreachableReason
		v.UnreachableCode, v.UnreachableReason = &code, &reason
	}
	if udid != "" {
		// PER (device, storage), which is the whole point of the `?udid=` form. Asked across all
		// storages it would report "incremental" for a first backup to a NEW storage — which is a
		// full transfer — and pre-empt the warning the user is owed.
		full := !s.hasVersionFor(m, udid)
		v.WillBeFull = &full
	}

	// Counts are the DB's answer, so they are populated whether or not the disk is reachable — and
	// they are CURRENT rather than a last-known reading, which is why no timestamp accompanies them
	// (quince#588, ruled 2026-08-03). A storage with no rows is simply absent from the map, and the
	// zero value is the right answer.
	c := counts[s.StorageID]
	v.BackupCount, v.DeviceCount = c.Backups, c.Devices

	// Capacity is the FILESYSTEM's and only exists when the path can be read. NULL rather than 0
	// when unreachable: a zero is a measurement and this is an absence (ruled 2026-08-03).
	if s.Reachable {
		if free, total, err := m.capacityOf(s); err == nil {
			f, t := int64(free), int64(total) //nolint:gosec // filesystem sizes do not exceed int64
			v.FilesystemFreeBytes, v.FilesystemTotalBytes = &f, &t
		} else {
			// Reachable but unmeasurable — a path that became unreadable between the resolve and
			// this call, or a zfs hook that could not be run. Leaving both null is honest;
			// substituting 0 would claim a full disk. quince#585's ruling points a hook failure at
			// this already-ruled path rather than inventing a second answer for it.
			m.log.Warn("capacity unavailable on a reachable storage — omitted",
				"storage", s.Name, "path", s.Root, "backend", s.BackendName, "err", err)
		}
	}
	return v
}

// capacityReporter is a Backend that knows its own capacity better than `statfs` does.
//
// OPTIONAL BY DESIGN. Only zfs implements it, because only zfs has the problem: its storage root is
// a dataset whose per-device CHILDREN hold every backup, and `statfs` on the parent counts none of
// them (quince#585). reflink, hardlink and copy are ordinary directories under one filesystem, where
// `statfs` is the right instrument — so requiring the method on the interface would make three
// backends implement a workaround for a fourth's problem.
type capacityReporter interface {
	Capacity() (free, total uint64, err error)
}

// capacityOf asks the backend first and falls back to `statfs`.
//
// The fallback is not a degraded mode: for three of four backends it IS the correct measurement,
// which is why there is no warning on taking it.
func (m *Manager) capacityOf(s Slot) (free, total uint64, err error) {
	if cr, ok := s.Backend.(capacityReporter); ok && s.Backend != nil {
		return cr.Capacity()
	}
	return FilesystemSpace(s.Root)
}

// hasVersionFor reports whether this storage already holds a committed, present version for a
// device. A MISSING version does not count: its artifact is gone, so the next backup transfers
// everything again and saying otherwise would understate the cost.
func (s Slot) hasVersionFor(m *Manager, udid string) bool {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		// Unknown, and the honest default is the EXPENSIVE one: claiming a backup will be
		// incremental when quince could not check is how a user gets a surprise multi-hour
		// transfer. Reporting "full" over-warns; the inverse under-warns, and only one of those
		// costs trust.
		m.log.Error("storage: will-be-full lookup failed", "udid", udid, "error", err)
		return false
	}
	for _, r := range rows {
		if !r.Missing && s.owns(r.StorageID) {
			return true
		}
	}
	return false
}

// RecheckStorage re-probes ONE storage's reachability and returns its new state (contracts §1
// POST /api/storages/{name}/recheck). ok=false means no storage is declared under that name.
//
// IT IS THE REACHABILITY CHECK, NEVER THE BACKEND-SELECTION PROBE (quince#438). It creates no
// directory, writes no marker and selects no backend, so G5b — which forbids re-probing a bare
// mountpoint back into a new empty storage — is untouched. The two operations share a word and
// nothing else.
// THE LOCK IS NEVER HELD ACROSS THE RE-PROBE. `m.refresh` does filesystem work — a stat on a path
// that may be a dead network mount — and holding a write lock across it would stall every read of
// `slots`, which includes the render and backup paths (quince#445 review).
// KEYED ON `name`, NOT ON THE MARKER UUID — Operator ruling 2026-08-02 (quince#570), built for
// this route by quince#610. A storage quince has never reached has NO id: none was ever minted,
// which is correct. Keying on the id made this endpoint unreachable for exactly the storage it
// exists to serve — the client sent `POST /api/storages//recheck`, the router path-cleaned that to
// a `307` (method-preserving, so the POST is re-sent), and the redirect target matched no pattern,
// giving a `404`. Measured against a running daemon rather than inferred.
//
// `name` exists for every declared storage, reachable or not, and it is the identity the config
// carries — the same one `Refresher` already re-resolves by, a few lines down. Forget was built on
// it from the start; this route was simply left behind.
func (m *Manager) RecheckStorage(name string) (wire.Storage, bool) {
	// NO INDEX SURVIVES A LOCK RELEASE ANY MORE (qn.6g). This used to capture `idx` here and carry
	// it through two unlocked windows into renderSlot; it now only asks whether the storage is
	// declared, and every later read re-finds by name.
	m.mu.RLock()
	known := false
	for _, s := range m.slots {
		if s.Name == name {
			known = true
			break
		}
	}
	m.mu.RUnlock()

	if !known {
		return wire.Storage{}, false
	}
	if m.refresh == nil {
		// Nothing wired a re-probe, so the honest answer is the state we hold — reported as-is
		// rather than dressed up as a fresh reading.
		m.log.Warn("storage: recheck requested but no refresher is wired — returning the last known state",
			"storage", name)
		return m.renderSlot(name)
	}

	fresh, ok := m.refresh(name) // OUTSIDE the lock: filesystem work
	if ok {
		m.mu.Lock()
		// Re-find by NAME rather than trusting a position across the unlocked window. Nothing else
		// mutated the list when this was written, and a position captured before an unlocked gap is
		// precisely the assumption that stops holding the moment something does — which is qn.6g.
		// (By name rather than by id for the same reason the lookup above is: an unreached storage
		// has no id to re-find by, so the id form silently failed to write back the very state a
		// recheck exists to refresh.)
		for i, s := range m.slots {
			if s.Name == name {
				m.slots[i] = fresh
				break
			}
		}
		m.mu.Unlock()
		m.log.Info("storage rechecked", "storage", fresh.Name, "reachable", fresh.Reachable,
			"code", fresh.UnreachableCode)
	}
	// The storage may have been forgotten while the re-probe ran. renderSlot reports that as a miss
	// and the caller 404s, rather than rendering a card for something no longer declared.
	return m.renderSlot(name)
}

// renderSlot renders one slot BY NAME under the read lock.
//
// IT TOOK AN INDEX UNTIL qn.6g, AND THAT WAS THE SHARPEST HAZARD IN THIS FILE (quince#577).
// `RecheckStorage` found `idx` under the read lock, released it, did filesystem work, re-locked,
// re-found, released again — and then called this, which ran a DATABASE ROUND TRIP before indexing.
// So there were TWO unlocked windows in front of `m.slots[idx]`, not one, and the `!ok` branch
// carried a stale `idx` past both. A list that shrinks in either window is an out-of-range panic —
// and `Slot` holds a Backend INTERFACE, so a torn read is an itab/data mismatch: a segfault, not a
// wrong answer.
//
// Narrowing those windows would have left a race that is rare rather than absent. Re-finding by
// name under the lock removes them, and name is the right key for the same reason `RecheckStorage`
// already re-finds by it: an unreached storage has no id to be found by (quince#570).
//
// `found=false` means the storage was forgotten while this call was in flight. That is a real state
// once the list is live, and the caller reports it as a 404 rather than rendering a zero Slot —
// which would put an empty card on screen for a storage that no longer exists.
//
// The count query still runs OUTSIDE the lock: it is a database round trip, and holding the slot
// lock across it would block every reader for its duration.
func (m *Manager) renderSlot(name string) (wire.Storage, bool) {
	counts, err := m.reg.CountVersionsByStorage()
	if err != nil {
		m.log.Warn("storage counts unavailable — card will show zero", "err", err)
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	for i, s := range m.slots {
		if s.Name == name {
			return m.storageToWire(s, i == 0, "", counts), true
		}
	}
	return wire.Storage{}, false
}

// Recheck satisfies httpapi.StorageReader.
func (m *Manager) Recheck(id string) (wire.Storage, bool) { return m.RecheckStorage(id) }
