package storage

import (
	"context"
	"os"
	"sort"

	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Reconcile is the first-class startup reconciliation subsystem (design §5, stack D3). The DISK
// is the source of truth; every half-state has a defined repair, following the roll-forward
// principle (a verified artifact is never destroyed): (1) complete any commit journal left by a
// crash; then per device (2) adopt on-disk versions with no registry row, (3) mark rows whose
// artifact vanished as `missing` (never drop), (4) recompute the single latest, (5) sweep
// orphaned work — only after the above. Safe to run at every startup.
func (m *Manager) Reconcile(ctx context.Context) error {
	journals, err := m.defaultSlot().Backend.PendingJournals()
	if err != nil {
		return err
	}
	for _, j := range journals {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		committed, ok, err := m.defaultSlot().Backend.ResumeCommit(j)
		if err != nil {
			m.log.Error("reconcile: roll-forward failed — left in place, not unwound",
				"udid", j.UDID, "version", j.VersionID, "phase", j.Phase, "error", err)
			continue
		}
		if !ok {
			continue
		}
		if _, exists, _ := m.reg.GetVersion(committed.VersionID); !exists {
			if err := m.registerCommitted(committed); err != nil {
				m.log.Error("reconcile: register rolled-forward version failed", "version", committed.VersionID, "error", err)
				continue
			}
			row, _, _ := m.reg.GetVersion(committed.VersionID)
			m.bus.PublishEvent(wire.EventVersionCreated, m.toWire(row))
		}
		m.log.Info("reconcile: completed a half-done commit (roll-forward)",
			"udid", j.UDID, "version", committed.VersionID, "from_phase", j.Phase)
	}

	for _, udid := range m.reconcileUDIDs() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.reconcileDevice(udid); err != nil {
			m.log.Error("reconcile: device reconciliation failed", "udid", udid, "error", err)
		}
	}
	return nil
}

func (m *Manager) reconcileDevice(udid string) error {
	arts, err := m.defaultSlot().Backend.Scan(udid)
	if err != nil {
		return err
	}
	onDisk := map[string]Artifact{}
	for _, a := range arts {
		onDisk[a.Marker.VersionID] = a
	}
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return err
	}
	// SCOPED TO THIS STORAGE. `Scan` walked one root, so it can only answer for one storage's
	// versions — and the loop below concludes "not on disk ⇒ missing" from it. Comparing this
	// backend's scan against ALL of a device's rows means storage B's pass marks storage A's
	// versions missing, then A's pass marks B's, and the last writer wins (quince#378 survey).
	//
	// Membership is `owns` — the NULL group is a real group (same shape as the is_latest ruling):
	// an UNATTRIBUTED Manager owns the unattributed rows, an attributed one does not. For an
	// attributed Manager a NULL row is SKIPPED rather than assumed to be ours, because quince
	// does not know where that version lives, so this scan cannot conclude anything about it —
	// marking it missing for being absent from THIS root would invent a fact. The attribution
	// sweep runs before reconciliation precisely so that set is empty in practice.
	inReg := map[string]store.VersionRow{}
	var skipped int
	for _, r := range rows {
		if m.owns(r.StorageID) {
			inReg[r.ID] = r
		} else {
			skipped++
		}
	}
	if skipped > 0 {
		m.log.Warn("reconcile: versions with no storage attribution were skipped — their artifacts "+
			"cannot be checked against any one root", "udid", udid, "count", skipped)
	}

	// Adopt on-disk versions with no row; clear `missing` where an artifact reappeared.
	for id, a := range onDisk {
		r, ok := inReg[id]
		if !ok {
			m.adopt(udid, a)
			continue
		}
		if r.Missing {
			if err := m.reg.MarkVersionMissing(id, false); err != nil {
				return err
			}
			m.log.Info("reconcile: version artifact reappeared", "id", id, "udid", udid)
		}
	}
	// Mark rows whose artifact vanished as missing (never drop).
	for id, r := range inReg {
		if _, ok := onDisk[id]; !ok && !r.Missing {
			if err := m.reg.MarkVersionMissing(id, true); err != nil {
				return err
			}
			m.log.Warn("reconcile: version artifact missing — kept as `missing`, not dropped", "id", id, "udid", udid)
		}
	}

	if err := m.recomputeLatest(udid); err != nil {
		return err
	}
	// Orphaned work is swept only after reconciliation has completed for the device.
	return m.defaultSlot().Backend.SweepWork(udid)
}

// adopt registers an on-disk version discovered without a row as ADOPTED (job_id null →
// protected from retention until the user says otherwise; contracts §2).
func (m *Manager) adopt(udid string, a Artifact) {
	created, _ := parseRFC(a.Marker.CreatedAt)
	row := store.VersionRow{
		ID: a.Marker.VersionID, UDID: udid, Backend: a.Backend, ZFSSnapshot: a.ZFSSnapshot,
		CreatedAt: created, JobID: nil, Kind: a.Marker.Kind, Encrypted: a.Marker.Encrypted,
		IsLatest: a.IsLatest, LogicalBytes: a.PhysicalBytes, PhysicalBytes: a.PhysicalBytes,
		// Attributed to the storage it was SCANNED FROM. An adopted version is found by walking a
		// specific root, so which storage it lives on is known here and never needs guessing.
		StorageID: m.storageIDPtr(),
	}
	if sv, err := parseRFC(a.Marker.StructureVerifiedAt); err == nil {
		row.StructureVerifiedAt = &sv
	}
	if err := m.reg.InsertVersion(row); err != nil {
		m.log.Error("reconcile: adopt insert failed", "id", row.ID, "udid", udid, "error", err)
		return
	}
	m.bus.PublishEvent(wire.EventVersionCreated, m.toWire(row))
	m.log.Info("reconcile: adopted on-disk version (no DB record)", "id", row.ID, "udid", udid,
		"backend", a.Backend)
}

// recomputeLatest makes the newest PRESENT (non-missing) version the sole latest.
func (m *Manager) recomputeLatest(udid string) error {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return err
	}
	var present []store.VersionRow
	for _, r := range rows {
		if !r.Missing {
			present = append(present, r)
		}
	}
	if len(present) == 0 {
		return nil
	}
	sort.Slice(present, func(i, j int) bool { return present[i].CreatedAt.After(present[j].CreatedAt) })

	// PER STORAGE, not per device (Operator ruling 2026-08-01, quince#378). One promotion per
	// group, so a device with versions on two storages ends with two `is_latest` rows — one each —
	// and `browse_root` resolves for both. Promoting once across all of a device's rows would leave
	// every storage but the winner with its newest version flagged false, whose browse_root then
	// points at a `versions/<ts>/` dir that does not exist.
	//
	// Unattributed rows (storage_id NULL) form their own group and get their own latest. That is
	// what keeps a pre-qn.6c device resolvable before the attribution sweep has reached it —
	// excluding them would leave it with no latest at all, which is the same defect inverted.
	promoted := map[string]bool{}
	for _, r := range present {
		key := "\x00" // the NULL group; cannot collide with a ULID
		if r.StorageID != nil {
			key = *r.StorageID
		}
		if promoted[key] {
			continue // `present` is newest-first, so the first of each group IS that group's latest
		}
		promoted[key] = true
		if err := m.reg.PromoteLatest(udid, r.ID, r.StorageID); err != nil {
			return err
		}
	}
	return nil
}

// reconcileUDIDs is the union of udids with registry rows and on-disk device dirs.
func (m *Manager) reconcileUDIDs() []string {
	set := map[string]struct{}{}
	if rows, err := m.reg.ListVersions(""); err == nil {
		for _, r := range rows {
			set[r.UDID] = struct{}{}
		}
	}
	if entries, err := os.ReadDir(m.defaultSlot().Root); err == nil {
		for _, e := range entries {
			if e.IsDir() && validUDID(e.Name()) {
				set[e.Name()] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(set))
	for u := range set {
		out = append(out, u)
	}
	sort.Strings(out)
	return out
}
