# quince — cross-track contracts (v0)

> The frozen interfaces that let the core, UI, and vault tracks build in parallel.
> A contract change is a cross-track event: it lands here first, version-bumped, and
> every affected track gets a rung. Field additions are non-breaking; renames/removals
> are breaking and need Operator sign-off.
>
> Wire casing: `snake_case` JSON everywhere. Times: RFC 3339 UTC strings. IDs: ULIDs.

## 1. REST API (`/api`)

Auth (endpoints ruled in qn.1, Operator 2026-07-19):

```
GET  /api/auth/status  → {state: "needs_setup" | "needs_login" | "authenticated",
                          csrf_token}
     // first-run detection + reload-auth check + CSRF-token delivery in one call;
     // always reachable without a session.
POST /api/auth/setup {password}  → 200 {state, csrf_token} + session cookie
     // FIRST-RUN ONLY: 409 if a password already exists — setup succeeds exactly once
     // and can never be an unauthenticated password reset. Auto-logs-in on success.
POST /api/auth/login {password}  → 200 {state, csrf_token} + HttpOnly session cookie
     // 401 on bad password; 429 when the per-IP login rate limit trips.
POST /api/auth/logout            → 204, clears the cookie.
```

Everything else requires the session cookie. State-changing requests (POST/PUT/DELETE,
except `login`/`setup`) must echo the CSRF token in the `X-CSRF-Token` header — a
double-submit check against the readable `quince_csrf` cookie. The session cookie is
`HttpOnly` + `SameSite=Strict` + `Secure` (Secure relaxed only for loopback-http and
`--demo`, so local/e2e over plain http still works — never in production). Errors are
`{error: {code, message}}` with sensible HTTP statuses.

### Devices

```
GET  /api/devices                      → {devices: Device[]}
GET  /api/devices/{udid}               → Device
POST /api/devices/{udid}/pair          → 202 {op_id}     // surfaces "tap Trust on phone"
POST /api/devices/{udid}/pair/validate → {paired: bool}
POST /api/devices/{udid}/encryption
     {action: "enable" | "change_password" | "disable",
      password?, old_password?, new_password?}            → 202 {op_id}
     // Drives `idevicebackup2 encryption`/`changepw`. Passwords travel in the TLS body
     // and reach the subprocess via interactive pty prompt (or the documented
     // BACKUP_PASSWORD env fallback, same-uid exposure only — qn.3 verifies which);
     // NEVER argv (world-readable /proc), never logged, never stored. The phone will
     // demand its own passcode confirmation — the op narrates that state to the UI.
     // NOTE: this is Apple's device-global backup password — the SAME password later
     // used to unlock versions in the vault. quince sets it, never keeps it.
GET  /api/ops/{op_id}                  → Op
     // pair/encryption return 202 {op_id}; the op's narration (e.g. "tap Trust on the
     // phone", "enter the passcode on the device") streams via `op.updated` WS events,
     // with this endpoint as the poll/refresh fallback.
```

### Jobs

```
POST /api/jobs {udid, transport: "usb"|"wifi"|"auto"}   → 202 Job
GET  /api/jobs?cursor&limit&udid                        → {jobs: Job[], next_cursor}
GET  /api/jobs/{id}                                     → Job
POST /api/jobs/{id}/cancel                              → 202 Job
GET  /api/jobs/{id}/log                                 → text/plain (full so-far; live tail is WS)
```

### Config

```
GET /api/config   → {config, warnings: [], source: {path, mtime}}
PUT /api/config   → full-document replace; validated then atomically written to
                    /data/config.yml; 422 {errors: [{path, message}]} on invalid
```

### Automation (shape frozen now; implemented in qn.12 — the assisted-backup flow, stack D13)

```
POST /api/automation/backup-opportunity {udid, trigger: "connected_to_power" | "manual"}
     → {action: "notify" | "none",
        reason: "backup_stale" | "backup_fresh" | "device_not_visible"
              | "job_running" | "recently_reminded"}
```

The Shortcut is a dumb opportunity signal (short-lived token auth); ALL policy is
server-side: device visibility, staleness threshold, active-job check, reminder
cooldown. Push kinds (Web Push, qn.12): `backup_available`, `action_required`,
`backup_completed`, `backup_overdue` — each deep-links to the device page.

### Versions & browsing

A **Version** is one immutable committed backup — on the zfs backend it IS a
`@quince-*` snapshot; on namespace backends it is the `latest/` dir (newest) or a
rotated-out `versions/<ts>/` dir. The password is never persisted — unlock is
per-session, always.

```
GET    /api/versions?udid              → {versions: Version[]}
DELETE /api/versions/{id}              → 202            // confirmed destructive action
POST   /api/versions/{id}/unlock {password} → Session
POST   /api/sessions/{id}/lock         → 204
GET    /api/sessions/{id}/browse?domain&prefix&cursor   → {entries: FileEntry[], next_cursor}
GET    /api/sessions/{id}/file/{file_id}                → streamed decrypted content
```

Domain endpoints (messages, photos, overview) are specified in their rungs (`qn.9+`) and
appended here when built; they are session-scoped lazy reads (`/api/sessions/{id}/...`)
following the same pagination/casing rules. **Only the domain envelope is frozen now**
(external-review point, accepted — concrete fields are designed after a research spike
on real iOS schemas, never before):

```jsonc
{"capabilities": ["threads", "attachments", "search"],   // what this adapter can do
 "adapter_version": "sms-ios17-26.v1",
 "warnings": ["attributedBody fallback used for 12 messages"],
 "unsupported_reason": null,      // set when the adapter can't serve this backup at all
 "page": {"items": [...], "next_cursor": "..."}}
```

## 2. Objects

```jsonc
Device: {
  "udid": "00008140-...",
  "name": "family-iphone",
  "model": "iPhone17,2",            // raw; UI maps to marketing name
  "ios_version": "26.0.1",
  "transports": {"usb": "2026-07-18T...", "wifi": "2026-07-18T..."}, // present keys only
  "paired": "yes" | "no" | "unknown",
  "backup_encryption": "on" | "off" | "unknown",   // lockdown com.apple.mobile.backup/WillEncrypt
  "last_seen": "...",
  "last_backup": {"at": "...", "job_id": "...", "status": "succeeded"} | null
}

Job: {
  "id": "01J...", "udid": "...", "kind": "backup",
  "transport": "usb" | "wifi",
  "state": "queued" | "waiting_for_device" | "preflight" | "backing_up" | "verifying"
         | "committing" | "succeeded" | "failed" | "cancelled" | "connection_lost",
  "progress": {"phase": "receiving",                          // incl. "waiting_for_passcode"
               "percent": 63.0,                               // percent nullable
               "bytes_done": 2400000000, "bytes_total": 3600000000,
               "files_received": 149,
               "liveness": "active"},   // active | silent_but_connected | suspected_stall
  "started_at": "...", "finished_at": "..." | null,
  "error": {"code": "device_disconnected", "message": "..."} | null,
  "retry_of": "01H..." | null,          // set when the user retried a failed job
  "intent_id": "01H...",                // root of the retry chain (== id for a first
                                        // attempt); groups attempts into one user-level
                                        // "I wanted a backup" operation
  "attempt": 2,                         // 1-based position within the intent
  "version_id": "..." | null            // set on succeeded
}
// UI contract: history is grouped by intent_id — a failed-then-retried-then-succeeded
// night renders as ONE operation ("Backup completed after 1 retry"), with attempts
// expandable for diagnostics. GET /api/jobs returns attempts; grouping is client-side
// (or via ?group=intent later). A full Intent entity (server-side object owning
// attempts, wired to automation pushes) is a parked future evolution — retry_of +
// intent_id carry the model until it's needed.

Version: {
  "id": "...", "udid": "...", "backend": "zfs" | "reflink" | "hardlink" | "copy",
  // zfs: a version IS a snapshot; browse_root goes through .zfs (read-only by nature).
  // namespace backends (reflink/hardlink/copy): a version is an immutable dir.
  "zfs_snapshot": "rpool/.../<udid>@quince-01J...-2026-07-18" | null,   // zfs backend only
  "browse_root": "/backups/<udid>/.zfs/snapshot/quince-01J.../working"  // zfs (per-device dataset)
              |  "/backups/<udid>/latest"                                // namespace backends, newest
              |  "/backups/<udid>/versions/2026-07-18T02-30-11Z",        // namespace, rotated-out
  // browse_root is computed per request on namespace backends: a version moves from
  // latest/ to versions/<ts>/ when the next commit rotates it.
  "created_at": "...", "job_id": "..." | null,
  // job_id null = adopted: a quince-format version found on disk/in snapshots without
  // a DB record (e.g. dataset replicated/restored to a fresh host; reconciliation
  // re-registers from quince-version.json). Adopted, listed, protected from retention
  // until the user says otherwise.
  "kind": "full" | "incremental" | "unknown",
  "encrypted": true,        // unencrypted versions are permanently badged incomplete
  "is_latest": true,
  "structure_verified_at": "..." | null,   // set at commit (structural verification)
  "content_verified_at": "..." | null,     // set by verify_canary on a later unlock
  "logical_bytes": 42400000000, "physical_bytes": 3400000000   // best-effort
}

Op: { "id": "...", "udid": "...", "kind": "pair" | "encryption",
      "state": "running" | "waiting_for_user" | "succeeded" | "failed",
      "message": "Tap Trust on the phone…",   // plain-language narration for the UI
      "error": {"code", "message"} | null }

Session: { "id": "...", "version_id": "...", "expires_at": "..." }

FileEntry: { "file_id": "ab12...", "domain": "CameraRollDomain",
             "relative_path": "Media/DCIM/100APPLE/IMG_0001.HEIC",
             "kind": "file" | "dir" | "symlink", "size": 123, "mtime": "..." }
```

## 3. WebSocket (`/api/ws`)

One socket per client, server→client only (commands go via REST). Envelope:

```jsonc
{"type": "job.updated", "ts": "2026-07-18T...", "data": { ... }}
```

| type | data | notes |
| --- | --- | --- |
| `device.attached` / `device.detached` | `Device` + `{transport}` | emitted per transport edge |
| `device.updated` | `Device` | name/pairing/info refresh |
| `job.updated` | `Job` | every state or progress change; progress throttled to ≤2/s |
| `job.log` | `{job_id, chunk}` | raw log tail chunks |
| `op.updated` | `Op` | pair/encryption op narration + state changes |
| `version.created` / `version.deleted` | `Version` | includes adopted versions found on disk |
| `session.locked` | `{session_id, reason: "user" \| "ttl" \| "vault_crash"}` | UI drops decrypted views |
| `hello` | `{server_version, time}` | first frame after auth |

Client contract: reconnect with backoff + `GET` refresh of current views on reconnect
(events are notifications, not a replayable log).

## 4. Vault RPC (core ⇄ `quince-vault serve`)

JSON-RPC 2.0, newline-delimited, over stdio. The first frame MUST be `initialize` —
password and backup path travel inside it (stdin-only, never argv/env; raw RPC frames
are never logged). The vault is spawned with its **session scratch root as its only
writable directory**; no filesystem destination ever crosses the RPC boundary — the
vault writes only under its root and returns opaque handles with scratch-relative paths.
The version dir is passed read-only. **This protocol is the replaceable seam**: the core
talks to a `vault.Vault` Go interface; any implementation (today's Python process, a
future all-Go port) must pass the golden conformance suite (`vault/conformance/`) —
recorded request/response pairs against fixture backups — before it can ship.

```
initialize  {password, backup_path}          → {protocol_version, device_name,
                                                ios_version, file_count, manifest_sha256}
list        {domain?, prefix?, cursor?, limit} → {entries: FileEntry[], next_cursor}
stat        {file_id}                          → FileEntry
materialize {file_id}                          → {handle, rel_path, size}
                                               // decrypted under scratch root; core
                                               // resolves rel_path against the root it
                                               // owns, streams, unlinks
verify_canary {}                               → {ok}   // decrypt one small known file;
                                               // basis for content_verified_at
lock        {}                                 → {}     // then process exits 0
```

Domain methods (`overview.*`, `messages.*`; `photos.*` if ever revived) are appended
here with their rungs (`qn.9+`); all reads are lazy (domain DBs decrypted to scratch on
first use) and paginated. Errors: JSON-RPC error with `data.code ∈ {bad_password, corrupt_manifest, io,
not_found, unsupported_ios}`. The core treats malformed output or nonzero exit as a vault
crash: session dies, `session.locked{reason: "vault_crash"}`, user sees it honestly.

## 5. Derived caches (`/cache`)

No persistent index of backup content exists (Operator decision — lazy session reads
only). The narrow exception: derived artifacts genuinely too expensive to rebuild per
session. **Currently this section has no consumer** — photos (the only planned one) are
parked at lowest priority, and if they return, the first move is reusing Apple's own
prebuilt thumbnails inside the backup (`CameraRollDomain → Media/PhotoData/Thumbnails`),
which may make this section permanently unnecessary. The contract stays defined for
whatever earns it:

```
/cache/derived/<version_id>/<artifact>/...
/cache/derived/<version_id>/fingerprint    // {manifest_sha256, artifact_schema_version}
```

Rules: validate fingerprint against the live version before *every* use; on mismatch or
missing source, drop silently and rebuild or serve without; wiping `/cache` at any time
is always safe and never user-visible beyond latency. Session scratch lives in
`/cache/scratch/<session_id>/` and is wiped on lock.

## 6. Config

**Bootstrap env** — deployment topology only, everything a container needs before the
app can run (unknown `QUINCE_*` vars are a startup warning, typo guard):

```
QUINCE_DATA=/data   QUINCE_CACHE=/cache   QUINCE_BACKUPS=/backups
QUINCE_LISTEN=:8080
```

**Everything else**: `/data/config.yml` — single source of truth, edited by the UI and
by hand equally (stack D12: atomic validated writes, canonical order + generated
doc-comments, file-watch pickup, invalid edits keep last-good + UI banner, no secrets
ever). Schema v0:

```yaml
backup:
  transport: auto           # auto (prefer usb when plugged, else wifi) | usb | wifi
  require_encryption: true  # preflight fails (actionably) on an unencrypted device;
                            # false permits unencrypted backups behind persistent UI
                            # warnings (no Health/Keychain/passwords in such backups)
storage:
  backend: auto             # auto | zfs | reflink | hardlink | copy
                            # auto: zfs when storage.zfs is configured, else probe
                            # reflink → hardlink → copy on the /backups filesystem
  zfs:
    parent_dataset: ""      # e.g. rpool/userdata/iphone-backup; one child dataset per device
    mode: exec              # exec (delegated) | hook
    hook_cmd: ""            # e.g. ssh -i /data/keys/zfs pve quince-zfs-helper
                            # (forced-command: snapshot/destroy/list @quince-*, create children;
                            #  dataset destroy deliberately impossible via the key)
    mirror: auto            # latest/ build strategy: auto (reflink → hardlink → copy) | reflink | hardlink | copy
  retention:
    keep_recent: 10
    keep_daily: 30
    keep_weekly: 12
devices:
  usbmuxd_socket: /var/run/usbmuxd
  netmuxd_addr: 127.0.0.1:27015
sessions:
  ttl_minutes: 30
automation:                 # assisted-backup policy (consumed from qn.12)
  staleness_days: 3         # last good backup older than this → backup_available push
  reminder_cooldown_hours: 24
ui:
  theme: system             # system | light | dark
```

Schema is versioned by presence/absence of keys (missing keys = defaults, written back
on next save); a key the app doesn't know is a warning surfaced in UI, never an error.
