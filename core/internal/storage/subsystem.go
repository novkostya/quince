package storage

import (
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// THE WRITE PATH RESOLVES THE JOB'S STORAGE; WHAT CANNOT IS LISTED HERE (stories 5b, 6).
//
// Seed, PrepareWork, SeedWork, CommitJob, Discard, VerifyWork, registerCommitted and seedKind all
// take the job's slot — resolved from the jobID they already carry, or passed in. `POST /api/jobs`
// carries `storage_id` as of story 6b, so a backup lands on the storage the user chose.
//
// STILL DEFAULT-SCOPED, and each is now a STANDING fact rather than a deadline:
//
//	RepairWorkingCopy   device-scoped — no jobID to resolve, and the working copy it repairs
//	                    belongs to whichever job last wrote it. WRONG for a job on a non-default
//	                    storage; it needs a different key, not a different slot (unfixed).
//	BackendName         health + onboarding report the DEFAULT's backend, which is the question
//	                    being asked — correct rather than pending.
//
// THIS LIST HAS BEEN WRONG TWICE, IN THE DIFF THAT MADE IT WRONG. It carried `storageIDPtr` as a
// commit-path site after the commit path stopped using it, and it promised "seedKind becomes the
// job's slot with 6b" in the PR that shipped 6b without changing seedKind — a promise whose
// deadline was the diff it sat in (quince#447 review, twice). Both cost a review round.
//
// The rule it now lives under: A "BECOMES X WITH STORY N" LINE IS A BUG THE MOMENT STORY N IS IN
// THE SAME DIFF. If it cannot be true when the diff lands, it is not a note — it is an unfinished
// change wearing one.

// Registry is the version-persistence slice the subsystem needs (*store.Store satisfies it).
type Registry interface {
	InsertVersion(store.VersionRow) error
	PromoteLatest(udid, id string, storageID *string) error
	ListVersions(udid string) ([]store.VersionRow, error)
	GetVersion(id string) (store.VersionRow, bool, error)
	DeleteVersion(id string) error
	MarkVersionMissing(id string, missing bool) error
	UDIDsWithVersions() ([]string, error)
	AttributeVersion(id, storageID string) error
	// CountVersionsByStorage backs the storage card's counts (qn.6d gap A, ruled 2026-08-03).
	// One query for every storage rather than one per slot, because Storages() renders the whole
	// list on every call.
	CountVersionsByStorage() (map[string]store.StorageCounts, error)
}

// Auditor records the version-delete audit rows (*store.Store satisfies it). Detail never
// carries a secret (design §6).
type Auditor interface {
	AppendAudit(store.AuditEntry) error
}

// Manager owns the storage subsystem: it drives the chosen Backend, keeps the registry in sync,
// publishes version.* events, runs startup reconciliation, and enforces retention. It serves
// httpapi.VersionReader (Versions) + the version-delete admin path structurally.
type Manager struct {
	// slots is every storage this Manager speaks for, in declaration order. slots[0] is the
	// DEFAULT — the storage a backup goes to when none is named (contracts §1).
	//
	// qn.6c story 3: this replaced a single `backend` + `backups` pair. Every per-storage
	// operation now has to say WHICH storage, which is the point — the four reads that took
	// `m.backups` were exactly the places that could not, and would have resolved a version on
	// storage B against storage A's root.
	//
	// GUARDED BY mu. `RecheckStorage` rewrites one slot from an HTTP handler goroutine while the
	// backup, commit, reconcile and render paths read them. That is a genuine race and not a stale
	// read: `Slot` holds a Backend INTERFACE, so a torn value is an itab/data mismatch — a segfault,
	// not a wrong answer — and `Usable()`'s nil check does not help, because the check and the
	// dereference are separate reads of a value being written between them. Proven under `-race`
	// (quince#445 review), and reachable in production: *plug the disk in and press the button* is
	// something a user does WHILE a backup runs to another storage, which is the case the ruling
	// allowing serve-with-one-unreachable exists for.
	mu      sync.RWMutex
	slots   []Slot
	reg     Registry
	audit   Auditor
	bus     *bus.Bus
	log     *slog.Logger
	newID   func() string
	now     func() time.Time
	refresh Refresher

	// jobStorage maps jobID → storageID for jobs in flight. Guarded by mu.
	jobStorage map[string]string
}

// NewManager wires the subsystem. audit may be nil (skipped).
//
// slots is every storage this Manager speaks for, in declaration order; slots[0] is the DEFAULT.
//
// IT MAY BE EMPTY, AND THE CONSTRUCTION PANIC THAT SAID OTHERWISE IS GONE (qn.6e, Operator ruling
// 2026-08-07). That comment read: "It must be non-empty — config.CheckStorages refuses to serve
// without a declared storage, so a Manager with no slots is a programming error rather than a state
// to degrade through." Its premise stopped being true: CheckStorages no longer refuses a
// zero-storage start, because a first run has no `storage:` key at all and quince now serves so one
// can be added from the UI.
//
// REMOVING IT IS SAFE BECAUSE qn.6g ALREADY DID THE WORK, not because the risk was re-argued. That
// rung moved the non-empty guarantee FROM CONSTRUCTION TIME TO CALL TIME — see defaultSlot below —
// precisely because the list can be REPLACED while the process runs, so "non-empty when it was
// built" already said nothing about "non-empty now". Every reader guards, and movinglist_test.go
// gates all of them on zero: defaultSlot, BackendName, storageIDPtr, policyFor, Storages,
// RecheckStorage and ResolveChoice. The panic was the last remnant of an invariant its own callers
// had stopped relying on.
//
// ApplyStorages still refuses an EMPTY list, so a Manager cannot be MOVED to zero — only built that
// way, on a first run, before anything has been declared.
//
// THE GLOBAL RetentionPolicy PARAMETER IS GONE (quince#473): retention is per-storage, so it
// arrives on each Slot. A Manager-wide policy would have been a number the config file no longer
// has a place to say.
func NewManager(slots []Slot, reg Registry, audit Auditor, b *bus.Bus,
	newID func() string, log *slog.Logger) *Manager {
	return &Manager{
		slots: slots, reg: reg, audit: audit, bus: b,
		log: log, newID: newID, now: func() time.Time { return time.Now().UTC() },
	}
}

// defaultSlot is the storage a backup goes to when none is named. Declaration order decides it.
//
// THE NON-EMPTY GUARANTEE MOVES FROM CONSTRUCTION TIME TO CALL TIME (qn.6g, quince#577). It used to
// rest on `config.CheckStorages` refusing an empty list before a Manager was ever built, plus
// `NewManager` panicking on one — both one-shot checks at startup. Once the list can be REPLACED
// while the process runs, "non-empty when it was built" says nothing about "non-empty now", and an
// unguarded `m.slots[0]` is an index into a slice another goroutine may just have shortened.
//
// The `ok` return is what makes that checkable instead of a panic. `CheckStorages` still refuses an
// empty declaration, so false should be unreachable; this is the guard for when it is not.
func (m *Manager) defaultSlot() (Slot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.slots) == 0 {
		return Slot{}, false
	}
	return m.slots[0], true
}

// slotFor resolves the storage a VERSION lives on, from its storage_id.
//
// This is the resolver the four `m.backups` reads needed and did not have. A version on storage B
// resolved against storage A's root produces a browse_root that does not exist and a Verify that
// fails on a perfectly good backup — so "which root" must come from the ROW, never from whichever
// storage the Manager happens to list first.
//
// A nil storage_id resolves to the UNATTRIBUTED slot if one exists (the pre-qn.6c world, where
// there is one storage and no marker yet), and otherwise fails. Guessing a root for a version
// whose storage quince does not know is the same class of invention as claiming an unmounted
// mountpoint: it would answer confidently and be wrong wherever it mattered.
func (m *Manager) slotFor(storageID *string) (Slot, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	want := ""
	if storageID != nil {
		want = *storageID
	}
	for _, s := range m.slots {
		if s.StorageID == want {
			return s, true
		}
	}
	return Slot{}, false
}

// owns reports whether a version row belongs to the storage this Manager speaks for.
//
// STORY 5: this asks "is this row the DEFAULT slot's", which is the right question for one slot and
// the wrong one for several — a row on slots[1] comes back not-owned, reconcileDevice skips it, and
// nothing fails, because Scan never looked at that root either. Consistent today and silently wrong
// the moment buildStorage loops config.storages. It is the same shape as the `m.backups` reads this
// change is fixing, so the slice that adds the second slot must rewrite this to ask "does ANY slot
// own it, and which" (quince#433 review).
//
// It is GROUP MEMBERSHIP, not equality, and the NULL group is a real group — the same shape the
// is_latest ruling settled (quince#378): an unattributed Manager owns the unattributed rows, and
// an attributed one does not. Written once because three call sites need it and getting it subtly
// different in each is how the cross-storage bugs arise in the first place:
//
//	m.storageID  row.StorageID  owns   why
//	""           nil            YES    both unattributed — the pre-qn.6c world, one storage
//	""           set            no     the row knows where it lives; this Manager does not
//	set          nil            no     quince does not know where that version is, so this scan
//	                                   cannot conclude anything about it — skipping is the honest
//	                                   answer, and marking it missing would invent a fact
//	set          equal          YES
//	set          different      no     another storage's version; not ours to judge
//
// STORY 5b made this a SLOT method rather than a Manager one: "does this row belong to the storage
// I am currently reconciling" has no answer at the Manager level once there is more than one, and
// the version that resolved through `defaultSlot()` was the marker quince#433 left here.
func (s Slot) owns(rowStorageID *string) bool {
	if rowStorageID == nil {
		return s.StorageID == ""
	}
	return *rowStorageID == s.StorageID
}

// storageIDPtr returns the Manager's storage id as the nullable the registry stores, so an
// unconfigured Manager inserts NULL rather than "" — the two are different states on the wire and
// "" is not one of them (contracts §2: null = not yet attributed).
func (m *Manager) storageIDPtr() *string {
	// ONE read, not two. It called defaultSlot() twice — a re-read across which the list can now
	// change, so the value tested and the value returned were not guaranteed to be the same one.
	d, ok := m.defaultSlot()
	if !ok || d.StorageID == "" {
		return nil
	}
	s := d.StorageID
	return &s
}

// BackendName reports the resolved backend (for /api/health + onboarding).
//
// "unknown" on an empty list, which is the value the wire already uses for a storage whose backend
// quince has not determined (`storageToWire`). A caller asking what backend is in use when there is
// no storage gets an answer that is true rather than an empty string that reads as "none".
func (m *Manager) BackendName() string {
	d, ok := m.defaultSlot()
	if !ok {
		return "unknown"
	}
	return d.BackendName
}

// KnownUDIDs returns the distinct UDIDs that have any committed version — the offline-device set the
// device registry unions with live presence so a powered-off device that has backups is still listed
// (qn.6a). Errors degrade to empty (a failed lookup must not blank the live device table).
func (m *Manager) KnownUDIDs() []string {
	udids, err := m.reg.UDIDsWithVersions()
	if err != nil {
		m.log.Error("storage: known-udids lookup failed", "error", err)
		return nil
	}
	return udids
}

// LastBackup summarizes a device's most recent SUCCESSFUL backup for Device.last_backup
// (contracts §2, ratified (bz); qn.4a finding (v)). Versions — not job rows — are the source of
// truth for "has this device been backed up": a version exists ONLY after verify + commit, it
// outlives the process that made it, and it covers ADOPTED versions (a dataset replicated or
// restored to a fresh host, or quince reinstalled over existing backups), which have no job at
// all — hence a nil JobID rather than a fabricated one. Versions the registry knows are MISSING
// on disk are skipped: claiming a backup whose artifact is gone would be exactly the overclaim
// this project forbids. ok=false → the device honestly has no backups ("No backups yet").
func (m *Manager) LastBackup(udid string) (wire.LastBackup, bool) {
	rows, err := m.reg.ListVersions(udid) // newest first
	if err != nil {
		m.log.Error("storage: last-backup lookup failed", "udid", udid, "error", err)
		return wire.LastBackup{}, false
	}
	for _, r := range rows {
		if r.Missing {
			continue
		}
		return wire.LastBackup{At: fmtRFC(r.CreatedAt), JobID: r.JobID, Status: "succeeded"}, true
	}
	return wire.LastBackup{}, false
}

// Versions implements httpapi.VersionReader (contracts §1 GET /api/versions). Reads the
// registry (indexed, no fs walk on the hot path — perf budget) and maps to the wire shape.
func (m *Manager) Versions(udid string) []wire.Version {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		m.log.Error("storage: list versions failed", "error", err)
		return []wire.Version{}
	}
	out := make([]wire.Version, 0, len(rows))
	for _, r := range rows {
		out = append(out, m.toWire(r))
	}
	return out
}

// Seed provisions the device area (idempotent) and returns the idevicebackup2 TARGET — the

// per-device working/ parent, seeded so the tool's own <target>/<UDID> convention lands the tree in
// working/<udid> with no symlink (qn.5b). A dirty working/ is resumed; else it is seeded from
// latest/ via the backend's safe strategy.
func (m *Manager) Seed(udid, jobID string) (string, error) {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return "", err
	}
	// BEFORE ANYTHING TOUCHES THE PATH (story 7). Provision creates directories, so a check
	// after it would be a check downstream of the thing that makes it pass — the same ordering
	// defect quince#415 fixed for the creation guard.
	if err := s.preflight(); err != nil {
		return "", err
	}
	if err := s.Backend.Provision(udid); err != nil {
		return "", err
	}
	return s.Backend.WorkDir(udid, jobID)
}

// PrepareWork + SeedWork are Seed split into two phases for the qn.6b gated seed (candidate C):
// PrepareWork provisions + does the fast resume-or-prepare (reporting whether a clone is pending);
// SeedWork does the slow clone while idevicebackup2 is paused at its --gate. Seed = PrepareWork +
// (if seedPending) SeedWork.
func (m *Manager) PrepareWork(udid, jobID string) (string, bool, error) {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return "", false, err
	}
	if err := s.preflight(); err != nil {
		return "", false, err
	}
	if err := s.Backend.Provision(udid); err != nil {
		return "", false, err
	}
	return s.Backend.PrepareWork(udid, jobID)
}

func (m *Manager) SeedWork(udid, jobID string) error {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return err
	}
	return s.Backend.SeedWork(udid, jobID)
}

// seedKind returns the AUTHORITATIVE full|incremental kind for the in-flight job from the work
// sentinel (whether working/ was seeded from an existing latest/ — finding #9(a), (cj)/(ck)); if
// the sentinel is missing it infers from whether the device already has a committed version, never
// from Status.plist.IsFullBackup (which the lab proved lies).
func (m *Manager) seedKind(s Slot, udid string) string {
	if w, ok, err := readWorkStateAt(workSentinelFor(s.BackendName, s.Root, udid)); err == nil && ok {
		return w.kindOf()
	}
	// SCOPED TO THIS STORAGE, and this is the line story 8's claim rests on. "Does this device
	// already have a version" asked across ALL storages reports `incremental` for a FIRST backup to
	// a NEW storage — which is a full transfer. Saying otherwise corrupts Version.kind AND
	// pre-empts the warning the user is owed before tens of gigabytes move.
	//
	// A NULL storage_id is not evidence about this storage: quince does not know where that
	// version lives, and "somewhere" is not "here".
	if rows, err := m.reg.ListVersions(udid); err == nil {
		for _, r := range rows {
			if r.Missing || !s.owns(r.StorageID) {
				continue
			}
			return "incremental"
		}
	}
	return "full"
}

// CommitJob verifies the job's tree then commits it into an immutable version, rows it into the
// registry (registry_committed phase), publishes version.created, and runs a post-commit Prune
// (A3). The caller has already written the tree into the Seed target (working/<udid>). A
// verification failure returns an error WITHOUT committing (state honesty — a version exists only
// after verify+commit).
func (m *Manager) CommitJob(udid, jobID string) (wire.Version, error) {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return wire.Version{}, err
	}
	tree := s.Backend.TreePath(udid, jobID)
	vr := Verify(tree, m.seedKind(s, udid))
	if !vr.OK {
		return wire.Version{}, fmt.Errorf("storage: structural verification failed: %s", vr.Detail)
	}
	req := CommitReq{UDID: udid, JobID: jobID, VersionID: m.newID(), CreatedAt: m.now(), Verify: vr}
	committed, err := s.Backend.Commit(req)
	if err != nil {
		return wire.Version{}, err
	}
	if err := m.registerCommitted(s, committed); err != nil {
		return wire.Version{}, err
	}
	row, _, _ := m.reg.GetVersion(committed.VersionID)
	v := m.toWire(row)
	m.bus.PublishEvent(wire.EventVersionCreated, v)
	if err := m.Prune(udid); err != nil {
		m.log.Warn("storage: post-commit prune failed", "udid", udid, "error", err)
	}
	return v, nil
}

// registerCommitted rows a committed version and makes it the sole latest (registry_committed).
func (m *Manager) registerCommitted(s Slot, c Committed) error {
	sv := c.StructureVerifiedAt
	row := store.VersionRow{
		ID: c.VersionID, UDID: c.UDID, Backend: c.Backend, ZFSSnapshot: c.ZFSSnapshot,
		CreatedAt: c.CreatedAt, JobID: c.JobID, Kind: c.Kind, Encrypted: c.Encrypted,
		IsLatest: true, StructureVerifiedAt: &sv, LogicalBytes: c.LogicalBytes, PhysicalBytes: c.PhysicalBytes,
		// Attributed AT COMMIT. Before this, a freshly committed version was inserted NULL and
		// only picked up by the next startup sweep — so between a backup and a restart the wire
		// said "not yet attributed" about a version quince had just written itself.
		//
		// FROM THE SLOT THE TREE WAS WRITTEN TO, not from the default (quince#447 review). While
		// this read `m.storageIDPtr()` a backup to a named non-default storage wrote its tree to B
		// and recorded it on A — and reconciliation then made it worse rather than repairing it:
		// browse_root resolved to a path that does not exist, Verify reported a good backup broken,
		// and A's next scan marked the row `missing` while B's correctly left it alone. A confidently
		// wrong row, which the NULL-filling sweep never touches.
		StorageID: slotIDPtr(s),
	}
	if err := m.reg.InsertVersion(row); err != nil {
		return err
	}
	// Promoted in the SAME group the row was recorded in — is_latest is per (device, storage), so
	// promoting in the default's group would leave B's newest version never becoming B's latest.
	return m.reg.PromoteLatest(c.UDID, c.VersionID, slotIDPtr(s))
}

// Discard drops a failed job's work (design §4). Returns the human note (dirty-working on zfs).
func (m *Manager) Discard(udid, jobID string) (string, error) {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return "", err
	}
	note, err := s.Backend.Discard(udid, jobID)
	if note != "" {
		m.log.Info("storage: job discarded", "udid", udid, "job", jobID, "note", note)
	}
	return note, err
}

// Delete removes a version (contracts §1 DELETE /api/versions/{id} → 202, confirmed
// destructive). Returns an HTTP status for the handler: 202 on success, 404 unknown, 500 error.
func (m *Manager) Delete(id string) (int, error) {
	return m.deleteVersion(id, "version.delete")
}

func (m *Manager) deleteVersion(id, event string) (int, error) {
	row, ok, err := m.reg.GetVersion(id)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	if !ok {
		return http.StatusNotFound, fmt.Errorf("no such version")
	}
	if !row.Missing {
		// THE VERSION'S OWN STORAGE DELETES IT, never the default (story 5b).
		//
		// This read `m.defaultSlot().Backend` — the same defect as the browse_root one quince#433
		// fixed, except DESTRUCTIVE. A backend resolves paths against its own root, so deleting
		// storage B's version through storage A's backend either fails to find it, or removes
		// whatever sits at the corresponding path under A. It could not fire while one storage was
		// declared; it becomes live with the loop this slice adds, which is why it is fixed here
		// rather than filed.
		slot, ok := m.slotFor(row.StorageID)
		if !ok {
			return http.StatusConflict, fmt.Errorf(
				"this version is not attributed to any configured storage, so quince cannot say " +
					"which disk its data is on — refusing to delete rather than guessing")
		}
		if !slot.Usable() {
			return http.StatusConflict, fmt.Errorf(
				"the storage holding this version is not reachable (%s) — plug it in and try again; "+
					"quince will not remove the registry row while its artifact cannot be removed too",
				slot.UnreachableCode)
		}
		if err := slot.Backend.DeleteArtifact(m.artifact(row)); err != nil {
			return http.StatusInternalServerError, err
		}
	}
	if err := m.reg.DeleteVersion(id); err != nil {
		return http.StatusInternalServerError, err
	}
	m.appendAudit(event, row.UDID+" "+id+" deleted")
	m.bus.PublishEvent(wire.EventVersionDeleted, m.toWire(row))
	m.log.Info("storage: version deleted", "id", id, "udid", row.UDID, "event", event)
	return http.StatusAccepted, nil
}

// Prune applies each storage's retention policy to a device's versions (post-commit + on demand;
// NO scheduler this rung — A3). Deletes only quince-created non-latest versions; adopted protected.
//
// GROUPED BY STORAGE (qn.6c, quince#473). Retention became per-storage when `storage:` flattened,
// and applying one policy across a device's whole history would mean a second disk silently
// changed what the first one keeps — `keep_recent: 10` has to mean ten ON THAT DISK, or the
// number in the config file is not the number the user gets.
//
// A version with NO storage_id is UNATTRIBUTED, which is transitional rather than permanent: it
// means reconciliation has not yet worked out which storage the artifact is under. Those rows are
// pruned under the DEFAULT storage's policy, because that is the only policy that exists for a
// version whose storage is unknown — and pruning them under nobody's policy would mean never
// pruning them at all, which is a silent unbounded keep.
func (m *Manager) Prune(udid string) error {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return err
	}
	byStorage := map[string][]store.VersionRow{}
	for _, r := range rows {
		key := ""
		if r.StorageID != nil {
			key = *r.StorageID
		}
		byStorage[key] = append(byStorage[key], r)
	}
	for key, group := range byStorage {
		policy, ok := m.policyFor(key)
		if !ok {
			// A version attributed to a storage this process does not have declared — the entry
			// was removed from config.yml, or the disk's storage is no longer listed. Pruning it
			// under some other storage's policy would delete versions using a number the user
			// never wrote for them, so it is skipped and SAID (no silent caps).
			m.log.Warn("prune skipped — this version's storage is not declared here",
				"udid", udid, "storage_id", key, "versions", len(group))
			continue
		}
		for _, r := range selectPrunable(group, policy) {
			if status, err := m.deleteVersion(r.ID, "version.prune"); err != nil {
				return fmt.Errorf("prune %s (status %d): %w", r.ID, status, err)
			}
		}
	}
	return nil
}

// policyFor returns the retention policy for a storage id, and whether one applies. The empty id
// is an unattributed version and resolves to the default slot's policy — see Prune.
func (m *Manager) policyFor(storageID string) (RetentionPolicy, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if storageID == "" {
		if len(m.slots) == 0 {
			return RetentionPolicy{}, false
		}
		return m.slots[0].Retention, true
	}
	for _, s := range m.slots {
		if s.StorageID == storageID {
			return s.Retention, true
		}
	}
	return RetentionPolicy{}, false
}

// RepairWorkingCopy rebuilds the mutable working area from the last good version (design §4).
//
// Guarded twice, and the second is the sharper one: an empty list has no slot, and an UNUSABLE slot
// has a NIL Backend (`Slot.Reachable`'s doc: "BACKEND IS NIL WHEN THIS IS FALSE"). The old form
// dereferenced it unconditionally, which was safe only while nothing could replace a reachable slot
// with an unreachable one mid-flight — exactly the assumption this rung removes.
func (m *Manager) RepairWorkingCopy(udid string) error {
	d, ok := m.defaultSlot()
	if !ok {
		return fmt.Errorf("repair working copy for %s: no storage is declared", udid)
	}
	if !d.Usable() {
		return fmt.Errorf("repair working copy for %s: the default storage %q is not reachable (%s): %s",
			udid, d.Name, d.UnreachableCode, d.UnreachableReason)
	}
	return d.Backend.RepairWorkingCopy(udid)
}

// VerifyWork is the passwordless structural-verification exposed to the backup engine (qn.4a/qn.5b):
// it resolves the job's working tree (working/<udid>) internally and returns primitives, so the
// backup package imports no storage types. The kind is the AUTHORITATIVE seed-derived value
// (finding #9(a)). The engine calls this for the `verifying` state; CommitJob re-runs it (cheap,
// quiescent tree).
func (m *Manager) VerifyWork(udid, jobID string) (ok bool, detail, kind string, encrypted bool) {
	s, err := m.jobSlot(jobID)
	if err != nil {
		return false, err.Error(), "", false
	}
	tree := s.Backend.TreePath(udid, jobID)
	r := Verify(tree, m.seedKind(s, udid))
	return r.OK, r.Detail, r.Kind, r.Encrypted
}

// VerifyReport is the outcome of an on-demand `versions verify` (the qn.4b CLI escape hatch): the
// STRUCTURAL, passwordless verification of a committed version's tree. Content verification (the
// vault canary + encrypted-manifest record sampling) is qn.8's and is NOT run here — state honesty:
// this reports the structural level only.
type VerifyReport struct {
	VersionID string
	UDID      string
	OK        bool
	Detail    string
	Kind      string
	Encrypted bool
	TreePath  string
}

// VerifyVersion re-runs the passwordless structural Verify on a committed version's tree
// (CLI `quince versions verify <id>`). ok=false when the version is unknown. It resolves the tree
// via browseRoot — the same path contracts §2 exposes as Version.browse_root — so it works for the
// latest, archived namespace versions, and zfs snapshots alike, with NO new backend surface. A
// version marked missing on disk reports OK:false honestly rather than opening a phantom path.
func (m *Manager) VerifyVersion(id string) (VerifyReport, bool) {
	row, ok, err := m.reg.GetVersion(id)
	if err != nil || !ok {
		return VerifyReport{}, false
	}
	rep := VerifyReport{VersionID: id, UDID: row.UDID}
	if row.Missing {
		rep.Detail = "version artifact is missing on disk"
		return rep, true
	}
	// The root comes from THE VERSION'S storage, not from whichever the Manager lists first.
	// Verifying storage B's version against storage A's root reports a perfectly good backup as
	// broken — the state-honesty failure this resolver exists to prevent.
	slot, ok := m.slotFor(row.StorageID)
	if !ok {
		rep.Detail = "version is not attributed to any configured storage — cannot locate its tree"
		return rep, true
	}
	tree := browseRoot(slot.Root, row.UDID, row.Backend, row.ZFSSnapshot, row.IsLatest, row.CreatedAt)
	if tree == "" {
		// zfs, with no snapshot on the row. browseRoot refuses rather than falling through to the
		// live tree, so name WHICH thing is missing: verifying "" fails anyway, but with a message
		// about an unreadable path — which reads like a broken disk rather than an unlocatable
		// version, and sends the reader to the wrong place.
		rep.Detail = "version has no snapshot recorded — its content cannot be located, and the live tree is not a version"
		return rep, true
	}
	r := Verify(tree, row.Kind)
	rep.OK, rep.Detail, rep.Kind, rep.Encrypted, rep.TreePath = r.OK, r.Detail, r.Kind, r.Encrypted, tree
	return rep, true
}

// VerifyLatest verifies a device's current latest version (CLI `versions verify --udid <udid>`).
// ok=false when the device has no committed version. Reuses VerifyVersion for the resolution.
func (m *Manager) VerifyLatest(udid string) (VerifyReport, bool) {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return VerifyReport{}, false
	}
	for _, r := range rows {
		if r.IsLatest {
			return m.VerifyVersion(r.ID)
		}
	}
	return VerifyReport{}, false
}

// VersionForJob reports the version id a job committed, if any — used by qn.4a's startup job-row
// reconciliation to distinguish a commit that rolled forward (→ succeeded) from a true orphan
// (→ connection_lost). Reads the registry (indexed by udid), never the fs.
func (m *Manager) VersionForJob(udid, jobID string) (string, bool) {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return "", false
	}
	for _, r := range rows {
		if r.JobID != nil && *r.JobID == jobID {
			return r.ID, true
		}
	}
	return "", false
}

// --- mapping helpers ---

func (m *Manager) toWire(r store.VersionRow) wire.Version {
	// browse_root resolves under THE VERSION'S OWN storage root — the sentence story 4's spec
	// promised and that this function could not honour while it held one root. A version on
	// storage B rendered against storage A's root hands the UI a path that does not exist.
	//
	// A version attributed to no configured storage yields "" rather than a guessed path: the
	// field is a non-nullable string, and an empty one is visibly wrong where a plausible-looking
	// wrong path is not.
	//
	// The EMPTINESS HAS TO BE BUILT HERE, not by passing "" down. browseRoot composes with
	// filepath.Join, which DROPS empty elements — so an empty root does not produce an empty path,
	// it produces a RELATIVE one ("<udid>/latest"), well-formed and right in every part except the
	// one that says which disk. That is more misleading than the wrong-absolute-path case this
	// resolver exists to prevent, because a wrong absolute path is at least somewhere real
	// (quince#433 review).
	browse := ""
	if slot, ok := m.slotFor(r.StorageID); ok {
		browse = browseRoot(slot.Root, r.UDID, r.Backend, r.ZFSSnapshot, r.IsLatest, r.CreatedAt)
	}
	v := wire.Version{
		ID: r.ID, UDID: r.UDID, Backend: r.Backend, ZFSSnapshot: r.ZFSSnapshot,
		BrowseRoot: browse,
		CreatedAt:  fmtRFC(r.CreatedAt), JobID: r.JobID, Kind: r.Kind, Encrypted: r.Encrypted,
		IsLatest: r.IsLatest, LogicalBytes: r.LogicalBytes, PhysicalBytes: r.PhysicalBytes,
		Missing: r.Missing, // crossed to the wire so the UI renders a gone artifact dead (qn.6a (cr))
		// nil until this version's storage has an identity marker (qn.6c). PASSED THROUGH rather
		// than defaulted: substituting a value here would turn "not yet attributed" into a claim,
		// which is the state-honesty failure the nullable ruling exists to avoid.
		StorageID: r.StorageID,
	}
	if r.StructureVerifiedAt != nil {
		s := fmtRFC(*r.StructureVerifiedAt)
		v.StructureVerifiedAt = &s
	}
	if r.ContentVerifiedAt != nil {
		s := fmtRFC(*r.ContentVerifiedAt)
		v.ContentVerifiedAt = &s
	}
	return v
}

func (m *Manager) artifact(r store.VersionRow) Artifact {
	return Artifact{
		UDID: r.UDID, Backend: r.Backend, ZFSSnapshot: r.ZFSSnapshot, IsLatest: r.IsLatest,
		Marker: Marker{CreatedAt: fmtRFC(r.CreatedAt), UDID: r.UDID, VersionID: r.ID},
	}
}

func (m *Manager) appendAudit(event, detail string) {
	if m.audit == nil {
		return
	}
	if err := m.audit.AppendAudit(store.AuditEntry{
		ID: m.newID(), TS: m.now(), Event: event, Detail: detail,
	}); err != nil {
		m.log.Warn("storage: audit append failed", "event", event, "error", err)
	}
}

// slotsSnapshot copies the slot list under the read lock.
//
// Callers that iterate — Reconcile, reconcileUDIDs, Storages — take a copy rather than holding the
// lock across their bodies, because those bodies do filesystem and database work. A read lock held
// across I/O does not block other readers but does block the recheck writer for the duration, which
// turns "press the button" into "press the button and wait for a scan".
func (m *Manager) slotsSnapshot() []Slot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Slot, len(m.slots))
	copy(out, m.slots)
	return out
}
