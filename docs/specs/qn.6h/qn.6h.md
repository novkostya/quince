# qn.6h — zfs writes in place: no seed, no working copy, no exchange; commit is verify → `zfs snapshot`

**Goal.** A zfs backup starts transferring as soon as the user has entered the passcode, instead of
after a clone of the previous backup; and the script the operator keeps on their ZFS host stops
containing quince's lifecycle, so quince can change how it writes without asking anyone to hand-edit
a file on a machine it does not manage.

Rung issue: [quince#591](https://github.com/novkostya/quince/issues/591), Operator ruling
**2026-08-04**, relayed on that issue by the architect seat. The ruling and its three conditions are
cited rather than re-litigated; canon carries it in `docs/quince.design.md` §5, end of section.

**Prerequisite, and it is a hard one: the canon PR must land before any code here.** Until it does,
`CLAUDE.md`'s opening paragraph and its *Never mutate a committed version* hard rule state the
invariant this rung changes with nothing saying it is decided. That PR is open and is the Operator's
to approve as code owner.

**Everything measured below was measured in this checkout at `ea71315`, 2026-08-08**, or read live
from a pinned upstream source or a vendor man page — never recalled. Where a claim is *not* measured
it says so in those words.

---

## Why `qn.6h`, and what it does not fix

**The letter.** `docs/specs/qn.6g/qn.6g.md` already reserved it: *"quince#591 takes `qn.6h`."*
`qn.6e` is quince#502's placeholder and `qn.6f` (quince#462) is closed.

**The order is discharged.** qn.6g's spec ruled itself first, because it discharged a live violation
where this rung fixes nothing currently wrong. quince#577 closed `COMPLETED` **2026-08-07T06:47:41Z**,
so that condition is spent.

**This rung makes an already-correct path faster and cheaper to operate.** It fixes no defect. If it
is not built, quince keeps working exactly as it does today.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/storage/zfs.go` | `PrepareWork`/`SeedWork`/`WorkDir` stop delegating to the shared seed machine; `seedWorking`, `seedInContainer`, `seedClosure` and the exchange in `finishCommit` go; `RepairWorkingCopy` becomes a rollback |
| `core/internal/storage/zfscli.go` | the `seed` verb becomes `rollback` |
| `core/internal/storage/journal.go` | doc only — `PhaseExchanged` stops occurring on zfs; the enum keeps its shape. **Specifically `:20-26`**: the *"both models share the atomic exchange as their pivot"* block, and `PhasePrepared`'s *"marker written into `working/<udid>`"* |
| `core/internal/storage/layout.go` | `browseRoot` stops being able to return `latestDir()` on zfs (D7) |
| `deploy/storage.md` | the reference helper loses `seed)`, gains `rollback)`; the offsite note; the one hand-edit |
| `docs/quince.design.md`, `CLAUDE.md` | the ruling — **a separate, prerequisite PR** |
| `docs/contracts.md` | `POST /api/devices/{udid}/reset-working` gains a failure the zfs backend can return |

**Out of scope, each with a why.**

- **The namespace backends.** reflink / hardlink / copy keep `working/` + seed + exchange + archive,
  keep `qn.6b`'s `--gate` patch, and keep Finding B's partial-clone discard. The patch is **not**
  deleted; zfs merely stops needing it. Their existing gates must still pass untouched, which is
  story 9.
- **The version model.** Markers, verify, retention, adopt, prune, browse and
  reconcile-from-snapshots are unchanged, because the snapshot still contains `latest/`, byte for
  byte the tree it contains today. This rung changes only how `latest/` gets filled.
- **Data migration, and a migration PROCEDURE for the helper.** Operator ruling relayed 2026-08-08:
  *"I am the only quince user for now and there was no v0.1 release tag yet — so migration is out of
  the table."* Existing `@quince-*` snapshots keep working **without a compatibility path**, because
  the layout inside a snapshot does not move. **There is no fleet**, so quince#591's build item 5 —
  *"the reference helper, and a migration note"* — is discharged by a **one-line changed-verbs note**
  (`seed)` out, `rollback)` in) rather than an upgrade procedure. `deploy/storage.md`'s `qn.5b`
  `mirror)` → `seed)` block is the shape **not** to copy: that was written for operators who did not
  exist.
- **A btrfs-native twin.** The sibling named in the **(cl) storage epic's point 8**
  (`quince-devlog/roadmap.md:696` onward — **not** M5, which has no numbered points and no storage
  items). No snapshot-capable non-zfs tester exists.
- **Making offsite read a snapshot mount.** This rung *states* that `latest/` is torn during a zfs
  backup and that quince must be excluded from a general whole-host rclone job; it does not build
  the snapshot-sourced offsite path. **That is quince#735**, filed 2026-08-07 because nothing
  tracked it: the ruling accepted the cost and left it ownerless.

---

## Interface facts — measured at `ea71315`, or read live

1. **`latestDir()` has exactly three callers in `zfs.go`** — `:107` (`Provision`), `:129`
   (`seedClosure`), `:221` (`finishCommit`). The second and third go.

2. **The fourth reader is on the zfs path and is shared.** `core/internal/storage/workdir.go:69`
   — `seeded := !isEmptyDir(latestDir(backups, udid))` — sits in `prepareWorkDirPhase1`, which
   **both** backends call (`zfs.go:120`, `namespace.go:57`), as does `finishSeed` (`zfs.go:124`,
   `namespace.go:61`). So `workState`, the `seed_in_progress` sentinel and Finding B's discard live
   in `workdir.go`, not in `zfs.go`. This is the architect's scope correction on quince#591 and it is
   the reason sub-question 1 is the load-bearing one.

3. **`idevicebackup2` composes its target as `<backup_directory>/<udid>` and cannot be told not
   to.** Read live from the pinned source — `LIBIMOBILEDEVICE_REF=1.4.0` (`versions.env:42`),
   `tools/idevicebackup2.c`:

   ```c
   1998  char* devbackupdir = string_build_path(backup_directory, source_udid, NULL);
   1999  __mkdir(devbackupdir, 0755);          /* return value DISCARDED */
   1779  info_path = string_build_path(backup_directory, source_udid, "Info.plist", NULL);
   1731  if (stat(backup_directory, &st) != 0) { /* ERROR: Backup directory does not exist */ }
   ```

   The `mkdir`'s return is not checked, so an entry already at that path is tolerated and every
   later path is composed the same way and opened through it. `backup_directory` itself must exist.

4. **`statvfs(backup_directory)` is what answers the device's free-space question**
   (`idevicebackup2.c:2321`, `DLMessageGetFreeDiskSpace`). qn.4c found what a wrong answer costs: a
   target on the cache filesystem made the device refuse any backup bigger than it. **The tool's
   target must be inside the backup dataset**, which rules out putting the shim anywhere else.

5. **`isDirty` stats the directory, deliberately** — `reset.go:101-104`, *"IT INCLUDES THE
   KILLED-SEED CASE deliberately … That is why this stats the directory rather than reading the
   sentinel."* Whatever replaces the working copy must therefore still be a directory that exists
   during a job and does not exist between jobs, or `RepairWorking`'s whole resolver changes.

6. **`seedKind` is the sole consumer of `workState.SeededFromLatest`** (`subsystem.go:334-354`) and
   it is what makes `Version.kind` authoritative — the lab proved `Status.plist.IsFullBackup` lies.

7. **`zfscli.Snapshot` is already idempotent** on `already exists` (`zfscli.go:69-79`), which is what
   makes a roll-forward resume of the new commit sequence safe with no new code.

8. **The helper discards every flag.** `deploy/storage.md`: `set -- $SSH_ORIGINAL_COMMAND`, then
   `target` = the **last** argument, then `exec zfs <verb> "$target"`.

9. **`zfs rollback` without `-r` refuses anything but the most recent snapshot.** OpenZFS
   `zfs-rollback(8)`, fetched 2026-08-08: *"By default, the command refuses to roll back to a
   snapshot other than the most recent one."* `-r`/`-R` destroy newer snapshots; **`-f` forces an
   unmount of *clones*, under `-R` only** — there is no flag that forces the unmount of the dataset
   being rolled back.

10. **rclone already excludes `working/`** by an anchored rule — `offsite.go`,
    `- /<subdir>/*/working/**`. Nothing there needs to change for the shim.

11. **Reset is already refused while a backup runs** — `engine.go:322-324`, `409` *"a backup is
    running for this device — cancel it before resetting"*. So a rollback can never race quince's own
    writer.

12. **Browse on zfs reads the snapshot, the newest version included — but only when the registry row
    carries one.** `browseRoot`'s zfs arm is `backend == BackendZFS && zfsSnapshot != nil` and
    **ignores `isLatest`** (`layout.go:82-91`), so the newest version genuinely is read from
    `.zfs/snapshot/<snap>/latest` today. **When `zfsSnapshot` is nil it falls through to
    `if isLatest { return latestDir(...) }`** — a live-tree read. Harmless today, because on zfs
    `latest/` is always a complete committed tree; **in place it is the mutable head.** The nil case
    is **representable and NOT shown to occur**: `ZFSSnapshot` is a `*string` off the registry row,
    and `zfs.go:287` handles a nil elsewhere. Found by the architect reviewing the canon PR
    (quince#733), not by this spec's own reading. See D7.

13. **`PhasePrepared`'s definition is written against the exchange** — `journal.go:26`, *"marker
    written into `working/<udid>`"*, under a block comment reading *"both models share the atomic
    exchange as their pivot"* and listing `zfs: prepared → exchanged → snapshot_created`. The phase
    name survives this rung; those sentences do not.

---

## Design

### D1 — The write path: a symlink shim at `working/<udid>`

`idevicebackup2` appends the UDID to whatever target it is given (fact 3), and the head is
`latest/`, so something must bridge the two. **The shim is a symlink, created per job:**

```
/backups/<udid>/                      the device's child dataset
├── latest/                           the live head — the tool writes HERE
└── working/<udid> -> ../../latest    a symlink, the ONLY thing in working/, created at job
                                      start and removed at commit
```

`PrepareWork(udid, job)` on zfs becomes, in full: ensure `latest/` exists, ensure `working/`
exists, create the symlink if it is not already the shim, write
`workState{SeededFromLatest: !isEmptyDir(latest), SeedInProgress: false}`, return
`(workingParent, seedPending=false)`. `SeedWork` is a no-op returning nil. `WorkDir` is the two
composed, as the interface says it must be.

**Why a shim rather than a different target — the whole system keeps working unchanged.** Each of
these is a thing that would otherwise need its own edit:

| what | why it still works |
| --- | --- |
| `TreePath` → verify | resolves through the shim, so verify verifies the head |
| the marker write in `Commit` | lands in `latest/`, which is where it must end up anyway |
| `isEmptyDir(tree)` commit guard | follows the symlink |
| `isDirty` (fact 5) | `working/` exists during a job and not between jobs — the meaning is preserved, not approximated |
| `seedKind` (fact 6) | reads the same sentinel with the same field |
| rclone exclusion (fact 10) | the anchored `working/**` rule already covers it |
| `statvfs` free space (fact 4) | `working/` is inside the dataset, so the device gets the dataset's number |

**Alternatives, and why each loses.**

- **Pass `<deviceDir>` so the tree is `<deviceDir>/<udid>`.** Renames the head. `latest/` is *a real
  directory on every backend* in canon, it is the offsite contract's anchor, and every existing
  snapshot browses through `.zfs/snapshot/<snap>/latest`.
- **Pass `<deviceDir>/latest` so the tree is `latest/<udid>`.** Moves the content one level deeper
  inside every future snapshot, so `browseRoot` and `committedFromSnapshot` grow a second shape and
  every existing snapshot becomes the other one. That is exactly the *"the version model does not
  change"* claim the ruling was taken on.
- **A sixth libimobiledevice patch.** A maintained patch forever, against a ruling whose prize is
  *shrinking* the surface somebody has to hand-maintain.
- **A bind mount.** Needs privilege; this deployment deliberately is not privileged, and the
  rbind/rslave war was fought to mount each device dataset exactly once.

**One behaviour differs from today and it is named rather than discovered.** `DLMessageRemoveItem`
does `stat(path)` then `rmdir_recursive(path)` for a device-sent path under the target
(`idevicebackup2.c:2359-2364`, `:2413-2417`). If the device ever sent the bare UDID, `stat` would
follow the shim and the contents of `latest/` would be removed — where today the same message would
empty a private clone. **No committed version is at risk either way**: the previous version is a
snapshot, and the emptied head is a dirty head, which is the resumable state. `rmdir_recursive` ends
with `remove_directory(path)`, which is a `rmdir` and fails `ENOTDIR` on the symlink, so the shim
itself survives.

**A pre-change dirty `working/<udid>` is a real directory, and it is discarded, loudly.** After this
rung a zfs `working/<udid>` that is not the shim is a clone from the old model. It cannot be resumed
— the tool now writes into `latest/` — so it is removed, with a log line naming its size. *No silent
caps or fallbacks* is why it is logged rather than quietly deleted; the sole user and the absent
`v0.1` tag are why nothing more is built for it.

### D2 — Commit: verify → remove the scaffolding → `zfs snapshot`

```
prepared → snapshot_created → registry_committed
```

`Commit` writes the marker into `TreePath` (the head, through the shim), journals `PhasePrepared`,
and `finishCommit` does two things where it used to do three:

1. **Remove `working/` and the work sentinel.** This happens **before** the snapshot, not after, and
   the ordering is the whole of it: canon says *between backups the dataset holds only `latest/`*,
   and a snapshot taken with the scaffolding still present would carry it forever. Today's code
   already removes them before snapshotting (`zfs.go:242-243` then `:245`); the ordering is
   inherited, not invented.

   **The CALL is inherited; the OBJECT is not, and that must be a written property rather than a
   lucky one.** Today `os.RemoveAll(workingParent)` deletes a real directory of superseded content.
   After this rung it deletes a directory containing a **symlink to the committed tree**. Go's
   `RemoveAll` is `lstat`-based, so it unlinks the symlink and does not traverse it — the code is
   correct. **The failure if it were not is deleting every backup at every commit**, so G1 asserts
   `latest/` still holds the committed tree after `finishCommit`, and the property is stated here so
   nobody "simplifies" the traversal later.
2. **`Snapshot`** → `PhaseSnapshotCreated`.

`PhaseExchanged` stops occurring. The enum keeps its shape — per-backend phase sets are already the
design.

**Roll-forward is unchanged and needs no new code.** `RemoveAll` is idempotent and `Snapshot` already
tolerates `already exists` (fact 7). The marker guard `latestHasVersion` was the *exchange's*
idempotency guard — re-running an exchange reverses it — and in place the marker write is idempotent
by content, so the guard becomes unnecessary rather than merely redundant. It is removed with a
comment saying why, so the next reader does not restore it as a missing safety.

**Once verify has passed, nothing unwinds.** A failure after verify completes the remaining phases.
A rollback here would destroy a transferred backup, which is the rule this rung is most at risk of
breaking and does not.

### D3 — Sub-question 1: `reset.go`, `worksentinel.go`, and `WorkingReset` with no working directory

**Answered: the surface does not change at all, and the sentinel survives with one field retired.**
The issue called this *"the item most likely to make the change bigger than the measurement
suggests"*. It is not, and the shim is why — `working/` still exists during a job, so facts 5 and 6
hold verbatim, and the whole `RepairWorking` resolver (`404` unknown, `409` unusable-or-ambiguous,
`202` clean, the multi-storage candidate list, the audit line, the CLI) is untouched.

**`workState.SeedInProgress` is always `false` on zfs, and must not be repurposed.** There is no
seed, so there is no partial clone and Finding B's discard is moot. The tempting move — reusing the
field to mean *a transfer is in flight* — would make `prepareWorkDirPhase1`'s guard **discard a
resumable dirty head**, which is precisely the *a failed job keeps its dirty working so a retry
resumes* rule. It is called out here because the field name invites it.

**`workState.SeededFromLatest` keeps its meaning; only its derivation moves** — `!isEmptyDir(latest)`
at job start, which is the same question asked of the same directory. `seedKind` and `Version.kind`
are unchanged.

**`RepairWorkingCopy(udid)` on zfs becomes a rollback, and this is `rollback`'s only caller.** Reset
is an explicit user abandon, which is exactly the licence the ruling gives the verb:

1. Roll the dataset back to the newest `@quince-*` snapshot.
2. Remove `working/` and the sentinel. The rollback usually does this for free — they are in the
   dataset and were not in the snapshot — but the call is kept for the case below.

**The outcome is identical to today's, which is the reassurance worth stating.** Today reset drops
the clone and leaves `latest/` alone, so the head returns to the newest committed version. In place,
rollback returns the head to the newest committed version. Reset does not become more destructive.

**A device with no `@quince-*` snapshot has nothing to roll back to.** That is a first backup that
never committed. Reset then empties `latest/` in-container and removes the scaffolding — safe *only*
because zero snapshots means zero committed versions, and the count is read from `cli.ListSnapshots`
and asserted, never assumed.

**A failed rollback is a real outcome.** `repairOn` returns non-2xx with the reason; the head stays
dirty, the sentinel stays, and nothing claims the working copy was discarded. Silently answering
`202` after a failed rollback is the state-honesty violation this rung is most likely to ship, which
is why it has its own story and its own gate.

### D4 — Sub-question 2: rollback under load

**The question, stated plainly.** Does `zfs rollback` succeed against a device dataset that is
**mounted into a running container, with quince live and possibly holding open handles under it**?

**MEASURED 2026-08-08 ON REAL ZFS — the answer is A, and a THIRD outcome nobody had was found.**
The Operator stood up a real dataset on the PVE host and bind-mounted it into an unprivileged LXC,
driven through the real forced-command helper over real ssh. H2 is **discharged**; quince#730's
*"no real ZFS host anywhere"* no longer holds for this question.

| probe | result |
| --- | --- |
| rollback, dataset mounted into a running unprivileged LXC, **read** handles held (an fd on a file, a child process holding another, a process with cwd inside `latest/`) | **exit 0**, no output; head reverted; post-snapshot file removed |
| rollback with an **active writer** appending, plus a persistent held **write** fd | **exit 0**; head reverted; the held-open file was removed |
| rollback when **any newer snapshot exists** | **exit 1** — see answer C |
| `rollback -r <snap>` through the helper | `-r` **discarded by the parse**; the newer snapshot survived |
| rollback targeting a **non-`@quince-*`** snapshot | **refused** by the helper |
| `destroy <dataset>` (no `@`) | **refused** by the helper |

**So both answers are still designed for, and a third is added.** Designing for the unobserved cases
remains right: the measurement is one host, one pool, one kernel.

**What is settled regardless of the outcome:**

- **quince's own writer can never race it** (fact 11): reset is already `409`ed while a backup runs.
- **quince's readers are largely absent**: browse on zfs reads `.zfs/snapshot/…`, never `latest/`
  (fact 12), so the obvious open-handle source is gone by construction.
- **There is no force flag** (fact 9): `-f` forces an unmount of *clones*, under `-R` only. Neither
  answer can be engineered around with a flag.

#### Answer A — the rollback SUCCEEDS. **This is the measured case.**

Reset behaves as D3 describes: the head returns to the newest committed version, `working/` and the
sentinel are gone (the rollback removes them, since they were not in the snapshot), `202`, one audit
line. **Nothing further is built.**

**Two things the measurement adds, and the second is a caution rather than a comfort.**

**A mounted dataset with open handles is not an obstacle at all.** Held read fds, a child process's
fd, a process with its cwd inside the tree, an active writer, and a persistent held **write** fd —
none prevented it. A file opened once and held for writing was **removed** by the rollback.

**But a rollback neither stops nor signals the writers, and the filesystem gives no error to
either side.** Measured: a loop that reopened its path every 50 ms had its file removed and
recreated it immediately, silently. So **answer A must not be read as "rollback is safe under
concurrency."** It is safe here because `engine.go:322-324` refuses reset with `409` while a backup
runs (fact 11) — the safety is quince's guard, not ZFS's. If that guard ever regressed, the symptom
would be a rolled-back tree immediately re-dirtied with **nothing logged anywhere**. That makes the
`409` load-bearing for correctness and not merely for tidiness, which is worth knowing before
somebody relaxes it.

#### Answer B — the rollback FAILS

Two shapes, one outcome. **B1**: it fails fast — `dataset is busy` or similar on stderr. **B2**: it
blocks and is cut off by `zfsOpTimeout` (60 s), so the failure is a timeout. Both produce:

- **non-2xx from `RepairWorking`, carrying the reason verbatim** — the helper's or `zfs`'s own words,
  not a paraphrase, because the operator's next action depends on which it was;
- **the head left dirty and the sentinel left in place.** Nothing is half-undone, and the job's
  resume state survives — which matters, because a failed reset must not cost what a failed *job* is
  guaranteed to keep;
- **no audit line**, since nothing was discarded;
- **no automatic retry.** A retry is the identical call against the identical mount and would only
  spend the timeout again. If the cause is transient the operator repeats the action; quince does not
  decide that on their behalf.

**The remedy is named in the message and it is an operator action on the host**, because there is no
in-product one: stop or restart the container so the dataset is not mounted, then reset. Saying so is
the whole of quince's job here — *no silent caps or fallbacks*.

**Rejected under answer B: emptying `latest/` in-container as a fallback.** It is available, it is
non-destructive to versions (the committed content is in the snapshot), and it would make reset
"work" — and it is wrong twice. It silently converts an abandon into a state where the next backup is
a **full multi-hour transfer**, and it does so at the moment the user asked for the cheap operation.
A refusal that names the remedy is better than a success that costs tens of gigabytes without saying
so.

**Answer B was NOT observed.** No busy-mount refusal and no timeout occurred in any probe. It is kept
because one host is not a proof, and because `zfsOpTimeout`'s 60 s bound still has to mean something
if a rollback ever does block. It is now the *unobserved* branch rather than the expected one.

#### Answer C — a NEWER SNAPSHOT EXISTS. **Measured, not anticipated, and it is the likely one in the field.**

```
cannot rollback to 'rpool/…/testdev@quince-2026-08-08-LABTEST': more recent snapshots or
bookmarks exist
use '-r' to force deletion of the following snapshots and bookmarks:
rpool/…/testdev@quince-2026-08-08-NEWER
```

exit `1`. **`zfs rollback` refuses whenever ANY newer snapshot exists — not only a quince one.**

**Why this is the common case rather than an edge.** The measurement host runs an automatic
snapshotter (`zfs-auto-snap_frequent-*`, firing every few minutes). Automatic snapshotting is
ordinary ZFS hygiene and quince neither controls nor should control it. So a `@quince-*` snapshot
**stops being the most recent within minutes of being taken**, and from then on reset is refused. The
spec previously expected *busy mount*; the real blocker is a third party's snapshot.

**quince cannot force past it, deliberately.** `-r` is the only escape and the helper discards every
flag — **measured**: `rollback -r <snap>` still failed and the newer snapshot survived. That is the
guard working exactly as designed, since `-r` is what destroys committed versions. **The verb's
safety and its unavailability are the same property.** And quince must not destroy foreign snapshots
to clear the path: the helper refuses non-`@quince-*` targets (measured), which is correct — they are
not quince's to delete.

**So the remedy under C is DIFFERENT from B's, and getting that wrong is a state-honesty failure.**
Stopping the container does nothing here. The message must name the actual situation — *a snapshot
newer than the one quince would restore exists, so the head cannot be rolled back* — and the actual
options: destroy the intervening snapshots on the host, or **do nothing, because the dirty head is
resumable**. That last clause is the important one and it is why C is not a crisis: **the normal path
after a failed job is to retry, which resumes from the dirty head.** Reset is the rare abandon
operation. A blocked reset costs the user the ability to abandon, not the ability to back up.

**A detection consequence, which is a real design finding.** `zfscli.List` filters to
`HasPrefix(short, "quince-")` (`zfscli.go:103`), so **quince currently cannot see foreign snapshots
and cannot predict this failure.** To answer *"will reset work?"* before attempting it — or to
explain the refusal precisely — it needs an unfiltered view of the dataset's snapshots. The helper's
`list` already returns everything; the filtering is quince-side. **Not built in this rung**; recorded
as open question 7, because deciding it silently is how a refusal ends up with a message that names
the wrong remedy.

### D5 — Sub-question 3: `Info.plist`

**Answered: the capture-and-restore step is not deleted — it stops being reached, on zfs only.**
`superviseGatedSeed` (`engine.go:770-819`) captures the fresh `Info.plist` the tool wrote and
restores it over the clone, because the clone overwrites it with `latest/`'s. With no clone there is
nothing to overwrite it. `PrepareWork` returns `seedPending=false`, so `engine.go:458` takes the
plain `supervise` branch with `gatePath=""`, and `--gate` is not passed. `superviseGatedSeed`,
`awaitInfoPlist`, `readStableInfo` and the patch all stay, for the seeding backends.

**One consequence, stated because it looks alarming and is not.** The tool does
`remove_file(info_path)` then writes a fresh one **before** sending the `Backup` request (spike C12,
`idevicebackup2.c:2242-2243`). On zfs that now rewrites the live head's `Info.plist` before a byte
transfers. If the job then fails, `latest/` carries a fresh `Info.plist` over the previous version's
content — which is the **dirty head**, the resumable state the retain rule protects. The previous
version is intact in its snapshot, and `quince-version.json` in `latest/` still names the old version
until commit, which is what keeps adopt and reconciliation honest across the window.

### D6 — The helper: `seed` out, `rollback` in

```sh
rollback) case "$target" in "$PARENT"/*@quince-*) exec zfs rollback "$target" ;; esac ;;
```

One line, the shape of its neighbours. `seed)` — mountpoint resolution, a `-d` check, an `rm -rf`, a
`cp -a --reflink=always`, a `zpool sync` and a `du`-based verdict — is deleted. Afterwards every verb
is O(1) and lifecycle-independent, which is the prize.

**The guard is free** (facts 8 and 9): the helper discards flags, so `rollback -r <snap>` reaches
`zfs` as a plain `zfs rollback <snap>`, and a plain rollback cannot pass the most recent snapshot.
The helper therefore **structurally cannot destroy a committed version**. If the newest snapshot is
itself bad, the recovery is `destroy` then `rollback`: two explicit acts, each bounded.

**`argv()` needs no exec/hook branch**, unlike `capacity` (`zfscli.go:193-196`), because `rollback`
takes a target and the generic `argv(op, args...)` composes correctly for both modes.

**The hand-edit is NOT detectable, and that is a stated cost.** `hookcheck` fires `capacity` and
`list` — both harmless. `rollback` cannot be probed, because probing it means performing it. So an
un-migrated helper surfaces at the **first reset**, as the helper's own
`quince-zfs-helper: refused: rollback …` on stderr. This rung's job is to make that message legible
(story 11), not to pretend it can be caught earlier.

### D7 — Browse must never resolve to the live head on zfs

**This is the one design item that came from review rather than from the ruling** (architect, on
quince#733), and it is the sharpest hazard in the rung: `browseRoot`'s nil-snapshot fallback returns
`latestDir()`, which in place is **the tree being written** (fact 12). A browse session would walk a
half-transferred backup and present it as a version.

**The fix is a refusal, not a repair, and the reasoning is what makes it the right shape.** The
tempting move is to argue the nil case cannot arise — the commit path always sets `ZFSSnapshot`, so a
zfs row without one is a corruption. That argument may well be true and it is **not what this rung
rests on**: the finding was raised as *representable, not shown to occur*, and building on
"it cannot happen" is building on an assumption nobody wrote down.

So: **on the zfs backend `browseRoot` never returns `latestDir()`, whatever the row holds.** A zfs
row with no snapshot yields no browse root and the version is surfaced as unbrowsable with a reason —
the same shape as a `missing` artifact, which is the existing vocabulary for *the row exists and the
content cannot be served*. Gate G14 asserts it against a deliberately-nil row, so the claim is proven
against the case rather than argued away from it.

**Why not simply keep `isLatest` working on zfs.** Because in place there is no moment at which
`latest/` is known-complete from a row's point of view: between the marker write and the snapshot it
is a committed-looking tree that is not yet a version, and after a failure it is a dirty head that
still carries the *previous* version's marker. Any read that resolves *the newest version* to the
head is reading a tree whose completeness is a timing question. The snapshot is the only artifact
that answers it, which is why the arm that already ignores `isLatest` is the correct one and the
fallback is the defect.

**`PhasePrepared` and its block comment move with the sequence** (fact 13). The phase keeps its name
and its position; on zfs it means *marker written into `latest/`*, and `journal.go`'s comment must say
so rather than leaving `working/<udid>` as the only definition. Named here because the rung's Boundary
lists `journal.go` as doc-only, and a doc-only file is the one most easily skipped.

---

## Stories

1. **A zfs backup writes into `latest/` and no clone is ever made.** Both cases: a first backup
   (empty `latest/`) and an incremental (populated `latest/`). No `cp`, no reflink, no `seed` verb.
2. **A committed zfs version is a `@quince-*` snapshot** whose `latest/` is what the tool wrote, and
   browsing that version reads `.zfs/snapshot/<snap>/latest` exactly as before.
3. **Between backups the dataset holds only `latest/`.** No snapshot carries `working/` or the work
   sentinel, because both are removed before the snapshot is taken.
4. **A failed zfs job keeps its dirty head and resumes.** The retry re-transfers nothing, and **no
   rollback fires** — the failure path never calls the verb.
5. **Reset rolls back.** `POST /api/devices/{udid}/reset-working` on zfs answers `202`, and the head
   afterwards equals the newest committed version.
6. **Reset with no snapshot empties the head**, having asserted from `ListSnapshots` that no
   committed version exists — not having assumed it.
7. **A failed rollback is surfaced, never swallowed.** Non-2xx, `zfs`'s own reason quoted verbatim
   rather than paraphrased, the head still dirty, the sentinel still present, no audit line, **no
   automatic retry**. Three causes, and **the message must not offer B's remedy for C's failure**:
   answer B (busy mount / timeout) → stop the container; **answer C (*more recent snapshots or
   bookmarks exist*) → destroy the intervening snapshots on the host, or do nothing, because the
   dirty head is still resumable.** Answer C is the measured one.
8. **`Info.plist` in the committed version is the one the tool wrote for that job**, with no
   capture/restore step and with `--gate` not passed.
9. **The namespace backends are untouched.** Seed, exchange, archive, `--gate` and Finding B's
   discard all still run, and every existing gate for them still passes.
10. **The helper's parse bounds the verb.** `rollback -r <snap>` reaches `zfs` as a plain rollback;
    a target that is not `<PARENT>/*@quince-*` is refused.
11. **An un-migrated helper produces a legible refusal at reset**, naming the verb and the remedy —
    never a silent success.
12. **A pre-change real `working/<udid>` is discarded with a log line naming its size**, and the shim
    replaces it.
13. **Browse never resolves to the live head on zfs.** A zfs version row with no snapshot is
    surfaced as unbrowsable with a reason, never as a `browse_root` pointing at `latest/`.

---

## Gates

| id | what it proves | where |
| --- | --- | --- |
| **G1** | A zfs commit produces a snapshot whose `latest/` matches the tree the fake tool wrote, with no clone step and no `PhaseExchanged` in the journal. Both first-backup and incremental. | CI (Go) |
| **G2** | The snapshot contains **only** `latest/` — asserted on the filesystem after commit, not on the API. | CI (Go) |
| **G3** | A killed job leaves the head dirty and the shim in place; the next `PrepareWork` resumes it, and nothing calls rollback. | CI (Go) |
| **G4** | `RepairWorkingCopy` on zfs issues exactly one `zfs rollback <newest @quince-*>` — asserted on the recorded argv, so `-r` can never creep in. | CI (Go, fake `zfsCLI`) |
| **G5** | **D4 answers B and C.** A rollback that fails fast, one that exceeds `zfsOpTimeout`, and one refused with **the measured answer-C text** (`more recent snapshots or bookmarks exist`) ⇒ non-2xx from `RepairWorking`, `zfs`'s reason propagated **verbatim**, the sentinel and `working/` still present, **no audit line**, **exactly one** rollback attempt recorded. This is the state-honesty gate. | CI (Go) |
| **G5c** | **The answer-C remedy is C's, not B's.** The message for *more recent snapshots exist* must not tell the operator to stop the container, and must say the dirty head remains resumable. Added because the two failures were one branch in the spec until H2 separated them. | CI (Go) |
| **G6** | Reset on a device with zero `@quince-*` snapshots empties the head and answers `202`; reset on a device **with** snapshots never takes that branch. | CI (Go) |
| **G7** | The committed `Info.plist` is the job's fresh one, and `supervise` was called with an empty `gatePath` on zfs. | CI (Go) |
| **G8** | The shim is a symlink to `../../latest`, is the only entry in `working/`, and `TreePath` resolves through it to the head. | CI (Go) |
| **G9** | A pre-change real directory at `working/<udid>` is removed and logged with its size. | CI (Go) |
| **G10** | Every existing namespace-backend storage test passes unchanged — the diff must not touch their expectations. | CI (Go) |
| **G11** | The reference helper in `deploy/storage.md` accepts `rollback <parent>/<udid>@quince-*`, refuses every other target, and drops `-r` — run against the real script text with a stub `zfs` on `PATH`. | CI (shell) |
| **G12** | `make gates` · `make image` · `make gates-ui-e2e` | CI |
| **G13** | `make privacy-check REF=origin/main...HEAD TEXT=<file under $HOME/scratch/<runner>/>` | host |
| **G14** | `browseRoot` on the zfs backend **never** returns `latestDir()` — driven with a deliberately-nil `ZFSSnapshot`, so the guard is proven against the case rather than argued away from it. The newest version still resolves to `.zfs/snapshot/<snap>/latest`. | CI (Go) |

**Hardware gates — OWED, and the owner is the Operator.** None of these can be run by a session;
they need the staging stand and a real device. **No claim in this spec rests on them having passed**,
and the rung is not done until they have.

| id | what it proves |
| --- | --- |
| **H1** | An in-place Wi-Fi backup end to end: transfer into `latest/`, verify, snapshot, browse the committed version. |
| ~~**H2**~~ | **DISCHARGED 2026-08-08 — see D4's table.** Answer **A**: rollback succeeded against a dataset bind-mounted into a running unprivileged LXC under read fds, a child's fd, a cwd inside the tree, an active writer and a held write fd. Answer **B** was never observed. Answer **C** — *more recent snapshots or bookmarks exist* — was found instead, and is the likely field case. `zfsOpTimeout`'s 60 s was never approached, so the near-miss it was flagged against did not occur. |
| **H2b** | **NOT a hardware gate and still owed: does the same hold with quince itself live?** H2 used a hand-built tree and an ssh-driven rollback, not `RepairWorkingCopy` on a real job. The ZFS-level answer will not change; what is unproven is quince's own path through it. Folds into H1. |
| **H3** | The seed latency is gone: time from passcode entry to first bytes, against `qn.6b`'s measured baseline (~17.5 s warm, past 60 s cold, on a 133k-file / 34 GB device). |
| **H4** | The host-helper hand-edit: `seed)` removed, `rollback)` added, and the four surviving verbs still answer. |

**What CI cannot prove here, said plainly.** Every Go gate above drives the **fake**
`idevicebackup2`, so G1–G9 prove quince's lifecycle and prove nothing about how the real tool behaves
when its target contains a symlink. Fact 3 is read from the pinned upstream source and is strong, but
a source read is not a run: **H1 is the first time that claim is tested.** Nothing in this spec should
be read as having proven it.

---

## Fixtures

**No new transcripts.** This rung changes where bytes land, not what the tool says, so the existing
replay transcripts in `core/internal/backup/testdata/transcripts/` cover the parser unchanged.

**Extended: `fakeZFS` gains a `rollback` case — and NOTHING ELSE, because argv recording already
exists.** `helpers_test.go:67` is `f.calls = append(f.calls, argv)`, unconditional and first, and the
type's comment already says *"It records every argv so tests can assert exact commands (argv arrays,
no shell) and inject failures."* So G4 needs the `switch` arm, not a fixture change. **Corrected
2026-08-08**: this paragraph asked for the recording to be added, which was an unmeasured claim about
the fixture in a spec whose standard is that facts about the code are measured — and the eleven
interface facts about *production* code all check out, so the one that did not was about the test
harness.

G4 still asserts on **argv rather than a call count**: a count passes on a rollback with the wrong
target. What `rollback` must do in the fake is delete `.zfs/snapshot/` entries newer than the target
and restore `latest/` from it, so answer A is exercised end-to-end; `failOp` already covers answer B.

**Two things PR 3 must change in the same fixture**, recorded here because it is the file every Go
gate runs on and neither is a Boundary entry:

- **its doc comment describes the exchange model** — *"the exchange already moved the verified tree
  into latest/ before the snapshot"* — which goes stale in the commit that deletes the exchange;
- **its `seed` case** is the host-side verb PR 4 deletes. It must **outlive PR 3** — the namespace
  backends do not use `fakeZFS`, but PR 3 must not remove a verb the reference helper still declares
  — and die with `seed)`.

**New: a shell fixture for G11** — the reference helper's text, extracted from `deploy/storage.md`
rather than duplicated, driven with a stub `zfs` on `PATH` that records its argv. Extracted rather
than copied because a copy is a second source of truth for the file this rung's whole argument is
about not having to hand-maintain.

**New: a pre-change working fixture for G9** — a real directory at `working/<udid>` with a known
byte size.

---

## Rule check

- **Never mutate a committed version.** Held, and this is the rung that comes nearest. On zfs a
  committed version is a `@quince-*` snapshot and copy-on-write leaves it untouched; `latest/`
  becomes the mutable head, which is exactly the sentence the ruling changed and canon now records.
  The helper's flag-discarding parse means the new verb **structurally cannot** reach a version
  (facts 8, 9). Roll-forward is preserved: nothing unwinds after verify (D2), and rollback is
  abandon-only with reset as its single caller (D3).
- **No silent caps or fallbacks.** A failed rollback returns non-2xx with its reason and writes no
  audit line (G5); a discarded pre-change working copy is logged with its size (G9); an un-migrated
  helper refuses legibly rather than appearing to succeed (story 11).
- **State honesty.** A version exists only after verify + snapshot. `202` from reset means the
  rollback happened. `H1`–`H4` are declared owed with an owner, and no gate above is ticked on their
  behalf.
- **Docs are part of the diff.** The canon PR is a stated **prerequisite** rather than a companion,
  because the ruling routed that block to the architect seat and the Operator as code owner;
  `deploy/storage.md` and `contracts.md` move in the code PRs that change them.
- **Interface facts looked up live.** `idevicebackup2` behaviour read from the pinned `1.4.0` source,
  not recalled; `zfs rollback` semantics read from OpenZFS `zfs-rollback(8)` on 2026-08-08. Both
  citations name where and when.
- **Privacy is a commit-time gate.** No topology, path or device identifier enters this spec or any
  PR under it; G13 sweeps every head.
- **Secrets discipline.** Untouched — this rung adds no subprocess argument carrying a password. The
  new verb's argv is a dataset and a snapshot name, both already logged today.
- **Subprocesses.** `rollback` goes through the existing `zfsCLI.run`, which already spawns in its
  own process group with group-SIGKILL hygiene (`proc.go`) and is bounded by `zfsOpTimeout`. The
  30-minute `zfsSeedTimeout` is deleted with the seed; a rollback is a metadata operation and takes
  the 60-second bound. **Near-miss named:** if H2 shows a rollback blocking on a busy mount, 60
  seconds may be the wrong bound — that is a thing H2 decides, not something this spec assumes.
- **D5a: `latest/` is a real directory, never a symlink.** `docs/quince.stack.md:347-349`, an
  accepted external-review point whose stated reason is that *"symlink behavior under rclone depends
  on flags and would make the offsite contract fragile."* **This is the rung that puts a symlink in
  the storage tree, so the rule is checked rather than assumed to be untouched.** It holds, for two
  independent reasons: the rule binds **`latest/`**, and under D1 `latest/` stays a real directory —
  the shim is at `working/<udid>`, a different path; and that path is **anchored-excluded from rclone
  already** (fact 10, `- /<subdir>/*/working/**`), so the flag-dependent behaviour the rule was
  written against is structurally out of reach rather than merely unlikely. The shim also does not
  survive a commit, so no snapshot contains one.
  **Named here because getting it wrong in the other direction has already happened**: the rule was
  reached for on quince#591 to rule the shim out, on the reading that the target directory *is* the
  offsite tree. It is not. A reader who reaches for D5a and finds no answer here will either repeat
  that or conclude the rule was missed.
- **Every bug found on hardware becomes a replay fixture.** Standing; nothing found yet.
- **Config tidiness.** `storage.zfs.seed` (`auto|reflink|copy`) becomes meaningless on zfs and
  **keeps meaning for the namespace backends**, which is a `qn.6g` contracts §6 question rather than
  a deletion. Named here as a near-miss so it is not quietly dropped; see open question 2.
- **Don't improvise architecture.** Every design decision above is either the ruling's, or is
  rung-local and recorded in the next section. Nothing here reopens the ruling.

---

## Rung-ruled decisions

1. **The shim is a symlink at `working/<udid>` → `../../latest`.** Rung-local: it changes no contract
   surface, no storage *layout* inside a snapshot, and nothing user-visible. The alternatives and why
   each loses are in D1.
2. **`workState` survives with `SeedInProgress` pinned to `false` on zfs**, rather than being
   replaced by a new head-dirty sentinel. Cheaper, and it keeps facts 5 and 6 true verbatim.
3. **The `latestHasVersion` idempotency guard is removed** from the zfs commit path, with a comment
   saying it guarded the exchange's reversibility and nothing reverses now.
4. **A pre-change real `working/<udid>` is discarded, not migrated.** Operator note 2026-08-08: sole
   user, no `v0.1` tag.
5. **The `rollback` verb lands in its own PR, before the lifecycle switch**, so the host hand-edit
   can be done ahead of the change that needs it. See PR slicing.

---

## Known gaps and open questions

1. ~~**Rollback under load is unmeasured.**~~ **MEASURED 2026-08-08 — answer A** (D4). What is open
   is now narrower and different: **answer C, a newer snapshot blocking the rollback, is the likely
   field case and quince cannot currently SEE it** (open question 7). Answer B was never observed and
   is retained as the unobserved branch, on one host's evidence.
2. **`storage.zfs.seed` on a backend that does not seed.** It stays valid and meaningful for the
   namespace backends, so it is not deleted; what a zfs storage should report for it — ignored,
   omitted, or an explicit *not applicable* — is a `qn.6g` contracts §6 shape question. **Not decided
   here**, because deciding it silently is how a config key ends up claiming to do something.
3. **Offsite on zfs has no snapshot-sourced path yet.** This rung states the cost and canon records
   it; building the snapshot-mount source is **quince#735**. Until then a zfs user must exclude
   quince from a whole-host rclone job, which `deploy/storage.md` must say in the words an operator
   will actually read. **The cost is not hypothetical and it fails silently from the operator's
   side**: a walk crossing a backup uploads a half-transferred tree as though it were verified. That
   is why PR 4 carries the `deploy/storage.md` wording rather than leaving it to quince#735.
4. **Whether the real `idevicebackup2` tolerates a symlink at its `<target>/<udid>`** is read from
   source (fact 3) and not run. **H1 is the first execution**, and it is the shim's whole risk.
   **If it does not, the fallback is D1's alternative (a)** — `latest/` becomes the target and the
   tree is `latest/<udid>/` — **not** a sixth libimobiledevice patch. Corrected 2026-08-08 after the
   Operator raised the `<target>/<UDID>` constraint on quince#591: this item named the patch, and a
   patch is strictly more expensive than a shape that is available today and needs no upstream
   change. Either way it is a spec change rather than an implementation detail, because it moves
   where committed content sits inside a snapshot.

   **The trade between the two is one fact and it is worth having written down**, since a future
   reader meeting a symlink will ask why: (a) has no runtime uncertainty, and it moves the content to
   `latest/<udid>/`, so **every snapshot taken before `qn.6h` stops being browsable** — silently,
   because `browseRoot` resolving to an absent path is an empty browse and not an error. The shim
   keeps `latest/`'s layout byte-identical, so old snapshots keep working and `browseRoot` needs no
   change at all. *Migration is out of the table* (Operator, 2026-08-08) is licence not to **build** a
   migration path; the shim is the option that does not need one.
5. **`SweepWork` is `return nil` today, and quince#731 makes reconciliation SCHEDULED — nothing
   sequences the two.** That issue already records that a future sweep must tell a live job's
   `working/` from an orphan. **Under this rung the object such a sweep would remove is the shim**,
   so removing it mid-job breaks the tool's target *immediately* rather than merely wasting a clone
   — a strictly worse failure than the one quince#731 is written against. `reconcile.go` is
   deliberately not in this rung's Boundary and quince#731 has no rung number, so this is recorded
   rather than built: whichever lands second owes the other a check.
6. **Whether a zfs version row can actually hold a nil `ZFSSnapshot`** is unresolved, and D7 is
   deliberately built so the answer does not matter: browse refuses rather than falling back. If
   somebody later establishes the nil case is unreachable, D7's guard becomes belt-and-braces and
   should be **kept** — the cost is one branch, and the failure it prevents is serving a
   half-transferred tree as a version. **This is the ruling's fourth sub-question in everything but
   name**, raised by the architect on quince#733 rather than by the issue, and recorded here so it is
   not mistaken for one of the three the Operator required.
7. **quince cannot see foreign snapshots, so it cannot predict or precisely explain answer C.**
   `zfscli.List` filters to `HasPrefix(short, "quince-")` (`zfscli.go:103`); the helper's `list`
   already returns everything, so this is a quince-side filter and not a capability gap. **Not built
   in this rung.** Two things it would enable and one it would not: a reset that refuses *before*
   attempting, with the blocking snapshot named; and a message that distinguishes answer C from
   answer B. It would **not** let quince clear the blockage — foreign snapshots are not quince's to
   destroy and the helper correctly refuses them (measured). Recorded rather than folded in, because
   an unfiltered snapshot list is a change to what quince claims to know about a storage, and that is
   contracts territory rather than a rung-local detail.
8. **The measurement is ONE host.** One pool, one kernel, one `zfs` version, an unprivileged LXC on
   PVE with `rbind` propagation. Answers A and C are facts about that host; nothing here establishes
   that a different topology — a NAS, a privileged container, a different ZFS release — behaves the
   same. Answer B is retained for exactly this reason.
9. **UNRULED AND BLOCKING PR 3: what reset DOES when rollback is refused.** Answer C turns an
   operation that is unconditional today — `RemoveAll` on a directory quince owns, refusable by
   nothing — into one that **usually refuses** on any host with ordinary snapshot hygiene. The
   2026-08-04 ruling licensed `rollback` as *available but destructive-if-misused*; C makes it **safe
   and mostly absent**, which is the opposite failure and was not contemplated.
   **The mitigation in D4 answered the wrong case and is withdrawn**: *"do nothing, the head is
   resumable"* is the **retry** path, and reset exists for when the head is **bad**. Four shapes are
   on the table — accept C with a documented `com.sun:auto-snapshot=false` recommendation; a
   full-copy restore from `.zfs/snapshot/<newest @quince-*>/latest/` as a fallback; the unfiltered
   snapshot view of question 7 so quince can refuse *before* the attempt; or a **delta** restore that
   makes reset unconditional at O(delta) data and removes the need for a `rollback` verb at all,
   shrinking the host hand-edit to a deletion. Raised on quince#736; **the delta size after a torn
   incremental is asserted and NOT measured**, and that figure is what the fourth option rests on.
10. **`engine.go:322-324`'s `409` is load-bearing for CORRECTNESS, not tidiness**, and PR 3 owes a
    comment at the guard saying so. A rollback removes files under an active writer with **no error
    to either side** (measured), so a regression there would produce a rolled-back tree immediately
    re-dirtied, silently. Recorded here because the reasoning lives in a review thread and the guard
    reads like a politeness check.

---

## PR slicing

Each PR branches from `main` and carries one reviewable claim. **Sequenced, never stacked** —
`CLAUDE.md` §1.

| | claim | proof |
| --- | --- | --- |
| **0** | *(prerequisite, already open)* canon records the ruling | Operator approval as code owner |
| **1** | **this spec** | architect review; `/docs/specs/**` is not code-owned |
| **2** | **quince can ask the host to roll back, and the parse bounds it.** `zfscli.Seed` → `Rollback`; the reference helper gains `rollback)` and **keeps** `seed)`. Purely additive — the exchange model still runs, nothing changes behaviour. Landing it first is what lets the Operator do the host edit before the change that needs it. | G4 (argv), G11 (helper), G12 |
| **3** | **zfs writes in place and commits by snapshot.** The shim, the deleted seed path, the deleted exchange, the new commit sequence, and reset-by-rollback. One PR because splitting it ships a broken intermediate: writing in place while still exchanging would swap `latest/` with a symlink, and switching the write path without switching reset would leave reset claiming a discard it did not perform. **D7's browse guard is in this PR, not a later one** — the fallback becomes live-tree-reading in the same commit that makes `latest/` mutable, so shipping it separately means shipping the hazard. | G1, G2, G3, G5, G6, G7, G8, G9, G10, G12, G13, G14 |
| **4** | **the helper stops carrying quince's lifecycle.** `seed)` deleted from `deploy/storage.md`, the **one-line changed-verbs note** (not a procedure), the offsite exclusion note, `contracts.md`'s reset failure. | G11, G12, G13 |
| **5** | **the hardware evidence.** H1–H4 recorded on the rung issue, and whatever H2 decides about the timeout bound. Not a code PR unless H2 says it is. | Operator |

**H2 HAS RUN — 2026-08-08 — so the block on PR 3 is DISCHARGED**, not waived. The Operator's
2026-08-08 ruling made the measurement a precondition of the implementation PR; the measurement
happened, on real ZFS, through the real helper. What it changed is inside PR 3's scope rather than
its timing: reset must now handle **answer C**, which the spec did not have when the ruling was
taken.

**The ordering it justified still holds and was vindicated in practice**: PR 2's verb existed on the
lab host before any measurement was attempted, which is exactly why H2 could run through the real
forced-command path instead of a hand-typed `zfs` call.

**PR 3 is the one that cannot be sliced further, and that is a claim worth checking at review rather
than accepting.** If a reviewer can name a smaller intermediate that is honest on `main`, it should
be taken.
