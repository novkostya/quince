# quince — cross-track contracts (v0)

> The frozen interfaces that let the core, UI, and vault tracks build in parallel.
> A contract change is a cross-track event: it lands here first, version-bumped, and
> every affected track gets a rung. Field additions are non-breaking; renames/removals
> are breaking and need Operator sign-off.
>
> Wire casing: `snake_case` JSON everywhere. Times: RFC 3339 UTC strings. IDs: ULIDs.

**BREAKING IS CHEAP HERE, AND THE CORRECT MODEL BEATS THE COMPATIBLE ONE.** Operator ruling,
2026-08-01 (quince#378): *"I am the only user atm and I see no reason to accumulate
backward-compatibility garbage."* The project is **pre-release with one operator**, and **the only
consumer of this API is the in-repo UI, shipped from the same commit** — a fact this document
already relies on elsewhere when it suits an argument (§`Op.kind`, the `wifi_sync` enum extension).
So when a change is between *the right shape* and *the shape that does not break a client*, take the
right shape. There is no client to break, and a compatibility path added now is one nothing will be
brave enough to remove in two years.

**Read the paragraph above this one accordingly.** *"Field additions are non-breaking; renames/
removals are breaking and need Operator sign-off"* still describes the **classification** and the
**sign-off**, and both stand — but it must not be read as a preference for additive changes. It was,
twice in one review: `qn.6c`'s gaps 1 and 3 were both first recommended on compatibility grounds, one
of them proposing a permanent implicit fallback to avoid editing a single YAML file on a single
machine. The Operator overruled it, and the architect's dissent is retracted on the record
(quince#378).

**IT APPLIES TO INTERFACE SHAPE, NOT TO PERSISTED STATE, AND THAT IS THE LOAD-BEARING LIMIT.** The
premise is *the only consumer ships from the same commit*. **A `quince-version.json` written last
month has no commit to ship with.** Neither does a `versions/<ts>/` directory, a `@quince-*` snapshot,
or a SQLite row behind a committed backup. For **data at rest the premise is simply false** — a
breaking change there is a **migration against data that cannot be regenerated**, and *never mutate a
committed version* is a hard rule sitting on the other side of it. Wire shape, config surface and
error codes are cheap to break; the things a backup is made of are not.

**This matters right now rather than in the abstract.** `qn.6c` — the rung that produced this ruling —
**is a data-model change**: multi-storage in the DB (`0006_storage`) plus `quince-storage.json` on
disk. A session designing that migration while reading *"breaking is cheap here"* is reading a true
statement in a context where its premise does not hold. Gap 4's *"written into a root that already
holds committed versions"* is exactly the class this limit governs.

**What this does NOT license.** It is not an argument against migrations, against `PROPOSED (gap)`,
or against Operator sign-off — a breaking change is still a **cross-track event** that lands here
first and still needs the ruling. It is cheaper, not free, and *"the UI ships from the same commit"*
is the whole of why. **The clause expires when that stops being true**: the first external consumer,
published API, or independently-deployed client retires it, and whoever adds one owns deleting this
paragraph.

**That "not an argument against migrations" sentence was too weak and is kept only as the record of
why** — it framed the limit as **process** (*still do the migration*) when the real point is that the
cheapness argument's **premise does not reach data at rest at all**. Doing the migration carefully is
the consequence; the premise failing is the reason. Caught by the supervisor seat on review, after the
paragraph had already been approved by the architect.

**One worked example, so the line is not read as "always break".** `qn.6c` gap 1 keeps
`Version.backend` — and that is *not* the compatibility reflex. A version can outlive its storage
once remove-a-storage exists, leaving `storage_id` dangling and the backend unrecoverable by join, so
the field is not derivable in all futures. It is a distinct fact, kept on modelling grounds. **The
test is whether the field earns its place, not whether removing it would break someone.**

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
     // 426 insecure_origin, BEFORE the password is stored (see below).
POST /api/auth/login {password}  → 200 {state, csrf_token} + HttpOnly session cookie
     // 401 on bad password; 429 when the per-IP login rate limit trips;
     // 426 insecure_origin, BEFORE the password is checked (see below).
POST /api/auth/logout            → 204, clears the cookie.
```

**`426 insecure_origin` on both credential endpoints.** When the session cookie a request
would earn is marked `Secure` while the origin is one the browser does not consider secure
— plain http to a non-loopback host, outside `--demo` — the endpoint refuses rather than
answering `200` with two cookies the browser then discards. Without it the client lands back
on the login form with no error of any kind, which is quince#497: a phone is not loopback,
so this is what the primary client of a Wi-Fi backup tool meets first. The response carries
an `Upgrade` header because RFC 9110 §15.5.22 makes one a MUST on a `426`; no browser acts
on it.

Two properties of the refusal are contract rather than implementation detail. It fires
**before the credential is examined**, so it answers identically for a right and a wrong
password and cannot become a password oracle over the one channel that is not encrypted —
and on `/setup` that is also what stops a refusal leaving the password set behind an error.
And it is conditioned on **the `Secure` decision**, not on the scheme: an option that turns
`Secure` off for plain-http transport turns the refusal off with it, which is how `qn.6f`'s
plain-http opt-in lands without a second switch to keep in step.

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
POST /api/devices/{udid}/pair          → 202 {op_id} | 404 | 409
     // 409: device not present on USB — pairing is USB-only at the protocol floor,
     // surfaced actionably ("pairing needs a USB connection"). The 202 op narrates
     // "tap Trust on the phone" / "enter the passcode on the device".
POST /api/devices/{udid}/pair/validate → {paired: bool} | 404 | 409
     // paired == "a pairing is CONFIRMED valid right now". A locked device cannot be
     // confirmed (`idevicepair validate` reports "passcode set" for ANY locked device,
     // paired or not — qn.3 hardware finding), so Device.paired shows "unknown" until
     // an unlocked validate succeeds; the endpoint's bool is the confirmed check only.
POST /api/devices/rescan               → 202 | 409
     // Restarts the MANAGED in-container USB muxer (devices.manage_muxer: true) so USB
     // devices missed by an unprivileged container's absent hotplug re-enumerate;
     // reuses the client's reconnect→Reset→replay reconcile (no new table semantics).
     // 409 when the muxer is external (manage_muxer: false) — quince doesn't own it.
     // Ruled from qn.2's gap capture; landed by qn.2b.
     // USB-ONLY by design (qn.4c, ruled (bz)): quince may also supervise netmuxd, but
     // rescan never restarts it — Wi-Fi has no hotplug problem to solve, and a restart
     // would tear a live Wi-Fi backup. Per-daemon state lives in GET /api/health.
POST /api/devices/{udid}/encryption
     {action: "enable" | "change_password" | "disable",
      password?, old_password?, new_password?}            → 202 {op_id} | 422
     // 422: bad action or a missing required password field. Drives `idevicebackup2
     // encryption`/`changepw`. Passwords travel in the TLS body and reach the
     // subprocess over an interactive pty (`idevicebackup2 -i` — VERIFIED qn.3,
     // libimobiledevice 1.4.0; the BACKUP_PASSWORD env fallback exists in the CLI but
     // quince does not use it); NEVER argv (world-readable /proc), never logged, never
     // stored. The phone will demand its own passcode confirmation — the op narrates
     // that state to the UI.
     // NOTE: this is Apple's device-global backup password — the SAME password later
     // used to unlock versions in the vault. quince sets it, never keeps it.
POST /api/devices/{udid}/wifi-sync
     {action: "enable" | "disable"}                         → 202 {op_id} | 404 | 409 | 422
     // qn.7, ruled at that rung's spec review (quince#332). Writes lockdown
     // com.apple.mobile.wireless_lockdown/EnableWifiConnections so Wi-Fi backups can be
     // turned on WITHOUT Finder — the D12 "everything in quince" promise for the PRIMARY
     // transport. 409: not connected, or NOT PAIRED — a lockdown write needs a trusted
     // session, and "not paired" is a state the user can act on where "the device
     // rejected it" is not. 422: unknown action.
     // NO PASSWORD, and that is a design point rather than an omission: the value is a
     // boolean, so this op uses the plain argv path, NOT the pty machinery that exists to
     // keep a password out of argv.
     // THE OP VERIFIES ITS OWN WRITE (decisions/0004). lockdownd_set_value succeeding means
     // the device ACCEPTED the request, not that the setting took effect — unverified for
     // this domain, which quince had never written before. So the op re-reads and compares,
     // and reports four distinguishable failures because the remedy differs:
     //   wifi_sync_failed        the device rejected it — retryable
     //   wifi_sync_not_applied   accepted and not applied; the state is UNCHANGED, not unknown
     //   wifi_sync_unconfirmed   accepted, and the read-back could not RUN — the state is
     //                           UNKNOWN, which is neither of the two above (quince#363)
     //   wifi_sync_unavailable   this build does not know the key, so quince will not guess
     // The unconfirmed/not_applied split is load-bearing rather than pedantic: conflating them
     // reported a write that had WORKED as "Wi-Fi sync is unchanged" on hardware, and the user
     // was told to expect a device that still syncs while holding one that needed a cable.
     // unconfirmed does NOT appear on the one path where an unreadable read-back is EXPECTED:
     // disabling over Wi-Fi severs the connection the read-back would use, so there the op
     // SUCCEEDS and the value becomes wifi_sync: "unknown" (ruled quince#363).
POST /api/devices/{udid}/reset-working {storage_id?} → 202 {note} | 404 | 409 | 503
     // qn.5b Reset (accepted contract proposal, decisions (co)): DISCARD the device's dirty
     // working/ so the next backup starts clean from latest/ — losing only the partial, NEVER a
     // committed version. Idempotent (a device with no working/ is already clean → 202). 409 while
     // a backup is running for the device (single-flight; cancel it first); 404 unknown device;
     // 503 no backup engine wired (--demo). The backend op is RepairWorkingCopy (CLI:
     // `quince device reset-working <udid>`). The honest COMPANION of a kept-dirty working/: on
     // failure the partial is kept so a retry RESUMES (no re-transfer); Reset is the explicit
     // discard. Audited (reset event, no secret, NAMING THE STORAGE); touches no version.*
     // / latest surface.
     //
     // qn.6c (Operator ruling 2026-08-02, quince#448): the endpoint is DEVICE-scoped and a
     // device can now have a dirty working/ on more than one storage, so "reset this
     // device's working copy" stopped having one answer. `storage_id` is OPTIONAL:
     //   present, usable       reset exactly that one
     //   present, unknown      404, matching unknown-device
     //   present, unreachable  409, carrying that storage's own unreachable_reason —
     //                         the job path's code, not a new one for a known condition
     //   omitted, 0 dirty      202, already clean (unchanged)
     //   omitted, exactly 1    reset it, and NAME it in the note
     //   omitted, 2 or more    409, listing them, saying to name one
     // REFUSE AND NAME RATHER THAN GUESS WELL: a dirty working/ is a resumable multi-hour
     // partial, so "reset all" would discard a transfer on a disk the user was not
     // thinking about — the same answer quince#435 gave a job that names no storage.
     // "Dirty" is `working/<udid>` existing, INCLUDING a killed seed. Unreachable storages
     // are NAMED as not-inspected, never silently skipped. CLI: `--storage <name>`.
     // No deployment with one storage can reach the new refusal.
GET  /api/ops/{op_id}                  → Op
     // pair/encryption return 202 {op_id}; the op's narration (e.g. "tap Trust on the
     // phone", "enter the passcode on the device") streams via `op.updated` WS events,
     // with this endpoint as the poll/refresh fallback.
```

### Jobs

```
POST /api/jobs {udid, transport: "usb"|"wifi"|"auto", retry_of?}   → 202 Job
     // Error codes: 409 a backup is already running for this UDID (never two concurrent jobs
     // per device); 422 bad/omitted transport, OR transport:"auto" when the device is present
     // on NEITHER transport (qn.4b, design §4/(bp): auto resolves against current presence —
     // prefer USB when plugged, else Wi-Fi — and refuses actionably when absent, since a guessed
     // transport would persist a dishonest Job.transport; the Job stores the resolved concrete
     // usb|wifi, never "auto"); 404 unknown device; 503 no backup engine wired (e.g. --demo before
     // qn.4b; from qn.4b --demo scripts on-demand jobs and the command surface is live). retry_of
     // (optional) sets the assisted-model retry chain: the new job inherits intent_id from the
     // chain root and increments attempt.
GET  /api/jobs?cursor&limit&udid                        → {jobs: Job[], next_cursor}
     // cursor pagination newest-first; the cursor is the last job id of the prior page.
GET  /api/jobs/{id}                                     → Job
POST /api/jobs/{id}/cancel                              → 202 Job
     // (qn.4a): 409 the job is not running (already terminal); 404 unknown job; 503 no engine.
GET  /api/jobs/{id}/log                                 → text/plain (full so-far; live tail is WS)
```

**RULED and IMPLEMENTED (was `PROPOSED (gap)`): a storage collection, and a job that names one — `qn.6c`, quince#378.** The READ half — `GET /api/storages`, the `Storage` object and `POST /api/storages/{id}/recheck` — ships in story 5c. `POST /api/jobs {storage_id}` is ruled and NOT yet built; it lands with story 6.
Storage becomes plural at `qn.6c`, so a backup must be able to say *where*. Additive:

```
GET  /api/storages                                        → {storages: Storage[]}
POST /api/jobs {udid, transport, storage_id?, retry_of?}  → 202 Job
```

Three sub-decisions, each with the rung's recommendation:

- **`storage_id` omitted** → the storage marked `default`, of which there is exactly one.
  Recommended over a 422 because it is what keeps every existing single-storage client working
  unchanged, which is what makes the whole change additive.
- **The chosen storage is unreachable** → **409**, not 422. It is a state conflict the user can
  act on (plug the disk in) — the same reading `POST /api/devices/{udid}/pair` already uses for
  *not present on USB*. A 202-then-queue is explicitly refused: queuing fights the assisted model
  (D13), and the multi-storage epic's own point 5 says an offline target is an honest "can't right
  now", never a background retry.
- **Unknown `storage_id`** → 404, matching unknown-device.

`Job` gains `storage_id` (non-null, the **resolved concrete** storage — never the word "default",
exactly as `transport` stores the resolved `usb`/`wifi` and never `auto`).

Open inside this proposal: whether the *"this will be a full transfer"* claim (see `Storage` in
§2) makes `GET /api/storages` device-scoped via `?udid=`, or is carried elsewhere. Both work; they
differ in whether a storage list is a device-independent resource.

Spec: `docs/specs/qn.6c/qn.6c.md`, gap 2. **RULED 2026-07-31 — as recommended, with the `?udid=`
sub-question settled. NOT YET BUILT: this block is flipped to its ruled form by the slice that
implements it (slice 5).** This sentence read *"Not built until ruled"* until 2026-08-01, which
stopped being true the moment the ruling landed and would have told a session to stop on a question
that is decided — the same inverted-marker defect quince#408 gates for. The distinction that matters
to a reader: **ruled-and-unbuilt** is work to do, where **unruled** is a thread to stop.

**Two surfaces this proposal does NOT cover** and which story 5 needs, both proposed in the spec and
neither built: a **re-probe endpoint** — the 2026-08-01 ruling makes reachability changeable without
a restart, *plug the disk in and press the button*, and there is no button in this contract — and
the **shape of `unreachable_reason`**, now that `missing_medium` and `unreachable` must be
distinguishable by it.

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
DELETE /api/versions/{id}              → 202 | 404 | 503   // confirmed destructive action
     // 202: artifact (snapshot or dir) + registry row removed, audited (event, no secret),
     // version.deleted emitted. 404: unknown id. 503: no storage subsystem wired (--demo
     // deletes fixtures). Error codes recorded qn.5 (implemented the frozen shape).
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
  "wifi_sync": "on" | "off" | "unknown",           // lockdown com.apple.mobile.wireless_lockdown (qn.7)
     // Added at qn.7, ruled at that rung's spec review (quince#332) as a non-breaking field
     // addition. Domain and key are both MEASURED on hardware, 2026-07-31: the key is
     // `EnableWifiConnections`, a boolean, read `true` on a device whose Wi-Fi sync was on.
     // It was NOT taken on trust — the name appears nowhere in libimobiledevice 1.4.0, so the
     // roadmap's guess (which turned out correct) could not be known to be until a device said so,
     // and the server shipped answering `unknown` WITHOUT querying until it did. Still owed: an
     // off/on differential, which is what would prove this key is the one that CHANGES rather than
     // `SupportsWifiSyncing`, also true in the same dump. `unknown` continues to mean quince does
     // not know — a failed read, or an unconfirmed pairing, never a guess.
  "last_seen": "...",
  "last_backup": {"at": "...", "job_id": "..." | null, "status": "succeeded"} | null
     // job_id NULLABLE — ratified at the qn.4c spec review ((bz)). last_backup is derived
     // from the newest COMMITTED VERSION, not from job history: versions are the source of
     // truth for "has this device been backed up", so the field survives restarts AND covers
     // ADOPTED versions (a restored/replicated dataset, or quince reinstalled over existing
     // backups) — which have no job at all, hence null. A non-null job_id links to the run
     // that produced it. Semantics follow: this is the last SUCCESSFUL backup (a committed
     // version implies success); a failed last *attempt* lives in the intent-grouped job
     // history, never here. Fabricating a job id for an adopted version would be exactly the
     // state-honesty violation this project forbids.
}

Job: {
  "id": "01J...", "udid": "...", "kind": "backup",
  "transport": "usb" | "wifi",
  "state": "queued" | "waiting_for_device" | "preflight" | "seeding" | "backing_up" | "verifying"
         | "committing" | "succeeded" | "failed" | "cancelled" | "connection_lost",
  // qn.6a (cu opt 1 / cv): `seeding` is emitted between `preflight` and `backing_up` while storage
  // Seed reflink/hardlink-clones latest/ → working/<udid> (O(files); ~23 s on a 34 GB iPhone,
  // near-instant on a resume). The UI narrates "Preparing — cloning from your last backup…" instead
  // of dead air before the on-device passcode prompt. progress.phase mirrors it; it is a running
  // (non-terminal) state.
  "progress": {"phase": "receiving",                          // incl. "waiting_for_passcode"
               "percent": 63.0,                               // percent nullable
               "bytes_done": 2400000000, "bytes_total": 3600000000,
               "files_received": 149,
               "liveness": "active"},   // "" | active | silent_but_connected | suspected_stall
  // A TERMINAL JOB CARRIES NO LIVE PROCESS CLAIM (qn.4a, corrected by quince#313). Once `state` is
  // terminal — succeeded | failed | cancelled | connection_lost:
  //   • `liveness` is `""` for EVERY terminal state, succeeded included. The other three values are
  //     claims about a process that is running; a finished job has none. `""` is a WIDENING of what
  //     was a closed enum, and it is why this line changed.
  //   • `phase` is `""` on failure/cancel/connection_lost and `"done"` on success. The asymmetry is
  //     deliberate: `done` is a true statement about a succeeded job, where `active` is not.
  //   • `percent` is NOT cleared. On a failure it is the last true measurement of how far the job
  //     got — information about the past rather than a claim about now.
  // A client rendering `waiting_for_passcode` on a failed job tells the user to act on something
  // that is over, which is the `State honesty` rule failing at its own example.
  //
  // Clients should still gate live narration on `state` rather than on `phase`. This paragraph says
  // what the server sends; a consumer that asks "is this job running" before quoting a running
  // field is correct whatever a producer does, and both consumers that got this wrong got it wrong
  // by reading `phase` without reading `state`.
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
  // HOW THIS VERSION WAS MADE — not what its storage uses now. Those are different facts
  // (qn.6c gap 1, Operator ruling 2026-08-01) and `Storage.backend` carries the second.
  // They agree permanently, because a storage's backend is immutable once chosen (design §5).
  // Kept on MODELLING grounds rather than compatibility: a version can OUTLIVE its storage —
  // once remove-a-storage exists, detach-and-forget leaves `storage_id` dangling and the backend
  // unrecoverable by join, so this is not derivable in all futures. It is a fact about the
  // version, not a cached copy of the storage's.
  // zfs: a version IS a snapshot; browse_root goes through .zfs (read-only by nature).
  // namespace backends (reflink/hardlink/copy): a version is an immutable dir.
  "zfs_snapshot": "rpool/.../<udid>@quince-2026-07-18T02-30-01J..." | null,   // zfs backend only
  // qn.5b: snapshot name is quince-<YYYY-MM-DDTHH-MM>-<ULID> (date-first for readable `zfs list`
  // ordering; the ULID == version id is the collision-free tail, decisions (co)).
  "browse_root": "/backups/<udid>/.zfs/snapshot/quince-2026-07-18T02-30-01J.../latest"  // zfs
              |  "/backups/<udid>/latest"                                // namespace backends, newest
              |  "/backups/<udid>/versions/2026-07-18T02-30-11Z",        // namespace, rotated-out
  // qn.5b: on zfs, browse_root goes through .zfs/snapshot/<snap>/LATEST (was /working) — the
  // commit atomically exchanges the verified tree into latest/ before snapshotting, so the
  // snapshot IS latest/ = the version. browse_root is computed per request on namespace backends:
  // a version moves from latest/ to versions/<ts>/ when the next commit rotates it.
  "created_at": "...", "job_id": "..." | null,
  // job_id null = adopted: a quince-format version found on disk/in snapshots without
  // a DB record (e.g. dataset replicated/restored to a fresh host; reconciliation
  // re-registers from quince-version.json). Adopted, listed, protected from retention
  // until the user says otherwise.
  "kind": "full" | "incremental" | "unknown",
  // qn.5b (finding #9(a), decisions (cj)/(ck)): kind is derived AUTHORITATIVELY from whether the
  // per-job working/ was seeded from an existing latest/ (incremental) or started empty (a first/
  // full backup) — NOT from Status.plist.IsFullBackup, which the lab proved lies (a first 33 GB
  // backup writes IsFullBackup:false). Every quince version is a COMPLETE, independently-restorable
  // backup regardless of kind; kind stays internal (it gates the encrypted verify's blob-shard
  // check — asserted only on a genuine full) and is dropped from the version CARD in the UI (qn.6a),
  // because "incremental" imports a false fragile-chain mental model.
  "encrypted": true,        // unencrypted versions are permanently badged incomplete
  "is_latest": true,
  // PER (DEVICE, STORAGE) — Operator ruling 2026-08-01 (quince#378). "the newest committed
  // version of this device ON ITS STORAGE". A device backed up to two storages has TWO
  // versions with is_latest: true, one each; a consumer that assumed at most one per device is
  // wrong. No field changed shape — what changed is what the field MEANS, which is why it
  // needed a ruling rather than a patch.
  //
  // Scoped because browse_root READS it: a single global latest would leave every storage but
  // the winner with its newest version flagged false, resolving browse_root to a versions/<ts>/
  // directory that does not exist — the artifact is still in latest/. A replug would silently
  // change which version the UI calls latest on the internal pool.
  //
  // Unattributed rows (storage_id null) form their own group and get their own latest. Excluding
  // them was considered and REJECTED: a device whose rows are all null would then have no latest
  // at all, which is the same unresolvable browse_root reached from the other side.
  "structure_verified_at": "..." | null,   // set at commit (structural verification)
  "content_verified_at": "..." | null,     // set by verify_canary on a later unlock
  "logical_bytes": 42400000000, "physical_bytes": 3400000000,  // best-effort
  "missing": false
  // qn.6a (cr(a) / cv): true = the registry row survives but its on-disk artifact is GONE
  // (reconciliation could not find the snapshot/dir — roll-forward keeps the row, never drops it).
  // store.VersionRow.Missing already exists and is honoured by LastBackup / recomputeLatest /
  // Delete / VerifyVersion; qn.6a crosses it to the wire so the UI renders such a version explicitly
  // DEAD — no size claim, no Unlock, an "artifact gone — remove?" action on DELETE /api/versions/{id}
  // — instead of asserting a backup that does not exist. The row is NEVER omitted: omission would
  // silently shrink history, masking exactly the drift a soak exists to surface.
}

Op: { "id": "...", "udid": "...", "kind": "pair" | "encryption" | "wifi_sync",
      "state": "running" | "waiting_for_user" | "succeeded" | "failed",
      "message": "Tap Trust on the phone…",   // plain-language narration for the UI
      "error": {"code", "message"} | null }
     // `wifi_sync` added at qn.7 and ruled at that rung's spec review (quince#332). An ENUM
     // EXTENSION, which this document's header does not classify — it covers field additions
     // and breaking changes only. Ruled additive here on the same reasoning as qn.6a's
     // `seeding`: the only consumer is the in-repo UI, so a new member breaks nothing but an
     // exhaustive switch with no default. Twice now by precedent rather than by rule; the
     // header is owed a clause saying so.
     // `wifi_sync` emits no `waiting_for_user`: whether iOS demands an on-device confirmation
     // for a lockdown write is UNMEASURED, and narrating a passcode prompt the op may never
     // fire would teach a flow that does not exist. It gains one if hardware shows one.

Session: { "id": "...", "version_id": "...", "expires_at": "..." }

FileEntry: { "file_id": "ab12...", "domain": "CameraRollDomain",
             "relative_path": "Media/DCIM/100APPLE/IMG_0001.HEIC",
             "kind": "file" | "dir" | "symlink", "size": 123, "mtime": "..." }
```

**RULED (was `PROPOSED (gap)`): a `Storage` object, and how a job picks one — `qn.6c`, quince#378.**
**Nothing in this block is unruled.** The `Storage` object and `GET /api/storages` are ruled AND
BUILT (story 5c); `POST /api/jobs {storage_id}` is ruled and **not yet built**, landing with story 6
— which is *work to do*, not *a thread to stop*.

The heading said `PROPOSED (gap)` until 2026-08-01 while its own body already read *"now ruled AND
built"* — the sixth instance of this defect in one day, and the third caught by a reviewer reading a
block rather than its heading. The mechanism is worth naming: **a diff that edits the body does not
force anyone to look at the heading**, and the heading is the part describing the whole. That is
quince#408's argument, made by a PR that had been asked in terms to clear exactly this marker.

The multi-storage epic names `Version.backend` as *the symptom* of a modeling error: a version's
backend is really its **storage's** backend. `qn.6c` fixes the model; this proposal is about how
much of that reaches the wire.

**Two halves have left this proposal and are RULED below: `Version.backend` and
`Version.storage_id`.** The **`Storage` object** and **`GET /api/storages`** are now ruled AND built
as well (story 5c), together with `POST /api/storages/{id}/recheck`. What remains **ruled but
unbuilt** is **the job's** `storage_id` on `POST /api/jobs`, which lands with story 6.

This sentence listed the `Storage` object and `GET /api/storages` as *open* until 2026-08-01 — after
the ruling that decided them — and was found while preparing a ruling on a question it made look
unresolved. **Ruled-and-unbuilt is work to do; unruled is a thread to stop**, and prose that
conflates the two costs a round trip to the seat that has already answered.

**`PROPOSED (gap)` is a load-bearing marker meaning *nothing may be built on this yet*, not a
title**, so a heading naming a half that has been decided tells a reader searching for open
questions the opposite of the truth. **This heading has now been narrowed twice, and the second
time it was wrong for a day** — quince#403 narrowed it off `Version.backend`; quince#405 then ruled
and shipped `Version.storage_id` on the wire, added the `RULED NULLABLE` note below, and **left
this heading and the sentence above still listing it as open.** The architect reviewed that PR,
having blocked the previous one for this exact defect, and merged it without re-reading the
heading (quince#407).

**The lesson is mechanical, not moral: the PR that flips a half must narrow the heading in the
same diff**, because nothing else will notice. A gap block shrinks one half at a time, and the
heading is the only part that describes the whole.

```jsonc
Storage: {
  "id": "01J...",              // the UUID from quince-storage.json (design §5) — stable across
                               // replug, which a PATH is not. Never the config `name`, which the
                               // user may change.
  "name": "pool",              // from config.yml; the label the UI shows
  "path": "/backups",
  "backend": "zfs" | "reflink" | "hardlink" | "copy" | "unknown",
                               // "unknown" = never yet reached, so quince does not know. Not a guess.
  "default": true,             // exactly one storage is default
  "reachable": true,
  "unreachable_reason": null,  // set when reachable is false; SHOWN, never thrown — an unreachable
                               // storage must not block backups to any other (epic point 5).
                               // THREE distinguishable causes, because the remedy differs:
                               //   path_unreachable  the path itself cannot be read
                               //   missing_medium    the path reads, the marker is GONE, and the DB
                               //                     knows this storage — an unplugged disk's bare
                               //                     mountpoint. Refuses; never re-creates. Added at
                               //                     spec review (quince#381): without it an unmounted
                               //                     mountpoint is created as a NEW storage and
                               //                     backups land on the system disk.
                               //   backend_mismatch  the marker and the probe disagree (remount)
  "will_be_full": true         // this device's next backup here is a FULL transfer, because
                               // incremental is scoped to (device, storage) and there is no prior
                               // version on this one. Present ONLY when `?udid=` is passed —
                               // the list is device-independent by ruling (2026-07-31).
}

Version: { ..., "storage_id": "01J..." | null }
  // RULED NULLABLE — Operator, 2026-08-01 (quince#378). null = NOT YET ATTRIBUTED.
  //
  // TRANSITIONAL, and that is the difference from `job_id`, whose null (= adopted) is permanent
  // and CORRECT. This one should disappear: a version committed before qn.6c has no storage id
  // because the value is a UUID from its storage's quince-storage.json, written at the storage's
  // creation moment. Migration 0006 deliberately does not fabricate one — backfilling an invented
  // identity onto data that cannot be regenerated is the class the data-at-rest limit governs.
  //
  // A client must NOT read null as "no storage" or substitute a default. It means the server has
  // not worked out which storage this is yet, and it stops meaning that once the storage has a
  // marker. A gate asserts none remains null past that point, because a nullable-with-meaning
  // field whose meaning is "temporary" decays into a permanent unknown unless something says so.
```

**RULED (was `PROPOSED (gap)`): `Version.backend` is KEPT, and it means something DIFFERENT from
`Storage.backend` — `qn.6c`, Operator ruling 2026-08-01, relayed on quince#378.**

> **`Version.backend` is *how this version was made*. `Storage.backend` is *what this storage uses
> now*.**

Two distinct facts that agree permanently, because a storage's backend is immutable by design §5's
own rule — chosen at the creation moment, recorded in `quince-storage.json`, never re-selected.

**This is NOT the compatibility reflex, and it was checked rather than assumed.** The same review
retired an implicit env-var fallback precisely because *"keep it for compatibility"* is how
permanent cruft arrives, so *"keep `Version.backend`"* deserved the same scrutiny. It survives on a
different footing: **a version can outlive its storage.** Remove-a-storage is out of `qn.6c` but
coming, and *detach-and-forget* is one of its candidate semantics — once the storage row is gone,
`storage_id` dangles and the backend is **not recoverable by join**. The field is therefore not
derivable in all futures, which makes it a genuine fact about the version rather than a cached copy
of somebody else's. **The test is whether the field earns its place, not whether removing it would
break someone** — the header's worked example, and this is the case it was written from.

**The proposal offered (a) "keep it, denormalized" and (b) "remove it, breaking", and the ruling is
neither.** (a) framed the field as a convenience copy carrying an implied future breaking removal;
the redefinition makes it a **distinct, permanently true field that never needs removing**. So the
epic's *"`Version.backend` is the symptom"* framing turns out not to apply to the wire at all — only
to the DB, where `versions.storage_id` fixes it.

`Version.browse_root` also stops being universally `/backups/<udid>/…` once roots are plural. That
is a **documentation** change and not a shape change — it is already computed per request from the
root, so only the literals above go stale.

**The OTHER half of this gap — `Version.storage_id` and the `Storage` object — is NOT flipped
here.** It lands with the `0006_storage` migration that creates the column, in its own PR, and is
**ruled nullable** (`null` = *not yet attributed*). Split on the architect's direction so the
redefinition does not wait on the migration: a reviewer of either PR should not go looking for the
other half in the same diff.

Spec: `docs/specs/qn.6c/qn.6c.md`, gap 1.

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
QUINCE_DATA=/data   QUINCE_CACHE=/cache
QUINCE_LISTEN=:8968
```

**`QUINCE_BACKUPS` was RETIRED at `qn.6c`** (gap 3, Operator ruling 2026-07-31 — quince#378).
Backup locations are **declared**, in `storage:` below: no env var, no implicit storage,
no fallback. **Setting it now produces the ordinary unknown-`QUINCE_`-variable warning** — it is
gone, not merely unread, and that is asserted rather than assumed.

The retired variable carried a built-in `/backups` default, so every deployment had a working
storage while declaring nothing. That implicit path is what the ruling removed: *"I see no reason
to accumulate backward-compatibility garbage."* Both agent seats recommended keeping a fallback
and both were wrong for the same reason — the argument priced a population of deployments that
does not exist.

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
storage:                    # REQUIRED, qn.6c. `storage:` IS THE LIST (quince#473) — no wrapper
                            # key, no globals, no inheritance. It is the ONLY key with no
                            # default: quince REFUSES TO START without at least one, naming the
                            # key and printing what to write. There is no sane default for where
                            # a user's backups live now that QUINCE_BACKUPS is retired, and the
                            # honest form of "no default" is a refusal, never a guess (D12
                            # near-miss, declared).
                            #
                            # A SINGLE STORAGE IS JUST A PATH — everything else has a default:
                            #
                            #     storage:
                            #       - path: /backups
                            #
  - name: local             # OPTIONAL — defaults to `path` (ruled 2026-08-01, quince#504). On a
                            # single-storage install `name: backups, path: /backups` says the
                            # same thing twice. It is the stable identity across replug, where a
                            # path is not, and it keys the DB row.
    path: /backups          # REQUIRED; absolute; unique across entries
    default: true           # OPTIONAL with exactly ONE storage, where it is implied. With
                            # several, exactly one must carry it — declaring none of several is
                            # an ERROR, not a pick: order is not intent.
    backend: auto           # auto | zfs | reflink | hardlink | copy. Defaults to `auto`.
                            # auto: zfs when this entry's zfs block is configured, else probe
                            # reflink → hardlink → copy on THIS path.
                            #
                            # `auto` IS STILL LEGAL AND IS NOT AN OVERSIGHT. The 2026-08-02
                            # direction that only a CONCRETE backend may land in this file is
                            # DEFERRED, not withdrawn — homed on quince#502 (`qn.6e`). Do not
                            # tidy it out: `auto` is the ONLY thing that checks a declaration
                            # against the medium (storage.Select returns an explicit backend
                            # WITHOUT probing), so removing it early would make
                            # quince-storage.json record a guess and fail at seed time.
    zfs:                    # THIS storage's zfs settings. No global to inherit from and none to
                            # opt out of, so the `zfs: {}` idiom is gone with quince#458 — a
                            # second storage can no longer be handed a parent dataset that was
                            # written for the first.
      parent_dataset: ""    # e.g. rpool/userdata/iphone-backup; one child dataset per device.
                            # Two storages sharing one parent dataset are REFUSED: they would
                            # create the same <parent>/<udid> per device and each believe they
                            # owned it. That refusal survived the flattening because it was
                            # never about inheritance (quince#473).
      mode: exec            # exec (delegated) | hook
      hook_cmd: ""          # e.g. ssh -i /data/keys/zfs pve quince-zfs-helper
                            # (forced-command: snapshot/destroy/list @quince-*, create children,
                            #  seed working/<udid> from latest/; dataset destroy impossible via the key)
      seed: auto            # qn.5b: in-container strategy to clone latest/ → working/<udid> at job
                            #   start (renamed from `mirror` when the reflink moved commit→seed).
                            #   auto (reflink → copy) | reflink | copy — hardlink is NEVER used for
                            #   the seed (it would alias the committed latest/; gate 12c). In hook
                            #   mode the host-side `seed` verb does the reflink and this is moot.
    retention:              # THIS storage's keep policy. ABSENT falls back to the code defaults
                            # below, which D12 permits — a setting with a sane default the file
                            # need not spell out. Prune groups a device's versions by storage and
                            # applies each one's policy, so `keep_recent: 10` means ten ON THIS
                            # DISK; a single policy across storages would have let a second disk
                            # silently change what the first one keeps.
      keep_recent: 10
      keep_daily: 30
      keep_weekly: 12
devices:
  manage_muxer: true        # true = SIMPLE profile: quince owns the lifecycle of EVERY muxer
                            # daemon it is configured to reach — usbmuxd (USB) and netmuxd
                            # (Wi-Fi) — as supervised subprocesses with restart-w/-backoff,
                            # each refusing loudly at startup if its address is already served
                            # (no silent adoption). false = HARDENED/external: quince only
                            # dials both and reports them `external` in /api/health.
                            # ONE flag for both daemons (D12; qn.4c ruling (bz)).
  usbmuxd_socket: /var/run/usbmuxd    # authoritative: the managed usbmuxd gets -S <this>
  netmuxd_addr: 127.0.0.1:27015       # authoritative: the managed netmuxd gets --host/--port
                            # from this (plus a private --socket-path and --disable-usb).
                            # Wi-Fi discovery is mDNS-only, so the container must be on the
                            # LAN — see deploy/compose.nas.yml.
tls:                        # qn.6f — the certificate quince serves ITSELF, for the tier with no
                            # reverse proxy in front of it. BOTH EMPTY (the default) MEANS TLS IS
                            # OFF, and that is a correct configuration, not a degraded one: it is
                            # what the reverse-proxy and `tailscale serve` tiers want, and what
                            # `--demo` runs. Setting exactly ONE of the two is a 422 naming the
                            # other — it can only be a mistake, and unreported it reads as "off".
  cert_file: ""             # PEM certificate chain
  key_file: ""              # PEM private key — a PATH. A key body never enters this file (D12);
                            # config.yml is hand-editable and carries no secrets, ever.
                            #
                            # WHETHER THESE FILES EXIST, PARSE, OR MATCH EACH OTHER IS NOT A
                            # VALIDATION ERROR. An invalid config is DISCARDED in favour of
                            # last-good/defaults, and the defaults have no TLS — so a certificate
                            # fault raised as a validation error would start quince on plain HTTP
                            # for somebody who asked for HTTPS, behind a warning banner they
                            # cannot see because they are not connected. It is a FATAL check on
                            # the serve path instead: quince refuses to start, names the file and
                            # the reason, in the shape the storage requirement already uses.
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

**RULED (was `PROPOSED (gap)`): storages are DECLARED; `QUINCE_BACKUPS` is retired — `qn.6c`,
Operator ruling 2026-07-31, relayed on quince#378.** Implemented above.

**Option (b) was chosen over the recommended (a).** (a) kept the env var as an implicit fallback
synthesizing one storage when the list was empty; (b) retires it outright. **Both agent seats
recommended (a) and both were wrong for the same reason** — the argument was *"this breaks every
deployment in the field"*, and there is no field. There is one instance; the cost is editing one
YAML file once. Against that, an env var with a built-in default that quietly conjures a storage
is a permanent implicit path: cheap now, expensive later, load-bearing by the time anyone wants it
gone.

**What the ruling requires, and each is implemented rather than described.** No `storage:` key →
**refuse to start**, name the key, print the remedy (`config.CheckStorages` + `StorageRequirement.Explain`,
called on the serve path). The variable is **gone rather than unread** — it is no longer in
`knownBootstrapVars`, so setting it produces the unknown-variable warning. A still-set
`QUINCE_BACKUPS` is echoed in the refusal as the likely reason a working deployment stopped, and
its value is suggested as the path to declare.

**Why the refusal is NOT a `Validate` error, which is the load-bearing design point.** `Load()`
discards a config that fails `Validate` and returns `Default()` with `OK:false`; `NewService`
logs *"running on last-good defaults"* and continues — never fatal, by its own contract. Routing
"no storages" through that path would start a daemon that serves a healthy-looking UI and can back
nothing up. **That silent zero-storage start is the one outcome the ruling forbids**, so the check
lives where it can stop the process. `Validate` still owns well-formedness of a list the user *did*
declare (empty name/path, relative path, duplicate name or path, not exactly one `default: true`) —
those are 422s, not exits.

**`PUT /api/config` is a THIRD path and answers 422, which is not the same check twice.** The
requirement is enforced in `Service.Replace` as well as at startup, because `Replace` has the
opposite property from `Load`: it returns the errors and **writes nothing**, so the hazard that
justifies keeping this out of `Validate` does not exist there. Without it the UI could remove the
user's last storage, receive a **200**, and the running daemon would keep serving on its
already-loaded config — the user discovering at the next restart that backups were disabled. D12
makes the UI the editing surface, so that is two clicks away; a silent acceptance of an edit that
disables backups is exactly what *no silent caps or fallbacks* forbids. Found at review
(quince#394): the exclusion had been reasoned from `Load`'s behaviour and applied to a path that
does not share it.

**`--demo` is unaffected and that is deliberate:** it serves fixture data and never builds the
storage subsystem, so the refusal sits inside the live branch. A check placed before it would
refuse every demo and every `ui-e2e` run over a subsystem they do not use.

**Second half — OVERRULED 2026-08-02 (quince#458).** It read *"ruled as recommended: `backend`, `zfs`
and `retention` stay global; a declared entry inherits them"*, and `backend` and `zfs` are now
**per-entry overrides with the global as the inherited default**. `retention` stays global.

**The recommendation's own reasoning is what fails.** It argued that per-storage zfs settings *"only
start mattering when a second zfs storage exists, which this rung cannot create"* — true, and it
addresses the wrong configuration. The breaking one is **one zfs storage beside a non-zfs one**,
which this rung creates trivially, and which is the first thing an operator tries: a USB disk
alongside a zfs default. It got a zfs backend whose parent dataset pointed at another pool.

**Interface fact 4 had already required otherwise** — *"a per-storage backend therefore needs
per-storage zfs settings **or an explicit rule that only one storage may be zfs**"* — and neither was
built. The word *ruled* in this sentence was also wrong: the spec's `RULED` block on gap 3 settles
`(b)`, the `QUINCE_BACKUPS` retirement, and is silent on this half. **A recommendation was
implemented as a ruling, and this line is how it came to look decided.**

**One hard refusal follows**, because it is not a degraded mode to surface: two storages sharing a
zfs `parent_dataset` would create the same `<parent>/<udid>` per device and each believe it owned it,
which voids every per-storage guarantee this rung adds. quince refuses to serve and names both
storages and the remedy.

A **restart** is still required to pick up a `storage:` change — D12 permits that only if the spec
says why, and `docs/specs/qn.6c/qn.6c.md` says why.
**RULED (was `PROPOSED (gap)`): `storage:` becomes a list of fully-specified storages; `auto`
REMAINS LEGAL and its removal is descoped to `qn.6e` — `qn.6c`, quince#473, quince#502.** Operator
ruling 2026-08-02, relayed by architect session `arch1` on quince#500 — a **relay of an out-of-band
decision**, which is the citable record rather than a forge artefact the Operator authored.

**What lands, in full and unchanged:** `storage:` **is** the list, with no global `backend`, `zfs`
or `retention` — the five inline directions on quince#461.

```yaml
storage:
  - path: /backups          # name and default optional, per the 2026-08-01 ruling
    backend: zfs            # concrete OR `auto`; see below
    zfs: {parent_dataset: rpool/quince, mode: hook, hook_cmd: "…", seed: auto}
    retention: {keep_recent: 10, keep_daily: 30, keep_weekly: 12}
  - {path: /mnt/shuttle, backend: hardlink}
```
It dissolves quince#458 **by construction** — no inheritance means nothing bleeds from a global onto
a storage it was never written for — and it **deletes** rather than amends: `BackendFor`, `ZFSFor`,
the `zfs: {}` opt-out idiom with its comments and tests, `CheckStorageBackends`' remedy BRANCHING,
and **quince#468 entirely**.

**`CheckStorageBackends` itself SURVIVES, and quince#473's deletion list is wrong about it.** Read
before building, not assumed from the issue. The function does two things and only one of them is
about inheritance:

- **The zfs-with-no-parent refusal** is an incoherent *declaration*, still reachable when an entry
  writes `backend: zfs` with no `parent_dataset`. It survives, and its remedy collapses from three
  branches to **one** — *set `parent_dataset` in this entry's `zfs:` block* — because with no global
  there is no other key it could mean. That is what deletes quince#468, whose whole content is
  choosing between remedies that no longer exist.
- **The duplicate-`parent_dataset` collision** — two storages that would create the same
  `<parent>/<udid>` per device and each believe they owned it — **is not caused by inheritance at
  all.** Two flattened entries can each spell out the same `parent_dataset`. Deleting the function
  would reintroduce quince#458's actual hazard by a different route, which is the opposite of what
  the flattening is for.

**What is DEFERRED, and this is the half that changed:** *"only a specific backend can land in
settings.yaml"* is deferred, **not withdrawn**. It is homed on **quince#502 (`qn.6e`)**, an explicit
placeholder. **`backend: auto` is still legal in `config.yml` and is not an oversight** — a reader
meeting it after a direction that said *concrete backends only* should not tidy it out. **Nothing is
built to compensate**; in particular the creation-time probe-and-refuse this block recommended is
**not** wanted.

**The measurement below is what decided it, and it is kept verbatim for that reason.**
`core/internal/storage/probe.go:42-48`:

```go
switch opts.Backend {
case BackendReflink, BackendHardlink, BackendCopy:
	// returned WITHOUT probing — "storage.backend: <x> (explicit)"
}
// auto: probe the real filesystem
name, reason := probeNamespace(opts.Backups)
```

`probeNamespace` — FICLONE independence, then `link()`+inode identity — runs on the **`auto` branch
only**. An explicit namespace backend is taken at face value. **So `auto` is not a convenience
default; it is the only thing in the product that checks a backend declaration against the medium**,
at a time when nothing creates a storage for you — that flow is quince#443, a later rung.

Without it, a wrong guess is accepted silently at startup, frozen into `quince-storage.json` where
gap 4 makes the marker the authority, and fails at **seed time, inside a backup the user just
pressed** — `ErrReflinkUnsupported` is a surfaced error and explicitly *"never a silent fallback"*
(`clonetree.go:49-52`). It also feeds quince#476, where a `backend_mismatch` clears only by
hand-deleting a checksummed file and the refusal never says so.

**Why deferral rather than a new mechanism, which is the reusable part.** Removing `auto` was chosen
as the *simpler* option — *"pick the simplest, don't accumulate debt for no reason"*. But keeping
`backend_mismatch` meaningful without it required **building** a creation-time probe-and-refuse:
more machinery, not less, and a cost the simplicity argument had not priced. Descoping is what makes
that argument actually hold. **The marker safety property then returns for free**: with `auto`, the
creation moment probes, so the marker records what the medium *is* and `Mismatch` compares a real
observation against a real observation.

**Two things quince#473 listed as undecided, measured rather than argued, and neither blocks.**

*The absent-vs-empty pointer distinction survives.* It is what G7 rests on — *no key* and *declared
none* want the same refusal for different reasons. Moving `*[]StorageEntry` up one level, onto
`storage:` itself, preserves it exactly, because `Parse` unmarshals over `Default()` and a nil
pointer stays nil:

```
PROBE absent key     → nil       PROBE declared none → empty       PROBE one storage → 1 entry
```

So `CheckStorages` keeps its shape and `Explain` changes one string: `storage.storages:` →
`storage:`.

*Unknown-key detection still reaches inside an entry.* `unknownKeys` recurses into slices of structs
and indexes them, so a typo in a flattened entry is still reported and still says which entry:

```
PROBE warning → unknown config key "storage[0].pth" (ignored)
```

**Two consequences to state in the implementing PR rather than discover.** Per-storage `retention`
with no global block means an absent `retention:` falls back to **code** defaults — D12 permits a
setting with a sane default the file need not spell out. And **the upgrade note gap 3 made a
deliverable now has two steps**, because this is the second config break in one rung.

**Whether this is `qn.6c` or its own rung was open in quince#473 and is answered by the Operator
naming it the most important piece left of `qn.6c` (2026-08-02).** Recorded because it was listed as
undecided and a later reader will look for where it went.

**Not ruled here:** `qn.6e`'s scope, which quince#502 leaves open by instruction; and whether `auto`
removal ultimately sits in `qn.6e` or travels with quince#443's add-storage flow, which would be a
scoping decision rather than a reversal.

**PROPOSED (gap): one listener or two, and what plain HTTP does once quince serves TLS?**
`qn.6f`, quince#462 — quince#446's open decision 3, spec `docs/specs/qn.6f/qn.6f.md`. Nothing is
built on this until it is decided.

**The question.** With `tls.cert_file` set, does quince **(a)** add a second port for HTTPS and keep
serving the app over http on `QUINCE_LISTEN`, **(b)** listen on two ports where the http one
redirects or refuses, or **(c)** serve both protocols on the single port `QUINCE_LISTEN` already
names, routed by inspecting the first byte of each connection?

**It is not an implementation detail**, which is why it is here rather than in the spec. It decides
the URL a user bookmarks, and serving the app on both a plain and a TLS origin is two origins with
different cookie behaviour — the same `secureCookie` split that makes this rung necessary, now
inside one deployment.

**The deployment constraint that shapes it.** `deploy/compose.nas.yml` documents that Wi-Fi is
quince's primary use case, that netmuxd finds devices only by mDNS, that multicast does not cross a
bridged container network, and therefore that the answer is `network_mode: host` with the `ports:`
block deleted. **On the deployment that matters there is no port forwarding at all** — so a second
listener is a second host bind, and a second collision surface on a box where nothing can be
remapped.

**The Operator's leaning, recorded and not decided:** *"environment variable, single port for both
http and https"* — option (c), with plain-HTTP connections getting a `301` to
`https://<same host>:<same port>`.

**What (c) buys beyond a saved port.** The onboarding URL never changes: open `http://host:PORT`,
complete step 1, obtain a certificate, and the same URL keeps working, upgraded in place. There is
no *"now go to a different port"* — the worst moment in any self-hosted first run — and no saved
bookmark that starts returning a TLS error, which browsers render as *"sent an invalid response"*
and a user cannot distinguish from the app being broken.

**Budget it at ~150 lines, not ~30.** The byte-sniff is small. The working feature is a
per-connection peek goroutine with a read deadline **cleared before hand-off** (or the HTTP server
inherits it), two synthetic channel-backed listeners implementing `Accept`/`Close`/`Addr`, a `Conn`
wrapper replaying the peeked byte on first `Read`, and shutdown coordination across two
`http.Server`s over one real listener without double-closing.

**The dependency question, measured live 2026-08-02 rather than recalled.**
`github.com/soheilhy/cmux` is the obvious import and it is **dormant**: latest published version
**v0.1.5, 2021-02-05** (module proxy), 2.8k stars, 26 open issues, 10 open PRs, not archived. Its
only activity since February 2021 is one commit on **2026-06-08**, *"Modernize for current Go
toolchains"* — **untagged, so no published version contains it**; taking it means pinning a
pseudo-version. Nothing in `golang.org/x` multiplexes connections. `github.com/inetaf/tcpproxy` is
genuinely maintained (2026-05-15) but is a *proxy* — it routes to backend addresses rather than
yielding `net.Listener`s, so it is not a drop-in. **Go 1.26 adds nothing here**: `bytes.Buffer.Peek`
is a buffer method, and the standard library still has no `net.Conn` peek, no protocol detection,
and no listener-wrapping helper.

**Recommended: (c), vendored rather than `cmux`.** One byte of discrimination — `0x16` is a TLS
ClientHello, every HTTP request begins with an ASCII method letter — is a `bufio.Reader`, a
`Peek(1)`, and a `Conn` whose `Read` drains the buffered reader first. cmux buys a general matcher
framework and its own close semantics, in exchange for a dependency whose newest release predates
this decision by five years.

**A trap that belongs with the decision, because it is silent and permanent: never send HSTS while
the certificate is self-signed.** HSTS instructs the browser to refuse untrusted connections to that
origin, which removes the click-through exception the self-signed tier depends on and locks the user
out with no in-browser recovery. quince sends none today — `httpapi.securityHeaders` is CSP,
`X-Frame-Options`, `X-Content-Type-Options` and `Referrer-Policy`, measured — and must not start
while any self-signed or plain-HTTP path is reachable.

### The default listen port

*This heading is load-bearing rather than decoration.* A `PROPOSED (gap)` block is bounded by the
next heading, the next live marker, or EOF (quince#408). While the listener gap above and the port
gap below were both open, each bounded the other. Flipping the port gap alone removed that
boundary, and the still-open listener block immediately read as though its own question had been
ruled — `gap-heading-check` caught it on the first run. A heading restores the bound **correctly**,
where the gate's documented opt-out comment would only have silenced the check, and would also have
hidden any genuine future violation inside the listener block.

**RULED and IMPLEMENTED (was `PROPOSED (gap)`): the default listen port is `:8968`.** Operator
ruling 2026-08-02, relayed by architect session `arch1` on quince#446 — *"Gap B: `8968`"* — and
built in the same rung. `:8080` was close to the worst available choice, and the change was free
only until v0.1.

**What moved with it, because a default nothing follows is not a default:** `QUINCE_LISTEN`'s
fallback in `config.Bootstrap`, `deploy/Dockerfile`'s `ENV` and `EXPOSE`, both compose files,
`deploy/dev.md`, the e2e harness and its Playwright `baseURL`, `make demo`, and the dev-deploy
convention URL in `deploy/devct/` and its spec. **`docs/specs/qn.0/qn.0.md` deliberately still
says `8080`**: it records what was proven at that rung, and a past acceptance is not rewritten to
match a later decision.

The criteria and the measurement that produced the number are kept below, because *"which ports
are free"* is a live fact and whoever revisits it needs the method rather than the answer.

**Why it had to be now.** The same argument the `QUINCE_BACKUPS` retirement above turned on: there is one
instance, so changing the default is one edit today. After v0.1 it is in every README, every
screenshot, every compose file anyone copies, and every user's bookmark.

**The constraint that makes it load-bearing rather than cosmetic.** Under bridged networking a
collision costs nothing — remap `8081:8080`. Under `network_mode: host`, which Wi-Fi requires,
**nothing can be remapped**: if anything on the box already holds the port, quince fails to bind and
does not start. `8080` is close to the worst available choice for that — Synology's own stack,
Tomcat, qBittorrent, UniFi and a long tail of homelab software live there.

**Measured live against the IANA registry, 2026-08-02** — `service-names-port-numbers.csv`,
`Last-Modified: Thu, 23 Jul 2026 20:36:24 GMT`, 15,398 rows:

- **`8080` is ASSIGNED**: `http-alt`, *"HTTP Alternate (see port 80)"*.
- **`8443` is ASSIGNED**: `pcsync-https`. So the natural pair for a two-listener design is **two
  assigned and heavily squatted ports**.
- 1,582 of the 2,000 ports in 8000–9999 are unassigned for TCP; the large contiguous runs are
  8475–8499, 8504–8553, 8616–8664, 8712–8731, 8810–8872 and 8955–8979.

**Criteria, not a number** — "which ports are free" is exactly the sort of fact canon says to read
live:

1. **IANA-unassigned**, in the registered range 1024–49151.
2. **Below 32768.** A session box's `/proc/sys/net/ipv4/ip_local_port_range` reads `32768 60999`, so
   a listener above that can lose a race with an outbound socket's source port. Under host
   networking that is a real failure mode. **This criterion was not in quince#446's list**, and it
   removes a whole range.
3. **Not on Chromium's restricted-port list** (`net/base/port_util.cc`). Nothing in 8000–9999 is
   blocked; the nearest entry is **10080**, where a web UI would simply be unreachable in Chrome.
4. **Avoid 9100–9999** — the Prometheus exporter allocation band, actively curated (wiki edited
   2026-07-31).
5. **Mid-block in a large unassigned run, clear of de-facto squatters.** IANA does not record
   squatters, so this is a separate cross-check: Synology 5000/5001, Plex **32400 (officially
   registered — the only one of these that bothered)**, Jellyfin 8096, Home Assistant 8123,
   Portainer 9000/9443, Syncthing 8384, Immich 2283, Proxmox 8006, Sonarr 8989, Radarr 7878, UniFi
   8443/8843/8880. **Several sit on IANA-unassigned ports, which is precisely why the registry alone
   is not sufficient.**

**Recommended: `8968`** — unassigned, mid-block in 8955–8979, nearest known neighbours 8983 (Solr,
15 away) and 8989 (Sonarr, 21). Runners-up **`8517`** (block 8504–8553, nearest 8501 Streamlit) and
**`8486`** (block 8475–8499; weakest of the three, because 8484 is two away).

**What changing it costs:** `deploy/Dockerfile`'s `ENV QUINCE_LISTEN`, both compose files,
`deploy/dev.md`, the e2e harness and the demo.

**Two honest notes.** No unassigned port is memorable, so the real mitigation is that the listen
address is already a first-class setting rather than a good number. And under host networking a bind
failure must be a **loud named error, never a fallback to another port** — *no silent caps or
fallbacks*.

