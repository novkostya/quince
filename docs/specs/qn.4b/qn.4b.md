# qn.4b — Wi-Fi first-class + transport policy + job-history UI (closes M3)

**Goal.** Make Wi-Fi a **first-class, UI-driven** backup transport: resolve `transport: auto`
(prefer USB when plugged, Wi-Fi otherwise — design §4 / contracts §6), wire the **"Back up now" +
retry + cancel** controls and the **intent-grouped job-history UI** to the live qn.4a engine, and
complete the `quince` CLI (`versions verify`, `device repair-working-copy`) — such that a user
drives a **real backup over either transport from the browser**, a **Wi-Fi backup interrupted
mid-transfer lands honestly** as `connection_lost` with no version, and the **destructive
hardlink-safety matrix (gate 12c)** is proven on real hardware. Closes **M3**.

**Status: APPROVED — architect go (pre-build spec-review gate cleared, decisions log (bp)).** The
one flagged user-visible edge (`auto`-when-absent) was **ratified as written** — refuse actionably
(422), no job minted — and is now **explicit canon** (design §4 gained the absent clause, (bp)).
The spec's former "design §4 (nothing changes)" line is corrected accordingly (the clause was
*silent* canon before; it is now written). Everything else approved exactly as specced: the demo
`JobControl` flip (its qn.4a-named condition is met), the CLI-only escape hatches, the netmuxd
started-not-supervised split (qn.2→qn.2b precedent), and the privacy near-miss (verify the
pattern file is on the dev box before the landing commit). Build order stays as planned; stories
**2/3/4/5** are the headline CI gates. **Consolidated hardware day** (architect note): qn.4a gate 15
(CLI USB + kill matrix + mirror/iMazing/syncoid) → qn.4b gate 11 (UI both-transports + honest Wi-Fi
disconnect) + gate 12c (the destructive matrix), in one Operator session.

This rung is the **second half of M3** and the closer. qn.4a built the transport-**agnostic** engine
(it already selects the muxer socket by transport and adds `-n` for Wi-Fi — [supervisor](../../../core/internal/backup/supervisor.go)),
replayed the Wi-Fi torn transcript in CI, and left `transport: auto` returning **422** on purpose
((be) reserved first-class Wi-Fi + `auto` for this rung). qn.4b makes that plumbing **reachable and
provable end-to-end**: the resolution policy, the UI controls that produce jobs, and the hardware
gate that proves both transports. It **consumes — does not change** the frozen `Job`, `job.*`
events, and jobs endpoints (contracts §1/§2/§3); it implements stack **D13** (assisted retry, one
tap) + design **§4** (transport policy, repair escape hatch) and the **UI contract** in contracts §2
(history grouped by `intent_id`, client-side).

## Boundary

**In scope:**

- `core/internal/backup` — **`auto` transport resolution.** [`StartBackup`](../../../core/internal/backup/engine.go)
  today rejects `auto` with 422; qn.4b resolves it against **current device presence** (design §4:
  "`auto` prefers USB when plugged, Wi-Fi otherwise") and **persists the resolved concrete
  transport** (`usb`|`wifi`) on the `Job` — never the literal `"auto"`, which is a request-only
  value (contracts §2 `Job.transport` is `usb`|`wifi`). See the flagged rung-local decision for the
  device-absent edge. No state-machine change; resolution happens once, at `Start`, before the job
  is minted.
- `core/internal/httpapi` — accept `transport:"auto"` on `POST /api/jobs` (the engine resolves it);
  the endpoint shape is already frozen (`"usb"|"wifi"|"auto"`). No new frozen shape. The interim
  "422 for auto" note in contracts §1 is updated (docs-are-part-of-the-diff).
- `core/cmd/quince` — **CLI completion** (qn.4a's forward ref; design §4 names them):
  - `quince backup <udid> [--transport usb|wifi|auto]` — `--transport` gains `auto` (default becomes
    `auto`, matching the `backup.transport` config default; rung-local, minor).
  - `quince versions verify <version-id>` (and `--udid <udid>` = the device's latest) — re-runs the
    passwordless **structural** verification (`storage.Verify`) on a **committed** version and prints
    an honest pass/fail + reason. Operator escape hatch; CLI-only.
  - `quince device repair-working-copy <udid>` — the design §4 escape hatch; calls
    `Manager.RepairWorkingCopy` (already exported by qn.5); honest fail when there is no last-good
    version to rebuild from. CLI-only, never automatic in v0.1.
- `core/internal/storage` — **one thin same-track method** `VerifyVersion(id) (VerifyReport, bool)`:
  resolve a committed version's tree path and run `storage.Verify` on it (for `versions verify`).
  This is the qn.4a precedent (qn.4a added `VerifyTree`/`VersionForJob` to `Manager` as same-track,
  no contract change). `RepairWorkingCopy(udid)` already exists — no storage change needed for it.
- `core/internal/demo` — make the demo **`JobControl` live**: `POST /api/jobs` scripts a backup job
  (queued → … → succeeded) and `cancel` ends it, following the existing `scriptPair`/`scriptEncryption`
  pattern. qn.4a ruled demo `JobControl` = 503 because "no e2e posts jobs"; **qn.4b's e2e story DOES
  post a job** (the "Back up now" flow), so the demo command surface becomes real (still no hardware
  in `--demo`). The demo read surface (`Jobs`/`JobLog`) already exists.
- `ui/` — **wire the existing job UI to a live engine** (the components exist from qn.1's demo shell —
  [`JobHistory`](../../../ui/src/features/jobs/JobHistory.tsx) with `groupByIntent`,
  [`JobProgress`](../../../ui/src/features/jobs/JobProgress.tsx), `JobLogPane`):
  - **"Back up now"** — the [disabled button](../../../ui/src/pages/DeviceDetailsPage.tsx) becomes
    live: `POST /api/jobs {udid, transport}`. Default `transport: auto`; a small **transport override**
    (USB / Wi-Fi / Auto) is offered when the device is present on both. Enabled only when the device is
    present on ≥1 transport; otherwise disabled with a "connect the device" tooltip (state honesty —
    no dead-end POST). The active job renders live via the existing `JobProgress`/`JobLogPane`.
  - **Retry** — a one-tap retry on a **failed/`connection_lost`** intent group (D13): `POST /api/jobs
    {udid, retry_of: <group-latest-id>, transport}`; the new attempt inherits `intent_id`, increments
    `attempt`, and folds into the same history group ("Backup completed after 1 retry").
  - **Cancel** — a cancel control on the running job: `POST /api/jobs/{id}/cancel`.
  - The Dashboard **`DeviceCard`** gains a live "Back up now" affordance consistent with the details
    page (device-centric IA, design §4 / ui.design.md — the dashboard is meant to look alive).
- `core/internal/backup/testdata/transcripts` — **wire the two shipped-but-unexercised transcripts**
  the qn.4a handoff review surfaced: `wifi-incremental-success` (the Wi-Fi **success** path) into a
  new CI story; `encryption-changed` into the retry/encryption-policy path where it fits (or declare
  it explicitly as reserved). No new lab extraction unless the lab gate finds a bug (then: a scrubbed
  fixture before the fix — hard rule).

**Out of scope (explicit):**

- **netmuxd co-supervision / Wi-Fi reliability hardening** → **qn.7** (roadmap; owns netmuxd
  co-supervision + the netmuxd-USB audition re-homed in (aw)). qn.4b **dials + uses** netmuxd for
  Wi-Fi (the device registry already does — [live.go](../../../core/cmd/quince/live.go)), exactly as
  qn.2 dialed usbmuxd before qn.2b supervised it. **The Wi-Fi lab gate requires netmuxd running**
  (started via the container entrypoint/compose for the session); auto-start/restart/health is qn.7.
  This is the deliberate first-class-vs-hardened split (near-miss flagged in the Rule check).
- **Automation / opportunity signal / push** (`/api/automation/*`) → **qn.12**. "Back up now" is a
  manual UI action; there is no scheduler, no auto-trigger, no auto-retry (D13).
- **A full server-side Intent entity** → parked (contracts §2). qn.4b groups by `intent_id`
  **client-side** (the `groupByIntent` that already exists); no server grouping endpoint.
- **Content verification / vault** → **qn.8**. `versions verify` is **structural** only
  (passwordless); `content_verified_at` stays null. State honesty: two levels, shown honestly.
- **Storage internals** (backends, journaled commit, reconciliation, mirror ladder) → **qn.5**
  (landed). qn.4b adds only the thin `VerifyVersion` reader; it does not restructure storage.
- **New REST endpoints or contract shapes.** `versions verify` / `repair-working-copy` are **CLI-only**
  operator escape hatches (design §4). qn.4b implements already-frozen surfaces and updates the
  contracts §1 error-code notes; building a new endpoint would be a STOP.

## Design

Canon this rung implements (linked, not repeated): design **§4** (transport policy `auto` =
prefer-USB-when-plugged **and the now-explicit absent clause — refuse actionably when on neither
transport, (bp)**; the assisted retry chain; the `repair-working-copy` escape hatch — "reserved
now, implemented qn.5, never automatic in v0.1"), stack **D13** (assisted model, one-tap retry
carrying `retry_of`/`intent_id`), stack **D2** (netmuxd serves Wi-Fi), the **UI contract** in
contracts §2 (intent grouping client-side), contracts §1 (jobs endpoints — consumed), **§6**
(`backup.transport`), and `ui.design.md` (device-centric IA, assisted narration).

Decisions this rung settles (rung-local unless flagged for architect ratification at the review gate):

- **`auto` resolution (policy is canon; the resolver is rung-local).** On `StartBackup(udid, "auto",
  …)` the engine reads the device from the registry and picks: **present on USB → `usb`; else present
  on Wi-Fi → `wifi`** (design §4 / contracts §6). The **resolved concrete transport** is stored on the
  `Job` and streamed in `job.updated`, so the UI and history always show the real transport, never
  `"auto"` (state honesty). Explicit `usb`|`wifi` is unchanged (still supports the "start, then the
  device appears" `waiting_for_device` flow).

- **`auto` when the device is present on NO transport — RATIFIED canon (design §4, (bp)).** `auto`
  resolves against **current presence only**; if the device is on neither transport, `StartBackup`
  **refuses actionably** — **422** `"device is not currently connected — connect it over USB or Wi-Fi,
  or choose a transport"` — rather than minting a job that guesses a transport and then hangs in
  `waiting_for_device`. Ratification reasoning (architect, recorded for the rung-ruled log): (1)
  `Job.transport` stores only concrete `usb`/`wifi`, so a guess would **persist a fabrication**; (2)
  the frozen **automation** contract's `device_not_visible` no-go shows canon **already refuses
  before create** for an absent device; (3) **default-wifi-and-wait would contradict "prefers USB
  when plugged"** the moment a cable appears. Explicit `usb`|`wifi` keeps the start-then-connect
  `waiting_for_device` flow. 422 reused (no new status code); recorded in contracts §1 at rung close.

- **Demo `JobControl` becomes live (rung-local, demo-only).** `POST /api/jobs` in `--demo` scripts one
  job through the real state names with lifelike timing (reusing the demo bus + `joblog` chunks the
  read surface already emits); `cancel` ends it `cancelled`. Single-flight per UDID is honored so the
  e2e can assert 409. No hardware in `--demo` (the scripted job writes no real tree). This reverses
  qn.4a's `Unavailable` (503) demo decision **because qn.4b's e2e now posts jobs** — the exact
  condition qn.4a named for keeping it 503.

- **`versions verify` = structural, passwordless (rung-local; state honesty).** `Manager.VerifyVersion(id)`
  resolves the committed version's tree and runs the existing encryption-branched `storage.Verify`
  (design §4 / qn.5 A1). It re-affirms `structure_verified_at`; it does **not** touch content
  verification (qn.8). Output states plainly which level ran. A missing/unknown version → honest error,
  never a false "verified".

- **`device repair-working-copy` (rung-local; consumes qn.5).** `quince device repair-working-copy
  <udid>` calls `Manager.RepairWorkingCopy` (zfs: rebuild `working/` from the last-good snapshot;
  namespace: reseed `work/` from `latest/`). No last-good version → honest failure (nothing to rebuild
  from), never a silent half-state (qn.5 story 11 already proves the method; qn.4b adds the CLI verb).

- **UI: assisted, honest, no fabricated progress (design §4 / ui.design.md).** "Back up now" is enabled
  only when the device is present; the transport actually used is shown; `waiting_for_passcode` narrates
  "enter the passcode on the device"; a failed group shows one-tap **Retry** (not a wall of red);
  Cancel really cancels (the engine SIGKILLs the group). Liveness (`silent_but_connected`/
  `suspected_stall`) is displayed from the job, never invented. The disk-low warning already rides
  `job.log` (qn.4a A3) and appears in the live log pane — no new field.

- **Wi-Fi first-class ≠ Wi-Fi hardened (the qn.4b/qn.7 split).** "First-class" = a fully-plumbed,
  UI-selectable transport that completes a real backup end-to-end and fails honestly on interruption,
  proven on hardware this rung. "Hardened" = netmuxd co-supervision + sustained-reliability tuning
  (the 15-min liveness constant, retries-shaping) = **qn.7**. This mirrors usbmuxd (dialed qn.2,
  supervised qn.2b) and honors ruling (h) (Wi-Fi is primary, keeps its own rung + hardware gate inside
  M3, before qn.7).

### Interface facts to verify live (looked up, never remembered; decisions (al))

Verified against the shipped tools/lab at the gate, recorded in Rung-ruled decisions once checked:

1. **`idevicebackup2 -n` selects the network (Wi-Fi) transport** for the `backup` subcommand on a
   Wi-Fi-sync-enabled device (qn.4a wired `-n` from the lab transcripts; confirm a real Wi-Fi backup
   completes through it — the headline of gate 15b).
2. **netmuxd serves the paired device over Wi-Fi** on the lab LAN (the registry shows the device on
   the `wifi` transport; `USBMUXD_SOCKET_ADDRESS=host:port` reaches netmuxd — VERIFIED shape qn.3).
   Wi-Fi sync must be enabled on the device (USB-only to enable, per (ag)) — a lab-gate precondition.
3. **A mid-transfer Wi-Fi disconnect freezes output** (qn.4a interface fact 2: the drop is a *stall*,
   caught by the liveness timeout, not an error line) — re-confirmed live by pulling Wi-Fi mid-backup
   and observing `connection_lost` after the timeout, work discarded, `latest/` untouched.

## Stories

Each independently checkable. CI stories use the qn.4a **fake `idevicebackup2`** + fake muxd/registry
+ qn.5 storage on a temp `/backups` — **no hardware in CI**. UI stories run under the demo provider.

1. **`auto` resolves to the present transport (state honesty).** Device present on USB only →
   `StartBackup(udid,"auto")` mints a job with `transport == "usb"`; Wi-Fi only → `"wifi"`; **both
   present → `"usb"`** (prefer plugged). The `Job.transport` and `job.updated` show the resolved
   concrete value, never `"auto"`. Runs the backup to `succeeded` on the resolved transport.
2. **`auto` when absent refuses actionably (ratified canon, design §4/(bp)).** Device present on
   neither transport → `StartBackup(udid,"auto")` → **422** with the actionable message; **no job row
   minted**, no process, no `Seed`. (Headline CI gate.)
3. **Wi-Fi success replay (wires the handoff-review fixture).** The fake replays
   `wifi-incremental-success.txt` on the `wifi` transport → the engine drives to `succeeded`, a
   committed **verified** version, `Job.transport == "wifi"`. (Closes the qn.4a coverage finding: the
   Wi-Fi success path now has a test that fails if it breaks.)
4. **Wi-Fi mid-backup disconnect lands honestly (the closer's headline).** `wifi-torn-session.txt`
   driven through the **`auto`/Wi-Fi** path → **`connection_lost`**, `Manager.Discard` (namespace:
   `work/<job>` gone; zfs: dirty `working/` reported), **`latest/` untouched, no version**, honest
   `Job.error`. (qn.4a proved this at the engine level; qn.4b proves it end-to-end from the resolved
   transport.)
5. **Retry chain through the API (D13).** A failed job → `POST /api/jobs {udid, retry_of:<id>,
   transport}` → the new job inherits `intent_id` from the chain root, `attempt` increments, both
   persisted + served; `groupByIntent` folds them into one operation.
6. **Cancel through the API.** `POST /api/jobs/{id}/cancel` on a running job → group SIGKILLed, job
   `cancelled`, `Discard` called, no version, `finished_at` set (qn.4a engine behavior, re-asserted
   via the command surface).
7. **`versions verify` CLI.** `quince versions verify <id>` on a good committed version → exit 0 +
   "structurally verified (encrypted|plaintext)"; on a version whose tree is torn (fixture) → nonzero
   + honest reason; unknown id → nonzero "no such version". `--udid` verifies the device's latest.
8. **`repair-working-copy` CLI.** `quince device repair-working-copy <udid>` with a last-good version →
   working area rebuilt (namespace: reseeded from `latest/`; zfs fake: from the snapshot); with **no**
   last-good version → nonzero honest failure, no half-state. (Drives qn.5's `RepairWorkingCopy`.)
9. **Demo command surface is live.** Against `--demo`: `POST /api/jobs {udid}` → 202 + a scripted job
   that progresses to `succeeded` over the WS; a second concurrent POST for the same UDID → **409**;
   `cancel` → `cancelled`. (Enables story 10.)
10. **e2e (Playwright, demo) — "Back up now" + history + retry + cancel (new story 4).** From the device
    details page: click **Back up now** → a job appears and progresses (`JobProgress` + live
    `JobLogPane`), then lands in **Backup history** as one intent group; a **Cancel** on a running job
    ends it; a **Retry** on a failed group starts a new attempt that folds into the same group. Proves
    the whole UI→API→engine(demo)→WS loop. `gates-ui-e2e` stays green (stories 1–3 unchanged).

**Lab gate (manual, hardware — the M3 closer + gate 12c).** On the qn.3-kept **paired staging
container** (managed usbmuxd + live `/dev/bus/usb`, (av)) with **netmuxd started for Wi-Fi** + the PVE
host's real **rpool** (zfs hook mode) + a real iPhone with **Wi-Fi sync enabled**:

11. **Both-transports UI-driven backup (closes M3).**
    - **(a) USB, from the browser.** "Back up now" (auto → resolves USB when plugged) drives a full
      **encrypted** backup to `succeeded` — a committed, structurally-verified version via qn.5's
      `Commit` on real zfs. (Overlaps qn.4a gate 15a; here it is **UI-driven**, not CLI.)
    - **(b) Wi-Fi, from the browser.** With the cable **unplugged** and the device on Wi-Fi (netmuxd),
      "Back up now" (auto → Wi-Fi) drives a backup to `succeeded` — the first-class Wi-Fi proof
      (interface facts 1–2 live). Record the state timeline + timings.
    - **(c) Wi-Fi disconnect mid-backup lands honestly.** Pull Wi-Fi (or drop the device off the LAN)
      **during** a Wi-Fi backup → the job lands **`connection_lost`** after the liveness timeout, work
      discarded, `latest/` untouched, no phantom version (interface fact 3 live; the honest-landing
      gate the closer promises). Retry from the UI → a new attempt in the same intent group.
    - **(d) Secrets on hardware (D13).** Capture argv + env + logs during the Wi-Fi backup: **no backup
      password in argv**, `BACKUP_PASSWORD` count **0**, clean logs (re-affirms qn.4a fact 5 on the
      network path).
12. **Gate 12c — the destructive hardlink-safety matrix ((bn); stack D5).** On the `hardlink` backend
    (and the zfs mirror's hardlink fallback; reflink builds exempt), across repeated real backups:
    byte- and metadata-identity of the previous version through **full→incremental, big-file change,
    SQLite `-wal`/`-shm` companions, deletions, renames, interrupted-backup-then-next-incremental, and
    encryption-settings change** (truncate/chmod/xattr traps included; **iOS-upgrade leg
    OPPORTUNISTIC** — runs at the next real update, a named trigger not a blocker, (bg)/(bn)). Any
    in-place-mutating file class is **proven copied, not linked**. Until this passes, the `hardlink`
    tier stays **disabled-to-copy (surfaced)** — the interim safety from (bn).
    - **Every bug found on the lab box becomes a scrubbed replay fixture (transcript or fake-`zfs`)
      before it is fixed** (hard rule).

## Gates

- `make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.
- **Declare coverage** (program loop step 3): `go test -cover` for `internal/backup` + touched
  packages (`storage`, `httpapi`, `demo`, `cmd/quince`) + the UI tests, plus a **known-untested list**
  (one line + reason each). Explicitly declare the Wi-Fi/`encryption-changed` fixtures now wired (story
  3) so the qn.4a debt is retired.
- CI stories **1–9** as Go tests; **story 10** as Playwright e2e (new story 4). Headline CI gates:
  **story 2 (`auto`-absent → 422, no job)**, **story 3 (Wi-Fi success → committed version)**, **story 4
  (Wi-Fi torn → `connection_lost`, latest untouched, no version)**, **story 5 (retry chain)**.
- **Lab gate 11 + 12** recorded in the rung report + decisions log — the "provable at rung close" rule:
  CI proves `auto` resolution + both-transport success/failure + retry/cancel + the CLI verbs; the lab
  gate proves the real both-transports UI-driven backups + the honest Wi-Fi disconnect + gate 12c on
  hardware. All dependencies are **landed** (qn.4a engine, qn.5 storage, qn.3 pairing, qn.2b muxer);
  the only non-landed piece the Wi-Fi gate needs is **netmuxd running**, started for the session (not a
  future-rung deliverable — the binary ships since qn.0; co-supervision is qn.7's *convenience*, not a
  correctness dependency).

## Fixtures

- **`wifi-incremental-success` wired** into story 3 (was shipped-unexercised — qn.4a handoff finding);
  **`encryption-changed`** wired where the retry/encryption path uses it, or declared reserved.
- A **fake torn-tree committed version** for `versions verify` story 7 (reuse qn.5's synthetic-tree
  builder — a valid encrypted tree for the pass case, a deliberately-torn one for the fail case).
- The **demo scripted-job** timing table (extends the existing demo script) for stories 9–10.
- **Privacy (hard rule):** all fixtures synthetic; `make privacy-check` before every commit; every lab
  bug → a scrubbed replay fixture before the fix; commit messages describe *what*, never *where*.

## Rule check (mandatory — written before building; program spec shape + decisions log (as))

Every hard rule / canon boundary this rung touches *or comes near*, one compliance line each.

- **State honesty** (central here). `auto` stores the **resolved** transport, never `"auto"`; a Wi-Fi
  torn/cancelled/verify-failed run is `connection_lost`/`cancelled`/`failed` with an honest `error` and
  **no version**; the demo scripts a real state progression (no fabricated "done"); `versions verify`
  reports which verification level ran (structural only; `content_verified_at` stays null); retry
  history reads honestly (grouped, not a red wall). **Stories 1/2/3/4/6/7 exercise it. Complies.**
- **Never mutate a committed version / storage invariants.** qn.4b spawns no new writer path — it
  reuses qn.4a's engine (writes only into the `Seed` work area) and adds only **read-only**
  `VerifyVersion` + the qn.5 `RepairWorkingCopy` verb (which rebuilds the *working* area from a
  committed version, never mutating the committed one). Story 4 re-asserts `latest/` untouched on a
  Wi-Fi loss. **Complies.**
- **No silent caps or fallbacks.** `auto`-absent refuses **actionably** (422, flagged), never a silent
  transport guess; the `hardlink` tier stays **surfaced copy-mode** until gate 12c passes ((bn)); a
  disabled "Back up now" carries a reason (device not connected); Cancel is a real kill. **Complies.**
- **Subprocesses: argv, own process group, supervised, killed on end.** Unchanged — qn.4b adds no new
  subprocess; it reuses qn.4a's `idevicebackup2` supervisor (`-n` for Wi-Fi already argv, `setpgid`,
  group-kill). The CLI verbs call in-process storage methods, not subprocesses. **Complies.**
- **Secrets discipline.** No secret is added anywhere. The Wi-Fi `backup` run takes no password (the
  device's keybag encrypts — qn.4a fact 5, re-affirmed on the network path at gate 11d). `versions
  verify` is passwordless/structural (no vault, no key). Test fixtures use `test`. **Complies.**
- **Config tidiness — D12.** qn.4b adds **no config key** — `backup.transport` (incl. `auto`) already
  exists in schema v0 (contracts §6), and this rung finally *implements* its `auto` value. No UI-only
  state, no env var, no secret in the file. **Near-miss flagged:** the transport override in "Back up
  now" is a per-action choice, not persisted config — correct (it's not a deployment setting).
  **Complies.**
- **A rung's goal is provable at rung close.** Every dependency is **landed** (qn.4a engine, qn.5
  storage, qn.3 pairing on the kept container, qn.2b usbmuxd + live `/dev/bus/usb`). **Near-miss
  flagged — netmuxd:** the Wi-Fi gate needs netmuxd *running*, not netmuxd *supervised*; the binary
  ships since qn.0 and the registry already dials it, so starting it for the session (compose/entrypoint)
  makes Wi-Fi provable now — co-supervision is a qn.7 *convenience*, not a correctness gate. The
  `auto`-absent rule is a *policy* decision (flagged), not a gate dependency. **Complies (pending the
  netmuxd-runnable confirmation at the gate).**
- **Every bug found on the lab box becomes a replay fixture before it's fixed.** The gate-11/12 method:
  each lab-found bug → a scrubbed transcript / fake-`zfs` fixture before the fix. **Complies.**
- **Perf budgets.** `GET /api/jobs` still reads the indexed registry (< 100 ms, no fs walk); `job.updated`
  stays throttled ≤ 2/s (qn.4a); the UI history groups client-side over a paged list. `versions verify`
  is an on-demand operator command (not on any request path). **Complies.**
- **Privacy is a commit-time gate.** New fixtures synthetic; `make privacy-check` before every commit;
  commit messages describe *what*, not *where*. **Near-miss flagged:** the worktree `local` symlink is
  set up (mandatory first step done); the dev box's `local/privacy-patterns.txt` must be present before
  the commit that lands qn.4b, or the gate no-ops silently — verified before committing. **Complies.**
- **Version pins / interface facts are looked up, never remembered.** The `-n` Wi-Fi behavior, netmuxd
  reachability, and the mid-Wi-Fi-disconnect stall signature are verified **live** at the gate and
  recorded; the transcripts are the output ground truth; any contradiction with canon is a **gap**.
  **Complies.**
- **Docs are part of the diff.** contracts §1 (the `auto` note → implemented; the 422-absent wording),
  design §4 (**absent clause now explicit** — landed by the architect at approval, (bp); qn.4b
  implements it), stack D13/D2 (implemented), the dashboard + decisions log — all updated at rung end.
  The 12c interim `hardlink`→copy surfacing flips to "matrix passed" if gate 12c is clean. **Complies.**
- **Contract / boundary discipline** (program loop step 2). qn.4b owns the `auto`-resolution slice of
  `backup`, the demo `JobControl`, the CLI verbs in `cmd/quince`, the thin storage `VerifyVersion`
  reader, and the job UI wiring; it **routes already-frozen** surfaces (jobs endpoints, `Job`, `job.*`)
  — implementation, not a contract change. **Near-misses flagged:** (1) `VerifyVersion` is a same-track
  storage addition (qn.4a precedent), not a rewrite; (2) `versions verify`/`repair-working-copy` are
  CLI-only (no new REST/contract); (3) netmuxd supervision + the netmuxd-USB audition are qn.7 — pulling
  them in would be a STOP. **Complies.**

## Rung-ruled decisions (settled during the build; *rung-ruled* canon)

- **`auto`-when-absent RATIFIED at the spec-review gate → refuse actionably (design §4, (bp)).** The
  spec's flagged proposal was approved as written: `auto` resolves against current presence only; a
  device on neither transport → **422**, no job minted. Architect reasoning, recorded: (1) a guessed
  transport would persist a dishonest `Job.transport` (contract stores only concrete `usb`/`wifi`);
  (2) the frozen automation contract's `device_not_visible` no-go shows canon already refuses-before-
  create for an absent device; (3) default-wifi-and-wait would contradict "prefers USB when plugged"
  the moment a cable appears. The absent clause is now **explicit** in design §4 (was silent canon).
- **Consolidated hardware day (architect note, (bp)).** qn.4a gate 15 + qn.4b gate 11 + gate 12c close
  M3 in a single Operator hardware session; the qn.4b lab legs are written to compose with qn.4a's.

- **`versions verify` resolves the tree via `browseRoot`, not a new backend method (rung-local,
  same-track).** The spec sketched a `VerifyVersion(id)`. During the build the tree-path resolution
  turned out to be free: `storage/layout.go`'s existing `browseRoot(...)` (what contracts §2 exposes
  as `Version.browse_root`) already maps any committed version — latest, archived namespace
  (`versions/<ts>/`), or zfs snapshot (`.zfs/…/working`) — to its on-disk tree. So `Manager.VerifyVersion`/
  `VerifyLatest` are thin readers over `browseRoot` + the existing free `Verify`; **no `Backend`
  interface change**, avoiding a per-backend version-path method (the qn.4a `VerifyTree`/`VersionForJob`
  precedent). The admin CLIs run on a factored-out **`buildStorage`** (storage subsystem only — no
  muxer/registry/engine goroutines the full serve stack spins up).
- **The demo on-demand target is a Run()-seeded spare device, not a static-seed change.** The perpetual
  ambient phone loop (which e2e story 2 depends on) keeps the phone busy, so on-demand "Back up now"
  targets a **stable spare iPhone seeded in `Run()`** (present USB+Wi-Fi, encryption on) — kept OUT of
  the static `seed()` so the golden contract tests are unaffected — plus a **seeded failed job** so the
  Retry affordance is exercisable. `StartBackup`/`CancelJob` + the ambient loop share one per-UDID
  `running` set, so single-flight is honest across both (a phone POST during a loop run → 409).
- **Demo timing tuned for a deterministic e2e cancel** (~5.9 s scripted backup): the story-4 cancel is
  reliably clicked mid-flight (queued/preflight window ≥ 1.8 s).

## Rung report (build outcome)

**Handoff review of qn.4a:** **CLEAN** — no blocker/major/canon-violation. `make gates` green on the
inherited `main` tip in `quince-dev`; the seams qn.4b consumes (transport guards, wifi-torn,
retry-chain/intent, jobs read+events, CLI) re-run verbose and pass. Four dimensions: (a) seams — the
transport plumbing already existed (`socketAddr`/`-n`), so qn.4b's Wi-Fi work is resolution+policy+UI,
not plumbing; (b) coverage — declaration verified exact, **one minor finding**: the
`wifi-incremental-success` (Wi-Fi success path) + `encryption-changed` transcripts shipped but
exercised by zero tests → **retired here** (a Wi-Fi-success story wires the former); (c) state honesty
— demo 503, honest terminals, disk_low surfaced, reconcile-before-serve all sound; (d) contracts — Job
shape/endpoints/intent-grouping match. Not a canon violation → no `qn.4a review fix` commit.

**Built (CI-proven):** transport `auto` resolution + the CLI escape hatches (`versions verify`,
`device repair-working-copy`) + the live demo `JobControl` + the Back up now/Retry/Cancel UI, all
against the frozen jobs surface (no contract change; contracts §1's `auto` note updated to
implemented). **Also folded the (bq) DeviceCard bug** (Operator-found, assigned to this rung while it
rewired that action row): the dashboard card's Pair now deep-links a pair intent (react-router state)
that auto-opens the pairing dialog on the details page — qn.3's narrated-flow-on-details decision
stands; a Run()-seeded unpaired demo device + an e2e assertion prove card Pair → dialog visible. **`make gates` (go+vault+ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.**
CI stories 1–9 as Go tests + **e2e story 4** (Back up now → live cancel → retry, `--demo`, no
hardware). Headline CI gates green: **story 2** (`auto`-absent → 422, no job), **story 3** (Wi-Fi
success → committed version — retires the finding), **story 4** (Wi-Fi torn → `connection_lost`,
latest untouched — inherited from qn.4a, re-run), **story 5** (retry chain).

**Coverage (declared, per program loop step 3):** `internal/backup` **83.4%**, `internal/demo`
**55.3%** (was 0.0% — the whole `JobControl` + scripting is now exercised), `internal/storage`
**78.2%**, `internal/httpapi` 72.2%, `cmd/quince` 8.5%. **Known-untested (accepted debt, all low-risk
or hardware/integration-gated):** the `cmd/quince` CLI command wiring (`versionsCmd`/`deviceCmd`/
`withStorage`/`buildStorage`/`backupCmd` — the storage/engine logic they call IS unit-tested; the
verbs are exercised on the hardware gate); the demo `waitStep` shutdown-`stop` branch and the
ambient-loop cancel branch (timing plumbing); the storage reflink leaf (unchanged from qn.5).

**NOT proven on hardware — the consolidated hardware day (owned by this rung; architect note, (bp)):**
qn.4a gate 15 (CLI USB e2e + kill-matrix + mirror/iMazing/syncoid) → **qn.4b gate 11** (UI-driven
backup over **both** transports + an injected Wi-Fi mid-backup disconnect landing `connection_lost`
honestly, interface facts 1–3 live) + **gate 12c** (the destructive hardlink-safety matrix), in one
Operator session; the Wi-Fi legs need netmuxd *running* (started for the session — co-supervision is
qn.7). M3 closes when it passes.
