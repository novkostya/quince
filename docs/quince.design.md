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

**Wi-Fi sync is a managed device property too** (qn.7). `wifi_sync` reads lockdown's
`com.apple.mobile.wireless_lockdown` domain and follows `backup_encryption` exactly: the same
`on | off | unknown` tri-state, the same absent-key-means-`off` rule, read only for a CONFIRMED
paired device, refreshed with device info. It exists because the setting otherwise has to be
ticked in Finder/iTunes, which breaks the D12 "everything in quince" promise for the **primary**
transport — a user can pair over USB in quince and still need a Mac to turn Wi-Fi backups on.

Two ways it deliberately differs from encryption. The value is **not a secret**, so the write uses
the ordinary argv wrapper rather than the pty machinery — that machinery exists to keep a *password*
out of argv, and importing it to guard a boolean would guard nothing. And **pairing does not
auto-enable it**: quince cannot distinguish a flag that was never set from one the user deliberately
turned off, so flipping it as a side effect of pairing would silently overrule a choice.

**The key is `EnableWifiConnections`, measured on hardware 2026-07-31** — a boolean, read `true` on
a device whose Wi-Fi sync was on. The read is over the muxer for the device's transport, which for
Wi-Fi means netmuxd's own socket rather than usbmuxd's; a tool pointed at the default socket sees no
network device at all.

**How that key was arrived at is the part worth keeping.** The name was guessed correctly by the
roadmap and appears **nowhere** in libimobiledevice's source, so nothing corroborated it until a
device did — and until then the read answered `unknown` **without querying**, because a wrong key
exits 0 printing nothing and the absent-key rule would turn that into a confident `off` on every
device. The third enum value exists precisely so quince can say it does not know, and this is the
case it was for. **One thing is still owed:** an off/on differential, which is what would prove this
key is the one that *changes* rather than `SupportsWifiSyncing`, also true in the same dump.

## 4. The backup job state machine

```
queued → waiting_for_device → preflight → seeding → backing_up → verifying → committing → succeeded
   └──────────────┴───────────────┴──────────┴────────────┴────────────┴──── failed / cancelled / connection_lost
```

(`seeding` — qn.6a — narrates the `working/` clone from `latest/`. qn.6b overlaps that clone
with the tool startup on a cold seed via candidate C's `--gate`: the engine launches
idevicebackup2 gated so the on-device passcode prompt fires in ~1–2 s, captures the fresh
`Info.plist` the tool writes, seeds `working/` while the tool waits, restores that `Info.plist`
over the clone's stale copy, then opens the gate. A resume/first-backup has nothing to clone and
starts the tool straight away. See stack D2 for the patch shape.)

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
  staged — `active → silent_but_connected → suspected_stall → timed out` (**18 min** of
  zero activity, qn.6b; coarse-tuned to the 15-min reality, fine stage-tuning is qn.7). The
  backstop is deliberately held **longer than the patched idevicebackup2 receive timeout**
  (15 min, #1413 — stack D2): on a Wi-Fi flap the tool blocks in one receive, writing nothing
  for up to that long, and a sampler kill inside the tool's own patience would SIGKILL a backup
  the tool was about to complete, undoing the patch. On a *cleanly-idle dead link* the tool loops
  `MOBILEBACKUP2_E_RECEIVE_TIMEOUT` forever without exiting (verified in the qn.6b spike), so the
  sampler — not the tool — is the sole authority that eventually classifies it `connection_lost`;
  the lab ((ct)) proved silent multi-minute app_limited stretches are normal and must not be killed.
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

**RULED and IMPLEMENTED (was `PROPOSED (gap)`): `quince-storage.json`, and the creation moment a
config-declared storage does not have — `qn.6c`, Operator ruling 2026-07-31, relayed on
quince#378.** Accepted as proposed; built across quince#398 (the marker as an artifact), quince#410
(the rule) and the PR carrying this edit (the wiring).

At `qn.6c` there are several roots. A removable disk's **path** changes on replug, so a storage
identified by its path cannot answer *"is this the same storage?"* — hence a marker at the storage
**root**, the analog of `quince-version.json` one tier up, modelled on `storage/marker.go` (a
self-checksum over the marshalled struct with `Checksum` emptied — self-contained, no companion
file):

```jsonc
{ "storage_id": "01J...", "backend": "zfs", "created_at": "...",
  "app_version": "...", "checksum": "..." }
```

**The creation moment, which is the question quince#378 actually asks.** The epic wants the backend
*selected at creation and immutable thereafter*, **and** a reachability check before each backup —
but a storage arriving from `config.yml` has no creation event, and *immutable after creation*
and *probed at startup* then disagree about a dataset remounted as something else. They stop
disagreeing once creation is defined by the storage's own contents — **and by whether quince has
created this storage before**, which the first version of this proposal omitted:

> **The first startup that finds a reachable path with no `quince-storage.json` at its root **and
> no row in `storages` for that config entry** is that storage's creation moment.** quince probes
> the backend then, writes the marker, and never probes for selection again.
>
> **A reachable path with no marker, for a storage the DB already knows, is a MISSING MEDIUM** —
> refuse, exactly as a mismatched marker refuses. Never re-create, never re-probe.
>
> Every later startup and every pre-backup check **reads** the marker and **compares**; it does not
> re-select.

**Without the second clause an unmounted mountpoint is created as a new storage, and backups then
go to the system disk** (found at spec review, quince#381). `/mnt/backup-disk` is a readable, empty
directory on the root filesystem whenever the disk is unplugged — the marker is on the disk. A
contents-only rule probes it as **`copy`** rather than the disk's `zfs`, writes a **new UUID** into
the mountpoint, has that marker **shadowed rather than deleted** when the disk mounts over it (so it
returns on the next unmount), and accepts backups onto the root filesystem while the user believes
they are going to the removable one. Silent, and the epic's own motivating case is a removable disk.

The discriminator already exists: the `storages` table plus a marker-derived `storage_id` mean the
DB knows whether a storage has ever been created, so *path reachable, no marker* resolves into two
states instead of one. **Keyed on the config entry's `name`, not its `path`** — a path moves when a
disk is remounted elsewhere; when the medium is present the marker is authoritative and a known
`storage_id` at a new path is a **move**, not a new storage.

**Residual, stated rather than engineered away:** the very first startup after declaring a storage
whose medium is absent has neither marker nor row, and is indistinguishable from a genuine
creation. The accompanying written requirement is **declare a storage with its medium present**.
Closing it mechanically means recording an expected filesystem/device id at creation — deliberately
out of this rung. Creation is therefore a **loud, user-visible event** naming the path, the probed
backend and the reason, so the one remaining silent case is not silent.

A marker that is present and **disagrees** with the probe — the remount case — is a **refusal**:
quince does not back up to that storage and says exactly why. Accepting the new backend would
write versions the marker misdescribes; silently refusing would be a fallback. Neither is
permitted (*no silent caps or fallbacks*).

**May it be written into today's `/backups`, which already holds committed versions?
Recommended yes, on measurement rather than argument.** The storage root is enumerated in exactly
two places and both skip non-directories — `scanJournals` (`storage/journal.go:96-98`) and
`Manager.reconcileUDIDs` (`storage/reconcile.go:156-161`, double-guarded by `IsDir()` **and**
`validUDID`). `Scan` starts a level deeper at `latestDir`/`nsVersions`; `Verify`
(`storage/verify.go:36`) runs against a *tree* dir, deeper still, and has no notion of a foreign
entry at all. The marker also sits **above** every device dir, hence above every version, so
*never mutate a committed version* is untouched. Written idempotently on first startup after
upgrade.

**One thing the measurement does NOT clear, and it is a real defect if unaddressed.**
`AnchoredFilterRules` (`storage/offsite.go:16-21`) returns exactly two rules — `- /<subdir>/*/working/**`
and `- /<subdir>/*/versions/**` — and **neither matches a root-level file**, so the marker would be
**synced offsite**. A third anchored rule is required:

```
- /<subdir>/quince-storage.json
```

**Exclude rather than include, and the reason is the epic's own open fork.** Point 3 leans that
offsite is a **replication** of a storage, not a storage. If the marker rides along, the replica
claims its source's UUID and two places assert one identity — precisely the question the file
exists to answer. Excluding it keeps that fork open; including it would decide it silently.

**Driven, not only tested.** Against the real image: a fresh root is CREATED (marker written,
`verified: true`); a restart OPENS the same `storage_id`; and with the marker removed — an unplugged
disk's bare mountpoint — quince **refuses, exits 1, and writes nothing**, naming the medium as the
cause and the remedy. That last case is the bug this block exists to prevent.

**That third measurement is a record of what was driven, and the REFUSAL half of it is superseded**
by the ruling in the next block (2026-08-01). A missing medium no longer stops the daemon: it is
served as `reachable: false` with a reason. What survives unchanged, and is the part that mattered,
is that quince **writes nothing** to such a path and it **never accepts a job** — the bare
mountpoint is still never treated as an empty new storage. Kept rather than rewritten because it
records a real run against the real image; read it as *what the one-storage build did*, not as
current behaviour.

Spec: `docs/specs/qn.6c/qn.6c.md`, gap 4.

**RULED (was `PROPOSED (gap)`): an unreachable storage is a LISTED STATE, not a refusal to serve.**
Operator ruling, 2026-08-01, on quince#435 — relayed by architect session `arch1`. It supersedes the
refusal half of the measurement above, which was ruled when exactly one storage could be declared.

**quince serves in every case, with reachability as data.** A storage that cannot be reached now is
listed `reachable: false` with a reason; it does not stop the daemon, and it does not block a backup
to any other storage.

- **The DEFAULT storage unreachable, others fine → serve.** A job naming no `storage_id` is
  **refused with a reason naming the default** — never silently redirected to whichever storage
  happens to be reachable. A fallback there would write a backup to a disk the user did not choose,
  which is *no silent fallbacks* at its most expensive.
- **EVERY declared storage unreachable → serve.** Refusing to start makes the page that would
  *explain* the problem unreachable, so the user gets a dead daemon and a log line instead of a
  screen naming the disk to plug in. A quince that can do nothing but explain itself beats one that
  cannot do that either.
- **`missing_medium` and `unreachable` get the SAME behaviour and DIFFERENT text.** Both serve, both
  are `reachable: false`; the reason string distinguishes them, because they call for different user
  actions — *plug the disk in* versus *this path is readable but it is not your backup medium*.
  `missing_medium` remains the more alarming of the two and its reason says why.
- **Reachability may change WITHOUT a restart** — re-probe on demand; *plug the disk in and press the
  button*. The storage **list** still needs a restart (rung decision 1, untouched).

**The one hard refusal that survives is a config declaring NO storages at all** — a configuration
error nothing at runtime fixes, whose remedy is editing a file. That is G7, unchanged.

**The invariant that makes serving safe, and it is not optional: a storage whose `Resolution` is not
`OK()` NEVER accepts a job.** Serving is honest only while nothing can write to a storage quince
could not verify. `missing_medium` is the case that proves it — a readable path with no marker is
exactly where a write would land on the wrong filesystem.

**Two consequences the ruling implies rather than states.** First, **`Resolution` stops being a
startup-time fact and becomes a current one**: anywhere it is cached, the cache is a claim about the
past — the same class as `preflight`'s *reach is presence, not freshness*. Where the probe happens is
to be decided deliberately rather than left to settle. Second, **the `(Slot, bool)` seam from
quince#433 is what absorbs this**, and both its call sites already refuse honestly; story 5 adds a
second *reason* for `!ok` — declared but not reachable now, versus not configured at all — which
callers distinguish in their **message**, not in their control flow.

**Not decided here:** the wire shape of `GET /api/storages` — field names, and whether the reason is
a code or prose. That is contracts territory and belongs in the story 5 spec, reviewed before code.
This ruling settles **behaviour**, not wire format.

**The four sub-questions that made it rulable are kept** — the default unreachable, all unreachable,
whether `missing_medium` diverges from `unreachable`, and whether reachability changes without a
restart — because the ruling reads better with the argument it was taken against than as a bare
verdict. Spec: `docs/specs/qn.6c/qn.6c.md`, story 5.

## 6. Security model

This app shows a person's entire digital life; "LAN-only" is context, not a defense
(external-review point, accepted). The web-facing baseline lands with qn.1 and is
non-negotiable:

- **Transport**: HTTPS via user's reverse proxy or built-in self-signed fallback; Web
  Push (later) requires a real cert — documented, not solved by us.
- **Auth**: single admin password (argon2id hash in app DB), cookie sessions
  (`HttpOnly` + `Secure` + `SameSite=Strict`; `Secure` relaxed only for loopback-http and
  `--demo`, so local/e2e over plain http still work — never in production; **whether a user
  may knowingly opt out of that on a trusted network is an open gap, proposed at the end of
  this section**), session
  rotation on login, rate-limited login, idle timeout. All API and WS behind it.
  **Rotation is PER CLIENT, and quince is multi-device**: a login supersedes the authenticating
  client's own prior session and leaves every other device's alone (Operator ruling, quince#373).
  Fixation is defeated by minting a fresh session id, never by evicting anybody — so "one
  concurrent session" would be a *separate* policy, and it is deliberately not the one taken:
  `ui.design.md` calls the iPhone a first-class client, and a second first-class client that
  evicts the first is not one. This line read only "session rotation on login" until 2026-08-01,
  which is ambiguous between the two readings; the code took the evicting one while quoting
  fixation as its reason, and the Operator found it by being signed out of a desktop by an iPad.
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

**PROPOSED (gap): may a user on a trusted network opt into plain HTTP — that is, may `Secure`
be relaxed for a NON-loopback host?** `qn.6f`, quince#462; the analysis is on quince#446, where
three separate rulings say this block is required before any plain-HTTP code exists. Affects the
**Auth** bullet above and `core/internal/auth/cookie.go`. Nothing is built on this until it is ruled.

**The defect it answers.** `secureCookie` returns `true` for every non-loopback host, so over
`http://` to a LAN address quince sets a `Secure` cookie on an insecure origin. The browser
**rejects it outright** — never stored, never sent. Login succeeds, the next request is
unauthenticated, and the user is returned to the login screen: **a loop with no error message.**
Only loopback works, and a phone is not loopback — so the primary client of a Wi-Fi backup tool
cannot log in at all.

**The current behaviour is correct rather than accidental**, and the code comment says so: canon
requires HTTPS and we never silently downgrade. **The question is not whether the rule is right.
It is whether the user may knowingly turn it off** — which relaxes the baseline this section calls
non-negotiable, and is therefore the Operator's, not a rung's.

**The case that inverts the obvious answer: a VPN.** Over WireGuard or Tailscale the transport is
*already* encrypted, and quince still breaks — for a reason with nothing to do with the threat
model. Adding TLS inside an encrypted tunnel buys nothing and costs a certificate to manage, so
**plain HTTP is the correct offer for a tunnelled deployment rather than the lazy one**. It also
makes plain HTTP strictly better than a self-signed certificate *in that case*: the same encryption
on the wire, minus the browser interstitial.

**Option (a) — no, status quo.** HTTPS or loopback, nothing else; one rule, no exceptions. Its cost
is not zero: a VPN user must terminate TLS inside a tunnel they already trust, and every LAN user
meets an unexplained login loop rather than a refusal. A baseline enforced as a silent failure is
itself a *no silent fallbacks* problem, in the direction where the user cannot tell what went wrong.

**Option (c) — yes, detected**: quince notices plain-HTTP LAN access and relaxes on its own.
**Rejected, and recorded only so the rejection is on the record.** A security baseline that switches
itself off when the network makes it inconvenient is the thing the baseline exists to prevent.

**Option (b) — an explicit, off-by-default, surfaced opt-in. RECOMMENDED**, and the shape is the
part that needs ruling rather than the yes/no:

1. **One config key, defaulting to off** — `sessions.allow_insecure_transport: false`. Under
   `sessions:` rather than a `tls:` section, because it governs the session and CSRF cookies and it
   applies precisely when there is *no* TLS. D12: in the file, editable in the UI, no secret.
2. **It relaxes the FALLBACK only, never a positive signal.** `r.TLS != nil` and
   `X-Forwarded-Proto: https` keep returning `true` regardless; only the final
   `return !isLoopbackHost(r.Host)` becomes conditional. *The header can only ever upgrade* is
   preserved verbatim.
3. **It is a degraded mode, so it is surfaced** — a startup log line, a **non-dismissible** UI
   banner naming what is unprotected, and visible in Settings. Not a one-time notice.
4. **The honest cost, stated in the UI and not only here.** The session cookie and the CSRF token
   cross the network in clear, so anyone who can read the path can impersonate the admin of an app
   that — in this section's own opening words — *shows a person's entire digital life*. On a VPN
   that path is the tunnel; on a LAN it is everyone on the LAN, and "LAN-only" is already recorded
   here as context rather than a defense.
5. **No HSTS while this is reachable.** Already true (quince sends none, `httpapi.securityHeaders`)
   and it must stay true, or a user who enables this is locked out with no in-browser recovery.

**What a ruling settles beyond yes/no**, any of which may be edited into it: the key's name and
section; whether "trusted" is the user's blanket assertion (recommended — someone who sets the flag
has already made that judgement) or a declared host/CIDR allowlist (more machinery, and it changes
nothing about who can read the wire); and whether the banner is dismissible (recommended: no).

<!-- gap-heading-check: ignore — the decided text below belongs to the NEXT block (step 1
     pre-auth), not to this one. This block LOST ITS TERMINATOR when that neighbour was flipped,
     because a live open-gap marker is one of the three things that bounds a block; flipping the
     block below removed the boundary, so this block's bounds now run past it to section 7.
     This block's own question — may a user opt into plain HTTP — is decided on quince#446 but is
     deliberately NOT flipped here: that ruling says it flips in the PR that implements it.
     Remove this opt-out in that PR, when this block is flipped too. -->

**RULED (was `PROPOSED (gap)`): onboarding step 1 IS reachable without a session — a fifth
`authExempt` route, by exact path.** Operator, 2026-08-02, on quince#501: *"Of course it's pre-auth,
that's the only viable option."* Option (a). Found by an Operator question on quince#462, not by the
spec — which names the step-1 endpoint in its Boundary and did not say which side of the auth guard
it sits on.

**The chicken-and-egg.** On the deployment this rung exists for — a phone, fresh install, plain HTTP
to a LAN address — the sequence is: `/setup` works (it is exempt), login returns `200`, the browser
discards the `Secure` cookie, and the user is back at the login screen with no error. **Step 1 is the
page that explains exactly this, and it is behind the door the defect locks.**

**The exempt set is four routes and has been since `qn.1`** — measured at `core/internal/httpapi/middleware.go:73-79`:

```go
case "GET /api/health", "GET /api/auth/status", "POST /api/auth/login", "POST /api/auth/setup":
```

Anything this rung adds is behind `authGuard` unless deliberately exempted. **Widening that set is a
security-posture change, which is why this is a gap rather than a line in the rung's spec.**

**What was ruled: step 1 is pre-auth, a fifth exempt route.** What it discloses is which transport
tiers exist and whether *this* request arrived over a secure origin — no device data, no
configuration, no secret. The precedent was already here: `needs_setup` deliberately puts first-run
guidance ahead of a session.

**BY EXACT PATH, and that is a constraint rather than a preference.** `authExempt` switches on
`r.Method + " " + r.URL.Path` — exact strings, no prefixes — so a prefix exemption such as
`/api/onboarding/*` would require changing the **matcher** as well as the set. The narrow form is the
only shape the function has. (Measured by the reviewer on quince#501; the block originally argued for
the narrow form on taste, and the code turns out to require it.)

**The cost, recorded because it will bite later:** this is one more pre-auth surface to keep honest
forever, and **every future onboarding step will cite it as precedent** — and steps 2 and 3 are
already specified in §9, so that precedent has named claimants. Exempting step 1 only is what keeps
the precedent from generalising by default.

**Option (b) — step 1 stays behind auth — was rejected**, and its cost is why: the rung's own remedy
would stay unreachable to the user who most needs it.

**quince#497 lands regardless, and the ruling does not subsume it.** A user who reaches `/login`
first never sees step 1, whichever side of the guard it is on — so the login refusal that names the
cause is still the only thing that helps them. It relaxes nothing and was never an alternative to
this.

**Still the rung's to settle, deliberately not decided here:** whether the exemption covers the UI
route as well as the endpoint, and what the page renders to a visitor who is not yet authenticated —
the *"already encrypted ✓, step 1 complete"* state implies knowing whose step 1 it is.

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
