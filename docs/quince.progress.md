# quince — progress dashboard

**One-line state.** ⚑ **FRONTIER = `qn.4c` — the DAILY-DRIVER target ((by)).** The Operator's
bar before a planned **code freeze + process revamp**: a full backup cycle over BOTH transports,
live progress without a page refresh, major bugs fixed. That is **one session (qn.4c: netmuxd
co-supervision + qn.4a findings (i)/(iv)/(v)) + one hardware day (the inherited qn.4b gate 11)**.
qn.4a and qn.4b are done/closed on CI, with qn.4a's engine goal hardware-proven on BOTH backends;
**gate 12c and all of qn.6–qn.12 are deferred past the freeze.** History below.

**qn.1 is BUILT — the app frame stands.** `make gates` (go + vault +
ui), `make gates-ui-e2e` (Playwright stories 1–2), and `make image` are green inside
`quince-dev`. The daemon now has typed config over `config.yml`, SQLite + migrations,
cookie auth with a first-run set-password flow, the event bus, the `/api/ws` socket, the
web-security baseline (CSRF, WS Origin, cookie flags, rate limit, audit), and a `--demo`
mode that scripts fixture devices + a job exercising every WS event; the UI ships the auth
flow, a WS bridge feeding Zustand stores, and the Dashboard / device-details / Settings
pages bound to live demo data. A post-build review of qn.0+qn.1 (see decisions log
`qn1-review`) landed the top minors (no blocker/major). **qn.2 is BUILT + CLOSED** — the
`internal/muxd` plist protocol client + the `internal/device` registry (merge N muxers →
per-transport, per-source table keyed by UDID; reset-on-reconnect reconcile clears
detached-while-away phantoms; `device.*` events), wired into non-demo `quince serve` as the
live `DeviceReader`; full `make gates` + `make image` + `make gates-ui-e2e` green. **CI
stories 1–5 done; lab gates 6–7 (plug/unplug ≤1 s, netmuxd-USB audition) DEFERRED** — the
muxer-startup gap has since been RULED (decisions log (ar)): supervision + rescan + those
lab gates all land in **`qn.2b`**. **qn.2b is now BUILT (CI)** — the `internal/muxsup` muxer
supervisor (spawns the in-container usbmuxd `-f -S <socket>` in its own process group,
restart-w/-backoff, refuse-loudly on an already-served socket, crash-loop → `/api/health`
degraded), `POST /api/devices/rescan → 202|409` reusing the muxd reconnect→Reset→replay
reconcile, the `devices.manage_muxer` config key, and a UI **Rescan** control; `make gates` +
`make image` + `make gates-ui-e2e` green, and the supervisor was smoke-tested against the
**real** usbmuxd in the built image (`/api/health` → `muxer:{managed,state:"running"}`). **qn.2b
is now DONE** — **lab gate 7 (managed USB + Rescan) PASSED on real hardware** (Operator-confirmed on
staging; it surfaced + fixed a "live `/dev/bus/usb`" deploy-config gap, (av)); gate 8 (netmuxd-USB
audition) was **re-homed to qn.7** with a named owner (not a silent defer, (aw)). **qn.3 is BUILT +
CLOSED** — `internal/deviceops` (pair/validate/info wrappers + backup-encryption management over a **pty**,
never argv/env) + registry lockdown enrichment + the four frozen device-op endpoints + the `Op` lifecycle
+ pairing-record persistence + UI pair/encryption dialogs; full `make gates`/image/e2e green (spec-approved
with the architect's three amendments + two Operator acks). **Lab gate 8 PASSED on real hardware
(2026-07-20)** — fresh container → pair (UI) → recreate-still-paired (amendment 1) → change_password +
disable→enable, secrets proven absent from argv/env/log; four findings caught + fixed + CI-validated
(incl. a real enrichment auto-pair-on-locked-device bug). **qn.5 is DONE (CI-proven; landed
`285c40b`..`3ce5bb1`)** — the version store: `internal/storage` (4 backends + auto-probe +
journaled commit + `quince-version.json` markers + startup-reconciliation kill-matrix + adopted
discovery + encryption-branched structural `Verify` + `RepairWorkingCopy` + retention + the
(bi)/(bk) **mirror ladder**) + `clonetree` + a `versions` registry (the real `VersionReader`) +
`DELETE /api/versions/{id}` + `version.*` events + reconcile-before-serve + `deploy/storage.md`;
full `make gates`/image/e2e green ((bd), (bl)). A five-round mirror investigation ((bf)→(bk))
proved block cloning works at the POOL level but EPERMs in the unprivileged userns — the mirror
ladder clones from `working/` (never `.zfs`) via a host-side hook `mirror` verb / in-container
reflink / hardlink / copy. **Lab gate 12's remaining hardware legs (host-side mirror verb,
iMazing, syncoid, 12c destructive matrix) RE-HOMED to qn.4a** ((bm) — named owner, not a silent
defer). **qn.4a is now BUILT (CI-proven)** — the `internal/backup` job engine drives `idevicebackup2`
through the state machine into qn.5 storage (per-UDID single-flight, streaming supervisor with the
`<target>/<UDID>` symlink adapter, transcript-grounded parser, activity-sampler liveness + A3
disk-low, startup job reconciliation), the `jobs` store + command surface (`POST /api/jobs`, cancel,
`job.*`), and the `quince backup` CLI; `make gates`/image/e2e green, CI stories 1–14 (incl.
wifi-torn→`connection_lost`, verify-gate→`failed`, single-flight→409). **Lab gate 15 (real
encrypted USB backup e2e + kill-matrix + the re-homed gate-12 legs) is the remaining hardware step,
owned by qn.4a** ((bp)). **qn.4b is now BUILT (CI-proven)** — transport **`auto` resolution**
(`StartBackup` resolves against current presence — prefer USB when plugged, else Wi-Fi — stores the
CONCRETE transport on the `Job`, never `"auto"`; a device on neither transport → actionable **422**,
no job minted, design §4/(bp)), the **`quince versions verify`** + **`device repair-working-copy`**
CLI escape hatches (thin `buildStorage` + `storage.VerifyVersion`/`VerifyLatest`, browseRoot-resolved,
no new backend surface), the **live demo `JobControl`** (scripts on-demand jobs + a seeded failed job
for the retry affordance; single-flight shared with the ambient loop — reversing qn.4a's 503), and
the **UI** (live "Back up now" w/ transport override, one-tap **Retry** on failed intent groups,
**Cancel** on the running job — details page + dashboard card); `make gates`/image/e2e green, e2e
**story 4** (Back up now → cancel → retry) + the qn.4a Wi-Fi-success coverage finding retired (a
`wifi-incremental-success` story). **Lab gate 11 (both-transports UI-driven backup + honest Wi-Fi
disconnect) + gate 12c (destructive hardlink-safety matrix) — the consolidated hardware day with
qn.4a's gate 15 — remain the hardware step**, owned by qn.4b. Frontier is **qn.4b** until the
hardware day; **M3 closes then.**

| Rung | Title | State |
| --- | --- | --- |
| qn.0 | Floor: scaffold, gates, CI, image | **done** — gates + image green in quince-dev (2026-07-19) |
| qn.1 | Core daemon skeleton + demo mode + UI shell | **done** — full gates + e2e + image green in quince-dev (2026-07-19) |
| qn.2 | muxd client + live device table | **done** — muxd client + registry + UI; `make gates`/image/e2e green (2026-07-20); lab gates 6–7 → owned by qn.2b |
| qn.2b | Muxer lifecycle + hardware proof (supervision, rescan, lab gate 7) | **done** — `internal/muxsup` supervisor + `POST /api/devices/rescan` + `devices.manage_muxer` + `/api/health` muxer + UI Rescan; `make gates`/image/e2e green + real-usbmuxd smoke test (2026-07-20); **lab gate 7 (managed USB + Rescan) PASSED on hardware**; gate 8 (netmuxd-USB audition) re-homed to qn.7 (aw) |
| qn.3 | Device ops + Devices page | **done** — `internal/deviceops` (pair/validate/`ideviceinfo` + encryption via **pty**, never argv/env) + registry `Enrich` + enrichment driver + 4 frozen endpoints + `Op` lifecycle + audit + **pairing-record persistence** (amendment 1) + UI pair/encryption dialogs; `make gates`/image/e2e green (e2e story 3); coverage deviceops 80.2%, device 97.6%, httpapi 71.8%. **Lab gate 8 PASSED on hardware (2026-07-20)** — fresh container → **pair** (via UI, record persisted) → **recreate → still paired** (amendment 1 proven twice) → **change_password + disable→enable** cycle, all succeeding; **secrets proven** (`idevicebackup2 -i … {changepw,encryption off,encryption on}` — no password in argv, `BACKUP_PASSWORD` env count 0, clean logs). **4 findings fixed + CI-validated** (enrichment auto-pair on locked device; 3 UI) |
| qn.5 | Storage backends (zfs snapshot-native / reflink / hardlink / copy) + reconciliation | **done (CI-proven; landed `285c40b`..`3ce5bb1`)** — `internal/storage` (4 backends + auto-probe + journaled commit + `quince-version.json` markers + startup-reconciliation kill-matrix + adopted-version discovery + structural `Verify` (encryption-branched, A1) + `RepairWorkingCopy` + retention + the (bi)/(bk) **mirror ladder**: clone-from-`working/`, hook `mirror` verb → in-container reflink → hardlink-under-matrix → copy, surfaced/UNVERIFIED reporting) + `clonetree` (FICLONE/hardlink/copy) + `versions` registry + `DELETE /api/versions/{id}` + `version.*` events + reconcile-before-serve + `deploy/storage.md`; `make gates`/image/e2e green. **Proven in CI** (11 stories + reconciliation matrix + D5a anchored-filter contract) + **real-zfs commit/Verify on hardware** during the gate-12 investigation ((bf)→(bk)). **Lab gate 12's remaining hardware legs (host-side `mirror` verb, iMazing, syncoid, 12c destructive matrix) RE-HOMED to qn.4a** ((bm); named owner, legs preserved in the qn.5 spec). Ran BEFORE qn.4 (order ruled (ar)) |
| qn.4a | Backup engine + supervisor + minimal CLI (USB gate) | **built + landed (CI); gate 15 hardware-proven — ENGINE legs (bs) + zfs half (bw); only iMazing-opens (Operator GUI) left** — `internal/backup` (state-machine engine + per-UDID single-flight + `idevicebackup2` streaming supervisor w/ the `<target>/<UDID>` **symlink adapter** + transcript-grounded parser + activity-sampler liveness w/ **A3** free-space watch + preflight + Seed→Verify→Commit/Discard + **startup job-row reconciliation**) + a `jobs` table/registry (real `JobReader`) + the job command surface (`POST /api/jobs` 202/409/422, `POST …/cancel`, `job.*` events) + the `quince backup` CLI (shared `buildLiveStack`); 6 lab transcripts extracted+scrubbed. `make gates`/image/e2e green; CI stories 1–14 incl. **wifi-torn→`connection_lost`** (a stall, not an error — sampler catches it), **verify-gate→`failed`**, **single-flight→409**, **startup-reconcile→`connection_lost`/rolled-forward-`succeeded`**. Coverage backup **83.2%** / store 80.8% / httpapi 72.2%. **Gate 15 split (clarified (bv)):** the ENGINE legs PASSED on real hardware (iPad, hardlink `/backups`) — CLI-USB backup both encryption variants (A1 encrypted `Verify` on real data), version rotation, interface facts 1+5, kill-matrix `backing_up`. The **zfs half is PROVEN ((bw))**: **engine→commit on the real zfs-hook backend** (encrypted, verified, version snapshot cut), host **`mirror` verb** + **`bclonesaved`** moving live (+~3 GB), **syncoid** mid-write (both `@quince-*` restore points + dirty `working/` replicated offsite) — the constrained forced-command hook key + `rbind,rslave` host→LXC→container propagation stood up on the real rpool; three deploy-doc hook bugs found+fixed (`$2`→last-arg, image-ssh-client, create-chown). Only **iMazing-opens** (Operator GUI) is unverified. **Landed on main.** |
| qn.4b | Wi-Fi first-class + transport policy + job-history UI (closes M3) | **built (CI-proven); lab gate 11/12c (hardware) pending** — transport **`auto` resolution** (prefer-USB-when-plugged, absent→**422** no job, concrete transport stored) + httpapi passes `auto` through; **`quince versions verify <id>\|--udid`** + **`device repair-working-copy <udid>`** CLI escape hatches (`storage.VerifyVersion`/`VerifyLatest`, browseRoot-resolved, no new backend surface); **live demo `JobControl`** (on-demand scripted jobs + seeded failed job for retry; single-flight; reverses qn.4a's 503); **UI** live Back up now (auto + transport override) / one-tap Retry on failed intent groups / Cancel on running job (details page + dashboard card). `make gates`/image/e2e green (e2e **story 4**: Back up now → cancel → retry). Retired the qn.4a Wi-Fi-success coverage finding (`wifi-incremental-success` story). Coverage backup **83.4%** / demo **55.3%** (was 0) / storage **78.2%** / httpapi 72.2% / cmd/quince 8.5% (CLI wiring hw-exercised). NOT a Wi-Fi demotion ((h) stands). **Lab gate 11 (both-transports UI-driven + honest Wi-Fi disconnect) + 12c (destructive hardlink matrix) = the consolidated hardware day with qn.4a gate 15**. **CLOSED (CI) 2026-07-20 ((by)):** its CI half is landed and complete; **gate 11 is RE-HOMED to `qn.4c`** (named owner — its Wi-Fi leg should run on SUPERVISED netmuxd, the shape actually deployed, not a hand-started one), **gate 12c is DEFERRED past the code freeze** (the destructive hardlink matrix gates a backend the Operator doesn't run — zfs deployment; the hardlink tier stays disabled-to-copy, surfaced), and findings (i)/(iv)/(v) **move to qn.4c**. No session work remains here. |
| qn.4c | **netmuxd supervision + usability fixes (the DAILY-DRIVER target)** | **frontier** — inserted 2026-07-20 ((by)) to reach the Operator's "personally usable" bar before a planned code freeze. Scope: generalize the hardware-proven `internal/muxsup` to **co-supervise netmuxd** (config-gated, TCP probe vs its unix-socket one, restart-with-backoff, health surfaced — without it nothing starts netmuxd on `compose up`, so Wi-Fi dies silently after any restart: the qn.2b-for-usbmuxd reason, pulled forward from qn.7) + fix qn.4a findings **(i)** `willEncrypt`→`unknown` mis-map + the cold-lockdown race that hard-fails a legitimate encrypted backup at preflight, **(v)** the engine never writing `device.last_backup` (→ "No backups yet" on a device with real versions), **(iv)** the card lingering at "Backing up 100%" (likely subsumed by (v)). **Inherits qn.4b gate 11** — both transports UI-driven, live progress observed on a real backup, Wi-Fi over SUPERVISED netmuxd surviving a container restart, + the iMazing glance. Gate 12c stays deferred past the freeze. |
| qn.6 | v0.1 release shape (after qn.7) | outlined |
| qn.7 | Wi-Fi reliability hardening (before v0.1) + **the netmuxd-USB audition (re-homed from qn.2b, (aw))** | outlined — **netmuxd co-supervision MOVED to qn.4c** ((by)); qn.7 keeps the patched-timeout libimobiledevice build, restart-policy tuning, the chaos suite, liveness thresholds, and the audition. Deferred past the code freeze |
| qn.8 | Vault: unlock, lazy browse, conformance suite | outlined |
| qn.9–10 | Domain viewers (overview / messages) | outlined |
| qn.11 | Photos viewer | **parked, lowest priority** (icloudpd+Immich cover photos; Apple-thumbnails spike first if revived) |
| qn.12 | PWA + push + schedules | outlined |

**Open questions for the Operator** (tracked here until resolved):
1. LAN registry port + creds (address recorded in `local/environment.md`; env-only,
   never committed).
2. ~~Who starts the muxer in the SIMPLE profile?~~ **RESOLVED 2026-07-20** — ruled
   option (a): quince-supervised in-container muxer behind `devices.manage_muxer`
   (refuse-loudly on an already-served socket) + `POST /api/devices/rescan`; landed as
   rung **qn.2b** together with qn.2's deferred lab gates. Full ruling: decisions log
   (ar); contracts §1/§6 + design §2 updated; the design capture stays in the qn.2 spec
   appendix.

*Resolved:* **project name = quince** (Operator, 2026-07-18, after due diligence — see
decisions log (y); repo `github.com/novkostya/quince`, images
`ghcr.io/novkostya/quince`, binaries `quince` / `quince-vault`, rung prefix `qn.`).
License = MIT. `@mercury-fx/ui` = not consumed; mainstream vendored-component stack
instead (decisions log (u)). GitHub owner = `github.com/novkostya` (org transfer only
on real traction).

**Decisions log.** *(Newest entries append at the bottom.)*
- 2026-07-18: full planning pass (this docs set) from the feasibility lab
  (`../local/chatgpt-original-idea-chat.md`); Go core + Python vault + React/mercury-style UI;
  USB primary / Wi-Fi experimental; ZFS first-class with hardlink portable fallback.
- 2026-07-18 (Operator review): (a) vault seam made explicitly swappable — a future
  all-Go vault is a drop-in behind `vault.Vault` + the conformance suite; (b) host
  auto-snapshot tooling rejected — quince relies only on snapshots it creates; (c) the
  never-mutate-latest layout (`versions/` + `latest` + `work/`) adopted — dataset is
  crash/replication-consistent at any instant (sanoid/syncoid-safe), rollback machinery
  deleted; (d) persistent backup-content indexing rejected in favor of lazy
  session-scoped reads; sole exception = fingerprint-validated derived caches
  (thumbnails, qn.11). Side effect of (d): no secrets at rest in v1.
- 2026-07-18 (Operator review 2): (e) photos parked at lowest priority — Operator's photo
  pipeline is icloudpd + Immich; if revived, spike Apple's prebuilt in-backup thumbnails
  (`Media/PhotoData/Thumbnails`) before any generation/cache machinery — likely moots the
  derived-cache exception entirely; (f) operations UX fixed as a core value (stack D12):
  Plex-grade setup (compose up → onboard in UI, everything configurable in-app) with
  OpenWrt/PVE-grade config — one tidy hand-editable `config.yml` as source of truth,
  atomic validated writes, no secrets in it, UI is an editor over the file.
- 2026-07-18 (external crosscheck review, `../local/chatgpt-planning-crosscheck-feedback.md`,
  adjudicated with the Operator): **Operator rulings** — (g) zfs backend is
  snapshot-native (in-place `current/`, versions = quince's own snapshots, no hardlinks
  under ZFS; consistency guarantee restated per-backend: on zfs it lives in the
  snapshots, the head is a working buffer); (h) Wi-Fi is the PRIMARY use case —
  first-class transport from qn.4, hardening rung (qn.7) moved BEFORE v0.1, experimental
  flag removed (rejects the crosscheck's Wi-Fi demotion). **Crosscheck adopted** —
  journaled commit + first-class startup reconciliation with on-disk
  `quince-version.json` markers; two-level verification (structural at commit, content
  canary at next unlock); vault RPC hardening (framed `initialize`, `materialize` with
  opaque handles — no paths cross the boundary, scratch-jailed vault); web security
  baseline pulled into qn.1 + audit trail + tmpfs scratch honesty; hardened deployment
  profile (muxd split) as a qn.6 compose example; domain APIs envelope-frozen only,
  fields after research spikes; D12 config staged (core in qn.1, live-reload/comments in
  qn.6); headless CLI added to qn.4; destructive hardlink-safety matrix replaces the
  single-file inode check. **Crosscheck rejected** — per-version/clone ZFS datasets
  (don't propagate into container bind mounts; fragile hook chains), CLI-first roadmap
  restructure (parallel tracks already decouple UI; CLI lands inside qn.4), Wi-Fi
  demotion (see h).
- 2026-07-18 (Operator clarification, second pass): the offsite model is **whole-tree
  file-level sync** — one rclone job over the entire storage parent (e.g.
  `/rpool/userdata`), walking live mounts; per-dataset `.zfs` paths don't fit it. Design
  restated as D5a: each zfs device dataset holds `current/` (in-place working copy,
  excluded by one static rclone filter) + `latest/` (verified mirror rebuilt at commit —
  reflink clone preferred, probed fallbacks hardlink/copy — atomic swap); flow =
  `zfs snapshot -r && rclone sync /rpool/userdata b2:…`, remote history via B2
  versioning/`--backup-dir`. **Operator ruling: one child dataset per device**
  (independent snapshot streams; snapshot list = version list), so the constrained hook
  gains `zfs create` scoped to children of the parent; dataset destroy stays
  human-only. PVE bind-mount propagation gotcha (new child = empty stub in a running
  LXC) handled by probe + printed `pct set` instructions; Docker via `:rshared`;
  single-dataset fallback mode documented.
- 2026-07-18 (Operator Q&A, third pass): (k) PVE propagation — recommended mount is a
  raw `lxc.mount.entry … rbind,rslave` (+ `propagation: rslave` on the nested OCI bind),
  making new child datasets appear live without restart; probe verifies, `pct set`
  instructions remain the fallback; (l) FICLONE works through container bind mounts
  (syscall reaches the real fs) — cloning implemented in-process in Go, so busybox `cp`
  is irrelevant; host OpenZFS must have block cloning (2.2+, probed); (m) **`reflink`
  promoted to a first-class backend and the auto-default** wherever the FICLONE probe
  passes (Btrfs/Synology, XFS, hookless ZFS) — `zfs` backend selected only on explicit
  config intent (`storage.zfs.*`), per the Operator's proposal; hardlink-safety matrix
  now applies only where hardlinks are actually used.
- 2026-07-18 (crosscheck v2 adjudication + Operator's passcode correction): **the
  product model is ASSISTED backup** — Operator established that modern iOS demands
  on-device passcode entry for every backup, so unattended backups are impossible;
  auto-retry ladder deleted (failed → `user action required` + one-tap manual retry
  with `retry_of`; run/attempt grouping thereby unnecessary); Shortcut becomes a dumb
  opportunity signal with ALL policy server-side (`/api/automation/backup-opportunity`,
  staleness + cooldown config); v0.1 gate rewritten to a week of real UI-driven Wi-Fi
  backups, qn.12 gate = the assisted acceptance list. Crosscheck v2 refinements
  adopted: zfs `latest/` built from the snapshot's `.zfs` path (snapshot = canonical
  version, latest = materialized view; FICLONE-from-snapshot probed with lock-guarded
  fallback); "self-heals" softened to candidate-plus-verification with
  `repair-working-copy` escape hatch; liveness = activity sampler with staged states
  (`active → silent_but_connected → suspected_stall`) + `waiting_for_passcode` pause;
  **`latest/` is a real directory on all backends, never a symlink** (namespace commit
  = journaled rotation, offsite filter excludes `versions/` too); roll-forward
  principle — post-verify artifacts are never destroyed by recovery, reconciliation
  completes commits instead of unwinding them.
- 2026-07-18 (crosscheck v3 + Operator): (p) **Intent model adopted lightweight** —
  `intent_id` (retry-chain root) + `attempt` on Job; UI groups history by intent
  ("Backup completed after 1 retry"); full server-side Intent entity parked as future
  evolution (Operator liked the concept; ChatGPT itself rated it non-essential for v1).
  (q) **`current/` renamed `working/`** (Operator ruling: names must be readable
  without context — `working`/`latest` self-explains, `current`/`latest` doesn't).
  While renaming, the offsite filter examples were fixed to **anchored** rules — an
  unanchored `**/working/**` exclude would silently drop same-named dirs inside backup
  content (corrupted offsite copy, no error); deploy docs must ship the exact anchored
  filter block.
- 2026-07-18 (Operator concern → process + first gap): (r) **the gap protocol** —
  CLAUDE.md's "everything is decided" softened to canon-so-far; the program doc now
  defines what an agent does at a gap: rung-local → decide in-spec + log (*rung-ruled*);
  architectural → `PROPOSED (gap)` block in the canon doc + open question + STOP for
  Operator ruling; silent deviation and silent doc-vs-reality "fixes" forbidden.
  (s) **first gap processed — backup-encryption management** (Operator-spotted):
  `Device.backup_encryption` from `WillEncrypt`; `POST /api/devices/{udid}/encryption`
  (enable/change_password/disable; passwords via pty or `BACKUP_PASSWORD` env, argv
  forbidden; on-device passcode step narrated); `backup.require_encryption: true`
  policy enforced actionably at preflight; unencrypted devices get a persistent warning
  (no Health/Keychain/passwords) and unencrypted versions carry `encrypted: false`
  badges; one-password-two-uses documented (device backup password == vault unlock
  password; quince sets it, never stores it). Landed in qn.3 scope.
- 2026-07-18 (Operator rulings, product/UX round): (t) **device-centric IA** — one
  primary area (`Devices` + `Settings`); home = Devices dashboard (device cards,
  `Back up now`, inline job progress, N most recent backups across devices — composed
  to look alive for small fleets); backups live inside their device's details page;
  phone-first entry point (PWA opened from a backed-up device lands on that device)
  parked for qn.12. (u) **frontend stack finalized** (revision of D7): Tailwind v4 +
  vendored shadcn-style components on Radix + Zustand + TanStack Query/Virtual; Effector
  dropped and `@mercury-fx/ui` not consumed (Operator wants maximally mainstream,
  maintainable, lightweight, LLM-fluent; mercury stays a taste reference). (v) license
  = MIT. (w) GitHub owner = Operator's personal account (org transfer only on real
  traction); handle pending — later confirmed as `novkostya`. (x) the original codename
  `compote` ruled out as the production name — naming brainstorm opened.
- 2026-07-18 (naming, final): (y) **the project is named `quince`.** Vetted: GitHub
  exact-name sweep (nothing above 31★; runner-ups sunduk/coffret/cargohold recorded in
  chat), npm/PyPI hits are dead micro-packages, Docker Hub clear, no dev-tool product
  conflict (QuinCe the oceanography QC tool is a distinct stylization in a distant
  field; the Quince fashion brand is retail-class — negligible confusion for a free
  self-hosted tool; re-check trademarks properly before any commercialization). All
  docs, rung IDs (`cp.` → `qn.`), env prefixes (`QUINCE_`), snapshot names
  (`@quince-*`), and marker files renamed from the `compote` codename this day.
- 2026-07-18 (post-rename completeness audit, Operator-requested): (z) full doc sweep
  against the conversation's decision history. Fixed: a stale D3 paragraph still
  describing the deleted auto-retry backoff ladder (contradicted D13; replaced with
  assisted-model wording); `reflink` missing from the `Version.backend` enum and two
  "hardlink/copy"-only phrasings; a leftover pre-reflink auto-probe sentence in design
  §5; qn.1 roadmap wrongly including file-watch (staged to qn.6 per D12); lab
  deployment note updated to the `rbind,rslave` recommendation; `dirty-current` →
  `dirty-working` leftovers; stale module-path rename note in qn.0. Gap closed: pair/
  encryption ops returned `op_id` with no way to observe them — added `Op` object,
  `GET /api/ops/{op_id}`, and `op.updated` WS event (the "tap Trust"/"enter passcode"
  narration channel). All other rulings verified present and correctly stated.
- 2026-07-19: (aa) repo root = `~/iphone-backup-app` as-is (git init in place, qn.0);
  the `chatgpt-*.md` planning transcripts and the generated `quince-planning-pack.md`
  stay on disk but are **gitignored** — private lab material never enters the public
  repo; committed transcript fixtures are the durable extract.
- 2026-07-19: (ab) device scope widened in wording (Operator): iPhone AND iPad are
  first-class (same pairing/MobileBackup2 protocol, no extra code); Vision Pro
  untested/unpromised (visionOS may be iCloud-only); Apple Watch out of scope (no
  backup protocol). No iPhone-string-specific code allowed.
- 2026-07-19: (ac) **dev environment ruled** (Operator, after the first qn.0 session
  correctly stopped at the undocumented gap): the driving workstation is a thin client —
  no toolchains or container runtime on it, ever; all gates/builds/pushes run in a
  dedicated `quince-dev` LXC on the Operator's local PVE host (same LAN as the
  test iPhone and the LAN registry); the remote big-iron host is NOT in the dev loop —
  heavy repeatable CI is GitHub Actions. Concrete hosts/addresses/sizing live in
  `local/environment.md` (gitignored Operator-local layer, created this day). Program
  doc gained "Where work runs"; qn.0 gained story 0.
- 2026-07-19: (ad) **public/private doc split** (Operator-spotted: the dev-env edit was
  about to push homelab internals to the public repo): `local/` (gitignored) now holds
  all Operator-specific facts — hosts, LAN addresses, container sizing, lab details;
  public canon states rules generically and references `local/environment.md` by path
  only. Personal identifiers scrubbed from public docs (example device names, private
  design-system paths). Standing rule: hostnames, IPs, topology, and hardware specifics
  never enter committed files.
- 2026-07-19: (ae) **dev box is Alpine + nerdctl via the house template flow** (Operator
  overruling the architect's Debian suggestion; the glibc-for-Playwright concern is
  solved the Alpine way — containerized Playwright runner, or system chromium; qn.1
  verifies and records). Template built by the Operator's template-factory script with
  buildkit enabled (the existing template lacks it); the clone is **resized up front**
  (cores/RAM/swap/rootfs) because template defaults will OOM/ENOSPC on builds — never
  wait for the OOM to size a build box. `TMPDIR` moved off the small tmpfs `/tmp`.
  Multi-arch images stay in GitHub Actions; local builds are amd64-only. Full sequence
  with exact commands: `local/environment.md`.
- 2026-07-19: (af) **the dev host is a container host, not a toolchain host** (Operator
  ruling, superseding the apk-toolchain part of (ae)): no language toolchains install
  on any host, ever — every gate target runs inside a pinned toolchain container
  (nerdctl/docker autodetect in the Makefile), using the same base images as the
  production Dockerfile stages; `versions.env` pins image references in exactly one
  place; named cache volumes keep it fast; Playwright runs in its official image
  (musl question mooted); CI runs the identical containerized `make gates`.
  Contributor requirement collapses to `make` + a container runtime.
- 2026-07-19: (ag) **the qn.0 usbmuxd `PROPOSED` gap is dissolved, not chosen between**:
  the architect verified live that `usbmuxd` IS packaged in Alpine community on every
  branch v3.21–v3.24 — the session's probe was faulty. Runtime ships it via `apk add`;
  profiles unchanged (simple = in-container daemon + USB mapping, hardened = host
  socket). Operator's netmuxd-only question ruled alongside: netmuxd alone fully serves
  **pre-paired, Wi-Fi-sync-enabled** devices, so netmuxd-first sequencing inside
  qn.2/qn.3 is encouraged — but initial pairing and enabling Wi-Fi sync are USB-only at
  the protocol level, so USB stays in scope with hardware validation in the lab CT, and
  fresh-device USB pairing must work by the qn.6 gate. Lesson added to D2: verify
  package existence with `apk search` against the target repo, never assume.
- 2026-07-19: (ah) **netmuxd is the single muxer for BOTH transports** (Operator-
  identified, README-verified, superseding the two-daemon halves of (ag) and D2's
  original wording): netmuxd v0.4+ handles USB natively via `nusb` — "no dependency on
  a separate usbmuxd daemon"; the project outgrew its network-only name. Core's muxd
  client targets N configured sockets with N=1 default; classic usbmuxd stays in the
  image as a config-only fallback because netmuxd's USB path is young (v0.4.3 released
  2026-07-14) vs usbmuxd's decades — lab gates in qn.2 (presence + fresh USB pairing)
  and qn.4/qn.5 (sustained USB backup) decide whether the fallback is ever needed.
  Protocol floor unchanged: fresh-device adoption requires a USB connection regardless
  of which daemon serves it.
- 2026-07-19: (ai) **Operator recalled hard evidence against netmuxd-USB** — an initial
  USB backup through netmuxd died with a "packet too big"-style error at the 64 MiB
  boundary + 1 byte (hardcoded-guard signature; unreported in netmuxd's tracker as of
  today; observed version unknown). Ruling amended: **default USB topology = usbmuxd,
  netmuxd serves Wi-Fi** until qn.2's netmuxd-USB audition (presence + fresh pairing +
  a >64 MiB transfer on pinned v0.4.3) passes clean, whereupon the default flips to
  single-muxer; a reproduction gets filed upstream with the signature, with a
  patched-pinned-build option (the qn.7 libimobiledevice pattern). N-socket client
  design makes the flip config-only either way.
- 2026-07-19: (aj) **the (ai) signature corrected against the lab log** (Operator found
  the exact line, dated 2026-07-13): it's the **64 KiB u16 boundary**, not 64 MiB —
  `netmuxd::usb::mux … asyncReadComplete, message was too large (65536 bytes,
  max = 65535)` — i.e. netmuxd HAD USB support during the lab and its mux read path
  choked one byte over `0xFFFF` on real backup traffic; plausibly a one-line fix.
  v0.4.3 shipped the NEXT DAY noting "Fixes iTunes on the Apple mux" — possibly this
  bug, unconfirmed; the qn.2 audition (real backup traffic on pinned v0.4.3) decides.
  Exact line quoted in stack D2; default topology ruling from (ai) unchanged.
- 2026-07-19: (ak) **RETRACTION of the "faulty probe" accusation in (ag)/(ah)**: the
  authoritative per-branch APKINDEX check shows `usbmuxd` in **Alpine 3.24 community
  ONLY** (absent 3.21–3.23) — the qn.0 session's original finding was CORRECT for its
  3.21 base; the architect's all-branches "verification" was the flawed one (apk's
  `--repository` appends to configured repos; all four queries were answered by the dev
  box's own 3.24 repo). The build session's `ALPINE_VERSION=3.21 → 3.24` bump is
  ratified — additionally right because 3.21 (Nov 2024) nears EOL while 3.24 is current
  stable and matches the dev/lab CT line. Follow-up (non-blocking): align toolchain
  images to 3.24-based tags where published. Lesson upgraded in D2: verify package
  claims against the branch APKINDEX or a clean container of that branch.
- 2026-07-19: (al) **new hard rule: "version pins are looked up, never remembered"**
  (Operator-proposed after tracing the 3.21 pin to LLM training-data staleness — a
  model's "current" is its training cutoff's current; third staleness incident today
  incl. two of the architect's). Every pin introduction/bump queries the live source at
  pin time, prefers the newest stable with support runway, and comments any deviation
  from newest with its reason. Landed in the program doc's hard rules.
- 2026-07-19: (am) **the private layer is now version-controlled** (Operator concern:
  gitignored = untracked, unbacked-up, unsynced — quince-dev had no `local/` at all):
  `local/` is a nested git repo pushed to a **private GitHub repo only** (Operator
  choice over self-hosted bare / hybrid), privacy verified; the four `chatgpt-*.md`
  lab/review logs MOVED into it (public doc references updated to `local/chatgpt-*`);
  clone landed on quince-dev (sync gap closed) with a deploy key awaiting the
  Operator's read-only registration; convention added to the program doc — sessions
  editing `local/**` commit in the nested repo. Root `/chatgpt-*.md` gitignore patterns
  retained as belt-and-braces.
- 2026-07-19: (an) **privacy incident + new hard rule**: early qn.0 commits carried LAN
  IPs/hostnames in docs and commit messages; the Operator had the implementer rewrite
  history to scrub them (history verified clean post-rewrite). Cemented in the program
  doc: privacy is a **commit-time gate** — private facts never enter committed files,
  commit messages, branch names, or fixtures; `make privacy-check` (new target) greps
  every staged diff against `local/privacy-patterns.txt` (private repo; no-ops for
  contributors/CI); leak-reaches-history = incident = rewrite + pattern added.
- 2026-07-19: (ao) **Go rewrite of the decryption library greenlit as a parallel
  independent project** (Operator-proposed; scope verified small+frozen — reference lib
  last released 2024, format stable since iOS 10.2, all primitives have mature Go
  counterparts). Repo `github.com/novkostya/ios-backup-crypt` (name vetted 2026-07-19 — 0 GitHub
  collisions, module path + pkg.go.dev free; public MIT; seed CLAUDE.md/README/LICENSE
  authored, awaiting kickoff); includes a test-only encrypt/builder that
  doubles as qn.8's synthetic-fixture generator. **Subprocess boundary kept** (Operator
  ruling): quince-vault becomes a thin Go binary on the unchanged stdio RPC; key
  isolation preserved. qn.8's vault implementation is now conditional — Go if the
  library passes the conformance + real-backup differential gates by rung start,
  Python otherwise. Zero coupling: quince contracts and schedule unaffected either way.
- 2026-07-19: (ap) **improvement-proposal channel added** (Operator-proposed, designed
  with the architect): a non-blocking sibling of the gap protocol — implementers may
  file at most ONE proposal per rung, at rung end only, never pre-implemented, meeting
  a material-value bar (correctness/reliability/security/UX/maintenance; anti-bikeshed
  clause), into `docs/quince.proposals.md`; Operator triages accepted/declined(+why)/
  parked, and decline reasons accumulate as readable taste. Rationale: implementers
  have repeatedly out-seen the canon (Alpine 3.24, Tailwind pin, Makefile design) but
  had no legitimate outlet; the cap + timing + no-prototype rules keep the
  no-improvising discipline intact. quince-only (Operator: ios-backup-crypt is
  near-complete — no value installing process there).
- 2026-07-19: (ag) **qn.0 BUILT — the floor stands.** Provisioned `quince-dev`
  on the PVE host per the `local/environment.md` sequence verbatim (Alpine+nerdctl+buildkit
  template → clone → sized → `<lan-ip>`); recorded the exact `pct` commands back into that
  file. Scaffolded `core/` (Go: `serve`+`version`, `/api/health`, `go:embed` UI, slog,
  race-tested), `vault/` (uv `quince-vault` with `selftest` importing
  `iphone_backup_decrypt`), `ui/` (Vite+React+TS+Tailwind v4 sidebar shell, vitest), the
  containerized `Makefile`, multi-stage `deploy/Dockerfile` (netmuxd built from source),
  CI, compose examples, `deploy/dev.md`, transcript-README. **Proven in-container:** full
  `make gates` green, `make image` green, and the image's runtime gates
  (`version`/`health`/`selftest`/embedded-UI) all pass. Rung-ruled bootstrapping fixes
  (in the qn.0 spec + `versions.env`): uv image `-alpine` tag, Tailwind `4.1.18` (4.0.0
  crashes), Rust `1.88` (netmuxd needs edition 2024), pnpm `overrides.vite`, mypy
  stub-override, vault venv built at its final path against the runtime python.
  **`.gitignore` bug caught by testing**: trailing inline comments silently disabled the
  private-file rules — rewritten with column-0 comments; private lab logs now verified
  `!!` ignored. **Registry push proven**: `make push REGISTRY=<lan-registry>` pushed
  `quince:local` to the LAN registry (endpoint in `local/environment.md`) and it pulls
  back. Per (ak)'s follow-up the toolchain images were then migrated to a single
  Alpine-3.24 line (Go 1.26.5 / Node 22.23.1 / Rust 1.97.1 / golangci-lint v2), re-proven
  green; the `usbmuxd` daemon (Alpine 3.24 community) now ships in the runtime — so the
  old D2 `PROPOSED` gap is closed, not open. Before any push, one privacy incident
  (Operator infra in commit messages + an earlier version of this entry) was scrubbed by a
  full `git` history rewrite — the origin of the "Privacy is a commit-time gate" hard
  rule. Next frontier: **qn.1** (spec to be written).
- 2026-07-19: (ah-qn1) **qn.1 BUILT — the app frame stands.** Full `make gates`
  (go + vault + ui), `make gates-ui-e2e` (Playwright stories 1–2), and `make image` green
  in `quince-dev`. **Core** (`core/internal/{wire,config,store,auth,bus,ws,demo}` +
  expanded `httpapi` + `id`): typed schema-v0 config with atomic canonical writes /
  last-good-on-invalid / `quince config validate`; modernc SQLite (WAL) with embedded
  migrations (`settings`/`sessions_auth`/`audit`); argon2id auth with first-run
  set-password (one-shot **409** guard), session rotation, idle/absolute timeouts, per-IP
  login rate limit, and double-submit CSRF; a race-clean event bus (drop-on-slow) + the
  `/api/ws` handler (pre-upgrade auth + strict Origin, `hello` frame, ping keepalive); the
  full REST read surface (devices/jobs/versions/config) golden-tested against contracts §2;
  a security middleware chain (recover, CSP + frame denial, body limit, auth guard, CSRF
  guard); and a `--demo` provider scripting device churn + a backup with a
  silent-stall→recovery arc + every WS event type. **UI**: react-router auth-gated shell,
  a WS bridge feeding Zustand stores with reconnect-backoff + GET-refresh, vendored
  shadcn-style components on Radix, Dashboard / device-details / Settings pages on live
  demo data, and a shared humanizer. **Operator rulings this rung** (also in the spec's
  rung-ruled section + contracts §1): the auth endpoints (`/api/auth/status`, `/api/auth/setup`
  with the 409 guard, double-submit CSRF) and adopting `react-router-dom`. Rung-local calls:
  library set looked up live (yaml.v3 / modernc / coder-websocket / x/crypto / oklog-ulid;
  zustand / TanStack Query / Radix), embedded-SQL migration runner, Secure-cookie-off in
  demo (so http e2e/localhost login works), hardcoded admin-session timeouts (future
  `auth:` config noted for qn.6), slog JSON/TTY, config exchanged as structured JSON,
  golden fixtures via `make gen-golden`, and a two-container Playwright e2e target
  (`gates-ui-e2e`, CI `e2e` job) using the official Playwright image. Not yet committed
  (awaiting Operator). Next frontier: **qn.2**.
- 2026-07-19: (aq) **domain parsing goes to a standalone sibling library —
  `ios-backup-parser` — and the repo-naming policy is ruled.** Naming (Operator, after
  discussion): `quince-*` prefixes only app satellites (the private local layer today;
  helm/docs/demo someday); standalone libraries carry descriptive names — the
  `ios-backup-*` family (`-crypt`, now `-parser`). Rationale: the brand lives in the
  owner segment (a future org would follow the `immich-app` pattern — the bare `quince`
  account is taken), descriptive names win search discoverability, and Go module paths
  make renames expensive. Name picked from a vetted-unique shortlist
  (parser/records/content/data; `-artifacts` rejected on taste). The library: pure-Go,
  MIT, typed *streaming* records for messages/contacts/call-history/calendar/notes
  from already-decrypted backups; zero coupling to quince OR ios-backup-crypt (host
  supplies a `BackupFS` accessor); schema detection by introspection + per-backup
  capability reports (state honesty ported); license-hygiene rule — iLEAPP (MIT) is
  translatable with attribution and a differential oracle, imessage-exporter (GPL-3)
  is a black-box oracle ONLY (its typedstream/`attributedBody` ground is the known
  hard part); milestones: schema spike → contacts → calls → messages → calendar →
  notes → v0.1. Ecosystem verified live this day: no reusable Go artifact-parsing
  library exists. Quince side: qn.10's research spike is subsumed by the library's M0
  (off the critical path); qn.9/qn.10 consume the library iff the Go vault (D4/(ao)
  chain) landed at qn.8 AND the domain is covered — else in-vault adapters as specced.
  Roadmap M7 + design §7 updated; §7's adapter keying refined from "iOS major version"
  to "detected schema" (introspection, never a trusted version string). Photos remain
  parked. Charter seeded at the sibling repo (CLAUDE.md/README/LICENSE); separate
  implementer to be spun up by the Operator.
- 2026-07-19: (qn1-review) **qn.0/qn.1 post-build review + fixes.** A read-only conformance
  review (specs + frozen contracts §1–§6 + design §6) found **no blocker/major** — both rungs
  conform and the security baseline is sound — plus a tail of minors. Top items fixed this
  pass (full `make gates` + `make image` + `make gates-ui-e2e` re-green): (1) `GET
  /api/jobs/{id}/log` (frozen in contracts §1) was unrouted — now served `text/plain` via
  `JobReader.JobLog`, demo-backed by a per-job log ring buffer, and the UI recovers a running
  job's log tail on WS reconnect (the `job.log` stream isn't replayable — closes the story-2
  hole); (2) the demo now emits `device.updated` on backup success (refreshing `last_backup`),
  so every §3 WS event type fires end to end and the device card no longer goes stale; (3) a
  demo fixture set `last_backup.job_id` to a version id, not the job id (golden regenerated);
  (4) auth hardening — `verifyPassword` now rejects an empty-key hash (was fail-open via
  `ConstantTimeCompare` of two empty slices) and the login rate-limiter sweeps stale per-IP
  buckets so the map can't grow unbounded; tests cover all three. **Deferred (logged, not
  blocking):** WS session re-validation on logout/idle-expiry, DSN-scoped SQLite pragmas, a
  `.dockerignore`, and assorted nits. Frontier unchanged: **qn.2**.
- 2026-07-19: (qn2-build) **qn.2 code built.** The `internal/muxd` plist protocol client
  (`howett.net/plist v1.0.1`, Listen handshake, per-connection DeviceID→UDID map, reconnecting
  dialer) and the `internal/device` **registry** (N-muxer merge, per-transport/per-source
  presence keyed by UDID, **reset-on-(re)connect reconcile** clearing detached-while-away
  phantoms, `device.*` events), wired into non-demo `quince serve` as the live `DeviceReader`
  (default topology usbmuxd-USB + netmuxd-Wi-Fi; single-muxer flip is config-only). CI stories
  1–5 green under full `make gates`; lab gates 6–7 (plug/unplug ≤1 s + the netmuxd-USB
  audition) remain a hardware step. `muxd.Client.Run` now takes a `Sink{Reset,Apply}`;
  rung-ruled details in `specs/qn.2/qn.2.md`. The no-flicker snapshot-debounce reconcile
  (idle-debounce + `testing/synctest`) is the documented refinement if reconnect churn bites.
- 2026-07-20: (qn2-close) **qn.2 closed; muxer-startup gap surfaced + documented.** qn.2's
  deliverables (muxd client + `internal/device` registry + UI; `make gates`/image/e2e green) are
  complete; a post-build review + UI polish (empty-state copy, state-driven device card — disabled
  `Pair`/`Back up now` reflecting muxd-minimal presence) landed alongside. Its **lab gates 6–7 are
  deferred** to a future hardware session (they need a real device AND the muxer-startup gap
  resolved). During staging testing an architectural gap was surfaced — **nothing starts the
  in-container muxer, breaking D12 for USB** — and captured as **open question 2** (`PROPOSED
  (gap)`, for the Architect; not decided/built here). A staging stand was stood up on the PVE host
  (CT 113, `quince:staging` from the private registry, HTTPS via the CT-102 Caddy) for manual
  testing; its USB path uses a **temporary usbmuxd-in-CT + socket-bind workaround** (hotplug needs
  the `/root/redetect.sh` helper), rebuilt onto the house template's `/root/compose.yml` autostart
  convention (specifics in `local/environment.md`). Frontier → **qn.3**.
- 2026-07-20: (ar) **qn.2 cleanup package: muxer gap ruled, qn.2b inserted, qn.5↔qn.4
  swapped, worktree-init fixed** (Architect adjudication + Operator rulings). (1) Open
  question 2 RULED as option (a): quince supervises the in-container muxer — Go subprocess
  in its own process group under the serve context, restart-on-crash with capped backoff,
  killed on shutdown, **refuse-loudly if the socket is already served** (no silent
  adoption) — behind `devices.manage_muxer` (true = simple profile; false =
  hardened/external, making the staging socket-bind topology a supported mode), plus
  `POST /api/devices/rescan → 202|409` + UI Rescan reusing the reset/replay reconcile.
  Contracts §1/§6 and design §2 updated (the architect landed the contract-change ahead of
  the rung, per program rule). (2) **New rung `qn.2b`** (M1, before qn.3): MINIMAL
  supervision scope + rescan + **ownership of qn.2's deferred lab gates 6–7** (plug/unplug
  ≤1 s + the netmuxd-USB audition) — one physical-presence session; FULL muxer work stays
  qn.6/qn.7. Deferred-without-owner is how gates evaporate; qn.3's "fresh container via UI
  only" gate also depends on this. (3) **New hard rule: "a rung's goal is provable at rung
  close"** (program doc) — the Operator-requested self-containment audit of qn.3–qn.12
  found exactly one more violation: qn.4's `succeeded` needs qn.5's `Commit()` → **order
  swapped, qn.5 before qn.4** (qn.5 proven on fixture trees + a manually-produced
  `idevicebackup2` tree; qn.4 closes M3 with the true e2e gate); rung numbers stay
  (labels, not order — qn.7-before-qn.6 precedent). (4) **Worktree init**: worktrees
  materialize only tracked files, so sessions there lacked the private `local/` layer —
  mandatory first step now documented: `ln -s ../../../local local` (symlink sits on the
  gitignored path, uncommittable; privacy-check + environment.md pointers work unchanged).
  Also noted: qn.2's out-of-scope moment was handled correctly by the gap protocol (code
  scope held; design captured as PROPOSED, not built) — the process worked. Frontier →
  **qn.2b** (spec to be written by its session from the roadmap outline + the qn.2 spec
  appendix).
- 2026-07-20: (as) **plan-time discipline made structural** (Operator correction to the
  (ar) framing: qn.2's rule-adherence was largely Operator-ENFORCED — the implementer's
  proposed plan drifted from canon until manually pointed at the rules it was about to
  break; supervision-as-guardrail doesn't scale). Two program-doc changes: (1) the spec
  shape gains a mandatory **Rule check** section — every hard rule / canon boundary the
  rung touches or comes near, one compliance line each, written before building (a plan
  about to break a rule can't fill it truthfully, so violations surface as text); (2) the
  build loop gains a **pre-build spec review gate** — spec incl. Rule check → Operator
  routes it through the architect → explicit go, only then code (formalizes what
  happened ad hoc for qn.2's spec, which picked up five amendments in review).
  Repositions Operator supervision from hunting unflagged violations to adjudicating
  flagged edges. Applies from qn.2b onward.
- 2026-07-20: (at) **coverage made a declared artifact; handoff review gets named
  dimensions** (Operator-driven — third vigilance→structure conversion: the qn.2b
  handoff review found untested qn.2 cases only because the Operator explicitly
  prompted for coverage). (1) Rung reports now DECLARE coverage: the `go test -cover`
  summary + an explicit **known-untested list** (one line + reason each); declared =
  accepted debt, undeclared-found-later = a finding — state honesty applied to tests.
  (2) The rung handoff review runs four named dimensions: seams / coverage (verify the
  declaration, then hunt untested branches in consumed code) / state honesty /
  contracts. Process-budget note (Architect, Operator-acked): the program's gate set is
  now considered FULL — the next process addition should displace something, not
  append. The current coverage findings route through the existing triage: tests for
  consumed code land as `qn.2 review fix:` commits; the rest becomes declared debt or
  ledger entries.
- 2026-07-20: (au) **qn.2b BUILT (CI) — the in-container muxer has a lifecycle.** Cleared the
  new pre-build spec-review gate ((as)): spec + Rule check → **architect APPROVED with four
  amendments** (all folded in). Shipped: `internal/muxsup` supervisor (`exec.Command` usbmuxd
  `-f -S <socket>` in its own process group, restart-w/-backoff 500 ms→×2→30 s, SIGTERM→grace→
  SIGKILL on shutdown, **refuse-loudly** probe on an already-served socket, **crash-loop →
  `/api/health` degraded** with the last exit reason); `POST /api/devices/rescan → 202|409`
  reusing the muxd reconnect→`Reset()`→replay reconcile (no new device-table code), incl.
  rescan-as-recovery from degraded (takeover once the socket frees); the `devices.manage_muxer`
  config key (default true, first in `DevicesConfig`); `/api/health` `muxer:{managed,state,
  detail}`; and a UI **Rescan** control (202 in-progress / 409-explains, never a dead button).
  Wiring: managed → supervisor; external/`--demo` → `UnmanagedMuxer` (409). `make gates` +
  `make image` + `make gates-ui-e2e` green; **supervisor additionally smoke-tested against the
  REAL usbmuxd in the built image** — `/api/health` → `muxer:{managed:true,state:"running"}`,
  `usbmuxd v1.1.1_git20250201 starting up`. **Amendment 1 (verify interface facts, not just
  versions) paid off:** `usbmuxd --help` showed the daemon owns `-S/--socket` — so
  `devices.usbmuxd_socket` is authoritative via the daemon's flag, NOT the client-side
  `USBMUXD_SOCKET_ADDRESS` env the draft guessed. **Handoff review of qn.2** (four dimensions,
  (at)): gates green; `internal/device` 97.2%, but `internal/muxd` was **44%** — the entire
  `Client.Run` reconnect/backoff/dial loop and the `readPlist`/`listen` guards were untested,
  exactly the seam qn.2b's rescan consumes. Landed as a `qn.2 review fix` (`muxd/client_test.go`,
  real-socket reconnect-reconcile over unix+tcp + codec-guard cases) → muxd **85.7%**. **Coverage
  declaration ((at)):** `muxsup` 82.7%, `httpapi` 70.6%; known-untested = the SIGTERM-grace→SIGKILL
  escalation branch, the 30 s backoff-cap arithmetic, and the dial-timeout / ctx-cancel-mid-dial
  paths (timing plumbing, low-risk). **Lab gates 7–8 (plug/unplug ≤ 1 s, netmuxd-USB audition)
  remain the hardware session**, owned by this rung. `.gitignore` `local`-symlink hole surfaced
  via the qn.2b Rule check and landed on `main` (`a057783`) — rebased in. Frontier → **qn.3**
  (inherits "enrich muxd devices with lockdown identity").
- 2026-07-20: (av) **qn.2b lab finding — managed-muxer USB needs a LIVE `/dev/bus/usb`, not
  `devices:`** (surfaced testing Rescan on staging with a real iPhone; "Rescan didn't work"). Not a
  code defect — the supervisor + rescan behaved correctly. A static `devices:` mapping (runc
  `--device`) SNAPSHOTS the device-node list at container start, so a device plugged/re-enumerated
  later never appears in the container; usbmuxd restarted by Rescan then hits
  `LIBUSB_ERROR_NO_DEVICE` (`/sys` live, `/dev` node missing) — restarting the muxer can't surface
  it. Fix (deploy-only): bind `/dev/bus/usb` as a **volume** (live) + grant char-device access
  (`device_cgroup_rules: ['c 189:* rmw']` on Docker; `privileged: true` on nerdctl/podman/unpriv-LXC
  which lack device-cgroup-rules). Validated in a throwaway then deployed to staging — the
  in-container usbmuxd connected to the iPhone. `deploy/compose.nas.yml` corrected; captured in the
  qn.2b spec's Lab finding. The lab gate did its job: a real device found a deploy gap CI fakes
  can't. Rescan's "re-detect a missed device" value now correctly depends on a live container `/dev`.
- 2026-07-20: (aw) **qn.2b CLOSED; netmuxd-USB audition re-homed to qn.7** (Operator ruling). Lab
  gate 7 (managed in-container usbmuxd brings USB up via `compose up` + UI **Rescan** re-detects a
  re-plugged device) **PASSED on hardware** (Operator-confirmed on staging, after the (av) deploy
  fix). Lab gate 8 (the netmuxd-USB audition on v0.4.3) is **moved to qn.7** — it answers a
  netmuxd-viability question that pairs with qn.7's netmuxd co-supervision, qn.2b's goal doesn't
  depend on it (default topology stays usbmuxd-for-USB; the single-muxer flip is config-only either
  way), and it's the risky one (`idevicepair unpair` destroys the pairing record). **Re-assignment
  with a named owner, NOT a silent defer** — the audition procedure is preserved verbatim in the
  qn.2b spec (gate 8) for the qn.7 session to inherit, and the qn.7 roadmap row now lists it, so the
  no-orphan-gate rule qn.2b was created to enforce stays intact. qn.2b's goal (managed usbmuxd
  supervision + rescan) is proven end-to-end (CI + hardware); the rung closes. Frontier → **qn.3**.
- 2026-07-20: (ax) **P1 accepted → qn.6** (first proposal through the channel; Operator ruling,
  architect-recommended): the broken-container-USB onboarding/health check joins qn.6's §9
  guided checks (ledger + roadmap M5 updated). Post-landing architect review of qn.2b: clean —
  (aw) ratified; one docs-part-of-diff slip swept (stale audition references in stack D2 +
  roadmap M1/M4, fixed on main).
- 2026-07-20: (ay) **one project, one dev host** (Operator-ruled after an incident: a sibling
  library's gates ran on the shared dev container alongside an active quince rung — cache/
  container/memory contention got messy enough to force an emergency second box mid-rung).
  Program doc updated: sibling projects never share a dev container with quince or each other;
  per-project boxes under the same pure-container-host rules; registry + provisioning in the
  Operator-local env doc; idle boxes are stopped, not deleted. Knock-on fixes: the parser's M0
  study-data bind re-pointed from quince-dev to the parser's own (to-be-provisioned) box, and
  the sibling repos' `privacy-check` pattern lookup extended (`../quince-local/…`) so the
  commit gate stays armed on boxes that have no quince checkout next door.
- 2026-07-20: (az) **qn.3 BUILT (CI) — device ops + Devices page.** Cleared the pre-build
  spec-review gate: spec + Rule check → **architect APPROVED with three amendments + two
  rulings**, all folded in (Operator acks: hardware encryption coverage = `change_password` +
  a disable→enable cycle; keep the freshly-paired container standing). **Interface facts verified
  live** in the built image (libimobiledevice 1.4.0) — the STOP-gap cleared: `idevicebackup2`
  supports interactive `-i` (pty getpass) **and** `BACKUP_PASSWORD`/`_NEW` env; per the spec's
  pty-preference qn.3 uses the **pty** (password never in argv/env/log); `idevicepair pair` is
  **error-and-retry** (not blocking) so `waiting_for_user` is a poll-until-`SUCCESS` loop;
  `USBMUXD_SOCKET_ADDRESS` = `UNIX:<path>`/`host:port`; `ideviceinfo -x` keys + `-q
  com.apple.mobile.backup -k WillEncrypt`. Shipped: **`internal/deviceops`** (argv wrappers with
  the muxsup subprocess hygiene + a `GO_WANT_HELPER_PROCESS` fake-CLI harness; the pty-driven
  encryption path via `creack/pty v1.1.24`); **`device.Enrich`** (lockdown identity overlaid on
  the muxd-minimal shell, `device.updated` on change) + a bus-driven **enrichment driver**
  (attach → `ideviceinfo`/`idevicepair validate`, per-UDID debounced, off the request path);
  the **four frozen endpoints** (`POST …/pair` 202|404|409, `…/pair/validate`, `…/encryption`
  202|422, `GET /api/ops/{id}`) behind a consumer-defined `DeviceOps` interface; the **`Op`
  lifecycle** manager (running→waiting_for_user→succeeded|failed, `op.updated`); **audit** rows
  for pair/encryption (no secret; design §6 list updated — amendment 3); **pairing-record
  persistence** (whole-dir copy of `/var/lib/lockdown` ↔ `$QUINCE_DATA/lockdown`, amendment 1 —
  survives a container recreate); non-demo wiring + a demo `DeviceOps` scripting the op flow;
  and **UI** pair + encryption dialogs (assisted narration, unencrypted-banner CTA, USB-only 409
  explained, passwords never in URL/log). **`make gates` + `make image` + `make gates-ui-e2e`
  green** (added e2e **story 3**: encryption op narrates the assisted flow to success). **Story 5
  headline gate proven** — a test asserts the password is in no argv/env/log/audit and only
  reaches the child over the pty. **Coverage declared:** deviceops **80.2%**, device **97.6%**,
  httpapi **71.8%**; **known-untested** (accepted debt, all low-risk error/edge or trivial
  helpers): the enrichment-driver subscription-overflow `refreshAll` recovery, the ctx-cancel
  process-group SIGKILL branch, the ops-map `pruneLocked` eviction (needs 200+ ops), the
  lockdown mkdir-error warn branches, and the trivial `SetLockdown`/`encStartMsg`/`encDoneMsg`
  defaults. **Lab gate 8 (fresh container → paired → encryption on real hardware) is the
  remaining physical-presence step** — owned by this rung, not deferred. Not yet committed
  (awaiting Operator).
- 2026-07-20: (ba) **qn.3 CLOSED — lab gate 8 PASSED on real hardware.** Deployed the qn.3
  build to the staging CT (managed usbmuxd, live `/dev/bus/usb`) and drove the gate with a real
  iPhone: **(1) pair** via the quince UI on a fresh container → `paired: yes`, with the record
  written to `$QUINCE_DATA/lockdown` (proves `Backup()` fired = a real pair op, not enrichment);
  **(2) persistence** (amendment 1) → `nerdctl compose down && up` → `lockdown: restored …
  count:2` → still `SUCCESS: Validated`, no re-Trust — **proven twice** (a second redeploy for
  the UI fix repeated it); **(3) encryption** → `change_password` then a full `disable → enable`
  cycle, all succeeding, ending encryption **ON** with an Operator-held password; **(4) secrets
  (story 5) on hardware** → the capture caught `idevicebackup2 -i -u <udid> {changepw,encryption
  off,encryption on}` — **no password in argv**, `BACKUP_PASSWORD` env count **0**, clean logs —
  the password reached the child only over the pty. **Four findings caught by the gate, all fixed
  + CI-validated + committed as `qn.3 lab finding:`** — the substantive one: **enrichment
  auto-paired a locked device** (`idevicepair validate` returns "passcode is set" for ANY locked
  device regardless of pairing — observed on a fresh host with no record — so mapping it to
  `paired: yes` + then doing the auto-pairing full `ideviceinfo` could silently trigger Trust;
  fixed → locked ⇒ `paired: "unknown"`, and the full/auto-pairing read runs only for a confirmed
  `validatePaired`, everything else uses the no-auto-pair simple read); plus three UI fixes (the
  dashboard card's stale disabled Pair now routes to the details flow; the encryption mode
  switcher reset after a completed op; a persistent "confirm on the device with its passcode"
  hint; mode frozen at open + dialog auto-closes on success so the title no longer mismatches the
  result). The lab gate did its job — a real device found a real code bug the CI fakes could not.
  The paired staging container is **kept standing** as the qn.4/qn.5 base (Operator ack).
  Frontier → **qn.5** (storage; qn.5-before-qn.4 per (ar)).
- 2026-07-20: (bb) **qn.3 post-landing architect review: clean; docs-drift swept.** All three
  amendments + both rulings verified in the landed code (pty-only secret path spot-checked;
  coverage declared with an honest debt list; lab findings committed as labeled fixes). Sweep
  (same class as qn.2b's): contracts §1 now records the implemented error codes
  (pair 404/409-USB-only, encryption 422) and the RESOLVED password channel (pty `-i` verified,
  env fallback deliberately unused — the stale "qn.3 verifies which" comment closed); design §3
  gains the locked-device rule (`paired: unknown` on locked; full lockdown read only after a
  confirmed validate — the accidental-auto-pair guard, since qn.4's preflight consults the same
  path). qn.3 worktree + branch removed post-landing.
- 2026-07-20: (bc) **canon fix found by the qn.5 spec review: structural verification branches
  on encryption.** Design §4's checklist ("`Manifest.db` opens read-only + record sample
  resolves") is impossible passwordless on ENCRYPTED backups — the product default — because
  since iOS 10.2 the manifest itself is encrypted; CI fixtures (unencrypted) would have passed
  while gate 11's real encrypted tree failed. Ruled: `Manifest.plist.IsEncrypted` selects the
  variant — encrypted: exists + non-trivial size + NOT-plaintext-SQLite-magic + blob-shard
  sanity, with record-sampling deferred to the content level (qn.8's unlock, which now owns it
  for encrypted versions); unencrypted: the full checklist. Design §4 amended; qn.5's spec
  folds the branch + an encrypted fixture variant (amendment A1).
- 2026-07-20: (bd) **qn.5 BUILT (CI) — the version store stands.** Cleared the pre-build
  spec-review gate: spec + Rule check → **architect APPROVED with three amendments (A1 encrypted
  `Verify` branch, A2 a `RepairWorkingCopy` story, A3 name `Prune`'s trigger) + five rulings**, all
  folded in. Shipped: **`internal/storage`** — the `Backend` interface with two genuinely
  different models (`zfs` snapshot-native via a validated exec/hook `zfsCLI`, dataset-destroy never
  issued; `reflink`/`hardlink`/`copy` namespace-versioned), the **auto-selection probe** (FICLONE
  independence / `link()`+inode on the real `/backups`; `copy` degraded mode surfaced), **journaled
  commit** with on-disk `quince-version.json` markers + an explicit per-device commit journal,
  **first-class startup reconciliation** (roll-forward matrix: kill at every phase → defined
  repair; adopt on-disk versions with no row = `job_id` null protected; row with no artifact →
  `missing`, never dropped; orphaned `work/` swept only after), structural **`Verify`** branching
  on `Manifest.plist.IsEncrypted` (A1), **`RepairWorkingCopy`**, and retention **`Prune`**
  (post-commit + explicit, no scheduler); **`internal/storage/clonetree`** (one FICLONE/hardlink/
  copy cloner; hardlink copies `MutatesInPlace` classes); a **`versions` table + registry** in
  `internal/store` (the real `VersionReader`); **`DELETE /api/versions/{id}` → 202|404|503** + a
  `VersionAdmin` consumer interface + audit + `version.created`/`version.deleted` events; non-demo
  wiring that **reconciles before serving**; a `--demo` delete path; and **`deploy/storage.md`**
  (the constrained `quince-zfs-helper` forced-command + the anchored rclone filter block).
  **`make gates` + `make image` + `make gates-ui-e2e` green.** `-cover` wired into `gates-go`
  (the "when first needed" moment). **Coverage declared:** storage **78.3%**, clonetree **71.4%**,
  store **80.1%**, httpapi **71.8%**; **known-untested** (accepted debt, all low-risk or
  environment-gated): the reflink/FICLONE leaf (`clonetree` reflink path + the zfs reflink-mirror
  branch) — proven for-real in lab gate 12, skipped-with-a-log in CI (tmpfs has no FICLONE); the
  zfs reflink-from-snapshot copy-fallback branch; a few reconcile/adopt error-log branches; the
  `zfsCLI` list/destroy not-found guards. **Build finding fixed:** `WriteMarker` now replaces
  (remove-then-write) rather than truncates, so a hardlink-seeded `work/` can't rewrite a committed
  version's marker. **Lab gate 12 (real zfs on the host + iMazing-opens + syncoid-mid-write + the
  destructive hardlink-safety matrix) is the remaining physical/host step** — owned by this rung,
  not deferred. Not yet committed (awaiting Operator). Frontier stays **qn.5** until gate 12; then
  → **qn.4a** (engine; qn.4 split into qn.4a/qn.4b per (be)).
- 2026-07-20: (be) **qn.4 split into qn.4a / qn.4b** (Operator-ruled after a plan-shape review:
  the rung was three heterogeneous concerns wide — engine, Wi-Fi, CLI — unlike qn.5's
  one-subsystem depth). **qn.4a** = the transport-AGNOSTIC job engine + supervisor + the minimal
  headless CLI as the rung's own lab harness; CI replays ALL lab transcripts including the Wi-Fi
  torn sessions (the engine is Wi-Fi-shaped from day one); hardware gate = an encrypted USB
  backup driven from the CLI ending as a committed verified version + the engine kill matrix.
  **qn.4b** = Wi-Fi first-class + `transport: auto` + the intent-grouped job history API/UI +
  CLI completion (`versions verify`, `repair-working-copy` surface), closing M3 with the
  both-transports UI-driven gate incl. an injected Wi-Fi mid-backup disconnect landing honestly.
  **Explicitly NOT a Wi-Fi demotion** — ruling (h) stands: Wi-Fi keeps its own rung + hardware
  gate inside M3, before qn.7 and far before v0.1. The CLI was ruled NOT a separate milestone:
  standalone it is thin plumbing with no goal sentence, and splitting it would rob the engine
  rung of its driving interface (its bulk IS the engine working). Roadmap M3 + dashboard
  restructured; numbers stay labels (qn.2b precedent). The updated frontier chain: qn.5 gate 12
  → qn.4a → qn.4b.
- 2026-07-20: (bf) **gate-12 gap RULED: the zfs mirror probes for MEASURED sharing, not FICLONE
  success.** The gate's Operator-run core PASSED on real ZFS 2.4.3 (throwaway child dataset;
  create → snapshot → mirror → registry → `RepairWorkingCopy`, twice; **A1's encrypted `Verify`
  proven on the real ~34G encrypted tree** — committed without opening `Manifest.db`, exactly
  the CI-blind bug the amendment predicted) and surfaced two definitive findings: (1)
  reflink-from-snapshot = `EXDEV` (interface fact 2 answered; the designed clone-from-`working/`
  fallback stands); (2) **FICLONE succeeds WITHOUT sharing blocks on the real pool**
  (`block_cloning` active, `zfs_bclone_enabled=1`; verified three independent ways) — the
  "zero extra space" reflink premise is false there. Ruling: option (c) sharpened — the mirror
  strategy chain stays reflink → hardlink → copy, but the probe measures real physical-usage
  sharing; ineffective reflink is demoted, the hardlink strategy is the space candidate GATED
  on the 12c destructive matrix, and copy is the always-correct floor with its cost SURFACED
  (no silent fallback). Option (b) — offsite sync from `.zfs` paths — REJECTED: `snapdir=hidden`
  hides them from rclone, `snapdir=visible` uploads every snapshot at full size; D5a stands.
  Option (d) — root cause — demoted to a non-blocking side quest; first check: `zfs get
  encryption` on the pool datasets (BRT + native encryption has documented no-share
  restrictions — this may be known behavior, not a 2.4.x bug), then an upstream issue if it
  reproduces on an unencrypted dataset. Stack D5 amended.
- 2026-07-20: (bg) **the (bf) no-share verdict is PROVISIONAL — Operator challenged it, and
  there is a specific accounting trap that could fully explain the evidence.** ZFS charges
  BRT-cloned blocks like dedup: full size per reference at dataset level (`zfs list used`,
  `du`); the savings are visible ONLY at pool level (`zpool get
  bcloneused,bclonesaved,bcloneratio` / pool ALLOC delta). All three gate-12 measurements are
  consistent with WORKING clones misread through dataset accounting. Discriminator protocol
  (host-side, zero container layers, ~10 min): on the PVE host — `zfs create` a throwaway,
  `dd` a test file, `zpool sync`, note `bclonesaved` + pool ALLOC, GNU `cp --reflink=always`,
  `zpool sync`, re-read both. `bclonesaved` grows ~file-size → cloning WORKS, reflink
  reinstated, (bf)'s demotion reverses (the probe still moves to pool-level measurement —
  that part of the ruling stands regardless). Flat → the no-share finding is real; then `zfs
  get encryption` (BRT × native-encryption restriction) before any upstream filing. Also
  eliminate stack layers while at it: the original harness ran through container/bind paths —
  the re-measure runs on the host with GNU cp; note `zfs_bclone_wait_dirty=0` makes clones of
  UNSYNCED data fail (a Go fallback chain could silently copy) — hence the `zpool sync`
  before cloning. The EXDEV-from-snapshot finding is unaffected (cross-superblock FICLONE is
  kernel behavior no mount option changes; the clone-from-`working/` fallback stands). Remaining gate-12 legs: iMazing-opens
  (Operator GUI), syncoid mid-write (needs a replication target), the 12c matrix — with the
  iOS-upgrade leg marked OPPORTUNISTIC (runs at the next real update; a named trigger, not a
  blocker), the rest forceable now.
- 2026-07-20: (bh) **(bg)'s discriminator RUN by the Operator on the host — CLONING WORKS;
  reflink REINSTATED.** `bcloneused` 388M→788M (+400M = the test file), `bclonesaved`
  695M→1.07G, pool ALLOC flat at 391G; the baseline itself proves prior clones were already
  sharing on this pool. (bf)'s demotion reverses per (bg)'s pre-registered branch: the zfs
  `latest/` mirror keeps reflink (near-instant, zero extra pool space; the ~34G-per-commit
  copy price evaporates). What stands from (bf): the EXDEV-from-snapshot finding + the
  clone-from-`working/` fallback (the operative path), and the probe measuring REAL sharing
  at the POOL level — rung-local pick for qn.5: the `avail`-delta method needs only the
  hook's existing `list` verb, or extend the helper with read-only `zpool get bclone*`.
  Dataset-level `used` is documented as the trap (BRT bills like dedup). Option (d) side
  quest CLOSED: root cause = accounting semantics, nothing is broken, no upstream issue.
  Chain of custody worth recording: the gap protocol caught canon-vs-reality, and Operator
  skepticism then caught evidence-vs-instrumentation — without (bg), a dataset-`used` probe
  would have silently demoted a working reflink on every pool, forever.
- 2026-07-20: (bi) **the Operator's layer ladder caught the THIRD layer: unprivileged userns
  blocks FICLONE (`EPERM`) — mirror strategy RULED as a ladder with a host-side hook verb.**
  The qn.5 session's mandated re-verification (OCI → LXC → host, exact production mount shape)
  established: host shares fully (+4.3G bcloneused/saved, ALLOC flat); unprivileged LXC and
  the OCI container inside it get `EPERM` — so in-container reflink is unavailable in the
  recommended secure topology, and the session's original practical outcome (mirror costs a
  copy) was RIGHT for the wrong reason, twice removed. Its confirmations were exemplary:
  recomputed dataset-`used` predictions match all three original readings (the accounting trap
  fully explains finding #2), EXDEV-from-snapshot reproduces at every layer. RULING (option 1
  + option 2 as fallback; 3 rejected on security posture — privileged topologies simply fall
  out of the ladder naturally; 4 stays rejected per (bf)): the mirror ladder = (i) hook
  present → new constrained **`mirror` verb** rebuilds `latest/` HOST-side where FICLONE
  works (`cp -a --reflink=always` from `working/` under the job lock + atomic swap; children
  of the parent only; touches only the derived `latest/`, never snapshots — bounded blast
  radius since `latest/` is rebuildable); (ii) hookless → in-container reflink attempt with
  the pool-level probe; (iii) hardlink-under-matrix; (iv) copy, surfaced. Stack D5 amended;
  deploy/storage.md + the helper reference gain the verb (qn.5 folds); interface facts 1–2
  close with the full three-layer evidence. Investigation arc complete: canon-vs-reality →
  evidence-vs-instrumentation → layer-privilege; each round caught by a different mechanism
  (gap protocol / Operator skepticism / the Operator's layer ladder).
- 2026-07-20: (bj) **probe semantics refined (fourth Operator challenge: "how can a
  hookless container run a pool-level probe?"): the sharing measurement governs REPORTING,
  never selection.** A non-sharing FICLONE is functionally a copy (same correctness, same
  cost), so FICLONE-works suffices to select reflink — the EPERM case self-selects down the
  ladder; the measurement only decides the honest claim (zero-space verified / unverifiable
  in this topology / copy cost). Measurement channels, best-available: hook `list`
  avail-delta → delegated `zfs list -o avail` (exec mode) → syscall-only `statfs(2)`
  `f_bavail` delta around an incompressible test clone (no zfs binary needed; sync-and-settle
  for txg accounting lag) → none ⇒ report UNVERIFIED, never claim zero-space. Stack D5
  amended. This closes the reflink investigation: selection is now trivially safe, and
  honesty degrades gracefully with the deployment's observability.
- 2026-07-20: (bk) **(bj) corrected on the fifth Operator challenge ("hardlink seems
  better"): the measurement DOES inform selection — in exactly one direction.** (bj)'s
  "never worse than the fallback" compared only against copy and forgot hardlink sits above
  it. Corrected rule: the ladder orders by RISK dominance (reflink clones are independent;
  hardlinks alias — in-place mutation of `working/` would silently corrupt a hardlinked
  `latest/`, which is why hardlink is matrix-gated and why reflink outranks it wherever both
  share); the one selection edge is **measured-not-sharing reflink → fall through to
  hardlink-under-matrix** (downgrade-for-space allowed; blind upgrade into aliasing risk
  never). Channel-less deployments still prefer reflink on the risk asymmetry: worst case =
  copy COST reported "unverified" vs hardlink's worst case = silent latest/ corruption.
  Stack D5 amended. Investigation tally: five Operator challenges, five outcome changes.
- 2026-07-20: (bl) **qn.5 folds the mirror-ladder ruling into code + docs.** Implemented the
  stack D5 (bi)/(bj)/(bk) ladder in `internal/storage`: the zfs `latest/` mirror now ALWAYS
  clones from `working/` (never `.zfs` — EXDEV every layer), via **(i) hook `mirror` verb
  (host-side reflink + atomic swap, touches only the derived `latest/`, reports SHARED/COPIED)
  → (ii) in-container reflink → (iii) hardlink-under-matrix → (iv) copy**, self-selecting by
  risk dominance; an in-container reflink reports **UNVERIFIED** (no channel yet — statfs
  `f_bavail` is a documented follow-up) and never takes the risky measured-not-sharing→hardlink
  downgrade absent a channel; every mode + honest claim is surfaced (`MirrorReport` / logs /
  `LastMirror()` for health). `deploy/storage.md` + the `quince-zfs-helper` reference gain the
  `mirror` verb. Interface facts 1–2 closed with the three-layer evidence (block cloning works
  at the POOL level but EPERMs in the unprivileged userns; FICLONE-from-snapshot is EXDEV).
  `make gates-go` green (0 lint, race-clean; storage 78.7%); CI proves the fallthrough + the
  hook-verb argv (fake hook), the reflink-shares + host-side-hook paths prove on the lab (gate
  12). **Still uncommitted pending the Operator's ask** (the two CI-half commits stand). Remaining
  gate-12 legs (Operator-driven): the host-side `mirror` verb on the real rpool, iMazing-opens,
  syncoid mid-write, and the 12c destructive matrix (which validates the hardlink tier).
- 2026-07-20: (bm) **qn.5 CLOSED (CI-proven); lab gate 12's remaining hardware legs RE-HOMED to
  qn.4a** (Operator ruling — session cut off after the five-round mirror investigation). Landed on
  `main` in four commits: `285c40b` (storage backends + reconciliation) + `9a4511b` (docs (bd)/(be))
  + `7e34034` (mirror ladder + lab harness) + `3ce5bb1` (docs (bf)→(bl)). **Proven at close:** the
  whole storage subsystem in CI (11 stories + the reconciliation kill-matrix + the D5a anchored-
  filter contract; `make gates`/image/e2e green; coverage storage 78.7% / clonetree 71.4% / store
  80.1% / httpapi 71.8%), plus the real-zfs commit + encrypted `Verify` + the reflink/EPERM/EXDEV
  facts exercised on hardware during the gate-12 investigation ((bf)→(bk)). **NOT proven on
  hardware (re-homed, NOT silently dropped — the qn.2b→qn.7 no-orphan-gate precedent):** the
  host-side `mirror` verb on the real rpool, iMazing-opens, syncoid mid-write, and the 12c
  destructive hardlink-safety matrix. **Owner = qn.4a**, whose first real-backup hardware session
  runs qn.5's storage `Commit` on real traffic (the natural home); the legs are preserved verbatim
  in the qn.5 spec's gate-12 section. Interim note: the `hardlink` mirror/backend tier is
  matrix-unproven until 12c runs (the Operator's rpool uses the reflink hook path, so it isn't hit
  there); the pushed staging image is pre-mirror-ladder and needs a re-push before the qn.4a
  hardware session. Frontier → **qn.4a**.
- 2026-07-20: (bn) **gate-12 legs REDISTRIBUTED by affinity (Operator-ruled, amending (bm)'s
  all-to-qn.4a; a separate qn.4c was considered and rejected as a hollow-goal rung):**
  iMazing-opens + syncoid-mid-write + the live `mirror`-verb proof (`bclonesaved` observed
  moving) → **qn.4a's existing gate** — they are measurements taken during the backup that gate
  already produces, zero added sessions; the **12c destructive hardlink-safety matrix →
  qn.4b's gate** — its transitions (full→incremental, interrupted+next, encryption change;
  iOS-upgrade opportunistic) are engine products of qn.4b's repeated-backup session, where
  driving them costs nothing versus qn.4a's single-backup outing forcing manual rituals.
  Interim safety stands: the hardlink mirror/backend tier is disabled-to-copy (surfaced) until
  the matrix passes — the Operator's rpool runs the hook path and never hits it; ext4-NAS
  deployments get honest copy-mode meanwhile. Roadmap qn.4a/qn.4b gates updated.
- 2026-07-20: (bo) **`rpool/userdata` DECLASSIFIED (Operator ruling), closing the qn.4a-reported
  pattern hit.** The qn.4a build's privacy self-check surfaced that a pattern-list string sat in
  committed public files (a contracts §6 config example + two planning-era decisions-log entries)
  — missed by the (ad) scrub and invisible to the commit-time gate, which greps staged DIFFS
  only. Ruled: the dataset path is acceptable-public (default-pool naming, already implied by the
  public offsite-model narrative); the pattern is removed from the private list; docs and history
  stand; no incident. Standing lesson kept: the gate cannot see pre-existing lines — a
  whole-tree `privacy-scan-all` target remains available as a future hardening if a genuinely
  sensitive pattern is ever added. Bare hostnames/IPs/MACs remain firmly private.
- 2026-07-20: (bt) **qn.4a BUILT (CI) — the backup engine drives idevicebackup2 end-to-end.**
  *(Letter fix 2026-07-20: this entry was originally mislabeled (bp), colliding with the qn.4b
  spec-approval entry below. Every `(bp)` cross-reference in canon + code means that auto-absent
  ruling, so THIS build record was renumbered — to (bt), since (bs) was legitimately taken by the
  gate-15 hardware entry that landed meanwhile — rather than churn 20 references. Out of strict
  alpha order by design; a terminal build record.)*
  Cleared the pre-build spec-review gate: spec + Rule check → **architect APPROVED with three
  amendments (1 startup job-row reconciliation story + explicit two-reconciler order; 2 the
  `waiting_for_device` bound named `const`; 3 the sampler free-space / `disk_low` leg — the
  implementer's "A3", ACCEPTED) + two ratifications (the double-`Verify` stands; `transport:auto`
  stays deferred to qn.4b) + one correction (no rung numbers in the `auto` 422 API string)**, all
  folded in. Shipped: **`internal/backup`** — the `Job` state machine (per-UDID single-flight),
  the `idevicebackup2` streaming supervisor (argv/`setpgid`/group-kill), a transcript-grounded
  tolerant parser, the activity-sampler liveness (staged, passcode-paused, startup-grace, + A3
  free-space `disk_low` warning surfaced via `job.log`/`slog`, never a silent kill), preflight
  (presence + pairing + encryption policy + disk headroom + Seed), the Seed→`Verify`→`CommitJob`/
  `Discard` handoff, and **startup job-row reconciliation** (crash-orphans → `connection_lost`, a
  rolled-forward commit → `succeeded`, run AFTER storage reconciliation); a **`jobs` table +
  registry** in `internal/store` (real `JobReader`, cursor pagination); the **job command surface**
  (`POST /api/jobs` 202/409/422/404/503, `POST …/cancel`, `JobControl` consumer interface, `job.*`
  events) + contracts §1 error codes recorded; the **`quince backup` CLI** (`DriveToCompletion`)
  via a shared `cmd/quince` `buildLiveStack` (serve + CLI); and the **six lab transcripts** +
  meta + a fake-`idevicebackup2` replayer. `make gates`/image/e2e green. **Two RULINGS that drove
  the build (both rung-local, in the qn.4a spec):** (1) *the Wi-Fi torn session is a STALL, not an
  error line* — the lab's `Heartbeat(SleepyTime)` freezes output; the sampler's tree-activity
  timeout produces `connection_lost` (the discriminator vs a survivable silence is tree churn, not
  output); (2) *`idevicebackup2 backup <target>` writes into `<target>/<UDID>/`* while qn.5 expects
  the tree at the work dir — bridged by an engine-side **symlink adapter** (`<UDID>` → work dir),
  no qn.5 change, no tree copy, no committed-state mutation (verify-live on lab gate 15).
  **Coverage:** backup **83.2%**, store 80.8%, httpapi 72.2%, cmd/quince 11.0% (the CLI wiring is
  hardware-exercised); known-untested = the real-`idevicebackup2` argv/symlink-follow + `statfsFree`
  leaf (fake-covered in CI) + `buildLiveStack`/`backupCmd`. **Handoff review of qn.5: clean** (one
  minor — `CommitJob`'s verify-fail branch, now covered by story 6). **Lab gate 15 (real encrypted
  USB backup + kill-matrix + the re-homed gate-12 legs) owned by this rung** — the hardware
  session; NOT proven yet. **Landed on `main` (CI half); gate-15 findings land later as labeled
  commits** (Operator relaxed the usual land-after-hardware order for this rung). Frontier stays
  **qn.4a** until lab gate 15, then → **qn.4b**.
- 2026-07-20: (bp) **qn.4b spec APPROVED; the `auto`-when-absent edge RULED: refuse actionably.**
  Architect ratification of the spec's flagged proposal, encoded into design §4: `auto` resolves
  against current presence only; a device on neither transport → actionable 422, no job minted
  (a guessed transport would persist a dishonest `Job.transport` — the contract stores only
  concrete values; the frozen automation contract's `device_not_visible` no-go shows canon
  already thinks this way; and default-wifi-and-wait would contradict "prefers USB when
  plugged" the moment a cable appears). Explicit `usb`/`wifi` keeps start-then-connect. One
  spec amendment: design §4 DOES change (the absent clause was silent canon — now explicit;
  the spec's "nothing changes" docs line updates accordingly). Everything else approved as
  written, incl. the demo JobControl flip (its own qn.4a-named condition met), the CLI-only
  escape hatches, and the netmuxd started-not-supervised split (the qn.2→qn.2b precedent).
  The consolidated hardware day closes M3: qn.4a gate 15 (CLI USB + kill matrix +
  mirror/iMazing/syncoid) then qn.4b gate 11 (UI both-transports + honest Wi-Fi disconnect) +
  gate 12c (the destructive matrix) in one Operator session.
- 2026-07-20: (bq) **BUG (Operator-found, assigned to qn.4b): Dashboard DeviceCard "Pair"
  navigates without opening the pairing dialog.** Clicking Pair on a dashboard device card
  routes to `/devices/{udid}` (`ui/.../DeviceCard.tsx:88`, a bare `<Link>`) and stops there —
  the user must find + click Pair again. Root cause: qn.3 correctly moved pairing to the
  details page (USB-only, narrated Trust + passcode) but wired the card as a plain navigation,
  not an intent. Expected: clicking Pair *initiates* pairing. Fix (assigned to qn.4b — it is
  already rewiring this exact action row for the live "Back up now" affordance): deep-link the
  navigation with a pair intent (query param or router state) that the details page reads to
  **auto-open the pair dialog** on arrival — keeps qn.3's "narrated flow lives on details"
  decision, just makes the click deliver on its label. Same pattern applies to any future
  card action that lives as a dialog on details. Small; no contract change.
- 2026-07-20: (br) **qn.4b BUILT (CI) — Wi-Fi first-class + transport policy + job-history UI; M3's
  CI half closed.** Cleared the pre-build spec-review gate ((bp)): spec + Rule check → architect
  APPROVED, with the flagged `auto`-when-absent edge **ratified as canon** (refuse actionably, design
  §4). **Handoff review of qn.4a: CLEAN** (no blocker/major; `make gates` green on the inherited tip,
  the consumed seams re-run verbose; one minor coverage finding — the shipped-unexercised
  `wifi-incremental-success`/`encryption-changed` transcripts — **retired** here by a Wi-Fi-success
  story). Shipped: **transport `auto` resolution** in `backup.Engine` (`resolveTransport` — prefer
  USB when present else Wi-Fi, store the CONCRETE `usb`/`wifi` on the `Job` never `"auto"`, absent →
  actionable **422** with no job minted; explicit `usb`/`wifi` keep the start-then-connect wait) +
  httpapi passes `auto` through; the **`quince versions verify <id>|--udid`** + **`quince device
  repair-working-copy <udid>`** CLI escape hatches (design §4; CLI-only, no REST/contract) on a
  factored-out **`buildStorage`** (storage-only, no muxer/registry/engine goroutines) + a thin
  same-track **`storage.Manager.VerifyVersion`/`VerifyLatest`** (resolves the tree via the existing
  `browseRoot` — works for latest/archived/zfs-snapshot, **no new backend method**); the **live demo
  `JobControl`** (`StartBackup`/`CancelJob` scripting on-demand jobs through the real state names, a
  Run()-seeded stable spare device + a seeded failed job so the retry affordance is exercisable,
  per-UDID single-flight shared with the ambient loop) — **reversing qn.4a's demo-503** (its own
  named condition — an e2e that posts jobs — is now met); and the **UI** (live "Back up now" with a
  transport override when on both, one-tap **Retry** on failed intent groups carrying `retry_of`,
  **Cancel** on the running job; details page + dashboard card; assisted narration, honest disabled
  states, no fabricated progress). **Folded the (bq) DeviceCard bug fix** (Operator-found, assigned
  to this rung): the dashboard card's **Pair** now deep-links a pair *intent* (react-router state)
  that **auto-opens the pairing dialog** on the details page — the click delivers on its label, and
  qn.3's narrated-flow-on-details decision stands (no contract change; a Run()-seeded unpaired demo
  device + an e2e assertion prove card Pair → dialog visible). **`make gates` + `make image` +
  `make gates-ui-e2e` green**; new
  e2e **story 4** (Back up now → live cancel → retry a failed backup, all against `--demo`). CI Go
  stories: `auto`→concrete + both→USB, `auto`-absent→422-no-job, Wi-Fi success replay (retires the
  finding), retry-chain, cancel, demo single-flight/cancel/retry, `versions verify` good/torn/unknown.
  **Coverage:** backup **83.4%**, demo **55.3%** (was 0), storage **78.2%**, httpapi 72.2%,
  cmd/quince 8.5%; **known-untested** (accepted debt): the `cmd/quince` CLI command wiring
  (`versions`/`device` verbs + `buildStorage` — the storage/engine logic they call is tested; the
  verbs are hardware/integration-exercised), the demo `waitStep` shutdown-`stop` branch, and the
  storage reflink leaf (unchanged from qn.5). Contracts §1's `auto` note updated to "implemented"
  (docs-part-of-the-diff). **NOT proven on hardware — the consolidated hardware day (architect note,
  (bp)):** qn.4a gate 15 (CLI USB + kill-matrix + mirror/iMazing/syncoid) → **qn.4b gate 11**
  (UI-driven backup over **both** transports + an injected Wi-Fi mid-backup disconnect landing
  `connection_lost`) + **gate 12c** (the destructive hardlink-safety matrix), one Operator session;
  the Wi-Fi legs need netmuxd *running* (started for the session — the binary ships since qn.0;
  co-supervision stays qn.7). Frontier stays **qn.4b** until the hardware day; **M3 closes then.**
  **Landed on `main` (CI half)** per the qn.4a relaxed-order precedent; the lab gate 11/12c findings land later as labeled commits.
- 2026-07-20: (bs) **qn.4a LAB GATE 15 — the engine legs PASSED on real hardware (iPad15,7, iOS 26.5).**
  The CLI-USB + kill-matrix half of gate 15 (the UI-driven both-transports backup moved to qn.4b
  gate 11 per (br); the mirror/iMazing/syncoid zfs legs deferred, below). Driven on the qn.2b/qn.3
  staging CT (managed usbmuxd, live `/dev/bus/usb`, `hardlink` `/backups`); the qn.4a image
  re-pushed as `quince:staging` + redeployed. **Proven end-to-end, both encryption variants:** (1)
  an UNENCRYPTED `quince backup` → committed structure-verified version — qn.5's **unencrypted
  `Verify` branch ran on a real 102 MB plaintext `Manifest.db`** (opened read-only, tables + sampled
  records → blobs), which CI had only faked; (2) after enabling encryption via the pty CLI, an
  ENCRYPTED backup → **A1's encrypted `Verify` branch on real encrypted data** (`Manifest.db` header
  is NOT SQLite-magic + 256 blob shards, verified WITHOUT opening the DB), `encrypted:true`;
  **version rotation** proven (encrypted → `latest/`, unencrypted → `versions/<ts>/`). **Interface
  fact 1 CONFIRMED live** — the real `idevicebackup2` follows the `<target>/<UDID>` **symlink
  adapter** into the qn.5 work dir (2.8 GB landed through it). **Interface fact 5 CONFIRMED** — the
  `backup` child argv/env carries NO password; the device's keybag encrypts (the password set once
  over the `encryption on` pty stayed masked — never in argv/env/logs/context; secrets discipline
  held). **Kill-matrix (backing_up) PASSED:** a hard `SIGKILL` of quince mid-`backing_up` left the
  committed versions **untouched** (never-mutate invariant held under a real crash); on restart,
  reconciliation **swept the orphaned 3.1 GB work dir + flipped the job → `connection_lost`, no
  phantom version** (storage `Scan` → engine job-row, the two-reconciler order). `verifying` is
  equivalent (pre-commit); the `committing` **roll-forward** is CI-proven (story 13) and impractical
  to time on the sub-second hardlink commit — declared, not hardware-run.
  **DEFERRED (named, not dropped) — the zfs legs** (host `mirror` verb / `bclonesaved` moving /
  iMazing-opens / syncoid mid-write): they need the rpool **hook-mode** topology (a forced-command
  SSH credential + a CT mount reconfig with `rbind,rslave`) — disproportionate production-host setup
  for incremental value, since the core zfs facts (reflink/EPERM/EXDEV, `bclonesaved` sharing) are
  already hardware-proven on this exact rpool in gate-12 ((bf)→(bk)). Operator ruling: wind down +
  record; run the zfs legs in a later dedicated session. The **syncoid receive target is prepped**
  on the offsite PVE host (specifics in `local/environment.md`; reachable from the workstation + the
  lab host over the existing inter-host path — no new key needed). (Aside: that host currently runs
  its pools DEGRADED on a known-dropped NVMe — Operator-accepted, to be fixed in person.)
  **FOUR lab findings surfaced + filed as tasks** (invisible to the CI fakes — the gate did its
  job): (i) `deviceops.willEncrypt` maps an ABSENT `WillEncrypt` key (exit-0, empty — a device that
  never set a backup password) to `"unknown"` not `"off"`, so the Manage-encryption UI asks for a
  *current* password on an unencrypted device + the off-warning banner never shows; (ii) **[FIXED 2026-07-20]** `quince
  backup <udid> --transport usb` failed — Go's `flag` stopped at the positional udid, so `--transport`
  was dropped → usage error (CI called `StartBackup()` directly, bypassing arg parsing). Fixed:
  extracted a pure `parseBackupArgs` with a multi-parse loop that honours flags before OR after the
  positional; red→green `TestParseBackupArgs` in `cmd/quince` (coverage 8.5%→14.9%); (iii) the
  version card's `Unlock` button (`ui/src/features/versions/VersionList.tsx:31-33` — a `disabled` qn.8
  placeholder) renders on EVERY version incl. unencrypted ones, implying a password gate an
  unencrypted backup doesn't have; fix = encryption-aware on `version.encrypted` (already used for the
  `unencrypted` badge, contracts §2 / `ui/src/lib/types.ts`): encrypted → `Unlock` (password → browse),
  unencrypted → `Browse` (direct read, no password), per design §7 (unlock is encrypted-only) — inert
  today so UI-polish / qn.8-area, not a functional defect; (iv) the device card lingers on "Backing up 100%" through verify+commit and doesn't
  reflect `device.last_backup` (check the engine sets it on success). (iii)/(iv) may be subsumed by
  qn.4b's landed job-history/backup UI (br) — dedup at fix time. **(v) CONFIRMED + root-caused
  (2026-07-20 zfs session):** `device.last_backup` is populated **only in the `demo` provider**
  (`internal/demo/{script,jobcontrol,fixtures}.go` `refreshLastBackup`) — the REAL path (engine
  `Commit` success + `wire.Device` serialization from the live registry/store) never writes it, so a
  paired device with committed versions shows **"No backups yet"** on the card while the version list
  right below shows them (Operator screenshot: 5 versions — 3 `zfs incremental · structure verified`,
  2 `hardlink` — under a "No backups yet" card). This proves (iv)'s hypothesis; fix = the engine sets
  `device.last_backup {at,job,status}` on commit success (or the device DTO derives it from the latest
  committed version) — dedup with qn.4b's backup UI (br). **(vi) GitHub Actions CI RED on `main` —
  root-caused + fixed (2026-07-20).** Only the `e2e` job failed (`gates`+`image` green), on bu+bv+a
  re-run: the two qn.4b **story4** Playwright tests time out waiting for the demo devices
  `spare-iphone` + `new-iphone` to appear. Root cause: `demo.deviceChurn` reset `p.order` to a
  hardcoded `[phone]`/`[phone,pad]` every 20 s, wiping the on-demand devices `seedOnDemandDevice`
  had appended at `Run()` — so story4 passed only if it ran inside the first 20 s (green at bq on a
  fast runner; reliably red once the runner scheduled story4 later). NOT a code regression (main
  unchanged since bq) — a latent demo bug CI timing finally exposed. **Fix:** churn toggles only the
  pad in `p.order` (new `removeUDID` helper), preserving phone + on-demand devices; stories 1–3 only
  assert phone/pad so they're unaffected. Verified by reading (no local Go toolchain) — CI confirms on
  the next push. **Observations (not bugs):** both
  runs came out `kind:incremental` — `idevicebackup2` did device-relative differentials, and the
  encryption change did NOT force a full backup on this iPad (unlike the lab-log iPhone) → a real
  product question (should the engine pass `--full` on the first backup / after an encryption
  change?); an unencrypted backup on an already-paired, unlocked device needed **no on-device
  passcode** (a D13 nuance — the "every backup" claim looks encryption/Trust-specific); startup
  reconciliation took **~7 s** (storage `Scan` walks `/backups`) — a scaling note for large stores.
  **qn.4a's engine goal — the M3 engine half — is hardware-proven.**
- 2026-07-20: (bu) **decisions-log letter hygiene (two collisions in one review — a process fix).**
  Concurrent appenders (architect + a hardware session + a build session) each guessed "next
  letter" and produced duplicate `(bp)` then `(bs)`. Rule going forward: **letters are cross-reference
  anchors, not sequence guarantees** — on a collision, the *unreferenced* side renumbers to the next
  free letter (grep `^- 2026-07-20: (b?)` first) and leaves a one-line breadcrumb; the *referenced*
  side never moves (churns canon + code). A build/close record out of strict alpha order is fine — a
  reader follows references, not the alphabet. (Fixes this session: (bp)-dup → the qn.4a build record
  became (bt); (bs) stayed the gate-15 entry that owns it.)
- 2026-07-20: (bv) **ownership resolved: qn.4a owns the deferred zfs-hook legs — and the plan
  ambiguity that caused the dispute is fixed.** Operator-flagged: qn.4a's session read the zfs work
  as "deferred to a later session, not mine," while the architect read gate 15(a) ("commit on the
  real zfs backend") as qn.4a-owned. **Both defensible — the plan conflated two things:** gate
  15(a) demanded a zfs-backend commit, but the session validly proved the engine on the `hardlink`
  backend and bundled everything zfs-specific into a deferred pile that enumerated only the
  mirror/iMazing/syncoid extras — never listing **engine→commit-on-zfs** itself, leaving it in a
  seam owned by no named rung ("a later dedicated session" ≠ a rung). **Ruling (Operator): qn.4a
  owns the whole zfs half** (it already holds the topology details — cheaper than re-teaching a
  fresh session); deferred ≠ reassigned, the rung finishes its own gate. **Ambiguity fixed:** the
  pending zfs half is now enumerated explicitly — **engine→commit on the real zfs-hook backend**
  (the implicit item) + host `mirror` verb + `bclonesaved` live + iMazing + syncoid — in the qn.4a
  spec status, the dashboard row, and here. Low risk (both halves independently hardware-proven —
  qn.5's lab harness committed a real 34 GB backup through the zfs backend, qn.4a proved the
  engine→backend handoff on hardlink; only their composition on zfs is unrun). Blocks nothing;
  runs when the Operator stands up the rpool hook topology (likely with qn.4b's gate 11/12c —
  one hook-topology setup serves both). Also fixed en route: the qn.4a dashboard row was stale
  ("Not committed") — reconciled to reflect the landed CI half + the hardware-proven engine legs.
- 2026-07-20: (bw) **qn.4a zfs half PROVEN on real hardware — the engine drives a committed,
  verified version on the real zfs-hook backend, end-to-end.** Stood up the deferred (bv) topology on
  the lab rpool: a throwaway parent dataset, a constrained `quince-zfs-helper` forced-command SSH key
  (create/snapshot/destroy/list/mirror; dataset-destroy + parent-escape both refused, verified), the
  per-device child dataset `rbind,rslave`-propagated host→LXC→container (a host-side `zfs create`
  appears live at `/backups/<udid>`), `storage.backend: zfs, mode: hook`. **The zfs legs (gate
  15(a)+(d), (bv) enumeration):** (a) **engine→commit on zfs** — `quince backup` drove
  `queued→…→succeeded` on the zfs backend; an ENCRYPTED backup (on-device keybag; Manifest carries
  `ManifestKey`+`BackupKeyBag`), the `verifying` state ran A1's Verify on the committed tree,
  `committing` cut the version snapshot `<ds>@quince-<versionID>` (~3.1 GB refer), `latest/`
  reflink-mirrored. (d) **host `mirror` verb + `bclonesaved` live** — the verb ran on the real rpool
  (`mode: hook-reflink`, "zero-space verified"); pool `bclonesaved` moved **+~3 GB** (measured `zpool
  get bclonesaved`, the pool-level way — [[zfs-reflink-clone-facts]], never dataset `used`). (d)
  **syncoid mid-write** — while a second backup was actively writing `working/`, a syncoid pass
  replicated the child dataset to the offsite PVE host: both committed `@quince-*` restore points
  intact (refer matched, working+latest trees present) + a sync-snap captured the dirty in-flight
  `working/`. Offsite replication is safe during an active backup. (d) **iMazing-opens** stays an
  Operator-GUI leg — flagged, not agent-verifiable. **Deploy-doc bugs (surface only once hook mode is
  actually stood up — nobody had; all fixed in `deploy/storage.md`):** (1) the reference helper read
  `target="$2"`, but quince sends the dataset LAST (`create -p <ds>`, `list … -r <ds>`) → it REFUSED
  create+list; now last-arg. (2) the stock image ships no ssh client that `hook_cmd` needs; documented.
  (3) a host-created dataset is root-owned → the unprivileged-userns container can't write `working/`;
  the `create` verb now chowns to the container's mapped uid. Documented the two-hop (LXC + OCI)
  `rbind,rslave` propagation too. **willEncrypt finding strengthened (backlog (bs)-(i)):** `unknown`
  also arises from a COLD-lockdown enrichment race, not only an absent key → preflight hard-fails
  `encryption_required` with no retry even on a device that WILL encrypt; the storage legs set
  `require_encryption: false` (device still encrypts) to test storage, not re-litigate pairing.
  **qn.4a zfs half CLOSED — only iMazing-opens (Operator GUI) remains.** M3's engine goal is now
  hardware-proven on BOTH backends: hardlink engine legs (bs), zfs half (bw).
- 2026-07-20: (bx) **qn.4a close review (architect): clean + strong — two real bugs given a rung
  home.** Verified the (bw) close: zfs half genuinely proven (the (bv) engine→commit-on-zfs seam
  discharged — mirror verb `bclonesaved` +~3 GB pool-level, syncoid mid-write), three deploy-doc
  hook bugs found+fixed on the first real hook-mode stand-up, letters unique, privacy clean, CI
  green on main (the (vi) e2e fix landed). The gap: two of the six lab findings are genuine v0.1-
  quality defects in landed code but were only task-chips with no rung owner — now **assigned to
  qn.4b** (its gate-11 real backup re-exercises both, and (v) already pointed there): **(i)** the
  `willEncrypt`→`unknown` mis-map on unencrypted devices (asks for a non-existent current password,
  no unencrypted-warning banner) + the cold-lockdown enrichment race that hard-fails a legitimate
  encrypted backup at preflight; **(v)** `device.last_backup` written only by the demo provider, so
  a device with real committed versions shows "No backups yet". Findings (iii)/(iv) stay UI-polish
  (subsumed by (v)/qn.4b's UI); (ii)/(vi) already fixed+landed. iMazing-opens rides the qn.4b
  hardware day (30-second Operator GUI check).
- 2026-07-20: (by) **DAILY-DRIVER TARGET set; qn.4b closed (CI); `qn.4c` inserted; netmuxd
  supervision pulled forward; gate 12c deferred past a planned code freeze** (Operator ruling).
  The Operator is heading for a **code freeze + process revamp**, but wants a *personally
  usable* quince first, defined as: **full backup cycle over BOTH transports + live progress
  without a page refresh + the major bugs fixed.** Mapping that to work exposed one unassigned
  piece — **netmuxd co-supervision**. It is genuinely required for *usable* (not merely for the
  proof): nothing starts netmuxd on `compose up`, so Wi-Fi is silently dead after every restart
  and unrecovered on any crash — precisely the qn.2b-for-usbmuxd situation. It is also a modest
  lift: `internal/muxsup` is hardware-proven and structurally generic, needing its hardcoded
  `usbmuxd -f -S <socket>` + **unix-socket** probe generalized to netmuxd's argv + **TCP** probe.
  **Ruled:** (1) **qn.4b CLOSED (CI half landed, complete)** — no session work remains; its
  **gate 11 re-homes to qn.4c** with a named owner (the qn.2b-gate-8→qn.7 pattern), which is
  *more correct*, not merely convenient: gate 11's Wi-Fi leg then runs on **supervised** netmuxd
  — the shape actually deployed — instead of a hand-started one proving a topology nobody runs.
  (2) **New rung `qn.4c`** = netmuxd co-supervision (moved out of qn.7) + qn.4a findings
  (i)/(iv)/(v) (re-pointed from qn.4b), inheriting gate 11. (3) **Gate 12c DEFERRED past the
  freeze** — the destructive hardlink matrix gates a backend the Operator does not run (zfs
  deployment); the hardlink tier stays disabled-to-copy and surfaced, which is already the safe
  interim ((bn)). (4) qn.7 keeps the patched-timeout build, restart-policy tuning, chaos suite,
  liveness thresholds, and the audition — all deferred past the freeze. **No handover session
  was needed for qn.4b:** its worktree was verified to hold ZERO uncommitted work and its branch
  was identical to `main` — the repo (spec + rung report + dashboard + log) *is* the handover,
  which is what the documentation discipline was for. Remaining path to the freeze point:
  **one fresh session (qn.4c) + one hardware day.**
- 2026-07-20: (bz) **qn.4c spec APPROVED; three architect rulings + the netmuxd socket hazard.**
  The spike's headline is a landmine caught by running the shipped binary (the "interface facts
  are looked up" rule earning its keep **again**): with its default `--socket-path`, **netmuxd
  DELETES a live usbmuxd's unix socket and binds its own** — reproduced in the built image
  (`Deleting old Unix socket`, usbmuxd still running with its inode gone = **silent USB
  blackout**). Naive supervision would have made enabling Wi-Fi kill USB. Ruled argv:
  `netmuxd --host <h> --port <p> --socket-path <private> --disable-usb`, with a **loud refusal**
  if that path collides with `devices.usbmuxd_socket`; the session's choice of a private socket
  over `--disable-unix` is **ratified** — the latter puts netmuxd in host mode where it depends
  on usbmuxd being alive, coupling Wi-Fi health to USB health, which is exactly backwards for
  two independent transports. **Rulings:** (1) **`last_backup.job_id` → NULLABLE: APPROVED**,
  landed in contracts §2 ahead of the rung (the qn.2b precedent). Deriving `last_backup` from
  the newest committed VERSION rather than job history is *more correct*: versions are the source
  of truth for "has this device been backed up", so it survives restarts and covers **adopted**
  versions (restored/replicated dataset — the case where "No backups yet" is most insulting),
  which honestly have no job. Semantic shift recorded: `last_backup` now means the last
  SUCCESSFUL backup; a failed last attempt lives in the job history, not here. (2) **One config
  flag: APPROVED** — D12 says config tidiness is a feature, and a second flag would serve a
  topology nobody has asked for while the mixed case still degrades *honestly* via refuse-loudly.
  If a real user ever needs it, one bool splits into two as a compatible migration
  (`manage_muxer: true` → both). (3) **Health shape: CLEAN BREAK recommended** — a `muxers`
  array (each entry naming its role/transport, managed state, and whether rescan applies)
  INSTEAD of keeping the singular `muxer` alongside it. Two overlapping representations rot
  (which is truth when they disagree?), and a top-level `muxer` is now *ambiguous* with two
  daemons; `/api/health` is not frozen and we are the only consumer, so this is the cheapest
  moment. Update any `local/` tooling that greps `.muxer.` in the same pass. **Affirmed:**
  rescan stays USB-only (restarting netmuxd would tear a live Wi-Fi backup — and rescan always
  existed for USB hotplug). **Flagged for the build:** verify finding (iv) is *genuinely*
  subsumed by (v) — if the card has no branch rendering the `verifying`/`committing` phases it
  will still read "Backing up 100%" after `last_backup` is fixed, which would be a small but
  real UI change contradicting "ui/ needs no changes".
- 2026-07-20: (ca) **mDNS-across-the-container-bridge named as an unproven dependency (qn.4c) —
  and it is the Wi-Fi twin of accepted proposal P1.** netmuxd discovers Wi-Fi devices ONLY by
  mDNS; both shipped compose examples run bridged with a published port, multicast does not
  cross that bridge, and **no gate has ever proven Wi-Fi device presence inside the container**.
  So supervising netmuxd may be **necessary but not sufficient** on the shipped deployment shape.
  The session named it rather than assuming it (host networking as the deploy answer, macvlan as
  the alternative) and gate 11(b) settles it on hardware in minutes — the right call. Two
  additions: (a) whatever the gate finds, the Wi-Fi networking requirement is a **first-class
  deployment constraint** in `deploy/`, not a footnote — and if host networking is the answer,
  its security tradeoff (shared network namespace vs. the hardened-profile story) is documented
  honestly; (b) "netmuxd running" ≠ "Wi-Fi works" — a netmuxd that runs while multicast never
  reaches it sees zero devices forever, which is **exactly the shape of accepted proposal P1**
  (a muxer that runs but cannot open devices → actionable onboarding/health warning). The Wi-Fi
  twin should land with P1 in qn.6, or at minimum be recorded beside it.
