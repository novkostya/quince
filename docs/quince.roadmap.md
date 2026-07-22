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

### ⚑ `qn.5b` — atomic `latest` + the `working/` lifecycle redesign (inserted 2026-07-22, (cg))

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
  Worse, during a `receive -F` the destination is rolled back and applied progressively, and is
  typically unmounted for the operation — so the **microsecond** window where `latest/` is
  missing becomes a **minutes-long** one. An rclone cron wouldn't "eventually" lose that race;
  it would reliably hit it.
- **The principle (why no dataset-level operation can work here):** the requirement is that a
  *filesystem path stays continuously valid for a walker*. Every dataset-level operation —
  send, receive, rename, promote — involves a **mount transition**, so none can satisfy it.
  Directory-level `renameat2(RENAME_EXCHANGE)` keeps the mount stable and flips the entry in one
  syscall with no observable gap. Right tool for the actual constraint.
- **Where send/receive IS right: replication.** It is already the offsite path (`syncoid` to the
  remote PVE host, proven at gate 11). It moves datasets *between pools*; it cannot swap a
  locally-visible directory without taking that directory offline. Right tool, wrong job.

*Gate: `RENAME_EXCHANGE` verified on the real pool; a snapshot taken at **any** point of a
running backup contains a complete `latest/` and never a partial one; a continuous `rclone
sync` loop running across many commits never deletes or tears the remote copy; a failed backup
leaves a resumable `working/` and a retry completes without re-transferring; between backups
the dataset holds only `latest/`.*

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
real lab box, honest UX copy for the slow/silent/passcode phases. *Gate: injected
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
Photos viewer (qn.11 — see M7 note: Apple-prebuilt-thumbnails spike first); restore &
Finder-compatible export; **offsite-sync ergonomics** (post-commit hook firing with the
committed version's path — lets rclone→B2 run push-style after every good backup; maybe
built-in rclone scheduling one day — until then the documented pattern is a cron line
over `quince versions path --latest`); more domains (WhatsApp, contacts, call history —
contacts/calls are easy wins, may pull forward); Prometheus metrics; Docker Hub mirror;
multi-device polish; public demo instance.

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
