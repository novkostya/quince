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
// STORY 5b: it runs PER STORAGE. Each usable slot rolls forward its own journals and reconciles
// each device against its own root — an unusable one is skipped, because a scan of a root that is
// not there cannot conclude anything, and concluding "artifact missing" from it would mark a
// perfectly good backup as gone the first time somebody unplugged a disk.
//
// IT IS NO LONGER WHAT `serve` CALLS (qn.6i). The two halves separate because they answer to
// different masters: roll-forward must finish before `Engine.Reconcile` judges crash-orphaned job
// rows, and the scan must not delay the listener. `serve` calls RollForwardAll and hands the scan to
// the runner; this composed form is what the ADMIN CLIs use, where a synchronous complete pass is
// exactly what `versions verify` needs and no listener is waiting on it.
func (m *Manager) Reconcile(ctx context.Context) error {
	if err := m.RollForwardAll(ctx); err != nil {
		return err
	}
	res, err := m.ReconcileScan(ctx)
	// A SYNCHRONOUS CALLER IS TOLD WHAT THE PASS DID NOT DO (quince#771 review). Returning nil having
	// skipped a device is the shape `state honesty` forbids, and `buildStorage` promises its callers a
	// *reconciled* Manager. Unreachable in a CLI process today — deferrals come from bindings that
	// process never makes — so this is logged rather than assumed, because that is a property of WHO
	// CALLS IT and not of this function.
	if len(res.Deferred) > 0 {
		m.log.Warn("reconcile: this pass SKIPPED devices whose commit path was live — the registry is "+
			"reconciled EXCEPT for these", "deferred", res.Deferred)
	}
	return err
}

// RollForwardAll completes every pending commit journal on every usable storage, and scans nothing.
// It is the half that must run BEFORE `Engine.Reconcile` (design §5, amendment 1): that pass asks
// `VersionForJob` to tell a rolled-forward commit from an interrupted one, so a job row judged
// before its commit completes becomes `connection_lost` for a backup that SUCCEEDED.
//
// IT IS CHEAP BY CONSTRUCTION RATHER THAN BY MEASUREMENT, and the spec said otherwise until this
// rung read it. Both backends roll forward in O(1) of tree size: the namespace path is
// `renameat2(RENAME_EXCHANGE)` then `os.Rename` of the displaced tree into `versions/<prev-ts>/` —
// same filesystem, both under the backups root, so a directory-entry move on the `copy` backend too,
// because the clone strategy governs the SEED and not the archive — and zfs is `zfs snapshot`. So
// keeping this in front of the listener costs a few syscalls, never a copy.
func (m *Manager) RollForwardAll(ctx context.Context) error {
	for _, s := range m.usableSlots() {
		journals, err := s.Backend.PendingJournals()
		if err != nil {
			return err
		}
		for _, j := range journals {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			m.rollForward(s, j)
		}
	}
	return nil
}

// ScanResult is what one scan pass did and — the field that matters — what it did NOT do.
type ScanResult struct {
	// Deferred names the "<storage>/<udid>" pairs skipped because a commit path was live on them.
	// A pass with a non-empty Deferred is a PARTIAL pass, and every caller must be able to say so:
	// health reports `reconciling`, the runner re-triggers, the synchronous path logs.
	Deferred []string
}

// ReconcileScan is the per-device half — adopt, mark missing, recompute latest, sweep. It is what
// moves off the startup path, because it is where the 36-48 seconds are.
func (m *Manager) ReconcileScan(ctx context.Context) (ScanResult, error) {
	var res ScanResult
	for _, s := range m.usableSlots() {
		// THE DECLARATION IS RE-READ BETWEEN SLOTS (qn.6i D8). `ApplyStorages` can replace the whole
		// list while a pass runs — that is what qn.6g's live apply IS — so a snapshot taken once at the
		// top would keep reconciling a disk the user has just forgotten. Mid-slot it finishes the
		// current device: abandoning one halfway leaves exactly the half-repaired registry this
		// subsystem exists to remove.
		if !m.stillDeclared(s.Name) {
			m.log.Info("reconcile: a storage was undeclared while this pass was running — dropped from "+
				"the pass rather than reconciling a disk quince no longer serves", "storage", s.Name)
			continue
		}
		for _, udid := range m.reconcileUDIDs() {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			deferred, err := m.reconcileDevice(s, udid)
			if deferred {
				res.Deferred = append(res.Deferred, s.Name+"/"+udid)
				continue
			}
			if err != nil {
				m.log.Error("reconcile: device reconciliation failed",
					"udid", udid, "storage", s.Name, "error", err)
			}
		}
	}
	return res, nil
}

// usableSlots is every declared storage a pass may touch, with the unreachable ones announced and
// skipped — a scan of a root that is not there cannot conclude anything, and concluding "artifact
// missing" from it would mark a good backup as gone the first time somebody unplugged a disk.
func (m *Manager) usableSlots() []Slot {
	var out []Slot
	for _, s := range m.slotsSnapshot() {
		if !s.Usable() {
			m.log.Info("reconcile: skipping an unreachable storage — its versions keep their last "+
				"known state rather than being judged by a root that is not there",
				"storage", s.Name, "code", s.UnreachableCode)
			continue
		}
		out = append(out, s)
	}
	return out
}

// stillDeclared reports whether a storage NAME is in the list right now. Name rather than id,
// because the name is the identity the config carries and the one that survives a replug — the same
// reasoning `SetRefresher`'s closure uses.
func (m *Manager) stillDeclared(name string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, s := range m.slots {
		if s.Name == name {
			return true
		}
	}
	return false
}

// rollForward completes one journal left by a crash, under the commit lease for its device.
//
// IT IS A FUNCTION SO THE LEASE CAN BE RELEASED BY defer. The loop body it replaces had four
// `continue` paths, and every one of them would have leaked a claimed lease — which is not a
// deadlock that shows up in a test, it is a device that silently stops being reconcilable for the
// life of the process.
func (m *Manager) rollForward(s Slot, j Journal) {
	// A JOURNAL A LIVE JOB IS DRIVING IS NOT A JOURNAL LEFT BY A CRASH (qn.6i D3, blocker 1).
	//
	// Roll-forward exists to finish a commit whose driver DIED. Resuming one whose driver is still
	// running puts two actors through the same phase sequence — on the namespace backends through the
	// same `renameat2(RENAME_EXCHANGE)`. Whether that is harmful was never established, and the
	// ruling is that it must not be REACHABLE rather than that it be proven safe: such a proof would
	// be a property of today's phase set, and qn.6h has just changed that set for zfs.
	//
	// The journal already carries the fact that decides it (`journal.go:51`), so this needs no new
	// bookkeeping — only the question, asked.
	if m.jobIsBound(j.JobID) {
		m.log.Info("reconcile: skipping a commit journal a LIVE job is still driving — roll-forward "+
			"is for a commit whose driver died, and this one has not",
			"udid", j.UDID, "version", j.VersionID, "job", j.JobID, "phase", j.Phase)
		return
	}
	// The same lease the commit path claims, for the same (storage, device) — so a job that binds
	// between the check above and the resume below cannot overlap this either. TryClaim rather than
	// Claim: reconciliation never waits, it defers and reports.
	lease := m.leaseFor(s.StorageID, j.UDID)
	if !lease.TryClaim() {
		m.log.Info("reconcile: deferring a commit journal — this device is committing on this "+
			"storage right now", "udid", j.UDID, "version", j.VersionID, "storage", s.Name)
		return
	}
	defer lease.Release()

	committed, ok, err := s.Backend.ResumeCommit(j)
	if err != nil {
		m.log.Error("reconcile: roll-forward failed — left in place, not unwound",
			"udid", j.UDID, "version", j.VersionID, "phase", j.Phase, "error", err)
		return
	}
	if !ok {
		return
	}
	if _, exists, _ := m.reg.GetVersion(committed.VersionID); !exists {
		if err := m.registerCommitted(s, committed); err != nil {
			m.log.Error("reconcile: register rolled-forward version failed",
				"version", committed.VersionID, "error", err)
			return
		}
		row, _, _ := m.reg.GetVersion(committed.VersionID)
		m.bus.PublishEvent(wire.EventVersionCreated, m.toWire(row))
	}
	m.log.Info("reconcile: completed a half-done commit (roll-forward)",
		"udid", j.UDID, "version", committed.VersionID, "from_phase", j.Phase)
}

// reconcileDevice reconciles ONE device against ONE storage's root. It reports `deferred` SEPARATELY
// from `err`, and that separation is the whole of quince#771's review note: a deferral is neither a
// success nor a failure, and collapsing it into either loses the fact a caller needs. Folded into
// `nil` it becomes a silent partial pass; folded into `err` it becomes a failure the logs would
// shout about, for the ordinary and correct case of a backup running.
func (m *Manager) reconcileDevice(s Slot, udid string) (deferred bool, err error) {
	// THE PRE-CHECK, AND WHY IT IS NOT REDUNDANT WITH THE LEASE BELOW (qn.6i D3).
	//
	// A job holds its lease only across CommitJob — seconds — while its BINDING lasts the whole job,
	// which over Wi-Fi is hours. So the lease alone would let a scan start on a device that is
	// mid-transfer, and the commit arriving later would then WAIT for that scan. Correct, and worse
	// than not starting: the wait lands on the one path that has a multi-hour transfer behind it.
	//
	// Asking the binding instead means the common case never contends at all. The lease is what makes
	// the remaining window safe rather than unlikely.
	if jobs := m.JobsOnDevice(s.StorageID, udid); len(jobs) > 0 {
		m.log.Info("reconcile: deferring this device — a backup is running on this storage for it. "+
			"Its versions keep their current state; the next trigger reconciles them",
			"udid", udid, "storage", s.Name, "jobs", jobs)
		return true, nil
	}
	lease := m.leaseFor(s.StorageID, udid)
	if !lease.TryClaim() {
		m.log.Info("reconcile: deferring this device — it is committing on this storage right now",
			"udid", udid, "storage", s.Name)
		return true, nil
	}
	defer lease.Release()

	arts, err := s.Backend.Scan(udid)
	if err != nil {
		return false, err
	}
	onDisk := map[string]Artifact{}
	for _, a := range arts {
		onDisk[a.Marker.VersionID] = a
	}
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return false, err
	}
	// ATTRIBUTION HAPPENS HERE, BECAUSE HERE IS WHERE "WHICH STORAGE" IS KNOWN (quince#439).
	//
	// `Scan` has just walked THIS root. A row with a NULL storage_id whose artifact is in that scan
	// lives on this storage — observed, not guessed. The sweep this replaced took one storage id and
	// filled every NULL for the device, which is unanswerable before the artifacts are located: with
	// several storages declared it recorded existing backups on whichever was passed.
	//
	// Fills only NULLs and never rewrites, so it is safe on every startup, and a storage that is
	// unreachable today simply attributes its versions at a later one.
	byID := map[string]store.VersionRow{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	// THE ID COMES FROM THE SLOT WHOSE Scan PRODUCED `onDisk`, PASSED IN — not looked up.
	//
	// quince#440's review asked for this as a signature rather than a guard, and that is why: while
	// this read `m.storageIDPtr()` it resolved the DEFAULT slot's id against whatever root had just
	// been scanned. Identical with one slot, and with the loop above it would attribute EVERY
	// storage's artifacts to the default — quince#439 exactly, reintroduced by the slice after the
	// one that fixed it. A check could be forgotten; a parameter cannot.
	var mine *string
	if s.StorageID != "" {
		id := s.StorageID
		mine = &id
	}
	if mine != nil {
		for id, r := range byID {
			if r.StorageID != nil {
				continue
			}
			if _, here := onDisk[id]; !here {
				// NOT ON THIS ROOT, so this scan learns nothing about it. It stays NULL — which is
				// the honest record for a version whose artifact is gone or on a disk nobody
				// declared. Inventing an identity for data that cannot be regenerated is what
				// migration 0006 already refuses, and this is not an exception to it.
				continue
			}
			if err := m.reg.AttributeVersion(id, *mine); err != nil {
				m.log.Error("reconcile: attributing a version to this storage failed",
					"id", id, "udid", udid, "error", err)
				continue
			}
			r.StorageID = mine
			byID[id] = r
			m.log.Info("reconcile: attributed a previously unattributed version to this storage",
				"id", id, "udid", udid, "storage_id", *mine)
		}
	}

	// SCOPED TO THIS STORAGE. `Scan` walked one root, so it can only answer for one storage's
	// versions — and the loop below concludes "not on disk ⇒ missing" from it. Comparing this
	// backend's scan against ALL of a device's rows means storage B's pass marks storage A's
	// versions missing, then A's pass marks B's, and the last writer wins (quince#378 survey).
	//
	// Membership is `owns` — the NULL group is a real group (same shape as the is_latest ruling):
	// an UNATTRIBUTED Manager owns the unattributed rows, an attributed one does not. A NULL row
	// that survived the attribution pass above is one whose artifact is NOT here, so skipping it is
	// the honest answer rather than a deferral: marking it missing for being absent from a root it
	// was never on would invent a fact.
	inReg := map[string]store.VersionRow{}
	var skipped int
	for _, r := range byID {
		if s.owns(r.StorageID) {
			inReg[r.ID] = r
		} else {
			skipped++
		}
	}
	if skipped > 0 {
		m.log.Warn("reconcile: versions belonging to another storage, or to none, were skipped — "+
			"their artifacts cannot be checked against this root", "udid", udid, "count", skipped)
	}

	// Adopt on-disk versions with NO ROW AT ALL; clear `missing` where an artifact reappeared.
	for id, a := range onDisk {
		r, ok := inReg[id]
		if !ok {
			// THE PREDICATE IS "no row at all", NOT "no row I own" (quince#428). A row that exists
			// but belongs elsewhere must not be adopted — inserting it would fail on the primary key
			// at best, and duplicate a committed version's identity at worst.
			if other, exists := byID[id]; exists {
				// The artifact is under THIS root and the row says it lives on another storage.
				// Genuinely ambiguous input — a bind mount, or a replica — so say so rather than
				// pick. First scan wins, which is the existing attribution, left untouched.
				m.log.Warn("reconcile: an artifact for a version attributed to ANOTHER storage is "+
					"present under this root — leaving the existing attribution alone",
					"id", id, "udid", udid, "attributed_to", derefID(other.StorageID))
				continue
			}
			m.adopt(s, udid, a)
			continue
		}
		if r.Missing {
			if err := m.reg.MarkVersionMissing(id, false); err != nil {
				return false, err
			}
			m.log.Info("reconcile: version artifact reappeared", "id", id, "udid", udid)
		}
	}
	// Mark rows whose artifact vanished as missing (never drop).
	for id, r := range inReg {
		if _, ok := onDisk[id]; !ok && !r.Missing {
			if err := m.reg.MarkVersionMissing(id, true); err != nil {
				return false, err
			}
			m.log.Warn("reconcile: version artifact missing — kept as `missing`, not dropped", "id", id, "udid", udid)
		}
	}

	if err := m.recomputeLatest(udid); err != nil {
		return false, err
	}
	// Orphaned work is swept only after reconciliation has completed for the device.
	return false, s.Backend.SweepWork(udid)
}

// adopt registers an on-disk version discovered without a row as ADOPTED (job_id null →
// protected from retention until the user says otherwise; contracts §2).
func (m *Manager) adopt(s Slot, udid string, a Artifact) {
	created, _ := parseRFC(a.Marker.CreatedAt)
	row := store.VersionRow{
		ID: a.Marker.VersionID, UDID: udid, Backend: a.Backend, ZFSSnapshot: a.ZFSSnapshot,
		CreatedAt: created, JobID: nil, Kind: a.Marker.Kind, Encrypted: a.Marker.Encrypted,
		IsLatest: a.IsLatest, LogicalBytes: a.PhysicalBytes, PhysicalBytes: a.PhysicalBytes,
		// Attributed to the storage it was SCANNED FROM. An adopted version is found by walking a
		// specific root, so which storage it lives on is known here and never needs guessing.
		StorageID: slotIDPtr(s),
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
	// EVERY USABLE ROOT, not just the default's (story 5b). A device whose backups live only on
	// storage B has no row until it is adopted and no directory under A — so reading one root means
	// it is never reconciled and never adopted, which is the bug that hides a whole disk's worth of
	// backups from the UI.
	//
	// Unusable roots are skipped rather than read: an absent path yields no entries, and treating
	// that as "no devices here" is only correct by accident.
	for _, s := range m.slotsSnapshot() {
		if !s.Usable() {
			continue
		}
		entries, err := os.ReadDir(s.Root)
		if err != nil {
			continue
		}
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

// derefID renders a nullable storage id for a log line, so an unattributed row reads as "none"
// rather than as a pointer address.
func derefID(s *string) string {
	if s == nil {
		return "none"
	}
	return *s
}

// slotIDPtr renders a slot's storage id as the nullable the registry stores, so an unattributed
// storage inserts NULL rather than "" — the two are different states and "" is not one of them
// (contracts §2: null = not yet attributed).
func slotIDPtr(s Slot) *string {
	if s.StorageID == "" {
		return nil
	}
	id := s.StorageID
	return &id
}
