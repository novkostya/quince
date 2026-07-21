# qn.4a — backup engine + supervisor + minimal CLI (USB gate)

**Goal.** A transport-agnostic backup **job engine** drives `idevicebackup2` as a supervised
subprocess through the frozen state machine (`queued → waiting_for_device → preflight →
backing_up → verifying → committing → succeeded`, with `failed / cancelled / connection_lost`
terminals), such that a real **encrypted USB backup driven from a minimal headless CLI** runs
end-to-end into a **committed, structurally-verified version** in qn.5's store — and **every lab
transcript, including the Wi-Fi torn sessions, replays to its honest terminal state in CI with no
phone in the loop.**

**Status: APPROVED — architect go + Operator go (pre-build spec-review gate cleared, decisions log
(as)).** Folded in below: **amendment 1** (a startup job-row reconciliation story + explicit
two-reconciler ordering), **amendment 2** (the `waiting_for_device` bound named as a `const`,
recorded with the other named constants), **amendment 3** (the activity sampler's free-space /
`disk_low` leg — the Operator's "A3", **accepted**); the `transport: auto` 422 message
de-processed (no rung numbers in a user-facing string, log may cite the rung); and two
ratifications recorded — the double-`Verify` stands as written, and `transport: auto` stays
deferred to qn.4b.

**BUILT (CI-proven) + lab gate 15 PASSED on hardware — engine legs (bs) + zfs half (bw).** `make
gates` + `make image` + `make gates-ui-e2e` green in `quince-dev`; CI stories 1–14 pass. Gate 15:
the CLI-USB engine legs passed on the hardlink backend (bs), and the zfs half — engine→commit on the
real zfs-hook backend, host `mirror` verb + `bclonesaved` live, syncoid mid-write — passed on the
real rpool (bw); only **iMazing-opens** (Operator GUI) is unverified. See the **Rung report** and
**Rung-ruled decisions** at the end. Landed on `main`.

This rung closes M3's engine half. It is the FIRST rung that runs a real backup end-to-end, so it
is where qn.5's storage `Seed`/`Verify`/`Commit`/`Discard` seam meets real traffic (the reason
qn.5 was ordered before qn.4, decisions (ar)). It consumes — does not change — the `Job` object,
the `job.*` events, and the jobs endpoints frozen since qn.1 (contracts §1/§2/§3), and fills the
`httpapi.JobReader` seam qn.1 stubbed with `Empty`. It implements stack **D3** (the
never-mutate-committed state machine around `idevicebackup2`) + **D13** (the assisted model — no
unattended backups, no auto-retry) and design **§4** (the job state machine).

**qn.4b** (the next rung) makes Wi-Fi first-class, resolves `transport: auto`, adds the
intent-grouped job-history UI, completes the CLI (`versions verify`, `repair-working-copy`
surface), and owns the 12c destructive hardlink-safety matrix ((be), (bn)). This split is
**explicitly not a Wi-Fi demotion** — ruling (h) stands: the engine is Wi-Fi-shaped from day one
(it replays the Wi-Fi torn transcripts in CI), and Wi-Fi keeps its own rung + hardware gate inside
M3, before qn.7.

## Boundary

**In scope:**

- `core/internal/backup` (**new package**) — the whole engine:
  - the **`Job` state machine** driving the frozen states (design §4), one goroutine per running
    job, **per-UDID single-flight** (never two concurrent jobs for one UDID — design §4 invariant;
    also required because qn.5's `Seed` is destructive/re-seeding);
  - the **`idevicebackup2` supervisor** — a *streaming* argv subprocess (never a shell string) in
    its own process group (`setpgid`), ctx-/cancel-killed as a group, pointed at the muxer via
    `USBMUXD_SOCKET_ADDRESS` (the qn.3 `deviceops` discipline, extended from one-shot
    `CombinedOutput` to a line-streamed stdout/stderr reader);
  - the **output parser** — maps `idevicebackup2` lines to progress (`receiving` phase, file
    counts, percent when present), detects `*** Waiting for passcode ***` → the
    `waiting_for_passcode` phase, `Backup Successful` → the process-success signal, and the
    connection-lost / `-4` / error signatures → `connection_lost`/`failed`;
  - the **activity-sampler liveness** — staged `active → silent_but_connected → suspected_stall`
    judged by cheap tree sampling (size, file count, newest mtime/`Manifest.db`+journal churn),
    **not byte-growth alone**; **paused during `waiting_for_passcode`**; a zero-activity timeout
    (design §4's 15 min, a named constant "tuned in qn.7") that a patched-long libimobiledevice
    timeout can't be undercut by;
  - **preflight** — device present on the chosen transport (`validate` via qn.3), disk-headroom
    check, the `require_encryption` policy check (§3), and qn.5 `Seed()`;
  - the **verify → commit handoff** — process success (exit 0 + `Backup Successful`) → `verifying`
    (`storage.Verify` on the produced tree) → `committing` (qn.5 `Manager.CommitJob`) →
    `succeeded` with `version_id`; any failure → `failed`/`connection_lost`/`cancelled` +
    `Manager.Discard`;
  - the **fake `idevicebackup2` replayer** (`GO_WANT_HELPER_PROCESS`, the qn.2b/qn.3 discipline)
    that replays a transcript on stdout/stderr with its original timing/silences, writes the
    matching tree into the target dir (so `storage.Verify` runs on it), and exits with the
    transcript's code.
- `core/internal/store` — a **`jobs` table migration** + a **jobs registry** (insert / update
  state+progress / get / list-by-udid with cursor / set-terminal) = the real `JobReader`, plus a
  **job-log store** (bounded in-memory ring for the live tail + a durable text tail for
  `GET /api/jobs/{id}/log` across a WS reconnect — the shape qn.1's review added for demo, made
  real). `intent_id`/`attempt`/`retry_of` persisted honestly.
- `core/internal/httpapi` — fill `JobReader` (`Jobs`/`Job`/`JobLog`) from the registry; implement
  the frozen **command surface**: `POST /api/jobs {udid, transport, retry_of?}` → 202 `Job` (409
  when a job for that UDID is already running), `POST /api/jobs/{id}/cancel` → 202 `Job`; publish
  `job.updated` (throttled ≤2/s) + `job.log`. A `JobControl` consumer-interface primitive (the
  established `DeviceOps`/`VersionAdmin`/`MuxerControl` pattern) — **no new frozen shape**.
- `core/cmd/quince` — non-demo wiring of the engine + jobs registry as `JobReader`/`JobControl`;
  the **minimal headless CLI** `quince backup <udid> --transport usb|wifi` that drives one job to
  a terminal state, streaming state/progress to stdout and exiting 0 on `succeeded` / nonzero
  otherwise. **This CLI is the rung's own lab-driving harness** — its bulk *is* the engine working
  ((be): the CLI is not a separate milestone).
- `core/internal/backup/testdata/transcripts` — **extract the lab transcripts** into the six named
  `*.txt` fixtures + `*.meta.json` companions (the task the qn.0 README lays down), scrubbed of
  UDIDs/device-names/paths (synthetic placeholders).
- `core/internal/demo` — the demo `Provider` already scripts a lifelike job (queued →
  waiting_for_device → backing_up with `active → silent_but_connected → suspected_stall → active`
  → succeeded) and serves `Jobs`. Keep it conformant; **demo stays the demo `JobReader`/`JobControl`**
  (no hardware in `--demo`); `POST /api/jobs` in demo triggers one scripted job so the API is
  exercisable without a phone.

**Out of scope (explicit):**

- **Wi-Fi first-class + `transport: auto` resolution + transport policy** → **qn.4b**. The engine
  is transport-agnostic (transport is a parameter that selects the muxer socket + is stored on the
  `Job`), and it **replays the Wi-Fi torn transcripts in CI** — but qn.4a implements only explicit
  `usb`|`wifi`; `auto` is **not resolved this rung** (see the rung-local decision below).
- **Intent-grouped job-history UI** + the "Back up now" button wiring + `versions verify` /
  `repair-working-copy` CLI surface → **qn.4b**. qn.4a serves the jobs **API + the `backup` CLI**
  only; it **persists** `intent_id`/`attempt`/`retry_of` honestly (the fields the grouping UI
  reads), but renders no history UI.
- **The 12c destructive hardlink-safety matrix** → **qn.4b's gate** ((bn) — its transitions are
  products of qn.4b's repeated-backup session). qn.4a's lab gate inherits the *other* re-homed
  gate-12 legs (host `mirror` verb live, iMazing-opens, syncoid mid-write — (bm), (bn)).
- **Storage internals** (backends, journaled commit, reconciliation, `Verify`'s tree inspection,
  retention) → **already qn.5** (landed). qn.4a *calls* `storage.Manager` + `storage.Verify`; it
  does not reimplement or restructure them. Extending the `Manager` with a thin method is allowed
  only if a seam gap forces it (flagged below), never a rewrite.
- **Vault / content verification** → **qn.8**. `content_verified_at` stays null; the engine sets
  only `structure_verified_at` (via qn.5's commit). State honesty: two verification levels, shown
  honestly.
- **Automation / opportunity signal / push** (`/api/automation/*`) → **qn.12**. qn.4a is
  UI/CLI-triggered only.
- **Contracts are consumed, not changed.** `Job`, `job.updated`/`job.log`, `POST /api/jobs`,
  `GET /api/jobs`, `GET /api/jobs/{id}`, `POST /api/jobs/{id}/cancel`, `GET /api/jobs/{id}/log`,
  and `backup.{transport,require_encryption}` are already frozen (contracts §1/§2/§3/§6). qn.4a
  implements them and treats any build-vs-contract mismatch as a **gap** (protocol), not a silent
  divergence.

## Design

Canon this rung implements (linked, not repeated): stack **D3** (never-mutate-committed state
machine around `idevicebackup2`, two-layer verify, journaled commit — the commit half is qn.5's),
**D9** (fixtures from real transcripts; the fake-CLI replay discipline), **D13** (assisted model —
on-device passcode per backup, no unattended mode, no auto-retry; failed → honest `user action
required`, one-tap retry carrying `retry_of`/`intent_id`); design **§4** (the job state machine,
liveness sampler, discard-or-commit), **§1/§6** (subprocess hygiene), **§10** (observability);
contracts **§1** (jobs endpoints), **§2** (`Job` object + the intent model), **§3** (`job.*`
events), **§6** (`backup.*` config).

Decisions this rung settles (rung-local unless a `PROPOSED (gap)` block says otherwise):

- **The engine shape.** `backup.Engine` owns a per-UDID single-flight map and a jobs registry
  handle; `Start(udid, transport, retryOf)` mints a `Job`, rejects if one is already running for
  that UDID, and launches one `run` goroutine. `run` is the state machine; it publishes every
  state/progress transition through a `bus`-backed sink (throttled ≤2/s for progress, immediate
  for state changes) and to the jobs registry. The engine holds a `context` per job so `cancel`
  and shutdown kill the process group deterministically.
- **The supervisor.** `idevicebackup2` is run as `argv` (looked up live — see interface facts),
  in its own process group, with `USBMUXD_SOCKET_ADDRESS` set for the job's transport (reusing the
  `deviceops` env helper's shape). stdout+stderr are read line-by-line on their own goroutines
  into the parser and the job-log store; the process group is SIGKILLed on cancel/ctx/timeout.
  Long silences are expected and normal (D3 lab grounding) — the supervisor never kills on silence
  alone; only the liveness timeout (below) or an explicit cancel kills.
- **The parser is transcript-grounded, not guessed.** Its recognizers are derived from the
  committed transcripts (the lab's real `idevicebackup2` output), not from assumptions: the
  `receiving`/file-count/percent lines, `*** Waiting for passcode ***`, `Backup Successful`, and
  the connection-lost/`-4`/error signatures. A line it doesn't recognize is passed to the log
  verbatim and does not change state (robust to version drift).
- **Liveness = activity sampler, staged, pausable.** A ticker samples the work tree cheaply (total
  size, file count, newest mtime; `Manifest.db`+journal presence) and stages
  `active → silent_but_connected → suspected_stall`; **`waiting_for_passcode` pauses the clock**
  (the user may take minutes). A zero-activity **timeout** (a named `const`, design §4's 15 min,
  "tuned in qn.7") transitions the job to `failed`/`connection_lost` with an honest reason — never
  a silent kill. Liveness is a *display + timeout* signal; it never fabricates progress.
- **Free-space watch on the sampler (amendment 3 / the Operator's "A3").** The same tick that
  stats the work tree also `statfs`-samples the **target filesystem's free space** — nearly free,
  one syscall on the path `Seed` returned. Dropping below `diskLowFreeBytes` (a named `const`)
  raises a **`disk_low` warning**, surfaced as a structured `slog` warning **and** a `job.log`
  chunk (the frozen text stream the UI live-tail already renders) — **never a silent kill**: the
  backup runs on, and if the fs genuinely fills, `idevicebackup2` fails and the job terminates
  honestly (`failed`, ENOSPC-class error). **Preflight** carries the matching guard: free space
  already below the floor → the job fails **actionably** before any process is spawned. Rationale
  (the surviving grain of the external review, ruled now while the sampler is being designed rather
  than as a post-mortem after a multi-hour backup dies at 98% disk): a disk-full death with no
  prior warning is exactly the silent failure the "no silent caps/fallbacks" rule forbids.
  **Contract surface:** A3 rides `job.log` + `slog` only — it does **not** alter the frozen
  `Job`/`JobProgress` wire shape this rung; a first-class persistent `disk_low` badge field on
  `Job` is qn.4b's to add when it builds the job-history UI (where badges live).
- **The verify → commit handoff (the qn.5 seam).** On process exit the engine first applies the
  **process-level** checks that are the supervisor's to own (per the qn.5 spec: "the exit-code +
  `Backup Successful` output checks belong to qn.4's supervisor"): exit code 0 **AND**
  `Backup Successful` seen. Fail either → terminal (`connection_lost` on a drop signature, else
  `failed`) + `Manager.Discard`. Pass → state `verifying`: call **`storage.Verify(treePath)`**
  (the exported tree-inspection half) for the `VerifyResult` (kind/encrypted/logical-bytes + a
  fail reason); a `Verify` failure → `failed` ("structural verification failed: …") +
  `Manager.Discard`, **no version**. Pass → state `committing`: call
  **`Manager.CommitJob(udid, jobID)`** → the committed `wire.Version`; set `version_id`, state
  `succeeded`. (The `treePath` is the workdir `Manager.Seed` returned at preflight.)
  - **Rung-local note — the double `Verify`.** `Manager.CommitJob` re-runs `storage.Verify`
    internally (it is a safe standalone). The engine also calls `storage.Verify` for the
    `verifying` *state* so a torn tree fails **before** the `committing` transition (honest state
    boundary + a fail reason for the UI). The second verify is idempotent and cheap (plist parse +
    a stat/head-read; the tree is quiescent — the writer has exited), so the redundancy is
    accepted. If profiling ever shows it matters, a `Manager.CommitVerified(udid, jobID, vr)` that
    skips the re-verify is the minimal follow-up (a same-track `core/` addition, not a contract
    change) — noted, **not built** this rung.
- **Discard-or-commit, never mutate committed state (D3 invariant).** The engine writes **only**
  into the `Seed` workdir (`work/<job>` on namespace backends, `working/` on zfs). It never
  touches `latest/` or `versions/`; promotion is qn.5's journaled swap. On any
  failure/cancel/loss: kill the group → close files → `Manager.Discard(udid, jobID)` (namespace:
  `work/<job>` removed, committed state untouched; zfs: dirty `working/` left, "last good = <ts>"
  reported). A crash *during* `backing_up` leaves an orphaned `work/<job>` and **no** commit
  journal (the journal is written only inside `Commit`), so qn.5's storage reconciliation sweeps
  the work dir after completing any real commits; the **job row** left behind is the engine's own
  to reconcile (next bullet).
- **Startup job-row reconciliation + the two-reconciler order (amendment 1; design §2 canon).**
  Design §2's `job engine` row is explicit: every transition persists to SQLite *before* its
  event, and "on startup, orphaned `backing_up` jobs become `connection_lost` and their work dirs
  are discarded." So on boot the engine flips every **non-terminal** row (`queued`/
  `waiting_for_device`/`preflight`/`backing_up`/`verifying`/`committing`) to **`connection_lost`**
  with an honest error ("interrupted by a restart"), sets `finished_at`, and calls
  `Manager.Discard(udid, jobID)` for each (namespace: drop `work/<job>`; zfs: report dirty). **The
  order is explicit and load-bearing — the two reconcilers compose for the first time this rung:**
  (1) qn.5 **storage** reconciliation (roll-forward half-commits, adopt, mark-missing, sweep
  orphaned work) → (2) engine **job-row** reconciliation → (3) **serve**. Storage goes first
  because a job killed mid-`committing` may have a journal qn.5 **rolls forward** into a real
  version; the engine then finds that a version now carries the row's `job_id` and reconciles the
  row to **`succeeded`** (with `version_id`) rather than blindly failing it — every other
  non-terminal row becomes `connection_lost`. All of this completes **before** the HTTP server
  accepts connections (wired in `cmd/quince` right after the existing `storageMgr.Reconcile`).
- **The assisted model (D13) — no auto-retry.** A failed job terminates honestly; there is no
  timer, no backoff, no automatic re-spawn (a retry would hang at the on-device passcode prompt).
  Retry is a **user action**: `POST /api/jobs` may carry `retry_of` → the new job inherits
  `intent_id` from the chain root and sets `attempt = prior.attempt + 1`; a first job has
  `intent_id == id`, `attempt == 1`. qn.4a **persists** these; the "completed after 1 retry"
  *grouping UI* is qn.4b.
- **Preflight + the encryption policy (design §4/§3).** `require_encryption: true` +
  device `WillEncrypt=false` → the job fails **actionably** at preflight (error code links to the
  encryption-management flow, qn.3) and **no process is spawned**; policy relaxed → the job
  proceeds and its version is permanently `encrypted: false` (a surfaced badge, not a silent
  downgrade). Encryption state comes from qn.3's device enrichment; presence from qn.2's registry.
- **Secrets (hard rule) — the backup password never reaches `idevicebackup2 backup`.** Under the
  assisted model the device performs encryption with its own keybag (the password was set via
  qn.3's pty flow); the `backup` run itself takes **no** password over argv/env/stdin. qn.4a
  therefore passes **no secret anywhere** — to be re-confirmed live at build (interface fact 5).

**Rung-local decision — `transport: auto` is deferred to qn.4b (architect-ratified).** The
contract freezes `transport: "usb"|"wifi"|"auto"`, but (be) reserves *first-class Wi-Fi + `auto`*
for qn.4b. qn.4a implements `usb` and `wifi` explicitly. A `POST /api/jobs {transport:"auto"}` (or
`--transport auto`) returns **422** with a **user-facing message free of process/rung leakage** —
"automatic transport selection is not available yet — choose usb or wifi" — rather than
half-building qn.4b's plugged-detection policy behind a valid-looking value (state honesty: no
silent partial policy). The **log** line for the same event may cite the owning rung (qn.4b); the
API string may not. No landed UI or demo path sends `auto` yet (the "Back up now" wiring is qn.4b),
so nothing breaks. The architect ratified the deferral (resolving `auto` trivially now would route
real jobs onto a Wi-Fi path qn.4a proves only via fakes). The **interim 422 (auto) and 409
(running) codes are recorded in contracts §1** at rung close (the qn.3/qn.5 pattern).

**Named constants (rung-ruled — recorded per amendment 2's "same treatment as the liveness
timeout").** The engine's tunables are code `const`s, not v0.1 config keys (D12 — none is a
per-deployment setting yet); each is surfaced in logs, never silent:

- `livenessZeroActivityTimeout = 15m` — the staged-stall → timeout bound (design §4; "tuned in qn.7").
- `waitForDeviceTimeout = 60s` — the `waiting_for_device` bound (**amendment 2**): how long a
  freshly-`Start`ed job waits for its device to appear on the chosen transport before failing
  actionably. Sized for a muxd reattach blip (the assisted model has the user + phone present), not
  for someone walking off to find the phone; tunable if the lab shows longer USB attach latency.
- `diskLowFreeBytes = 2 GiB` — the **amendment 3** free-space floor: the sampler warns (`disk_low`)
  when the target fs drops below it and preflight refuses to start below it. Absolute (not a
  percentage) so it fires only when space is genuinely low, independent of filesystem size.
- `progressThrottle = 500ms` — the ≤ 2/s `job.updated` progress throttle (contract §3); **state**
  changes emit immediately, unthrottled.

Values may be refined during build (recorded here if so).

If a live check contradicts canon (e.g. `idevicebackup2 backup` needs a password over argv, or its
success string differs from the transcripts, or its target-dir layout differs from what `Seed`
returns), that is a **gap** — `PROPOSED (gap)` in the affected canon doc + an open question + stop,
never a silent workaround.

### Interface facts to verify live (evidence — looked up, never remembered; decisions (al))

Verified against the shipped tools in the built image (libimobiledevice **1.4.0**, present since
qn.3), recorded in Rung-ruled decisions once checked; code built to match. The committed
transcripts are the ground truth for **output** format.

1. **`idevicebackup2 backup` argv + target layout** — the exact subcommand/flags (`backup`, the
   `-u <udid>` selector, full-vs-incremental behavior, whether a `-i` interactive flag is involved
   for the *backup* path as it is for encryption), and **where it writes** relative to the target
   dir (it writes into `<target>/<udid>/…`) so `Seed`'s returned workdir is passed correctly and
   `storage.Verify` is pointed at the produced tree, not the parent.
2. **Success + terminal signatures** — the literal `Backup Successful` string and exit code 0 on
   success; the connection-lost / `-4` / "device disconnected" signatures on a torn session; the
   `*** Waiting for passcode ***` line (byte-exact) — all cross-checked against the transcripts.
3. **Progress output** — the shape of the file-count / percent / "Sending/Receiving files" lines
   the parser keys on (from the transcripts; the tool's progress reporting is erratic by design —
   D3 — so the parser must tolerate gaps and silence).
4. **`USBMUXD_SOCKET_ADDRESS`** — `UNIX:<path>` for usbmuxd, `host:port` for netmuxd (VERIFIED
   qn.3; re-confirmed here because the streaming supervisor inherits the same env helper).
5. **No-secret confirmation** — that `idevicebackup2 backup` on an encryption-enabled device needs
   **no** password over argv/env/stdin (the device prompts on-device; the keybag is the device's).
   If false → a **gap** (the secrets-discipline hard rule would force the pty channel, as qn.3
   does for encryption management).

## Stories

Each independently checkable. CI stories use the **fake `idevicebackup2`** (a `GO_WANT_HELPER_PROCESS`
binary replaying a committed transcript with its timing + writing the matching tree) + a **fake
muxd**/registry + qn.5 storage on a temp `/backups` — **no hardware in CI** (D9).

1. **Transcript fixtures.** The six named transcripts + `*.meta.json` companions exist under
   `core/internal/backup/testdata/transcripts/`, scrubbed to synthetic UDIDs/device-names/paths;
   `make privacy-check` is clean. Each meta records the expected terminal state, timing hints, and
   the scrub map (README format).
2. **Full USB success (happy path).** The fake replays `full-usb-success.txt` (exit 0 +
   `Backup Successful`, a valid **encrypted** tree written); the engine drives
   `queued → waiting_for_device → preflight → backing_up → verifying → committing → succeeded`,
   parses progress phases, and produces a committed **verified** version in qn.5 storage (listed,
   `structure_verified_at` set, `version_id` on the job). One test per backend where cheap (copy in
   CI; zfs via the fake-`zfs`).
3. **Waiting for passcode.** `waiting-for-passcode.txt` → the engine surfaces the
   `waiting_for_passcode` progress phase **and the liveness clock pauses** across the wait (no
   false `suspected_stall`/timeout while waiting), then continues to its terminal state.
4. **Wi-Fi torn session (headline "engine is Wi-Fi-shaped" gate).** `wifi-torn-session.txt`
   (connection lost / `-4`) → the engine ends in **`connection_lost`**, calls `Manager.Discard`
   (namespace: `work/<job>` gone; zfs: dirty `working/` reported), **`latest/` untouched**, **no
   committed version**, and the `Job.error` is honest. Proven by running the replay.
5. **Silent stall is not a failure.** `silent-stall.txt` (multi-minute silence, then success) →
   liveness stages to `silent_but_connected`/`suspected_stall` but the job is **not killed** (the
   sampler sees the silence is within tolerance / tree still consistent); the run reaches its
   transcript terminal (success). Proves the app-level clock can't undercut normal long silences.
6. **Structural-verify gate at the engine (state honesty).** A transcript that exits 0 +
   `Backup Successful` but whose written tree **fails `storage.Verify`** (e.g. torn `Status.plist`
   / missing `Manifest.db`) → the engine ends **`failed`** ("structural verification failed: …"),
   **no version committed**, `Discard` called. (Process-success ≠ backup-success; this also
   exercises the qn.5 `CommitJob` verify-fail seam from above — see the handoff-review finding.)
7. **Per-UDID single-flight (D3 invariant).** Two concurrent `POST /api/jobs` for one UDID → the
   second is rejected **409** ("a backup is already running for this device"); never two engine
   goroutines / two `Seed`s for one UDID. A different UDID runs concurrently.
8. **Cancel.** `POST /api/jobs/{id}/cancel` on a running job → the process **group** is SIGKILLed,
   the job ends **`cancelled`**, `Manager.Discard` called, no version, `finished_at` set.
9. **Preflight + encryption policy.** Device absent on the chosen transport → `waiting_for_device`
   → (bounded by `waitForDeviceTimeout`) honest fail, no process spawned. `require_encryption: true`
   + `WillEncrypt=false` → **actionable** preflight fail (error links the encryption flow), no
   process spawned; policy relaxed → the job proceeds and its version is badged `encrypted: false`.
   Free space already below `diskLowFreeBytes` → actionable preflight fail before any spawn (A3).
10. **Jobs REST + events + log.** `POST /api/jobs` → 202 `Job`; `GET /api/jobs?udid&cursor&limit`
    → a page + `next_cursor`; `GET /api/jobs/{id}` → `Job`; `GET /api/jobs/{id}/log` → `text/plain`
    (full-so-far); `job.updated` fires on every state/progress change (progress throttled ≤2/s);
    `job.log` streams chunks. Golden-tested against contracts §2 (`make gen-golden`); job list is
    a registry read (< 100 ms, no fs walk — perf budget).
11. **CLI harness.** `quince backup <udid> --transport usb` drives one job through the real engine
    (against the fake CLI in tests) streaming state/progress to stdout, exits **0** on `succeeded`
    and **nonzero** on a failed terminal; `--transport auto` → a clear 422-equivalent CLI error.
    This is the rung's own lab-driving command.
12. **Retry-chain fields (assisted model).** A first job → `intent_id == id`, `attempt == 1`,
    `retry_of == null`. A `POST /api/jobs {retry_of: <failed-id>}` → the new job **inherits
    `intent_id`** from the chain root and **`attempt` increments**. Persisted + served honestly;
    the grouping *UI* is qn.4b.
13. **Startup job reconciliation (amendment 1).** Boot the engine against a jobs registry holding
    stale non-terminal rows (`queued`/`backing_up`/`verifying`/`committing`) from a prior crash →
    each flips to **`connection_lost`** with an honest error + `finished_at`, `Manager.Discard`
    runs for each, **before the server serves**. The test pins the two-reconciler **order**: a row
    crashed mid-`committing` whose commit **rolled forward** in storage reconciliation ends
    **`succeeded`** (its `version_id` now exists), not failed — proving storage reconciliation ran
    first.
14. **Disk-low warning (amendment 3 / "A3").** With the target-fs free-space probe injected below
    `diskLowFreeBytes` **during** a backing-up job → a `disk_low` warning reaches `slog` + the
    `job.log` stream and the job is **not** killed (it runs to its transcript terminal). Injected
    below the floor **at preflight** → the job fails **actionably** before any process spawns. No
    silent kill on either path; the frozen `Job`/`JobProgress` wire shape is unchanged.

**Lab gate (manual, hardware — USB; the rung's headline proof + the re-homed gate-12 legs (bm)/(bn)).**

> **STATUS (2026-07-20, decisions (bs)):** the **engine legs PASSED on real hardware** (an iPad15,7,
> iOS 26.5 — not the iPhone below): CLI-USB backup, both **unencrypted and encrypted** variants
> (A1's encrypted `Verify` on a real encrypted `Manifest.db`), **version rotation**, **interface
> facts 1 & 5** confirmed, and the **kill-matrix `backing_up` leg** (crash → committed state intact,
> reconciliation sweeps work + `→ connection_lost`, no phantom). The UI-driven both-transports
> backup was re-homed to **qn.4b gate 11** (br). **ARCHITECT CLARIFICATION (bv):** the engine legs
> ran on the **`hardlink`** backend, so gate (a)'s "committed via qn.5's Commit **on the real zfs
> backend**" is NOT yet satisfied — engine→commit-on-zfs is part of the pending **zfs half**, which
> STAYS this rung's (Operator ruling; the session holds the topology details). The zfs half =
> **engine→commit on the real zfs-hook backend** + `mirror` verb / `bclonesaved` / iMazing / syncoid.
> The zfs legs are **DEFERRED** to a later session — they need the rpool
> hook-mode topology, disproportionate vs. the incremental value (core zfs facts already proven in
> gate-12 (bf)→(bk)); syncoid target prepped on the offsite PVE host (`local/environment.md`). Four
> lab findings filed. **The
> M3 engine half is hardware-proven.**

On the qn.3-kept **paired staging container** + the PVE host's real **rpool** (zfs hook mode) + a
real iPhone:

15. **Real encrypted USB backup, CLI-driven, end-to-end.**
    - **(a) The backup.** `quince backup <udid> --transport usb` drives a full **encrypted** backup
      to `succeeded` — the state machine runs `queued → … → succeeded`, producing a **committed,
      structurally-verified** version via qn.5's `Commit` on the **real zfs backend** (A1's
      encrypted `Verify` branch exercised on real data again, now through the engine). Record the
      state timeline + timings; assert `latest/` presents the verified tree and the version lists.
    - **(b) Secrets on hardware (story 6-class check, D13).** Capture the child's argv + env +
      logs during the backup: **no backup password in argv**, `BACKUP_PASSWORD` count **0**, clean
      logs — the on-device keybag does the encryption (interface fact 5 confirmed live).
    - **(c) Engine kill matrix on hardware.** Kill quince mid-backup at each phase
      (`backing_up`, `verifying`, `committing`); on restart, qn.5 reconciliation leaves a **defined**
      state (orphaned `work/`/dirty `working/` handled; a half-commit rolls forward; no phantom
      version). Confirms the engine + reconciliation compose on real traffic.
    - **(d) Re-homed gate-12 legs ((bn) — measurements taken during this same backup, zero extra
      sessions):** the host-side **`mirror` verb** runs on the real rpool and **`bclonesaved` is
      observed moving** (reflink sharing proven live, closing qn.5's pending hardware leg);
      **iMazing opens the committed version**; a **syncoid** pass **mid-write** replicates every
      committed version intact (dirty `working/` + all `@quince-*` restore points + a consistent
      `latest/`). *(The 12c destructive hardlink matrix is qn.4b's — (bn).)*
    - **Every bug found on the lab box becomes a scrubbed replay transcript (or fake-`zfs`
      transcript) fixture before it is fixed** (hard rule) — the mechanism this whole rung runs on.

## Gates

- `make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`.
- **Declare coverage** (program loop step 3): the `go test -cover` summary for `internal/backup` +
  the touched packages (`store`, `httpapi`, `cmd/quince`), plus a **known-untested list** (one line
  + reason each). `-cover` is already wired into `gates-go` (qn.5).
- Stories 1–14 as Go tests (fake `idevicebackup2` + fake muxd/registry + qn.5 storage on a temp
  `/backups`); **story 4 (wifi-torn → `connection_lost`, latest untouched, no version)**, **story 6
  (verify-gate → `failed`, no version)**, **story 7 (single-flight)**, and **story 13 (startup
  reconciliation → `connection_lost` / rolled-forward-`succeeded`, before serving)** are the
  headline CI gates.
- `gates-ui-e2e` stays green (qn.4a adds no UI; the demo `JobReader`/`JobControl` keeps stories
  1–3 alive). No new e2e story this rung (the job-history UI is qn.4b).
- **Lab gate 15** recorded in the rung report + progress decisions log — the "provable at rung
  close" rule: CI proves the engine against all transcripts (incl. Wi-Fi torn) + the verify/discard/
  single-flight/cancel/startup-reconciliation invariants; the lab gate proves the real encrypted USB
  backup end-to-end + the kill matrix + the re-homed gate-12 legs on real hardware.

## Fixtures

- **The six transcripts** (`core/internal/backup/testdata/transcripts/*.txt`) extracted from the
  lab log per the qn.0 README, each with a `*.meta.json` (expected terminal state, replay timing
  hints, scrub map): `full-usb-success`, `wifi-incremental-success`, `waiting-for-passcode`,
  `wifi-torn-session`, `silent-stall`, `encryption-changed`. **Scrubbed** — synthetic UDIDs
  (e.g. `UDID0`), synthetic device names (e.g. `test-iphone`), no Operator paths/hosts. The lab log
  (`local/chatgpt-original-idea-chat.md`) is the private source; these scrubbed extracts are the
  public durable form (D9 / decisions (aa)).
- **The fake `idevicebackup2` replayer** (`GO_WANT_HELPER_PROCESS`) — replays a transcript's
  stdout/stderr with the meta's timing (incl. injected silences for story 5), **writes the matching
  MobileBackup2 tree** into the target dir (reusing qn.5's synthetic-tree builder so
  `storage.Verify` runs on real structure — a valid encrypted tree for success transcripts, a
  deliberately-torn tree for story 6), and exits with the transcript's code. An injectable
  "hang" mode (never emit, never exit) exercises the liveness timeout + cancel (stories 5/8).
- **A fake muxd/registry + device-ops stub** supplying presence + encryption state for preflight
  (stories 2/9) without hardware.
- **An injectable free-space probe** (a `func(path string) (freeBytes uint64, err error)` seam over
  `statfs`) so story 14 (A3) can force the `diskLowFreeBytes` threshold in CI without a real full
  disk; production wires the real `statfs`.
- **Privacy (hard rule):** all fixtures synthetic; `make privacy-check` before every commit; every
  lab bug → a scrubbed replay fixture before the fix; commit messages describe *what*, never *where*.

## Rule check (mandatory — written before building; program spec shape + decisions log (as))

Every hard rule / canon boundary this rung touches *or comes near*, one compliance line each.

- **State honesty** (hard rule — this rung's central rule). A job is `succeeded` **only** after
  process-success (exit 0 + `Backup Successful`) **and** `storage.Verify` **and** `CommitJob`; a
  torn/verify-failed/commit-failed run is `connection_lost`/`failed` with an honest `error` and
  **no version**; `cancelled` is real (group killed); liveness `active/silent_but_connected/
  suspected_stall` reflect the sampler, never fabricated progress; `content_verified_at` stays
  null (two levels, honest); and orphaned rows from a crash reconcile to `connection_lost` — or
  roll-forward `succeeded` — **before** serving, never left mid-flight (amendment 1, design §2).
  **Stories 2/4/5/6/8/13 exercise it directly. Complies.**
- **Never mutate a committed version / storage invariants** (hard rule). The engine writes **only**
  into the qn.5 `Seed` workdir; it never opens `latest/`/`versions/` for write; promotion +
  atomic swap are qn.5's; `Discard` drops only the work area; a `backing_up` crash leaves an
  orphaned `work/` that qn.5 reconciliation sweeps (no new reconciliation surface here).
  **Re-proven per backend in stories 2/4; story 4 asserts `latest/` untouched. Complies.**
- **No silent caps or fallbacks** (hard rule). `suspected_stall`, the liveness timeout,
  `connection_lost`, a relaxed-encryption `encrypted:false` badge, the deferred `auto` transport
  (422, not a silent partial policy), and the **`disk_low` free-space warning** (amendment 3 —
  `slog` + `job.log`, and a preflight refusal, **never** a silent kill; the fs filling still
  terminates the job honestly as `failed`) are each **surfaced** (state/error/log/badge).
  **Complies.**
- **Subprocesses: argv arrays, own process group, supervised, killed on end** (hard rule; design
  §1/§6). The `idevicebackup2` supervisor is argv (never a shell string), `setpgid`, group-SIGKILL
  on cancel/ctx/timeout/job-end — the qn.3 `deviceops`/qn.2b `muxsup` discipline, extended to
  line-streamed I/O. UDIDs are pattern-validated (the qn.3 allowlist) before argv. **Complies.**
- **Secrets discipline** (hard rule). The `backup` run takes **no** password (argv/env/stdin) — the
  device's on-device keybag encrypts under the assisted model; qn.4a passes no secret anywhere.
  **Near-miss flagged:** interface fact 5 **must** confirm this live at build; if `idevicebackup2
  backup` unexpectedly wants a password, that is a **gap** forcing the pty channel (qn.3's
  pattern), never argv/env. Test fixtures use the password `test` where a password appears at all
  (encryption fixtures inherited from qn.3). **Complies (pending the live confirm).**
- **Config tidiness — D12** (hard rule). qn.4a adds **no** config key — `backup.transport` +
  `backup.require_encryption` already exist in schema v0 (contracts §6). **Near-miss flagged:**
  nothing to add → trivially compliant; no UI-only state, no new env var, no secret in the file;
  the liveness timeout, `waitForDeviceTimeout`, `diskLowFreeBytes`, and `progressThrottle` are code
  `const`s (see Named constants), deliberately **not** v0.1 config keys (design §4 — "tuned in
  qn.7"; a per-deployment disk-low threshold, if ever wanted, is a later config addition).
  **Complies.**
- **A rung's goal is provable at rung close** (hard rule). Every dependency is a **landed** rung:
  qn.5 storage (the `Seed`/`Verify`/`Commit`/`Discard` seam), qn.3 pairing+encryption (the paired
  staging container is kept standing), qn.2b managed usbmuxd + live `/dev/bus/usb`. CI proves the
  engine against all transcripts incl. Wi-Fi torn; the lab gate proves the real encrypted USB
  backup end-to-end on that landed infrastructure — **no future-rung dependency**. `transport:auto`
  (qn.4b) is deferred as a *policy*, not a gate dependency (the gate uses explicit `usb`).
  **Complies.**
- **Every bug found on the lab box becomes a replay fixture before it's fixed** (hard rule). This
  rung's entire method is the transcript corpus; each lab-found bug → a scrubbed transcript/
  fake-`zfs` fixture before the fix (lab gate 15). **Complies.**
- **Perf budgets** (hard rule). `GET /api/jobs` reads the indexed registry (no fs walk) < 100 ms;
  progress events throttled ≤ 2/s (contract §3); liveness sampling is a cheap periodic stat, not a
  per-event fs walk; version list unchanged (qn.5). **Complies.**
- **Privacy is a commit-time gate** (hard rule). Transcripts + meta scrubbed (synthetic UDIDs/
  names, no Operator paths/hosts); the lab log stays gitignored; `make privacy-check` before every
  commit; a lab dump is rewritten or stays Operator-local; commit messages describe *what*, not
  *where*. **Complies.**
- **Version pins / interface facts are looked up, never remembered** (hard rule). The exact
  `idevicebackup2 backup` argv/flags/exit-codes/output-strings/target-layout and the no-secret fact
  are verified **live** in the built image (libimobiledevice 1.4.0) at build and recorded as
  evidence; the transcripts are the output ground truth; any contradiction with canon is a **gap**.
  **Complies.**
- **Docs are part of the diff** (hard rule). design §4 + stack D3/D13 + contracts §1/§2/§3/§6
  already describe the engine/`Job`/job-events/config — qn.4a **implements to them and verifies the
  match** (mismatch = a gap). Implemented `POST /api/jobs`/`cancel` error codes (409 running, 422
  auto) are recorded in contracts §1 (the qn.3/qn.5 pattern); the dashboard + decisions log are
  updated at rung end. **Complies.**
- **Contract / boundary discipline** (program loop step 2). qn.4a owns `core/internal/backup` +
  the `jobs` slice of `store` + the jobs slice of `httpapi` + the `backup` CLI in `cmd/quince`; it
  **routes already-frozen** surfaces (`Job`, `job.*`, jobs endpoints) — implementation, not a
  contract change. **Near-misses flagged:** (1) `transport: auto` resolution is qn.4b — qn.4a
  returns 422 (see the rung-local decision; architect to ratify); (2) the intent-grouping UI +
  `versions verify`/`repair-working-copy` CLI are qn.4b; (3) a thin `Manager.CommitVerified` is
  noted-not-built (same-track, would avoid the double verify). Building qn.4b's Wi-Fi/transport
  policy, or reaching into qn.8's vault, would be a **STOP**. **Complies.**

## Rung-ruled decisions (settled during the build; *rung-ruled* canon)

- **Interface fact 1 — the `idevicebackup2` target-layout adapter (RESOLVED for CI; verify live on
  hardware).** `idevicebackup2 backup <target>` writes the MobileBackup2 tree into
  **`<target>/<UDID>/`** (lab-confirmed: the log's `mv "/backup/$UDID"`), but qn.5's
  `Seed`/`TreePath`/`Verify` expect the tree **directly** at the work dir. Rather than a
  STOP-the-rung gap or mutating qn.5's landed layout, the engine builds a per-job scratch target
  dir whose `<UDID>` entry is a **symlink to the qn.5 work dir**, so `idevicebackup2` writes
  straight into the work dir — no tree copy, no committed-state mutation, no qn.5 change. Rung-local
  (engine-side, no contract/layout change). The symlink-follow behavior is exercised end-to-end by
  the fake in CI; the real `idevicebackup2`'s acceptance of the symlinked target is a **lab-gate-15
  verify-live** item — **CONFIRMED on hardware** (2.8 GB landed through it, (bs)).
  **AMENDED by qn.4c's gate-11 lab finding (2026-07-21):** the scratch dir must live on the **same
  filesystem as the work dir**, not under `$QUINCE_CACHE`. mobilebackup2 asks the host for its free
  space and `idevicebackup2` answers with a `statfs` of **the target directory it was handed** — it
  does NOT follow the `<UDID>` symlink. With the stub on a small cache filesystem, the DEVICE
  refuses the backup (`ErrorCode 105: Insufficient free disk space`, exit 151, zero bytes), so no
  device whose backup exceeds the cache filesystem could ever be backed up. The stub is now derived
  as `<dir of workDir>/.quince-targets/<jobID>` — quince-writable on every backend and always on the
  storage filesystem — and `ToolConfig.TargetRoot` is gone. Proven both ways on real hardware:
  refused at 26 GB, transferring at 546 GB.
- **Interface fact 2 — a Wi-Fi torn session is a STALL, not an error line (RESOLVED from the lab
  transcripts).** The lab's Wi-Fi drop (`Heartbeat(SleepyTime)`) **freezes** `idevicebackup2`'s
  output with no error — the process hangs on the dead transport. So `connection_lost` is produced
  by the engine's **liveness timeout**, not by parsing an error signature. The discriminator
  against a *survivable* silence is **tree activity** (the lab saw `du` still churning during a
  survivable pause), so the sampler judges liveness by a cheap tree fingerprint, not output
  silence. `full-usb-success`/`silent-stall`/`wifi-torn-session` encode this: the last two share
  the frozen-output pattern and differ only in whether the tree keeps churning.
- **Interface facts 3–5** — the success string (`Backup Successful.`), the `Full/Incremental
  backup mode` + `*** Waiting for passcode … ***` lines, and the `-u <udid>` / `-n` argv are
  transcript-grounded; the **no-secret-for-backup** posture (interface fact 5) is asserted and
  remains a **lab-gate-15 verify-live** (the device's own keybag encrypts; no password crosses
  argv/env/stdin).
- **`storage.Manager` gains two thin methods** (same-track, no contract change): `VerifyTree`
  (returns `storage.Verify` as primitives) and `VersionForJob` (registry lookup by `job_id`, for
  the roll-forward-aware job reconciliation) — so `internal/backup` satisfies its `Storage` /
  `StorageForJob` seams with **no import edge into `storage`**.
- **Demo `JobControl` = `Unavailable` (503)** — a rung-local downgrade from the draft's "demo POST
  triggers a scripted job". `--demo` already loops a rich scripted backup for the **read** surface
  (list/log), and its single fixed-`jobID` loop doesn't cleanly host distinct on-demand jobs; the
  **command** surface refuses honestly (503) rather than half-simulating. No e2e story posts jobs,
  so nothing regresses.
- **The fake `idevicebackup2` must exit via `syscall.Exit`, not `os.Exit`** (test-harness fact
  worth recording). The fake is a `-race`-instrumented test-binary re-exec; `os.Exit` runs the Go
  runtime exit hooks (incl. the race finalizer), which **deadlocks** in a helper process that has
  done file I/O — the process stays alive until the engine's timeout SIGKILLs it, masquerading as a
  liveness failure. `syscall.Exit` calls `exit_group` directly; stdout is an unbuffered pipe, so no
  replayed output is lost. (Cost the session several debug rounds — noted so the next fake-CLI
  author doesn't rediscover it.)
- **Sampler startup grace** — the sampler accrues **no** idle time before the first sign of life
  (output or tree change): a process/re-exec startup can outlast a short timeout, and killing a
  just-spawned job for "no activity" would be wrong. Production is unaffected (15-min timeout).
- **Package shape.** `internal/backup` = engine (state machine + single-flight + reconcile) +
  supervisor (argv + the symlink adapter + group-kill) + parser (transcript-grounded) + sampler
  (activity liveness + A3) + `joblog` (in-memory ring) + `cli` (`DriveToCompletion`, the CLI body);
  a `jobs` table + registry in `internal/store`; the live subsystem set built once by
  `cmd/quince`'s **`buildLiveStack`** (shared by `serve` and the `backup` CLI), which runs the
  two-reconciler order (storage → job rows) before returning. Named constants + the double-`Verify`
  are as ruled above.

## Rung report (build outcome)

**Handoff review of qn.5:** clean (no blocker/major); one minor coverage note — `CommitJob`'s
verify-fail branch was undeclared-untested at the `CommitJob` level, now covered from above by
qn.4a **story 6** (a torn tree → `failed`, no version).

**Built (CI-proven):** the whole `internal/backup` engine + the `jobs` store + the httpapi job
command surface + the `quince backup` CLI, driving qn.5 storage on real traffic-shaped fakes.
`make gates` (go + vault + ui) + `make image` + `make gates-ui-e2e` green in `quince-dev`. **CI
stories 1–14** pass, incl. the headline **story 4** (Wi-Fi torn → `connection_lost`, `latest/`
untouched, no version), **story 6** (verify-gate → `failed`, no version), **story 7**
(single-flight → 409), and **story 13** (startup reconciliation → `connection_lost` /
rolled-forward-`succeeded`, before serving). All six transcripts extracted + scrubbed
(`privacy-check` clean).

**Coverage (declared, per program loop step 3):** `internal/backup` **83.1%**, `internal/store`
**80.8%**, `internal/httpapi` **72.2%**; other touched packages unchanged. **Known-untested
(accepted debt, all low-risk or hardware/deploy-gated):** the supervisor's real-`idevicebackup2`
argv path + the symlink adapter's live-follow (proven by the fake in CI, real on lab gate 15); the
`statfsFree` production leaf (CI injects the free-space probe); `cmd/quince`'s `buildLiveStack` +
`backupCmd` wiring (11.0% — exercised by `serve`/the hardware gate, not CI); the shutdown-ctx
`outcomeShutdown` branch and a few store scan-error paths.

**NOT proven on hardware — lab gate 15 (owned by this rung; the qn.4a hardware session):** the real
encrypted USB backup end-to-end on the rpool + the engine kill-matrix + the re-homed gate-12 legs
(host `mirror` verb `bclonesaved` moving, iMazing-opens, syncoid mid-write). Interface facts 1 + 5
(the symlinked target + no-secret-for-backup) verify-live there.
