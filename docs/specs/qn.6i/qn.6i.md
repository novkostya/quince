# qn.6i — reconciliation is asynchronous and triggered, and never runs beside a commit

**Goal.** quince answers its API about one second after the process starts instead of ~48 seconds
later, adding a storage returns immediately instead of blocking the button for a full scan, a
reconcile runs on a schedule as well as on those two events — and while it has not finished, quince
says so rather than serving a version list it knows is short.

Rung issue: [quince#731](https://github.com/novkostya/quince/issues/731), Operator-ruled on
2026-08-08 in two parts relayed by the architect seat — first both blockers, then the letter
(*"this is `qn.6i`"*). **It closes [quince#592](https://github.com/novkostya/quince/issues/592) and
[quince#715](https://github.com/novkostya/quince/issues/715)**, which are one defect with two faces:
`Manager.Reconcile` is synchronous on a critical path twice.

**Everything measured below was read in this checkout at `22b695a`, 2026-08-09.** Where a claim is
*not* measured it says so in those words. The two durations quoted — 36 s and 48 s — are from the
issues, on the staging stand, and are cited rather than re-measured here.

---

## Why `qn.6i`, and what it does not fix

**Two live bugs and one ruling.** The bugs are ordinary latency defects with an unusual cost: the
36-second one makes a deploy check indistinguishable from a crashed daemon, and the 48-second one
holds `writeMu` across the whole scan so an unrelated config write queues behind a button press. The
ruling adds a **scheduled** trigger that neither bug asked for, and that is what turns blocker 1 from
a rare race into a routine one.

**What it does not fix.** No backup gets faster, more reliable or more likely to succeed. A device
that backs up today backs up identically afterwards. The rung buys three things — a daemon that
answers, a button that returns, and a registry that repairs itself without a restart — and one
guarantee it does not have today: that a repair pass cannot collide with a commit.

**And what it costs, stated rather than discovered.** After this rung a version list can be **short**
for a bounded window, where today it is complete before anything is served. That is the trade blocker
2 was ruled on: the window is *declared* instead of hidden. A client that ignores the new state gets
today's behaviour with an occasional missing row, which is why the state is on `/api/health` — the one
authExempt endpoint that is explicitly not frozen — and not only in a log line.

---

## Boundary

**In scope**

| tree | what changes |
| --- | --- |
| `core/internal/storage/` | `reconcile.go` gains a trigger-driven runner and a lease; `jobstorage.go`'s binding gains the device; `subsystem.go`'s `CommitJob` takes the lease |
| `core/cmd/quince/` | `live.go` startup ordering and the storage applier; `main.go` reports the new state |
| `core/internal/httpapi/` | `HealthResponse` gains `reconciling` |
| `core/internal/config/` | one new key and its default |
| `core/internal/wire/` | `Storage`'s count comment, which this rung makes conditionally false |
| `docs/` | `contracts.md` §1 and §6, `quince.design.md` §5, this spec |
| `ui/` | one honest indicator for the new state — see D10, which argues whether it belongs here at all |

**Explicitly out of scope**

- **`SweepWork` stays `return nil` on both backends.** quince#731 item 3 says *record, do not build*,
  and `qn.6h` open item 5 makes the zfs case sharper than the issue's own wording. D9 discharges the
  check the two rungs owe each other; it writes no sweep.
- **The file-watch rung** (`qn.6g`'s ruled split): a hand-edit to `config.yml` still needs a restart,
  including for this rung's new key.
- **quince#735**, the snapshot-sourced offsite path. Unrelated, named because it also touches
  `reconcile`-adjacent zfs paths.
- **`Engine.Reconcile`'s own behaviour.** D1 constrains *when* it runs. What it does is untouched.

---

## Interface facts — read at `22b695a`, not recalled

| fact | where |
| --- | --- |
| `Reconcile` runs at startup **before the listener binds** — `buildStorage` → `buildLiveStack` → the router → `runHTTP` | `core/cmd/quince/live.go:238`, `main.go:248`, `main.go:299` |
| `Reconcile` runs **inside the HTTP request** on a live add, from the `storage` config subscriber | `core/cmd/quince/live.go:295` |
| **there is no mutex anywhere in `reconcile.go`** — `grep -c 'sync\.\|Mutex'` returns `0`, exit 1 | `core/internal/storage/reconcile.go` |
| step 1 of every slot's reconcile **completes commits**: `PendingJournals()` → `ResumeCommit(j)` | `reconcile.go:38`, `reconcile.go:46` |
| the commit journal **carries the `JobID`** that wrote it | `core/internal/storage/journal.go:51` |
| a job's storage binding is taken at start and dropped only when the job ends — success, failure, cancel and shutdown alike | `backup/engine.go:260` (bind), `engine.go:1073` (unbind, in `release`) |
| `JobsOn` already answers *which jobs are live on this storage*, under `m.mu` — and its own comment says the way to close its residual window "is to make the check and the write share the lock" | `storage/jobstorage.go:185`, `:181` |
| `Store.InsertVersion` is a **plain `INSERT`** with no `ON CONFLICT` | `core/internal/store/versions.go:36` |
| `Engine.Reconcile` decides `succeeded` vs `connection_lost` by asking `VersionForJob`, and its doc comment says it must run **AFTER** storage reconciliation | `backup/engine.go:377`, called at `live.go:142` |
| `SweepWork` is `return nil` on both backends | `storage/namespace.go:269`, `storage/zfs.go:496` |
| **`storage:` is a LIST**, not a section — `Storage *[]StorageEntry` — so no daemon-wide key can live under it | `core/internal/config/schema.go:18` |
| `/api/health` is authExempt and **explicitly not a frozen contract** | `httpapi/server.go:20`, `:40` |
| `buildStorage` is also the admin CLIs' entry point, and its doc comment promises a **reconciled** Manager on return | `live.go:171`, `admin_cmd.go:42` |

---

## Design

### D1 — What "async" must not break: amendment 1's composition

**This is the constraint quince#731 does not carry, and it is the one that decides the shape.**

`Engine.Reconcile` walks crash-orphaned non-terminal job rows. For each it asks storage
`VersionForJob(udid, jobID)`: **a version means the commit rolled forward, so the job becomes
`succeeded`; no version means the job becomes `connection_lost`** with `error_code: interrupted`.
Its doc comment states the dependency in as many words — *"Run AFTER storage reconciliation and
BEFORE serving (the two reconcilers compose here)"* — and `live.go` orders the two calls accordingly.

**So moving `Manager.Reconcile` wholesale off the startup path silently inverts that composition.**
If the job pass runs before the roll-forward, a backup that transferred for hours, verified, and
crashed one phase from done is written to the database as **interrupted by a restart** — and then
storage rolls its commit forward anyway, leaving a `connection_lost` job beside the version it
produced. A successful backup reported as failed is the sharpest state-honesty defect this rung could
introduce, and it would be introduced *by the fix*.

**Ruled here as rung-local, because it preserves canon rather than changing it:** the ordering stands.
What changes is *which part* of storage reconciliation the job pass waits for — D2.

### D2 — The split: roll-forward is synchronous, the scan is not

`reconcileSlot` does two unrelated jobs in one function:

1. **roll-forward** — read the journals, complete any half-done commit. Reads a handful of small
   files; does work only after a crash mid-commit.
2. **the scan** — per device: `Scan` the root, attribute, adopt, mark missing, recompute latest.
   Walks every device tree on every declared storage. **This is the 36–48 seconds.**

**The rung splits them.** Roll-forward stays where it is: synchronous, before `Engine.Reconcile`,
before the listener binds. The scan moves behind the runner.

**Why this is the right cut and not a compromise.** The property `Engine.Reconcile` depends on is
produced entirely by step 1 — `VersionForJob` answers from the rows roll-forward wrote. Step 2
produces nothing the job pass reads. So the split preserves the composition exactly, without a
barrier, a callback, or a promise to sequence two goroutines.

**The cost, stated rather than left to be found.** Roll-forward is *usually* trivial and is not
free: on the namespace backends `PhaseArchived` moves the displaced tree into `versions/<ts>/`, which
on the `copy` backend is a real copy. So **a start after a crash mid-commit can still be slow**, and
this rung does not make it fast. That is defensible — it is recovery, it is rare, and it is the one
case where finishing before serving is what protects the data — but it means quince#592's window is
**closed in the ordinary case and not abolished**. Anyone re-measuring after a crash should expect it.
Whether that residual deserves its own reporting is open question 2.

**Unmeasured, and named as such:** nobody has timed roll-forward with a real pending journal on any
backend. The 48 s on record is a scan.

### D3 — Blocker 1, ruled: the lease

The Operator ruled **a lock or lease spanning the engine's commit path and reconciliation**, and
rejected *demonstrate `ResumeCommit` is idempotent* on the ground that such a proof is a property of
today's phase set — which `qn.6h` has just changed for zfs. That is settled and is not re-argued
here. What the spec owes is the shape.

**The lease already half-exists.** `Manager.jobStorage` maps jobID → storageID, is taken at
`BindJobStorage` before the job row exists anywhere observable, and is dropped in `Engine.release` —
the one path every ending job takes. It therefore **spans the whole job, including the commit**, and
`JobsOn` already reads it under `m.mu`. Nothing new has to learn what "a live job on this storage" is.

**The proposed shape:**

1. **The binding gains the device.** `BindJobStorage(jobID, storageID)` becomes
   `BindJobStorage(jobID, udid, storageID)` — one call site (`engine.go:260`), which already holds the
   row. Without the udid the Manager knows *a* job is live on a storage but not on which device, and
   every guard below would be a storage-wide sledgehammer.
2. **The lease is per `(storage, device)`**, held by whichever of the two sides is inside a critical
   section: `reconcileDevice` for one device, or `CommitJob` for one job.
3. **The reconciler pre-checks and skips.** Before a device it asks whether a job is bound to that
   `(storage, device)`; if one is, it **defers that device**, logs which job, and moves on. Check and
   claim share `m.mu` with `BindJobStorage`, which is what `JobsOn`'s comment asks for.
4. **The reconciler yields; the commit never does.** A job that binds *after* the pre-check finds the
   lease held. It waits **at most one device's scan** — bounded, and logged when it happens, so it is
   not silent waiting. It is not "a backup blocked": binding happens at job start, hours before a
   commit, and the wait is on the order of the per-device scan.
5. **Roll-forward takes the same lease**, keyed by the journal's own `UDID`, and additionally **skips
   any journal whose `JobID` is currently bound** — a journal a live job is driving is not a journal
   left by a crash, and the file already carries the fact that distinguishes them.

**Why the reconciler is always the loser.** The ruling requires that a held lock not become a hang and
that the losing side *defer and report* rather than block. Reconciliation is idempotent and
re-triggerable by construction — adopt-if-absent, mark-if-vanished, recompute — so deferring it costs
one interval. Deferring a commit costs a multi-hour transfer.

**A deferred device is re-enqueued, not dropped.** The runner marks it and re-triggers when the
blocking job ends (`release` already exists as the single termination path). Re-triggering there
rather than waiting for the next scheduled tick is the difference between *the new disk's backups
appear when the running backup finishes* and *…in six hours*. Rung-ruled: re-enqueue.

**The alternative, named because it is what a reader will reach for.** A single storage-wide mutex
held across `Reconcile` is smaller. It also serialises a whole disk's reconcile against a single
device's backup, and — the ruling's own objection to a different proposal — it would make the *engine*
wait, which is the one thing forbidden. Rejected.

### D4 — The hazard is wider than roll-forward, and the issue does not say so

quince#731 states the roll-forward race precisely and stops there. Reading the adopt path at
`22b695a` finds a second one, in ordinary code with no journal involved:

```
engine:      Backend.Commit(req)         → the artifact exists on disk
   ⟵ scan:   Scan(udid) sees the artifact, finds no row, adopt() → InsertVersion
engine:      registerCommitted(...)      → InsertVersion → PRIMARY KEY conflict → error
             CommitJob returns an error  → a COMPLETED backup is reported failed
```

`Store.InsertVersion` is a plain `INSERT` (`versions.go:36`), so the second insert fails rather than
merging. The version survives — as an **adopted** row with `job_id` null, which is deliberately
retention-protected — and the job that produced it lands in a failed state.

**Reasoned from the code, not measured.** No test drives a scan concurrently with a commit today. PR 2
is where this becomes a measurement: if the window is genuinely unreachable, the finding is withdrawn
in that PR's text rather than left standing.

**It matters to the design regardless of the outcome**, because it is the reason the lease is held
across `CommitJob` and not merely across roll-forward. A guard scoped to journals would leave this
one open, and scheduling is what makes it routine.

### D5 — Blocker 2, ruled: `reconciling` on the wire

The Operator ruled **serve, and report `reconciling`** — bind immediately, surface the incomplete
state — and rejected refusing until reconciled, which is honest by construction but keeps quince#592's
dead window and multiplies it by the schedule.

**Where it goes.** `GET /api/health` gains one field. Health is authExempt, is already the endpoint a
deploy check polls, and its own doc comment records that it is deliberately not frozen — three reasons
that all point the same way.

```go
// HealthResponse
Reconciling bool `json:"reconciling"`
```

**What it promises, written down because a state nobody can act on is decoration:**

- **`true` means a version list may be SHORT.** Versions present on disk but not yet adopted are
  absent from `GET /api/versions` and from `Storage.backup_count`; rows whose artifact has vanished are
  not yet marked `missing`. **This is a declared provisional state, not an empty result** — a client
  must not conclude *this disk has no backups* while it holds.
- **`false` means the last triggered pass completed.** It does not mean the disk was read a moment
  ago; the counts remain what the DB says, which is what they have always been.
- **It is daemon-wide, not per storage.** A per-storage flag is the more precise answer and is
  deliberately not built: `Storage` already carries `reachable` + `unreachable_code` +
  `unreachable_reason`, and adding a fourth state field to that object is a bigger contracts change
  than this rung needs. Open question 1 records the argument for revisiting it.
- **A boolean, not a string.** `mode` is a string because a third mode must not need a second field;
  here there are exactly two states and no candidate third. If a third ever appears — *deferred*, say —
  it is a widening, and this sentence is the note that says so.

**And one comment this rung falsifies.** `wire.Storage`'s counts carry *"the counts are CURRENT, not
a last-known reading … there is nothing to date"* (quince#588, ruled 2026-08-03). That stays true of
*staleness* and stops being the whole story: the counts can now be **incomplete** while `reconciling`
is true. The ruling is untouched — no timestamp is added — but the comment is edited in the same diff
that makes it conditionally misleading, per *docs are part of the diff*.

### D6 — The triggers and the runner

**One runner, one queue, one pass at a time.** Three triggers enqueue; the runner collapses duplicates
(a queued pass that has not started absorbs another request rather than queuing twice) and runs one at
a time. Concurrency here would buy nothing — the work is disk-bound — and would reintroduce exactly
the class of race D3 exists to remove.

| trigger | when | notes |
| --- | --- | --- |
| **startup** | once, immediately after the listener binds | roll-forward has already run synchronously (D2) |
| **storage-added** | from the `storage` config subscriber, when `addedStorage(before, after)` | the applier **enqueues and returns**; the request no longer waits |
| **scheduled** | every `reconcile.interval_minutes` | D7 |

**The add path's warning changes shape.** Today a failed post-add reconcile returns a `config.Warning`
in the `POST` response saying backups "may not be listed until quince restarts". Once the scan is
enqueued there is no result to report in that response, so **the warning is deleted rather than
reworded** — a warning about an outcome the handler can no longer observe is a claim it cannot make.
The honest replacement is `reconciling` on health plus the log line, which is what the user watching
the storage card actually sees.

**The admin CLIs keep the synchronous path.** `buildStorage` promises a *reconciled* Manager on return
and `versions verify` depends on it. The runner exposes a synchronous "run one pass now and wait"
entry that the CLI uses; only `serve` enqueues. Stated because the alternative — a CLI that starts,
enqueues, and exits before the scan runs — reads correct and reports on a registry nobody repaired.

### D7 — The interval is a config key (D12)

```yaml
reconcile:
  interval_minutes: 360        # 0 disables the scheduled pass; startup and add still fire
```

- **A new top-level section, because there is nowhere else for it.** The obvious home — under
  `storage:` — **does not exist**: `storage:` is a *list* (`Storage *[]StorageEntry`,
  `config/schema.go:18`), so a daemon-wide key placed there would have to become a property of every
  declared storage, which is a different setting with a different meaning. `automation:` was the other
  candidate and is declared as `qn.12`'s, holding two keys nothing reads; adding a live key to a
  section whose contract is *"declared debt"* would make that row of contracts §6 false.
- **Integer minutes, following the file's existing convention** — `sessions.ttl_minutes`,
  `automation.staleness_days`, `automation.reminder_cooldown_hours`. A duration string (`6h`) is more
  expressive and matches nothing in the document. Consistency inside one file wins.
- **`0` disables the schedule and nothing else.** Startup and storage-added still fire, because they
  are correctness triggers rather than hygiene ones.
- **Written only when set** — quince#728's 2026-08-08 rulings: the file contains only what was set and
  carries no generated annotation. This key ships during `qn.6j`'s lifetime, so it must not be the one
  key that reintroduces default-pinning.
- **Live or restart?** It joins `qn.6g`'s per-key table (contracts §6) as **live** — the runner reads
  the interval when it schedules the next pass, so an edit takes effect from the following tick. That
  is a real answer rather than a convenient one, and the table's third bin exists precisely so a key
  read by nothing is not called *restart-required*.

### D8 — A reconcile in flight when the storage list is replaced

New with concurrency and not present today: `ApplyStorages` can swap the whole slot list while a pass
is running. `slotsSnapshot()` copies under `m.mu`, so the pass keeps working on **slots that may no
longer be declared** — including one the user has just forgotten.

**Ruled rung-local: the pass re-reads the declaration between slots and drops any that is no longer
declared, logging it.** Mid-slot it finishes the current device — abandoning a device halfway leaves
the registry in exactly the half-repaired state reconciliation exists to remove, and `qn.6g` already
established that a forget is refused while a job runs on that storage, so the destructive case is
already closed elsewhere.

### D9 — `SweepWork`: recorded, not built — and `qn.6h`'s owed check, discharged

`qn.6h` open item 5 says *whichever of these two rungs lands second owes the other a check*.
`qn.6h` merged first. This is the check.

**Nothing in this rung calls a sweep that does anything**, because `SweepWork` returns `nil` on both
backends and this rung does not implement it. So the hazard both specs describe stays latent, and this
rung neither closes nor worsens it.

**What it does add is the constraint, in the form `qn.6h` sharpened it.** Two different failures now
sit behind one call site:

- **namespace** — quince#731's version: a sweep that cannot tell a live job's `working/` from an
  orphan destroys a multi-hour transfer.
- **zfs, after `qn.6h`** — quieter and different: there is no `working/` at all, so a sweep either
  finds nothing, or, if generalised to *job scaffolding*, reaches the **sentinel in the parent
  dataset** and clears a live job's dirty marker, making a resumable head look clean.

**Whoever implements `SweepWork` inherits both sentences, and now also inherits the lease** — D3's
per-`(storage, device)` claim is the mechanism that answers *is a job live here*, which is the question
a safe sweep has to ask. That is this rung's actual contribution to the problem: it does not build the
sweep, and it leaves behind the thing the sweep needs.

### D10 — Does the UI say it?

*No silent caps or fallbacks* names the UI explicitly: degraded modes are surfaced in the UI **and**
the logs. A knowably-short version list is a degraded mode. The ruling, however, names `/api/health`
and the storage payload and stops there.

**Planned as the last PR and severable.** One indicator driven by `health.reconciling` — the wording
belongs to `docs/ui.design.md`'s taste rather than to this spec — and it renders nothing new when the
flag is false. If review rules the rung ends at the wire, PR 6 becomes an issue and this section
records why it was proposed.

---

## Stories

1. **The daemon answers immediately.** `quince serve` against a data dir with existing versions
   accepts a connection on `/api/health` within ~1 s, and the log still shows reconciliation
   progressing afterwards. *(quince#592)*
2. **Health says what it is doing.** While the startup pass runs, `GET /api/health` returns
   `reconciling: true`; after it completes, `false`.
3. **Adding a storage returns at once.** `POST /api/config/storage` responds without waiting for a
   scan, and the new disk's existing backups appear without a restart once the enqueued pass
   completes. *(quince#715)*
4. **`writeMu` is no longer held across a scan.** A second config write issued immediately after an
   add is not queued behind a reconcile.
5. **A reconcile does not run against a device with a live job on that storage.** It defers, names the
   job in the log, and runs when the job ends.
6. **A commit is never driven by two actors.** A journal whose `JobID` is currently bound is not
   rolled forward.
7. **A commit concurrent with a scan does not fail the job.** Whatever the outcome of D4's
   measurement, the committed version is registered once and the job reaches `succeeded`.
8. **The scheduled pass fires.** With `reconcile.interval_minutes` set small, a pass runs without any
   startup or add event; with `0`, none does.
9. **The job-row reconciler still composes.** A crash mid-commit followed by a restart yields a
   `succeeded` job with its version — not `connection_lost`. *(D1's regression guard)*
10. **The admin CLI still gets a reconciled registry.** `versions verify` behaves exactly as it does
    today.

---

## Gates

Beyond `make gates` / `make image`:

| id | story | how |
| --- | --- | --- |
| **G1** | 1 | on the demo container, time from process start to the first `200` on `/api/health`; recorded against the 36 s and 48 s on the issues |
| **G2** | 2 | Go test: health reports `true` while a pass is in flight and `false` after; plus the demo observation |
| **G3** | 3, 4 | Go test on the applier: the subscriber returns without calling the scan, and a second write is not serialised behind one |
| **G4** | 5 | `go test -race`: a bound job on `(storage, device)` makes the pass defer that device, and it runs after `UnbindJob` |
| **G5** | 6 | a pending journal whose `JobID` is bound is skipped; the same journal with an unbound id is rolled forward |
| **G6** | 7 | **SPLIT IN THREE BY PR 2, because one test could not do the job.** **G6a** interleaves a scan between `Backend.Commit` and `registerCommitted` deterministically — the D4 measurement, and a permanent tripwire on the seam. **G6b** runs a commit and a scan concurrently under `-race` — a regression net, *not* a proof: it passed 5/5 with the guard removed. **G6c** holds the lease exactly as `CommitJob` does and asserts the scan declines to enter — the only one of the three that is red without the lease |
| **G7** | 8 | fake clock: N passes in M intervals; `0` produces none |
| **G8** | 9 | the existing crash-mid-commit fixture, restarted, asserting `succeeded` — red if the ordering is inverted |
| **G9** | 10 | `versions verify` against a fixture with an unadopted on-disk version still reports it |
| **G10** | — | `make demo` + the click-list: add a storage, watch the card populate without a restart |

**No hardware gate is owed by this rung, and that is declared up front rather than at the end.**
Everything above is provable in the pinned container or on the demo deploy. Nothing here touches the
device transports, the muxers, or `idevicebackup2`.

---

## Fixtures

- **A pending commit journal whose `JobID` is bound** — the negative for G5. Built from the existing
  journal fixtures; no new hardware capture.
- **A storage tree holding a committed version with no registry row** — already exists for the adopt
  path; reused for G9 and G1's timing.
- **A fake clock for the scheduler** (G7), following the pattern the existing engine tests use.

No new transcripts. This rung fixes no bug found on hardware, so the *every hardware bug becomes a
replay fixture* rule has nothing to add here.

---

## Rule check

| rule | how this rung complies |
| --- | --- |
| **State honesty** | The whole of D5. A short list is declared, never silently served. D1 is the near-miss: the obvious implementation of this rung would report a successful backup as `connection_lost`, and G8 is the gate that keeps it red. |
| **Never mutate a committed version** | This rung adds no write path to `latest/`, `versions/` or any snapshot. D3 exists precisely so a second actor cannot drive `renameat2(RENAME_EXCHANGE)` beside the engine. |
| **Roll-forward** | Untouched, and D2 keeps it *before* serving rather than moving it. **The near-miss is real and named**: a lease that deferred a journal forever would leave a verified artifact un-registered indefinitely. D3 defers per pass and re-enqueues on job end, so a deferral has a defined end; G5 asserts the journal is rolled forward once the job is unbound. |
| **No silent caps or fallbacks** | A deferred device is logged with the job that blocked it; a bounded wait at commit is logged when it occurs; `reconciling` is on the wire and D10 proposes the UI half. |
| **Config tidiness (D12)** | One key, `reconcile.interval_minutes`, with a default, a Settings control, and a row in contracts §6's per-key table. Written only when set, per quince#728. |
| **Docs are part of the diff** | contracts §1 (health), contracts §6 (the `storage[]` row's *"an add also reconciles"* claim, which becomes *shortly after*), design §5 (reconciliation is no longer described as a startup-only subsystem), and `wire.Storage`'s count comment — each in the PR that makes it true. |
| **Coverage declared** | Each PR carries its `go test -cover` summary and a known-untested list. Already known: the scheduler's long-interval path is exercised only with a fake clock. |
| **Secrets discipline** | No password, token or credential is on any path this rung touches. |
| **Subprocesses** | None added. |
| **Privacy** | `make privacy-check` before every push; the only measurements quoted are durations and line numbers. |
| **Don't improvise architecture** | Both blockers arrive **ruled** and are cited rather than re-argued. D1 and D4 are findings this spec adds; D1 preserves canon and is rung-ruled, D4 changes no contract and is a reason for the ruled lease rather than a new decision. Anything that turns out to need a ruling is listed below rather than built. |

---

## Rung-ruled decisions

1. **The ordering stands; the split is what moves** (D2). Roll-forward stays synchronous and before
   `Engine.Reconcile`; only the per-device scan becomes asynchronous.
2. **The lease is per `(storage, device)`, and the binding gains the udid** (D3).
3. **The reconciler always loses, and a deferred device is re-enqueued on job end** rather than left
   to the next scheduled tick (D3).
4. **`reconciling` is a daemon-wide boolean on `/api/health`**, not a per-storage state (D5).
5. **The interval key is a new top-level `reconcile:` section, in integer minutes** — `reconcile.interval_minutes`,
   integer, default `360`, `0` disables the schedule only (D7).
6. **The post-add warning is deleted, not reworded** (D6): the handler can no longer observe the
   outcome it was claiming.
7. **A pass drops a storage that has been undeclared beneath it, between slots** (D8).

---

## Known gaps and open questions

1. **Per-storage `reconciling` was considered and not built** (D5). One disk being scanned while
   another is idle is a real distinction the daemon knows and the wire does not carry. Deliberately
   deferred rather than dropped: it is a `Storage` object change, and that object already carries three
   fields describing its condition.
2. **A slow roll-forward after a crash still delays the listener** (D2), and nothing reports it. The
   window is closed in the ordinary case and not abolished. Whether recovery deserves its own reported
   state is not decided here.
3. ~~**D4 is reasoned, not measured.** G6 settles it.~~ **MEASURED IN PR 2 (quince#771), and the
   answer splits in two — the mechanism is REAL and the RATE is still unknown.** G6a interleaves a
   scan between `Backend.Commit` and `registerCommitted` by hand: the adopt path inserts the version
   and the engine's own insert then collides on the primary key, so a completed backup is reported
   failed. That is now a deterministic gate. **What is NOT established is that a real scheduler ever
   lands there**: the first attempt at G6 raced a commit against a scan loop and **passed 5/5 with the
   guard removed**, because the window is microseconds wide. So the finding is **confirmed as a seam
   property and unconfirmed as a field event**, and nobody has a rate.
   **Recorded rather than collapsed into "measured", because the two halves license different
   things.** The mechanism is what justifies holding the lease across `registerCommitted` rather than
   only across roll-forward. The missing rate is why nobody should claim this rung fixed an observed
   bug: it closed a window that has never been seen to open.
4. **Roll-forward duration is unmeasured on every backend.** Nobody has timed `ResumeCommit` with a
   real pending journal, so D2's "usually trivial" is an argument from what the code does, not a
   measurement.
5. **`RepairWorkingCopy` is still device-scoped** (`subsystem.go:23`'s standing list) and this rung
   does not fix it. Named because the lease's key is `(storage, device)` and a reader may expect that
   to have resolved it. It does not.
6. **Whether the UI half is this rung's** (D10).

---

## PR slicing

**Sequenced from `main`, never stacked** (canon §1). Branches `r2/…`.

| | claim | gates |
| --- | --- | --- |
| **1** | **this spec** | reviewed before any code exists |
| **2** | **the lease** — the binding gains the udid; roll-forward skips a bound journal; `reconcileDevice` and `CommitJob` claim per `(storage, device)`. Behaviour-preserving except that a reconcile now defers instead of colliding | G4, G5, G6 |
| **3** | **the runner + async startup** — roll-forward stays synchronous, the scan is enqueued, the listener binds first, health reports `reconciling`; contracts §1 and design §5 in the same diff | G1, G2, G8, G9 |
| **4** | **the add trigger** — the applier enqueues and returns, the warning is deleted; contracts §6's `storage[]` row edited | G3 |
| **5** | **the schedule** — the config key, its default, its Settings control, its contracts §6 row | G7 |
| **6** | **the UI indicator** — severable per D10 | G10 |

**PR 2 lands before PR 3 deliberately.** The ruling says the hazard is reachable on `main` today and
the fix is not conditional on the scheduling half. Landing the guard first means the runner cannot be
read as having introduced the race it is guarded against — and it means the one PR that is pure risk
reduction is not held behind the one that changes startup.
