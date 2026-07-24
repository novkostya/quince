# quince — roadmap

> The forward plan: milestones made of reasonably-sized rungs (`qn.N`), each independently
> buildable, testable, and reviewable. The live state is
> [`quince.progress.md`](quince.progress.md); the build loop is
> [`program/quince.program.md`](program/quince.program.md). Rungs get a spec in
> `specs/qn.N/` before implementation (the first two exist; later specs are authored when
> their turn approaches, from the outlines here).

## The epic

**A self-hosted, rock-solid iPhone backup manager + browser: Apple-native encrypted
backups into versioned storage with a calm real-time web UI — first useful release =
Devices + one-button backup + live progress + history.**

## Milestones

### M0 — Floor (`qn.0`)
Repo scaffold (core/vault/ui/deploy/docs), Makefile gates, CI skeleton green, container
builds and pushes to the LAN registry. *Gate: `make gates` green in CI on a hello-world
slice of all three languages; `make image` produces a runnable container.*

### M1 — Live core (`qn.1`, `qn.2`, `qn.2b`)
- `qn.1` core daemon skeleton: bootstrap env + the `config.yml` core (schema,
  validation, atomic canonical writes, `GET/PUT /api/config`; file-watch and generated
  doc-comments are staged to qn.6 per D12), slog, SQLite
  (migrations), HTTP+WS with auth (first-run set-password), event bus, `--demo` mode
  serving fixture devices/jobs, embedded UI shell (sidebar + empty pages).
- `qn.2` muxd client: Listen-mode connection to netmuxd (single muxer, USB + Wi-Fi in
  v0.4+ — stack D2) abstracted over N sockets (usbmuxd fallback topology = config only),
  device table with per-transport presence, `device.*` events; record/replay fake muxd
  for tests. *Gate: plug/unplug iPhone in the lab LXC → attach/detach visible in UI
  within 1 s (USB via the default usbmuxd topology AND Wi-Fi via netmuxd mDNS); the
  netmuxd-USB audition (stack D2): presence, fresh USB pairing, and real backup traffic
  through netmuxd's USB path on v0.4.3 (messages cross the 64 KiB boundary immediately)
  — clean → default flips to single-muxer; reproduces the lab's documented
  `message was too large (65536 bytes, max = 65535)` failure → upstream issue filed
  with the exact log line (patch-in-pinned-build optional); replay tests in CI.*
- `qn.2b` muxer lifecycle + hardware proof (inserted 2026-07-20 from qn.2's gap capture,
  decisions log (ar)): quince supervises the in-container usbmuxd — `devices.manage_muxer`
  config gate, own process group under the serve context, restart-on-crash with capped
  backoff, killed on shutdown, **refuse-loudly if the socket is already served**;
  `POST /api/devices/rescan` (+ UI Rescan button) reusing the reset/replay reconcile
  (contracts §1/§6 already landed); hardware-free supervisor tests (`TestHelperProcess`
  fake muxer). Then the rung **runs qn.2's deferred lab gates as its own acceptance**.
  *Gate: `compose up` on the lab CT brings USB up with NO host muxer (the D12 Plex-bar
  promise restored); plug/unplug ≤1 s in the UI — **PASSED on hardware 2026-07-20**.*
  As-built: the netmuxd-USB audition was **re-homed to qn.7** at rung close (Operator
  ruling, decisions log (aw) — named owner, procedure preserved in the qn.2b spec gate 8);
  FULL muxer work (netmuxd co-supervision, restart policy, muxer health in UI,
  `compose.hardened.yml`) stays in qn.6/qn.7.

### M2 — Device ops (`qn.3`)
Pair / validate / info subprocess wrappers + **backup-encryption management** (status
from `WillEncrypt`; enable / change-password / disable via `idevicebackup2` with
pty-or-env password passing — the spec verifies which the tool supports — argv
forbidden; on-device passcode step narrated; persistent unencrypted-device warning
banner) + Devices UI page (presence, identity, paired state, encryption state, "Trust
this computer" flow narration). *Gate: fresh container to paired, encryption-on device
via UI only — including setting the backup password through quince; wrappers covered
by fake-CLI tests; no password ever appears in argv, logs, or the audit trail.*

### M3 — Backup engine, both transports (`qn.5` storage FIRST, then `qn.4a` engine, then `qn.4b` Wi-Fi + history)

Order ruled 2026-07-20 (decisions log (ar), the "rung closes provable" hard rule): the
engine's `succeeded` requires `Commit()`, which is storage's — so storage lands first and
is proven on fixture trees + manually-produced backups. The old `qn.4` was then split
((be)): it was three heterogeneous concerns wide (engine, Wi-Fi, CLI) — `qn.4a` proves
the transport-agnostic engine over USB with the minimal CLI as its harness, `qn.4b`
makes Wi-Fi first-class and closes M3 with the both-transports UI-driven gate. Rung
numbers are labels, not order (the qn.7-before-qn.6 precedent).

- `qn.5` storage backends per the two-layout model (stack D5): `zfs` snapshot-native
  with per-device child datasets (Provision via constrained hook, visibility probes +
  `rbind,rslave` mount guidance, `.zfs` browse, `latest/` mirror reflink→hardlink→copy,
  dirty-working reporting) + `reflink` (FICLONE-based smart default —
  Btrfs/XFS/hookless-ZFS) + `hardlink` + `copy` (journaled commit, `latest` swap), the
  auto-selection probe (FICLONE-independence / inode tests on the real `/backups` fs),
  one shared `clonetree` package, `quince-version.json` markers, **startup reconciliation matrix** (every commit
  phase × crash point has a defined, tested repair), adopted-version discovery, **the
  destructive hardlink-safety matrix** (full→incremental, big-file change, `-wal`/
  `-shm`, deletions, renames, interrupted + next incremental, iOS upgrade, encryption
  change; wherever hardlinks are used — the hardlink backend and the zfs mirror's
  hardlink fallback; reflink builds exempt). *Gate (no engine needed — fixture- and
  manual-driven, provable at rung close): the kill-at-every-stage matrix (seed / verify /
  each commit phase) recovers to a defined state on restart, on fixture trees; a
  manually-produced `idevicebackup2` tree commits into a version; **an rclone sync of
  the whole tree running concurrently with a (manual) backup uploads a valid backup**
  (the D5a contract, automated with a local target); on zfs a syncoid pass mid-write
  replicates every committed version intact; iMazing opens the committed version.*
- `qn.4a` **job engine + supervisor + the minimal driving CLI** (split from qn.4,
  decisions log (be) — the old rung was three heterogeneous concerns wide): the
  transport-AGNOSTIC core — stdout parser (fixtures = real lab transcripts incl.
  stalls, `-4` failures, `waiting_for_passcode`, **and the Wi-Fi torn sessions**, so
  the engine is Wi-Fi-shaped from day one in CI), activity-sampler liveness with
  staged stall states, NO auto-retry (failed → `user action required`, manual retry
  with `retry_of`), cancel via process group, crash-safe persistence, per-UDID lock,
  the supervisor half of structural verification (exit code + `Backup Successful`
  output; the tree half landed in qn.5), integration with qn.5's
  `Seed`/`Verify`/`Commit`. Ships the minimal **headless CLI as the rung's own lab
  harness** (`quince device list / backup start / versions list / versions path
  --latest` — the offsite-sync source printer) — the fastest way to torture the
  engine before the UI exists (external-review point, accepted in-place; the CLI was
  ruled NOT a separate milestone — standalone it is thin plumbing that would rob the
  engine rung of its driving interface). **Inherits qn.5's re-homed gate-12 hardware
  legs** (decisions (bm)): its first real-backup hardware session runs qn.5's storage
  `Commit` on real traffic, so the host-side `mirror` verb on the real rpool, iMazing-
  opens, syncoid mid-write, and the **12c destructive hardlink-safety matrix** (which
  validates the hardlink mirror/backend tier) attach here (legs preserved verbatim in
  the qn.5 spec's gate-12 section). *Gate (USB): an encrypted backup on the lab
  box, driven from the CLI, ends `succeeded` as a committed verified version on the
  real backend; the engine-level kill matrix (kill at seed / backing_up / verify /
  commit hand-off) recovers to defined states on restart; iMazing opens it.
  Inherited from qn.5's gate 12 ((bn)) — measurements taken during this same gate's
  backup, no extra session: the host-side `mirror` verb proves live on the real
  rpool (`bclonesaved` observed moving on the commit), and a syncoid pass
  mid-backup replicates every committed version intact.*
- `qn.4b` **Wi-Fi first-class + transport policy + job history** (closes M3): real
  Wi-Fi backups over netmuxd, `transport: auto` (prefer USB when plugged, Wi-Fi
  otherwise), the job history API/UI (raw but live, grouped by intent — contracts
  §2), CLI completion (`versions verify`; `quince device repair-working-copy` — the
  surface over qn.5's backend op). **Explicitly NOT a Wi-Fi demotion** — ruling (h)
  stands: Wi-Fi keeps first-class status with its own rung and hardware gate inside
  M3, before qn.7 and far before v0.1; qn.4a already proves the engine against
  Wi-Fi's failure modes in CI. *Gate (the integrated e2e, closing M3): encrypted
  backups over BOTH transports, driven from the UI, end `succeeded` with committed
  verified versions; a Wi-Fi mid-backup disconnect lands in an honest
  `user action required` with committed versions untouched. Plus the 12c destructive
  hardlink-safety matrix inherited from qn.5 ((bn)) — its transitions
  (full→incremental, interrupted+next-incremental, encryption change; iOS-upgrade
  opportunistic) are natural products of this rung's repeated real backups; the
  hardlink mirror/backend tier stays disabled-to-copy (surfaced) until the matrix
  passes.*

### `qn.5b` — atomic `latest` + the `working/` lifecycle redesign (inserted 2026-07-22, (cg); **BUILT (CI) 2026-07-24, (cp)**)

> **BUILT (CI-proven).** Atomic `latest` via in-container `renameat2(RENAME_EXCHANGE)`; per-job
> `working/<udid>` seeded from `latest/` (safe strategy — hardlink→copy), kept-dirty-on-failure for
> resume; commit reordered verify→exchange→snapshot; symlink dance dropped (free-space bug 28b97de
> structurally impossible); snapshot `quince-<YYYY-MM-DDTHH-MM>-<ULID>`; honest seed-derived `kind`;
> Reset REST+CLI. `make gates`/image green. Spec approved-with-amendments ((co)); build in (cp).
> **Remaining: the real-rpool lab legs** (G-snapshot / G-rclone / G-exchange-live + syncoid) on an
> Operator hardware day. Original scope below.

**Storage correctness against a stated requirement, not polish** — and the reason it runs
**before the B2 cron is trusted**. The Operator's three constraints are: a `zfs snapshot` at
*any* instant captures a solid `latest/`; the directory `idevicebackup2` writes into is
excluded from rclone; and changes to `latest/` are **atomic**. Constraint 3 is violated today
(stack D5 `PROPOSED (gap)`): both swap paths do `mv latest → latest.old; mv latest.new →
latest`, so `latest/` briefly **does not exist** — an rclone sync crossing it deletes the
remote copy.

- **Atomic `latest`.** Replace both two-rename swaps with **exchange-rename**
  (`renameat2(RENAME_EXCHANGE)`), which never leaves the name unoccupied. **Verify
  `RENAME_EXCHANGE` on ZFS live first** (interface fact — a VFS flag the filesystem must
  implement); the symlink workaround stays forbidden (D5a). Privilege split: the hook keeps
  the FICLONE reflink into `latest.new`; **quince does the exchange in-container** (rename
  needs no privilege).
  - **UNVERIFIED LEAD (2026-07-22, from an LLM — treat as a hypothesis, not a fact; the
    "interface facts are looked up live, never remembered" rule applies doubly here):** modern
    OpenZFS is *reported* to support `RENAME_EXCHANGE` including ZIL replay records, and Debian
    13 / PVE 9 ships a one-liner CLI for it — `apt install util-linux-extra` then
    `exch <dir-a> <dir-b>`. Two models were asked and **disagreed** (one pessimistic, one
    optimistic), which is precisely why this is a test and not an argument. **First task of the
    rung:** run `exch` on two non-empty dirs in the real pool and settle it. If unsupported,
    the fallback ladder is: atomic **symlink swap** (fixes both observers; requires reopening
    D5a's no-symlink rule + a mandatory documented `rclone --copy-links`, Operator sign-off),
    with **rclone-from-an-explicit-snapshot-path** as defense-in-depth worth adopting either way.
  - **⚠ Load-bearing constraint reported with the lead — it kills one design option.** The
    exchange requires both paths to be **ordinary directories on the SAME mounted filesystem**;
    two child datasets in one pool are still different filesystems and fail with `EXDEV`. So the
    idea of making `latest/` its **own child dataset** (floated to keep snapshots clean) is
    **incompatible with the atomic exchange** — `latest` and its staging sibling must be plain
    directories inside one dataset. The per-job `working/` design below already satisfies the
    clean-snapshot goal without needing a separate dataset, so nothing is lost — but verify this
    constraint alongside the flag itself.
- **Per-job `working/` (Operator-proposed, architect-agreed).** Stop keeping `working/`
  permanently. Seed it as a **reflink clone of `latest/` at job start** (near-free, proven at
  gate 11: `bclonesaved` +33.6 GiB) — MobileBackup2 increments from a clone exactly as it does
  from a persistent dir; the old "Seed is a no-op" elegance predates knowing cloning was cheap.
  Between backups the dataset then holds **only `latest/`**, so every snapshot contains exactly
  one complete backup *structurally*, and the rclone exclusion covers a directory that usually
  doesn't exist. **Preserve resume: on FAILURE keep the dirty `working/`** so a retry resumes
  into it (a 33 GB Wi-Fi backup dying at 90% must not restart); on success it *becomes*
  `latest/`.
- **Reorder commit:** verify → atomic exchange → **then** snapshot, so the snapshot holds
  `latest/` = the version and `browse_root` points at the real latest backup rather than a
  directory named `working`.
- **Rename the snapshot format `quince-<ULID>-<date>` → `quince-<date>-<ULID>`** (Operator, readability
  — the opaque ULID-first name reads badly in `zfs list`). LOW-RISK, verified: the name's identity is
  carried by the `quince-version.json` **marker**, not by parsing the name (`zfscli.go` only
  prefix-matches `quince-*` for the hook glob and uses the whole name opaquely as a `.zfs/snapshot/
  <name>/` path element), so this is a one-line change to `snapNameFor` (`zfscli.go:152`) + its
  comments + the contracts §2 example + tests. The `quince-` prefix is preserved so the constrained
  hook glob (`@quince-*`) is unaffected, and old-format snapshots still adopt (marker-driven, prefix
  still matches) — no migration needed. Rides qn.5b because this rung already reworks the snapshot
  path (`browse_root` moves `…/working` → `…/latest`).
- **Drop the symlink dance.** The `<target>/<UDID>` stub exists only because
  `idevicebackup2` insists on writing to `<target>/<UDID>/` — and it caused the gate-blocking
  free-space bug (28b97de) by putting the stub on the wrong filesystem. Choose the staging path
  so the tool's own convention lands where we want, then exchange that directory into `latest/`.
  No symlink, and the bug class is structurally impossible.
- **Post-failure UX (Operator-raised; the shape is the implementer's call, reviewed at the
  contract proposal).** With a dirty `working/` kept, the honest actions are **Retry** (resume
  into it) and **Reset** (discard it; the next backup re-clones from `latest/`, losing only the
  partial). A third, **Retry clean**, is Reset + Backup-now and may just confuse — **the
  implementer decides 2 vs 3 and lands it as a contract proposal for review.** Note `Reset` is
  already the landed `RepairWorkingCopy` backend op (qn.5), exposed CLI-only by qn.4b — a UI
  surface means a REST addition, hence the contract review.
- **Model unification.** The namespace backends already seed-from-`latest` and rotate; with
  this, zfs does the same, differing only in that a version is a snapshot rather than a
  directory. D5's "two version models" collapses toward one.

**Alternative considered and REJECTED — the all-ZFS-primitives design** (proposed 2026-07-22;
recorded because it is a reasonable idea an implementer may independently have, and the
reasoning generalizes): *(1) `zfs clone` the working area into its own dataset, (2) run the
backup there, (3) `zfs send workdir@ready | zfs receive -F …/latest` to publish it.*

- **Step 1 is genuinely clever** and deserves credit: a clone is instant, zero-space,
  ZFS-native, and would sidestep the whole FICLONE-`EPERM`-in-unprivileged-userns saga, since
  cloning is a `zfs` command through the hook rather than a filesystem syscall. It still loses:
  we already have a cheap seed (host-side reflink, *measured* at gate 11); making `working` a
  **dataset** instead of a **directory** is exactly what forces step 3's problem; and a clone
  **pins its origin snapshot** (undestroyable until the clone is gone or promoted), entangling
  retention with the backup lifecycle for no gain.
- **Step 3 is fatal — it makes the very problem qn.5b fixes far worse.** A plain send/receive
  is a **full copy** (33 GB rewritten, no block sharing — discarding the reflink win), and
  incrementality would demand fragile send-lineage bookkeeping between two datasets forever.
  **Precisely what `receive -F` does** (corrected 2026-07-22 — an earlier draft here said
  "applied progressively," which is wrong and would leave an implementer with a bad model):
  it **rolls the destination back** to the most recent common snapshot, then streams into a
  hidden `%recv` temporary, and the new state appears **atomically at the end**. So nothing is
  ever half-written — and **a ZFS snapshot is never corrupt**; it is atomic and internally
  consistent by construction. The failure is different and still disqualifying: (a) for the
  duration, `latest` holds an **older** backup (the rollback target), and (b) rollback/receive
  into a *mounted* dataset requires an **unmount/remount**, which leaves the mountpoint
  **present and EMPTY** — the worst shape for `rclone sync`, which reads it as "source has no
  files" and **deletes the remote copy**. Exposure becomes **minutes** (a 33 GB receive) rather
  than microseconds. *(The exact mount behaviour of `recv -F` into a mounted dataset is
  reasoned, not measured — verifiable in minutes if this is ever revisited; it is not blocking,
  since the copy cost alone disqualifies the approach.)*
- **The principle (why no dataset-level operation can work here):** the requirement is that a
  *filesystem path stays continuously valid for a walker*. Every dataset-level operation —
  send, receive, rename, promote — involves a **mount transition**, so none can satisfy it.
  Directory-level `renameat2(RENAME_EXCHANGE)` keeps the mount stable and flips the entry in one
  syscall with no observable gap. Right tool for the actual constraint.
- **Where send/receive IS right: replication.** It is already the offsite path (`syncoid` to the
  remote PVE host, proven at gate 11). It moves datasets *between pools*; it cannot swap a
  locally-visible directory without taking that directory offline. Right tool, wrong job.

*Gate — note the requirement bundles **two observers that fail independently**, so they are
asserted separately (Operator-sharpened 2026-07-22): **(1) the snapshot observer** — a `zfs
snapshot` taken at **any** point of a running backup and of a commit contains a **complete
`latest/`** (today it can contain **none at all**: a snapshot landing in the two-rename window
is perfectly consistent and perfectly useless as a restore point); **(2) the filesystem-walk
observer** — a continuous `rclone sync` loop running across many commits **never deletes and
never tears** the remote copy (today the same window shows a missing `latest/` and sync mirrors
the deletion). Plus: `RENAME_EXCHANGE` verified on the real pool before any code; a failed
backup leaves a resumable `working/` and a retry completes without re-transferring; between
backups the dataset holds only `latest/`.*

### ⚑ `qn.6a` — soak-ready UI (a slice of qn.6 pulled forward; inserted 2026-07-22, (ch))

**Purpose: make quince good enough that the Operator actually *uses* it daily, so it soaks
under real usage on staging *while the process revamp runs*.** The revamp is process work — the
codebase is idle during it — so a usable app converts that idle stretch into soak time, and soak
time cannot be compressed or backfilled. **Mobile is the precondition**, not polish: if you must
be at a desktop, the daily use (and therefore the soak) never happens.

**Sequence: `qn.5b` → `qn.6a` → freeze + revamp (app soaking).** 5b runs first deliberately —
it changes the `working/` lifecycle and the failure/Retry semantics, i.e. exactly the behaviour
the soak observes. Soaking on a model about to change would waste the findings.

- **Mobile-first responsive pass** over the **existing** device-centric IA — explicitly a
  *responsive + touch-target* pass, **not an IA redesign**. The dashboard-cards → device-details
  structure is already mobile-amenable; the desktop-shaped pieces (job log pane, version lists,
  the pair/encryption dialogs, the backup-history table) are the real work. That distinction is
  the difference between a rung and a project — hold it.
- **Offline devices are listed.** Today the device table is live muxd presence only, and a device
  with no transports is *removed* — so a backup tool forgets your device exists the moment you
  unplug it. Minimal shape: union the live table with the distinct UDIDs already in the versions
  registry, and **persist the identity already fetched at enrichment** (name/model) so offline
  rows aren't bare UDIDs — a column or small table, not a new subsystem. **Offline card
  behaviour (Operator-specified):** keep the same card shape and a **disabled "Back up now"** so
  layout stays aligned with online cards — but disabled **with a reason** on hover/tap, never a
  dead button (the established qn.4b pattern, and the (bq) lesson). Show last-seen and version
  count; versions stay browsable.
- **Device labels in the backup list** — the list currently doesn't say which device a backup
  belongs to. Small, real.
- **The log-blob `SplitFunc` fix** (gate-11 finding, optional if it grows): one fix reportedly
  clears the mangled log pane, the stale byte counter, **and** the log bloat. It is directly on
  the soak path — live progress is what you stare at from a phone — so it earns its place if it
  stays small.
- **From the qn.5b hardware session (homing CONFIRMED, contract shapes ruled — (cv)):** **(cr)(a)**
  missing/dead versions must stop rendering as normal backups — add **`missing: bool` to
  `wire.Version`** (contracts §2, ruled) and render such versions **explicitly dead, not omitted**
  (no size claim, no `Unlock`/browse affordance, an "artifact gone — remove?" action wired to the
  existing `DELETE /api/versions/{id}`). Omission would silently shrink history — during a soak
  that masks exactly the drift the soak exists to surface; same family as finding #6. **(cu)
  option (1)** — add a **`seeding` job phase** between `preflight` and `backing_up` (contracts
  phase-enum addition, ruled) so the UI narrates "Preparing — cloning from your last backup…"
  instead of dead air during the O(files) seed. Both are soak-path UX and cheap.

**Explicitly NOT in scope:** storage setup in onboarding (the auto-probe already chooses; what's
missing is *explaining* the choice, which is qn.6's onboarding-checks work beside P1/P1b — don't
build a config UI for something that should be automatic); the **Synology/alpha-tester
prerequisites** (a platform feasibility spike on DSM, and gate **12c**, which un-defers the
moment a non-zfs tester appears — see the note below); anything in qn.6's release half.

**Forward note — "Wake up" (Operator idea, post-`qn.12` spike, NOT this rung).** An offline
device may simply be *asleep on the same LAN*. Once qn.12 ships Web Push, a **"Wake up"** action
could send a push to the device's PWA to rouse it so its mDNS announcement resumes and netmuxd
rediscovers it. Fits the assisted model exactly (quince cannot back up unattended, but it may
*nudge*), and needs no new infrastructure beyond a push kind. Genuinely uncertain and therefore
a **spike**: it is unproven that waking the screen restores Wi-Fi-sync visibility, and it can
only ever work when the device is on the same network — so the UI must stay honest ("wake
attempt sent; if it's on your network it should appear shortly"), never claiming success.

*Gate: the Operator drives a complete backup **from the iPhone browser** — start, watch live
progress, cancel, retry — without pinch-zooming or meeting an unusable control; a powered-off
device appears in the list with last-seen, version count, and a disabled-with-reason "Back up
now"; the backup list names its device. Then the soak begins: quince in real daily use on
staging for the duration of the freeze/revamp.*

**Process note:** this is the **last rung under the current process**. Its implementer should
record process friction as it goes (decisions-log letter collisions, doc drift, gate-ownership
seams, spec overhead) and hand that to the revamp as **evidence** — otherwise the process gets
redesigned from memory.

### ⚑ Daily-driver target — the Operator's "personally usable" milestone (ruled 2026-07-20, (by))

Before a planned **code freeze + process revamp**, the bar is a quince the Operator
actually *uses*: **a full backup cycle over BOTH transports, live progress without a page
refresh, and the major bugs fixed.** That is exactly two things:

- **`qn.4c`** — netmuxd **co-supervision** (pulled forward from qn.7) + the qn.4a
  usability findings (i)/(iv)/(v). Without supervision nothing starts netmuxd on
  `compose up`, so Wi-Fi is silently dead after every restart — the same reason qn.2b
  exists for usbmuxd. Reuses the hardware-proven `internal/muxsup` (generalize its
  hardcoded `usbmuxd -f -S <socket>` + unix-socket probe to also drive netmuxd over TCP).
- **one hardware day** — the **inherited qn.4b gate 11** (both transports, UI-driven,
  live progress watched on a real backup) + netmuxd surviving a container restart + the
  iMazing glance. Gate 11's Wi-Fi leg runs on the **supervised** netmuxd — the shape
  actually deployed, not a hand-started one.

**Explicitly deferred past the freeze:** **gate 12c** (the destructive hardlink matrix —
the Operator's deployment is zfs; the hardlink tier stays disabled-to-copy, surfaced),
the rest of qn.7's Wi-Fi hardening, and everything from qn.6 on. **Reaching this target
is the freeze point.**

- `qn.4c` **netmuxd supervision + usability fixes** (inserted 2026-07-20, (by)): generalize
  `internal/muxsup` to co-supervise netmuxd (config-gated like `devices.manage_muxer`,
  TCP probe, restart-with-backoff, health surfaced) + fix the qn.4a findings: **(i)**
  `willEncrypt`→`unknown` mis-map on unencrypted devices AND the cold-lockdown enrichment
  race that hard-fails a legitimate encrypted backup at preflight; **(v)** the engine never
  writes `device.last_backup` on commit (only demo does) → "No backups yet" on a device
  with real versions; **(iv)** the card lingering at "Backing up 100%" through verify+commit
  (likely subsumed by (v)). *Gate: the inherited qn.4b gate 11 — encrypted backups over
  BOTH transports driven from the UI end `succeeded` with live progress observed and no
  page refresh; Wi-Fi runs over SUPERVISED netmuxd which survives a container restart; a
  device with committed versions shows its real last backup.*

### M4 — Wi-Fi reliability hardening (`qn.7`)
The flakiness-absorption rung, BEFORE the public release because Wi-Fi is primary:
patched-timeout libimobiledevice source build in the image (30 s → 15 min, upstream
#1413), ~~netmuxd supervision~~ (**moved to qn.4c**, (by)) + restart-policy tuning,
**the netmuxd-USB audition on pinned
v0.4.3** (re-homed here from qn.2b, decisions log (aw); procedure verbatim in the qn.2b
spec, gate 8 — verdict flips the D2 default to single-muxer or files the upstream issue),
chaos suite (replay every torn-session
transcript + injected mid-file disconnects), liveness-stage thresholds tuned against the
real lab box, honest UX copy for the slow/silent/passcode phases.

**Hardware evidence banked 2026-07-24 ((ct)):** the qn.5b hardware session reproduced real Wi-Fi
failures on the iPhone (`Could not receive from mobilebackup2 (-4/-256)` / netmuxd
`Heartbeat(Timeout)`), root-caused via pcap/`ss`/netmuxd-DEBUG to genuine Wi-Fi packet loss + link
drops (netmuxd exonerated; not a message-size bug) — exactly what the patched timeout absorbs. **Two
real-world captures are preserved as chaos-suite fixtures** (a genuine Wi-Fi failure + a
success-with-pause) — **local-only on the lab host, LAN IPs, must never enter git.** **New, load-bearing
finding for the liveness-threshold tuning:** iOS Wi-Fi backups have long *legitimate* `app_limited`
idle pauses (the phone doing its own snapshot/file-prep) — quince must NOT treat a multi-minute idle
window as a stall/deadlock; a real 34 GB backup completed after exactly such a pause.

**Spike (feasibility-first, (cn)): enable/disable Wi-Fi discoverability ("Wi-Fi sync") from
inside quince.** Today a fresh device's Wi-Fi sync must be ticked in **Finder/iTunes** ("Show
this device when on Wi-Fi") — so the D12 "everything in quince" onboarding promise is **broken
for the PRIMARY transport** (a user can pair over USB in quince but then needs a Mac to actually
turn Wi-Fi backups on). **Likely mechanism, to VERIFY not assume** (interface-facts-looked-up
rule): a lockdown `SetValue` on the `com.apple.mobile.wireless_lockdown` domain
(`EnableWifiConnections`-ish key) — libimobiledevice's `lockdownd_set_value` supports it, and it
is a **USB-trusted** operation, which fits perfectly: pairing is USB-only anyway (stack D2), so
the natural moment is *during the qn.3 USB pair* (plug → Trust → quince pairs **and** flips
Wi-Fi sync on → unplug → Wi-Fi works, no Finder detour). Read-back gives a `wifi_sync:
on|off|unknown` device property to display + toggle beside pairing/encryption. **Spike answers:**
the exact domain/key; whether SetValue is accepted and takes effect (reboot/respring needed?);
USB-required confirmed; whether the device must be unlocked / a Trust re-confirm fires. Home is
either here (Wi-Fi) or a small device-ops addition folded into qn.6 onboarding; if infeasible,
onboarding must document the Finder step honestly. *Gate: injected
disconnects land in clean `user action required` states with committed versions
untouched, and a manual retry from dirty `working/` completes and verifies.*

### M5 — v0.1 public shape (`qn.6`)
Devices + Backup button (both transports) + live progress + history + version list, UI
polished to the design canon; full first-run onboarding (guided checks per design §9 —
the Plex bar; incl. accepted **P1**: detect a USB muxer that runs but can't OPEN devices
— frozen container `/dev` or missing cgroup perms — and surface the actionable
live-`/dev/bus/usb`-bind fix in onboarding + `/api/health`); Settings as a transparent editor over `config.yml` (D12 staging
completes: file-watch, generated doc-comments); `compose.hardened.yml` (muxd split
profile); release pipeline (tag → goreleaser → ghcr multi-arch → GitHub Release); deploy
examples; license audit; README with demo screenshots. *Gate: **one week of real Wi-Fi
backups driven from the UI** (phone in hand — unlock, confirm, passcode; zero cable,
zero tmux), every failure landing in an honest actionable state and no committed
version ever perturbed; the Operator retires the tmux ritual; a fresh user goes
compose-up → onboarding → paired → first backup without reading anything but the
compose file's comments.*

### M6 — Vault: unlock & browse, lazy (`qn.8`)
`quince-vault serve` (stdio RPC per contracts) behind the Go `vault.Vault` interface
(the swappable seam), session lifecycle (TTL, lock, scratch wipe), lazy Manifest reads,
file browser + single-file download in UI, the golden conformance suite. **Vault
implementation is conditional (stack D4 successor ruling):** the independent Go
decryption library + thin Go RPC binary if that library is conformance-ready when this
rung starts; else Python/`iphone_backup_decrypt` as originally specced. Either way:
same RPC, session lifecycle, scratch jail, and conformance suite, whose goldens come
from the Python reference regardless. Fixture-backup generator comes from the Go
library's encrypt/builder side (or a documented lab-only gate if unavailable). No
persistent indexing — session-scoped reads only. *Gate:
unlock a real version, browse domains, download a file, lock; keys provably confined to
the vault process (no password/keys in core logs, env, argv, or disk — and nothing
persisted after lock).*

### M7 — Domain viewers (`qn.9` overview, `qn.10` messages)
Each its own release-worthy rung with iOS-versioned adapters + scrubbed fixtures, all
lazy session-scoped reads:
- overview: device summary, app list, sizes;
- messages: **research spike first** (real `sms.db` across available iOS versions —
  schema, `attributedBody`, attachments join — before any API fields are designed; only
  the domain envelope is pre-frozen, contracts §1), then adapter + chats + session-
  scratch FTS5 search + virtualized thread UI.

**Domain parsing is planned to come from `ios-backup-parser`** — a standalone Go
library (separate repo, sibling of the decryption library; charter lives there) whose
M0 schema spike *is* the research spike above, run off quince's critical path.
Consumption is conditional, chained on the D4 successor ruling: if the Go vault landed
at qn.8 **and** the library covers the domain when qn.9/qn.10 starts, the vault's
adapter reduces to glue (materialize domain files → stream the library's typed records
+ capability report); otherwise the rung proceeds as specced here (in-vault adapter,
spike in-rung). Either way the vault RPC / domain envelope stays the contract surface,
changed only via a contract-change rung.

*Gate per rung: renders the Operator's real backup correctly (spot-checked against
iMazing) + fixture tests in CI.*

Photos (`qn.11`) are **parked at lowest priority** — the Operator's photo pipeline is
already covered by icloudpd + Immich, which is the better tool for that job. If the rung
is ever picked up, its spec MUST start with a research spike: reuse Apple's own prebuilt
thumbnails from the backup (`CameraRollDomain → Media/PhotoData/Thumbnails`) before
building any generation or cache machinery — that path may make photos just another lazy
domain with zero persistence.

### M8 — Phone-first assisted automation (`qn.12`)
The assisted-backup flow (stack D13): PWA manifest + service worker + Web Push (VAPID);
the Shortcut opportunity signal (`POST /api/automation/backup-opportunity`, short-lived
token; ALL policy server-side — staleness threshold, visibility, active-job,
reminder cooldown); push kinds `backup_available` / `action_required` /
`backup_completed` / `backup_overdue`, each deep-linking into the PWA; retention
policies UI. *Gate (the assisted acceptance list): the Shortcut trigger reliably reaches
quince; a fresh backup produces no notification; a stale one produces exactly one; the
push opens the right screen; a manual Wi-Fi backup started from it completes; a
mid-backup disconnect produces an `action_required` push and a one-tap retry works; no
committed version is ever perturbed; reminders never spam (cooldown honored).*

### Later / parked

**Scoped per-device view + QR/link device enrollment (captured 2026-07-22, (cm) — Later, not soon).**
A single-**device** view of quince reachable via an admin-issued **scoped token** (permissions:
view / backup / restore-later), so the *device owner* — not the admin — can trigger their own
backups and browse their own data without whole-app admin access. Onboarding is seamless: the admin's
device-detail page has an action that produces a **link + QR code**; opened on another device it
auto-authorizes *that* device to the scoped view. Why it matters (more than convenience): **it is the
delegated-access dimension the phone-first assisted model (qn.12) implicitly assumes away** — qn.12
treats admin = phone owner, but in a household they differ, and the scoped device view is exactly the
per-owner phone surface. Natural home: **after / with qn.12**. Prior art: Plex home users, Jellyfin,
streaming-device "claim/pair" flows. *Architect notes banked so a naive later build doesn't get the
security wrong:* **(1)** the link/QR must carry a **one-time, short-TTL enrollment secret that mints a
device-bound scoped session on first use — NOT a bearer token in the URL** (a URL is leaked by
history, screenshots, chat forwards); **(2) restore is a dangerous scope** (it can overwrite a
device) — likely admin-only or re-auth-gated even here; **(3)** this is a real **auth subsystem**
(capability tokens, per-device sessions, an enrollment flow, a revocation UI, audit coverage of
scoped actions), so it **reopens the qn.1 security baseline** (design §6) — not a small feature.

Photos viewer (qn.11 — see M7 note: Apple-prebuilt-thumbnails spike first); restore &
Finder-compatible export; **offsite-sync ergonomics** (post-commit hook firing with the
committed version's path — lets rclone→B2 run push-style after every good backup; maybe
built-in rclone scheduling one day — until then the documented pattern is a cron line
over `quince versions path --latest`); more domains (WhatsApp, contacts, call history —
contacts/calls are easy wins, may pull forward); Prometheus metrics; Docker Hub mirror;
multi-device polish; public demo instance.

## ⚑ Post-freeze EPIC — Storage as a first-class entity (multi-storage) — captured 2026-07-22, (cl)

**Not a rung — an epic (several rungs), deliberately post-freeze, recorded so the direction lives
in the docs, not only the Operator's head.** The Operator's core insight is *correct and it names a
real modeling error*: **a storage backend (`zfs`/`reflink`/`hardlink`/`copy`) is a property of a
STORAGE, not of a backup.** Today quince has exactly one `/backups`, one backend auto-probed
globally, and `Version.backend` (contracts §2) recorded per-version — that last field is the
*symptom*: a version's backend is really its storage's backend. The v1 single-storage model was a
reasonable simplification; it is "kind of wrong" as the long-term shape. This is how mature tools
(Immich external libraries, Plex) model it.

**The target model (Operator, architect-endorsed):**
- **Storage is a first-class entity** — created (first one during onboarding, Plex-style), shown on
  the dashboard with stats (backup count, space used / free), managed in the UI.
- **One immutable backend per storage**, selected + probed **at creation**. Backend never changes
  in place; a future **migration** = *create a new storage from an existing one*.
- **A device backs up to multiple storages** (the 3-2-1 rule a backup tool should embody: local
  fast + offsite/removable).
- **Incremental is scoped to (device, storage)** — a delta can only be taken against the previous
  backup *on that storage*. So the `latest/`/`working/` lifecycle (which qn.5b reworks) becomes
  per-(device, storage), and **the first backup to any NEW storage is always a full transfer** even
  for a long-backed-up device (surface this honestly). "full vs incremental" is thus explicitly
  per-storage.
- **A storage can be OFFLINE** (removable HDD plugged occasionally; a network share that's down) —
  shown, not errored; it must not block backups to *other* storages.

**Architect challenges + refinements (the Operator asked to be challenged):**
1. **Storage identity must be written INTO the storage** — a `quince-storage.json` (UUID + backend +
   created-at) at its root, the analog of `quince-version.json`. A removable HDD's *path* changes on
   replug; only an embedded UUID lets quince know "this is the same storage" and sanity-check it.
2. **Reframe the "pre-backup probe"** the Operator was unsure about: backend is *selected* at
   creation (immutable), but **reachability + still-the-expected-backend is checked before each
   backup** — that check *is* the offline-detection and the "did this dataset get remounted as
   something else" guard. Selection at creation; health-check before use. Both, not either.
3. **Offsite/B2 is a REPLICATION of a storage, not a storage** (my lean, flagged as a real open
   question). The D5a rclone→B2 model syncs a storage's `latest/` offsite; B2 isn't a place quince
   *commits versions to*, it's a mirror. Folding B2 into the storage abstraction vs keeping
   replication separate is a genuine design fork to settle when this is scoped.
4. **Split the iMazing case into TWO features** the Operator conflated: **(a) external read-only
   storage** — mount a foreign backup (iMazing/iTunes/`ios-backup-crypt` output) `:ro` and
   browse/restore *in place*, no copy (Immich's "external library"); **(b) import/migration** — copy
   a foreign backup INTO a new quince-managed storage. (a) is lighter and lands the "connect iMazing
   over the network `:ro`" use case cleanly — and it's a natural fit for the **sibling libraries**
   (`ios-backup-crypt` + `ios-backup-parser` read *any* standard backup, not only quince-committed
   ones), so external-readonly storages are feasible precisely because those libraries exist.
5. **Offline-storage policy: don't queue unattended backups** (queuing fights the assisted model,
   D13). A backup targets a storage (or a set, skipping offline ones with a clear report); an
   offline target is an honest "can't right now," not a background retry.
6. **Consider a storage `mode`** (`managed` | `external-readonly`) rather than treating every foreign
   source as a migration — external-readonly is a first-class *mode*, not a one-time import.
7. **Continuous reconciliation is BLOCKED on this epic's offline distinction ((cr)(b), ruled (cv)).**
   Today disk↔index reconciliation runs at startup only, so an artifact vanishing under a live
   daemon goes unnoticed until restart (proven on hardware: snapshots destroyed under a running
   quince). The fix everyone reaches for — a periodic sweep or revalidate-on-read — **must not be
   built before "storage unreachable" and "artifact gone" are distinguishable states**: a sweep that
   cannot tell them apart would mark every version on an unplugged removable HDD `missing`, which is
   exactly wrong. Sequencing: this epic lands the storage-health model first; the background sweep
   is a rung *inside* it, not a near-term patch.

**Interaction with near-term work:** qn.5b's atomic-`latest` + per-job-`working/` mechanics are the
SAME within a storage's device-tree whether there is one storage or many — only the path prefix
changes (`/backups/<udid>/…` → `<storage>/<udid>/…`). **So qn.5b is safe to build now**, provided it
does not hard-bake single-storage assumptions that are costly to unwind (paths should be
storage-scopeable; `Device.last_backup` derivation should tolerate becoming per-storage later).
Scope this epic into rungs *after* the freeze, under the revamped process — it is exactly the kind of
large, contract-touching, multi-surface work the revamp should make smoother.

## Parallelization map (multi-agent)

Independent tracks after M1 freezes the contracts (`contracts.md`):

| Track | Owns | Rungs |
| --- | --- | --- |
| **core** | daemon, muxd, device ops | qn.2, qn.2b, qn.3 |
| **backup** | job engine, supervisor, backends, wifi hardening | qn.5, qn.4a, qn.4b, qn.7 (in that order — (ar)/(be)) |
| **vault** | Python: decryption, lazy domain adapters, fixtures, conformance suite | qn.8 groundwork (fixture generator, RPC harness, conformance goldens) can start right after M0 against contracts + real transcripts |
| **ui** | React app, design system, demo polish | UI halves of qn.1/qn.3/qn.4 against `--demo` fixtures |
| **infra** | CI, images, release, deploy docs | qn.0 hardening, qn.6 pipeline |

Rules: a track never edits another track's tree except via a contract-change rung;
boundaries and gates per track are in the program doc. Sequential spine: qn.0 → qn.1 →
(then tracks fan out) → qn.6 integrates.
