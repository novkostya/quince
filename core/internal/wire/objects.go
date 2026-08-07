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
	WifiSync         string      `json:"wifi_sync"`         // on | off | unknown — lockdown wireless_lockdown (qn.7)
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
	// StorageID is the RESOLVED concrete storage this backup was aimed at — never the word
	// "default", exactly as `transport` stores the resolved usb/wifi and never "auto" (qn.6c).
	// null for jobs that ran before qn.6c, meaning quince did not record where this went.
	StorageID *string `json:"storage_id"`
	IntentID  string  `json:"intent_id"`  // == id for a first attempt
	Attempt   int     `json:"attempt"`    // 1-based position within the intent
	VersionID *string `json:"version_id"` // set on succeeded
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
	// StorageID is which storage this version lives on (qn.6c gap 1, contracts §2).
	//
	// NULL MEANS *NOT YET ATTRIBUTED*, and that is TRANSITIONAL — unlike JobID, whose null
	// (= adopted) is permanent and correct. A version committed before qn.6c has no storage id
	// because the value is a UUID from its storage's quince-storage.json, written at the
	// storage's creation moment; migration 0006 deliberately does not fabricate one.
	//
	// A client must not read null as "no storage" or substitute a default: it means quince has
	// not yet worked out which storage this is, and it stops meaning that once the storage has a
	// marker. Operator ruling 2026-08-01, quince#378.
	StorageID *string `json:"storage_id"`
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

// Storage is one declared backup location (contracts §1 GET /api/storages, qn.6c).
//
// Ruled 2026-07-31 (the object) and 2026-08-01 (the split cause). It is DEVICE-INDEPENDENT: "will
// the next backup be full" is a property of a (device, storage) PAIR, not of a storage, so it
// appears only when `?udid=` is passed and the storage-cards rung that follows can keep asking for
// a plain list.
type Storage struct {
	ID      string `json:"id"`      // the UUID from quince-storage.json; stable across replug
	Name    string `json:"name"`    // from config.yml; the label the UI shows
	Path    string `json:"path"`    //
	Backend string `json:"backend"` // zfs | reflink | hardlink | copy | unknown

	// Backend is "unknown" when the storage has never been reached: the backend is chosen by
	// probing a filesystem, so a storage that was never opened has no answer. That is distinct
	// from a guess, which is why the enum carries the value rather than the field going empty.

	Default   bool `json:"default"`   // exactly one storage is default
	Reachable bool `json:"reachable"` //

	// UnreachableCode is the machine-readable cause and UnreachableReason the daemon's sentence.
	// BOTH ARE NULL WHEN REACHABLE AND NEVER ABSENT — the same ruling as Version.storage_id: a
	// present null is a fact, an absent key is a version-skew question.
	//
	// Two fields rather than one because prose cannot be branched on and an enum cannot be shown,
	// and because `missing_medium` and `path_unreachable` call for different user actions — *plug
	// the disk in* versus *this path is readable but it is not your backup medium*. A client
	// mapping the code to its own copy cannot include what the daemon knows and it does not:
	// which path, which marker.
	UnreachableCode   *string `json:"unreachable_code"`   // path_unreachable | missing_medium | backend_mismatch
	UnreachableReason *string `json:"unreachable_reason"` //

	// WillBeFull answers "will the next backup to this storage be a full transfer" for ONE device,
	// and is null unless `?udid=` was passed. Story 8 states the cost before it is paid: the first
	// backup to a new storage is always full, and the server owns that answer because only it knows
	// whether a (device, storage) pair has a prior version.
	WillBeFull *bool `json:"will_be_full"`

	// FilesystemFreeBytes / FilesystemTotalBytes are `statfs` on this storage's path — of the
	// FILESYSTEM, never of the storage (qn.6d gap A, ruled 2026-08-03). The prefix is the whole
	// point of the name: two storages that are two directories on one disk report IDENTICAL
	// figures, and no field distinguishes them. `filesystem_id` and a `filesystem_shared` boolean
	// were both offered to the Operator and both declined, so the card renders no caveat and a
	// user may read 1.2 + 1.2 as 2.4 TB. That is a RULED ACCEPTANCE — do not "fix" it by
	// reintroducing the distinction, and do not file it as a bug.
	//
	// NULL when the storage is unreachable, never 0: a zero is a measurement and this is an
	// absence. Same discipline as WillBeFull.
	FilesystemFreeBytes  *int64 `json:"filesystem_free_bytes"`
	FilesystemTotalBytes *int64 `json:"filesystem_total_bytes"`

	// BackupCount and DeviceCount are properties of the STORAGE, so they appear with or without
	// `?udid=` — which continues to add only WillBeFull. They come from the DB, so they stay
	// populated even when the storage is unreachable. NO TIMESTAMP ACCOMPANIES THEM: the counts are
	// CURRENT, not a last-known reading — the DB is reachable whether or not the disk is, so there
	// is nothing to date (quince#588, ruled 2026-08-03). The asymmetry a client needs is carried by
	// THESE TWO FIELDS being populated while capacity is null, which is visible without one.
	//
	// MISSING versions are counted (qn.6d rung-ruled decision 3), matching UDIDsWithVersions and
	// deliberately unlike Slot.hasVersionFor, which excludes them because "will the next backup be
	// full" depends on a usable artifact.
	BackupCount int `json:"backup_count"`
	DeviceCount int `json:"device_count"`
}

// StoragesResponse is GET /api/storages. StorageResponse is POST /api/storages/{id}/recheck.
type StoragesResponse struct {
	Storages []Storage `json:"storages"`
}

type StorageResponse struct {
	Storage Storage `json:"storage"`
}

// StorageProbeRequest is POST /api/storages/probe (qn.6e): what IS this path?
type StorageProbeRequest struct {
	Path string `json:"path"`
}

// StorageProbe answers it. IT IS NOT A Storage AND MUST NOT BECOME ONE — a Storage is declared, has
// an identity and is being served; this describes a candidate that may never be declared at all.
// Sharing the object would mean either lying about `id` and `default` or making them nullable on a
// resource where they are guarantees.
//
// Every refusal is carried IN this object rather than as an HTTP status, because "that path does not
// exist" is the ANSWER to the question asked, not a failure to answer it. Only a malformed question
// — no path, or one that is not absolute — is a 422. See handleStorageProbe for where that line is
// drawn and why.
type StorageProbe struct {
	// Path is what the client sent, verbatim; CleanPath is filepath.Clean of it. Both, because the
	// user should see their own typing back and quince acts on the cleaned form.
	Path      string `json:"path"`
	CleanPath string `json:"clean_path"`

	// Outcome is the verdict: adopt | new | missing | not_a_directory | unwritable |
	// corrupt_marker | unreadable. FROZEN — a client renders different prose and a different next
	// action for each, so adding one is a contract change.
	Outcome string `json:"outcome"`
	// Reason is the daemon's own sentence and ALWAYS NAMES THE PATH (quince#514). A client shows it
	// rather than composing its own, for the reason Storage.unreachable_reason gives: quince knows
	// which path and which marker, and a client's copy of an enum cannot.
	Reason string `json:"reason"`

	// Backend is the recommendation (new) or the marker's recorded value (adopt), and is EMPTY on
	// every refusal. BackendReason is the sentence behind it.
	//
	// On adopt it is not a recommendation at all: a storage's backend is written at its creation
	// moment and is immutable, so the form shows it and offers no selector.
	Backend       string `json:"backend"`
	BackendReason string `json:"backend_reason"`

	// Marker is present whenever one was readable, ON ANY OUTCOME — so a client can say "this IS
	// storage X, and the path is read-only" rather than only the second half. Null otherwise.
	Marker *StorageProbeMarker `json:"marker"`

	// NonEmpty reports data at the path that is not quince's own. It is a FACT, never a refusal: a
	// path holding backups from before storage markers existed has no marker and is not empty, and
	// is exactly what an upgrading operator types.
	NonEmpty bool `json:"non_empty"`

	// ZFS is path | host | none.
	//
	// `none` MEANS NO SIGNAL AND MUST NEVER BE RENDERED AS "ZFS NOT SUPPORTED". In hook mode the
	// container holds no zfs userland at all and zfs works perfectly through the host helper, so a
	// negative reading is a guaranteed false negative for the supported containerised topology.
	// "Not detected", or silence.
	ZFS string `json:"zfs"`

	// FILESYSTEM, NOT STORAGE, and the names carry that (contracts §2, ruled 2026-08-03). Two
	// candidate paths on one disk return identical figures and nothing here distinguishes them.
	// Both zero when the path could not be stat'd.
	FilesystemFreeBytes  uint64 `json:"filesystem_free_bytes"`
	FilesystemTotalBytes uint64 `json:"filesystem_total_bytes"`
}

// StorageProbeMarker is the identity quince found at the path. A SUBSET of the on-disk marker: the
// checksum is an integrity detail of the file and app_version is quince's own history, neither of
// which a form has anything to do with.
type StorageProbeMarker struct {
	StorageID string `json:"storage_id"`
	Backend   string `json:"backend"`
	CreatedAt string `json:"created_at"`
}

type StorageProbeResponse struct {
	Probe StorageProbe `json:"probe"`
}

// HTTPSDetected names WHY step 1 is or is not complete. Values are frozen: a client renders
// different prose for each, and adding one is a contract change.
const (
	// HTTPSDetectedTLS — quince terminated TLS itself (`tls:` configured, this connection
	// arrived on the TLS half of the listener).
	HTTPSDetectedTLS = "tls"
	// HTTPSDetectedForwarded — a proxy in front terminated it and said so with
	// `X-Forwarded-Proto: https`.
	HTTPSDetectedForwarded = "forwarded_proto"
	// HTTPSDetectedNone — plain http, so the step is not complete. Includes loopback, which
	// is deliberate: the step asks whether a PHONE can reach quince, and a browser on
	// http://localhost cannot answer yes on its behalf.
	HTTPSDetectedNone = "none"
)

// OnboardingHTTPS is GET /api/onboarding/https — the FIRST onboarding surface in the product
// (qn.6f, design §9). Deliberately two fields: it sets the shape steps 2 and 3 inherit, and a
// richer payload here would be a precedent every later step cites.
//
// Complete is derivable from Detected today, and both are sent anyway. Detected is the
// EVIDENCE and Complete is the VERDICT: a client that only wants to know whether to show the
// tiers should not have to keep a list of which reasons count, because that list is exactly
// the thing that goes stale when a fourth reason is added.
type OnboardingHTTPS struct {
	Complete bool   `json:"complete"`
	Detected string `json:"detected"` // tls | forwarded_proto | none
}

// StorageHookCheckRequest is POST /api/storages/probe/hook (qn.6e): does the operator's constrained
// ZFS helper actually work, and does it agree about the parent dataset?
type StorageHookCheckRequest struct {
	ParentDataset string `json:"parent_dataset"`
	HookCmd       string `json:"hook_cmd"`
}

// StorageHookCheck is the verdict. FOUR outcomes rather than ok/failed, because the remedies differ
// and a user cannot guess between them — a missing helper, an un-migrated helper and a mistyped
// parent dataset all present as "it did not work".
type StorageHookCheck struct {
	// Outcome is ok | not_migrated | parent_mismatch | unreachable. FROZEN: a client renders a
	// different remedy for each, so adding one is a contract change.
	Outcome string `json:"outcome"`
	// Reason is quince's own sentence, safe to render anywhere.
	Reason string `json:"reason"`
	// Detail is the transport's own output, verbatim — ssh's "Permission denied (publickey)" is the
	// whole answer to why a key does not work, and quince cannot improve on it.
	//
	// IT MAY NAME THE OPERATOR'S HOST. It is shown to the authenticated admin in their own browser
	// and MUST NEVER be logged, put in a fixture, or pasted into a PR or an issue — which is the
	// privacy gate's actual scope (committed files, commit messages, forge text) rather than a
	// redaction rule on a running product. THE ARGV IS NEVER INCLUDED: `hook_cmd` carries
	// `user@host` by construction, where the transport's output only sometimes does.
	Detail string `json:"detail"`
}

type StorageHookCheckResponse struct {
	Check StorageHookCheck `json:"check"`
}
