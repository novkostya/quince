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
| `core/internal/storage/zfs.go` | the tool's target becomes `<backups>` and the tree the child dataset root; `PrepareWork`/`SeedWork`/`WorkDir` stop delegating to the shared seed machine; `seedWorking`, `seedInContainer`, `seedClosure`, `Provision`'s `latest/` mkdir and the exchange in `finishCommit` all go; `RepairWorkingCopy` becomes a `rollback` (ruled 08-08) |
| `core/internal/storage/zfscli.go` | the `seed` verb becomes `rollback` |
| `core/internal/storage/journal.go` | **NOT doc-only — the journal MOVES to the parent dataset**, for the work sentinel's reason and at the same time: it is on disk when `zfs snapshot` runs, so at `<deviceDir>/` it rides into every committed version. `writeJournal`/`readJournal`/`removeJournal` take a PATH; `scanFlatJournals` reads the parent-level files; `PendingJournals` switches to it. **This row said `doc only` until quince#745 measured otherwise** (86 changed lines, three signatures, one new function) — recorded rather than quietly rewritten, because the Boundary is what the following PRs are planned against. The doc half stands: `:20-26`'s *"both models share the atomic exchange as their pivot"* block, and `PhasePrepared`'s *"marker written into `working/<udid>`"* |
| `core/internal/storage/layout.go` | `browseRoot`'s zfs arm loses its trailing `latest` component and can no longer return the live tree (D7); `isEmptyDir` must ignore `.zfs` (D8); the work-sentinel path becomes backend-dependent (D3) |
| `deploy/storage.md` | the reference helper loses `seed)`, gains `rollback)`; **the snapshotter exclusion as REQUIRED setup** (D6); the offsite note; the one hand-edit |
| `docs/quince.design.md`, `CLAUDE.md` | the ruling — **a separate, prerequisite PR** |
| `docs/contracts.md` | `POST /api/devices/{udid}/reset-working` gains a failure the zfs backend can return |

**Out of scope, each with a why.**

- **The namespace backends.** reflink / hardlink / copy keep `working/` + seed + exchange + archive,
  keep `qn.6b`'s `--gate` patch, and keep Finding B's partial-clone discard. The patch is **not**
  deleted; zfs merely stops needing it. Their existing gates must still pass untouched, which is
  story 9.
- **The version MODEL — but NOT the layout, and that distinction is now load-bearing.** A version is
  still a `@quince-*` snapshot; markers, verify, retention, adopt, prune and
  reconcile-from-snapshots keep their semantics. **What moves is where the content sits inside the
  snapshot**: `<snap>/latest/` becomes `<snap>/`. The consequence is that pre-`qn.6h` snapshots are
  **not browsable**, ruled with no dual-read fallback (D1).
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
- **Making offsite read a snapshot mount.** This rung *states* that the live tree is torn during a zfs
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
   target must be inside the backup dataset**, which is satisfied by construction under D1 — the target IS the parent dataset. Condition 1 in D8 is what this fact costs.

5. **`isDirty` stats the directory, deliberately** — `reset.go:101-104`, *"IT INCLUDES THE
   KILLED-SEED CASE deliberately … That is why this stats the directory rather than reading the
   sentinel."* **The fact stands; the inference this entry used to draw from it does not.** It read
   *"whatever replaces the working copy must therefore still be a directory … or `RepairWorking`'s
   whole resolver changes"* — the 2026-08-08 ruling leaves **no** such directory on zfs, so `isDirty`
   becomes a backend method and the killed-seed case it protects has no zfs analogue (D3). Kept as a
   fact because the *namespace* behaviour it describes is unchanged and still load-bearing there.

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

10. **rclone's rules are EXCLUSIONS only** — `offsite.go`: `working/**`, `versions/**`, the storage
    marker. `latest/` is synced by *not being excluded*, so its name is not in the executable
    contract. **After D1 the zfs arm has neither `working/` nor `latest/`**, so those rules simply
    match nothing on that backend and the whole device dataset is in scope — which is the torn-tree
    exposure quince#735 tracks, not something this rung fixes.

11. **Reset is already refused while a backup runs** — `engine.go:322-324`, `409` *"a backup is
    running for this device — cancel it before resetting"*. So a rollback can never race quince's own
    writer.

12. **Browse on zfs reads the snapshot, the newest version included — but only when the registry row
    carries one.** `browseRoot`'s zfs arm is `backend == BackendZFS && zfsSnapshot != nil` and
    **ignores `isLatest`** (`layout.go:82-91`), so the newest version genuinely is read from
    `.zfs/snapshot/<snap>/` today. **When `zfsSnapshot` is nil it falls through to
    `if isLatest { return latestDir(...) }`** — a live-tree read. Harmless today, because on zfs
    `latest/` was always a complete committed tree. **Under D1 the fallback resolves to the dataset ROOT, which is the tree being written.** The nil case
    is **representable and NOT shown to occur**: `ZFSSnapshot` is a `*string` off the registry row,
    and `zfs.go:287` handles a nil elsewhere. Found by the architect reviewing the canon PR
    (quince#733), not by this spec's own reading. See D7.

13. **`PhasePrepared`'s definition is written against the exchange** — `journal.go:26`, *"marker
    written into `working/<udid>`"*, under a block comment reading *"both models share the atomic
    exchange as their pivot"* and listing `zfs: prepared → exchanged → snapshot_created`. The phase
    name survives this rung; those sentences do not.

---

## Design

### D1 — The write path: the tree IS the dataset root

**RULED by the Operator, 2026-08-08** (quince#737, relayed by the architect): *"I would like to have
clean structure on zfs for myself. I'm making the product for myself after all."* On zfs there is
**no `latest/`, no working copy, no symlink** — `idevicebackup2` writes the backup tree directly into
the device's child dataset root.

```
<backups>/                      the PARENT dataset — the tool's target
└── <udid>/                     the CHILD dataset's mountpoint == deviceDir == THE TREE
    ├── Info.plist              written by the tool, at the root, iTunes-layout
    ├── Manifest.db
    ├── quince-version.json     quince's marker, riding into the snapshot
    └── 00/ a1/ …               backup content

<backups>/.quince-work-<udid>.json     the work sentinel — in the PARENT, outside the child
                                       dataset, so it can never enter a snapshot
<backups>/.quince-commit-<udid>.json   the commit journal — same place, same reason. It exists
                                       only while a commit is in flight, and a commit's LAST act
                                       is the snapshot, so this is the one quince file guaranteed
                                       to be on disk at capture time.
```

**Both sidecars, not just the sentinel.** The journal was missed when this diagram was written and
found in review of the PR that implements it (quince#745): the Boundary called `journal.go` doc-only,
which was true of the *enum* and false of the *path*. Named here because a reader planning PR 4, or
reasoning about what a snapshot contains, looks at this diagram rather than at the code.

**This satisfies fact 3 rather than bridging it.** The tool appends `<UDID>` to its target and cannot
be told not to; give it `<backups>` and its own convention lands the tree at `<backups>/<udid>`,
which is already the child dataset's mountpoint (`layout.go:30`'s `deviceDir` — the exact path
`Provision` stats today as its visibility probe, `zfs.go:99-100`). **The dataset root is the tree.**
Browse becomes `.zfs/snapshot/<snap>/` with no trailing component.

**Why this works on zfs and NOT on the namespace backends.** There, the device dir must stay a
container, because three trees coexist inside it — `latest/`, `versions/<ts>/` and `working/<udid>`,
plus the sentinel (`layout.go:32-50`). On zfs after this rung there is exactly **one** tree: history
moved into snapshots and the working copy is gone. **One thing needs no container.** That asymmetry
is the whole reason the shape is available here and nowhere else, and it is why **`latest/` stays,
and stays correct, on reflink / hardlink / copy** — it pairs with `versions/<ts>/` and there is no
snapshot for the name to be wrong inside.

**Pre-`qn.6h` snapshots stop being browsable, and this is ruled rather than overlooked.** Their
content sits at `<snap>/latest/`; afterwards quince reads `<snap>/`. **No dual-read fallback** —
Operator, 2026-08-08. The degradation is at least structurally quiet rather than wrong:
`committedFromSnapshot` reads the marker at the snapshot root, finds none, and skips the snapshot,
exactly as `zfs.go:317-329` already skips pre-`qn.5b` snapshots holding content at `working/`.
**Skipping must be LOGGED and not silent** (story 14) — *no silent caps or fallbacks* — because an
unbrowsable version that says nothing is indistinguishable from one that was never taken.

#### Superseded alternatives, kept because each was rejected for a reason that still holds

- **A per-job symlink shim at `working/<udid>` → `../../latest`.** This spec's own D1 until the
  ruling, and the reason it fell is not aesthetics: it kept `latest/`, so it inherited the name the
  Operator objected to, and it rested on the real `idevicebackup2` tolerating a symlink at its
  target — a source read (`__mkdir`'s return is discarded, `idevicebackup2.c:1999`), never a run.
  **The ruled shape removes that risk entirely** rather than deferring it to H1.
- **`latest/` as the target, tree at `latest/<udid>/`.** Doubles the UDID in the path and moves
  content one level deeper inside every future snapshot — the Operator's *"ugly"*, and it still
  keeps the disputed name.
- **A sixth libimobiledevice patch.** A maintained patch forever, against a ruling whose prize is
  shrinking what must be hand-maintained.
- **A bind mount.** Needs privilege; this deployment deliberately is not privileged.

#### A consequence that is parked, not a feature

`<backups>/<udid>/Info.plist` **is** the iTunes/Finder `MobileSync/Backup` layout, so a shared
dataset would be directly readable by external tools. **Operator ruling: parked.** No share, no
Samba, no documented external-tool workflow in this rung. Recorded because the property will be
discovered and otherwise treated as supported — and note it would be **zfs-only**, since a namespace
storage still presents `<udid>/latest/…` where those tools cannot find `Info.plist`.
### D2 — Commit: verify → remove the scaffolding → `zfs snapshot`

```
prepared → snapshot_created → registry_committed
```

`Commit` writes the marker into `TreePath` — which is now the **dataset root itself** — journals
`PhasePrepared`, and `finishCommit` does **one** thing where it used to do three:

1. **`Snapshot`** → `PhaseSnapshotCreated`.

**There is no pre-snapshot cleanup, and that is the ruled shape paying off.** Under D1 the child
dataset holds only the tree and its marker: `working/` does not exist, and **both** per-device
sidecars live in the **parent** — the work sentinel `<backups>/.quince-work-<udid>.json` and the
commit journal `<backups>/.quince-commit-<udid>.json` — outside the snapshot's path entirely. The
journal is the sharper of the two: it is written *before* the snapshot and removed *after* it, so it
is the one file that is certainly on disk at capture time and would be in every version. So canon's
*between backups the dataset holds only the backup* is satisfied by construction rather than by
ordering. The sentinel is cleared whenever — before or after the snapshot — because it was never
capturable.

*(There is no `working/` and no symlink on this backend, so the removal-ordering property that would
otherwise guard the committed tree guards nothing here.)*

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

### D3 — Sub-question 1: `reset.go`, `worksentinel.go`, `WorkingReset` with **no** working directory

**The previous answer is WITHDRAWN.** It read *"the surface does not change at all, and the shim is
why — `working/` still exists during a job, so facts 5 and 6 hold verbatim."* That answered the
ruling's sub-question by **arranging for there to still be a working directory**. Under the
2026-08-08 ruling there is none on zfs, so the sub-question is live again and is answered here
properly rather than dissolved.

**The HTTP surface genuinely is unchanged**, and that part survives: `POST
/api/devices/{udid}/reset-working`, `WorkingReset`, `Engine.ResetWorking`'s `404`/`409`/`503`, and
`Manager.RepairWorking`'s multi-storage resolver (`202` clean, `409` ambiguous, the candidate list,
the audit line, the CLI) all keep their shapes. What changes is what "dirty" *means* underneath.

#### The sentinel moves OUT of the tree, and the path becomes backend-dependent

Today `workSentinel` is `<deviceDir>/.quince-work.json` (`layout.go:60-62`) — inside the device dir.
On the namespace backends that is a **container** directory and the sentinel is correctly out of the
way. On zfs after this rung `deviceDir` **is the backup tree**, so the same path would put quince's
bookkeeping inside the directory the tool writes, an external reader treats as an iTunes backup, and
the snapshot captures.

So on zfs it becomes **`<backups>/.quince-work-<udid>.json`** — in the *parent* dataset, outside the
child entirely. Two properties fall out and both matter: it **cannot ride into a snapshot** (so
commit no longer has to remove it before snapshotting for the snapshot to be clean), and the tool
never touches it (fact 3: the tool writes only under `<target>/<udid>`).

**The namespace path does not move.** Changing it there would alter a backend this rung is otherwise
not touching, and `workState`'s legacy-safe decode exists precisely so an upgrade never discards a
resumable tree.

#### `isDirty` must become backend-dispatched — fact 5's inference no longer holds

`reset.go:101-104` stats `workingParent`, and its comment explains why: *"IT INCLUDES THE KILLED-SEED
CASE deliberately … that is why this stats the directory rather than reading the sentinel."* On zfs
that path will now **never exist**, so an unmodified `isDirty` returns `false` always and
`RepairWorking` stops seeing a dirty head at all — a reset that silently reports nothing to do.

`isDirty` is a package-level function over `(root, udid)`; it becomes a **`Backend` method**:

| backend | dirty means |
| --- | --- |
| namespace | `working/` exists — unchanged, killed-seed case included |
| zfs | **the work sentinel exists** — a job wrote into the head and no snapshot has been taken since |

The killed-seed case it was protecting has no zfs analogue: there is no seed, so there is no partial
clone. **`workState.SeedInProgress` is always `false` on zfs and must not be repurposed** to mean *a
transfer is in flight* — that would make `prepareWorkDirPhase1`'s guard discard a **resumable dirty
head**, which is exactly the *a failed job keeps its dirty working so a retry resumes* rule. The
field name invites the mistake, which is why it is called out.

#### `seedKind`'s derivation moves; its meaning does not

`workState.SeededFromLatest` stays the authoritative full-vs-incremental signal (fact 6). Its
derivation was *is `latest/` non-empty at job start*; it becomes **is the dataset root non-empty at
job start** — the same question about the same content, asked at the path that now holds it.

**With one trap that is condition 2's:** `isEmptyDir` must ignore `.zfs`. At `snapdir=hidden`
(the default) `readdir` never returns it and the check is safe by luck; at `snapdir=visible` — which
operators set in order to browse snapshots by hand — a device with zero backups would read as
*non-empty*, and its first backup would be recorded `incremental`. Silent, and wrong in the field
rather than in CI.

#### `RepairWorkingCopy` on zfs — **RULED 2026-08-08: `zfs rollback`, and answer C is a refusal**

Reset rolls the dataset back to the newest `@quince-*` snapshot. It is `rollback`'s only caller,
abandon-only, never after verify, never the failure default.

**When answer C blocks it, reset refuses and says so** — non-2xx carrying `zfs`'s own words, head
left dirty, sentinel left in place, no audit line, no automatic retry, and **C's remedy named rather
than B's** (G5c). The full-copy and delta restores, and question 7's unfiltered view, were all
considered and **not taken**; open question 9 records shape 4 and why it lost.

**The condition the ruling attaches is REQUIRED SETUP rather than a tip**: quince's datasets must be
excluded from whatever snapshotter the host runs. D6 carries the wording and the reason, and the
reason is not reset.

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
- **quince's readers are largely absent**: browse on zfs reads `.zfs/snapshot/…`, never the live tree
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

**Rejected under answer B: emptying the dataset root in-container as a fallback.** It is available, it is
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
restores it over the clone, because the clone overwrites it with the previous version's. With no clone there is
nothing to overwrite it. `PrepareWork` returns `seedPending=false`, so `engine.go:458` takes the
plain `supervise` branch with `gatePath=""`, and `--gate` is not passed. `superviseGatedSeed`,
`awaitInfoPlist`, `readStableInfo` and the patch all stay, for the seeding backends.

**One consequence, stated because it looks alarming and is not.** The tool does
`remove_file(info_path)` then writes a fresh one **before** sending the `Backup` request (spike C12,
`idevicebackup2.c:2242-2243`). On zfs that now rewrites the live head's `Info.plist` before a byte
transfers. If the job then fails, the dataset root carries a fresh `Info.plist` over the previous version's
content — which is the **dirty head**, the resumable state the retain rule protects. The previous
version is intact in its snapshot, and `quince-version.json` at the root still names the old version
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

#### REQUIRED SETUP: exclude quince's datasets from the host snapshotter

**Ruled 2026-08-08 as part of the reset decision, and `deploy/storage.md` must carry it as required
setup in the words an operator reads — not as a tip.**

```sh
zfs set com.sun:auto-snapshot=false <parent-dataset>
```

**Written snapshotter-AGNOSTICALLY.** That property is `zfs-auto-snapshot`'s specifically —
confirmed from its man page: *"by default `zfs-auto-snapshot` will snapshot all datasets except for
those in which the user-property `com.sun:auto-snapshot` is set to `false`"*. sanoid, zrepl and
`pve-zsync` each need their own exclusion, so the instruction is **exclude quince's datasets from
whatever snapshotter you run**, with the command above as the worked example. Setting it on the
**parent** covers every per-device child, now and future, because ZFS user properties inherit.

**The reason is NOT reset, and that is what makes this a fix rather than a shim for answer C.** A
snapshot taken by another tool alongside a `@quince-*` one **pins the same blocks**, so destroying
the quince snapshot frees nothing: quince's retention would run, report versions removed, and
reclaim no space. **That is a live defect today, unrelated to this rung**, and it is quince#738. With
the exclusion in place, answer C stops being the field case and becomes the misconfiguration case —
the reset benefit is a side effect of a setting that was already correct.

**The hand-edit is NOT detectable, and that is a stated cost.** `hookcheck` fires `capacity` and
`list` — both harmless. `rollback` cannot be probed, because probing it means performing it. So an
un-migrated helper surfaces at the **first reset**, as the helper's own
`quince-zfs-helper: refused: rollback …` on stderr. This rung's job is to make that message legible
(story 11), not to pretend it can be caught earlier.

### D7 — Browse reads `.zfs/snapshot/<snap>/`, and must never resolve to the live head

**The path loses its trailing component.** Under the 2026-08-08 ruling a version's content is at the
snapshot root, so `browseRoot`'s zfs arm becomes `.zfs/snapshot/<snap>` — not
`.zfs/snapshot/<snap>/`. `committedFromSnapshot` and `Scan` (`zfs.go:321`, `:353`) move with
it. **Measured this session on real ZFS**: that directory is walkable from inside an unprivileged
LXC, tree and marker both readable, which is the roadmap's *"known minefield (probe first)"* probed.

**The rest of this section came from review rather than from the ruling** (architect, on
quince#733), and it is the sharpest hazard in the rung: `browseRoot`'s nil-snapshot fallback returns
the live device dir, which in place is **the tree being written** (fact 12). A browse session would
walk a half-transferred backup and present it as a version.

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
the live tree is known-complete from a row's point of view: between the marker write and the snapshot it
is a committed-looking tree that is not yet a version, and after a failure it is a dirty head that
still carries the *previous* version's marker. Any read that resolves *the newest version* to the
head is reading a tree whose completeness is a timing question. The snapshot is the only artifact
that answers it, which is why the arm that already ignores `isLatest` is the correct one and the
fallback is the defect.

### D8 — The ruling's three conditions

**Attached to the 2026-08-08 shape ruling and unchanged by it.** Each is a consequence of the tree
moving to the dataset root, and each is cheap to honour and silent to get wrong.

**1. Per-device quotas become UNSUPPORTED, and must be DECLARED — not merely left broken.**
`deploy/storage.md` says, in words an operator reads: *set the quota on the parent dataset, not per
device.* The reason is measured rather than argued. `engine.go:524` already statfs's `e.backups` —
**the parent** — so quince's own preflight is quota-blind today; the only thing honouring a
per-device quota is the tool's own `statvfs` on a target inside the child, and this ruling moves that
target up to the parent.

**MEASURED on the real pool, 2026-08-08, both cases — this condition is discharged, not asserted.**

| child dataset | what the tool sees at the **parent** (ruled shape) | at the **child** (today) |
| --- | --- | --- |
| no quota | 1620 GiB | 1620 GiB — **identical** |
| `quota=10G` | **1620 GiB** | **9 GiB** |

**So the move is free in the ordinary case and catastrophic in the quota case, with nothing in
between.** With a 10 GiB quota the tool would be told 1620 GiB is available — a **180×
overstatement** — and would start a backup that cannot fit. **The failure mode is what makes this a
declaration rather than a footnote:** a per-device quota stops producing a clean up-front refusal by
the device and starts producing **ENOSPC mid-transfer**, which costs a multi-hour Wi-Fi backup.
`engine.go:641` names the property being spent — *"free-space statfs truthful by construction"* —
and `engine.go:524` shows quince's own preflight already statfs's the parent, so **after this rung
nothing anywhere honours a per-device quota.** That is why `deploy/storage.md` must say *set the
quota on the parent dataset, not per device*, in words an operator reads.

**2. `.zfs` must be skipped explicitly by every walker, and `snapdir` asserted at `Provision`.**
Once the tree is the dataset root, `.zfs` is a child of the tree. At the `hidden` default `readdir`
never returns it and everything is safe **by luck**; at `snapdir=visible` — which operators set in
order to browse snapshots by hand, and which this session set on the lab dataset — `verify`,
`dirSize`/`logical_bytes` and the `isEmptyDir` check behind `seedKind` would all recurse into **every
snapshot**. Silently wrong, and wrong in proportion to how many versions exist. So: an explicit skip
in each walker, plus an assertion at `Provision`, which already performs a visibility probe
(`zfs.go:99-100`) and is therefore the natural place for it.

**3. `statvfs` measured on the real pool. DONE** — the table under condition 1. Both cases, on the stand that discharged H2. No gate is owed for it.

**`PhasePrepared` and its block comment move with the sequence** (fact 13). The phase keeps its name
and its position; on zfs it means *marker written into the dataset root*, and `journal.go`'s comment must say
so rather than leaving `working/<udid>` as the only definition. Named here because the rung's Boundary
lists `journal.go` as doc-only, and a doc-only file is the one most easily skipped.

---

## Stories

1. **A zfs backup writes into the DATASET ROOT and no clone is ever made.** Both cases: a first
   backup (empty root) and an incremental (populated root). No `cp`, no reflink, no `seed` verb, and
   no `latest/` anywhere on the zfs path.
2. **A committed zfs version is a `@quince-*` snapshot** whose ROOT is what the tool wrote, and
   browsing that version reads `.zfs/snapshot/<snap>/` — no trailing component.
3. **Between backups the dataset holds only the backup tree.** No snapshot carries `working/` or the work
   sentinel, because neither lives in the child dataset: `working/` does not exist and the sentinel is in the parent.
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
12. **A pre-change `latest/` or `working/` in the dataset root is discarded, loudly**, with a log line
    naming its size, before any snapshot can capture it. Under the ruled shape those directories are
    no longer siblings of the tree — **they are inside it** — so leaving them rides them into every
    future version and shows them to any external reader.
13. **Browse never resolves to the live tree on zfs.** A zfs version row with no snapshot is
    surfaced as unbrowsable with a reason, never as a `browse_root` pointing at the dataset root.
14. **A pre-`qn.6h` snapshot is skipped with a LOG LINE, not silently.** Its content sits at
    `<snap>/latest/`, so a marker read at `<snap>/` finds nothing. Skipping is ruled; skipping
    *quietly* is what *no silent caps or fallbacks* forbids.

---

## Gates

| id | what it proves | where |
| --- | --- | --- |
| **G1** | A zfs commit produces a snapshot whose ROOT matches the tree the fake tool wrote, with no clone step and no `PhaseExchanged` in the journal. Both first-backup and incremental. | CI (Go) |
| **G2** | The snapshot contains **only** the backup tree and its marker — no `latest/`, no `working/`, and **neither sidecar**: not the work sentinel and not the commit journal. Asserted on the filesystem after commit, entry by entry, not on the API. | CI (Go) |
| **G3** | A killed job leaves the head dirty and the sentinel in place; the next `PrepareWork` resumes it, and nothing calls rollback. | CI (Go) |
| **G4** | `RepairWorkingCopy` on zfs issues exactly one `zfs rollback <newest @quince-*>` — asserted on the recorded argv, so `-r` can never creep in. | CI (Go, fake `zfsCLI`) |
| **G5** | **D4 answers B and C.** A rollback that fails fast, one that exceeds `zfsOpTimeout`, and one refused with **the measured answer-C text** (`more recent snapshots or bookmarks exist`) ⇒ non-2xx from `RepairWorking`, `zfs`'s reason propagated **verbatim**, the sentinel and `working/` still present, **no audit line**, **exactly one** rollback attempt recorded. This is the state-honesty gate. | CI (Go) |
| **G5c** | **The answer-C remedy is C's, not B's.** The message for *more recent snapshots exist* must not tell the operator to stop the container, and must say the dirty head remains resumable. Added because the two failures were one branch in the spec until H2 separated them. | CI (Go) |
| **G6** | Reset on a device with zero `@quince-*` snapshots empties the head and answers `202`; reset on a device **with** snapshots never takes that branch. | CI (Go) |
| **G7** | The committed `Info.plist` is the job's fresh one, and `supervise` was called with an empty `gatePath` on zfs. | CI (Go) |
| **G8** | **The ruled shape, asserted directly.** The tool's target is `<backups>`; the tree lands at the child dataset mountpoint; the child dataset contains the tree and its marker and **nothing else** — no `latest/`, no `working/`, no sentinel. Replaces a gate that asserted the withdrawn shim. | CI (Go) |
| **G9** | **A pre-change `latest/` (and `working/`) sitting in the dataset root is removed and logged with its size**, before any snapshot can capture it. Under the ruled shape the old layout's directories become *subdirectories of the new tree*, so leaving them would ride them into every future version and show them to any external reader. | CI (Go) |
| **G10** | Every existing namespace-backend storage test passes unchanged — the diff must not touch their expectations. | CI (Go) |
| **G11** | The reference helper in `deploy/storage.md` accepts `rollback <parent>/<udid>@quince-*`, refuses every other target, and drops `-r` — run against the real script text with a stub `zfs` on `PATH`. | CI (shell) |
| **G12** | `make gates` · `make image` · `make gates-ui-e2e` | CI |
| **G13** | `make privacy-check REF=origin/main...HEAD TEXT=<file under $HOME/scratch/<runner>/>` | host |
| **G14** | `browseRoot` on the zfs backend **never** returns `latestDir()` — driven with a deliberately-nil `ZFSSnapshot`, so the guard is proven against the case rather than argued away from it. The newest version still resolves to `.zfs/snapshot/<snap>/`. | CI (Go) |

**Hardware gates — OWED, and the owner is the Operator.** None of these can be run by a session;
they need the staging stand and a real device. **No claim in this spec rests on them having passed**,
and the rung is not done until they have.

| id | what it proves |
| --- | --- |
| **H1** | An in-place Wi-Fi backup end to end: transfer into the dataset root, verify, snapshot, browse the committed version. |
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
and restore the dataset root from it, so answer A is exercised end-to-end; `failOp` already covers answer B.

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
  committed version is a `@quince-*` snapshot and copy-on-write leaves it untouched, while
  the dataset root becomes the mutable head — canon's `latest/` sentence is what the 2026-08-04 ruling changed, and the 2026-08-08 shape ruling removes `latest/` from this backend entirely.
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
- **D5a: `latest/` is a real directory, never a symlink.** `docs/quince.stack.md:347-349`, whose
  stated reason is that *"symlink behavior under rclone depends on flags and would make the offsite
  contract fragile."* **The rule is untouched, and after the 2026-08-08 ruling it is MOOT on zfs**:
  the rung introduces no symlink anywhere, and there is no `latest/` on that backend for the rule to
  reach. It continues to bind — unchanged and correct — on reflink / hardlink / copy, which this rung
  does not modify.
  **Kept in the Rule check rather than dropped, because the entry earned its place twice.** It was
  added when the design *did* put a symlink in the storage tree, and before that the rule was reached
  for on quince#591 to argue the shim *out*, on the reading that the tool's target directory **is**
  the offsite tree. It is not. A reader who meets `latest/`-adjacent changes and finds no D5a entry
  here will repeat one of those two errors.
- **Every bug found on hardware becomes a replay fixture.** Standing; nothing found yet.
- **Config tidiness.** `storage.zfs.seed` (`auto|reflink|copy`) becomes meaningless on zfs and
  **keeps meaning for the namespace backends**, which is a `qn.6g` contracts §6 question rather than
  a deletion. Named here as a near-miss so it is not quietly dropped; see open question 2.
- **Don't improvise architecture.** Every design decision above is either the ruling's, or is
  rung-local and recorded in the next section. Nothing here reopens the ruling.

---

## Rung-ruled decisions

1. ~~**The shim is a symlink at `working/<udid>` → `../../latest`.**~~ **SUPERSEDED by the Operator's
   2026-08-08 shape ruling** — and note it was *never* rung-local as this entry claimed: it was
   recorded here as changing "no storage layout inside a snapshot", which was true of the shim and is
   emphatically not true of what replaced it. The ruled shape is D1 and it is an Operator decision,
   not a rung-ruled one.
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
4. ~~**Whether the real `idevicebackup2` tolerates a symlink at its `<target>/<udid>`.**~~ **CLOSED by
   the 2026-08-08 shape ruling — there is no symlink.** This was the shim's whole risk and the reason
   H1 was its first real test; the ruled shape uses the tool's own `<target>/<UDID>` convention
   directly, so nothing here depends on undocumented tolerance. **Recorded as closed rather than
   deleted, because it is the clearest thing the ruling bought**: it removed a runtime unknown rather
   than deferring it.
5. **`SweepWork` is `return nil` today, and quince#731 makes reconciliation SCHEDULED — nothing
   sequences the two.** That issue already records that a future sweep must tell a live job's
   `working/` from an orphan. **Under this rung there is no `working/` on zfs at all**, so such a
   sweep either finds nothing — harmless — or, if generalised to *job scaffolding*, reaches the
   sentinel in the **parent** dataset and clears a live job's dirty marker, making a resumable head
   look clean. A different failure from the one quince#731 is written against, and a quieter one.
   `reconcile.go` is
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
9. **RULED 2026-08-08 (Operator, relayed on quince#736): SHAPE 1 — keep proper `zfs rollback`.** This
   question blocked PR 3 and no longer does. Reset on zfs rolls the dataset back to the newest
   `@quince-*` snapshot, exactly as D3 and D6 have it; **answer C is accepted as a refusal**, with the
   message naming C's remedy and not B's (G5c). That is a capability regression against today's
   unconditional reset — `RemoveAll` on a directory quince owns, refusable by nothing — and it was
   **taken knowingly** rather than inherited.
   **The mitigation this spec first offered for C was withdrawn before the ruling and stays
   withdrawn**: *"do nothing, the head is resumable"* is the **retry** path, and reset exists for when
   the head is **bad**.
   **Shape 4 — a delta restore-from-snapshot that would have dropped the `rollback` verb entirely —
   was DECLINED, and the reasoning is kept because it was sound.** It made reset unconditional, was
   immune to C by construction, and would have shrunk the host hand-edit to a pure deletion. It lost
   because **reset is a rare escape hatch** — the normal path after a failed job is a retry that
   resumes from the dirty head — and shape 4 paid for that rarity with a second restore mechanism,
   quince-implemented tree-diff logic, a non-atomic operation, and **a delta size nobody measured**.
   `zfs rollback` is O(1), atomic, and the filesystem doing exactly the right thing. The full-copy
   variant and question 7's unfiltered view were likewise not taken; question 7 stands on its own
   merits and is **not** part of this ruling.
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
| **3** | **zfs writes into the dataset root and commits by snapshot.** The target move, the deleted seed path, the deleted exchange, the new commit sequence, the move of BOTH per-device sidecars to the parent (the work sentinel and the commit journal), `isDirty` becoming a backend method, the pre-change `latest/`/`working/` cleanup, and reset. One PR because splitting it ships a broken intermediate: writing to the root while still exchanging would exchange the tree with itself, and switching the write path without switching `isDirty` leaves reset silently reporting nothing to do. **D7's browse guard and D8's `.zfs` skips are in this PR, not a later one** — both become live-tree hazards in the same commit that moves the tree. | G1, G2, G3, G5, G5c, G6, G7, G8, G9, G10, G12, G13, G14 |
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
