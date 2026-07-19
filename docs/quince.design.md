# quince — architecture canon

> The system's shape: components, data flow, the job state machine, storage semantics,
> and the security model. Stack rationale lives in [`quince.stack.md`](quince.stack.md);
> wire-level shapes in [`contracts.md`](contracts.md). This doc is the map an implementing
> agent reads before touching anything.

## 1. Topology

```
                       ┌────────────────────────── container ──────────────────────────┐
 iPhone ──USB──► usbmuxd ─┐                                                            │
 iPhone ──Wi-Fi► netmuxd ─┤ muxd sockets (plist protocol, Listen mode)                 │
                          ▼                                                            │
                    ┌──────────┐   spawns    ┌──────────────────┐                      │
                    │ quince  │────────────►│ idevicebackup2   │──writes──► /backups  │
                    │ (Go core)│             │ idevicepair etc. │            (mounted) │
                    │          │   spawns    ├──────────────────┤                      │
                    │  event   │────────────►│ quince-vault    │──reads───► /backups  │
                    │  bus     │  stdio RPC  │ (Python sidecar) │──writes──► /cache    │
                    └────┬─────┘             └──────────────────┘                      │
                         │ REST + WebSocket                                            │
                         ▼                                                             │
                    embedded React UI  ◄── browser / iPhone PWA                        │
                       └───────────────────────────────────────────────────────────────┘

 /backups  = the backup dataset (ZFS dataset bind-mount, NAS shared folder, …)
 /data     = app state: SQLite DB, config.yml, logs, pairing records copy — NEVER inside /backups
 /cache    = fingerprint-validated derived caches (thumbnails) + session scratch — disposable at any time, NEVER inside /backups
```

Everything is one process tree under the core: subprocesses are supervised, killed by
process group, and may not outlive their job. The core is the only writer of app state
and the only network listener.

## 2. Core components (Go)

| Component | Responsibility |
| --- | --- |
| `muxd client` | Maintains Listen connections to N configured muxer sockets — default: ONE, netmuxd v0.4+ serving both USB and Wi-Fi (stack D2); classic usbmuxd is a config-only fallback topology. Merges sources into one device table keyed by UDID with per-transport presence (`ConnectionType`); reconnects with backoff; emits `device.*` events. |
| `muxer supervisor` | With `devices.manage_muxer: true` (simple profile): owns the in-container usbmuxd lifecycle — Go subprocess in its own process group under the serve context, restart-on-crash with capped backoff, killed on shutdown; **refuses loudly at startup if something already serves the socket** (no silent adoption). Powers `POST /api/devices/rescan` (restart → re-enumerate → the muxd client's reset/replay reconcile). `false` = hardened profile: external muxer sockets only, rescan → 409. Ruled 2026-07-20 from qn.2's gap capture; MINIMAL scope in qn.2b, netmuxd co-supervision + health surfacing in qn.7. |
| `device ops` | Pair / validate / info via argv subprocess wrappers; caches `ideviceinfo` snapshots; never interpolates UDIDs into shell. |
| `job engine` | One goroutine per job driving the state machine (§4); global per-UDID mutex; persists every transition to SQLite *before* emitting the event (crash-safe: on startup, orphaned `backing_up` jobs become `connection_lost` and their work dirs are discarded). |
| `backup supervisor` | Spawns `idevicebackup2` in its own process group; parses stdout incrementally (tolerant line parser — unknown lines are logged, never fatal); tracks progress and liveness via the activity sampler (§4). |
| `storage backends` | `VersionBackend` implementations (§5) behind capability probe. |
| `vault manager` | Spawns/kills `quince-vault` processes; owns session lifecycle (unlock → TTL/lock → wipe scratch); brokers RPC. |
| `event bus` | In-process pub/sub; every state change is an event; WS handler fans out to subscribers with per-client send buffers (slow client → dropped connection, never a blocked publisher). |
| `http api` | REST + WS per `contracts.md`; auth middleware (session cookie); serves embedded UI. |
| `config` | Owns `/data/config.yml` (source of truth for all non-bootstrap settings, stack D12): schema+defaults, validation, atomic canonical writes with generated doc-comments, file watch → apply-or-keep-last-good with a UI banner; serves `GET/PUT /api/config`. |

## 3. Device model

A device is identified by UDID and may be present on multiple transports at once:

```
Device { udid, name, model, ios_version, transports: {usb: seen_at, wifi: seen_at},
         paired: yes|no|unknown, backup_encryption: on|off|unknown,
         last_seen, last_backup: {at, job_id, status} }
```

Device scope: anything speaking the standard pairing + MobileBackup2 stack — iPhone and
iPad are first-class (identical protocol; the lab proved iPhone, iPad needs no extra
code); Vision Pro is untested and unpromised (visionOS may be iCloud-only); Apple Watch
has no direct backup protocol and is out of scope. Nothing in the codebase may be
iPhone-string-specific — the `model` field drives any per-device presentation.

Rules: presence is muxd-event-driven, never polled. `paired` is refreshed lazily
(`idevicepair validate`) — on attach and before any job, not on a timer. A device
vanishing mid-job does not remove it from the table; it flips presence and lets the job
engine decide.

**Backup encryption is a managed device property.** `backup_encryption` reads lockdown's
`com.apple.mobile.backup / WillEncrypt` (refreshed with device info). Device ops expose
enable / change-password / disable via `idevicebackup2 encryption` / `changepw`
(contracts §1): the password reaches the subprocess by pty prompt or the `BACKUP_PASSWORD`
env fallback — **never argv** — and the phone's own passcode-confirmation step is
narrated in the UI. This is Apple's device-global backup password: the same one that
later unlocks versions in the vault; quince sets it and never stores it. Product
stance: encryption on is the default expectation (`backup.require_encryption: true`) —
unencrypted backups silently omit Health, Keychain/passwords, call history and more, so
an unencrypted device shows a persistent warning banner, and disabling encryption is
allowed but explicitly discouraged in the UI copy.

## 4. The backup job state machine

```
queued → waiting_for_device → preflight → backing_up → verifying → committing → succeeded
   └──────────────┴───────────────┴────────────┴────────────┴──── failed / cancelled / connection_lost
```

**The invariant above all: `latest/` is never written by `idevicebackup2`** — it writes
only into the mutable area (`working/` on zfs, `work/<job>` on namespace backends). The
sync-facing namespace is consistent at every instant: any snapshot, syncoid pass, or
filtered rclone walk captures a complete verified `latest/`, immutable versions, and at
worst a dirty mutable area that the offsite filter never reads (stack D5a).

- **preflight**: device present on chosen transport, `validate` passes, disk headroom
  checked, encryption state checked against policy (§3): `WillEncrypt=false` under
  `require_encryption: true` fails the job *actionably* — the error links straight to
  the encryption-management flow; with the policy relaxed, the job proceeds and its
  version is permanently marked `encrypted: false` — and
  backend `Seed()` done (namespace backends: populate `work/<job-id>/` from `latest` so
  MobileBackup2 runs a true incremental; zfs: no-op — `working/` already holds the
  previous state).
- **backing_up**: supervisor runs `idevicebackup2 [-n] backup` into the backend's
  working area. The `*** Waiting for passcode ***` output line is detected and surfaced
  as a `waiting_for_passcode` progress phase — **the liveness clock pauses there** (the
  user may take minutes; modern iOS requires on-device passcode entry for every backup).
  Liveness is judged by a cheap **activity sampler**, not byte growth alone (files can
  be replaced size-neutrally): tree size, recent mtime/ctime churn, `Manifest.db` +
  journal activity, file count, process I/O counters where available. Stall handling is
  staged — `active → silent_but_connected → suspected_stall → timed out` (15 min of
  zero activity, tuned in qn.7) — so patched long libimobiledevice timeouts can't be
  undercut by an impatient app-level kill; the lab proved silent multi-minute stretches
  are normal.
- **verifying** — *structural verification*, automatic and passwordless: exit code 0
  AND `Backup Successful` in output AND `Status.plist` parses with
  `SnapshotState == finished` AND `Manifest.plist`/`Info.plist` parse AND `Manifest.db`
  opens read-only with the required tables AND a deterministic sample of Manifest
  records resolves to existing blob files. *Content verification* (vault decrypts a
  canary file) cannot run unattended — no stored password — so it happens on the user's
  next unlock and is recorded per version as `content_verified_at`; the UI shows both
  levels honestly.
- **committing**: backend `Commit()` under the journaled phase model (§5). Failure here
  = job `failed` with the working state preserved for inspection — surfaced loudly,
  never silently.
- **any failure/cancel/loss**: kill process group → close files → backend `Discard()`
  (namespace backends: delete `work/<job>` — committed state untouched; zfs: leave the
  dirty `working/` as-is, report "working copy dirty, last good = <version>"). App
  state, logs, and job history live outside the dataset and survive regardless.
- **No auto-retry — assisted model (stack D13).** A retry would hang at the on-device
  passcode prompt, so a failed job terminates into an honest `user action required`
  state (push, once qn.12 lands; always visible in UI) and the user retries with one
  tap; the new job carries `retry_of` and inherits `intent_id` from the chain root, so
  history reads as one user-level operation ("Backup completed after 1 retry"), not a
  string of red failures (Intent model, contracts §2; a full server-side Intent entity
  is parked as future evolution). A dirty `working/` is a *candidate* for the next
  manual incremental — the lab showed MobileBackup2 continues from torn state — but
  that's a policy, not a guarantee: every result still passes full structural
  verification, and if incrementals from a dirty working copy repeatedly fail, the
  **escape hatch** is `quince device repair-working-copy <udid>` (zfs: rebuild
  `working/` from the last good snapshot; reserved semantically now, implemented in
  qn.5, never automatic in v0.1). Never two concurrent jobs per UDID. Transport policy
  `auto` prefers USB when plugged, Wi-Fi otherwise.

There is no post-backup indexing state: backup content is only ever read lazily inside
an unlocked viewer session (§7), so success is defined purely by verify + commit.

## 5. Storage backend semantics

Two layouts, per stack D5 (Operator rulings: ZFS versions natively via per-device
datasets; the live namespace always presents a consistent last-verified backup for
whole-tree offsite sync — D5a):

```
zfs backend (one child dataset per device)   reflink / hardlink / copy backends (plain dirs)
<parent>/<udid>  mounted at /backups/<udid>  /backups/<udid>/latest/
├── working/ ← idevicebackup2 mutates        ← REAL DIR: newest verified backup;
│             in place; dirty mid-job;          offsite-sync source
│             excluded from offsite sync     /backups/<udid>/versions/<ts>/
└── latest/   ← REAL DIR: materialized view    ← prior versions (rotated out of latest
              of the newest snapshot, built      by rename at commit); local-only
              from its .zfs path at commit;  /backups/<udid>/work/<job-id>/
              THE offsite-sync source          ← the only place idevicebackup2 writes
versions = zfs snapshots @quince-<job>-<ts>     (seeded from latest: reflink | hardlink
(post-verify only), browsed via .zfs/snapshot/    | copy)
```

`latest/` is a real directory on every backend, never a symlink — one uniform offsite
contract (stack D5a): include `latest/`, exclude `working/`, `work/`, and `versions/` — via ANCHORED
filter rules only (unanchored name matches would silently drop same-named dirs inside
backup content; exact block in stack D5a).

Interface (all operations idempotent, all logged with their real commands):

```
Provision(udid) → device store   // zfs: create child dataset via hook + visibility probe
Seed(job)    → workdir     // namespace: populate work from latest; zfs: no-op
Commit(job)  → VersionRef  // journaled promotion (below)
Discard(job)               // namespace: rm work; zfs: no-op (dirty working/ stays a
                           // candidate; repair-working-copy is the escape hatch)
List() / Delete(ref) / Prune(policy) / Verify(ref)
```

| Backend | Version = | Commit | Notes |
| --- | --- | --- | --- |
| `zfs` | `zfs snapshot <parent>/<udid>@quince-<job>-<ts>` | snapshot via hook/exec + rebuild `latest/` from the new snapshot's `.zfs` path (immutable source; reflink→hardlink→copy, probed) + atomic swap | hook = forced-command SSH key: `snapshot`/`destroy`/`list` on `@quince-*` + `create` of children under the configured parent; **dataset destroy never in the key** (quince prints the host command); `.zfs` visibility + new-child-dataset propagation probed — recommended PVE mount is `lxc.mount.entry … rbind,rslave` (live propagation, no restart), else printed `pct set -mpN` instructions; nested-OCI bind uses `propagation: rslave`; single-dataset fallback mode documented |
| `reflink` | `latest/` (newest) + `versions/<ts>/` dirs | journaled rotation: `latest/`→`versions/<prev>`, `work/`→`latest/` | smart default where FICLONE probe passes (Btrfs/XFS/bcachefs, ZFS 2.2+ without a hook); clones are independent files — **no hardlink-safety matrix needed**; cloning in-process via FICLONE ioctl (no `cp --reflink` dependency) |
| `hardlink` | `latest/` (newest) + `versions/<ts>/` dirs | same journaled rotation | for no-reflink filesystems (ext4); gated by the destructive hardlink-safety matrix (stack D5); in-place-mutating file classes copied, not linked |
| `copy` | `latest/` (newest) + `versions/<ts>/` dirs | same journaled rotation | full-copy seed; transient 2× space; retention defaults to latest-only |

Auto-selection: explicit zfs config → `zfs`; else probe `/backups` at runtime:
FICLONE-independence test → `reflink`, `link()`+inode test → `hardlink`, else `copy`
(stack D5). One shared `clonetree` package implements all three clone strategies for
seeding, version promotion, and the zfs `latest/` mirror.

**Commit is journaled, and startup reconciliation is a first-class subsystem** (adopted
from external review). Commit phases persist to the job journal AND to on-disk markers —
each committed version carries `quince-version.json` (version id, job id, created_at,
structural-verify result, app version), written before promotion:

```
prepared → previous_archived → latest_promoted → registry_committed   (reflink/hardlink/copy:
                                                  latest/ → versions/<prev>, work → latest/)
prepared → snapshot_created → latest_rebuilt → registry_committed     (zfs: latest/ built
                                                  from the new snapshot's .zfs path)
```

**Roll-forward principle (external-review point, accepted): once structural
verification has passed and the immutable artifact exists (the zfs snapshot, or the
promoted version dir), that backup is never discarded by recovery.** Reconciliation
always completes the remaining phases — rebuild `latest/`, finish the rotation, write
the registry row — rather than unwinding them; the only exception is an artifact whose
`quince-version.json` marker is missing or fails its hash check. A commit failure must
never destroy a successfully transferred multi-hour Wi-Fi backup.

On startup the disk is the source of truth; every half-state has a defined repair:
half-rotated `latest`/`versions` → finish the rename pair by journal phase; version on
disk/in snapshots without a DB record → adopt (protected from retention); DB record
without its dir/snapshot → mark `missing`, never silently drop; stale tmp dir → remove;
snapshot created but `latest/` stale → rebuild from the snapshot path and swap;
registry write lost → re-register from `quince-version.json`; orphaned `work/` dirs →
swept only after reconciliation completes.

Retention (`Prune`) is backend-uniform policy:
keep N recent + M dailies + K weeklies (config; generous defaults; deletion always
requires confirmed UI action or explicit policy opt-in), acting on quince-created
versions only.

## 6. Security model

This app shows a person's entire digital life; "LAN-only" is context, not a defense
(external-review point, accepted). The web-facing baseline lands with qn.1 and is
non-negotiable:

- **Transport**: HTTPS via user's reverse proxy or built-in self-signed fallback; Web
  Push (later) requires a real cert — documented, not solved by us.
- **Auth**: single admin password (argon2id hash in app DB), cookie sessions
  (`HttpOnly` + `Secure` + `SameSite=Strict`; `Secure` relaxed only for loopback-http and
  `--demo`, so local/e2e over plain http still work — never in production), session
  rotation on login, rate-limited login, idle timeout. All API and WS behind it.
- **Web baseline**: CSRF protection on mutating endpoints; strict WS `Origin`
  validation; CSP + frame denial; reverse-proxy trust headers only from configured
  addresses; path-traversal-safe file serving (malicious filenames inside backups are
  expected input); response size limits + range requests for large files; rate limits on
  expensive vault operations, not just login.
- **Audit trail**: login, unlock, file download, version delete — appended to the app
  DB, visible in UI.
- **Backup-encryption management** (§3): passwords for `encryption on`/`changepw` travel
  in TLS request bodies, reach `idevicebackup2` via pty prompt or `BACKUP_PASSWORD` env
  (same-uid exposure, short-lived process) — argv is forbidden (world-readable
  `/proc`); never logged, never stored; audit-trailed as an event *without* the secret.
- **Backup password**: never written to disk. Unlock flow: user submits it → core sends
  it inside the framed `initialize` request on the vault's stdin (never argv/env, never
  logged — raw RPC frames are unloggable by rule) → keys exist only in the vault
  process. Locking a session or TTL expiry kills the process and wipes
  `/cache/scratch/<session>`. Session scratch should be tmpfs (compose examples set it
  up, with a configurable memory limit); docs state honestly that on SSD/ZFS a "secure
  wipe" of on-disk scratch is not achievable — deleted plaintext may persist in lower
  storage layers.
- **No secrets at rest.** v1 stores no backup password in any form — lazy session-scoped
  reading (§7) removed the only feature that wanted one (unattended post-backup
  indexing). If a future feature reintroduces the need, it returns as an explicit
  opt-in design rung with honest threat-model framing, not as a default.
- **Pairing records** (`/var/lib/lockdown`) are **private-key-grade secrets** — they let
  any holder talk to the iPhone as a trusted host. Backed up into `/data` (0600), never
  served, never logged, called out in the backup-your-appdata docs.
- **Committed versions are read-only** to the vault (ro bind of the version's `browse_root`).
- **Subprocess hygiene**: argv arrays only; UDIDs and paths validated against strict
  patterns before use.

## 7. Vault: lazy, session-scoped reading (Python today, swappable seam)

- **Lazy is the model** (Operator decision): backup content is read only inside an
  unlocked session, from live decrypted copies in session scratch. Nothing persistent is
  derived from backup content except fingerprint-validated caches (below). The backup
  dataset is external storage the user may prune, replicate, or hand-edit — a stored
  index would diverge; a session can't.
- `vault serve`: JSON-RPC over stdio (shapes in `contracts.md`), opened by a framed
  `initialize` request carrying password + backup path (stdin-only, unambiguous parsing,
  never logged). The vault is **jailed to its session scratch root**, passed at spawn:
  no filesystem destination ever crosses the RPC boundary — `materialize {file_id}`
  writes under the scratch root and returns an opaque handle + scratch-relative path
  (external-review hardening, accepted). On unlock: load keybag, decrypt `Manifest.db`
  into scratch (lab-measured: ~sub-second reads, a few seconds for big manifests —
  narrated in the UI, once per session). Then serve `list/stat/materialize` lazily;
  domain DBs (`sms.db`, `Photos.sqlite`) are decrypted to scratch on first use of their
  domain. Core streams materialized files and unlinks. Hard memory ceiling documented
  per op; everything paginates; search FTS is built in scratch on first search,
  session-scoped.
- **Domain modules** — `overview`, `messages` (photos parked, lowest priority) — are
  independent, versioned adapters keyed by detected schema (introspection — table and
  column presence — never a trusted iOS version string), each with fixtures and
  tests, each failing soft (a broken module reports itself; others still serve).
  Record parsing inside these adapters is planned to come from the standalone
  `ios-backup-parser` Go library (sibling repo of the decryption library; streaming
  typed records + per-backup capability reports) once the Go vault successor (D4) has
  landed — the adapter then reduces to glue: materialize domain files into scratch,
  stream the library's records. Where that condition doesn't hold at qn.9/qn.10, the
  adapter is built in-vault as originally specced (roadmap M7). If
  photos return, the mandatory first step is reusing Apple's own prebuilt thumbnails
  inside the backup (`CameraRollDomain → Media/PhotoData/Thumbnails`) — likely lazy-
  servable like any other file, with no generation step at all.
- **Derived caches (the dormant persistence exception, D8):** artifacts genuinely too
  expensive to rebuild per session may live in `/cache`, keyed by version identity +
  `Manifest.db` hash, validated before every use, dropped silently on any mismatch or
  missing source, wipeable at any time with zero correctness impact. Currently nothing
  uses this (its only planned consumer was photo thumbnail generation, parked — and
  possibly mooted by Apple's prebuilt thumbnails).
- Memory discipline (small-NAS requirement): stream rows in batches (500–2000), never
  `fetchall()` the Manifest, cap thumbnail workers (default 2, config); the vault process
  dies at session lock — RSS returns to zero between sessions.
- **Swap-ready seam** (Operator decision): the core depends on a Go `vault.Vault`
  interface; the stdio-RPC Python process is one implementation. The RPC contract +
  golden conformance suite against fixture backups define correctness; a future all-Go
  vault (decryption ported as a separate side project) is a drop-in second
  implementation that must pass the same suite.

## 8. Frontend shape

**Device-centric IA** (`ui.design.md` §4): home is the Devices dashboard (device cards +
`Back up now` + inline job progress + N most recent backups across devices); a device's
details page owns its job history (grouped by intent) and its version list with
unlock/browse (files → overview → messages; photos parked); `Settings` is the only
other area. One WS connection feeds the client stores; REST for commands. Virtualized
lists for anything unbounded (messages, files). Design language and stack conventions:
`ui.design.md`.

## 9. Deployment reference & onboarding

**The Plex bar (stack D12):** copy-paste a compose file, `compose up`, open the UI —
that's the whole install. Compose carries only topology: image, port mapping, the three
volume binds (`/data`, `/cache`, `/backups`), and USB access (device mapping /
passthrough — the one thing a web UI can't do; each compose example documents its
variant in comments). First-run onboarding in the UI: set admin password → guided checks
(backups dir writable; backend probe with a plain-language explanation of what was
picked and why; usbmuxd reachable; optional Wi-Fi toggle) — every choice written into
`config.yml`, every check re-runnable later from Settings.

- **PVE LXC lab shape** (the Operator's own setup; specifics in `local/environment.md`,
  gitignored): Alpine LXC, USB passthrough, zfs backend with a parent dataset (one
  child per device, bind-mounted into `/backups` via an `rbind,rslave` mount entry so
  new children propagate live — probed, `pct set` fallback instructions printed),
  constrained-SSH hook to the host; whole-tree offsite sync (rclone → B2) and snapshot
  replication are both safe at any instant by construction (stack D5a).
- **Generic NAS**: docker-compose; `/backups` = shared folder bind mount; USB via device
  mapping; hardlink backend.
- **Two deployment profiles** (external-review point, accepted): `simple` — everything
  in one container (v1 default); `hardened` — usbmuxd/netmuxd run separately (host or
  sidecar container holding the USB privileges) and quince consumes only their sockets,
  keeping the HTTP-facing, plaintext-handling process free of device privileges. The
  core already speaks to muxd via configurable sockets, so the split is configuration,
  not architecture; a `compose.hardened.yml` example ships with qn.6.
- Compose examples live in `deploy/`; the lab and NAS shapes are release-gate test
  targets (the first manually, per release checklist).

## 10. Observability

Structured logs (slog, JSON in container), per-job log files under `/data/logs/<job>` and
streamed over WS live; `/api/health` includes muxd connectivity, backend probe result,
disk headroom; Prometheus `/metrics` is a cheap later rung, not v1.
