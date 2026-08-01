package storage

import "github.com/novkostya/quince/core/internal/wire"

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
	out := make([]wire.Storage, 0, len(slots))
	for i, s := range slots {
		out = append(out, m.storageToWire(s, i == 0, udid))
	}
	return out
}

func (m *Manager) storageToWire(s Slot, isDefault bool, udid string) wire.Storage {
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
	return v
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
func (m *Manager) renderSlot(idx int) wire.Storage {
	m.mu.RLock()
	s := m.slots[idx]
	m.mu.RUnlock()
	return m.storageToWire(s, idx == 0, "")
}

// Recheck satisfies httpapi.StorageReader.
func (m *Manager) Recheck(id string) (wire.Storage, bool) { return m.RecheckStorage(id) }
