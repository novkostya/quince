// Package wire holds the JSON shapes frozen in docs/contracts.md (§2 objects, §3 WS
// envelope, the error/config-error envelopes). It is the single source of truth shared by
// the HTTP handlers, the demo provider, and the golden contract tests, so a wire-shape
// drift fails a test rather than silently diverging across tracks.
//
// Casing is snake_case everywhere. Timestamps are RFC3339 UTC strings (contracts.md). The
// contract distinguishes two kinds of optionality, and this package encodes them
// deliberately per field:
//
//   - "… | null"        → a value pointer WITHOUT omitempty (nil marshals to JSON null,
//     so the key is always present, e.g. finished_at, job_id, percent, last_backup).
//   - "present keys only" → a pointer WITH omitempty (absent → key omitted, e.g. the
//     per-transport timestamps in Transports).
package wire

// Device is one iPhone/iPad, keyed by UDID, possibly present on several transports at
// once (contracts §2, design §3).
type Device struct {
	UDID             string      `json:"udid"`
	Name             string      `json:"name"`
	Model            string      `json:"model"` // raw, e.g. "iPhone17,2"; UI maps to marketing name
	IOSVersion       string      `json:"ios_version"`
	Transports       Transports  `json:"transports"`
	Paired           string      `json:"paired"`            // yes | no | unknown
	BackupEncryption string      `json:"backup_encryption"` // on | off | unknown
	LastSeen         string      `json:"last_seen"`
	LastBackup       *LastBackup `json:"last_backup"` // null when the device has no backups
}

// Transports carries a per-transport last-seen timestamp; absent transports are omitted
// ("present keys only").
type Transports struct {
	USB  *string `json:"usb,omitempty"`
	WiFi *string `json:"wifi,omitempty"`
}

// LastBackup summarizes a device's most recent SUCCESSFUL backup for the dashboard card
// (contracts §2, ratified (bz)). It is derived from the newest committed VERSION, not from job
// history — versions are the source of truth for "has this device been backed up", so the field
// survives restarts and covers ADOPTED versions (a restored/replicated dataset). Those have no
// job at all, hence JobID is nullable; fabricating one would be a state-honesty violation.
// A failed last *attempt* lives in the intent-grouped job history, never here.
type LastBackup struct {
	At     string  `json:"at"`
	JobID  *string `json:"job_id"` // nil = adopted version (no job record) → JSON null
	Status string  `json:"status"`
}

// Job is one backup attempt driven by the state machine (contracts §2, design §4).
type Job struct {
	ID         string      `json:"id"`
	UDID       string      `json:"udid"`
	Kind       string      `json:"kind"`      // "backup"
	Transport  string      `json:"transport"` // usb | wifi
	State      string      `json:"state"`     // queued … succeeded/failed/cancelled/connection_lost
	Progress   JobProgress `json:"progress"`
	StartedAt  string      `json:"started_at"`
	FinishedAt *string     `json:"finished_at"` // null until the job terminates
	Error      *JobError   `json:"error"`       // null unless failed/connection_lost
	RetryOf    *string     `json:"retry_of"`    // null unless this is a manual retry
	IntentID   string      `json:"intent_id"`   // == id for a first attempt
	Attempt    int         `json:"attempt"`     // 1-based position within the intent
	VersionID  *string     `json:"version_id"`  // set on succeeded
}

// JobProgress is the throttled progress + liveness snapshot for a running job.
type JobProgress struct {
	Phase   string   `json:"phase"`   // incl. "seeding", "waiting_for_passcode"
	Percent *float64 `json:"percent"` // null when indeterminate; the trustworthy OVERALL signal
	// BytesDone/BytesTotal are the CURRENT-TRANSFER bytes from idevicebackup2's "(X/Y)" — the current
	// file, NOT the whole backup (the tool gives no reliable upfront backup-byte total). The UI labels
	// them as the current file and leads with Percent + FilesReceived (qn.6a #10-byte, (cj)). Best-effort.
	BytesDone     int64  `json:"bytes_done"`
	BytesTotal    int64  `json:"bytes_total"`
	FilesReceived int64  `json:"files_received"`
	Liveness      string `json:"liveness"` // active | silent_but_connected | suspected_stall
}

// JobError is the {code, message} shape reused by Job.error and Op.error.
type JobError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Version is one immutable committed backup (contracts §2, design §5).
type Version struct {
	ID                  string  `json:"id"`
	UDID                string  `json:"udid"`
	Backend             string  `json:"backend"`      // zfs | reflink | hardlink | copy
	ZFSSnapshot         *string `json:"zfs_snapshot"` // zfs backend only; null elsewhere
	BrowseRoot          string  `json:"browse_root"`
	CreatedAt           string  `json:"created_at"`
	JobID               *string `json:"job_id"` // null = adopted (found on disk, no DB record)
	Kind                string  `json:"kind"`   // full | incremental | unknown
	Encrypted           bool    `json:"encrypted"`
	IsLatest            bool    `json:"is_latest"`
	StructureVerifiedAt *string `json:"structure_verified_at"` // set at commit
	ContentVerifiedAt   *string `json:"content_verified_at"`   // set on a later unlock
	LogicalBytes        int64   `json:"logical_bytes"`
	PhysicalBytes       int64   `json:"physical_bytes"`
	// Missing = the registry row survives but its on-disk artifact is GONE (reconciliation could not
	// find the snapshot/dir; roll-forward keeps the row, never drops it — contracts §2, qn.6a
	// (cr)(a)/(cv)). The UI renders such a version explicitly dead (no size claim, no Unlock, an
	// "artifact gone — remove?" action on DELETE), never omitting it.
	Missing bool `json:"missing"`
}

// Op is a pair/encryption operation whose narration streams over op.updated (contracts §2).
type Op struct {
	ID      string    `json:"id"`
	UDID    string    `json:"udid"`
	Kind    string    `json:"kind"`  // pair | encryption
	State   string    `json:"state"` // running | waiting_for_user | succeeded | failed
	Message string    `json:"message"`
	Error   *JobError `json:"error"`
}

// Session is an unlocked vault session (contracts §2). Populated from qn.8; carried here
// so session.locked events have a shape from this rung.
type Session struct {
	ID        string `json:"id"`
	VersionID string `json:"version_id"`
	ExpiresAt string `json:"expires_at"`
}

// FileEntry is one browse row (contracts §2); unused by qn.1 handlers but part of the
// frozen surface.
type FileEntry struct {
	FileID       string `json:"file_id"`
	Domain       string `json:"domain"`
	RelativePath string `json:"relative_path"`
	Kind         string `json:"kind"` // file | dir | symlink
	Size         int64  `json:"size"`
	Mtime        string `json:"mtime"`
}

// DevicesResponse is GET /api/devices.
type DevicesResponse struct {
	Devices []Device `json:"devices"`
}

// JobsResponse is GET /api/jobs (cursor pagination; next_cursor null on the last page).
type JobsResponse struct {
	Jobs       []Job   `json:"jobs"`
	NextCursor *string `json:"next_cursor"`
}

// VersionsResponse is GET /api/versions.
type VersionsResponse struct {
	Versions []Version `json:"versions"`
}

// APIError is the contracts.md error envelope: {error: {code, message}}.
type APIError struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the {code, message} inside APIError.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ConfigError is one PUT /api/config validation failure (contracts §1: 422
// {errors: [{path, message}]}).
type ConfigError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// AuthStatus is GET /api/auth/status (rung-ruled contract addition, contracts §1).
type AuthStatus struct {
	State     string `json:"state"` // needs_setup | needs_login | authenticated
	CSRFToken string `json:"csrf_token"`
}
