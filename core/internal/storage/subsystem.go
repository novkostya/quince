package storage

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Registry is the version-persistence slice the subsystem needs (*store.Store satisfies it).
type Registry interface {
	InsertVersion(store.VersionRow) error
	PromoteLatest(udid, id string, storageID *string) error
	ListVersions(udid string) ([]store.VersionRow, error)
	GetVersion(id string) (store.VersionRow, bool, error)
	DeleteVersion(id string) error
	MarkVersionMissing(id string, missing bool) error
	UDIDsWithVersions() ([]string, error)
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
	slots  []Slot
	reg    Registry
	audit  Auditor
	bus    *bus.Bus
	log    *slog.Logger
	newID  func() string
	now    func() time.Time
	policy RetentionPolicy
}

// NewManager wires the subsystem. audit may be nil (skipped).
//
// slots is every storage this Manager speaks for, in declaration order; slots[0] is the DEFAULT.
// It must be non-empty — `config.CheckStorages` refuses to serve without a declared storage, so a
// Manager with no slots is a programming error rather than a state to degrade through, and the
// panic says so rather than producing an index-out-of-range three calls later.
func NewManager(slots []Slot, reg Registry, audit Auditor, b *bus.Bus,
	policy RetentionPolicy, newID func() string, log *slog.Logger) *Manager {
	if len(slots) == 0 {
		panic("storage: NewManager needs at least one Slot — config.CheckStorages should have refused first")
	}
	return &Manager{
		slots: slots, reg: reg, audit: audit, bus: b,
		log: log, newID: newID, now: func() time.Time { return time.Now().UTC() },
		policy: policy,
	}
}

// defaultSlot is the storage a backup goes to when none is named. Declaration order decides it,
// and `config.CheckStorages` guarantees the list is non-empty before a Manager is ever built.
func (m *Manager) defaultSlot() Slot { return m.slots[0] }

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
func (m *Manager) owns(rowStorageID *string) bool {
	if rowStorageID == nil {
		return m.defaultSlot().StorageID == ""
	}
	return *rowStorageID == m.defaultSlot().StorageID
}

// storageIDPtr returns the Manager's storage id as the nullable the registry stores, so an
// unconfigured Manager inserts NULL rather than "" — the two are different states on the wire and
// "" is not one of them (contracts §2: null = not yet attributed).
func (m *Manager) storageIDPtr() *string {
	if m.defaultSlot().StorageID == "" {
		return nil
	}
	s := m.defaultSlot().StorageID
	return &s
}

// BackendName reports the resolved backend (for /api/health + onboarding).
func (m *Manager) BackendName() string { return m.defaultSlot().BackendName }

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
	if err := m.defaultSlot().Backend.Provision(udid); err != nil {
		return "", err
	}
	return m.defaultSlot().Backend.WorkDir(udid, jobID)
}

// PrepareWork + SeedWork are Seed split into two phases for the qn.6b gated seed (candidate C):
// PrepareWork provisions + does the fast resume-or-prepare (reporting whether a clone is pending);
// SeedWork does the slow clone while idevicebackup2 is paused at its --gate. Seed = PrepareWork +
// (if seedPending) SeedWork.
func (m *Manager) PrepareWork(udid, jobID string) (string, bool, error) {
	if err := m.defaultSlot().Backend.Provision(udid); err != nil {
		return "", false, err
	}
	return m.defaultSlot().Backend.PrepareWork(udid, jobID)
}

func (m *Manager) SeedWork(udid, jobID string) error {
	return m.defaultSlot().Backend.SeedWork(udid, jobID)
}

// seedKind returns the AUTHORITATIVE full|incremental kind for the in-flight job from the work
// sentinel (whether working/ was seeded from an existing latest/ — finding #9(a), (cj)/(ck)); if
// the sentinel is missing it infers from whether the device already has a committed version, never
// from Status.plist.IsFullBackup (which the lab proved lies).
func (m *Manager) seedKind(udid string) string {
	if w, ok, err := readWorkState(m.defaultSlot().Root, udid); err == nil && ok {
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
			if r.Missing || !m.owns(r.StorageID) {
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
	tree := m.defaultSlot().Backend.TreePath(udid, jobID)
	vr := Verify(tree, m.seedKind(udid))
	if !vr.OK {
		return wire.Version{}, fmt.Errorf("storage: structural verification failed: %s", vr.Detail)
	}
	req := CommitReq{UDID: udid, JobID: jobID, VersionID: m.newID(), CreatedAt: m.now(), Verify: vr}
	committed, err := m.defaultSlot().Backend.Commit(req)
	if err != nil {
		return wire.Version{}, err
	}
	if err := m.registerCommitted(committed); err != nil {
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
func (m *Manager) registerCommitted(c Committed) error {
	sv := c.StructureVerifiedAt
	row := store.VersionRow{
		ID: c.VersionID, UDID: c.UDID, Backend: c.Backend, ZFSSnapshot: c.ZFSSnapshot,
		CreatedAt: c.CreatedAt, JobID: c.JobID, Kind: c.Kind, Encrypted: c.Encrypted,
		IsLatest: true, StructureVerifiedAt: &sv, LogicalBytes: c.LogicalBytes, PhysicalBytes: c.PhysicalBytes,
		// Attributed AT COMMIT. Before this, a freshly committed version was inserted NULL and
		// only picked up by the next startup sweep — so between a backup and a restart the wire
		// said "not yet attributed" about a version quince had just written itself.
		StorageID: m.storageIDPtr(),
	}
	if err := m.reg.InsertVersion(row); err != nil {
		return err
	}
	return m.reg.PromoteLatest(c.UDID, c.VersionID, m.storageIDPtr())
}

// Discard drops a failed job's work (design §4). Returns the human note (dirty-working on zfs).
func (m *Manager) Discard(udid, jobID string) (string, error) {
	note, err := m.defaultSlot().Backend.Discard(udid, jobID)
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
		if err := m.defaultSlot().Backend.DeleteArtifact(m.artifact(row)); err != nil {
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

// Prune applies the retention policy to a device's versions (post-commit + on demand; NO
// scheduler this rung — A3). Deletes only quince-created non-latest versions; adopted protected.
func (m *Manager) Prune(udid string) error {
	rows, err := m.reg.ListVersions(udid)
	if err != nil {
		return err
	}
	for _, r := range selectPrunable(rows, m.policy) {
		if status, err := m.deleteVersion(r.ID, "version.prune"); err != nil {
			return fmt.Errorf("prune %s (status %d): %w", r.ID, status, err)
		}
	}
	return nil
}

// RepairWorkingCopy rebuilds the mutable working area from the last good version (design §4).
func (m *Manager) RepairWorkingCopy(udid string) error {
	return m.defaultSlot().Backend.RepairWorkingCopy(udid)
}

// VerifyWork is the passwordless structural-verification exposed to the backup engine (qn.4a/qn.5b):
// it resolves the job's working tree (working/<udid>) internally and returns primitives, so the
// backup package imports no storage types. The kind is the AUTHORITATIVE seed-derived value
// (finding #9(a)). The engine calls this for the `verifying` state; CommitJob re-runs it (cheap,
// quiescent tree).
func (m *Manager) VerifyWork(udid, jobID string) (ok bool, detail, kind string, encrypted bool) {
	tree := m.defaultSlot().Backend.TreePath(udid, jobID)
	r := Verify(tree, m.seedKind(udid))
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
