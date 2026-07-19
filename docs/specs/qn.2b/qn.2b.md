# qn.2b — muxer lifecycle + hardware proof

**Goal.** In the simple one-container profile (`devices.manage_muxer: true`), `compose up`
brings USB up with no external muxer — quince supervises the in-container usbmuxd — and a
UI **Rescan** re-enumerates a device the unprivileged container's absent hotplug missed;
and qn.2's deferred lab gates (plug/unplug ≤ 1 s, netmuxd-USB audition) are proven on real
hardware. This closes the D12 "Plex-bar" USB gap that qn.2 surfaced.

**Status: BUILT (CI stories 1–6); lab gates 7–8 are the hardware session.** `make gates` +
`make image` + `make gates-ui-e2e` green in `quince-dev`; the supervisor was additionally
smoke-tested against the **real** usbmuxd in the built image (see rung-ruled evidence). Cleared
the pre-build spec-review gate (program loop step 1 + decisions log (as)) — **APPROVED by the
architect with four amendments (folded in below)**. The amendments:
(1) verify the usbmuxd listen-socket flag live in the image, don't assume the env var;
(2) `deploy/` is in scope — a managed-mode compose example, else the rung's own gate is
unrunnable; (3) gate 7's Wi-Fi leg starts netmuxd manually (netmuxd supervision is FULL /
qn.7) — stated so "provable at rung close" isn't overstated; (4) define the two muxer
states left implicit — rescan-while-degraded and a crash-looping child.

## Boundary

In scope:
- `core/` — a **muxer supervisor** (design §2 component; new package `internal/muxsup`):
  spawns/kills the in-container usbmuxd as a supervised subprocess; refuses loudly if the
  socket is already served; powers rescan.
- `core/internal/config` — the `devices.manage_muxer` bool (schema v0; contracts §6 already
  documents it — this rung fills the struct + default + golden).
- `core/internal/httpapi` — `POST /api/devices/rescan → 202 | 409` (contracts §1, already
  frozen) + a consumer-defined `MuxerControl` dep, satisfied by the supervisor.
- `core/cmd/quince` — non-demo wiring: start the supervisor when managed, pass `MuxerControl`
  to the API.
- `ui/` — a **Rescan** control on the Devices view (POST + CSRF; reflects 202 in-progress and
  the 409 "external muxer" reason).
- `deploy/` — a **managed-mode compose example** (amendment 2): USB device mapping, **no** host
  socket bind, `manage_muxer: true`. Realizes `compose.nas.yml`'s aspirational "usbmuxd inside
  the container" comment and documents the host-side USB caveats (device passthrough,
  unprivileged-container perms) in comments per D12. Without it the rung's own gate 7 ("`compose
  up` brings USB up with no host muxer") is unrunnable — the spec would re-create the exact flaw
  the rung exists to fix.
- Lab gates 6–7 inherited from qn.2 (renumbered 7–8 here), run on the physical-presence
  session this rung includes.

Out of scope (explicit), all FULL-muxer work → **qn.6/qn.7** (design §2): netmuxd
co-supervision, a muxer restart-**policy** config, muxer **health surfaced to the UI** (this
rung surfaces managed/degraded state to logs + `/api/health` only, not a live UI panel),
`compose.hardened.yml`, and **live re-supervision on a `manage_muxer` edit** (this rung: the
flag takes effect at process start — see Rule check / D12). Device identity (`name`/`model`/
`ios_version`/`paired`/`backup_encryption` via lockdown) stays **qn.3**; jobs (qn.4), storage
(qn.5), vault (qn.8) stay `httpapi.Empty`. Contracts are **consumed, not changed** — §1
`rescan` and §6 `manage_muxer` were landed ahead of this rung by the architect (decisions log
(ar)); this rung verifies its build matches them and treats any mismatch as a gap.

## Design

- **Muxer supervisor** (`internal/muxsup`, design §1/§2, gap-capture in the qn.2 spec
  appendix). One process tree under the core: `exec.CommandContext(serveCtx, usbmuxd, …)`
  in its **own process group** (`SysProcAttr{Setpgid: true}`), **restart-on-crash with capped
  backoff** (reuse the muxd client's shape — 500 ms → ×2 → 30 s cap), **killed on shutdown**
  by signalling the whole group when `serveCtx` cancels. It adds **no image dependency** —
  usbmuxd already ships (Alpine 3.24 community, decisions log (ak)). Foreground mode
  (`usbmuxd -f`) keeps it a supervised child, not a self-daemonising fork.
- **Verify the listen-socket mechanism live — don't assume the env** (amendment 1; "looked up,
  never remembered" applies to *interface facts*, not just versions). `USBMUXD_SOCKET_ADDRESS`
  is a **client-side** variable (it's what points `idevicebackup2` at netmuxd in the audition) —
  whether the usbmuxd **daemon** honours it for where it *listens* is an unverified guess. At
  build time: run the shipped `usbmuxd --help` in the image and use the **daemon's own flag**
  (`-S` / `--socket` if this build has it) to make `devices.usbmuxd_socket` authoritative; if the
  build has no such flag, run on usbmuxd's default path and **validate `devices.usbmuxd_socket`
  agrees, erroring loudly on mismatch** (never silently listen somewhere the client won't dial).
  The verified flag/behaviour is recorded in the rung-ruled section as evidence.
- **Refuse loudly, never silently adopt** (ruling (ar); design §2). Before the first spawn the
  supervisor **probe-dials** the configured socket. If something already answers, quince does
  **not** start a competing daemon: it logs a loud error, marks the muxer **degraded/unmanaged**
  in `/api/health`, and does not spawn. (`manage_muxer: true` asserts "I own the muxer" — a
  socket already served contradicts that, so honesty beats a silent second daemon fighting over
  the socket.) The muxd **client** still dials configured sockets regardless of who started them
  — so a mis-set managed flag degrades to a visible warning, not a black hole.
- **Two muxer states made explicit** (amendment 4; state honesty). **(a) A crash-looping child:**
  the supervisor tracks the last child exit (code + reason) and, once restarts exceed a small
  threshold in a window, reports the muxer **degraded in `/api/health`** with that last exit
  reason, not merely an ever-growing backoff log line the operator never sees. **(b) Rescan
  while degraded** (the refused / externally-served-at-startup state): rescan **re-probes** the
  socket and, if it is now free, **attempts takeover** (spawn + supervise), turning rescan into
  the recovery path *out* of the degraded state; it returns **409 with a reason** only if the
  socket is *still* externally served. (With `manage_muxer: false`, rescan is always 409 — quince
  never owns the muxer there.)
- **Rescan = a supervised restart, reusing the existing reconcile** (ruling (ar); no new
  device-table code). `POST /api/devices/rescan` restarts the managed usbmuxd (signal the
  group → wait → respawn). usbmuxd enumerates attached USB devices at startup, so the restart
  **re-enumerates** a device the LXC's missing hotplug never delivered. The muxd client's live
  connection EOFs when the daemon dies, its **reconnect loop dials the fresh socket →
  `sink.Reset()` → replay → registry reconcile** (qn.2, now covered end-to-end by
  `muxd.TestClientRunReconnectResetsAndReplays`). So rescan is a new *trigger* for a proven
  path. Returns **202** when `manage_muxer: true`; **409** when `false` (external muxer — quince
  doesn't own it, so it can't restart it). Concurrent/rapid rescans coalesce (a restart already
  in flight is not stacked) — rung-local.
- **Config** — `devices.manage_muxer bool`, default `true` (contracts §6). Added to
  `DevicesConfig`, `Default()`, and the httpapi config golden. Editable in the UI like every
  other key.
- **Wiring** (`cmd/quince`, non-demo). `manage_muxer: true` → construct the supervisor, start it
  under `serveCtx` alongside the muxd clients, pass it to the API as `MuxerControl`.
  `manage_muxer: false` → no supervisor; clients dial external sockets; `MuxerControl` reports
  unmanaged (rescan → 409). `--demo` is untouched (a no-op `MuxerControl` → 409).
- **Hardware-free tests** (`os/exec` `TestHelperProcess`, the stdlib `GO_WANT_HELPER_PROCESS`
  pattern — same discipline as qn.2's fake muxd socket). Re-exec the test binary as a fake
  "usbmuxd" that can (a) exit non-zero to drive the restart path and (b) create + serve the
  socket (reusing muxd's plist fixtures) so an integration test sees spawn → client connect →
  attach. No real muxer or device in CI.

## Stories

1. **Config key.** `devices.manage_muxer` is in schema v0 with default `true`, round-trips
   through `GET`/`PUT /api/config`, and the httpapi config golden is regenerated
   (`make gen-golden`). An unknown value is handled by existing validation rules.
2. **Supervisor lifecycle.** With `manage_muxer: true` the supervisor spawns the usbmuxd child
   under `serveCtx` in its own process group; a non-zero child exit triggers a restart with
   capped backoff; `serveCtx` cancel signals the group and stops **without** a further restart.
   A child that keeps exiting (crash loop) flips the muxer to **degraded in `/api/health`** with
   the last exit reason (amendment 4a), not just an endless backoff log. (helper-process fake;
   assert start, restart-after-crash, clean stop, and degraded-on-crash-loop.)
3. **Refuse loudly.** When the socket is already served at startup, the supervisor does **not**
   spawn a second daemon — it logs a loud error and `/api/health` reports the muxer degraded/
   unmanaged (no silent adoption). (pre-occupy the socket in-test; assert no child spawned.)
4. **Rescan endpoint.** `POST /api/devices/rescan` → **202** with `manage_muxer: true` and
   triggers a supervised restart whose reconnect→Reset→replay re-enumerates the device table;
   → **409** with `manage_muxer: false`. When the muxer is **degraded** (socket externally served
   at startup), rescan **re-probes and attempts takeover**, returning 202 if it can now spawn or
   **409-with-reason** only if the socket is still served (amendment 4b — rescan is the recovery
   path). CSRF-guarded like all mutating endpoints; unknown method/verbs handled by the existing
   chain. (httpapi test + a muxsup integration test showing the re-enumeration reconcile against
   the fake, and the degraded→takeover path.)
5. **No-demo wiring.** Non-demo serve with `manage_muxer: true` starts supervisor + clients;
   with `false` starts clients only and rescan → 409; `--demo` unaffected (rescan → 409).
6. **UI Rescan.** The Devices view shows a **Rescan** control that POSTs `/api/devices/rescan`
   with the CSRF token, reflects the 202 in-progress state, and — when the muxer is external —
   shows the 409 reason (disabled/explained, never a dead button). (vitest; extend an e2e story
   if the demo path allows a 409 assertion.)

**Lab gates (manual, hardware — run on the lab CT this rung, recorded in the rung report; not
CI). These are qn.2's deferred gates 6–7, now owned here (ruling (ar)):**

7. Bring USB up **via the managed-mode compose example** (`deploy/`, amendment 2) on the lab CT —
   `compose up`, no host muxer/socket bind — then plug/unplug the lab iPhone → device appears/
   disappears in the UI **via the supervised in-container usbmuxd** (not qn.2's staging
   socket-bind stopgap): **USB attach/detach within 1 s**. Also exercise **Rescan** with a device
   plugged after startup (the missing-hotplug case) and confirm it appears.
   **Wi-Fi leg (dependency stated — amendment 3):** netmuxd supervision is FULL scope (**qn.7**),
   so for this rung netmuxd is **started manually** alongside the managed usbmuxd; then Wi-Fi
   attach lands **within a few seconds** of the mDNS announcement (record the observed value; an
   honest run isn't failed on the phone's announce timing). This manual step is why the Wi-Fi
   sub-check does not contradict "provable at rung close": the rung's *goal* (simple-profile USB
   up + rescan) is fully self-owned; Wi-Fi presence is a carried-over qn.2 observation, not a new
   supervised capability of this rung.
8. **netmuxd-USB audition on pinned `v0.4.3`** (stack D2), run manually with the raw CLIs
   pointed at netmuxd via `USBMUXD_SOCKET_ADDRESS`, with qn.2's guards intact:
   - **Back up `/var/lib/lockdown` before `idevicepair unpair`** (fresh pairing destroys the
     treasured record); requires physical presence (Trust + passcode); old copies go stale once
     re-paired.
   - **Backup traffic = a manual `idevicebackup2 backup` into a throwaway scratch dir, deleted
     after** — never the real dataset (no storage backends until qn.5; a raw run must not
     pollute the layout). The point is only that traffic crosses the 64 KiB boundary.
   - Clean pass → flip the configured default to single-muxer netmuxd (config-only for the
     N-socket client) and credit the v0.4.3 mux fix. Reproduces `message was too large (65536
     bytes, max = 65535)` → file the upstream issue with the exact log line (patch-in-pinned-
     build optional, the qn.7 pattern). Either outcome is recorded in the rung report + log.

## Gates

- `make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.
- New: supervisor lifecycle/refuse-loudly/rescan tests (stories 2–5) as Go tests; config golden
  (story 1); UI Rescan test (story 6). The managed-mode compose example (amendment 2) is
  syntax-checked (`docker compose config` / equivalent) in the gate ladder where cheap, and
  proven live by lab gate 7.
- Lab gates 7–8: outcome (esp. the audition verdict + any upstream issue link + the
  supervised-USB timings) recorded in the rung report and the progress decisions log. **A rung's
  goal is provable at rung close** (program hard rule): stories 1–6 prove the simple-profile
  USB-up + rescan in CI; gate 7 proves supervised USB + rescan on hardware **this same session**
  via the managed-mode compose; gate 8 runs the audition. The Wi-Fi sub-check of gate 7 uses a
  manually-started netmuxd (its supervision is qn.7) — stated so the provability claim is honest
  (amendment 3). No gate is deferred to a later rung (this rung exists precisely because qn.2
  deferred them).

## Fixtures

- The helper-process **fake usbmuxd** (`GO_WANT_HELPER_PROCESS`) — no committed data; it reuses
  `core/internal/muxd/testdata/` plist sequences when it serves the socket.
- **Every bug found on the lab box becomes a replay fixture before it is fixed** (program hard
  rule) — including the audition's failure signature if it reproduces, with a **synthetic UDID**
  (a real `SerialNumber` is the device UDID — personal data `make privacy-check` blocks).

## Rule check (mandatory — written before building; program spec shape + decisions log (as))

Every hard rule / canon boundary this rung touches *or comes near*, one compliance line each.

- **Subprocesses: argv arrays, own process group, supervised, killed on end** (program hard rule;
  design §1). The supervisor is this rule's poster child: `exec.CommandContext` with an argv
  slice (never a shell string), `Setpgid: true`, restart-with-backoff while serving, group-signal
  on `serveCtx` cancel. The socket path reaches the daemon by its own verified flag/env (never
  interpolated into a shell). **Complies by construction — this rung is the rule.**
- **No silent caps or fallbacks; state honesty** (hard rules). Refuse-loudly-on-served-socket is
  the explicit anti-silent-adoption path; managed/degraded/unmanaged state is surfaced in logs +
  `/api/health`; rescan returns a real **409** when external, never a quiet no-op. **Complies.**
- **Config tidiness is a feature — D12** (hard rule). `manage_muxer` lives in `config.yml`, has a
  sane default (`true`), is UI-editable, and carries a doc-comment. **Near-miss to flag:** the
  rule says a setting "never requires a container restart unless the spec says why" — this rung
  applies `manage_muxer` **at process start only** (live start/stop of the supervisor on an edit
  is FULL scope → qn.7). **Stated here as the why:** spawning/killing the muxer subprocess live is
  beyond MINIMAL; until qn.7 a `manage_muxer` change takes effect on the next serve start, and the
  UI/doc-comment says so. Compliant *because the spec states the exception*.
- **A rung's goal is provable at rung close** (hard rule; the reason this rung exists). The goal
  sentence — simple-profile USB up + rescan — is exercised end-to-end when the rung ends: CI
  proves supervision + rescan (stories 1–6), and the hardware session proves lab gates 7–8 **in
  this rung**, owned here. The **one** future-rung touch is gate 7's Wi-Fi sub-check, which starts
  netmuxd manually (netmuxd supervision is qn.7); this is stated explicitly (amendment 3) and does
  not gate the rung's own goal — Wi-Fi presence is a carried qn.2 observation, not a capability
  this rung claims to supervise. No acceptance gate for the goal depends on a future deliverable.
  **Complies.**
- **Secrets discipline** (hard rule). No backup password crosses this rung; the usbmuxd argv/env
  carry no secret. Fixtures that need a password would use `test` — none here. **Complies (nothing
  to handle; noted because subprocess spawning is nearby).**
- **Privacy is a commit-time gate** (hard rule). CI fixtures use **synthetic UDIDs**; any lab
  capture is rewritten to a synthetic UDID or stays Operator-local; no hostnames/IPs/UDIDs/paths
  enter committed files, commit messages, or branch names; `make privacy-check` before every
  commit. **Flag surfaced during worktree init — now RESOLVED:** `.gitignore` ignored `/local/`
  (directory) but not the `local` **symlink** the worktree-init step creates — reported through
  the Rule check rather than silently fixed, adjudicated by the architect, and landed on `main`
  (`a057783`: pattern changed to `/local`, no slash), rebased into this branch (`git check-ignore
  local` now IGNORED). The doc guarantee is true again.
- **Version pins are looked up, never remembered — and so are interface facts** (hard rule,
  extended per amendment 1). usbmuxd is already pinned via the Alpine 3.24 base (no new pin); the
  audition's netmuxd `v0.4.3` is already pinned (stack D2). The rule's *spirit* also binds the
  usbmuxd listen-socket mechanism: it is **verified live** against the shipped `usbmuxd --help`,
  never assumed from the client-side `USBMUXD_SOCKET_ADDRESS` guess, with a loud error if the
  daemon's flag and `devices.usbmuxd_socket` can't be reconciled. **Complies.**
- **Never mutate a committed version / storage invariants** (hard rule). This rung touches no
  storage. The audition's guard is explicit: a raw `idevicebackup2` run writes only to a
  throwaway scratch dir, deleted after — **never** the dataset (no backends until qn.5).
  **Complies (near — the lab guard).**
- **Docs are part of the diff** (hard rule). contracts §1 (`rescan`) + §6 (`manage_muxer`) +
  design §2 (supervisor) were landed **ahead** of this rung by the architect (ruling (ar)); this
  rung **verifies** its build matches them (not re-adds), and updates the progress dashboard +
  closes the lab-gate-ownership loop. A build-vs-contract mismatch would be a gap, not a silent
  divergence. **Complies.**
- **Contract/boundary discipline** (program loop step 2). qn.2b consumes frozen contracts and owns
  a slice of `core/` + `ui/` + `deploy/` as the sole frontier session — no other agent is in those
  trees; no contract surface is changed here. **Complies.**

## Rung-ruled decisions (settled during the build; *rung-ruled* canon — a later rung changes them only via the gap protocol)

- **usbmuxd invocation = `usbmuxd -f -S <devices.usbmuxd_socket>`** (amendment 1, verified live).
  `usbmuxd --help` in the runtime image (usbmuxd **1.1.1_git20250201**) ships `-S/--socket ADDR|PATH`
  (default `/var/run/usbmuxd`) and `-f/--foreground` — so `-S` makes `devices.usbmuxd_socket`
  authoritative (the client-side `USBMUXD_SOCKET_ADDRESS` env was the wrong guess), and `-f` keeps
  it a supervised child. **Proven end-to-end:** the real daemon starts and listens on the `-S` path
  in the built image — `GET /api/health` returns `muxer:{managed:true,state:"running"}` with
  `usbmuxd v1.1.1_git20250201 starting up` in the log (no real device needed).
- **Package `internal/muxsup`** (supervisor), kept separate from `internal/muxd` (wire client) per
  design §2's two-component split.
- **Supervision shape**: `exec.Command` + `SysProcAttr{Setpgid:true}` (own group); restart backoff
  500 ms → ×2 → 30 s (reused from muxd); terminate = SIGTERM → 3 s grace → SIGKILL to the **process
  group**; stop-without-restart on `serveCtx` cancel.
- **Crash-loop threshold = 3** consecutive fast exits (a child up ≥ 30 s resets the counter) →
  `/api/health` muxer `degraded` with the last exit reason (amendment 4a).
- **Refuse-loudly** = probe-dial the unix socket before the first spawn; served → `degraded` + loud
  error, **no spawn**; the muxd client still dials, so a mis-set `manage_muxer` degrades to a
  visible warning, not a black hole.
- **Rescan** = restart (or take over from `degraded`) the managed daemon, reusing the muxd
  reconnect→`Reset()`→replay reconcile (no new device-table code). `POST /api/devices/rescan` →
  **202** managed / **409** external-or-still-served (amendment 4b: rescan is the recovery path out
  of `degraded`). Rescans serialize on the supervisor's single control channel — a restart already
  in flight isn't stacked; true multi-request coalescing is a documented refinement, not needed for
  a human-clicked button.
- **`MuxerControl`** = consumer-defined in httpapi (primitives only, like `DeviceReader`):
  `Rescan(ctx) (accepted bool, reason string)` + `MuxerStatus() (managed bool, state, detail string)`,
  satisfied by `*muxsup.Supervisor`; `httpapi.UnmanagedMuxer{}` is the stub for external/`--demo`
  (rescan → 409, health `unmanaged`). `NewRouter` defaults a nil `Muxer` to `UnmanagedMuxer{}`.
- **`/api/health`** gains `muxer:{managed,state,detail}` (design §10 — surface muxer state honestly).
- **`devices.manage_muxer`** default **true**, declared **first** in `DevicesConfig` (canonical YAML
  key order, contracts §6); applied at process start (live re-supervision on an edit → qn.7).
- **Rescan 202 body** = `{"status":"rescanning"}` (no `op_id` — fire-and-forget; re-enumerated
  devices arrive via `device.*` WS events, unlike pair/encryption ops).

## Lab finding (2026-07-20) — the managed profile needs a LIVE `/dev/bus/usb`, not `devices:`

Surfaced testing Rescan on the staging CT with a real iPhone ("Rescan didn't work"). **Not a code
defect** — the supervisor + rescan behaved correctly (logs: `usbmuxd shutting down` → `muxsup:
usbmuxd started` → muxd client `reconnecting` on each click). The failure was **USB access into
the container**: a static `devices: [/dev/bus/usb:/dev/bus/usb]` (runc `--device`) **snapshots the
device-node list at container start**, so a device plugged/re-enumerated later never appears inside
the container — usbmuxd (restarted by Rescan) then hits `LIBUSB_ERROR_NO_DEVICE` (`/sys/bus/usb` is
live and shows the device, but the `/dev/bus/usb/BBB/DDD` node is missing), so **restarting the
muxer can never surface it**. In the unprivileged LXC, userns additionally strips the runc-created
node's perms (`c---------`).

**Fix (deploy-only, no code change; validated in a throwaway then deployed to staging):** bind
`/dev/bus/usb` as a **volume** (contents stay live + real perms) and grant char-device access —
Docker: `device_cgroup_rules: ['c 189:* rmw']`; nerdctl/podman/unprivileged-LXC (no
`device_cgroup_rules`): `privileged: true`. With that, the container's usbmuxd connected to the
iPhone and the muxd client held a stable Listen. `deploy/compose.nas.yml` corrected accordingly;
staging switched to `privileged: true` + the live bind. **No replay fixture** — this is a
container `/dev` semantics / deploy-config finding, not a reproducible code path. **Implication for
Rescan's contract:** its "re-detect a missed device" value depends on the deployment giving the
container a live `/dev/bus/usb`; a snapshotting `devices:` mapping silently defeats it. (The lab
gate 7 doing its job — a real device found a real deployment gap the CI fakes could not.)
