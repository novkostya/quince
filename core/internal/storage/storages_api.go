package storage

import (
	"time"

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

// SetRefresher wires the re-probe used by POST /api/storages/{id}/recheck.
func (m *Manager) SetRefresher(f Refresher) { m.refresh = f }

// Storages implements the contracts §1 GET /api/storages read.
//
// udid "" means the device-independent list. With a udid, each storage also answers "will the next
// backup here be full" — the (device, storage) pair fact that story 8 owes the user BEFORE tens of
// gigabytes move.
func (m *Manager) Storages(udid string) []wire.Storage {
	slots := m.slotsSnapshot()
	// ONE count query for the whole list, not one per slot (qn.6d gap A). A failure is not fatal:
	// counts are a card detail, where reachability and the full-transfer warning are the facts a
	// user acts on. A nil map yields zeroes, and `counts_as_of` still stamps the attempt — the
	// alternative, failing the whole list because a COUNT did not run, would hide the storages.
	counts, asOf, err := m.countsNow()
	if err != nil {
		m.log.Warn("storage counts unavailable — cards will show zero", "err", err)
	}
	out := make([]wire.Storage, 0, len(slots))
	for i, s := range slots {
		out = append(out, m.storageToWire(s, i == 0, udid, counts, asOf))
	}
	return out
}

// countsNow reads every storage's counts and stamps them ONCE.
//
// The stamp belongs to the QUERY, not to each row: all the counts in one response came from one
// round trip, so rendering them with per-slot timestamps would say two cards were true at different
// moments when they were true at the same one. Cheap to get wrong — `now()` per slot compiles and
// looks right — and it also made an unsynchronised test clock race under `-race`.
func (m *Manager) countsNow() (map[string]store.StorageCounts, string, error) {
	asOf := m.now().UTC().Format(time.RFC3339)
	counts, err := m.reg.CountVersionsByStorage()
	return counts, asOf, err
}

func (m *Manager) storageToWire(s Slot, isDefault bool, udid string,
	counts map[string]store.StorageCounts, countsAsOf string) wire.Storage {
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

	// Counts are the DB's answer, so they are populated whether or not the disk is reachable, and
	// CountsAsOf is what tells a client they were true at last contact rather than now. A storage
	// with no rows is simply absent from the map — the zero value is the right answer.
	c := counts[s.StorageID]
	v.BackupCount, v.DeviceCount = c.Backups, c.Devices
	v.CountsAsOf = countsAsOf

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
// POST /api/storages/{id}/recheck). ok=false means no storage with that id.
//
// IT IS THE REACHABILITY CHECK, NEVER THE BACKEND-SELECTION PROBE (quince#438). It creates no
// directory, writes no marker and selects no backend, so G5b — which forbids re-probing a bare
// mountpoint back into a new empty storage — is untouched. The two operations share a word and
// nothing else.
// THE LOCK IS NEVER HELD ACROSS THE RE-PROBE. `m.refresh` does filesystem work — a stat on a path
// that may be a dead network mount — and holding a write lock across it would stall every read of
// `slots`, which includes the render and backup paths (quince#445 review).
func (m *Manager) RecheckStorage(id string) (wire.Storage, bool) {
	m.mu.RLock()
	idx, name := -1, ""
	for i, s := range m.slots {
		if s.StorageID == id {
			idx, name = i, s.Name
			break
		}
	}
	m.mu.RUnlock()

	if idx < 0 {
		return wire.Storage{}, false
	}
	if m.refresh == nil {
		// Nothing wired a re-probe, so the honest answer is the state we hold — reported as-is
		// rather than dressed up as a fresh reading.
		m.log.Warn("storage: recheck requested but no refresher is wired — returning the last known state",
			"storage", name)
		return m.renderSlot(idx), true
	}

	fresh, ok := m.refresh(name) // OUTSIDE the lock: filesystem work
	if ok {
		m.mu.Lock()
		// Re-find by id rather than trusting idx across the unlocked window. Nothing else mutates
		// the list today, and a position captured before an unlocked gap is precisely the assumption
		// that stops holding the moment something does.
		for i, s := range m.slots {
			if s.StorageID == id {
				m.slots[i] = fresh
				idx = i
				break
			}
		}
		m.mu.Unlock()
		m.log.Info("storage rechecked", "storage", fresh.Name, "reachable", fresh.Reachable,
			"code", fresh.UnreachableCode)
	}
	return m.renderSlot(idx), true
}

// renderSlot renders one slot by index under the read lock.
//
// The count query runs OUTSIDE the lock — it is a database round trip, and holding the slot lock
// across it would block every reader for its duration. Rechecking one storage is rare enough that
// a second query costs nothing worth pooling with Storages().
func (m *Manager) renderSlot(idx int) wire.Storage {
	counts, asOf, err := m.countsNow()
	if err != nil {
		m.log.Warn("storage counts unavailable — card will show zero", "err", err)
	}
	m.mu.RLock()
	s := m.slots[idx]
	m.mu.RUnlock()
	return m.storageToWire(s, idx == 0, "", counts, asOf)
}

// Recheck satisfies httpapi.StorageReader.
func (m *Manager) Recheck(id string) (wire.Storage, bool) { return m.RecheckStorage(id) }
