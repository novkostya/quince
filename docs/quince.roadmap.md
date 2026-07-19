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
  fake muxer). Then the rung **runs qn.2's deferred lab gates 6–7 as its own acceptance**.
  *Gate: `compose up` on the lab CT brings USB up with NO host muxer (the D12 Plex-bar
  promise restored); plug/unplug ≤1 s in the UI; the netmuxd-USB audition verdict recorded
  (D2 default flips to single-muxer, or the upstream issue is filed with the exact line).*
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

### M3 — Backup engine, both transports (`qn.5` storage FIRST, then `qn.4` engine)

Order ruled 2026-07-20 (decisions log (ar), the "rung closes provable" hard rule): the
engine's `succeeded` requires `Commit()`, which is storage's — so storage lands first and
is proven on fixture trees + manually-produced backups; the engine then closes M3 with
the true end-to-end gate. Rung numbers are labels, not order (the qn.7-before-qn.6
precedent).

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
- `qn.4` job state machine + `idevicebackup2` supervisor, **USB and Wi-Fi first-class
  from the start** (stack D13 — Wi-Fi is the primary use case, assisted model): stdout
  parser (fixtures = real lab transcripts incl. stalls, `-4` failures, and the
  `waiting_for_passcode` phase), activity-sampler liveness with staged stall states, NO
  auto-retry (failed → `user action required`, manual retry with `retry_of`), cancel via
  process group, crash-safe persistence, per-UDID lock, structural verification,
  `repair-working-copy` reserved in CLI semantics, job history API/UI (raw but live,
  grouped by intent — contracts §2). Also ships the **headless
  CLI** (`quince device list / backup start / versions list / versions verify /
  versions path --latest` — the last one prints the consistent offsite-sync source: the
  `latest/` mirror on zfs, the resolved version dir elsewhere) — the lab-testing and
  scripting interface, and the fastest way to
  torture the engine before the UI exists (external-review point, accepted in-place
  rather than as a CLI-first roadmap). *Gate (the integrated e2e, closing M3): encrypted
  backups over BOTH transports on the lab box, driven from the UI/CLI, end `succeeded`
  with committed verified versions on a real backend; the engine-level kill matrix
  (kill at seed / backing_up / verify / commit hand-off) recovers to defined states on
  restart; iMazing opens each committed version.*

### M4 — Wi-Fi reliability hardening (`qn.7`)
The flakiness-absorption rung, BEFORE the public release because Wi-Fi is primary:
patched-timeout libimobiledevice source build in the image (30 s → 15 min, upstream
#1413), netmuxd supervision + restart policy, chaos suite (replay every torn-session
transcript + injected mid-file disconnects), liveness-stage thresholds tuned against the
real lab box, honest UX copy for the slow/silent/passcode phases. *Gate: injected
disconnects land in clean `user action required` states with committed versions
untouched, and a manual retry from dirty `working/` completes and verifies.*

### M5 — v0.1 public shape (`qn.6`)
Devices + Backup button (both transports) + live progress + history + version list, UI
polished to the design canon; full first-run onboarding (guided checks per design §9 —
the Plex bar); Settings as a transparent editor over `config.yml` (D12 staging
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
| **backup** | job engine, supervisor, backends, wifi hardening | qn.5, qn.4, qn.7 (in that order — (ar)) |
| **vault** | Python: decryption, lazy domain adapters, fixtures, conformance suite | qn.8 groundwork (fixture generator, RPC harness, conformance goldens) can start right after M0 against contracts + real transcripts |
| **ui** | React app, design system, demo polish | UI halves of qn.1/qn.3/qn.4 against `--demo` fixtures |
| **infra** | CI, images, release, deploy docs | qn.0 hardening, qn.6 pipeline |

Rules: a track never edits another track's tree except via a contract-change rung;
boundaries and gates per track are in the program doc. Sequential spine: qn.0 → qn.1 →
(then tracks fan out) → qn.6 integrates.
