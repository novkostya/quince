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
                    │  bus     │  stdio RPC  │ (vault sidecar)  │──writes──► /cache    │
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
| `muxer supervisor` | With `devices.manage_muxer: true` (simple profile): owns the lifecycle of **every configured muxer daemon** — usbmuxd for USB *and* netmuxd for Wi-Fi (qn.4c) — each a Go subprocess in its own process group under the serve context, restart-on-crash with capped backoff, killed on shutdown; each **refuses loudly at startup if something already serves its address** (no silent adoption; unix-socket probe for usbmuxd, TCP probe for netmuxd). Powers `POST /api/devices/rescan` (restart → re-enumerate → the muxd client's reset/replay reconcile) — **USB only**: Wi-Fi has no hotplug gap and a netmuxd restart would tear a live Wi-Fi backup. `false` = hardened profile, and **the ONLY profile v0.1 ships** (qn.6p D1/D2): external muxers dialed only, reported `external` **when connected** and `unreachable` when not (§10), and **rescan RE-READS them** rather than returning 409 — it drops the muxd connection so the muxer replays its attached set. That is weaker than a restart and no surface may claim otherwise: the hotplug gap moved to the muxer's container, which quince cannot restart. (bz)'s USB-only rule is retired with the restart it was about. Ruled 2026-07-20 from qn.2's gap capture (qn.2b: usbmuxd), extended by ruling (by)/(bz) (qn.4c: netmuxd, pulled forward from qn.7 because nothing else starts it — Wi-Fi was silently dead after every restart). Per-daemon state lands in `GET /api/health` (§10); a live UI muxer-health panel + restart-policy config remain qn.7. **The netmuxd argv is load-bearing** (verified live, stack D2): a private `--socket-path` — with the default it DELETES and rebinds usbmuxd's socket — plus `--disable-usb`. |
| `device ops` | Pair / validate / info via argv subprocess wrappers; caches `ideviceinfo` snapshots; never interpolates UDIDs into shell. |
| `job engine` | One goroutine per job driving the state machine (§4); global per-UDID mutex; persists every transition to SQLite *before* emitting the event (crash-safe: on startup, orphaned `backing_up` jobs become `connection_lost` and their work dirs are discarded). |
| `backup supervisor` | Spawns `idevicebackup2` in its own process group; parses stdout incrementally (tolerant line parser — unknown lines are logged, never fatal); tracks progress and liveness via the activity sampler (§4). |
| `storage backends` | `VersionBackend` implementations (§5) behind capability probe. |
| `vault manager` | Spawns/kills `quince-vault` processes; owns session lifecycle (unlock → TTL/lock → wipe scratch); brokers RPC. |
| `event bus` | In-process pub/sub; every state change is an event; WS handler fans out to subscribers with per-client send buffers (slow client → dropped connection, never a blocked publisher). |
| `http api` | REST + WS per `contracts.md`; auth middleware (session cookie); serves embedded UI. |
| `config` | Owns `/data/config.yml` (source of truth for all non-bootstrap settings, stack D12): schema+defaults, validation, atomic canonical writes of **only the keys the user set, with no generated annotation** (ruled 2026-08-08, quince#728; this row said *"with generated doc-comments"* until then), file watch → apply-or-keep-last-good with a UI banner; serves `GET/PUT /api/config`. |

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

**THIS PARAGRAPH IS RULED TO END FOR `zfs` — BOTH SENTENCES, AND IT IS THE STRONGEST STATEMENT OF
THE DYING INVARIANT IN THE CORPUS** (Operator, 2026-08-04, quince#591; §5, end of section; **not
built**). On that backend `idevicebackup2` writes into `latest/` directly — the clause naming
`working/` *as* zfs's mutable area is the clause that stops being true — and the sync-facing
consistency goes with it, because a torn `latest/` is exactly what the second sentence promises
cannot happen. **Both hold unchanged for the namespace backends**, which keep `working/` and the
exchange. Marked here rather than left to §5's block, because this sits above it and a reader who
stops at *the invariant above all* never reaches the ruling.

- **preflight**: device present on chosen transport, `validate` passes, disk headroom
  checked, encryption state checked against policy (§3): `WillEncrypt=false` under
  `require_encryption: true` fails the job *actionably* — the error links straight to
  the encryption-management flow; with the policy relaxed, the job proceeds and its
  version is permanently marked `encrypted: false` — and
  backend `Seed()` done (namespace backends: populate `work/<job-id>/` from `latest` so
  MobileBackup2 runs a true incremental; zfs: no-op — `working/` already holds the
  previous state). **The zfs arm stays a no-op under the ruling and its REASON changes** —
  there is nothing to seed because the tool writes into `latest/`, not because `working/`
  already holds anything. Flagged because a clause that is still true for a different reason
  is the kind that survives a rewrite unexamined.
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
  `auto` resolves against **current
  presence only**: a device on neither transport is **refused actionably** (no job
  minted; the UI disables "Back up now" with the reason), because a guessed transport
  would persist a dishonest `Job.transport` (the contract stores only concrete
  `usb`/`wifi`). Explicit `usb`/`wifi` keeps the start-then-connect
  `waiting_for_device` flow. (Ruled at the qn.4b spec review, decisions log (bp).)

  **WHICH transport `auto` prefers when the device is present on BOTH is configurable —
  `backup.preferred_transport`, `usb` | `wifi`, default `usb`** (Operator ruling 2026-08-04,
  quince#654). This paragraph read *"prefers USB when plugged, Wi-Fi otherwise"*, which described a
  hardcoded USB-first sitting beside a `backup.transport` key that was validated, documented,
  editable in Settings — and read by nobody.

  | requested | device present on | result |
  | --- | --- | --- |
  | `usb` or `wifi` | — | the requested transport, unchanged |
  | `auto` | both | **the preference** |
  | `auto` | one | that one — **the preference is IGNORED** |
  | `auto` | neither | `422`, unchanged |

  **A preference, NEVER a restriction** — part of the ruling rather than a caveat on it: the other
  reading would make a Wi-Fi-only device silently unbackupable through a setting whose name does not
  say so, and Wi-Fi is the primary transport under the assisted model. **There is no `auto` in the
  preference enum**, because as a preference it would mean *prefer whatever is already preferred*;
  `auto` stays legal and correct as a REQUEST transport — `Job.transport`, the wire contract, and the
  CLI's `--transport auto`. Two enums sharing two of their values. **The default is `usb` because it
  preserves today's behaviour and for no other reason**: no claim is made about throughput in either
  direction — the Operator reports Wi-Fi outperforming USB on the soak stand — and if anyone measures
  it, this default is the thing to revisit.

There is no post-backup indexing state: backup content is only ever read lazily inside
an unlocked viewer session (§7), so success is defined purely by verify + commit.

## 5. Storage backend semantics

Two layouts, per stack D5 (Operator rulings: ZFS versions natively via per-device
datasets; the live namespace always presents a consistent last-verified backup for
whole-tree offsite sync — D5a):

**qn.5b unified the two lifecycles onto one** (decisions (cg)/(co)): every backend now writes into a
per-job `working/<udid>` seeded from `latest/`, verifies it, and **atomically exchanges** it into
`latest/`. The models differ only in what a *version* is (a snapshot vs a directory).
**That unification is RULED to end for `zfs`** — the block at the end of this section is the one
part of §5 that describes a decided future rather than the code, and everything between here and it
describes the exchange model, which is what runs today on every backend including zfs.

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
**`latest/` stays a real directory under the zfs ruling below, but it stops being a stable offsite
source on that backend** — it is torn mid-backup, so the uniform contract holds for the namespace
backends and not for zfs.

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
| `zfs` | `zfs snapshot <parent>/<udid>@quince-<YYYY-MM-DDTHH-MM>-<ULID>` | verify → **exchange** working/<udid> ⇄ latest/ (in-container `renameat2`, no privilege, no window) → rm working/ → `snapshot` via hook/exec. Seed is host-side reflink via the hook `seed` verb, or in-container reflink→copy. **RULED to become verify → `snapshot`, with no seed and no exchange — see the block at the end of this section; the Version column is unchanged by it** | hook = forced-command SSH key: `snapshot`/`destroy`/`list` on `@quince-*` + `create` of children + `seed` (clone latest/→working/<udid>); **dataset destroy never in the key** (quince prints the host command); `.zfs` visibility + new-child-dataset propagation probed — recommended PVE mount is `lxc.mount.entry … rbind,rslave` (live propagation, no restart), else printed `pct set -mpN` instructions; nested-OCI bind uses `propagation: rslave`; single-dataset fallback mode documented |
| `reflink` | `latest/` (newest) + `versions/<ts>/` dirs | verify → **exchange** working/<udid> ⇄ latest/ → archive the displaced content to `versions/<prev>` | smart default where the FICLONE probe shows the clone SHARING its extents (Btrfs/XFS measured on the lab rig; bcachefs, ZFS 2.2+ without a hook untested); clones are independent files — **no hardlink-safety matrix needed**; cloning in-process via FICLONE ioctl (no `cp --reflink` dependency) |
| `hardlink` | `latest/` (newest) + `versions/<ts>/` dirs | same exchange+archive | for no-reflink filesystems (ext4); the seed hardlinks **every** regular file — gate 12c passed on hardware (quince#518), and the class list it used to exempt is retired. Safety rests on `idevicebackup2` unlinking before it creates, so the alias breaks rather than being written through; the one path that does not (`DLMessageCopyItem`) is upstream and was not observed firing on two devices |
| `copy` | `latest/` (newest) + `versions/<ts>/` dirs | same exchange+archive | full-copy seed; transient 2× space; retention defaults to latest-only |

Auto-selection: explicit zfs config → `zfs`; else probe `/backups` at runtime:
FICLONE **clone-sharing** test (`FIEMAP_EXTENT_SHARED`, plus the independence check that rules
out a hardlink) → `reflink`, `link()`+inode test → `hardlink`, else `copy` (stack D5). A FICLONE
that succeeds without sharing falls THROUGH to hardlink — D5's one selection edge, because the
reflink backend is chosen for space and an unshared clone delivers none of it (quince#747). Where
the filesystem cannot report sharing at all, `reflink` still wins on D5's risk asymmetry and the
backend reason says the saving is UNVERIFIED. One shared `clonetree` package implements the three clone strategies; qn.5b uses it for
the **seed** (clone `latest/` → `working/<udid>` at job start, in the backend's own strategy — the
hardlink downgrade is retired, quince#518), and the atomic `latest/` swap is a plain
`renameat2(RENAME_EXCHANGE)`, not a clone.

**Gate 12c ran on hardware, 2026-08-10, and its premise did not hold** — which is why the hardlink
tier is now a real tier rather than an alias for `copy`. Amendment A disabled the hardlink seed on
the reasoning that an in-place write would corrupt the committed version through the shared inode,
and that a list of file classes (`clonetree.MutatesInPlace`) had to be proved complete first.
Measured with that list fully disabled, on two devices — a 94,034-file iPad and a 135,183-file
iPhone with a 4.1 GB incremental — the committed tree came back **byte-identical** both times, and
the metadata files ended at link count 1: `idevicebackup2` unlinks before it creates. The list was
never the mechanism, so completing it was never the blocker. **What it cost:** a seed of 272 MiB
instead of ~35 GB, per incremental, on that 35 GB backup. **The one path measurement could not
close is closed by construction:** `mb2_copy_file_by_path` (reached from `DLMessageCopyItem`) wrote
without unlinking, and its destination is named by the device at runtime, so no class list could
have covered it and no amount of device testing could enumerate it — the handler is not
command-gated, so it is reachable during a backup. `deploy/patches/libimobiledevice/0004` adds the
missing `remove_file(dst)`, making all four write paths unlink. **The hardlink tier therefore depends
on that patch**, not on an observed absence.

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

RULED, not built — the zfs sequence loses its exchange: prepared → snapshot_created →
registry_committed. Per-backend phase SETS are already the design (archived is namespace-only,
snapshot_created is zfs-only), so the enum keeps its shape; PhaseExchanged stops OCCURRING on zfs.
```

**Roll-forward principle (external-review point, accepted): once structural
verification has passed and the immutable artifact exists (the zfs snapshot, or the
promoted version dir), that backup is never discarded by recovery.** Reconciliation
always completes the remaining phases — finish the exchange (marker-guarded), archive/snapshot,
write the registry row — rather than unwinding them; the only exception is an artifact whose
`quince-version.json` marker is missing or fails its hash check. A commit failure must
never destroy a successfully transferred multi-hour Wi-Fi backup.

**RECONCILIATION IS NO LONGER A STARTUP-ONLY STEP** — Operator ruling 2026-08-08 (quince#731), built
in `qn.6i`. It splits in two, and which half runs where is the whole of the decision:

- **roll-forward** — complete any commit journal — stays **synchronous and ahead of the listener**,
  because the job-row reconciler that follows it decides `succeeded` vs `connection_lost` by asking
  whether a commit completed. Judging a job before its commit rolls forward writes *interrupted by a
  restart* for a backup that succeeded. It is O(1) in tree size on both backends — two renames, or a
  snapshot — so it costs syscalls rather than a walk.
- **the per-device scan** — adopt, mark missing, recompute latest, sweep — runs **asynchronously** on
  a runner with three triggers: startup, storage-added, and a schedule. This is where the 36–48
  seconds were, and moving it is what closes quince#592 and quince#715.

While a scan has not finished the registry is knowably incomplete, and quince **says so** on
`GET /api/health` (`reconciling`, contracts §1) rather than serving a short list silently. A scan never
runs against a `(storage, device)` whose commit path is live: it defers that device and reports it.

The disk is the source of truth; every half-state has a defined repair:
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
  button*. ~~The storage **list** still needs a restart (rung decision 1, untouched).~~ **THE LIST IS
  LIVE AS OF `qn.6g`** (quince#577): a `storage:` edit — membership, a path, a backend, a zfs block or
  retention — reaches the running `storage.Manager` through the config applier, rebuilt by the same
  `resolveSlot` a restart uses, so a live apply and a restart cannot disagree about what a storage is.
  `qn.6c`'s rung decision 1 is **spent rather than overturned**: it was a scope call for that rung,
  and this is the rung that paid it off. Which keys are live and which are not is contracts §6's
  per-key table.
  **One refusal came with it:** forgetting a storage is refused `422` while a backup is running on it,
  because every write phase re-resolves through the job's binding — so a forget between verify
  passing and commit completing would strand a job with no way forward and no cleanup path
  (contracts §1; *a commit failure must not destroy a multi-hour Wi-Fi transfer*).

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

**RULED (was `PROPOSED (gap)`): on the `zfs` backend `idevicebackup2` writes into `latest/` IN
PLACE — no seed, no working copy, no exchange. Commit becomes verify → `zfs snapshot`; the host
helper loses `seed` and gains `rollback`.** Operator ruling, 2026-08-04, relayed on
[quince#591](https://github.com/novkostya/quince/issues/591) by the architect seat — the Operator
ruled it to a session directly rather than on the forge, so that comment is the citable record
rather than a pointer to one.

**`RULED`, not `RULED and IMPLEMENTED`: nothing below is built.** Everything else in §5 describes
the exchange model, which is what the code does today on every backend, zfs included. The rung is
`qn.6h` and its **spec is the next PR, before any code** — part of the ruling, because this is
storage semantics and not a rung-local detail that review can settle.

**The never-mutate-a-committed-version rule SURVIVES, literally.** On zfs a committed version is a
`@quince-*` snapshot and copy-on-write leaves it untouched, so writing into `latest/` cannot reach
one. The version model does not move either: the snapshot still contains `latest/`, byte-for-byte
the tree it contains today, so markers, verify, retention, adopt, browse and reconcile-from-snapshots
are unchanged. `committedFromSnapshot` already builds every version's `browse_root` from
`.zfs/snapshot/<snap>/latest`, the **newest included** — the read side is already snapshot-only.

**What stops being true is a different sentence sitting in the same paragraph: *`latest/` is not
scratch space — it IS the newest committed version's content*.** On zfs it becomes the mutable head,
and the newest version becomes a snapshot **of** it. Separating those two claims is what made this
rulable rather than forbidden: a reader who conflates them concludes the change is barred by canon
and drops it.

**The price, accepted knowingly rather than discovered later: a SECOND LIFECYCLE.** `qn.5b`'s
one-lifecycle-across-all-backends property ends for this backend. The reflink / hardlink / copy
backends keep `working/` + exchange, and keep `qn.6b`'s `--gate` patch as **their** seed-latency
answer — the patch is not deleted; zfs merely stops needing it. Two write models to maintain and
test, against a seed tax paid on every zfs backup forever.

**What the ruling was taken ON is the host helper, not the latency.** `seed` is ~17 lines of
lifecycle logic in a forced-command script on a machine quince does not manage, and it has already
forced one hand-migration (`deploy/storage.md`: operators upgrading had to replace `mirror)` with
`seed)`). Afterwards every verb is O(1) and lifecycle-independent, so a future lifecycle change no
longer reaches into a file on somebody else's host. That is the prize, more than the deleted lines.
It costs **one more** forced hand-edit — remove `seed)`, add `rollback)` — and it should be the last.

**`rollback` is for ABANDON. It is NEVER the failure default, and it never fires after verify
passes.** Two canon rules meet here and both point the same way. *A failed job keeps its dirty
`working/` so a retry resumes without re-transferring* — in place, **the dirty head IS that resume
state**, so a rollback on failure discards exactly what that rule protects, and in-place would have
made retries strictly worse than the model it replaced. And roll-forward, above: once verify has
passed and the immutable artifact exists, recovery completes the remaining phases and never unwinds
them.

**`rollback` is FALLIBLE, and quince must treat it as a real outcome that can fail rather than as a
formality.** A rollback against a dataset mounted into a running container with open handles can
fail, and there is no flag that forces the unmount — `-f` forces unmounting *clones*, under `-R`
only (OpenZFS `zfs-rollback(8)`, read 2026-08-08). The behaviour on this topology is **NOT asserted
here**; it is measured before the spec fixes the semantics. A lifecycle whose abandon path has an
unhandled terminal state is what this clause exists to prevent.

**The blast radius is bounded by the helper's own parsing, which is free rather than designed.** The
helper takes the **last** argument as the target and reconstructs the command as verb + target only,
discarding every flag — so a caller sending `rollback -r <snap>` reaches `zfs` as a plain `zfs
rollback <snap>`. Without `-r`, `zfs rollback` *"refuses to roll back to a snapshot other than the
most recent one"* (OpenZFS `zfs-rollback(8)`, read 2026-08-08), and `-r`/`-R` are what destroy newer
snapshots — i.e. committed versions. So the helper structurally **cannot** destroy a version. If the
newest snapshot is itself bad, the recovery is `destroy` then `rollback`: two explicit acts, each
bounded, rather than one flag with a wide blast radius.

**Accepted cost — rclone offsite loses its stable whole-tree source on zfs.** `latest/` is torn
during a backup, so offsite must read a snapshot mount, and quince must be excluded from a general
whole-host rclone job and handled separately. The Operator accepted this explicitly (2026-08-03):
the tolerance requirement *"is probably not worth the complexity it brought to users."* D5a's
uniform include-`latest/` contract holds for the namespace backends and **not** for zfs.

**Three sub-questions are answered IN THE SPEC, not during implementation** — part of the ruling.
(1) What `reset.go`, `worksentinel.go` and the `WorkingReset` surface do on a backend with no working
directory. This is where the change is larger than a call-site count suggests: the seed state machine
is in **shared** `workdir.go`, which both backends delegate to, not in `zfs.go`. (2) Rollback under
load, measured on the real topology. (3) `Info.plist` handling — `qn.6b` candidate C captures a fresh
one and restores it over the clone, and there is no clone, so that step is re-derived rather than
deleted by assumption.

## 6. Security model

This app shows a person's entire digital life; "LAN-only" is context, not a defense
(external-review point, accepted). The web-facing baseline lands with qn.1 and is
non-negotiable:

**A SESSION AUTHORISES USE. IT DOES NOT AUTHORISE CHANGING WHAT CAN AUTHENTICATE** — Operator
ruling, 2026-08-13, on [quince#888](https://github.com/novkostya/quince/issues/888) item 3, built as
`qn.6n`. Stated before the bullets rather than inside them, because those describe the session and
this describes its limit.

A session proves a **past** authentication. Every operation that changes the credential set — adding
a credential, removing one, changing the password — requires a **present** one, presented as part of
that operation: the password, or a passkey assertion carried as a single-use proof (`contracts.md`
§1, `POST /api/auth/reauth/*`).

**Per-operation proof, not a sudo window.** The window was considered and rejected: its grant is
ambient, so a stolen session acting inside it inherits exactly the authority being defended against.

**Removing a credential requires presenting a DIFFERENT one**, which does more than it looks like.
The surviving credential is proven *usable* by the act of presenting it, so quince cannot remove its
own last way in — and no lockout check implements that. It is unreachable by construction.

**The only exemption is an install with no credentials at all** — first launch, or after
`quince auth reset`. There is deliberately none for *"a credential exists but cannot be presented
here"*, the state a `Host`-rewriting proxy produces: an attacker holding a stolen session controls
that header and could manufacture the state with one crafted request, so the waiver would hand them
their own trigger. The remedy for that state is `quince auth reset`, and its cost is stated on the
screen that offers passwordless.

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
  addresses — **with one deliberate asymmetry, below**; path-traversal-safe file serving
  (malicious filenames inside backups are expected input); response size limits + range
  requests for large files; rate limits on expensive vault operations, not just login.
- **Forwarded headers: `X-Forwarded-For` is gated ALWAYS; `X-Forwarded-Proto` only once a
  trust list EXISTS** (`QUINCE_TRUSTED_PROXIES` — quince#547, quince#549, quince#555). With
  the list unset, `X-Forwarded-For` is ignored outright and `X-Forwarded-Proto` is believed
  from any peer.

  **The asymmetry is about what DISBELIEVING falls back to, and that is the whole reason.**
  Disbelieving `X-Forwarded-For` falls back to the peer address, which is **true** — a worse
  bucketing key, never a false statement. Disbelieving `X-Forwarded-Proto` falls back to
  *"this origin is not encrypted"*, which on a correctly proxied deployment is **false** —
  and two things read that predicate and would fail toward alarm: `CookieWillBeDiscarded`
  would warn of a login loop that is not happening, and onboarding step 1 would report HTTPS
  incomplete on a working HTTPS install. Gating it unconditionally would break every existing
  proxied deployment on upgrade, to close a hole that only matters once someone has told
  quince who their proxy is.

  **So the gate is opt-in by construction. It is not a weaker rule — it is the same rule
  applied to a predicate whose failure direction differs.** Do not "fix" the inconsistency by
  gating `X-Forwarded-Proto` unconditionally: that reintroduces the false negative rejected
  on friction grounds.
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

**RULED (was `PROPOSED (gap)`): a user on a trusted network MAY knowingly opt into plain HTTP —
`Secure` is relaxed for a non-loopback host by an explicit, off-by-default, surfaced switch.**
Operator, 2026-08-02, relayed on quince#446 at `05:58:57Z`. `qn.6f`, quince#462. Option (b). Affects
the **Auth** bullet above and `core/internal/auth/cookie.go`; **built in slice 8**, which flips
nothing further — this block is already decided text.

**Flipped ahead of its implementing PR, deliberately.** The rung process flips a block in the PR that
builds it, and this one is early because **gap A's ruling depends on it**: the redirect exception
below is written in terms of this setting, and a session reading canon to decide what it may build
would otherwise meet a `PROPOSED (gap)` marker for the thing slice 4 must honour (quince#446,
2026-08-02).

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

**Option (b) — an explicit, off-by-default, surfaced opt-in. THIS IS WHAT WAS RULED**, in the shape
below and with every recommendation taken:

1. **One config key, defaulting to off** — `sessions.allow_insecure_transport: false`. Under
   `sessions:` rather than a `tls:` section, because it governs the session and CSRF cookies and it
   applies precisely when there is *no* TLS. D12: in the file, editable in the UI, no secret.
2. **It relaxes the FALLBACK only, never a positive signal.** `r.TLS != nil` and a **believed**
   `X-Forwarded-Proto: https` keep returning `true` regardless; only the final
   `return !isLoopbackHost(r.Host)` becomes conditional on this flag. *The header can only ever
   upgrade* is preserved — **but since quince#555 it upgrades only from an address in
   `QUINCE_TRUSTED_PROXIES`**, an unset list believing anyone, as before. The old unconditional
   phrasing was true of the **cookie** and false of the two consumers that invert the predicate:
   the `426` refusal and the onboarding check both read it, and both fail toward *everything is
   fine* on an injected header.
3. **It is a degraded mode, so it is surfaced** — a startup log line, a **non-dismissible** UI
   banner naming what is unprotected, and visible in Settings. Not a one-time notice.
4. **The honest cost, stated in the UI and not only here.** The session cookie and the CSRF token
   cross the network in clear, so anyone who can read the path can impersonate the admin of an app
   that — in this section's own opening words — *shows a person's entire digital life*. On a VPN
   that path is the tunnel; on a LAN it is everyone on the LAN, and "LAN-only" is already recorded
   here as context rather than a defense.
5. **No HSTS while this is reachable.** Already true (quince sends none, `httpapi.securityHeaders`)
   and it must stay true, or a user who enables this is locked out with no in-browser recovery.

**What the ruling settled beyond yes/no, and every recommendation above was taken.** The key is
`sessions.allow_insecure_transport`. **"Trusted" is the user's BLANKET ASSERTION — one boolean, not a
host/CIDR allowlist**: someone who sets the flag has already made that judgement, and an allowlist
narrows which requests get a usable cookie without changing who can read the wire, which is
machinery that reads as security it does not provide. **The banner is NOT dismissible** — a degraded
mode that can be hidden stops being surfaced.

**AND IT WINS OVER THE REDIRECT.** Gap A (contracts §6) routes one port by first byte and sends
plain-HTTP connections a `301` to `https://<same host>:<same port>` — **except when this setting is
enabled, where quince serves them.** Operator, 2026-08-02 (quince#446).

The reason is the case that inverted this question to begin with: over WireGuard or Tailscale the
transport is *already* encrypted, and a user who configured a certificate for one path should not be
forced to terminate TLS inside a tunnel they already trust. **A redirect that overrode an explicit,
off-by-default, surfaced opt-in would make this setting undeclarable whenever a certificate exists** —
which is most of the deployments that would want it.

**Sequencing — and IT DID NOT HAPPEN IN THIS ORDER, which is why the licence below went unused.**
The plan was: the flag in **slice 8**, the listener and its redirect in **slice 4**, and *"slice 4
may ship the redirect unconditional, because until slice 8 there is no flag to honour and therefore
no user it can wrong."*

**Slice 8 shipped FIRST** (quince#540, merged 2026-08-02), so by the time slice 4 (quince#551) was
written the flag existed and the licence had expired. The redirect therefore landed **with** its
exception, in one commit, and no unconditional `301` was ever on `main`.

Kept rather than deleted because the reasoning is still the rule — *a condition may not be written
on a key that does not exist* — and because a licence that was granted and then quietly dropped
looks, to the next reader, like an obligation somebody forgot. It was not forgotten; it stopped
applying. The general form is worth carrying: **a sequencing licence is conditional on the sequence,
and slices do not always land in the order a spec numbers them.**

*(These numbers are the `docs/specs/qn.6f/qn.6f.md` PR-slicing table's. They read `slice 2` until
2026-08-02 — wrong, and wrong in the direction that misdirects: slice 2 is the page and has no
listener, so it could not ship a redirect to anything. Corrected with quince#513, which found the
same defect in three more places.)*

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
forever, and **every future onboarding step will cite it as precedent**. Exempting step 1 only is
what keeps the precedent from generalising by default.

**Option (b) — step 1 stays behind auth — was rejected**, and its cost is why: the rung's own remedy
would stay unreachable to the user who most needs it.

**quince#497 lands regardless, and the ruling does not subsume it.** A user who reaches `/login`
first never sees step 1, whichever side of the guard it is on — so the login refusal that names the
cause is still the only thing that helps them. It relaxes nothing and was never an alternative to
this.

**Still the rung's to settle, deliberately not decided here:** whether the exemption covers the UI
route as well as the endpoint, and what the page renders to a visitor who is not yet authenticated —
the *"already encrypted ✓, step 1 complete"* state implies knowing whose step 1 it is.

## 7. Vault: lazy, session-scoped reading behind a swappable seam

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
  interface, and a stdio-RPC child process is one implementation of it. The RPC contract +
  golden conformance suite against fixture backups define correctness; any other
  implementation — including an in-process one — is a drop-in that must pass the same
  suite. Which of the two qn.8 builds is open (stack D4).

## 8. Frontend shape

**Device-centric IA** (`ui.design.md` §4): **`Home`** at `/` — labelled `Devices` until `qn.6d`
(quince#443), because storage joined the page and the label stopped describing it; `/devices` still
resolves — holds device cards + `Back up now` + inline job progress + **storage cards** + N most
recent backups across devices; a device's details page owns its job history (grouped by intent) and
its version list with unlock/browse (files → overview → messages; photos parked); a **storage's**
details page owns its marker-as-identity, space, the devices backed up there and the versions it
holds; `Settings` is the only
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

**Of those four, the BACKEND PROBE is built (`qn.6e`).** *Add storage* takes a typed path, reports
what is there **without changing it**, and writes the concrete backend it recommended — the
probe's own sentence is the plain-language explanation this paragraph asks for, and it names the
path it probed. The **backups-dir-writable** check is folded into the same probe rather than being
separate: an unwritable path is one of its outcomes, with its own remedy.

**The other two are not built and are not `qn.6e`'s.** *usbmuxd reachable* and the *Wi-Fi toggle*
are accepted proposals P1 and P1b in the devlog ledger, homed at "qn.6" and **not at a letter** —
so a reader should not take this sentence as describing shipped behaviour for them.

**These four are UNNUMBERED and stay that way.** quince#558 found canon claiming twice that "§9
already names steps 2 and 3" when it names no numbered steps at all, and the correction was to
strike the claim rather than invent the numbering. Onboarding surfaces are therefore named for
their **subject** — `GET /api/onboarding/https`, and `/onboarding/storage` when the first-run path
lands — which is the 2026-08-02 ruling in contracts §1.

- **PVE LXC lab shape** (the Operator's own setup; specifics in `local/environment.md`,
  gitignored): Alpine LXC, USB passthrough, zfs backend with a parent dataset (one
  child per device, bind-mounted into `/backups` via an `rbind,rslave` mount entry so
  new children propagate live — probed, `pct set` fallback instructions printed),
  constrained-SSH hook to the host; whole-tree offsite sync (rclone → B2) and snapshot
  replication are both safe at any instant by construction (stack D5a).
- **Generic NAS**: docker-compose; `/backups` = shared folder bind mount; USB via device
  mapping; hardlink backend.
- **ONE deployment profile in v0.1: `hardened`** (qn.6p, Operator 2026-08-16). The muxer runs
  separately — a host daemon, a sidecar container, or another tool's — and quince consumes only
  its socket, keeping the HTTP-facing, plaintext-handling process free of device privileges.
  `deploy/compose.hardened.yml` is the example, and it ships.

  **This read "Two deployment profiles … `simple` — everything in one container (v1 default)".**
  That `simple` profile is **DESCOPED, NOT ABANDONED**: `devices.manage_muxer: true` is refused at
  startup, the image ships no muxer daemon, and `muxsup`'s supervision is parked with its tests
  still running under `make gates`. Bringing it back is deleting one validation branch and
  restoring two Dockerfile stanzas — which is why none of it was deleted.

  **What decided it was a trade DISAPPEARING rather than being rebalanced.** Wi-Fi discovery is
  mDNS-only, so under `simple` the muxer lives inside quince's container and *that* container needs
  host networking — `compose.nas.yml` said so about itself, calling it *"strictly weaker isolation
  than the bridged default, and at odds with the hardened-profile story."* Wi-Fi is the primary
  transport, so the recommended path cost isolation by construction. Split out, the **muxer** takes
  host networking and quince stays bridged, unprivileged, with no `/dev/bus/usb` at all.

  **"The split is configuration, not architecture" survives intact** — and quince#897 is the record
  of what it cost when that was true of the settings and false of the dialer: `netmuxd_addr` would
  not take a unix socket, so the shape this profile actually wants could not be configured at all.
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

`state` ∈ `starting | running | degraded | stopped | external | unreachable`; `detail` carries the
last exit reason / why degraded / why external / why unreachable; `rescan` says whether
`POST /api/devices/rescan` restarts that daemon (USB only). An external muxer
(`manage_muxer: false`) appears with `managed: false` rather than being omitted — an absent entry
would read as "no muxer". `--demo` reports `[]`.
qn.2b's singular `muxer` object is **gone** (qn.4c clean break, ruled (bz)): with two daemons a
single aggregate could not say which one was degraded, and two overlapping representations rot.

**`external` IS A PROBED CLAIM, AND `unreachable` IS ITS OTHER HALF** (qn.6p D5, quince#897 item 2).
It was asserted from configuration alone until then — `AddUnmanaged` built a status reading *"is
served by an external muxer"* without dialing anything — and it was measured saying exactly that
about an address the daemon was **simultaneously** logging `connection refused` against. So
`external` now means *quince does not own this daemon **and is connected to it***, and one it cannot
reach reports `unreachable` carrying the dialer's own words.

**Read from the DIALING CLIENT, not from a prober beside it.** The `muxd.Client` for that endpoint
already holds the connection, so health asks it at read time. A second prober is the obvious design
and is wrong for the reason this state exists: it can dial successfully while the client sits in a
30 s backoff after a protocol-level failure, so health would read `external` while no device could
appear — the same defect relocated rather than fixed.

**`unreachable` is not `degraded`, and the distinction is the honest one.** `degraded` describes a
daemon **quince runs** misbehaving; under the hardened profile quince runs nothing, so no child
crash surfaces and rescan restarts nothing. Health is then the *only* muxer signal there is, which
is why it must not be incapable of saying anything but fine.
