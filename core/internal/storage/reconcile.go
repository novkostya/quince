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
	journals, err := m.backend.PendingJournals()
	if err != nil {
		return err
	}
	for _, j := range journals {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		committed, ok, err := m.backend.ResumeCommit(j)
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
	arts, err := m.backend.Scan(udid)
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
	inReg := map[string]store.VersionRow{}
	for _, r := range rows {
		inReg[r.ID] = r
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
	return m.backend.SweepWork(udid)
}

// adopt registers an on-disk version discovered without a row as ADOPTED (job_id null →
// protected from retention until the user says otherwise; contracts §2).
func (m *Manager) adopt(udid string, a Artifact) {
	created, _ := parseRFC(a.Marker.CreatedAt)
	row := store.VersionRow{
		ID: a.Marker.VersionID, UDID: udid, Backend: a.Backend, ZFSSnapshot: a.ZFSSnapshot,
		CreatedAt: created, JobID: nil, Kind: a.Marker.Kind, Encrypted: a.Marker.Encrypted,
		IsLatest: a.IsLatest, LogicalBytes: a.PhysicalBytes, PhysicalBytes: a.PhysicalBytes,
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
	return m.reg.PromoteLatest(udid, present[0].ID)
}

// reconcileUDIDs is the union of udids with registry rows and on-disk device dirs.
func (m *Manager) reconcileUDIDs() []string {
	set := map[string]struct{}{}
	if rows, err := m.reg.ListVersions(""); err == nil {
		for _, r := range rows {
			set[r.UDID] = struct{}{}
		}
	}
	if entries, err := os.ReadDir(m.backups); err == nil {
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
