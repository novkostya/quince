# quince — progress dashboard

**One-line state.** **qn.1 is BUILT — the app frame stands.** `make gates` (go + vault +
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
**real** usbmuxd in the built image (`/api/health` → `muxer:{managed,state:"running"}`). Its
**lab gates 7–8 (plug/unplug ≤ 1 s, netmuxd-USB audition) await a physical-presence session**.
**qn.3 is the new frontier.** The qn.5-before-qn.4 order swap is also ruled in (ar).

| Rung | Title | State |
| --- | --- | --- |
| qn.0 | Floor: scaffold, gates, CI, image | **done** — gates + image green in quince-dev (2026-07-19) |
| qn.1 | Core daemon skeleton + demo mode + UI shell | **done** — full gates + e2e + image green in quince-dev (2026-07-19) |
| qn.2 | muxd client + live device table | **done** — muxd client + registry + UI; `make gates`/image/e2e green (2026-07-20); lab gates 6–7 → owned by qn.2b |
| qn.2b | Muxer lifecycle + hardware proof (supervision, rescan, lab gates 6–7) | **BUILT (CI); lab gates 7–8 = hardware** — `internal/muxsup` supervisor + `POST /api/devices/rescan` + `devices.manage_muxer` + `/api/health` muxer + UI Rescan; `make gates`/image/e2e green (2026-07-20); supervisor smoke-tested vs the real usbmuxd in the image; lab gates 7–8 (plug/unplug ≤1 s, netmuxd-USB audition) await a physical-presence session |
| qn.3 | Device ops + Devices page | **frontier** — after qn.2b; inherits "enrich muxd devices with lockdown identity" |
| qn.5 | Storage backends (zfs snapshot-native / reflink / hardlink / copy) + reconciliation | outlined — **runs BEFORE qn.4** (order ruled in (ar)) |
| qn.4 | Backup engine, both transports + headless CLI | outlined — after qn.5; closes M3 with the integrated e2e gate |
| qn.6 | v0.1 release shape (after qn.7) | outlined |
| qn.7 | Wi-Fi reliability hardening (before v0.1) | outlined |
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

**Decisions log.**
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
