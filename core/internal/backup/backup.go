// Package backup is quince's transport-agnostic backup engine (stack D3/D13, design §4). It
// drives idevicebackup2 as a supervised streaming subprocess through the frozen job state
// machine — queued → waiting_for_device → preflight → backing_up → verifying → committing →
// succeeded, with failed/cancelled/connection_lost terminals — and hands the produced tree to
// qn.5 storage (Seed → Verify → Commit, or Discard on failure). The invariant above all: the
// writer only ever touches the storage work area; a version exists only after verify + commit
// (state honesty). The engine is Wi-Fi-shaped from day one (it replays the Wi-Fi torn transcripts
// in CI); qn.4b makes Wi-Fi first-class and resolves transport: auto.
package backup

import (
	"time"

	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Transports (contracts §2). auto is resolved in qn.4b — qn.4a accepts usb|wifi only.
const (
	TransportUSB  = "usb"
	TransportWiFi = "wifi"
	TransportAuto = "auto"
)

// Job states (contracts §2, design §4).
const (
	StateQueued           = "queued"
	StateWaitingForDevice = "waiting_for_device"
	StatePreflight        = "preflight"
	StateBackingUp        = "backing_up"
	StateVerifying        = "verifying"
	StateCommitting       = "committing"
	StateSucceeded        = "succeeded"
	StateFailed           = "failed"
	StateCancelled        = "cancelled"
	StateConnectionLost   = "connection_lost"
)

// Liveness values (contracts §2 JobProgress.liveness; design §4 staged stall).
const (
	LivenessActive          = "active"
	LivenessSilentConnected = "silent_but_connected"
	LivenessSuspectedStall  = "suspected_stall"
)

// Progress phases.
const (
	PhaseStarting           = "starting"
	PhaseReceiving          = "receiving"
	PhaseWaitingForPasscode = "waiting_for_passcode"
	PhaseDone               = "done"
)

// Error codes surfaced on Job.error (contracts §2).
const (
	ErrDeviceDisconnected = "device_disconnected"
	ErrDeviceNotVisible   = "device_not_visible"
	ErrNotPaired          = "not_paired"
	ErrEncryptionRequired = "encryption_required"
	ErrDiskLow            = "disk_low"
	ErrVerifyFailed       = "verify_failed"
	ErrCommitFailed       = "commit_failed"
	ErrBackupFailed       = "backup_failed"
	ErrInterrupted        = "interrupted"
	ErrCancelled          = "cancelled"
)

// Config holds the engine's tunables — code constants (design §4; NOT v0.1 config keys, D12).
// Tests inject small durations. Values are the Named constants recorded in the qn.4a spec.
type Config struct {
	LivenessTimeout      time.Duration // zero-activity → connection_lost (15m; "tuned in qn.7")
	SampleInterval       time.Duration // activity-sampler tick
	WaitForDeviceTimeout time.Duration // waiting_for_device bound (amendment 2: 60s)
	ProgressThrottle     time.Duration // ≤2/s job.updated progress throttle (500ms)
	DiskLowFreeBytes     uint64        // A3 free-space floor (2 GiB); warn + preflight-refuse
	RequireEncryption    bool          // backup.require_encryption
}

const gib = 1 << 30

// DefaultConfig returns the production tunables (spec Named constants).
func DefaultConfig() Config {
	return Config{
		LivenessTimeout:      15 * time.Minute,
		SampleInterval:       2 * time.Second,
		WaitForDeviceTimeout: 60 * time.Second,
		ProgressThrottle:     500 * time.Millisecond,
		DiskLowFreeBytes:     2 * gib,
		RequireEncryption:    true,
	}
}

// JobStore is the persistence slice the engine needs (*store.Store satisfies it). Every job
// transition is persisted BEFORE its event is emitted (design §2 job engine — crash-safe).
type JobStore interface {
	InsertJob(store.JobRow) error
	UpdateJob(store.JobRow) error
	GetJob(id string) (store.JobRow, bool, error)
	ListJobs(udid, cursor string, limit int) ([]store.JobRow, string, error)
	ListNonTerminalJobs() ([]store.JobRow, error)
}

// Storage is the qn.5 seam the engine drives (*storage.Manager satisfies it via thin methods).
// The engine imports no storage internals — CommitJob already returns the frozen wire.Version,
// and VerifyTree returns primitives, so there is no import edge into the storage package.
type Storage interface {
	// Seed provisions the device area and returns the writer's work dir (== the tree path).
	Seed(udid, jobID string) (workDir string, err error)
	// VerifyTree is the passwordless structural verification (storage.Verify) — the tree half.
	VerifyTree(treeDir string) (ok bool, detail, kind string, encrypted bool)
	// CommitJob verifies + commits the job's tree into an immutable version (returns it).
	CommitJob(udid, jobID string) (wire.Version, error)
	// Discard drops a failed job's work (returns a human note, e.g. dirty-working on zfs).
	Discard(udid, jobID string) (note string, err error)
}

// Devices reports device presence + encryption state for preflight (*device.Registry satisfies
// it via the frozen wire.Device it already serves — no device-package import edge).
type Devices interface {
	Device(udid string) (wire.Device, bool)
}

// jobToWire maps a stored row to the frozen Job shape (contracts §2).
func jobToWire(r store.JobRow) wire.Job {
	j := wire.Job{
		ID: r.ID, UDID: r.UDID, Kind: r.Kind, Transport: r.Transport, State: r.State,
		Progress: wire.JobProgress{
			Phase: r.Phase, Percent: r.Percent, BytesDone: r.BytesDone,
			BytesTotal: r.BytesTotal, FilesReceived: r.FilesReceived, Liveness: r.Liveness,
		},
		StartedAt: fmtRFC(r.StartedAt), RetryOf: r.RetryOf, IntentID: r.IntentID,
		Attempt: r.Attempt, VersionID: r.VersionID,
	}
	if r.FinishedAt != nil {
		s := fmtRFC(*r.FinishedAt)
		j.FinishedAt = &s
	}
	if r.ErrorCode != "" {
		j.Error = &wire.JobError{Code: r.ErrorCode, Message: r.ErrorMessage}
	}
	return j
}

func fmtRFC(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func f64(v float64) *float64 { return &v }
func strptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
