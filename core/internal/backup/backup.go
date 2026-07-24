// Package backup is quince's transport-agnostic backup engine (stack D3/D13, design §4). It
// drives idevicebackup2 as a supervised streaming subprocess through the frozen job state
// machine — queued → waiting_for_device → preflight → seeding → backing_up → verifying → committing →
// succeeded, with failed/cancelled/connection_lost terminals — and hands the produced tree to
// qn.5 storage (Seed → Verify → Commit, or Discard on failure). The invariant above all: the
// writer only ever touches the storage work area; a version exists only after verify + commit
// (state honesty). The engine is Wi-Fi-shaped from day one (it replays the Wi-Fi torn transcripts
// in CI); qn.4b makes Wi-Fi first-class and resolves transport: auto.
package backup

import (
	"context"
	"time"

	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// Transports (contracts §2). "auto" is a request-only value resolved by the engine against current
// presence (design §4, decisions (bp)); a Job only ever stores the concrete usb|wifi.
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
	StateSeeding          = "seeding" // cloning latest/ → working/<udid> before the tool starts (qn.6a)
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
	PhaseSeeding            = "seeding" // mirrors StateSeeding into progress.phase (qn.6a)
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
// and VerifyWork returns primitives, so there is no import edge into the storage package.
type Storage interface {
	// Seed provisions the device area and returns the idevicebackup2 TARGET — the working/ parent,
	// seeded so the tool's own <target>/<UDID> convention lands the tree in working/<udid> with no
	// symlink (qn.5b). A dirty working/ is resumed; else it is seeded from latest/.
	Seed(udid, jobID string) (target string, err error)
	// VerifyWork is the passwordless structural verification of the job's working tree
	// (working/<udid>); the kind is the authoritative seed-derived value (qn.5b, finding #9(a)).
	VerifyWork(udid, jobID string) (ok bool, detail, kind string, encrypted bool)
	// CommitJob verifies + commits the job's tree into an immutable version (returns it).
	CommitJob(udid, jobID string) (wire.Version, error)
	// Discard keeps a failed job's dirty working/ so a retry resumes (returns a human note; qn.5b).
	Discard(udid, jobID string) (note string, err error)
}

// Devices reports device presence + encryption state for preflight (*device.Registry satisfies
// it via the frozen wire.Device it already serves — no device-package import edge).
type Devices interface {
	Device(udid string) (wire.Device, bool)
}

// EncryptionProber re-reads a device's backup-encryption state live at preflight
// (*deviceops.Manager satisfies it). OPTIONAL: with no prober wired (e.g. --demo) preflight
// decides on the registry's cached value alone. It exists because that cached value can read
// `unknown` merely because enrichment ran while lockdown was cold, which hard-failed a
// legitimately-encrypted device's backup with no retry (qn.4a finding (i)-B, (bw)).
// ok=false means the probe itself failed — never a state to infer from.
type EncryptionProber interface {
	RefreshEncryption(ctx context.Context, udid, transport string) (state string, ok bool)
}

// DeviceAnnouncer asks the device registry to re-publish a device (device.updated) because
// something outside the registry changed what it reports — today: a successful commit changing
// last_backup (*device.Registry satisfies it). OPTIONAL, nil-safe: without it the card catches
// up on the next fetch instead of live (qn.4a finding (v)).
type DeviceAnnouncer interface {
	AnnounceBackup(udid string)
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
