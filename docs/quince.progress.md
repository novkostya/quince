# quince — progress dashboard

**One-line state.** **qn.1 is BUILT — the app frame stands.** `make gates` (go + vault +
ui), `make gates-ui-e2e` (Playwright stories 1–2), and `make image` are green inside
`quince-dev`. The daemon now has typed config over `config.yml`, SQLite + migrations,
cookie auth with a first-run set-password flow, the event bus, the `/api/ws` socket, the
web-security baseline (CSRF, WS Origin, cookie flags, rate limit, audit), and a `--demo`
mode that scripts fixture devices + a job exercising every WS event; the UI ships the auth
flow, a WS bridge feeding Zustand stores, and the Dashboard / device-details / Settings
pages bound to live demo data. **The frontier is now `qn.2` (muxd client + live device
table).** Spec: [`specs/qn.2/qn.2.md`](specs/qn.2/qn.2.md) (to be written).

| Rung | Title | State |
| --- | --- | --- |
| qn.0 | Floor: scaffold, gates, CI, image | **done** — gates + image green in quince-dev (2026-07-19) |
| qn.1 | Core daemon skeleton + demo mode + UI shell | **done** — full gates + e2e + image green in quince-dev (2026-07-19) |
| qn.2 | muxd client + live device table | **frontier** — outlined in roadmap, spec to be written |
| qn.3 | Device ops + Devices page | outlined |
| qn.4 | Backup engine, both transports + headless CLI | outlined |
| qn.5 | Storage backends (zfs snapshot-native / hardlink / copy) + reconciliation | outlined |
| qn.6 | v0.1 release shape (after qn.7) | outlined |
| qn.7 | Wi-Fi reliability hardening (before v0.1) | outlined |
| qn.8 | Vault: unlock, lazy browse, conformance suite | outlined |
| qn.9–10 | Domain viewers (overview / messages) | outlined |
| qn.11 | Photos viewer | **parked, lowest priority** (icloudpd+Immich cover photos; Apple-thumbnails spike first if revived) |
| qn.12 | PWA + push + schedules | outlined |

**Open questions for the Operator** (tracked here until resolved):
1. LAN registry port + creds (address recorded in `local/environment.md`; env-only,
   never committed).

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
  no-improvising discipline intact. Mirrored in ios-backup-crypt's charter.
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
