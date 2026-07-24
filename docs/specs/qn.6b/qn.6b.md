# qn.6b — transport patience

> Status: **BUILT (CI-proven) ((df)).** Spec approved-with-amendments ((dg)); the item-3/item-4
> capture edits re-confirmed ((dh)). Amendments folded in: **A** (bound non-backup tool ops Go-side —
> the shared-timeout leak), **B** (Info.plist written *before* the Backup request), **C** (gate
> crash-safety). `make gates` + `make image` + `make gates-ui-e2e` green in `quince-dev`; lab legs
> declared owed (stories 9–11). Spike evidence:
> [`spike-libimobiledevice.md`](spike-libimobiledevice.md) (all C-source facts verified against
> upstream tag `1.4.0`).

## Goal

A Wi-Fi flap or a long legitimate iOS pause no longer kills or fakes a backup — the tool waits, the
UI narrates honestly, a genuinely dead link is classified eventually — and "Back up now" reaches the
on-device passcode prompt in ~1–2 s regardless of device size.

## Boundary

**In scope (the four ruled items, (de)):**

- `deploy/Dockerfile`, `versions.env`, new `deploy/patches/libimobiledevice/*.patch` — switch
  `idevicebackup2` from the Alpine package to a patched **from-source build** at a pinned tag.
- `core/internal/backup/` — the engine change for the gate patch (candidate C) *or* candidate B
  fallback, and the liveness-threshold retune (`backup.go` `Config`, `sampler.go`,
  `engine.go`/`supervisor.go`, `fake_idevicebackup2_test.go` + tests).
- `docs/` canon touched by behavior changes (stack D2 timeout note, design §4 liveness, deploy docs).

**Out of scope (stays qn.7 — (de) is explicit):** chaos suite, netmuxd-USB audition, restart-policy
tuning, the #2 409-race, the full #8 classification taxonomy (incl. reclassifying the tool's `-4`
exit `failed`→`connection_lost`), #9b, #10-percent, **UX copy polish** for the slow/silent/passcode
phases, and **fine** liveness-stage-threshold tuning against the lab box. This rung does the *coarse*
retune to the 15-min reality; qn.7 does the chaos-driven fine tuning.

**Expected contract changes: NONE.** The `seeding` state, `missing`, the job state machine, and the
`JobProgress.liveness`/`phase` enum values are all landed and untouched (values reused, never added).
If review finds a contract touch is needed, STOP and propose it before building.

## Design

Links canon rather than repeating it: stack D2 (Apple userland), design §4 (engine + staged
liveness), (ct) (Wi-Fi root-cause + the legitimate-`app_limited`-pause finding), (cs) (seed is
O(files), ~23 s / 34 GB), (cx)/(cz)/(de) (the seed-latency decision record; candidate C settled, B
the fallback). All idevicebackup2/libimobiledevice interface facts below are from the spike, verified
against upstream `1.4.0`; **re-verified mechanically at build time** — the patches fail loudly if a
line does not match (their own proof).

### Item 1 — patched libimobiledevice, built from source in the image

**Interface fact (looked up):** today the runtime does `apk add libimobiledevice libimobiledevice-progs
libusbmuxd` (Alpine 3.24 community — the spike corrects the brief's stale `1.1.1_git20250201`: Alpine
3.24 actually ships **`1.4.0-r0`**). A patch to `idevicebackup2`/libimobiledevice therefore requires
building **libimobiledevice from source** — the same shape `netmuxd` already uses.

- **Pin tag `1.4.0`** (commit `149f7623`, 2025-10-10) — newest release; master is only cleanup ahead
  and byte-identical in the three files we touch. New pin `LIBIMOBILEDEVICE_REF=1.4.0` in
  `versions.env` (looked-up-live rule satisfied: the spike queried the live releases API + Alpine
  APKINDEX at pin time).
- **Build ONE repo.** Alpine 3.24 ships every `-dev` dependency above the `configure.ac` minimums:
  `libplist-dev`, `libimobiledevice-glue-dev`, `libusbmuxd-dev`, and — **the undocumented 4th dep the
  brief omitted** — `libtatsu-dev` (`configure.ac` needs `libtatsu-1.0 ≥ 1.0.3`; Alpine has `1.0.5`).
  So `apk add` those `-dev` packages + autotools (`autoconf automake libtool pkgconf`) +
  `openssl-dev` (OpenSSL is the default backend) + `build-base git`; build only libimobiledevice.
- **In-tree patch files, no hosted fork** (ruled shape): `deploy/patches/libimobiledevice/`:
  - `0001-receive-timeout-15min.patch` — `src/property_list_service.c:272` and `src/service.c:179`,
    each `30000` → `900000` (= 15 min). These are the two hardcoded call-site timeouts the #1413
    thread converged on. They are the *default* receive timeout for **every** libimobiledevice-backed
    binary (narrowing would still touch a shared file — spike A3), so the patch **leaks 15-min
    patience into non-backup ops too** — see the amendment-A audit below.
  - `0002-idevicebackup2-gate-flag.patch` — the `--gate <path>` flag (item 2).
- **Build stage** `libimobiledevice-build` (mirrors `netmuxd-build`): `git clone --branch 1.4.0
  --depth 1` → `git apply` (or `patch -p1`) both patches → `./autogen.sh` (a tag checkout has no
  `configure`) → `./configure` → `make && make install` into a prefix `COPY`d into `runtime`. Drop
  `libimobiledevice-progs` from the runtime `apk add`; keep `usbmuxd`, `libusbmuxd`, and the runtime
  `-dev`→runtime libs the built binaries link (`libplist`, `libimobiledevice-glue`, `libusbmuxd`,
  `libtatsu`, `openssl` libs). Watch `configure.ac:88`'s unconditional `-Werror` — pass
  `CFLAGS=-Wno-error` if Alpine's GCC trips it.
- **Fresh-clone property (qn.0 gate) survives:** patches are committed in-tree; no network fork, no
  second repo, pins in `versions.env`. A fresh `git clone` + `make image` reproduces the patched
  binary.
- **Cost, surfaced (no silent caps rule):** image build now compiles libimobiledevice (autogen +
  cc), cached as a buildkit layer like netmuxd. Noted in the build report.

#### Amendment A — bound non-backup tool ops Go-side (a UX regression hiding in a reliability rung)

Patch `0001` raises the default receive timeout for the whole libimobiledevice family, and item 1
swaps the `apk` tools for that patched build. So **pairing, `ideviceinfo` enrichment reads, and the
(i)-B live encryption re-read all inherit 15-min patience** — a wedged lockdown read during preflight
would now sit **15 minutes** where it used to fail in 30 s. That is a UX regression, not a
reliability win, and it is invisible unless bounded on quince's side.

- **Audit** every non-backup libimobiledevice invocation (chiefly `core/internal/deviceops/`:
  pair/validate/`ideviceinfo`/the encryption pty ops; and any `idevice*` call in `core/internal/device/`
  enrichment): each must run under a **Go-side `context` deadline ≲ 60 s**. Any unbounded call found
  is bounded **in this rung** (a named `deviceOpTimeout` constant). The long 15-min patience is
  intentional **only** for the backup receive path (idevicebackup2 in the engine), where item 3's
  sampler is the backstop; every other op stays fast-failing.
- **Boundary note (near-miss, listed per the rule):** this reaches into `core/internal/deviceops/`
  (and possibly `core/internal/device/`), outside the backup tree — authorized by amendment A as
  in-scope for *this* rung precisely because item 1 is what introduces the regression there. No
  contract or behavior change beyond adding/​tightening a timeout; surfaced here so it is not a silent
  cross-tree edit.
- CI-provable (a test asserting the bounded ops carry a deadline; a fake slow tool returns control
  within the bound, not after 15 min).

### Item 2 — the gate patch (candidate C) + engine overlap; candidate B is the in-rung fallback

**The C patch (`0002`).** Spike-confirmed single clean insertion in `tools/idevicebackup2.c` between
line 2279 and 2280 — end of the `if (err == MOBILEBACKUP2_E_SUCCESS)` branch of `case CMD_BACKUP`,
**after** the `"Backup"` request (2261) and the passcode-observe window (2270–2279), **before** the
`do{…}while(1)` receive loop (2504, first receive 2507). Plus a `--gate <path>` long-opt (getopt
table ~1762) and a `static const char *gate_path` global. Behavior: if `--gate` is set, poll
`access(gate_path, F_OK)` every 100 ms until the file appears (or `quit_flag`), then proceed. No
protocol impact — no receive has happened; the device's first `DLMessage*` waits in the TCP/SSL
buffer until the gate releases. **Crash safety (amendment C):** the poll checks `!quit_flag`, so a
supervised cancel (SIGTERM → the tool's handler sets `quit_flag`) breaks the wait; and a quince or
container crash cannot orphan a forever-polling tool — idevicebackup2 is a child in the same container
and dies with it (process-group kill / container teardown).

**Why this gives the UX win.** The passcode UI is presented by the *device* in response to the
`Backup` request (2261), which fires within ~1–2 s of launch — **before** the gate. So quince launches
idevicebackup2 immediately; the phone prompts for the passcode in ~1–2 s; the ~23 s seed runs during
the gate wait; the dead air the Operator complained of is gone.

**Engine sequencing (candidate C).** Today: preflight → `seeding` (blocking `storage.Seed`) →
`backing_up` (`supervise` starts the tool). New, only on the **seed-from-latest** path (a resume
already has a populated `working/` — no seed, no gate needed):

1. Enter `seeding` (narration unchanged: "Preparing — cloning from your last backup…").
2. **Provision + create an empty `working/<udid>`** so the tool can write into `<target>/<UDID>/`.
3. **Start the tool with `--gate <gatefile>`** against the target. It handshakes, then — in this
   source order (spike C12) — writes a fresh `Info.plist` (2242 remove + 2243 write, ~1 s after
   launch), sends the `Backup` request (2261 → passcode fires on the phone), observes the passcode
   window (2270–2279), and blocks at the gate. **Info.plist lands BEFORE the request and the
   passcode**, so quince's seed-wait safe-point (step 4) is reached within ~1 s of launch — *not*
   gated on the user entering their passcode (amendment B).
4. **Capture-then-seed-then-restore Info.plist** (the (cz) "seed must preserve/rewrite that one
   file"): wait for `working/<udid>/Info.plist` to appear (the tool passed 2243; the source shows no
   further local-target writes before the gate, so this is a safe point) → copy it aside → run
   `storage.Seed` (its hook `rm -rf`s `working/<udid>` and reflink-clones `latest/`, bringing
   `latest`'s stale `Info.plist`) → write the captured fresh `Info.plist` back. This keeps the
   committed artifact's `Info.plist` current AND correct for the incremental handshake (the device
   requests `Info.plist`/`Status.plist`/`Manifest.db` from the computer via `DownloadFiles` in the
   loop). Safe because the tool holds **no long-lived fd** into the target dir (spike C13).
5. **Touch `<gatefile>`** → the tool proceeds into the loop against the seeded tree. Transition to
   `backing_up`; the existing `supervise` sampler/scan loop attaches to the already-running process.

The refactor splits "start the process + gate-release" from "run the sampler/scan loop" inside
`supervise`; the sampler, scanners, process-group kill, and outcome mapping are unchanged. If the tool
exits before `Info.plist` appears (handshake failure), the seed-wait aborts and the job terminates on
the existing failure path — no deadlock.

**Residual risk = LAB-OWED, not CI:** device tolerance of ~20 s host silence between the `Backup`
request and the first receive (the gate hold). (ct)'s multi-minute device-side pauses strongly
suggest the device tolerates it, and the spike confirms the device's first message simply buffers —
but this is device-firmware behavior, verifiable only on hardware. **Declared as a lab leg.** The
C-source spike itself **passed** (favorable facts), so per (de) candidate C is the build target.

**Candidate B — the in-rung fallback (built only if the lab device-tolerance leg fails, (cz)/(de)).**
Pre-seed `working/<udid>` right after each successful commit so the next "Back up now" finds it ready
(prompt ~1–2 s), **zero concurrency, zero external code**. Staleness objection is empty (`latest/`
never changes between commits). Costs: re-breaks "between backups the dataset holds only `latest/`"
(the snapshot then carries a reflink-shared `working/`, ~zero space; `rclone` already excludes
`work/**`), and a copy-fallback seed would pay real disk — so **config-gated and only where the seed
reports `SHARED`** (no silent cap). No C patch needed for B; the `0001` timeout patch and item 3 ship
regardless.

### Item 3 — liveness thresholds retuned to the 15-minute reality (inseparable from item 1)

**The mechanism, source-grounded (spike C10, `idevicebackup2.c:2504–2510`):** on a clean idle a
receive returns `-5 RECEIVE_TIMEOUT` and the loop **retries indefinitely — the tool never exits on
pure silence.** Only a `-4 SSL_ERROR` (peer reset / SSL fault) is fatal. Consequences:

- **quince's sampler is the sole authority for a cleanly-idle dead link** — the tool will loop `-5`
  forever, silently (the retry line is `PRINT_VERBOSE(2)`, suppressed). If quince doesn't classify it,
  nothing will. **Corollary ((dg)):** this is true *patched or unpatched* — the unpatched tool also
  loops `-5` forever, just at 30 s intervals; raising the timeout changes only the `-4`-SSL-mid-record
  path, never the clean-idle loop. So the two Wi-Fi hang shapes are distinguished exactly by **whether
  the sampler fired**: a `-4` SSL/reset → tool exits `failed` fast (the captured item-4 run); a
  `-5`-forever clean idle → only the sampler ends it, `connection_lost` (the likely shape of a
  *silent* soak hang). The captured run was the former; a future silent hang would be the latter.
- **The item-1 coupling made precise:** the patched receive timeout (15 min) means a receive blocked
  across a Wi-Fi flap writes nothing to the tree and emits no output for **up to 15 min** while the
  tool is legitimately waiting to recover. A sampler kill shorter than that would SIGKILL a backup the
  tool was about to complete — **undoing the patch.** So `LivenessTimeout` **must exceed** the tool's
  receive timeout.

**The retune (deliberately coarse — fine stage-tuning is qn.7):**

- Introduce a documented constant `toolReceiveTimeout = 15 * time.Minute` (a Go-side mirror of the
  `0001` patch's `900000` ms — one source of truth, referenced by comment + the guard test).
- Raise `LivenessTimeout` from `15m` to **`18m`** (= `toolReceiveTimeout` + a 3-min margin): it clears
  a full 15-min tool receive so the sampler never cuts a flap the tool is riding out, and classifies a
  genuinely dead link at 18 min — honest and eventual, only 3 min past the tool's own patience. The
  exact margin is a rung-local tuning call; qn.7 refines it against the chaos suite.
- **Guard test** asserting `LivenessTimeout > toolReceiveTimeout` — fails if anyone lowers the sampler
  below the tool's patience (mechanical coupling, not just a comment).
- Keep the staged narration (`silent_but_connected` at `/6` ≈ 3 min, `suspected_stall` at `/2` ≈ 9
  min, kill at `LivenessTimeout`). Values `active`/`silent_but_connected`/`suspected_stall` are
  contract §2 and **unchanged** — only *when* they fire moves. Both (ct) sides held: a legitimate
  multi-minute pause (well under 15 min) never trips a kill; a dead link is narrated
  (`silent_but_connected` → `suspected_stall`) then classified `connection_lost` at 18 min.
- **Not touched (stays qn.7 #8):** the `-4`-exit `failed`-vs-`connection_lost` classification. A dead
  *idle* link already lands `connection_lost` via the sampler (`outcomeTimeout`); only the `-4` *reset*
  path is `failed`, and reclassifying it is the qn.7 taxonomy.

### Item 4 — the Wi-Fi hang as the acceptance case (incident captured; verdict = tool-patience, not a quince bug)

**Incident data received (the (ct) Wi-Fi session; two runs, one root cause).** Job
`01KY95VPJ8WW9ESN3EFMRGMRFZ`, iPhone (34 GB) over Wi-Fi, resumed a dirty `working/` (no re-seed),
transferred at ~1500 pkts/s, then **died at ~44 s** with
`backup_failed | backup failed: Could not receive from mobilebackup2 (-4)`. The pcap root-caused it
(architect, ratified (ct)): **intermittent Wi-Fi link drops** — a ~4 s dead patch recovered, a ~10 s
one did not; the container retransmitted the same segment with 2 s→4 s exponential backoff into
silence, then idevicebackup2 gave up. 39 phone-side retransmits, **0 non-RST zero-window** (netmuxd
exonerated), variable failure offset (not a message-size bug). The earlier run died via netmuxd
`Heartbeat(Timeout)` — a *different* watchdog on the *same* event (whichever trips first wins).

**The verdict for this rung — decisive, verified in code:** the error message prefix
`backup failed: …` is `engine.go:367`'s `outcomeProcErr` path — **the tool's own `-4` exit**, NOT
`outcomeTimeout` (which would read `no activity for … — connection lost`, state `connection_lost`).
So **the quince liveness sampler never fired** (the tool self-terminated at ~44 s, nowhere near the
15-min sampler), and quince behaved correctly (kept the dirty `working/`, clean discard-for-retry,
resume-without-re-seed on the next attempt). This is therefore **neither a quince tuning miss nor a
quince liveness bug — it is idevicebackup2 giving up too fast on a ~10 s recoverable drop.** The fix
is **item 1** (make the tool patient); **item 3 is not the hero here** (see its section).

**The honest residual (from the spike, load-bearing for story 9):** the `-4` fired after only ~10 s of
silence — *faster* than even the current 30 s receive timeout would expire cleanly — because `-4` is
an **SSL-layer error** (short read on a mid-record stall), not the clean `-5` socket timeout. The
community + (ct) assert that raising the two `30000`→`900000` constants fixes it (the receive gets far
more time for the stalled record to complete once the link returns), and multiple users confirm it
empirically — but it is **empirically-backed, not mechanically-proven** for `-4`, and some `-4` cases
are device-initiated resets a timeout cannot cure. So **story 9 (lab: does the patched build ride out
a real ~10 s drop instead of `-4`ing?) is genuinely decisive, not a formality.** If it does NOT fix
the `-4` (a device reset), that is a **qn.7 finding** (retry-on-`-4` / `-4`→`connection_lost`
reclassification, the #8 taxonomy) — not a qn.6b blocker; qn.6b still delivers the patched build +
retune + the honest measurement.

**Acceptance gate = lab-owed** (story 9: re-run this drop on the patched build; ride it out to
`succeeded`, or land `connection_lost`/`failed` honestly only after real patience), sequenced with
the Operator; CI proves the threshold logic and the tool-patience wiring underneath it. The captured
idevicebackup2 `-4` output becomes a scrubbed replay fixture (hard rule) — the **pcap stays
local-only** (LAN IPs, never git).

## Stories (each independently checkable)

1. **Patched build.** `make image` builds libimobiledevice `1.4.0` from source with both patches
   applied in the pinned toolchain; the runtime `idevicebackup2 --help` lists `--gate`; a fresh clone
   reproduces it. *(CI-provable.)*
2. **Timeout patch present.** The built `libproperty`/service receive timeout is 900000 (proven by
   the patch applying against the pinned source — context match — and the image building green).
   *(CI-provable.)*
3. **`--gate` pauses the tool.** With the fake-CLI harness honoring `--gate`, the tool writes its
   `Info.plist` and fires the passcode line, then blocks until quince touches the gate file; the
   passcode-observed narration is visible before the seed completes. *(CI-provable, engine test.)*
4. **Overlap preserves Info.plist.** After a candidate-C run over the fake tool, `working/<udid>`
   holds the *fresh* `Info.plist` (not `latest`'s clone) and `latest`'s shards; the seed ran during
   the gate hold. *(CI-provable.)*
5. **No panic on a legitimate pause.** A 14-min tree-and-output-silent window during a running backup
   does NOT kill; liveness stages to `suspected_stall` and recovers to `active` on the next write.
   *(CI-provable, sampler test.)*
6. **Honest eventual dead-link.** A permanently idle link kills at `LivenessTimeout` → `connection_lost`
   with the dirty `working/` kept for resume. *(CI-provable.)*
7. **Coupling guard.** A test fails if `LivenessTimeout ≤ toolReceiveTimeout`. *(CI-provable.)*
8. **Non-backup ops stay fast (amendment A).** A slow/wedged fake lockdown tool returns control to a
   bounded device op within its `deviceOpTimeout` (≲ 60 s), NOT after 15 min; a test asserts each
   audited op carries a context deadline. *(CI-provable.)*
9. **Acceptance (item 4).** The captured Wi-Fi `-4` hang, re-run on the patched build, ends in an
   honest terminal state and a retry resumes+verifies. *(LAB-owed — declared.)*
10. **15-min patience.** A real Wi-Fi flap mid-backup is ridden out to `succeeded` (or lands
    `connection_lost`/`failed` only after real patience), not a fast `-4`. *(LAB-owed — declared.)*
11. **Gate vs real device.** On hardware, "Back up now" reaches the passcode prompt in ~1–2 s on the
    34 GB device; the device tolerates the ~20 s gate hold; the seeded incremental completes+commits.
    *(LAB-owed — declared; candidate B fallback if it fails.)*

## Gates

- `make gates` + `make image` green in `quince-dev` (the build stage compiles libimobiledevice).
- `make gates-ui-e2e` green (no UI change expected; re-proven).
- Stories 1–8 above (CI). Coverage declared in the report (`backup` + `deviceops` package deltas +
  known-untested list).
- **Lab-owed, declared not faked** (state-honesty rule; sequenced with the Operator): stories 9–11.

## Fixtures

- `deploy/patches/libimobiledevice/0001-*.patch`, `0002-*.patch` (the in-tree patches — reviewable).
- `fake_idevicebackup2_test.go` extended to honor `--gate` (poll the file) and to write a stub
  `Info.plist`, so the engine overlap is exercised without a real device.
- If the 6a-soak hang yields a reproducible transcript, add it to
  `core/internal/backup/testdata/transcripts/` (every lab bug becomes a replay fixture — hard rule).
  **Privacy:** the (ct) pcap captures are LOCAL-ONLY (LAN IPs) and never enter git; any transcript
  fixture is scrubbed of serials/UDIDs/IPs before commit.

## Rule check (written before building)

- **State honesty.** Lab legs (9–11) are *declared* owed, never faked green; a dead link is classified
  `connection_lost` only by the sampler actually firing; the seeding narration is truthful (the seed
  really runs). ✔
- **A rung's goal is provable at rung close.** The goal splits into CI-provable (patch/build, gate
  behavior, threshold logic) + explicitly-owned lab legs with a named owner (the Operator hardware
  day) and the dashboard "done" will state what was/wasn't proven. ✔
- **Never mutate a committed version.** Untouched — all work stays in `working/<udid>`; the Info.plist
  preserve/restore operates only on the mutable working tree pre-commit; commit path unchanged. ✔
- **No silent caps or fallbacks.** Candidate B is config-gated + `SHARED`-only + surfaced; the
  from-source build cost is reported; the 18-min classification is narrated, not silent. ✔
- **Config tidiness (D12).** `LivenessTimeout`/`toolReceiveTimeout` are **code constants** (design §4,
  "NOT v0.1 config keys") — no new `config.yml` key. Candidate B, *if* built, adds one config key with
  a doc-comment + default (spec'd then). ✔
- **Secrets discipline.** No password touches argv/env/log; `--gate` carries a path, never a secret;
  the device encrypts with its own keybag (interface fact 5). ✔
- **Subprocesses.** idevicebackup2 stays a supervised, own-process-group, ctx-killed subprocess; the
  `--gate` change does not alter the group-kill/cancel path. ✔
- **Track boundary / cross-tree edit (amendment A).** This rung touches `core/internal/deviceops/`
  (and possibly `core/internal/device/`) to bound non-backup tool ops — outside the backup tree.
  Authorized by amendment (dg)-A as in-scope for *this* rung because item 1's shared-timeout patch is
  what introduces the regression there; it adds/tightens a timeout only (no contract, no behavior
  change beyond fast-fail), and is surfaced here, not slipped in silently. ✔
- **Every lab bug becomes a replay fixture.** Story-9's hang becomes a scrubbed transcript fixture
  before the fix lands. ✔
- **Version pins looked up.** `LIBIMOBILEDEVICE_REF=1.4.0` was verified live at pin time (releases API
  + Alpine v3.24 APKINDEX; the brief's `1.1.1_git20250201` corrected to `1.4.0-r0`). ✔
- **Privacy is a commit-time gate.** `make privacy-check` before every commit; the pcap fixtures stay
  local-only; patch files + transcripts scrubbed. ✔
- **Docs are part of the diff.** Stack D2 gains the patched-timeout note; design §4 the retuned
  liveness rationale; deploy docs the from-source build. Updated in the same change. ✔
- **Contract boundary.** No contract change intended (enum values reused, states unchanged); if review
  disagrees, STOP and propose — do not build across it. ✔
- **Scope edge (near-miss, listed per the rule):** the qn.7 line is close — this rung does the
  *coarse* liveness retune + the *initial* patched build; it must NOT drift into chaos-suite fine
  tuning, the #8 `-4` reclassification, the netmuxd-USB audition, or UX copy. Held explicitly. ✔

## Lab-leg sequencing (to confirm with the Operator)

Owner = the Operator hardware day (staging CT 113 / lab CT). Suggested order once CI is green and the
image is pushed: (a) redeploy staging with the patched image; (b) story 11 (gate vs real device — the
~1–2 s passcode + 20 s tolerance) on the 34 GB device; (c) story 10 (15-min patience across a real
flap); (d) story 9 (re-run the captured hang). If (b) shows the device resetting on the gate hold →
switch item 2 to candidate B and re-run (a)–(d) minus the gate legs.
