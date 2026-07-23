# qn.5b — atomic `latest` + the `working/` lifecycle redesign

**Goal.** A `zfs snapshot` taken at *any* instant of a running backup or a commit contains a
**complete `latest/`** (never a partial, never none), and a continuous `rclone sync` across many
commits **never deletes or tears** the remote copy — because `latest/` is replaced by one atomic
`renameat2(RENAME_EXCHANGE)` instead of two non-atomic renames, and the backup is written into a
per-job `working/` seeded from `latest/` so that between backups the dataset holds **only
`latest/`**.

> **STATUS: APPROVED WITH AMENDMENTS — BUILDING (decisions log (co), 2026-07-22).** All seven gate
> forks ruled **YES**; the Reset REST proposal **accepted**. Two Operator amendments are folded in:
> **A** — the seed clone uses the backend's SAFE strategy (**hardlink → copy** until 12c), never
> "reflink" (§"The privilege split"); **B** — the snapshot name **keeps the ULID** and widens the
> date to `YYYY-MM-DDTHH-MM` (§"Snapshot name reorder"). The pre-approval review-gate section is
> retained below as the record of what was ruled.

---

## Handoff review of qn.5 + qn.4a/qn.4c storage seams (program loop; four dimensions)

This is the rung whose surface I consume: `internal/storage` (qn.5, landed `285c40b`..`3ce5bb1`)
and the `backup.Storage` seam it feeds (qn.4a/qn.4c). The review is **read-anchored this session**
(a spec-only session that stops before building); the **run-anchored baseline** — `make gates` on
the inherited tip in `quince-dev` + driving the storage/engine seams the redesign rewrites — is
scheduled as the **first build action** once the review gate approves, so findings land as
`qn.5 review fix:` commits *before* qn.5b's own commits (program §"Findings triage"). The inherited
code is CI-proven (qn.5's 11 stories + reconciliation matrix) and hardware-proven (qn.4c: 33.3 GB
full + incremental committed, `bclonesaved` moved), so this is a foundation check, not a re-gate.

- **(a) seams — the surfaces qn.5b rewrites, confirmed by reading them against their specs:**
  - `Backend` interface (`storage.go:70`): `WorkDir`/`TreePath`/`Commit`/`ResumeCommit`/`Discard`/
    `RepairWorkingCopy`/`Scan`/`SweepWork`. qn.5b changes the *contract* of `WorkDir` (returns the
    idevicebackup2 **target**, seeds `working/<udid>` from `latest/`), unifies `Discard`
    (keep-dirty-working on **all** backends), and reworks `Commit`/`ResumeCommit` phases.
  - `backup.Storage` seam (`backup.go:108`): `Seed → VerifyTree → CommitJob | Discard`. The engine
    (`engine.go:284`) drives it through `backing_up → verifying → committing`. qn.5b replaces the
    symlink adapter (`supervisor.go:65 prepareTarget`) — the engine will pass `Seed`'s returned
    target straight to `idevicebackup2`, and verify via a tree-less `VerifyWork(udid, jobID)`.
  - The two **non-atomic swap paths** the rung fixes — verified by reading, not by trusting canon:
    - `zfs.go:203-214` (`rebuildLatest`, in-container / delegated-exec path): `os.Rename(latest,
      old)` then `os.Rename(staging, latest)` — between them `latest/` **does not exist**. Comment
      literally says "Atomic swap" (line 203); it is not.
    - `deploy/storage.md:87-88` (host-side hook `mirror` verb): `[ -d "$mp/latest" ] && mv
      "$mp/latest" "$mp/latest.old"; mv "$mp/latest.new" "$mp/latest"` — same gap, host-side.
    - **Third instance found (not in the (cg) writeup):** `namespace.go:104-153` (`finishRotation`)
      has the same class of gap — it archives `latest/` → `versions/<prev-ts>/` (line 120) and only
      then promotes `work/<job>` → `latest/` (line 141); **between those two renames `latest/` does
      not exist either.** The namespace `latest/` is an rclone target too, so this is the same
      remote-deletion hazard on reflink/hardlink/copy deployments. qn.5b's exchange model closes all
      three by construction.
- **(b) coverage:** qn.5's known-untested declaration held on read; the reconciliation matrix
  (`reconcile_test.go`) covers adopt / mark-missing / roll-forward per phase. The redesign adds new
  phases (`exchanged`) and a **non-idempotent exchange** (re-running it *undoes* it), so a
  marker-guarded resume + its kill-matrix test are net-new obligations (§Stories 5, §Gates).
- **(c) state honesty:** the current `Version.kind` is derived from `Status.plist.IsFullBackup`
  (`verify.go:57-64`), which the lab proved lies — a first 33 GB backup writes `IsFullBackup:false`
  (gate-11 finding #9(a), (cj)/(ck)). qn.5b makes `kind` **authoritative** from the seed decision.
  Everything else read honest.
- **(d) contracts:** spot-checked `Version` (§2) `browse_root`/`zfs_snapshot`/`kind`/`is_latest`
  and the `version.*` events — all served as frozen. qn.5b moves `browse_root` `…/working` →
  `…/latest` and reorders the snapshot name (both §2 example edits), and **proposes** one new REST
  endpoint (Reset) — the contract-touching pieces are exactly what the review gate rules.

**Outcome:** no blocker found in the inherited code; the three non-atomic windows are the rung's
subject, not review fixes. The run-anchored `make gates` baseline + seam-driving run at build-start.

---

## The defect, restated from the code (not the prose)

The Operator's three storage constraints (roadmap, (cg)): **(1)** a snapshot at any instant
captures a solid `latest/`; **(2)** the directory `idevicebackup2` writes into is rclone-excluded;
**(3)** changes to `latest/` are **atomic**. **Constraint 3 fails** in three places (above). The
consequence has **two observers that fail independently** (roadmap gate, Operator-sharpened):

- **Snapshot observer:** a `zfs snapshot` landing in a swap window is perfectly consistent and
  perfectly useless — it captures a device with **no `latest/` at all**.
- **Filesystem-walk observer:** a continuous `rclone sync` crossing the window sees `latest/`
  missing and **mirrors the deletion — it deletes the remote B2 copy** (a wipe + full re-upload,
  not the "briefly mixes two valid versions" the stale `deploy/storage.md:144-146` note claims).

`deploy/storage.md:144-146` already *documents* the window ("The only non-atomic instant is the
`latest/` swap itself (two renames)") and understates it. qn.5b removes the window.

---

## Boundary

**In scope** (trees/files):

- `core/internal/storage/`: `zfs.go`, `namespace.go`, `zfscli.go`, `layout.go`, `verify.go`,
  `journal.go`, `subsystem.go`, `reconcile.go`, `offsite.go`, `marker.go` (+ tests), and a new
  `exchange_linux.go` / `exchange_other.go` pair (the `RENAME_EXCHANGE` primitive, OS-guarded).
- `core/internal/backup/`: `supervisor.go` (delete the symlink adapter), `engine.go`/`backup.go`
  (the `Storage` seam: `Seed` returns the target; `VerifyWork`; `Discard` keeps dirty working).
- `core/internal/config/` + `wire`: the `storage.zfs.mirror` → `storage.zfs.seed` rename **iff the
  review approves it** (§gate decision 6); otherwise a doc-comment update only.
- `core/internal/httpapi/` + `core/cmd/quince/`: the **Reset** REST surface (**iff the contract
  proposal is accepted**) + its `RepairWorkingCopy` reconciliation.
- Canon (docs are part of the diff): `quince.stack.md` (D5/D5a), `quince.design.md` (§4/§5),
  `contracts.md` (§2 examples + the new endpoint iff accepted), `deploy/storage.md` (the hook + the
  offsite filter block). **Canon edits happen after the ruling, during build — not this session.**

**Explicitly out of scope:**

- **The multi-storage epic (cl).** qn.5b stays single-storage but must not *hard-bake* it: paths
  stay `<backups>/<udid>/…` (storage-scopeable to `<storage>/<udid>/…` later) and `last_backup`
  derivation stays per-version (tolerant of going per-(device,storage)). Costs nothing now.
- **Gate 12c** (the destructive hardlink-safety matrix) — deferred past the freeze; the hardlink
  tier stays disabled-to-copy, surfaced (unchanged posture). qn.5b must not *rely* on hardlink
  correctness it doesn't prove.
- **Wi-Fi drop classification #8, progress/liveness shaping #10-percent** — qn.7.
- **UI** — the failed-attempt "needs attention · Retry" line (#6), two-`latest`-badges (#7),
  `kind`-off-the-card + delta-size (#9(a) user half, (ck)), byte labelling (#10) — all **qn.6a**.
  qn.5b lands the **backend + REST** for Reset and the **internal** honest `kind`; the UI consumes
  them next rung.
- **qn.8 content verification** (the vault canary) — unchanged; qn.5b is structural only.

---

## Design

Canon implemented (linked, not repeated): stack **D5**/**D5a** (the two version models heading
toward one; the whole-tree offsite contract + anchored filters), **D3** (verified-commit-or-discard,
journaled phases, first-class reconciliation, roll-forward); design **§4**/**§5**/**§6**; contracts
**§1**/**§2**/**§3**/**§6**; decisions **(cg)**(the insertion)/(cj)/(ck)(finding #9(a))/(cl)(the
multi-storage guardrail). The banked interface fact: `renameat2(RENAME_EXCHANGE)` **is** supported
on the Operator's ZFS (inode-swap + content-swap measured on the real pool; PVE 9 ships `exch` via
`util-linux-extra`) with the constraint that both paths be **ordinary directories on the same
mounted filesystem** — which the per-job design respects. The symlink-swap fallback ladder is
therefore **dead** (D5a's no-symlink rule stands).

### The unified model — one lifecycle, four backends

Today there are two lifecycles: zfs keeps a **persistent** `working/` (Seed is a no-op) and mirrors
it into `latest/` at commit; namespace seeds a **per-job** `work/<job>` from `latest/` and rotates.
qn.5b collapses them (the D5 "two models → one" the roadmap wants):

**Layout (all backends), `deviceDir = <backups>/<udid>/`:**

- `latest/` — the newest committed version's live directory; **permanent** between backups; the
  sole rclone-synced payload. Provisioned as an empty dir on first use.
- `working/` — a **per-device staging parent** that exists **only during/after a job**. The backup
  tree lives at `working/<udid>/`. Removed on success; **kept dirty on failure** (resume).
- `versions/<ts>/` — rotated-out prior versions (**namespace only**; zfs versions are snapshots).
- Between (successful) backups the dataset holds **only `latest/`** (zfs: no `working/`, no
  `versions/`), so every snapshot structurally contains exactly one complete backup.

**Why the tree is `working/<udid>/` and not `working/` — this drops the symlink dance (scope #4):**
`idevicebackup2 backup <target>` writes into `<target>/<UDID>/` (INTERFACE FACT, already relied on).
Point the tool at **`working/` as the target** and it writes straight into `working/<udid>/` with
**no symlink, no `.quince-targets` stub**. And because the target is derived from the storage
backend's own device dir, it is **always on the storage filesystem** → `idevicebackup2`'s free-space
`statfs(target)` is truthful **by construction** → the gate-blocking free-space bug (28b97de) is
**structurally impossible**, not merely fixed. `supervisor.go`'s `prepareTarget`/`targetRootFor`/
`targetStubDir` are **deleted**.

### Seed (`WorkDir`) — seed-from-latest, or resume the dirty working

```
WorkDir(udid, jobID) -> (target string):          # target = <deviceDir>/working
  Provision(udid)                                  # idempotent; ensures deviceDir + latest/
  tree := <deviceDir>/working/<udid>
  if tree exists and is non-empty:                 # a prior failed attempt's dirty working
      log "resuming dirty working" ; return <deviceDir>/working     # already seeded; kind from sentinel
  mkdir <deviceDir>/working
  if latest/ is non-empty:
      clone latest/ -> working/<udid>              # via the backend's SAFE strategy (see split); incremental
      seededFromLatest = true
  else:
      mkdir working/<udid>                         # first/full backup
      seededFromLatest = false
  writeSeedSentinel(deviceDir, {seeded_from_latest})   # survives crash/resume; see below
  return <deviceDir>/working
```

- **One per-device `working/`** (single-flight per UDID makes a job key unnecessary and simpler).
- **Resume is automatic:** a non-empty `working/<udid>` is *always* resumed (never re-seeded).
  MobileBackup2 increments from a pre-populated `<target>/<UDID>/` exactly as namespace already
  relies on. So a fresh "Back up now" *and* a Retry both resume a dirty working — the only explicit
  discard is **Reset** (§Post-failure UX). A 33 GB Wi-Fi backup dying at 90% never restarts.
- **Honest `kind` falls out of the seed decision** (finding #9(a), (cj)/(ck)): `seeded_from_latest
  ⇒ incremental`, `started empty ⇒ full`. This is *authoritative* — quince knows whether it seeded
  — replacing the lying `Status.plist.IsFullBackup`. Stored in a **seed sentinel**
  `<deviceDir>/.quince-work.json` (a tiny sidecar, NOT inside `working/<udid>` — it must not ride
  into `latest/` at the exchange), so a resume/crash recovers the right `kind`. `verify.go`'s
  `Verify(treeDir)` gains a `kind` parameter (the seed-derived value); it stops deriving `kind` from
  `IsFullBackup`. The encrypted-verify **blob-shard check** (`verifyEncryptedDB`, `verify.go:99`)
  now runs on a *genuinely* full backup — today a mislabeled-incremental first backup silently skips
  it, so a broken first backup could pass verification (the correctness bug #9(a) names).

### Commit — verify → **atomic exchange** → snapshot/archive (scope #1, #3)

The reorder makes the version *be* `latest/` and `browse_root` point at the real latest backup.

**zfs** (`prepared → exchanged → snapshot_created`), replacing `prepared → snapshot_created →
latest_rebuilt`:

```
CommitJob:
  tree := working/<udid>
  Verify(tree, seedKind)                         # from the sentinel; OK required
  WriteMarker(tree, ...)                          # marker rides the tree into latest/
  journal{phase: prepared, ZFSSnapshot: <ds>@quince-<date>-<id>, kind, ...}
  finishCommit:
    prepared:   if ReadMarker(latest).VersionID != versionID:   # marker-guard (exchange is NOT idempotent)
                    exchange(working/<udid>, latest)             # renameat2(RENAME_EXCHANGE) — atomic
                phase = exchanged
    exchanged:  rm -rf working/ ; rm .quince-work.json          # dataset now holds only latest/
                Snapshot(<ds>, quince-<date>-<id>)               # via hook/exec; "already exists" tolerated
                phase = snapshot_created
    snapshot_created: clear journal
  browse_root := <deviceDir>/.zfs/snapshot/<snap>/latest         # was …/working
```

**namespace** (`prepared → exchanged → archived`), replacing `prepared → previous_archived →
latest_promoted`:

```
  prepared:  PrevTS := tsOf(ReadMarker(latest))    # captured BEFORE the exchange (empty on first backup)
             if ReadMarker(latest).VersionID != versionID: exchange(working/<udid>, latest)
             phase = exchanged
  exchanged: if working/<udid> non-empty:          # old latest content, post-exchange
                 mv working/<udid> -> versions/<PrevTS>/     # archive the previous version
             rm -rf working/ ; rm .quince-work.json
             phase = archived
  archived:  clear journal
```

- **The exchange never leaves `latest/` unoccupied** — one syscall flips the entry. The subsequent
  archive (namespace) / snapshot (zfs) touch `working/` and `versions/`/snapshots, **never
  `latest/`**. All three of today's windows vanish.
- **Exchange is not idempotent** — re-running it *reverses* the swap. Crash-safety is a **marker
  guard**: after the exchange `latest/` carries *this* version's `VersionID`; a resume at `prepared`
  that sees it skips straight to the next phase. This is the one genuinely new reconciliation
  subtlety and gets its own kill-matrix test.
- **First backup:** `latest/` is the empty Provision dir → the exchange swaps empty ↔ full (both
  dirs exist, the exchange's precondition); namespace archives nothing (old content empty). ✓

### The privilege split — where the reflink and the exchange run (scope #1)

The exchange needs **no privilege** (a plain VFS rename requiring only directory-write on both
parents). The **reflink** (FICLONE, on the reflink/zfs backends) is the privileged part, and it **moves from commit-time to
seed-time** (clone `latest/` → `working/<udid>` at job start). This is the reconciliation of
scope-item-1's "the hook keeps the FICLONE reflink" wording with the per-job model: the hook still
owns the privileged FICLONE — it just clones **into `working/`** now instead of into `latest.new`.

| backend / mode | seed clone via the backend's SAFE strategy (`latest/` → `working/<udid>`) | exchange | snapshot |
|---|---|---|---|
| **zfs, hook** | host-side, via a hook **`seed`** verb (replaces `mirror`) — FICLONE `EPERM`s in the unprivileged userns (gate-12 (bi)); the host reflinks + chowns to the container uid | **in-container** Go `renameat2` (recommended; §gate decision 2) | host-side hook `snapshot` (unchanged) |
| **zfs, exec / hookless** | in-container `clonetree` ladder **reflink → copy** (the hardlink tier is gated to copy for the seed until 12c — amendment A; surfaced) | in-container | exec/host |
| **reflink / hardlink / copy** | in-container `clonetree.Clone(working/<udid>, latest, seedStrategy)` where `seedStrategy` maps **hardlink → copy** (amendment A) — reflink and copy pass through | in-container | n/a |

> **Amendment A ((co)) — the seed clone is NEVER hardlink until 12c.** Seeding the **hardlink**
> backend would make `working/<udid>` share inodes with the committed `latest/`, so an in-place
> write by `idevicebackup2` (any file class not yet on `clonetree.MutatesInPlace`) corrupts the
> committed version through the alias — the exact hazard the deferred 12c matrix governs. So the
> seed uses the **backend's safe strategy**: reflink (independent CoW) and copy are safe; **hardlink
> downgrades to copy (surfaced)**. A `seedStrategy(clonetree.Strategy)` helper enforces this
> (`Hardlink → Copy`, others pass through); the prose says "the backend's safe strategy," never
> "reflink." The hardlink backend is thereby *disabled-to-copy for the seed too* (space-shared
> versions return when 12c proves the `MutatesInPlace` list complete).

- The hook's **`mirror` verb is removed**; a **`seed` verb** is added (host-side
  `cp -a --reflink=always latest working/<udid>` + `chown -R "$CTUID" working` so the container can
  write/rename its children, reporting `SHARED`/`COPIED` for the honest space claim). The Operator's
  deployed `quince-zfs-helper` must be updated — a real, one-time deployment cost (§gate decision 3).
- The commit-time mirror **disappears entirely**: `latest/` *is* the backup (via exchange), and the
  snapshot captures it. Net effect is **less** space (no mirror duplication) and **fewer** privileged
  operations. `MirrorReport`/`LastMirror`/`/api/health` "mirror" surfacing is renamed to **seed**
  reporting (the sharing verdict now belongs to the seed clone). `storage.zfs.mirror` → `seed`
  (§gate decision 6).

### Snapshot name reorder (scope #6) — `quince-<ULID>-<date>` → `quince-<YYYY-MM-DDTHH-MM>-<ULID>`

**Amendment B ((co)) — keep the ULID; never drop it.** The ULID *is* the `versionID` (the marker /
journal / `Version.id` / `browse_root` key) — embedding it is what maps a `zfs list` line back to
its version and logs, and two same-minute backups (a failed→retry, or rapid gate testing) would
**collide on a date-only name and fail `zfs snapshot`**. ULIDs are lexically time-sortable, so
`quince-<date>-<ULID>` already sorts chronologically *and* stays collision-free. For time-of-day
readability (the whole reason for the reorder), the date is **widened to `YYYY-MM-DDTHH-MM`** (the
`T` separator + dash-minutes keep it snapshot-name-safe; no `:`) with the ULID kept as the tail.

One line in `snapNameFor` (`zfscli.go:152`) + `snapDateLayout` (`layout.go:16`, `2006-01-02` →
`2006-01-02T15-04`) + the comments (`zfscli.go:150`, `snapShortPattern` `:17`), the contracts §2
example (`:205-206`), and tests. **No migration:** identity is carried by the `quince-version.json`
marker, `zfscli.go` uses the whole name opaquely (`.zfs/snapshot/<name>/` path element; `snapName`
splits on `@`), and the `quince-` prefix is preserved so the hook glob `@quince-*` and
`ListSnapshots`' `HasPrefix("quince-")` are unaffected. Old-format `quince-<ULID>-<date>` snapshots
still adopt (marker-driven, prefix still matches).

### Offsite filter (D5a) — drop the obsolete `work/` rule

New anchored block (`offsite.go`'s `AnchoredFilterRules` + `deploy/storage.md`): `- /<subdir>/*/
working/**` and `- /<subdir>/*/versions/**`. The old `- /<subdir>/*/work/**` rule goes (no more
`work/<job>`). Anchoring stays load-bearing (the unanchored-`**/working/**` hazard is unchanged).

### Multi-storage forward-compat (cl guardrail)

Every path added stays `<backups>/<udid>/…`; nothing assumes a single global `latest`. `last_backup`
stays derived from the newest committed **version** (`subsystem.go:67`), already per-version and
tolerant of becoming per-(device,storage). No unwind cost is incurred.

### Interface facts to verify live (looked up, never remembered — hard rule (al), doubly here)

1. **`RENAME_EXCHANGE` on ZFS — BANKED (do not re-derive).** Measured on the real pool: inode-swap
   + content-swap confirmed; both paths must be ordinary dirs on the same mounted fs (child datasets
   → `EXDEV`), which the per-job design respects. The `exch` CLI exists on PVE 9 via
   `util-linux-extra`.
2. **The Go API — reference the *symbol*, not a constant literal.** `golang.org/x/sys/unix` v0.47.0
   (already a dep, `go.mod`) exposes `unix.Renameat2(olddirfd int, oldpath string, newdirfd int,
   newpath string, flags uint) error` and `unix.RENAME_EXCHANGE` on Linux. Using the named symbols
   (never a hardcoded `0x2`) means the compiler verifies them at build in `quince-dev`; the build is
   the evidence. `Renameat2` is **Linux-only** in x/sys/unix, so the primitive lives in
   `exchange_linux.go` (`//go:build linux`) with an `exchange_other.go` stub (`//go:build !linux`,
   returns an error) so macOS editor tooling still compiles — matching the codebase's existing
   Linux-shaped syscalls (`supervisor.go`'s `Setpgid`).
3. **The exchange runs in the layer it will run in.** The FICLONE-`EPERM` lesson: the gate must run
   an **in-container `exch`/`renameat2` probe on the real deployed dataset** (two non-empty dirs)
   before trusting the in-container exchange. If it fails in the deployed userns (not expected — a
   plain rename is not a privileged ioctl), fall back to a host-side hook `exchange` verb
   (§gate decision 2).

---

## Spec-review gate — decisions for the architect

These are the forks that are architectural (contracts / storage semantics / deployed hook /
user-visible behaviour). My recommendation is stated; each waits for a ruling before build.

1. **Adopt the full per-job-`working/` + exchange model (Design B), not a swap-only minimal fix.**
   *Recommend: yes.* A swap-only fix (just replace the two renames with an exchange) would satisfy
   constraint 3 but **not** the gate's "between backups the dataset holds only `latest/`" nor
   finding #9(a)'s seed-derived `kind`, and would keep the two version models. Design B is what
   (cg) and the roadmap gate mandate; it is also *simpler and cheaper on space* (no commit mirror).
2. **The exchange runs in-container (Go `renameat2`); a host-side hook `exchange` verb is the
   gated fallback.** *Recommend: in-container*, per (cg) ("only FICLONE needs the host; quince does
   the exchange in-container"). Contingent on the in-container `exch` probe (interface fact 3). Fall
   back to a hook verb only if the probe fails.
3. **The hook's `mirror` verb becomes a `seed` verb — the Operator's deployed `quince-zfs-helper`
   must be updated.** *Recommend: yes* (the reflink genuinely moves to seed-time). This is a real
   one-time deployment change; `deploy/storage.md` ships the new verb and a migration note. Flagged
   because it changes an operator-facing security artifact (the forced-command script).
4. **Pre-qn.5b snapshots (content under `…/working`, not `…/latest`) are treated as disposable lab
   data — `Scan` skips them gracefully (no crash, no false adoption), documented as a one-time
   note.** *Recommend: yes.* quince is pre-v0.1; the only existing snapshots are qn.4c hardware-test
   data. Full adoption would require either a read-path `stat` probe (violates the <100 ms version-
   list perf budget) or a `Marker` content-dir field with `omitempty` to preserve old checksums —
   avoidable churn for throwaway data. *Alternative if the architect wants zero-loss adoption:* the
   `omitempty` marker field + a startup-only two-path `Scan`. Either way reconcile keeps such rows as
   `missing`, **never dropped** (roll-forward).
5. **Post-failure UX: 2 actions (Retry, Reset), not 3.** The **contract proposal** below. Reset needs
   a REST endpoint (its backend op `RepairWorkingCopy` is CLI-only today).
6. **Rename `storage.zfs.mirror` → `storage.zfs.seed` (+ the `MirrorReport`/health "mirror" →
   "seed" surfacing).** *Recommend: yes* — "mirror" is now a misnomer (there is no commit mirror);
   the surfaced sharing verdict describes the seed clone. Touches the config schema + golden + docs.
   Pre-freeze, single-user: an alias is over-engineering. Flagged because config keys are a D12
   surface.
7. **Unify `Discard` to keep-the-dirty-`working/` on *all* failure terminals, including cancel.**
   *Recommend: yes.* Today namespace `Discard` deletes `work/<job>` (`namespace.go:170`) → a Retry
   silently restarts; zfs keeps it. Unify to keep. Cancel keeps the dirty working too; **Reset** is
   the explicit discard. `SweepWork` becomes a no-op on all backends (the dirty working is
   first-class resumable state, not an orphan). Confirm cancel-keeps-working is the desired shape.

---

## Post-failure UX — contract proposal (reviewed at this gate, per (cg))

**Problem.** With a dirty `working/` now *kept* on failure, the honest user actions are **Retry**
(resume into it) and **Reset** (discard it; the next backup re-seeds from `latest/`, losing only the
partial). A third, **Retry clean** (Reset + Back-up-now), is composable from the two and risks
reading as "delete my backup" — so I propose **2 actions**.

- **Retry** — already exists (`POST /api/jobs` with `retry_of`, qn.4b one-tap Retry). With per-job
  resume it now *resumes* rather than restarts — **no new contract**, a behaviour improvement that
  falls out of `WorkDir`.
- **Reset** — discard the dirty `working/` so the next backup starts clean from `latest/`. Backend
  op = the landed **`RepairWorkingCopy`**, whose semantics change from *rebuild working from latest*
  to *discard the dirty working* (simpler, and it preserves "between backups only `latest/`"; the
  next `WorkDir` re-seeds). CLI `quince device repair-working-copy` keeps working (consider a
  `reset-working` alias — non-breaking). **New REST surface** (the contract addition):

  ```
  POST /api/devices/{udid}/reset-working   → 202 | 404 | 409
       // 202: dirty working/ discarded (or already clean); audited (reset event, no secret);
       //      no version, no latest/, ever touched. 404: unknown device.
       //      409: a job is currently running for this device — cannot reset mid-backup.
  ```

  Synchronous and fast (an `rm -rf working/`), but modelled `202` for consistency with the other
  device commands. **No `version.*`/`latest` surface changes** (Reset never touches committed state).
  qn.6a adds the UI button (disabled-with-reason while a job runs, per the (bq) pattern).

**Value:** correctness/UX — makes the kept-dirty-working model *controllable* (without Reset the
only way to clear a bad partial is to delete files by hand). **Cost:** S (one handler + the op
already exists). This is the one contract-touching addition; **not built until accepted.**

---

## Stories (each independently checkable)

1. **`exchange(a, b)` primitive** — `unix.Renameat2(unix.AT_FDCWD, a, unix.AT_FDCWD, b,
   unix.RENAME_EXCHANGE)` behind `exchange_linux.go`; `exchange_other.go` stub. Unit test: two dirs
   with distinct sentinel files swap inode + content in one call; the operation on a missing path
   errors (never partially applies).
2. **Per-job `working/` seed + resume** — `WorkDir` seeds `working/<udid>` from `latest/` (via the
   backend's SAFE strategy — `seedStrategy`, hardlink→copy per amendment A), writes the seed
   sentinel, and **resumes** a non-empty `working/<udid>` without
   re-seeding. Test: first call on empty `latest/` → empty tree + `full`; call with a populated
   `latest/` → cloned tree + `incremental`; a second call with a dirty tree present → same tree,
   `kind` recovered from the sentinel, no re-clone.
3. **Atomic commit — zfs** — `Commit` runs verify → marker → exchange → rm working → snapshot;
   `browse_root` resolves to `…/.zfs/snapshot/<snap>/latest`; between backups only `latest/` remains.
4. **Atomic commit — namespace** — `Commit` runs verify → marker → exchange → archive → rm working;
   `latest/` never has an unoccupied instant; the previous version lands in `versions/<prev-ts>/`.
5. **Marker-guarded, non-idempotent-exchange resume** — kill-at-every-phase matrix (`prepared`
   before/after the exchange, `exchanged`, `snapshot_created`/`archived`) rolls **forward** to one
   consistent version; a resume that finds `latest/` already carrying the version id does **not**
   re-exchange (the double-swap bug is impossible).
6. **Unified keep-dirty-working `Discard` + Reset** — `Discard` keeps `working/<udid>` on all
   backends (cancel/timeout/procErr/verify-fail); `SweepWork` no-ops; `RepairWorkingCopy` discards
   the dirty working; a Retry after any failure resumes and completes **without re-transferring the
   already-received tree**.
7. **Honest internal `kind`** — `Verify(tree, kind)` uses the seed-derived `kind`; a first full
   backup runs the encrypted blob-shard check (and fails a shard-less "full"); an incremental
   skips it. The committed marker + `Version.kind` carry the authoritative value.
8. **Drop the symlink dance + free-space** — `idevicebackup2` is invoked with `target = working/`;
   no `.quince-targets` stub, no symlink; `statfs(target)` reports the **storage** filesystem
   (a fixture/lab assertion that the target resolves onto the storage mount, not a scratch fs).
9. **Snapshot name reorder** — `snapNameFor` emits `quince-<YYYY-MM-DDTHH-MM>-<ULID>` (ULID kept,
   amendment B); two same-minute commits do not collide (distinct ULIDs); adoption of an old-format
   `quince-<ULID>-<date>` snapshot still works (marker-driven); the hook glob is unaffected.
10. **Offsite filter** — `AnchoredFilterRules` emits `working/**` + `versions/**` (no `work/**`);
    `PathExcluded` proves a partial `working/<udid>/…` is excluded and content named `working`
    *inside* `latest/` is **not** over-excluded.

---

## Gates (beyond `make gates`; the roadmap gate, asserted as its two independent observers)

- **G-snapshot (the snapshot observer):** a script snapshots the dataset at a tight interval while a
  (fixture-driven, then real) backup runs *and* during the commit; **every** snapshot contains a
  complete `latest/` (verified structurally), **never** none. Includes a snapshot landing between
  the exchange and the `rm working/` (a complete `latest/` + a stray partial `working/` is still a
  valid restore point). *This is the assertion the two-rename model fails.*
- **G-rclone (the filesystem-walk observer):** a continuous `rclone sync` loop to a **local**
  target runs across **many** commits; the mirrored `latest/` is **never deleted and never torn**
  (asserted by diffing the target against a known-good `latest/` after each pass). *This is the
  assertion that today deletes the remote copy.*
- **G-resume:** a backup killed mid-`backing_up` leaves a resumable `working/<udid>`; a retry
  completes and verifies **without re-transferring** the already-present tree (asserted on the fake
  `idevicebackup2` harness's byte counter).
- **G-between:** after any successful backup, the zfs dataset (and each of its snapshots) contains
  **only `latest/`** (plus the transient hidden commit journal, cleared immediately).
- **G-exchange-live (lab, Operator):** the in-container `exch`/`renameat2` probe on the **real
  deployed dataset** succeeds on two non-empty dirs (interface fact 3) — the go/no-go for the
  in-container exchange.
- **G-freespace:** the `idevicebackup2` target resolves onto the storage filesystem (no scratch-fs
  stub) — the structural proof that bug-class 28b97de cannot recur.
- **G-kind:** a shard-less "full" tree fails verification; a first real backup is `kind:"full"` and
  its shard check runs.

Lab legs (Operator hardware day, scheduled at build/gate time, not this session): G-snapshot +
G-rclone + G-exchange-live on the **real rpool** with the updated hook `seed` verb; a `syncoid`
pass mid-write still replicates every committed version intact (regression of the qn.4c/gate-11
result under the new lifecycle).

---

## Fixtures

- Reuse qn.5's fixture trees + the qn.4a lab transcripts (incl. the Wi-Fi torn sessions) — no new
  device transcripts needed; the lifecycle change is storage-side.
- **New:** a `//go:build lab` harness (qn.5's precedent — no deployed commit surface for zfs) that
  drives the snapshot loop (G-snapshot) and the rclone loop (G-rclone) against a real/loopback pool,
  plus a CI-level fake that exercises the exchange + phase matrix on tmpfs (the CI unprivileged-
  userns caveat: reflink can't be *proven* in CI — the hook/host path proves it, per qn.5 (bk)).
- A fixture pair for the **marker-guarded resume** (a `latest/` pre-stamped with the version id, to
  assert the no-double-exchange path).
- **Every lab bug becomes a replay fixture before it's fixed** (hard rule) — the exchange/resume
  matrix is fixture-first.

---

## Rule check (mandatory — written before building; program §"Spec shape")

- **State honesty.** A version is still `succeeded` only after verify + commit; the exchange happens
  *inside* commit, so `latest/` becomes the version atomically and `browse_root` points at it — no
  window where the UI could claim a `latest/` that isn't there. `kind` stops lying (#9(a)). Degraded
  seed modes (copy-fallback) are surfaced (the renamed seed report). **Compliant.**
- **A rung's goal is provable at rung close.** The goal sentence (a snapshot/rclone across a running
  backup is always consistent) is exercised by G-snapshot/G-rclone on fixtures at rung close; the
  real-pool lab legs are an explicit debt owned by *this* rung's hardware gate (named, not silently
  deferred), same shape as qn.5→qn.4a. **Compliant.**
- **Never mutate a committed version.** The exchange only ever swaps `working/<udid>` with `latest/`;
  the archive/snapshot touch `working`/`versions`/snapshots, never a committed version. `latest/` is
  a committed version and it changes *only* by the atomic exchange (the invariant this rung
  *strengthens*: two renames → one). zfs versions (snapshots) and namespace `versions/<ts>/` stay
  immutable. Reconciliation completes commits (roll-forward), never unwinds — the marker guard makes
  the non-idempotent exchange safe. **Compliant, and re-proven** (the hard rule requires any
  storage-touching rung to re-prove its model's invariant — G-snapshot/G-rclone/G-resume do).
- **No silent caps or fallbacks.** Seed **reflink → copy** degradations are surfaced (log + health),
  same as today's ladder. The hardlink tier is gated to copy for the seed (amendment A, 12c
  deferred) — qn.5b does **not** rely on unproven hardlink correctness (the earlier draft's
  "reflink seed" prose did, which is exactly what amendment A caught). **Compliant.**
- **Config tidiness (D12).** The one config touch (`storage.zfs.mirror` → `seed`, iff approved) keeps
  a generated doc-comment, a sane default (`auto`), UI-editability, no restart requirement, no secret.
  No new UI-only or env-only state. **Compliant (pending gate decision 6).**
- **Secrets discipline.** No password path changes; `idevicebackup2` argv/env are untouched (the
  device encrypts with its own keybag; interface fact 5). The Reset endpoint audits with no secret.
  **Compliant.**
- **Subprocesses.** `idevicebackup2` keeps argv-array + own process group + ctx-kill; the change is
  *removing* the symlink stub and passing `working/` as the target argument. The hook `seed` verb is
  an argv array through the same qn.3 hygiene; dataset names stay pattern-validated. **Compliant.**
- **Every lab bug becomes a replay fixture.** The exchange/resume matrix is fixture-first; any
  hardware-day finding lands as a fixture before its fix. **Compliant.**
- **Perf budgets.** `browse_root` stays a computed string (no read-path `stat`) — decision 4's
  "skip old snapshots" choice is *because* the alternative would add a read-path probe. Version-list
  < 100 ms preserved. **Compliant (and load-bearing on decision 4).**
- **Privacy is a commit-time gate.** No hostnames/UDIDs/paths in committed code, messages, or
  fixtures; `make privacy-check` before every commit; the lab harness lives behind `//go:build lab`
  and reads addresses from `local/` only. **Compliant.**
- **Version pins looked up, never remembered.** `unix.Renameat2`/`unix.RENAME_EXCHANGE` are
  referenced as **symbols** (compiler-verified against the pinned `x/sys v0.47.0`), never a literal
  constant; the `exch` CLI/util-linux facts are the banked live measurement, not memory. **Compliant.**
- **Docs are part of the diff.** stack D5/D5a, design §4/§5, contracts §2 (browse_root + snapshot
  example + the `kind`-derivation note) and §1 (the Reset endpoint iff accepted), and
  `deploy/storage.md` (the `seed` verb + offsite filter + delete the stale two-rename note) all
  update **in the build change** — after the ruling, not this session. **Compliant (scheduled).**
- **Near-miss surfaced (not a violation).** The **non-idempotent exchange** is the one place a naive
  resume would corrupt state (double-swap). It is called out explicitly and guarded by the
  version-id marker check + tested by the phase matrix — surfaced here as text, exactly as this
  section is meant to.
- **Multi-storage guardrail (cl).** Paths stay `<backups>/<udid>/…`; `last_backup` stays
  per-version. No single-storage assumption is hard-baked that (cl) would have to unwind. **Compliant.**

---

## Rung-ruled decisions (settled during build)

Architectural forks resolved by the (co) ruling (recorded in the decisions log); rung-local calls
settled here as they land:

- **(co-A) Seed strategy is safe, never hardlink.** `seedStrategy(clonetree.Strategy)` maps
  `Hardlink → Copy` (surfaced), `Reflink`/`Copy` pass through; used by every seed path (namespace
  `WorkDir`, zfs exec/hookless in-container seed). The zfs-hook `seed` verb reflinks host-side
  (safe). *rung-ruled detail:* the zfs in-container seed ladder is **reflink → copy** (no hardlink
  tier), matching the namespace mapping.
- **(co-B) Snapshot name `quince-<YYYY-MM-DDTHH-MM>-<ULID>`.** ULID (== versionID) kept as the tail
  (collision-free on same-minute commits, maps a `zfs list` line to its version); date widened to
  minute precision for readability. `snapDateLayout = "2006-01-02T15-04"`.
- *rung-ruled:* the seed sentinel is `<deviceDir>/.quince-work.json` (outside `working/<udid>` so it
  never rides into `latest/` at the exchange); it records `seeded_from_latest` for the authoritative
  `kind` and survives crash/resume.
- *rung-ruled:* the exchange is idempotency-guarded by the **version-id marker check** on `latest/`
  (a resume that finds `latest/` already carrying this version's id does not re-exchange).

## Rung report (build outcome)

**BUILT + CI-PROVEN (2026-07-24, decisions (cp)).** `make gates` (go + vault + ui) + `make image`
green in `quince-dev`; coverage backup **85.2%** / storage **78.9%** / httpapi **73.2%**. No lint
issues; gofmt clean.

- **Handoff review of qn.5's storage seams (run-anchored at build-start):** `make gates` established
  green on the inherited tip before any change; the three non-atomic windows (zfs `rebuildLatest`,
  the hook `mirror` verb, **and** namespace `finishRotation` — the third found during the review) are
  the rung's subject, not review fixes. No blocker in the inherited code.
- **Landed (all 10 stories):** the `exchange` primitive (its test doubles as the in-CI proof
  RENAME_EXCHANGE works on the container tmpfs); the unified per-job `working/<udid>` seed/resume
  (safe strategy — hardlink→copy, amendment A); atomic exchange commit on both models with the
  marker-guarded kill-matrix (prepared/exchanged/archived|snapshot_created); keep-dirty `Discard` +
  Reset-discard `RepairWorkingCopy`; honest seed-derived `kind` (`Verify(tree, kind)`); the symlink
  dance deleted (free-space regression test rewritten); snapshot `quince-<YYYY-MM-DDTHH-MM>-<ULID>`
  (amendment B); offsite filter minus `work/**`. The Reset REST endpoint + CLI landed; the config
  `mirror`→`seed` rename + `MirrorReport`→`SeedReport` landed; the hook `mirror`→`seed` verb +
  migration note landed in `deploy/storage.md`.
- **Gate proof (CI):** `TestCommitLatestNeverGoesMissing` — a concurrent reader confirms `latest/`'s
  marker is never missing/torn across a running commit on both models (the exact failure the
  two-rename swap caused; this test would fail against the old code). Plus resume-without-re-transfer,
  the kind-gated shard check, the reconcile matrix, and the offsite-filter contract.
- **Owed to a hardware day (named, not silently deferred):** the real-rpool lab legs — **G-snapshot**
  (probe-snapshot loop during a running backup + at commit), **G-rclone** (continuous sync never
  deletes/tears the remote), **G-exchange-live** (the in-container `exch` probe on the deployed
  dataset — the go/no-go for the in-container exchange), plus a syncoid mid-write pass. Preserved in
  §Gates + the `//go:build lab` harness. Gate 12c stays deferred (hardlink disabled-to-copy, now
  including the seed).
- **Not committed** — awaiting the Operator's go (commit-when-asked; no push/tag).
