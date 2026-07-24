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
| `muxer supervisor` | With `devices.manage_muxer: true` (simple profile): owns the lifecycle of **every configured muxer daemon** — usbmuxd for USB *and* netmuxd for Wi-Fi (qn.4c) — each a Go subprocess in its own process group under the serve context, restart-on-crash with capped backoff, killed on shutdown; each **refuses loudly at startup if something already serves its address** (no silent adoption; unix-socket probe for usbmuxd, TCP probe for netmuxd). Powers `POST /api/devices/rescan` (restart → re-enumerate → the muxd client's reset/replay reconcile) — **USB only**: Wi-Fi has no hotplug gap and a netmuxd restart would tear a live Wi-Fi backup. `false` = hardened profile: external muxers dialed only, reported `external` in health, rescan → 409. Ruled 2026-07-20 from qn.2's gap capture (qn.2b: usbmuxd), extended by ruling (by)/(bz) (qn.4c: netmuxd, pulled forward from qn.7 because nothing else starts it — Wi-Fi was silently dead after every restart). Per-daemon state lands in `GET /api/health` (§10); a live UI muxer-health panel + restart-policy config remain qn.7. **The netmuxd argv is load-bearing** (verified live, stack D2): a private `--socket-path` — with the default it DELETES and rebinds usbmuxd's socket — plus `--disable-usb`. |
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
         last_seen, last_backup: {at, job_id|null, status} }
```

**`last_backup` is derived, never stored on the device** (qn.4c, ratified (bz)): it is the
newest committed **version** for that UDID — versions are the source of truth for "has this
device been backed up", so the field is right after a restart and covers **adopted** versions
(a replicated/restored dataset), which have no job at all → `job_id: null`. It therefore means
the last **successful** backup; a failed last attempt lives in the intent-grouped job history.
The engine re-publishes the device (`device.updated`) after a successful commit so the card
updates without a page refresh.

Device scope: anything speaking the standard pairing + MobileBackup2 stack — iPhone and
iPad are first-class (identical protocol; the lab proved iPhone, iPad needs no extra
code); Vision Pro is untested and unpromised (visionOS may be iCloud-only); Apple Watch
has no direct backup protocol and is out of scope. Nothing in the codebase may be
iPhone-string-specific — the `model` field drives any per-device presentation.

Rules: presence is muxd-event-driven, never polled. `paired` is refreshed lazily
(`idevicepair validate`) — on attach and before any job, not on a timer. **A locked
device reads as `paired: unknown`** — `validate` reports "passcode set" for ANY locked
device, paired or not (qn.3 hardware finding), and the full lockdown identity read
(which can pop a Trust prompt on an unpaired device — an accidental auto-pair) runs
only after a *confirmed* validate; all other reads use the no-auto-pair simple query.
A device vanishing mid-job does not remove it from the table; it flips presence and
lets the job engine decide.

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
  records resolves to existing blob files. **The DB checks branch on encryption**
  (architect ruling at the qn.5 spec review — the original checklist silently assumed
  an unencrypted manifest): since iOS 10.2 an *encrypted* backup's `Manifest.db` is
  itself encrypted, so passwordless open-and-sample is impossible there.
  `Manifest.plist`'s `IsEncrypted` selects the variant — encrypted (the product
  default): `Manifest.db` exists, has non-trivial size, and does **NOT** carry the
  plaintext SQLite magic (an "encrypted" manifest that opens as plain SQLite is a
  red flag), plus blob-shard sanity (the two-hex-char directories exist and are
  non-empty on a full backup); the record-sample resolution moves to the content
  level. Unencrypted: the full checklist as written. *Content verification* (vault
  decrypts a canary file — and, for encrypted versions, performs the deferred
  manifest-record sampling) cannot run unattended — no stored password — so it happens
  on the user's next unlock and is recorded per version as `content_verified_at`; the
  UI shows both levels honestly.
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
  **escape hatch** is **Reset** — `quince device reset-working <udid>` / `POST /api/devices/{udid}/
  reset-working` (qn.5b: discard the dirty `working/` so the next backup re-seeds clean from
  `latest/`, losing only the partial; the landed `RepairWorkingCopy` op, never automatic in v0.1).
  On FAILURE the dirty `working/` is otherwise KEPT so a one-tap retry RESUMES into it (no
  re-transfer). Never two concurrent jobs per UDID. Transport policy
  `auto` prefers USB when plugged, Wi-Fi otherwise — and resolves against **current
  presence only**: a device on neither transport is **refused actionably** (no job
  minted; the UI disables "Back up now" with the reason), because a guessed transport
  would persist a dishonest `Job.transport` (the contract stores only concrete
  `usb`/`wifi`). Explicit `usb`/`wifi` keeps the start-then-connect
  `waiting_for_device` flow. (Ruled at the qn.4b spec review, decisions log (bp).)

There is no post-backup indexing state: backup content is only ever read lazily inside
an unlocked viewer session (§7), so success is defined purely by verify + commit.

## 5. Storage backend semantics

Two layouts, per stack D5 (Operator rulings: ZFS versions natively via per-device
datasets; the live namespace always presents a consistent last-verified backup for
whole-tree offsite sync — D5a):

**qn.5b unified the two lifecycles onto one** (decisions (cg)/(co)): every backend now writes into a
per-job `working/<udid>` seeded from `latest/`, verifies it, and **atomically exchanges** it into
`latest/`. The models differ only in what a *version* is (a snapshot vs a directory).

```
all backends — /backups/<udid>/ (zfs: a child dataset mounted here; namespace: plain dirs)
├── latest/            ← REAL DIR: the newest verified backup = the version content; the SOLE
│                        offsite-sync source; permanent between backups. Changed ONLY by a single
│                        renameat2(RENAME_EXCHANGE) at commit — never unoccupied.
├── working/<udid>/    ← the ONLY place idevicebackup2 writes (target = working/, its own
│                        <target>/<UDID> convention → no symlink). Per-job: seeded from latest/ at
│                        job start (safe strategy), dirty mid-job, KEPT on FAILURE (a retry
│                        resumes — no re-transfer), removed on success. Excluded from offsite sync.
└── versions/<ts>/     ← prior versions — NAMESPACE ONLY (rotated out of latest/ at commit);
                         local-only. zfs has NO versions/ dir: prior versions are
                         @quince-<YYYY-MM-DDTHH-MM>-<ULID> snapshots (post-verify), browsed via
                         .zfs/snapshot/<snap>/latest/. So on zfs, between backups the dataset holds
                         ONLY latest/ (every snapshot = exactly one complete backup, structurally).
```

`latest/` is a real directory on every backend, never a symlink — one uniform offsite
contract (stack D5a): include `latest/`, exclude `working/` and `versions/` — via ANCHORED
filter rules only (unanchored name matches would silently drop same-named dirs inside
backup content; exact block in stack D5a).

Interface (all operations idempotent, all logged with their real commands):

```
Provision(udid) → device store   // zfs: create child dataset via hook + visibility probe; else mkdir latest/
Seed(udid,job)  → target   // return the idevicebackup2 target (working/ parent); seed working/<udid>
                           // from latest/ (safe strategy: hardlink→copy), or RESUME a dirty one —
                           // UNLESS the work sentinel says a seed was in progress (a partial clone
                           // killed mid-seed), in which case discard + re-seed (Finding B, (cw))
Commit(udid,job) → VersionRef  // verify working/<udid> → atomic exchange into latest/ → snapshot/archive
Discard(udid,job)          // KEEP the dirty working/ so a retry resumes (all backends; Reset discards)
RepairWorkingCopy(udid)    // Reset: discard the dirty working/ (the next backup re-seeds from latest/)
List() / Delete(ref) / Prune(policy) / Verify(ref)
```

| Backend | Version = | Commit | Notes |
| --- | --- | --- | --- |
| `zfs` | `zfs snapshot <parent>/<udid>@quince-<YYYY-MM-DDTHH-MM>-<ULID>` | verify → **exchange** working/<udid> ⇄ latest/ (in-container `renameat2`, no privilege, no window) → rm working/ → `snapshot` via hook/exec. Seed is host-side reflink via the hook `seed` verb, or in-container reflink→copy | hook = forced-command SSH key: `snapshot`/`destroy`/`list` on `@quince-*` + `create` of children + `seed` (clone latest/→working/<udid>); **dataset destroy never in the key** (quince prints the host command); `.zfs` visibility + new-child-dataset propagation probed — recommended PVE mount is `lxc.mount.entry … rbind,rslave` (live propagation, no restart), else printed `pct set -mpN` instructions; nested-OCI bind uses `propagation: rslave`; single-dataset fallback mode documented |
| `reflink` | `latest/` (newest) + `versions/<ts>/` dirs | verify → **exchange** working/<udid> ⇄ latest/ → archive the displaced content to `versions/<prev>` | smart default where FICLONE probe passes (Btrfs/XFS/bcachefs, ZFS 2.2+ without a hook); clones are independent files — **no hardlink-safety matrix needed**; cloning in-process via FICLONE ioctl (no `cp --reflink` dependency) |
| `hardlink` | `latest/` (newest) + `versions/<ts>/` dirs | same exchange+archive | for no-reflink filesystems (ext4); the **seed is disabled-to-copy** until the destructive hardlink-safety matrix passes (gate 12c) — a hardlink seed would alias the committed `latest/`; in-place-mutating file classes copied, not linked |
| `copy` | `latest/` (newest) + `versions/<ts>/` dirs | same exchange+archive | full-copy seed; transient 2× space; retention defaults to latest-only |

Auto-selection: explicit zfs config → `zfs`; else probe `/backups` at runtime:
FICLONE-independence test → `reflink`, `link()`+inode test → `hardlink`, else `copy`
(stack D5). One shared `clonetree` package implements the three clone strategies; qn.5b uses it for
the **seed** (clone `latest/` → `working/<udid>` at job start, hardlink downgraded to copy — gate
12c), and the atomic `latest/` swap is a plain `renameat2(RENAME_EXCHANGE)`, not a clone.

**Commit is journaled, and startup reconciliation is a first-class subsystem** (adopted
from external review). Commit phases persist to the job journal AND to on-disk markers —
each committed version carries `quince-version.json` (version id, job id, created_at,
structural-verify result, app version), written before promotion:

```
qn.5b — the atomic exchange is the shared pivot (marker-guarded, since re-running it reverses it):
prepared → exchanged → archived → registry_committed          (namespace: working/<udid> ⇄ latest/,
                                                                then displaced content → versions/<prev>)
prepared → exchanged → snapshot_created → registry_committed  (zfs: working/<udid> ⇄ latest/,
                                                                rm working/, then snapshot latest/)
```

**Roll-forward principle (external-review point, accepted): once structural
verification has passed and the immutable artifact exists (the zfs snapshot, or the
promoted version dir), that backup is never discarded by recovery.** Reconciliation
always completes the remaining phases — finish the exchange (marker-guarded), archive/snapshot,
write the registry row — rather than unwinding them; the only exception is an artifact whose
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
- **Audit trail**: login, unlock, file download, version delete, **device pairing and
  encryption changes** (qn.3 — event + UDID + outcome, never the password) — appended to
  the app DB, visible in UI.
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

**`/api/health` muxer shape** (design-level, deliberately NOT frozen in contracts until the
qn.7 UI panel consumes it). One entry per configured muxer daemon — quince may supervise two:

```jsonc
{"status": "ok", "version": "…",
 "muxers": [
   {"name": "usbmuxd", "role": "usb",  "managed": true, "state": "running", "rescan": true},
   {"name": "netmuxd", "role": "wifi", "managed": true, "state": "degraded",
    "detail": "netmuxd keeps exiting: exit status 1", "rescan": false}
 ]}
```

`state` ∈ `starting | running | degraded | stopped | external`; `detail` carries the last exit
reason / why degraded / why external; `rescan` says whether `POST /api/devices/rescan` restarts
that daemon (USB only). An external muxer (`manage_muxer: false`) appears with `managed: false`
rather than being omitted — an absent entry would read as "no muxer". `--demo` reports `[]`.
qn.2b's singular `muxer` object is **gone** (qn.4c clean break, ruled (bz)): with two daemons a
single aggregate could not say which one was degraded, and two overlapping representations rot.
