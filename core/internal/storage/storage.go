// Package storage is quince's version store (stack D5, design §5): it turns a
// structurally-verified idevicebackup2 tree into an immutable, listable, deletable version on
// one of four backends — zfs (snapshot-native), reflink / hardlink / copy (namespace-versioned)
// — auto-selected by a capability probe. It owns the journaled commit, on-disk
// quince-version.json markers, first-class startup reconciliation, adopted-version discovery,
// retention (Prune), and the structural-verification tree inspection. The invariant above all:
// a committed version is never mutated — the writer only ever touches the mutable working area,
// and latest/ changes only by a journaled atomic swap.
package storage

import "time"

// Backend names (contracts §2 Version.backend; config storage.backend).
const (
	BackendZFS      = "zfs"
	BackendReflink  = "reflink"
	BackendHardlink = "hardlink"
	BackendCopy     = "copy"
)

// VerifyResult is the outcome of structural verification (design §4, both encryption variants).
type VerifyResult struct {
	OK           bool
	Encrypted    bool
	Kind         string // full | incremental | unknown
	LogicalBytes int64  // best-effort tree size
	Detail       string // reason on failure, or a human note
}

// CommitReq drives one commit. VersionID + CreatedAt are minted by the subsystem; JobID is the
// job that produced the tree ("" for a manual/fixture commit → the subsystem substitutes the
// version id, so a committed version always carries a non-nil job_id; only reconciliation
// discovery yields job_id null = adopted).
type CommitReq struct {
	UDID      string
	JobID     string
	VersionID string
	CreatedAt time.Time
	Verify    VerifyResult
}

// Committed is a freshly promoted version, ready to row into the registry.
type Committed struct {
	VersionID           string
	UDID                string
	Backend             string
	ZFSSnapshot         *string
	CreatedAt           time.Time
	JobID               *string
	Kind                string
	Encrypted           bool
	StructureVerifiedAt time.Time
	LogicalBytes        int64
	PhysicalBytes       int64
}

// Artifact is a version discovered on disk by Scan (for reconciliation / adoption).
type Artifact struct {
	UDID          string
	Backend       string
	ZFSSnapshot   *string
	Marker        Marker
	IsLatest      bool
	PhysicalBytes int64
}

// Backend is one version model. All operations are idempotent and log their real commands
// (design §5). Backends live in this package, so the commit-step orchestration can share the
// journal helper while each model keeps its genuinely-different promotion.
type Backend interface {
	Name() string

	// Provision ensures the device's storage area exists (zfs: create the child dataset +
	// visibility probe; namespace: make latest/ versions/ work/).
	Provision(udid string) error

	// WorkDir returns the path the writer should write into for a job, seeded where the model
	// requires it (namespace: clone latest/ → work/<job>; zfs: working/, seeded by nature).
	// This is design §5's Seed. It is destructive (it re-seeds), so call it once per job.
	WorkDir(udid, jobID string) (string, error)

	// TreePath returns where a job's to-be-committed tree lives WITHOUT mutating it (namespace:
	// work/<job>; zfs: working/), so the subsystem can Verify before Commit.
	TreePath(udid, jobID string) string

	// Commit promotes a verified tree into an immutable version, journaled (design §5). Fresh
	// commits write and clear the journal; a crash leaves it for ResumeCommit.
	Commit(req CommitReq) (Committed, error)

	// ResumeCommit rolls a journal left by a crash forward to a consistent state (roll-forward
	// principle — never unwind a verified artifact). ok=false when there was nothing to finish.
	ResumeCommit(j Journal) (committed Committed, ok bool, err error)

	// Discard drops a failed job's work (namespace: rm work/<job>; zfs: leave dirty working/,
	// report the last good version). Returns a human note for the UI/log.
	Discard(udid, jobID string) (note string, err error)

	// DeleteArtifact removes a committed version's on-disk artifact (snapshot or dir).
	DeleteArtifact(a Artifact) error

	// RepairWorkingCopy rebuilds the mutable working area from the last good version (design §4
	// escape hatch). zfs: from the last snapshot's .zfs; namespace: reseed work from latest.
	RepairWorkingCopy(udid string) error

	// Scan enumerates versions present on disk for reconciliation/adoption.
	Scan(udid string) ([]Artifact, error)

	// PendingJournals returns commit journals left by a crash, across all devices.
	PendingJournals() ([]Journal, error)

	// SweepWork removes orphaned working areas for a device (design §5: swept only AFTER
	// reconciliation completes). namespace: rm work/<*>; zfs: no-op (working/ is the live copy).
	SweepWork(udid string) error
}
