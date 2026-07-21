# qn.4c — netmuxd supervision + usability fixes (the DAILY-DRIVER target)

**Goal.** After `compose up` (and after every restart and crash), **both** transports come up by
themselves — quince co-supervises netmuxd exactly as it already supervises usbmuxd — and the three
defects that make a working backup *look* broken are gone: an unencrypted device reads `off` (not
`unknown`), a legitimately-encrypted device is not hard-refused at preflight by a cold-lockdown
race, and a device with committed versions shows its **real last backup** instead of "No backups
yet". Proven by the inherited **lab gate 11**: encrypted backups over both transports driven from
the UI, live progress with no page refresh, the Wi-Fi leg on **supervised** netmuxd surviving a
container restart. This is the Operator's "personally usable" bar and the **code-freeze point**
((by)).

**Status: APPROVED — architect go (spec-review gate cleared, decisions log (bz)/(ca)).** All three
flagged decisions were ruled and are folded in below: (1) **`last_backup.job_id` nullable —
APPROVED**, landed in contracts §2 ahead of the rung (the qn.2b precedent), with the semantic shift
recorded there (`last_backup` = the last **successful** backup; a failed last attempt lives in the
intent-grouped job history); (2) **one config flag — APPROVED** (D12; splits compatibly later if a
real user needs the mixed topology); (3) **health shape — FLIPPED to a clean break**: a `muxers`
array *instead of* the singular `muxer`, because two overlapping representations rot and a
top-level `muxer` is ambiguous with two daemons — `/api/health` is not frozen and quince is its
only consumer, so this is the cheapest moment it will ever be. Also ruled: the netmuxd argv +
private-socket-over-`--disable-unix` choice **ratified**; rescan stays USB-only; the mDNS
dependency is recorded as (ca) with two additions folded into the deploy work below.

## Handoff review of qn.4a + qn.4b (program loop; run-anchored, four dimensions)

Reviewed at `c27dfa8` (this branch is byte-identical to `main`; qn.4a and qn.4b are landed, so the
usual "unlanded branch" case does not apply).

- **Run-anchored.** `make gates` green in `quince-dev` on the inherited tree: Go `-race` all
  packages ok, vault (ruff/mypy/pytest) ok, UI (tsc/eslint/vitest 32/9/vite build) ok. Only
  pre-existing eslint *warnings* (2, `react-refresh/only-export-components` in vendored
  `badge.tsx`/`button.tsx`). Drove the seams this rung consumes: `internal/muxsup` verbosely
  (`StartsAndStops`, `CrashLoopDegrades`, `RefusesServedSocket`, `RescanRestarts` — all PASS,
  82.7%), plus the shipped **netmuxd binary in the built image** (see *Interface facts*, below —
  the review turned up a real hazard there).
- **(a) Seams.** `muxsup.Supervisor` is structurally generic as (by) predicted: only `New`'s
  hardcoded `usbmuxd -f -S <socket>` argv and `probeServed`'s `net.DialTimeout("unix", …)` are
  usbmuxd-specific; the loop, backoff, crash-loop accounting, process-group terminate and rescan
  channel are transport-agnostic. `device.Registry` merges N muxer sources by (source, udid,
  transport) and already retains a per-UDID overlay (`identity`) that survives absence — the same
  shape `last_backup` needs. `backup.Engine.transition/progress` persists **then** emits every
  state change (so `verifying`/`committing` do reach the WS). `httpapi.MuxerControl` is
  consumer-defined with primitives only.
- **(b) Coverage.** qn.4b's known-untested declaration checks out (CLI command wiring, demo
  `waitStep` shutdown branch, storage reflink leaf). One **honest gap found in the code I build
  on**, and it is exactly finding (v): no test asserts that the *live* `wire.Device` ever carries
  `last_backup` — `device.Registry.deviceShellLocked` never sets it, and only `internal/demo`
  populates it. A test that would fail if the behavior broke does not exist because the behavior
  does not exist. Fixed by story 9 (test written first).
- **(c) State honesty.** No overclaiming found in the job/state machine (a version exists only
  post-verify+commit; `succeeded` is written after `CommitJob` returns). Two honesty defects are
  precisely the assigned findings: `willEncrypt` maps "key absent" (exit 0, empty) to `unknown`
  when the honest answer is `off` (finding i-A), and the device card says "No backups yet" while
  the version list below it shows real versions (finding v).
- **(d) Contracts.** Spot-checked `Device`/`Job`/`Version` wire shapes against contracts §2 and the
  jobs/devices endpoints against §1 — they match. `GET /api/health` is **not** in contracts at all
  (its `muxer` slice is qn.2b *rung-ruled* canon described in design §2/§10) — which is why this
  rung may extend it without a contract change (§ *Design*).
- **Triage.** No canon violation, no blocking defect → **no `qn.4b review fix` commits**. The three
  defects in landed code are this rung's assigned scope (findings (i)/(iv)/(v)) and are fixed as
  qn.4c stories, not as review fixes.

## Boundary

**In scope:**

- `core/internal/muxsup` — **generalize the supervisor to any muxer daemon** (spec = name, argv,
  probe network+address, health label) and add the **netmuxd** constructor with a **TCP** probe.
  Everything else (own process group, restart-with-backoff, crash-loop → degraded, refuse-loudly on
  an already-served address, kill-on-shutdown) is inherited unchanged, now exercised for both.
- `core/cmd/quince` — start **both** supervisors under the serve context when `devices.manage_muxer`
  is true and the respective address is configured; hand httpapi a muxer *group*.
- `core/internal/httpapi` — `GET /api/health` grows a per-daemon `muxers` array; `MuxerControl`
  grows `MuxersStatus()`. `POST /api/devices/rescan` semantics are **unchanged** (USB muxer only).
- `core/internal/deviceops` — finding **(i)-A**: `willEncrypt` maps exit-0-with-empty-output to
  `off`; `unknown` is reserved for a real failure/unparseable value. New exported
  `Manager.RefreshEncryption(ctx, udid, transport)` (thin wrapper over the existing `Info` +
  `Enrich`) so a caller can re-read live *and* refresh the registry.
- `core/internal/backup` — finding **(i)-B**: preflight re-reads the encryption state live through
  an optional prober before refusing; finding **(v)**: announce a device update on commit success.
- `core/internal/device` — finding **(v)**: a `last_backup` source hook consulted when merging a
  device, plus `AnnounceBackup(udid)` (re-merge + publish `device.updated`).
- `core/internal/storage` — finding **(v)**: `Manager.LastBackup(udid)` derived from the newest
  committed, non-missing version (read-only; no backend surface).
- `deploy/` — the **Wi-Fi networking requirement** for the managed profile (mDNS reachability), in
  the same spirit as qn.2b amendment 2: without it this rung's own Wi-Fi gate is unrunnable.
- `docs/` — design §2 (supervisor covers both daemons), §3 (`last_backup` semantics), §10 (health
  shape); contracts §6 doc-comment for `manage_muxer` + the §2 `PROPOSED (gap)`; stack D2 gets the
  verified netmuxd invocation facts; progress dashboard + decisions log at rung close.

**Out of scope (explicit):**

- The rest of **qn.7** — patched-timeout libimobiledevice build, the chaos suite, liveness-threshold
  tuning, restart-**policy** config, the netmuxd-USB audition, and a live UI muxer-health panel.
  This rung supervises netmuxd and surfaces its state in `/api/health` + logs; it does not tune it.
- **Live re-supervision on a `manage_muxer` edit** — still process-start-only (qn.2b's stated D12
  exception carries over verbatim; qn.7 owns it).
- **Gate 12c** (destructive hardlink matrix) — deferred past the freeze ((by)); the hardlink tier
  stays disabled-to-copy and surfaced ((bn)).
- **`ui/` behavior** — no component changes. The encryption banner/dialog already branch on
  `backup_encryption === "off"`, and the card already narrates `verifying`/`committing` and renders
  `last_backup` (**verified by running** — see finding (iv) below); both defects are *server-side
  lies*, and telling the truth fixes the UI with no behavior diff. The two UI files this rung does
  touch are `lib/types.ts` (mirroring the ratified nullable `job_id`) and new tests.
- Everything qn.6+.

## Design

Canon implemented (linked, not repeated): design **§2** (muxer supervisor), **§3** (device model —
`last_backup`), **§10** (health surfaces subsystem state honestly), stack **D2** (two-daemon
topology: usbmuxd serves USB, netmuxd serves Wi-Fi), **D12** (config tidiness), **D13** (assisted
model), contracts **§1/§2/§6**.

### Interface facts — verified live this session, not remembered (hard rule; qn.2b amendment 1)

Run against the **shipped** `netmuxd` (pinned `NETMUXD_REF=v0.4.3`, source-built) inside the built
image on the dev box:

1. **Flags** (`netmuxd --help`): `-p/--port <port>`, `--host <host>`, `--plist-storage <path>`,
   `--disable-heartbeat`, `--disable-unix`, `--disable-mdns`, `--disable-usb`,
   `--upstream-usbmuxd [addr]`, `--socket-path <path>` (**default `/var/run/usbmuxd`**). There is
   no `--version`.
2. **It listens on both a TCP address and a unix socket, independently.** With
   `--host 127.0.0.1 --port 27015 --socket-path /var/run/netmuxd --disable-usb` it logged
   `Listening on 127.0.0.1:27015` **and** `Listening on /var/run/netmuxd`; `netstat` confirmed the
   TCP listener and the socket file existed.
3. **HAZARD, reproduced in the shipped image: netmuxd deletes and takes over whatever unix socket
   `--socket-path` names — including a live usbmuxd's.** With usbmuxd running and serving
   `/var/run/usbmuxd`, starting netmuxd with the **default** socket path logged
   `Deleting old Unix socket` → `Binding to new Unix socket` → `Listening on /var/run/usbmuxd`;
   after netmuxd exited, usbmuxd was still alive but its socket inode was gone — a **silent USB
   blackout**. The feasibility lab recorded the same class of accident. → the supervised netmuxd
   MUST NOT use the default socket path (see the argv decision).
4. **The "host mode" warning is unconditional.** `WARNING: Running in host mode will not work
   unless you are running a daemon in unix mode as well` is printed whenever `--host` is passed —
   including when netmuxd *is* serving its own unix socket. It is not a diagnostic; the lab's
   matching failure was caused by `--disable-unix` with **no** unix daemon present, not by `--host`.
5. **Pairing records:** netmuxd reads `/var/lib/lockdown/<UDID>.plist` — the same
   `lockdownSystemDir` quince already restores its persisted pairing records into (qn.3
   amendment 1). No `--plist-storage` needed; Wi-Fi trust works off the records quince manages.
6. **Logging:** netmuxd is silent below `error` unless `RUST_LOG` is set; `RUST_LOG=info` yields
   the discovery/pairing/heartbeat lines the lab used for diagnosis.

*(Still to verify — at the hardware gate, because CI cannot: mDNS reachability from inside the
container. See "The one unproven dependency".)*

### Decisions this rung settles

- **The supervised netmuxd argv (rung-ruled):**
  `netmuxd --host <host> --port <port> --socket-path <path> --disable-usb`, with `RUST_LOG=info`
  injected into the child env when unset.
  - `--host`/`--port` are split from `devices.netmuxd_addr`, making the configured address
    **authoritative** — the same discipline as usbmuxd's `-S` (qn.2b amendment 1).
  - **`--socket-path` (not `--disable-unix`)**: a private path (`<dir of usbmuxd_socket>/netmuxd`,
    default `/var/run/netmuxd`) keeps netmuxd off usbmuxd's socket (fact 3 — proven destructive)
    *and* keeps netmuxd self-sufficient. `--disable-unix` would put it in "host mode", where it
    depends on another unix-mode daemon (fact 4) — i.e. Wi-Fi would break whenever usbmuxd is down.
    A private socket decouples the two daemons; it is also a free escape hatch
    (`USBMUXD_SOCKET_ADDRESS=/var/run/netmuxd`).
  - **Guard:** if the computed netmuxd socket path equals `devices.usbmuxd_socket`, quince
    **refuses to supervise netmuxd** with a loud error (never silently clobbers — fact 3).
  - **`--disable-usb`**: stack D2 rules usbmuxd the USB anchor until qn.7's audition; without this
    flag two daemons claim the same USB device (the lab saw netmuxd enumerate USB). The flag is the
    single line qn.7's single-muxer flip removes.
- **One config flag, per-daemon gating (rung-ruled; flagged for ratification).**
  `devices.manage_muxer: true` means "quince owns the muxers it is configured to reach": supervise
  usbmuxd when `usbmuxd_socket != ""`, supervise netmuxd when `netmuxd_addr != ""`. **No new config
  key** (D12). The mixed topology ("manage usbmuxd, dial an external netmuxd") is not expressible
  as a flag, but it *is* handled: the refuse-loudly probe finds the address already served, does not
  start a competitor, logs loudly and reports that daemon `degraded` — while the muxd client keeps
  dialing it, so Wi-Fi still works. That is qn.2b's accepted degradation, unchanged.
- **Refuse-loudly generalizes by network, not by transport.** The probe becomes
  `net.DialTimeout(<unix|tcp>, <addr>, probeTimeout)`. A successful dial = "someone already serves
  this" → no spawn, `degraded` with the reason. A misconfigured address that quince cannot bind
  crash-loops → `degraded` with the real bind error, which is the honest outcome (no loopback
  heuristics, no silent skipping).
- **Rescan stays USB-only (rung-ruled).** `POST /api/devices/rescan` exists to re-enumerate USB
  devices an unprivileged container's absent hotplug missed (contracts §1). Restarting netmuxd
  would **tear a live Wi-Fi backup** for no benefit, so the group delegates rescan to the usbmuxd
  supervisor only; with no managed usbmuxd it is the existing 409. Written into contracts §1's
  comment so the restriction is canon, not folklore.
- **Health shape — CLEAN BREAK (ruled (bz)-3).** `GET /api/health` **drops** the singular
  `muxer` object and returns `muxers: [{name, role, managed, state, detail, rescan}, …]`, one entry
  per configured daemon (`name` = the daemon, `role` = the transport it serves, `rescan` = whether
  `POST /api/devices/rescan` applies to it). Rationale, recorded: two overlapping representations
  rot — which one is truth when they disagree? — and with two daemons a top-level `muxer` is
  genuinely ambiguous. `/api/health` is design-level canon with quince as its only consumer, so the
  break costs one golden + one runbook line now and would cost a migration later. The UI panel that
  consumes it is qn.7; the shape is documented in design §10, deliberately **not** frozen into
  contracts until then. Any `local/` runbook line quoting the old `muxer:{…}` check is updated in
  the same pass.
- **Finding (i)-A — `willEncrypt` (rung-ruled).** `ideviceinfo -q com.apple.mobile.backup -k
  WillEncrypt` on a device that never set a backup password exits **0 with empty output** (gate-15
  hardware finding). Mapping: `"true"` → `on`, `"false"` → `off`, **exit 0 + empty → `off`**
  (the key is absent because encryption was never enabled), any other exit/unparseable value →
  `unknown`. This is not a guess: absence of the key *is* the device saying it will not encrypt,
  and the UI's unencrypted-warning + enable-flow both key off `off`.
- **Finding (i)-B — preflight re-reads live instead of trusting a cold registry (rung-ruled).**
  The registry's `backup_encryption` can be `unknown` simply because enrichment ran while lockdown
  was cold ((bw)). Preflight therefore, when `backup.require_encryption` is true and the cached
  value is not `on`, calls the optional prober **once** (`deviceops.Manager.RefreshEncryption`,
  which reuses qn.3's `Info` — so it cannot auto-pair a locked device, the qn.3 lab hazard) and
  decides on the **fresh** value: `on` → proceed (the bug fixed); `off` → refuse, unchanged
  actionable message; still `unknown` → refuse with a **different, honest message** ("couldn't
  confirm this device's backup-encryption state — unlock the device and try again") under the same
  `encryption_required` code (no new error code, no new contract surface). The refresh also lands
  in the registry, so the UI's encryption badge self-corrects.
  **Deliberately NOT done:** proceeding on `unknown` and letting the post-hoc `Verify` decide.
  `require_encryption: true` means "do not take an unencrypted backup"; discovering that after
  writing GBs and then discarding is worse than an actionable refusal, and it would let a silent
  policy violation exist for the length of a backup.
- **Finding (v) — `last_backup` is derived from committed versions, and pushed live (contract
  ratified (bz)-1).** The persistent truth is the version registry: a version exists **only**
  after verify+commit, it survives restarts, and it covers **adopted** versions (a dataset
  replicated/restored to a fresh host — explicitly first-class in contracts §2). So:
  - `storage.Manager.LastBackup(udid)` = the newest non-missing committed version → `{at:
    created_at, job_id: version.job_id (null when adopted), status: "succeeded"}`.
  - `device.Registry` gains an injected source hook, consulted when merging a device (same place
    the qn.3 identity overlay is applied), so **every** read path (`GET /api/devices`,
    `GET /api/devices/{udid}`, every `device.*` event) is honest with no cache to go stale.
  - The engine calls `AnnounceBackup(udid)` after a successful commit → one `device.updated` →
    the card updates **without a page refresh** (this is also the fix for finding (iv)).
  - **`last_backup` means the last SUCCESSFUL backup** (design §3 clarified). Failed attempts live
    in job history and the retry affordance; `automation.staleness_days` already reasons about the
    "last good backup", so this is the reading canon already assumed.
  - **Declared limitation:** deleting the newest version updates the card only on the next fetch
    (no live `device.updated` on `version.deleted`). Declared here rather than papered over; a
    bus subscriber is the qn.7-era refinement if it ever matters.
- **Finding (iv) — subsumption VERIFIED by running, not assumed ((bz) build flag).** The architect
  flagged the risk that the card might have no branch for `verifying`/`committing` and would linger
  on "Backing up 100%" even after (v). Checked before committing to the claim: a new
  `DeviceCard.test.tsx` drives the card through `backing_up(100%) → verifying → committing` via the
  live store path and asserts the rendered label each time. **It already narrates them** ("Backing
  up" disappears; "Verifying", then "Committing" appear) — the card renders `humanJobState(state)`,
  and `isRunning` covers both phases. So (iv) is genuinely subsumed by (v): the only thing missing
  was the last-backup line after success. The test is kept as the regression guard.
  **The original observation explained:** the gate-15 sighting was during a **CLI-driven**
  (`quince backup`) run — the CLI is a separate process with its own bus, so the serving process's
  WebSocket never saw those transitions and the card showed the last row it had fetched. Gate 11
  drives from the UI, where the WS path applies.
  **`ui/` boundary correction:** the claim "no UI changes" holds for behavior, but the rung does
  touch two UI files — `lib/types.ts` (mirroring the ratified nullable `job_id`) and the new
  `DeviceCard.test.tsx` (a test). Stated rather than glossed.

### The one unproven dependency: mDNS from inside the container

netmuxd discovers Wi-Fi devices **only** by mDNS (`_apple-mobdev2._tcp.local`). Both shipped
compose examples run the container on a **bridged** network with a published port; multicast to the
LAN is not forwarded across that bridge, so a supervised in-container netmuxd is expected to
discover **nothing** there — and no gate has ever proven Wi-Fi presence inside the container
(qn.2b's Wi-Fi sub-check was recorded as a carried observation, and the staging deployment has run
with netmuxd unconfigured). This is stated, not assumed: the rung ships the deploy documentation
for it and **gate 11 settles it on hardware in minutes** (the phone is in the room):

1. Deployed shape first (bridged). If the device appears on `wifi` — done, nothing to change.
2. Otherwise the managed Wi-Fi profile requires **host networking** for the container
   (`network_mode: host`, which also retires the `ports:` mapping) — the documented requirement,
   with macvlan named as the alternative for hosts where host networking is unacceptable.

The deploy examples land with option 2 documented as a clearly-labelled requirement-if-needed, so
the gate is runnable either way (qn.2b amendment 2's lesson: a spec whose own gate can't be run is
the flaw the rung exists to fix).

**Two additions ruled in (ca):**

- **The Wi-Fi networking requirement is a first-class deployment constraint in `deploy/`, not a
  footnote** — whatever gate 11(b) finds. If host networking is the answer, its **security
  tradeoff is documented honestly** (a shared network namespace gives the container the host's
  interfaces, which is in tension with the hardened-profile story) rather than buried in a comment.
- **"netmuxd running" ≠ "Wi-Fi works".** A netmuxd that runs while multicast never reaches it sees
  zero devices *forever* — a green supervisor over a dead transport, which is exactly the shape of
  accepted proposal **P1** (a muxer that runs but cannot open devices → an actionable
  onboarding/health warning). This rung records the **Wi-Fi twin beside P1** in the proposals
  ledger so it lands with it in qn.6; qn.4c itself only supervises and reports honestly.

## Stories

Each independently checkable; 1–10 are CI (no hardware).

1. **One flag, two daemons.** A pure `plannedMuxers(devicesConfig) []muxsup.Spec` in `cmd/quince`
   returns: both specs when `manage_muxer` and both addresses are set; usbmuxd only when
   `netmuxd_addr` is empty; netmuxd only when `usbmuxd_socket` is empty; none when
   `manage_muxer: false`; and **none for netmuxd** when its socket path would collide with
   `usbmuxd_socket` (loud error). Table test.
2. **netmuxd argv + TCP probe.** `muxsup.NewNetmuxd(addr, log)` produces
   `netmuxd --host <h> --port <p> --socket-path <path> --disable-usb` with `RUST_LOG=info` in the
   child env, and probes `tcp`. The helper-process fake records its argv/env; the test asserts them
   exactly (this is where the verified interface facts are locked down against drift).
3. **Supervision guarantees hold for netmuxd** (the qn.2b guarantees, re-proven for the new spec):
   spawn under the serve context in its own process group; non-zero exit → restart with capped
   backoff; ≥3 consecutive fast exits → `degraded` in health with the last exit reason; serve-ctx
   cancel → the whole group is signalled and it stops **without** a further restart. Parameterized
   over both specs so usbmuxd's proven behavior and netmuxd's are one test body.
4. **Refuse loudly on an already-served TCP address.** A pre-bound listener on the configured port
   → **no child is spawned**, that daemon reports `degraded` with "already served by another
   process", and the muxd client is unaffected (it still dials — a mis-set flag is a visible
   warning, not a black hole).
5. **Health surfaces both daemons — clean break.** `GET /api/health` returns
   `muxers: [{name, role, managed, state, detail, rescan}]` covering every configured daemon, and
   **no longer carries the singular `muxer`** ((bz)-3). Unmanaged/`--demo` reports an entry per
   *dialed* daemon with `managed:false` (never an empty array that reads as "no muxers"). Golden
   regenerated (`make gen-golden`); the `local/` runbook line quoting the old shape is updated.
6. **Rescan stays USB-only.** With both daemons supervised, `POST /api/devices/rescan` → 202
   restarts **only** usbmuxd (the netmuxd child's pid is unchanged); unmanaged → 409, unchanged.
7. **(i)-A — an unencrypted device reads `off`.** Fake `ideviceinfo` scenario "encryption never
   set" (exit 0, empty stdout) → `backup_encryption: "off"`; a real error still → `unknown`;
   `true`/`false` unchanged. Red before the fix, green after.
8. **(i)-B — preflight re-reads live.** With `require_encryption: true` and a registry value of
   `unknown`: a prober returning `on` → the job proceeds past preflight (regression test for the
   cold-lockdown hard-fail); returning `off` → refused with the "enable encryption first" message;
   returning `unknown` → refused with the "couldn't confirm … unlock the device" message. No prober
   wired (e.g. `--demo`) → today's behavior, unchanged.
9. **(v) — `last_backup` is real.** (a) `storage.Manager.LastBackup(udid)` returns the newest
   non-missing committed version, `job_id` null for an adopted one, and nothing for a device with no
   versions; (b) with the source hook wired, `GET /api/devices` and `GET /api/devices/{udid}` carry
   `last_backup` for a device whose versions were committed **before this process started** (the
   restart-survival case that a commit-time cache would fail); (c) a successful commit triggers
   exactly one `device.updated` carrying the new `last_backup`.
10. **(iv) — the card lands on the last-backup line, live.** A new test in the story-4 e2e file
    (`--demo`): start a backup from the **dashboard card** and let the scripted job run to
    `succeeded` (story 4 itself cancels, so this needs its own test) — the card walks
    Backing up → Verifying → Committing and then shows "Last backup … · succeeded" **without a
    reload**, with no progress bar left behind. This proves the UI half; the server half (an
    honest `last_backup` + one `device.updated` on commit) is story 9's Go tests.

**Lab gate 11 — inherited from qn.4b ((by)), one Operator hardware day.** Deploy prepared and
verified container-side by the session; the Operator drives phone + UI (the qn.3/qn.4a pattern).

11. **The daily-driver gate.**
    - **(a) USB, from the browser.** "Back up now" → a full **encrypted** backup to `succeeded`
      with a committed, structurally-verified version; **live progress observed with no page
      refresh** (states walk backing_up → verifying → committing).
    - **(b) Wi-Fi, from the browser, on SUPERVISED netmuxd.** Cable unplugged; nothing started by
      hand — `compose up` alone brought netmuxd up. Backup → `succeeded`. Record the mDNS outcome
      (the bridged-vs-host-networking question above) and the discovery latency.
    - **(c) It survives a restart.** `compose down && up -d` (and a `kill -9` of the netmuxd child):
      netmuxd is respawned by the supervisor, the device returns on `wifi` unaided, `/api/health`
      shows both daemons `running`, and a second Wi-Fi backup succeeds. *(This is the leg that
      justifies pulling the work forward — the failure it prevents is "Wi-Fi silently dead after
      every restart".)*
    - **(d) A Wi-Fi disconnect mid-backup lands honestly.** Drop the device off the LAN during a
      Wi-Fi backup → `connection_lost` after the liveness timeout, work discarded, `latest/`
      untouched, no phantom version; **Retry** from the UI joins the same intent group.
    - **(e) `last_backup` on a device with pre-existing versions.** Immediately after deploy,
      **before** any new backup, the card shows the real last backup (not "No backups yet") — the
      (v) fix on real committed versions, including adopted ones if present.
    - **(f) Encryption honesty.** An unencrypted device shows the "not encrypted" banner + Enable
      (i-A); an encrypted device backed up straight after a cold container start is **not** refused
      at preflight (i-B).
    - **(g) Secrets on hardware.** During the Wi-Fi backup: no password in the child argv,
      `BACKUP_PASSWORD` env count 0, clean logs (re-affirms qn.4a fact 5 on the network path).
    - **(h) iMazing-opens (30-second Operator GUI glance).** The last unverified leg of qn.4a's
      gate 15 ((bw)) — a committed version opens in iMazing.

## Gates

- `make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.
- Stories 1–9 as Go tests; story 10 as the extended Playwright story 4.
- The netmuxd **interface facts** above are re-verified against the image built by this rung
  (`netmuxd --help` + the two runtime observations), recorded in *Rung-ruled decisions* — the
  "looked up, never remembered" rule applied to interfaces, qn.2b amendment 1.
- Lab gate 11 (a)–(h) recorded in the rung report + decisions log. **Provable at rung close:** every
  CI story is self-contained, and gate 11 runs **inside this rung** on the Operator's hardware day —
  no acceptance gate depends on a future rung's deliverable. Gate 12c stays deferred with a named
  owner (post-freeze), which is a re-assignment, not a silent defer.
- Coverage declared at rung end (`muxsup`, `deviceops`, `backup`, `device`, `storage`, `httpapi`,
  `cmd/quince`) with an explicit known-untested list.

## Fixtures

- **Extended helper-process fake** (`GO_WANT_HELPER_PROCESS`, the qn.2b pattern): a fake muxer that
  can serve a **TCP** listener (as well as the existing unix socket), record its argv/env, and exit
  on demand. No committed data, no real daemon in CI.
- **`deviceops` fake scenario "encryption never set"**: exit 0 with empty stdout for
  `ideviceinfo … -k WillEncrypt` — the replay fixture for lab finding (i)-A, **written before the
  fix** (hard rule).
- **Store-derived `last_backup` fixtures**: version rows (committed / adopted / missing) seeded in
  the test store — the replay fixture for lab finding (v), also written before the fix.
- No new `idevicebackup2` transcripts: this rung changes no backup-process parsing.
- **Privacy:** all fixtures synthetic (synthetic UDIDs); the lab evidence behind these findings
  stays Operator-local; `make privacy-check` before every commit; commit messages describe *what*,
  never *where it runs*.

## Rule check (mandatory — written before building)

- **Subprocesses: argv arrays, own process group, supervised, killed on end** (hard rule; design
  §1). netmuxd inherits the rule's poster-child implementation verbatim — `exec.Command` with an
  argv slice (never a shell string), `Setpgid: true`, restart-with-backoff, SIGTERM→grace→SIGKILL to
  the **group** on serve-ctx cancel. The address reaches the daemon through its own verified flags.
  **Complies by construction.**
- **No silent caps or fallbacks** (hard rule). Refuse-loudly generalizes to TCP; a crash-looping or
  refused daemon is `degraded` in `/api/health` **per daemon** plus a loud log; a netmuxd socket
  path colliding with `usbmuxd_socket` is a loud refusal, never a silent clobber (fact 3 proves what
  silence would cost); rescan's USB-only scope is documented in contracts §1 rather than being a
  surprise. **Complies.**
- **State honesty** (hard rule — this rung is mostly *about* it). `last_backup` is derived from
  committed versions only (a version exists only after verify+commit), so the card cannot claim a
  backup that did not happen; `unknown` encryption is never upgraded to a guess — it is re-read, and
  if still unknown the refusal says exactly that; `off` is now reported where the device really
  means off. **Complies.**
- **Config tidiness — D12** (hard rule). **No new config key**; `manage_muxer`'s doc-comment is
  updated to say it governs every configured muxer, and `netmuxd_addr`'s says it is the address the
  managed netmuxd binds. **Near-misses flagged:** (1) the setting still applies **at process
  start** — qn.2b's stated exception, restated here, owner qn.7; (2) the netmuxd unix socket path is
  **derived**, not configurable — a deliberate non-key (it is an implementation detail of the
  managed profile, invisible to the user); if the architect wants it configurable it becomes a key
  with a doc-comment and a default. **Complies (with both near-misses stated).**
- **Secrets discipline** (hard rule). No backup password crosses this rung. netmuxd's argv/env carry
  no secret (`RUST_LOG` only). **Near-miss noted:** netmuxd reads the pairing records under
  `/var/lib/lockdown` — private-key-grade material (design §6) — but that is the same path
  libimobiledevice already uses and quince already manages; no new exposure, no new copy. Its
  `RUST_LOG=info` lines name device UDIDs; those go to container logs (never to committed files),
  which is the same posture as the existing muxd/deviceops logs. **Complies.**
- **Privacy is a commit-time gate** (hard rule). The evidence behind this spec (lab transcripts, LAN
  addresses, device UDIDs) stays in `local/`; the committed spec quotes only loopback addresses and
  tool output with no identifiers. `make privacy-check` before each commit; the branch is swept
  before landing. **Complies.**
- **Version pins / interface facts are looked up, never remembered** (hard rule). No pin changes
  (`NETMUXD_REF=v0.4.3` unchanged). Every netmuxd flag and behavior used here was **executed
  against the shipped binary this session** (§ Interface facts), including the destructive
  socket-takeover, rather than recalled from its README. **Complies.**
- **Never mutate a committed version / storage invariants** (hard rule). This rung only **reads**
  the version registry (`LastBackup`). No backend, layout, commit, or reconciliation change.
  **Complies (read-only).**
- **Every bug found on the lab box becomes a replay fixture before it is fixed** (hard rule).
  Findings (i)-A, (i)-B and (v) each get their fixture/test first (§ Fixtures); (iv) gets an e2e
  assertion. **Complies.**
- **A rung's goal is provable at rung close** (hard rule). CI proves supervision + the three fixes;
  gate 11 proves the daily-driver goal on hardware **in this rung**. The single external unknown
  (container mDNS) is named, carries a documented deploy answer, and is settled by the same gate —
  not deferred. **Complies.**
- **Docs are part of the diff** (hard rule). design §2/§3/§10, contracts §6 doc-comment + the §2
  `PROPOSED (gap)`, stack D2's verified invocation facts, the deploy compose examples, and the
  dashboard/decisions log all land with the code. **Complies.**
- **Contract/boundary discipline** (program loop step 2). One frozen surface is touched —
  `Device.last_backup.job_id` nullable — and it was **ratified before any code**: raised as
  `PROPOSED (gap)` at the review gate, ruled (bz)-1, and landed in contracts §2 on `main` ahead of
  the rung (the qn.2b precedent); this rung rebased onto it and implements the ratified text.
  `/api/health` is design-level canon, not a frozen contract, so the clean break is in bounds.
  `ui/` gets no behavior change (types mirror + tests only). Sole frontier session in these trees.
  **Complies.**

## Rulings received (spec-review gate, decisions log (bz)/(ca))

1. **`last_backup.job_id` nullable — APPROVED**, landed in contracts §2 ahead of the rung.
   Versions are the source of truth for "has this device been backed up"; an adopted version
   honestly has no job, and fabricating one would be the state-honesty violation this project
   forbids. Semantics fixed in the contract: `last_backup` = the last **successful** backup.
2. **One config flag — APPROVED.** D12 tidiness wins; the mixed topology still degrades honestly
   through refuse-loudly, and one bool splits into two as a compatible migration if a real user
   ever needs it.
3. **Health shape — FLIPPED to a clean break.** `muxers` array *instead of* the singular `muxer`
   (folded into the Design + story 5 above). Also affirmed: rescan stays USB-only. Also flagged
   for the build and now discharged: the (iv) subsumption was **verified by running**, not assumed.

## Rung-ruled decisions (settled during the build; *rung-ruled* canon — a later rung changes them only via the gap protocol)

- **netmuxd invocation** = `netmuxd --host <h> --port <p> --socket-path <private> --disable-usb`,
  with `RUST_LOG=info` injected when unset. Verified live against the shipped pinned v0.4.3 (facts
  1–6 above) and re-proven in the image this rung built (see Rung report). `--socket-path` is a
  safety flag, not a preference: netmuxd deletes and rebinds whatever socket it names, and its
  default is usbmuxd's.
- **The netmuxd socket path is derived, not configured**: `<dir of devices.usbmuxd_socket>/netmuxd`,
  else `/var/run/netmuxd`. It is an implementation detail of the managed profile (no D12 key). If
  the derived path would equal `devices.usbmuxd_socket`, quince **refuses to supervise netmuxd**
  (loud error) and falls back to dialing it — never a silent clobber, never a silent drop.
- **One flag, per-daemon gating**: `manage_muxer` supervises usbmuxd when `usbmuxd_socket` is set
  and netmuxd when `netmuxd_addr` is set; `false` dials both and reports them `external`.
- **`/api/health` = `muxers: [{name, role, managed, state, detail, rescan}]`**; the singular
  `muxer` key is removed. States: `starting | running | degraded | stopped | external`.
- **Rescan is USB-only**, delegated by the group to the daemon whose spec carries `Rescan: true`.
  With no managed USB muxer it is an honest 409 with the reason.
- **`willEncrypt`: exit-0-with-empty-output → `off`**; `unknown` is reserved for a genuine read
  failure or an unparseable value.
- **Preflight re-reads encryption live** (once) whenever `require_encryption` is on and the cached
  value is not `on`; decides on the fresh value; a still-`unknown` result refuses with a *different*
  message naming the real cause. Proceeding on `unknown` was considered and rejected.
- **`last_backup` derives from the newest non-missing committed version** (`storage.Manager.
  LastBackup`), read through an injected source at merge time (no cache to go stale), and the
  engine calls `AnnounceBackup` after a successful commit for the live update.

## Rung report (build outcome)

**Handoff review of qn.4a/qn.4b: clean** (four dimensions, run-anchored — see the section at the
top). No canon violation, no blocking defect, so **no `review fix` commits**; the three defects in
landed code were this rung's assigned scope.

**Built (CI-proven).** netmuxd co-supervision (the hardware-proven `internal/muxsup` generalized
to a daemon `Spec` — name/role/argv/probe-network/address — plus `muxsup.Group` for the
two-daemon topology and the `plannedMuxers` resolution table in `cmd/quince`), the clean-break
`muxers` health array, and qn.4a findings **(i)-A**, **(i)-B**, **(iv)**, **(v)**. `make gates`
(go + vault + ui) + `make image` + `make gates-ui-e2e` **green in `quince-dev`**; e2e 6/6.

**The smoke test that matters (built image, real daemons, no hardware).** `quince serve` in the
image quince built this rung:

- `/api/health` → `muxers:[{usbmuxd,usb,managed,running,rescan:true},{netmuxd,wifi,managed,running,rescan:false}]`;
- the child argv is exactly the ruled one: `netmuxd --host 127.0.0.1 --port 27015 --socket-path
  /var/run/netmuxd --disable-usb`, with netmuxd's `RUST_LOG=info` lines in the container log;
- **both sockets coexist** — `/var/run/netmuxd` *and* `/var/run/usbmuxd` — i.e. the spike's
  socket-takeover hazard is empirically avoided in the shipped image;
- TCP `127.0.0.1:27015` is listening;
- `kill -9` of the netmuxd child → **the supervisor respawned it** (new pid) and health stayed
  `running`, while **usbmuxd kept its original pid and a live socket** (`idevice_id -l` exit 0 —
  served, not a stale file). That is gate 11(c)'s guarantee, minus the container restart itself.

**Stories 1–10: all green.** Highlights: the netmuxd argv/probe assertions (story 2) lock the
verified interface facts against drift; the qn.2b supervision guarantees (stories 3–4) now run
**parameterized over both a unix-socket and a TCP daemon**, so netmuxd inherits proof rather than
just code; story 6 asserts a group rescan restarts usbmuxd **exactly once and never netmuxd**;
story 9 covers committed/adopted/missing versions and the restart-survival read path; story 10 is
a new e2e (`--demo`) driving a dashboard-card backup to success and asserting the card lands on
its real last-backup line **with no reload**.

**Coverage (declared).** `internal/muxsup` **86.9%** (was 82.7), `internal/device` **97.8%**,
`internal/backup` **83.8%**, `internal/deviceops` 80.3%, `internal/storage` 78.2%,
`internal/httpapi` 72.0%, `cmd/quince` **20.9%** (was 14.9 — `plannedMuxers` is table-tested),
`internal/demo` 54.9%. **Known-untested (accepted debt):** `buildMuxerGroup` + the `muxerHealth`
adapter (assembly/logging around a table-tested plan — exercised by the image smoke test above);
`Group.Run`'s multi-daemon join (each supervisor's stop is tested individually); `muxsup.Netmuxd`'s
"RUST_LOG already set" branch (env-dependent); `deviceops.RefreshEncryption`'s own error branch
(the engine-side `ok=false` path IS tested via a fake prober).

**Finding filed (pre-existing, out of scope, given a home).** A job's row goes terminal *before*
its work is discarded and the per-UDID single-flight slot is released — correct behavior (a new
job must not race the old one's work dir), but during that window `POST /api/jobs` answers 409
"a backup is already running for this device", which reads as wrong right after the UI announced a
failure. On a multi-GB `Discard` the window is seconds, and qn.4b's one-tap **Retry** sits exactly
there. Surfaced by the full-suite sweep (it made `TestStoryRetryChainFields` fail under load; the
test now waits the window out with a comment pointing at this). Filed as a task; the smallest
honest fix is a distinct reason string for "the previous backup is still cleaning up".

**NOT proven on hardware — lab gate 11 (a)–(h)**, the consolidated Operator day this rung
inherits. Its Wi-Fi legs also settle the **(ca) mDNS question**: whether the deployed (bridged)
shape discovers Wi-Fi devices at all, or whether the documented `network_mode: host` requirement
is needed. `deploy/compose.nas.yml` ships that constraint as a first-class header section with the
honest security tradeoff; `compose.lab.yml` documents the host-run netmuxd equivalent (including
the `--socket-path` warning). **M3's daily-driver bar closes when gate 11 passes.**
