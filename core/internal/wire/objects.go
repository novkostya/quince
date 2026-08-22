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
	UDID             string     `json:"udid"`
	Name             string     `json:"name"`
	Model            string     `json:"model"` // raw, e.g. "iPhone17,2"; UI maps to marketing name
	IOSVersion       string     `json:"ios_version"`
	Transports       Transports `json:"transports"`
	Paired           string     `json:"paired"`            // yes | no | unknown
	BackupEncryption string     `json:"backup_encryption"` // on | off | unknown
	WifiSync         string     `json:"wifi_sync"`         // on | off | unknown — lockdown wireless_lockdown (qn.7)
	// NotificationsEnabled is the per-device notifications switch (quince#1270) — whether quince
	// notifies about THIS device at all. A bool rather than wifi_sync's on|off|unknown, because
	// this is quince's own policy and not a value read back off the device: there is no unknown.
	NotificationsEnabled bool        `json:"notifications_enabled"`
	LastSeen             string      `json:"last_seen"`
	LastBackup           *LastBackup `json:"last_backup"` // null when the device has no backups
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
	// LogicalBytes is the APPARENT size of the version's tree — the sum of its regular files.
	// It is the only size a version carries, and it is the same figure on every backend.
	//
	// There is no companion "physical" figure. `physical_bytes` shipped beside this until
	// quince#442 and was the same `dirSize` walk under a second name, so the UI rendered two
	// identical numbers as two facts. A per-version physical size is not merely unmeasured but
	// ILL-DEFINED wherever blocks are shared: on reflink a shared extent counts in full against
	// every file that references it, and on zfs the newest snapshot's unique bytes are 0 because
	// latest/ still holds them. What a user could act on — "what would deleting this version free"
	// — is a property of the version WITHIN THE CURRENT SET, not of the version: removing a
	// neighbour changes it, so it cannot be computed at commit and stored. If it ever ships it is
	// computed on demand at the point of decision, not carried in a list row.
	LogicalBytes int64 `json:"logical_bytes"`
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

	// Incomplete marks a file the BACKUP holds fewer bytes for than its own index records —
	// captured while it was being written. It is NOT a read failure: every recovered byte is
	// delivered, and a retry cannot change it (qn.8 spec D8.1).
	//
	// A NON-BREAKING FIELD ADDITION, following the qn.7 precedent for `wifi_sync`.
	//
	// IT CANNOT BE KNOWN BEFORE THE FILE IS READ, which is why it is a field and not a
	// property of the manifest: the index records a size, and only decrypting the blob shows
	// that fewer bytes are there. So it is false on first sight and true on any view AFTER a
	// read that came up short — the session remembers.
	//
	// `omitempty` because absent means "not known to be incomplete", which is the honest
	// reading of a file nobody has read yet. Safe here where quince#493 would forbid it on a
	// config round trip: this is a read-only surface and no client PUTs it back.
	Incomplete bool `json:"incomplete,omitempty"`

	// Overlong is Incomplete's MIRROR: the backup holds MORE bytes for this file than its own
	// index records, so the download carries the RECORDED length and stops there. Measured at
	// ~34–38 files per version on real backups (quince#1379).
	//
	// `omitempty` for the same reason: absent means "not known to be overlong", which is not
	// the same as knowing it is not — and, like Incomplete, it can only be known after a read.
	//
	// IT IS A FIELD RATHER THAN A CODE FOR THE SAME REASON, AND IT IS NEEDED MORE. The read
	// SUCCEEDS: every byte the index promises is delivered. But where an incomplete file at
	// least ends a body early against a declared length, this one produces a response where
	// the status, the header and the body all agree — so without this field the truncation is
	// invisible to a client, which is the silent cap the hard rules forbid.
	Overlong bool `json:"overlong,omitempty"`
}

// BrowseQuery is GET /api/sessions/{id}/browse (contracts §1).
type BrowseQuery struct {
	Domain string
	Prefix string
	Cursor string
	Limit  int
}

// BrowsePage is what that route returns.
type BrowsePage struct {
	Entries    []FileEntry `json:"entries"`
	NextCursor string      `json:"next_cursor,omitempty"`

	// EffectiveLimit is set ONLY when the server clamped the requested limit, so a caller
	// that asked for more than the maximum can tell a clamp from a short last page. "No
	// silent caps or fallbacks" as a wire field.
	EffectiveLimit int `json:"effective_limit,omitempty"`
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
	// Accepts names the factors that would satisfy a `reauth_required` refusal — qn.6o D1.
	//
	// `omitempty` IS THE D4 RULE IN THE TAG. The field is present-and-non-empty or absent; there is
	// no `accepts: []` on the wire, because an empty list would make the client responsible for
	// turning emptiness back into an explanation. A dead end is `last_credential`, which carries a
	// sentence naming the remedy.
	//
	// ON THE SHARED ENVELOPE rather than on a `reauth_required`-specific body, because every error
	// in the product goes through this one type. Only the five re-authentication emitters set it
	// today; whether it should appear on other refusals is left open by the rung.
	Accepts []string `json:"accepts,omitempty"`
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

	// Scope is null for an ADMIN principal and names the device for a scoped one (qn.13 slice 8d,
	// D8; ruled on quince#1443).
	//
	// THE SAME TYPE AND THE SAME SPELLING AS `Passkey.Scope`, deliberately and by ruling. One
	// concept gets one shape across both wire objects; a second convention for the same question
	// is the drift this project files issues about.
	//
	// `state` IS THE DISAMBIGUATOR, NOT THIS FIELD'S PRESENCE. `scope` is meaningful only when
	// `state == authenticated`; on any other state its value carries no claim. Making ABSENCE mean
	// both *not authenticated* and *admin* would be quince#744's defect — one value standing for
	// several states with no way to tell them apart — reproduced in a new field, and it is why
	// there is no `omitempty` here.
	//
	// IT IS NOT AN AUTHORIZATION SURFACE. The shell hides what a principal cannot use; the server
	// refuses it regardless (D8: *unreachable is a server property, not a routing one*). A client
	// that forged this field would hide things from its own user and gain nothing.
	//
	// AN OLD CLIENT IGNORES IT AND RENDERS ADMIN CHROME TO A SCOPED HOLDER — a cached service
	// worker will do exactly that, since qn.12 shipped a PWA. Nothing leaks, because every route
	// behind that chrome refuses, and it is today's behaviour rather than a regression. Stated
	// because the opposite was claimed while this was being ruled, and a wrong premise left
	// standing gets cited later (quince#1443).
	Scope *PasskeyScope `json:"scope"`
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
	// FOUR CODES SINCE quince#569, and the wire vocabulary is deliberately NOT the daemon's internal
	// `storage.Resolution` enum — `live.go`'s wireUnreachableCode translates, so a new internal state
	// cannot reach a client without somebody declaring it here. `unmapped` is what an undeclared one
	// produces: never expected, and it means quince has a state it failed to map.
	UnreachableCode   *string `json:"unreachable_code"`   // path_unreachable | missing_medium | backend_mismatch | corrupt_marker | unmapped
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
	//
	// SINCE qn.6i THEY CAN ALSO BE INCOMPLETE, WHICH IS A DIFFERENT THING FROM STALE. The ruling above
	// is untouched — there is still no timestamp, because the counts are not a last-known reading —
	// but reconciliation now runs asynchronously, so between a start and the first pass finishing
	// these are counts of a registry quince KNOWS it has not finished reading. That window is declared
	// on `GET /api/health` as `reconciling`, and contracts §1 states what it promises: while it holds,
	// a count may be short, and short is not zero-meaning-none.
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

// UnencryptedCode refines `HTTPSDetectedNone` — WHICH of the four shapes of evidence quince saw
// when it concluded this origin was not encrypted (quince#940 §2 + quince#939 §7).
//
// A SECOND FIELD RATHER THAN MORE `HTTPSDetected` VALUES — architect ruling (delegated), door 2,
// 2026-08-14. The two obvious alternatives are both closed:
//
//   - Widening `HTTPSDetected` also widens the CROSS-ORIGIN probe, silently: `OnboardingProbe.Detected`
//     says at its own definition that it takes the same values and comes from the same function. The
//     CORS ruling froze that body at `{nonce, detected}` on the argument that it leaks nothing, and
//     said widening it needs that ruling revisited. Adding a value here would revisit it without
//     anybody saying so.
//   - Widening it and having the probe map the new values back to `none` leaves one type whose values
//     are legal on one endpoint and illegal on another, with the type definition telling the reader
//     they are the same — a trap set for the person who checks.
//
// FROZEN, for `HTTPSDetected`'s reason: a client renders a different remedy for each, so adding one
// is a contract change.
const (
	// UnencryptedNoProxySeen — no `X-Forwarded-Proto` and no `X-Forwarded-For`, so quince has no
	// evidence that any proxy is involved. Probably a browser talking to quince directly over plain
	// http, and the remedy is a proxy in front or quince's own certificate.
	UnencryptedNoProxySeen = "no_proxy_seen"
	// UnencryptedProxyNotForwardingScheme — no `X-Forwarded-Proto`, but `X-Forwarded-For` IS present,
	// so something in front is adding forwarding headers and not the one that matters. The remedy is
	// one line of proxy config (`proxy_set_header X-Forwarded-Proto $scheme;`).
	//
	// A HINT, NOT A VERDICT, and the distinction is the whole reason this value is separate from the
	// one above (quince#939 §7). nginx does not set `X-Forwarded-For` by default either, so a client
	// stating this as fact would tell some correctly-configured operators their proxy is broken.
	UnencryptedProxyNotForwardingScheme = "proxy_not_forwarding_scheme"
	// UnencryptedProxyUntrusted — `X-Forwarded-Proto: https` arrived, a trusted-proxy list IS
	// configured, and the peer is not in it. The proxy is doing its job and quince is declining to
	// believe it; the remedy is to add that peer to `QUINCE_TRUSTED_PROXIES`.
	UnencryptedProxyUntrusted = "proxy_untrusted"
	// UnencryptedProxyReportsPlain — `X-Forwarded-Proto` is present and does not say `https`. THE
	// PROXY IS CORRECT and is reporting that the client reached IT over plain http; the remedy is the
	// proxy's own listener, not quince.
	//
	// IT COVERS ANY NON-`https` VALUE, not only the literal `http`. A header that does not say https
	// is not https whatever else it says, and inventing a fifth code for a value nobody sets would be
	// a remedy nobody needs.
	UnencryptedProxyReportsPlain = "proxy_reports_plain"
)

// OnboardingHTTPS is GET /api/onboarding/https — the FIRST onboarding surface in the product
// (qn.6f, design §9).
//
// Complete is derivable from Detected today, and both are sent anyway. Detected is the
// EVIDENCE and Complete is the VERDICT: a client that only wants to know whether to show the
// tiers should not have to keep a list of which reasons count, because that list is exactly
// the thing that goes stale when a fourth reason is added.
//
// IT WAS DELIBERATELY TWO FIELDS AND IS NOW THREE, which spends a precedent this comment used to
// argue against — *"a richer payload here would be a precedent every later step cites."* The
// architect's ruling accepts that and BOUNDS it: the narrowness argument is against enriching step 1
// GRATUITOUSLY, and a later step citing this must show the same three things —
//
//	the daemon ALREADY HOLDS the distinction;
//	without it a user is sent to the WRONG remedy, not merely a vaguer one;
//	and no same-origin route already carries it.
//
// Two of three users meeting `detected: none` were being told their proxy was broken while it was
// behaving correctly, which is what cleared that bar.
type OnboardingHTTPS struct {
	Complete bool   `json:"complete"`
	Detected string `json:"detected"` // tls | forwarded_proto | none
	// UnencryptedCode is set ONLY when Detected is `none`, and omitted otherwise — there is no
	// question to answer when the origin IS encrypted. See the Unencrypted* constants.
	UnencryptedCode string `json:"unencrypted_code,omitempty"`
	// TLSUnusableCode is set when `config.yml` ASKS for TLS and the daemon is serving no
	// certificate — quince tried the operator's pair, failed, and knows what kind of failure it
	// was (quince#940 §1). Omitted whenever TLS is off or the certificate is being served.
	//
	// A CLASSIFICATION AND NEVER A DETAIL. Operator ruling 2026-08-14: the KIND may be pre-auth on a
	// claimed install, and the raw reason — which names the file and carries the loader's own text —
	// is AUTHENTICATED. A pre-auth observer already knows TLS is not working, because they are
	// reading the page over http; a filesystem path is different in kind.
	//
	// IT IS ORTHOGONAL TO `UnencryptedCode`, and both can be set at once: one says why this
	// CONNECTION is not encrypted, the other says why quince's OWN certificate is not serving. A
	// user with no proxy and a mismatched pair has two true answers and needs both.
	TLSUnusableCode string `json:"tls_unusable_code,omitempty"`
}

// TLS failure kinds for OnboardingHTTPS.TLSUnusableCode (quince#940 §1).
//
// THE VALUES ARE `tlsx.Inspect`'s OWN OUTCOMES, passed through rather than re-mapped, so there is
// one classification in the product instead of two that can drift. `usable` never appears here: a
// pair that inspects clean is not a failure, and the field is absent.
//
// FROZEN, like every other enum on this surface: a client renders a different sentence for each.
const (
	TLSUnusableUnreadable  = "unreadable"    // the file could not be read at all
	TLSUnusableMalformed   = "malformed"     // read, but not PEM quince could parse
	TLSUnusableMismatched  = "mismatched"    // the certificate and the key are not a pair
	TLSUnusableNotYetValid = "not_yet_valid" // its validity period has not started
	TLSUnusableExpired     = "expired"       // its validity period has ended
	// TLSUnusableUnknown is the honest answer when the pair inspects CLEAN and is still not loaded.
	// The ruling asks for exactly this rather than a guess: *quince could not use this certificate
	// and could not tell why* beats leaking the loader's string to say something.
	TLSUnusableUnknown = "unknown"
)

// CertificateProbeRequest is POST /api/onboarding/certificate (quince#908 §5, slice 4).
//
// `Hostname` MAY BE EMPTY and that is not a refusal — the field starts empty by ruling (do not
// pre-fill it from the `Host` header: that is the name they are leaving), so somebody checking a pair
// before choosing a name gets every answer except the coverage one.
type CertificateProbeRequest struct {
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	Hostname string `json:"hostname"`
}

// CertificateProbe answers it — the OFFLINE half, no network.
//
// IT IS `StorageProbe`'s SHAPE, deliberately, because it answers the same kind of question: *what is
// this thing I am about to declare?* Every refusal is carried IN this object rather than as an HTTP
// status, because "that certificate expired last week" is the ANSWER, not a failure to answer it.
// Only a malformed question — a missing or relative path — is a 422.
type CertificateProbe struct {
	// Echoed, so a form can show the user their own typing beside the verdict.
	CertFile string `json:"cert_file"`
	KeyFile  string `json:"key_file"`
	Hostname string `json:"hostname"`

	// Outcome is the verdict: usable | unreadable | malformed | mismatched | not_yet_valid |
	// expired | wrong_host. FROZEN, for StorageProbe.Outcome's reason — a client renders different
	// prose and a different next action for each, so adding one is a contract change.
	Outcome string `json:"outcome"`
	// Reason is the daemon's own sentence and ALWAYS NAMES THE FILE OR THE HOST (quince#514). A
	// client shows it rather than composing its own: quince knows which of the two files failed and
	// which names the certificate carries, where a client's copy of an enum cannot.
	Reason string `json:"reason"`

	// Names is every DNS name and IP the leaf covers — populated even on `wrong_host`, ESPECIALLY
	// then: "does not cover quince.example" is a status, "covers quince.lan, not quince.example" is
	// something a person can act on. The legacy CN is deliberately absent; no browser has honoured
	// it since 2017, so listing it would show a name that does not work.
	//
	// AN ARRAY ALWAYS, `[]` WHEN THERE IS NO LEAF TO READ. The outcomes that fail before the
	// certificate parses have no names to report, and a client is entitled to treat this as a list on
	// every one of them — see the handler, which is where the empty slice is supplied, because a nil
	// Go slice marshals to `null`.
	Names []string `json:"names"`

	// NotBefore/NotAfter are RFC3339 UTC, empty when the leaf never parsed. Sent on `usable` too — a
	// certificate that works today and expires in nine days is not a refusal and is worth seeing.
	NotBefore string `json:"not_before"`
	NotAfter  string `json:"not_after"`

	// ChainLength is how many certificates the file held. ONE IS NOT AN ERROR AND IS OFTEN A PROBLEM:
	// a leaf without its intermediate validates on a machine that caches the issuer and fails on a
	// phone that does not. Reported rather than judged, because whether it matters depends on the
	// issuer.
	ChainLength int `json:"chain_length"`

	// CurrentHost is the host this request arrived on, without its port, and CurrentHostCovered is
	// whether the leaf covers it. THE SECOND COVERAGE QUESTION, and the one nobody was asking:
	// `Outcome` answers *does it cover the name they typed*, which is unanswerable while that field
	// is empty — and empty means *keep using the address I am on*, so this is the answer for exactly
	// that case.
	//
	// IT NEVER CHANGES `Outcome`. A pair reached by IP is a legitimate install and this product must
	// not refuse one; the fact is here so a client can say what leaving the name empty will mean.
	CurrentHost        string `json:"current_host"`
	CurrentHostCovered bool   `json:"current_host_covered"`
}

// CertificateConfirmRequest is POST /api/onboarding/certificate/confirm (quince#908 §5, slice 5).
//
// ONE FIELD, AND THE OTHER HALF OF THE PROOF IS NOT IN THE BODY AT ALL: the request must have
// arrived on quince's own TLS half (`r.TLS != nil`). That is the whole ceremony — the token says
// WHICH trial, the connection says THAT IT WORKS — and only then is `config.yml` written.
type CertificateConfirmRequest struct {
	Token string `json:"token"`
}

// ProbeNonce is GET /api/onboarding/probe/nonce — the token a page obtains SAME-ORIGIN before probing
// a name it is about to be redirected to (Operator ruling 2026-08-14, for quince#908 slice 4 and
// quince#939).
//
// The mint response is never CORS-readable, and that asymmetry IS the gate: a legitimate page holds a
// nonce, a drive-by page holds none.
type ProbeNonce struct {
	Nonce string `json:"nonce"`
}

// ProbeResult is GET /api/onboarding/probe?nonce=… — the cross-origin half, and the only endpoint in
// this product that answers with `Access-Control-Allow-Origin`.
//
// TWO FIELDS, AND THE LIMIT IS THE SAFETY ARGUMENT RATHER THAN TASTE. The ruling permitted the
// widening because this body gives up nothing a successful connection has not already revealed to the
// page that made it — so **adding a field is a contracts change AND needs that ruling revisited.** It
// has been revisited once already: the body was `{nonce}` alone for about an hour, until quince#939
// showed a probe that must report what quince SAW rather than only that it answered.
//
//	Nonce      echoed back. Proves the caller reached THIS quince rather than another one answering
//	           at that name — without it, success means only "a quince answered".
//	Detected   what quince saw on the probe request's OWN connection: its own TLS, a forwarded
//	           scheme, or neither. `none` behind a working https proxy is the nginx caveat.
//
// `Detected` TAKES THE SAME VALUES AS `OnboardingHTTPS.Detected` and comes from the same function.
// Two three-ways for one question is the defect this codebase names most often.
type ProbeResult struct {
	Nonce    string `json:"nonce"`
	Detected string `json:"detected"` // tls | forwarded_proto | none
}

// StorageHookCheckRequest is POST /api/storages/probe/hook (qn.6e): does the operator's constrained
// ZFS helper actually work, and does it agree about the parent dataset?
//
// IT TAKES THE TRANSPORT STRUCTURED, NOT AS AN ARGV (quince#818). It carried `hook_cmd` — a
// free-text command line the caller composed — until the Operator ruled SSH the only shape. The
// server builds the argv from these now, so the form asks for what a person knows (a user, a host)
// rather than for something only quince should be writing.
//
// THAT ALSO NARROWS WHAT THIS ENDPOINT EXECUTES. The declaration below still holds — it runs a
// request-influenced argv — but the influence is four typed fields through one composer, where it
// used to be the entire command line.
type StorageHookCheckRequest struct {
	ParentDataset string `json:"parent_dataset"`
	SSHUser       string `json:"ssh_user"`
	SSHHost       string `json:"ssh_host"`
	// SSHPort and SSHKey are OPTIONAL and default exactly as the config does — 22 and
	// `/data/keys/zfs`. A form that has not asked for them sends neither, and the check then tests
	// the same transport the saved storage will use, which is the only thing that makes this button
	// mean anything.
	SSHPort int    `json:"ssh_port"`
	SSHKey  string `json:"ssh_key"`
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

// PasskeyRegisterBegin is POST /api/auth/passkeys/register/begin's response (qn.6k).
//
// `options` is the WebAuthn `PublicKeyCredentialCreationOptions` structure VERBATIM as the library
// produced it, passed to `navigator.credentials.create()` unmodified. It is deliberately `any`
// rather than a mirrored Go type: the shape is the W3C spec's, it changes when the library's
// conformance does, and a hand-maintained copy here would be a second definition that can drift
// from the one actually signed over.
type PasskeyRegisterBegin struct {
	Ceremony string `json:"ceremony"`
	Options  any    `json:"options"`
}

// PasskeyLoginBegin is POST /api/auth/passkeys/login/begin's REQUEST (qn.13 slice 7, spec D2.2).
//
// `credential_id` is the credential this browser remembers, or absent. It is a HINT: it selects
// which credential the platform offers and grants nothing, because authority resolves from the
// assertion afterwards (D2). A caller naming an id that is not theirs narrows themselves to a
// signature they cannot produce.
//
// OPTIONAL, AND THE ABSENT CASE IS THE OLD BEHAVIOUR. The body was `{}` before this rung and still
// may be, which is what keeps a browser that has never stored an id — or one holding qn.6k's
// boolean `"1"` — on the discoverable flow rather than broken.
type PasskeyLoginBegin struct {
	CredentialID string `json:"credential_id,omitempty"`
}

// Passkey is one registered credential as the API renders it (qn.6k).
//
// NO PUBLIC KEY AND NO CREDENTIAL ID. Neither is a secret, and neither is any use to the UI — the
// list exists so a human can recognise a device and remove it. Sending them would widen what a
// compromised session can enumerate for nothing.
type Passkey struct {
	ID        string `json:"id"`   // opaque handle for remove/rename; the credential id
	Name      string `json:"name"` // what the user called it
	RPID      string `json:"rp_id"`
	CreatedAt string `json:"created_at"`
	// LastUsedAt is null until the first successful assertion — "never used" rather than a zero
	// timestamp, because a credential nobody has signed in with is exactly the one worth removing.
	LastUsedAt *string `json:"last_used_at"`

	// Scope is null for an ADMIN credential and names the device for a scoped one (qn.13 D9).
	//
	// WITHOUT THIS THE ADMIN CANNOT ANSWER *WHAT HAVE I ISSUED*, which is D9's requirement in as
	// many words, and `enrolment_ceremony.go` already records what its absence cost: the stored
	// label had to be derived from the scope precisely because "`wire.Passkey` carries no scope, so
	// two enrolled devices produced two rows the admin could tell apart only by guessing." The
	// label made the rows distinguishable; it did not make them CLASSIFIED, because an admin
	// credential may be called anything, including a device's name.
	//
	// NULL MEANS ADMIN, mirroring `store.Passkey.ScopeUDID` rather than inventing a second
	// spelling — and for the same reason it gives there: letting a zero value stand for admin is
	// how a forgotten field becomes a privilege. A client that does not know this field reads the
	// payload it always read and treats every row as admin, which is the safe direction to be
	// wrong at a surface only the admin can reach.
	//
	// IT IS NOT AN AUTHORIZATION SURFACE. `GET /api/auth/passkeys` is admin-only, so this discloses
	// the shape of the household to the one principal already entitled to change it. Nothing here
	// decides anything: revocation is `DELETE`, and the server checks the caller either way.
	Scope *PasskeyScope `json:"scope"`
}

// PasskeyScope names the device a credential is confined to (qn.13 D2, D9).
//
// AN OBJECT RATHER THAN A BARE `scope_udid` STRING, so the field can gain what a scope turns out to
// need without a second nullable column beside it — and so `scope: null` reads as *no scope* rather
// than as an empty device id, which is the distinction `store.Scope` exists to protect.
//
// THE UDID AND NOT A NAME. The row's `name` already carries the device's name, derived at enrolment
// — but that is a SNAPSHOT: renaming the device does not rewrite issued credentials, so the label
// can go stale while the udid cannot. The udid is what links the row to the device page, and it is
// the only value here that stays correct.
type PasskeyScope struct {
	UDID string `json:"udid"`
}

// PasskeyList is GET /api/auth/passkeys' response (qn.6k).
//
// It carries the CURRENT relying party alongside the rows, so the Settings surface can mark the
// credentials that will not work at this address without deriving the domain itself. A browser can
// read `location.hostname`, but it cannot know what quince considered the rpId — behind a proxy the
// two agree only if the proxy preserves `Host`, which is precisely what can be misconfigured
// (deploy/tls.md). Sending it makes the UI's warning agree with the server's behaviour.
type PasskeyList struct {
	Passkeys []Passkey `json:"passkeys"`
	RPID     string    `json:"rp_id"`
	// Supported is false where this address cannot be a relying party at all — a bare IP, or a
	// name with no dot. The surface refuses to offer a button that cannot work (spec story 4).
	Supported bool `json:"supported"`
	// HasPassword reports whether an admin PASSWORD exists — qn.6m, quince#855.
	//
	// ON THIS ENDPOINT RATHER THAN ON `GET /api/auth/status`, WHICH IS THE OBVIOUS HOME AND THE
	// WRONG ONE. `auth/status` is PRE-AUTH, so putting it there would tell an anonymous visitor
	// whether this quince has a password — close to free today, since the login screen renders a
	// password field either way, but a disclosure decision rather than a field, and one nobody
	// ruled. This endpoint already requires a session, so it discloses to somebody who is already
	// the admin.
	//
	// IT ALSO FITS WHAT THIS PAYLOAD HAS BECOME. `rp_id` and `supported` are not facts about the
	// listed credentials either; they are what the auth SURFACE needs in order to render honestly,
	// and so is this. The endpoint name now under-describes its body, which is the cost and is
	// cheaper than a fourth auth endpoint.
	//
	// WITHOUT IT THE SCREEN LIES QUIETLY: `/settings/auth` said "Change your password / Current
	// password" on a passwordless install, where the field had to be left blank and nothing said
	// so. PUT /api/auth/password already handled that case correctly, so the defect was entirely
	// in what the surface CLAIMED.
	HasPassword bool `json:"has_password"`
}

// StorageZFSKey is the keypair quince uses to reach the ZFS helper, as a form needs it
// (quince#818 piece B). POST /api/storages/zfs/key.
//
// EVERYTHING HERE IS SAFE TO RENDER. The private half never leaves `/data/keys/` — it is not on this
// type, it is not logged, and it is never in a fixture. Same discipline as a backup password.
type StorageZFSKey struct {
	// Path is where the private half lives, so the form can say what `ssh_key` would point at.
	Path string `json:"path"`
	// PublicKey is the `ssh-ed25519 AAAA… quince` line on its own.
	PublicKey string `json:"public_key"`
	// AuthorizedKeys is the COMPLETE line to paste on the ZFS host, forced command included.
	//
	// BOTH ARE SERVED because they are different acts: the public key is what an operator recognises,
	// and this is what they must actually paste. A key shown WITHOUT its `command="…"` prefix invites
	// pasting a naked key, which is an unconstrained shell login on the storage host rather than a
	// helper pinned to one dataset.
	AuthorizedKeys string `json:"authorized_keys"`
	// Created is true when quince made this key just now, false when it FOUND one already there.
	//
	// ON THE WIRE BECAUSE THE FORM MUST SAY WHICH. "quince made you a key" and "quince found your
	// existing key" call for different next steps — the first needs pasting, the second may already
	// be installed — and guessing wrong invites an operator to replace an entry that works.
	//
	// IT DESCRIBES THE STORAGE, NOT THE FILE (quince#1038). As *did this call write a file* it was
	// permanently false: the debounced re-fetch meant the keystroke finishing a dataset name found
	// what an earlier keystroke had made, so the panel said *quince found an ssh key it made earlier*
	// about a key one second old.
	Created bool `json:"created"`
	// Pending says this key is not in `/data/keys/zfs-*` yet — it is the single `.pending` key, shown
	// for a storage nobody has added.
	//
	// A KEY UNDER `zfs-*` MEANS A STORAGE QUINCE COMMITTED TO (quince#1038). Nothing reaches that
	// surface until *Add this storage*, so the directory answers *which parents can quince reach*
	// exactly, rather than recording everything anyone ever typed into the field.
	Pending bool `json:"pending"`
	// LandsAt is where a pending key will be moved on Add; empty when `pending` is false.
	//
	// ON THE WIRE SO THE SCREEN SAYS WHERE IT IS GOING rather than where it is. `path` names the
	// dot-file it sits in now, which is not somewhere an operator should point `ssh_key`.
	LandsAt string `json:"lands_at"`
	// Fingerprint is the `SHA256:…` of the public half — the string `ssh-keygen -lf` prints.
	//
	// THE SAVE CARRIES IT BACK, which is constraint 2 of quince#1038's ruling. One pending key is
	// shared by every open tab, so `POST /api/config/storage` must prove it is committing the key
	// whose line the operator actually pasted, and refuse rather than quietly making another.
	Fingerprint string `json:"fingerprint"`
}

type StorageZFSKeyResponse struct {
	Key StorageZFSKey `json:"key"`
}

// StorageZFSKeyRequest names the dataset the key will be confined to — quince#985.
//
// THE ONLY FIELD, AND DELIBERATELY NOT A PATH. The endpoint took no body at all until the parent
// moved into the forced command; §1's rule that it must not accept a *path* is unchanged, because a
// caller-supplied path would make it a write-a-file-anywhere primitive whose contents are a private
// key. A dataset name is interpolated into a line quince renders on screen and never writes itself.
type StorageZFSKeyRequest struct {
	// ParentDataset is what goes inside `command="/usr/local/sbin/quince-zfs-helper <this>"`.
	// An unsafe name is 422 naming this field — refused rather than escaped.
	ParentDataset string `json:"parent_dataset"`
}

// StorageZFSHelperResponse carries the constrained helper script — quince#818 piece C.
//
// THE SCRIPT IS THE SAME BYTES FOR EVERY INSTALL since quince#985, so this response is a constant.
// It used to arrive with the operator's own `parent_dataset` substituted into a `PARENT=` line,
// which made the one documented install path a collision: a second zfs storage on the same host
// saved its helper over the first's, and the first broke at its next commit. The dataset now rides
// in the `authorized_keys` forced command, which is per key, so nothing installation-specific is
// left in the file.
type StorageZFSHelperResponse struct {
	// Script is the complete file, ready to save as /usr/local/sbin/quince-zfs-helper.
	Script string `json:"script"`
	// Path is where it goes. On the wire because it is half the instruction — a script with no
	// destination is a thing the operator still has to look up, which is what piece C exists to end.
	Path string `json:"path"`
	// SourcePath is where this same script is served as plain text, for the machine that has to
	// install it — `/zfs/helper`, unauthenticated, no parameters.
	//
	// A PATH AND NOT A URL, because quince does not know its own address. What reaches an operator's
	// storage host has to be an address that works from there, and the only one quince can be sure
	// of is the one the client is already using — so the client joins this to its own origin. A
	// daemon-side guess would be config quince cannot verify, on a screen where a wrong address
	// looks exactly like a right one until somebody runs it.
	//
	// ON THE WIRE RATHER THAN HARDCODED IN THE UI so the route and the link cannot drift. Moving the
	// route would otherwise leave a `curl` line pointing at a 404 that nobody meets until they are
	// on the storage host with a terminal open.
	SourcePath string `json:"source_path"`
}

// StorageZFSHostKeyRequest asks what host key an address offers (quince#912).
type StorageZFSHostKeyRequest struct {
	SSHHost string `json:"ssh_host"`
	SSHPort int    `json:"ssh_port,omitempty"`
}

// StorageZFSHostKey is what the scan found — all of it public by construction, since every client
// that connects to that host is handed the same key.
type StorageZFSHostKey struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	KeyType string `json:"key_type"`
	// Fingerprint is the SHA256 form ssh prints, so it compares character for character against
	// `ssh-keygen -lf /etc/ssh/ssh_host_<type>_key.pub` run on the host — the one command an
	// operator can actually use to check it.
	Fingerprint string `json:"fingerprint"`
	// Line is the complete known_hosts entry, and it goes BACK on the trust call unchanged.
	//
	// THAT ROUND TRIP IS THE SECURITY PROPERTY, not a convenience. If trust re-scanned instead, a
	// host answering differently between the two calls would have its key recorded after the
	// operator confirmed a different fingerprint, and the confirmation would mean nothing.
	Line string `json:"line"`
}

// StorageZFSHostKeyResponse carries the scan. `reason` is set when there is no key to show —
// unreachable, refused, no answer — and is the daemon's own sentence.
type StorageZFSHostKeyResponse struct {
	Found   bool               `json:"found"`
	HostKey *StorageZFSHostKey `json:"host_key"`
	Reason  string             `json:"reason"`
	// Trust is what `known_hosts` ALREADY says about the key just scanned: `unknown`, `trusted` or
	// `changed`. Empty when there is no key to compare against.
	//
	// WITHOUT IT THE SCAN TELLS THE OPERATOR NOTHING THEY DO NOT ALREADY KNOW, and the ceremony
	// reads identically on every press: compare this fingerprint, then confirm it. Pressed on a host
	// confirmed an hour earlier it asked for the comparison again and offered a button that would do
	// nothing, since TrustHostKey returns early on an exact match (Operator, 2026-08-14).
	//
	// `changed` IS WHY THIS IS THREE VALUES AND NOT A BOOLEAN, and it is the one that matters. A
	// host offering a DIFFERENT key from the recorded one is either a rebuilt machine or something
	// impersonating it, and quince cannot tell which. Trust already refuses that; reporting it at
	// SCAN time moves it to the moment the operator is looking at the fingerprint, which is the
	// moment they can act on it.
	Trust string `json:"trust,omitempty"`
}

// StorageZFSHostKeyTrustRequest records a confirmed key. It carries the LINE the operator was
// shown, never a host to re-scan.
type StorageZFSHostKeyTrustRequest struct {
	Line string `json:"line"`
}

// StorageZFSHostKeyTrustResponse says where it was written, so the screen can name the file.
type StorageZFSHostKeyTrustResponse struct {
	Trusted bool   `json:"trusted"`
	Path    string `json:"path"`
}

// ReauthFinish is POST /api/auth/reauth/finish's response (qn.6n D4).
//
// A TOKEN AND NOTHING ELSE — no session, no CSRF token, no `state`. The endpoint it is modelled on,
// `passkeys/login/finish`, returns all three because its job is to sign somebody in; this one's job
// is to prove a credential is present, and the difference is the whole of D3. A response shape that
// looked like a login's would be the first step towards behaving like one.
//
// The proof is single-use, expiring, and bound to the operation named at `begin`, to the credential
// that asserted, and to the session that began the ceremony.
type ReauthFinish struct {
	Proof string `json:"proof"`
}

// PushSubscription is one device's Web Push registration AS THE UI SEES IT (qn.12, contracts §1).
//
// THE ENDPOINT AND THE KEYS ARE ABSENT, AND THAT IS THE POINT. They are capability-grade — anyone
// holding them can push to that phone — so the wire shape carries what a person needs to recognise
// and manage a device, and nothing anyone could send with. A field added here that could be used to
// deliver a push would put a capability behind an ordinary session read.
type PushSubscription struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// State is `live` | `expired`. EXPIRED ROWS ARE LISTED rather than hidden: a device that stopped
	// receiving must be nameable, or the failure is invisible and its first symptom is a missed
	// backup (qn.12 spec D8).
	State      string `json:"state"`
	CreatedAt  string `json:"created_at"`
	ExpiredAt  string `json:"expired_at,omitempty"`
	LastSentAt string `json:"last_sent_at,omitempty"`
	// Fingerprint identifies WHICH row belongs to the browser reading this list, without the list
	// ever carrying an endpoint.
	//
	// A SHA-256 OF THE ENDPOINT, base64url. The browser holds its own endpoint and can hash it; the
	// server holds every endpoint and hashes each. Neither has to send one. This is NOT a capability:
	// an endpoint is a high-entropy URL, so the digest is not reversible and cannot be pushed to,
	// which is what lets it appear in a response that D8 forbids endpoints from appearing in.
	//
	// IT REPLACES A LOCALLY REMEMBERED ID, which was wrong in a way that only hardware showed: the id
	// was stored at subscribe time, so every subscription created before that code existed — and every
	// cleared profile, and every private window — reported its own device as Off while it was
	// subscribed and receiving. Operator-reported 2026-08-18: an iPhone looking at its own row.
	Fingerprint string `json:"fingerprint"`
}

// NotificationsResponse is GET /api/notifications (qn.12, contracts §1).
//
// THE PUBLIC KEY RIDES WITH THE LIST because a browser needs it BEFORE it can subscribe, and a
// second round trip to fetch it would be a round trip whose only purpose is tidiness. It is public
// by construction — the `applicationServerKey` every subscription is created against.
//
// THE CATEGORY TOGGLES ARE NOT HERE. They are config, read through GET /api/config and written
// through PUT, which is what keeps them hand-editable and restart-free (D12). Duplicating them onto
// this response would create a second source of truth for a setting the config contract owns.
type NotificationsResponse struct {
	VAPIDPublicKey string             `json:"vapid_public_key"`
	Subscriptions  []PushSubscription `json:"subscriptions"`
}

// PushDeliveryResult is what happened to one device on a send (qn.12).
//
// BY LABEL, NEVER BY ENDPOINT. This is the shape that ends up in a log line or pasted into an issue,
// and an endpoint plus its keys is a capability against that phone.
//
// THREE STATES, NOT A BOOLEAN. `sent`, `expired` and `error` are distinct because "the push service
// was unreachable" and "the phone is gone" are different facts with different remedies — re-subscribe
// on that device, versus try again — and a caller that cannot tell them apart cannot report either
// honestly.
type PushDeliveryResult struct {
	Label string `json:"label"`
	// State is `sent` | `expired` | `error`.
	State string `json:"state"`
	// Error is present only for `error`, and never carries an endpoint path.
	Error string `json:"error,omitempty"`
}

// DeviceUDID lets `EventDevice` read a payload's device without `wire` importing its producer.
//
// ONE METHOD PER DEVICE-BEARING PAYLOAD, and the set is closed by the gate over the event constants.
// A payload that gains a device later must gain this too, or its events reach only the admin — which
// is the safe direction to be wrong in, and the gate names it either way.
func (d Device) DeviceUDID() string  { return d.UDID }
func (j Job) DeviceUDID() string     { return j.UDID }
func (v Version) DeviceUDID() string { return v.UDID }
func (o Op) DeviceUDID() string      { return o.UDID }

// Enrolment is one outstanding enrolment secret, as the ADMIN sees it — qn.13 slice 9c, spec D4.
//
// NO SECRET FIELD, AND THAT IS THE CONTRACT RATHER THAN AN OMISSION. The value is returned exactly
// once, by the mint call, in `EnrolmentIssued`. A listing that could re-display it would make every
// GET of the device page a fresh chance to leak a live credential into a screenshot, a cache or a
// log — and the ruling asked that unused secrets be VISIBLE and REVOCABLE, not re-displayable.
type Enrolment struct {
	// ID names this secret for revocation. Safe in a response, a URL path and a log line, which is
	// exactly what the secret is not.
	ID string `json:"id"`
	// UDID is the device the credential this mints will be confined to.
	UDID      string `json:"udid"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// EnrolmentIssued is the mint response, and the ONLY place a secret appears on the wire.
type EnrolmentIssued struct {
	Enrolment
	// Secret is shown once. The client composes the QR's URL from it and its OWN address — quince
	// never guesses one (spec D5): the address the admin is currently using is knowable in the
	// browser and not on the server, which may sit behind a proxy that strips or misreports it.
	Secret string `json:"secret"`
}

// Overview is GET /api/sessions/{id}/overview — contracts §1's frozen domain envelope, plus
// the two additive fields qn.9 amends it with.
//
// THE ENVELOPE'S OWN FIELDS KEEP THEIR SHAPES AND MEANINGS EXACTLY, which is what makes this
// additive rather than a spend of the freeze (architect ruling, quince#1459). A client that
// ignores the new fields behaves identically, and no domain endpoint that never sets them can
// be observed to have changed.
type Overview struct {
	// Capabilities is what THIS adapter can do — the frozen field, unchanged. It is NOT the
	// per-domain capability report; that is Domains below, and the two sharing a word is
	// what quince#1459 was filed about.
	Capabilities []string `json:"capabilities"`

	AdapterVersion string `json:"adapter_version"`

	// Warnings is the home for anything degraded, per the envelope.
	Warnings []string `json:"warnings"`

	// UnsupportedReason is null for overview: it can always serve something, because the
	// device summary and the domain totals do not depend on any domain parsing. The
	// per-domain equivalent is each DomainCapability's own State.
	UnsupportedReason *string `json:"unsupported_reason"`

	Page OverviewPage `json:"page"`

	// Domains is the per-domain capability report (qn.9 D6).
	//
	// ABSENT, NOT NULL, when an endpoint has no report — `omitempty` on a nil slice. Ruled
	// explicitly rather than left to the implementation (quince#1459 condition 1), because
	// this project already has quince#744 open about a field that means three things and a
	// client cannot tell them apart. A client tests for the key's presence; it never has to
	// distinguish a null from an empty list, because an endpoint WITH a report always sends
	// at least one row and one WITHOUT sends no key at all.
	Domains []DomainCapability `json:"domains,omitempty"`

	// Totals are the whole-version figures the Page's rows sum to.
	//
	// CARRIED BECAUSE THE PAGE IS PAGINATED. qn.9 D3 requires that what a surface shows and
	// what it claims as a total reconcile — and a client holding one page of 1,264 domain
	// rows cannot compute the total itself. Without this the reconciliation is unprovable at
	// exactly the scale that makes it matter.
	Totals OverviewTotals `json:"totals"`
}

// OverviewPage is the envelope's `page`, whose shape is frozen as {items, next_cursor}.
type OverviewPage struct {
	Items      []DomainSummary `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

// DomainSummary is one domain's file count and size in this version.
//
// THESE ARE DOMAINS, NOT APPS, AND THE DIFFERENCE IS NOT COSMETIC. qn.9 D3 measured four app
// counts on one real backup — 21 user-installed, 1,203 bundles with a container, 1,205 app
// domains holding files, 1,264 domains in total. This carries the LAST of those, because it
// is what the manifest can answer without a password. Naming the user-installed subset needs
// Info.plist, which arrives with the pre-unlock tier.
type DomainSummary struct {
	Domain string `json:"domain"`
	Files  int64  `json:"files"`
	Bytes  int64  `json:"bytes"`
}

// OverviewTotals are the version-wide figures.
type OverviewTotals struct {
	Files int64 `json:"files"`
	Bytes int64 `json:"bytes"`

	// DomainCount is the number of domains in the version, which is NOT len(page.items)
	// unless
	// the caller has walked every page. A client showing "N of M" needs both.
	//
	// NAMED `domain_count`, NOT `domains`, BECAUSE THE ENVELOPE ALREADY HAS A `domains`. One
	// response carrying two fields of that name — a count here, the capability report at top
	// level — is two different things wearing one word, which is the exact confusion
	// quince#1459 was filed about. Caught by a test whose substring check matched the wrong
	// one.
	DomainCount int `json:"domain_count"`
}

// DomainCapability is one row of the capability report.
type DomainCapability struct {
	Domain string `json:"domain"`

	// State is one of: supported, unsupported_schema, absent, unreadable. FOUR, because an
	// unrecognised schema and bytes that are not a database have different remedies — the
	// first invites a schema-support issue and needs the fingerprint below, the second means
	// the backup is damaged (qn.9 D6).
	State string `json:"state"`

	Schema string `json:"schema,omitempty"`

	// Missing names record fields this backup's schema cannot provide — "no silent caps" as
	// a data structure rather than as a discipline.
	Missing []string `json:"missing,omitempty"`

	// Fingerprint is the observed structure, present only on unsupported_schema. Report it
	// when filing a schema-support issue: without it "unsupported" is a dead end for whoever
	// has to add support, and it is what distinguishes that state from unreadable.
	Fingerprint string `json:"fingerprint,omitempty"`
}
