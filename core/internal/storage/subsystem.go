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
	PromoteLatest(udid, id string) error
	ListVersions(udid string) ([]store.VersionRow, error)
	GetVersion(id string) (store.VersionRow, bool, error)
	DeleteVersion(id string) error
	MarkVersionMissing(id string, missing bool) error
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
	backend     Backend
	backendName string
	reg         Registry
	audit       Auditor
	bus         *bus.Bus
	backups     string
	log         *slog.Logger
	newID       func() string
	now         func() time.Time
	policy      RetentionPolicy
}

// NewManager wires the subsystem. audit may be nil (skipped).
func NewManager(backend Backend, name string, reg Registry, audit Auditor, b *bus.Bus,
	backups string, policy RetentionPolicy, newID func() string, log *slog.Logger) *Manager {
	return &Manager{
		backend: backend, backendName: name, reg: reg, audit: audit, bus: b,
		backups: backups, log: log, newID: newID, now: func() time.Time { return time.Now().UTC() },
		policy: policy,
	}
}

// BackendName reports the resolved backend (for /api/health + onboarding).
func (m *Manager) BackendName() string { return m.backendName }

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

// Seed provisions the device area (idempotent) and returns the writer's work dir (design §5).
func (m *Manager) Seed(udid, jobID string) (string, error) {
	if err := m.backend.Provision(udid); err != nil {
		return "", err
	}
	return m.backend.WorkDir(udid, jobID)
}

// CommitJob verifies the job's tree then commits it into an immutable version, rows it into the
// registry (registry_committed phase), publishes version.created, and runs a post-commit Prune
// (A3). The caller has already written the tree into the Seed work dir. A verification failure
// returns an error WITHOUT committing (state honesty — a version exists only after verify+commit).
func (m *Manager) CommitJob(udid, jobID string) (wire.Version, error) {
	tree := m.backend.TreePath(udid, jobID)
	vr := Verify(tree)
	if !vr.OK {
		return wire.Version{}, fmt.Errorf("storage: structural verification failed: %s", vr.Detail)
	}
	req := CommitReq{UDID: udid, JobID: jobID, VersionID: m.newID(), CreatedAt: m.now(), Verify: vr}
	committed, err := m.backend.Commit(req)
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
	}
	if err := m.reg.InsertVersion(row); err != nil {
		return err
	}
	return m.reg.PromoteLatest(c.UDID, c.VersionID)
}

// Discard drops a failed job's work (design §4). Returns the human note (dirty-working on zfs).
func (m *Manager) Discard(udid, jobID string) (string, error) {
	note, err := m.backend.Discard(udid, jobID)
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
		if err := m.backend.DeleteArtifact(m.artifact(row)); err != nil {
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
func (m *Manager) RepairWorkingCopy(udid string) error { return m.backend.RepairWorkingCopy(udid) }

// VerifyTree is the passwordless structural-verification tree half exposed to the backup engine
// (qn.4a): it runs Verify and returns primitives, so the backup package imports no storage types.
// The engine calls this for the `verifying` state; CommitJob re-runs it (cheap, quiescent tree).
func (m *Manager) VerifyTree(treeDir string) (ok bool, detail, kind string, encrypted bool) {
	r := Verify(treeDir)
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
	tree := browseRoot(m.backups, row.UDID, row.Backend, row.ZFSSnapshot, row.IsLatest, row.CreatedAt)
	r := Verify(tree)
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
	v := wire.Version{
		ID: r.ID, UDID: r.UDID, Backend: r.Backend, ZFSSnapshot: r.ZFSSnapshot,
		BrowseRoot: browseRoot(m.backups, r.UDID, r.Backend, r.ZFSSnapshot, r.IsLatest, r.CreatedAt),
		CreatedAt:  fmtRFC(r.CreatedAt), JobID: r.JobID, Kind: r.Kind, Encrypted: r.Encrypted,
		IsLatest: r.IsLatest, LogicalBytes: r.LogicalBytes, PhysicalBytes: r.PhysicalBytes,
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
