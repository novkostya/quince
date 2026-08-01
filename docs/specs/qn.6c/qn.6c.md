# qn.6c — storage becomes plural

**Goal.** A user with more than one place to keep backups — a fast internal pool and a
removable disk, say — can declare both to quince and aim a device's backup at either, with the
cost of that choice (a first backup to a new storage is a **full** transfer) stated before it
starts.

**Status: APPROVED, and ALL FOUR GAPS ARE RULED — code PRs may open.** The spec was approved on
quince#381 (`quince-review[bot]` as the independent read, `@novkostya` as code owner on the two
canon files). The rulings were relayed by architect session `arch1` on
[quince#378](https://github.com/novkostya/quince/issues/378) — a **relay of an out-of-band
Operator decision**, which is the citable record, not a forge artefact the Operator authored.
First rung of the multi-storage epic (`roadmap.md`, *"Storage as a first-class entity"*, captured
2026-07-22), scoped 2026-07-31.

**The four rulings, and where each one landed:**

| gap | ruled | vs. what this spec recommended |
| --- | --- | --- |
| **1** — `Version.backend` | **keep it, and REDOCUMENT it** — `Version.backend` is *"how this version was made"*, `Storage.backend` is *"what this storage uses now"* | **better than (a).** Two distinct facts that agree permanently, because a storage's backend is immutable by gap 4's own rule — so it is not a denormalized copy awaiting a breaking removal, and the epic's "symptom" framing does not apply to the wire at all |
| **2** — the storage collection | **as recommended**, and the open sub-question settled: `GET /api/storages` stays **device-independent**; `?udid=` is an optional parameter that adds `will_be_full` | *will the next backup be full* is a property of a **(device, storage) pair**, not of a storage; modelling it as a storage property would distort the resource for the storage-cards rung |
| **3** — `QUINCE_BACKUPS` | **(b): HARD RETIRE it.** Every storage declared; no env var, no implicit storage, no fallback | **the architect recommended (a) and was OVERRULED.** See *Rung-ruled decisions* #5 for the dissent and the four costs the rung now carries |
| **4** — `quince-storage.json` | **accepted as written**, including the offsite exclude | the exclude is the load-bearing half: a replicated marker makes two places assert one UUID |

**Each implementing PR flips its `PROPOSED (gap)` block in canon to decided text, citing the
ruling comment** — already required by the *Docs are part of the diff* row.

**Two things are the Operator's and block nothing yet:** writing the staging stand's `storages:`
entry before this rung lands, and reviewing the upgrade note when it appears.

**What the rung is.** Today quince has exactly one `/backups`, one backend probed globally, and
`Version.backend` recorded per version. The epic names that last field as *the symptom*: a
version's backend is really its **storage's** backend. This rung makes storage the entity that
owns a backend, a path and an identity, and makes a backup name one.

**What the rung is not.** It builds no way to *create* a storage. Storages arrive from
`config.yml`, which is why the rung is small enough to be one spec — and why the sharpest
question it answers is *when does a config-declared storage get probed, if it has no creation
moment?*

---

## Boundary

**In scope:**

- `core/internal/config` — a `storage.storages` list in the schema, its validation, and the
  REFUSAL to start when it is absent or empty. `QUINCE_BACKUPS` is retired — there is no implicit
  storage and no fallback (gap 3, ruled).
- `core/internal/storage` — a storage **registry** holding one `Backend` per storage rather
  than one globally; `quince-storage.json` (a new `storageMarker`, modelled on `marker.go`); the
  pre-backup reachability + backend check; one new anchored offsite exclude rule.
- `core/internal/store` — migration `0006_storage`: a `storages` table and `versions.storage_id`.
  **Purely additive, and NOT backfilled** — `null` means *not yet attributed* (ruled), because the
  value is a marker UUID that does not exist until the creation moment.
- `core/internal/backup` — the engine resolves a storage for a job and records it.
- `core/internal/httpapi` + `core/internal/wire` — `GET /api/storages`, `storage_id` on `POST
  /api/jobs`, `Storage` and `Version.storage_id` on the wire.
- `core/internal/demo` — two fixture storages, one of them deliberately unreachable.
- `ui/` — a storage selector on *Back up now*, carrying the full-transfer warning.
- `docs/` — contracts §1/§2/§6, design §5, stack D5/D12.
- **An UPGRADE NOTE — a deliverable of this rung, not an afterthought** (gap 3's ruling). What to
  add to `config.yml`, and that it must be added **before** upgrading or the instance stops
  backing up. Retiring `QUINCE_BACKUPS` means an existing deployment that upgrades without a
  `storages:` key has zero storages; every such deployment today relies on that variable's
  built-in `/backups` default (`config/bootstrap.go:51`) while declaring nothing.

**Out of scope, explicitly — and where each went:**

- **Storage cards on the dashboard** (used/free, backup count) — its own later rung. It needs
  *offline* as a first-class dashboard state, and a card that errors on an unplugged disk defeats
  the case removable media exists for. This rung lands the offline **model** (story 5); the card
  is what consumes it.
- **Add / remove a storage** — **a spike before a spec**, per quince#378. Adding one means
  choosing a backend, and that choice is permanent; removing one is destructive with unruled
  semantics (detach-and-forget vs delete-the-data). Nothing here creates or destroys a storage.
- **`external-readonly` mode** (epic point 6) and **import/migration** (epic point 4). Every
  storage in this rung is `managed`.
- **Folding B2/offsite into the storage abstraction** (epic point 3) — a genuine open fork, and
  this rung takes no position beyond one exclude rule, which it justifies rather than assumes.
- **Continuous reconciliation** (epic point 7). It is blocked on *unreachable* and *artifact
  gone* being distinguishable, which is exactly what story 5 supplies — so this rung unblocks it
  and deliberately does not build it.
- **`zfs-native` mode** (epic point 8) — epic-scale, gated on soak evidence.
- **Onboarding.** The epic says the first storage is created during onboarding; there is no
  onboarding, and this rung must not depend on one. See *Sequencing* below.
- **Per-storage retention and per-storage zfs settings.** See gap 3's second half.

---

## Sequencing — why this comes before onboarding

The epic's first bullet says a storage is *"created (first one during onboarding, Plex-style)"*,
which reads as onboarding-first. It is the other way round: **onboarding needs storage to be an
entity before it can create one.** There is no onboarding today, so building the entity first
costs nothing, and it saves building onboarding twice.

It also keeps this rung inside the program doc's rule that **a rung's goal is provable at rung
close.** A spec that needed a creation UI to demonstrate two storages would be depending on a
future rung's deliverable. This one does not: two storages are two entries in `config.yml`.

**Correction — gap 3's ruling made part of this section false, and it is corrected rather than
left standing.** This section argued the rung takes on **no** dependency on an onboarding that does
not exist. Under the ruling (retire `QUINCE_BACKUPS`; every storage is declared) **a fresh install
has no storage at all until someone hand-edits YAML** — which is that dependency, discharged by
documentation rather than removed.

Stated plainly because the difference matters to whoever builds onboarding: this rung does not
*need* onboarding to be **provable** — G1 still runs on two directories with no creation UI, and
the rung-close rule is still satisfied — but it does leave a fresh install **unusable** until a
human writes a `storages:` entry. That is a real cost the ruling accepted knowingly, and the
mitigation is story 1's **loud refusal** (new G7) plus the upgrade note, not a synthesized storage.

---

## Interface facts — measured in this checkout, not recalled

Per the interface-facts rule, everything the design leans on was read at `main` rather than
remembered. Each is load-bearing for a decision below.

1. **The lifecycle is already parameterised on a root.** `core/internal/storage/layout.go:30` —
   `func deviceDir(backupsRoot, udid string) string { return filepath.Join(backupsRoot, udid) }`
   — and every other path function funnels through it (`latestDir` :51, `workingParent` :54,
   `workingTree` :57, `workSentinel` :60, `nsVersions` :63, `nsVersionDir` :66, `browseRoot` :82).
   `backupsRoot` is a **pure parameter**, never package state.

   **This is why the rung is lighter than the epic's prose implies.** The epic's *"the
   `latest/`/`working/` lifecycle becomes per-(device, storage)"* reads as a redesign of what
   `qn.5b` just unified. It is not: `latest/` and `working/` are already per-`(root, device)`, so
   multi-storage is *multiple roots* and falls out of root **resolution**. `layout.go` needs no
   change at all. What changes is that the three callers holding a single `backups string` field
   — `namespaceBackend.backups` (`namespace.go:23`), `zfsBackend.backups` (`zfs.go:64`) and
   `Manager.backups` (`subsystem.go:39`) — become a set with a resolution step in front.

2. **There is exactly one funnel.** `buildStorage` (`core/cmd/quince/live.go:139-159`) is the
   only place `storage.Select` and `storage.NewManager` are called, from `live.go:73` (serve) and
   `admin_cmd.go:42` (the read-only admin CLIs). The registry replaces one function's body.

3. **`QUINCE_BACKUPS` is bootstrap env with no YAML key at all.**
   `core/internal/config/bootstrap.go:15` and `:51` (`orDefault(vals["QUINCE_BACKUPS"],
   "/backups")`). `StorageConfig` (`core/internal/config/schema.go:27-31`) carries `backend`,
   `zfs` and `retention` — **no path**. So a storage *location* in `config.yml` would be the
   first, and contracts §6's *"Everything else: `/data/config.yml`"* has never had to arbitrate
   a path before. This is gap 3.

4. **zfs intent is declared config-side only, never probed.** `probe.go:30-31` —
   `wantZFS := opts.Backend == BackendZFS || (opts.Backend == "auto" && (opts.ZFSParent != "" ||
   opts.ZFSHookCmd != ""))`. The three namespace tiers *are* probed from the filesystem
   (`probeNamespace`, `probe.go:63`). A per-storage backend therefore needs per-storage zfs
   settings or an explicit rule that only one storage may be zfs; gap 3 takes this up.

5. **A file at the storage root is invisible to every existing walk.** The root of `backupsRoot`
   is enumerated in exactly two places and both skip non-directories:
   - `scanJournals` — `journal.go:96-98`, `if !e.IsDir() { continue }`
   - `Manager.reconcileUDIDs` — `reconcile.go:156-161`, `if e.IsDir() && validUDID(e.Name())`,
     double-guarded (and `validUDID`, `layout.go:25`, is `^[A-Za-z0-9-]{8,64}$`, which a dotted
     filename fails anyway).

   `Scan` starts at `latestDir`/`nsVersions`, one level deeper. `Verify` (`verify.go:36`) runs
   against a *tree* dir, deeper still, and **has no notion of a foreign entry at all** — no
   allowlist, no rejection of unknown names; its only enumeration, `hasNonEmptyShard`
   (`verify.go:146-157`), ignores everything that is not a hex-shard dir.

   **So quince#378's open spec question — "can `quince-storage.json` be added to today's
   `/backups` without perturbing committed versions?" — answers YES on the read paths**, and by
   measurement rather than by argument. The one place it does *not* answer yes is (6).

6. **A root-level file WOULD be synced offsite, and that is a real defect if unaddressed.**
   `AnchoredFilterRules` (`offsite.go:16-21`) returns exactly two rules:
   ```
   - /<subdir>/*/working/**
   - /<subdir>/*/versions/**
   ```
   Neither matches `/<subdir>/quince-storage.json`. Story 2 adds a third rule and *Rung-ruled
   decisions* #2 says why exclusion rather than inclusion is right.

7. **`versions` has no column naming where the artifact lives.** `store/migrations/0002_versions.sql`
   — `backend` is the closest thing and it is a *type*, not a *location*. This is the modeling
   error the epic names, in its most concrete form.

8. **`quince-version.json` is the shape to copy.** `marker.go:17` (`MarkerName`), `:24-35` (ten
   fields), `:39-48` (a self-checksum — sha256 over the marshalled struct with `Checksum`
   emptied, so it is self-contained with no companion file), `:62-67` (`os.Remove` before write,
   so it never truncates a possibly-hardlinked inode). `ReadMarker` returns `ErrMarkerCorrupt` on
   a hash mismatch **or an empty checksum** (`:86`).

   Note the tiering: `quince-version.json` lives *inside* a version and rides into `latest/`,
   while `.quince-work.json` (`worksentinel.go:9-15`) and `.quince-commit.json` (`journal.go:11-14`)
   live in the **device** dir so they never enter a committed version. A storage marker is a
   **third tier**, above the device dir, and has no precedent.

---

## Design

### The model

A **storage** is a named, identified place quince commits versions to. It owns:

- a stable **id** (a UUID, written into the storage itself — see identity below),
- a **path**,
- exactly one **backend**, chosen once and immutable thereafter,
- a **reachability** state, which is a fact about now and not a property.

A **version** belongs to exactly one storage. A **device** may have versions on several. The
`latest/` + `working/` + `versions/` lifecycle is unchanged and runs independently under each
storage root, per interface fact 1.

**Incremental is scoped to `(device, storage)`.** A delta can only be taken against the previous
backup on the same storage, so the first backup of any device to any new storage is a full
transfer. This is not a new mechanism: the seed already decides `kind` authoritatively from
whether `working/` was seeded from an existing `latest/` (contracts §2, `Version.kind`), and
under a second root there is no `latest/` to seed from. What the rung adds is **saying so
first** — story 8.

### Identity, and the creation moment a config-declared storage does not have

quince#378 asks where the backend gets probed when a storage arrives from `config.yml` with no
creation moment, and notes that *immutable after creation* and *probed at startup* disagree about
a dataset remounted as something else.

**They stop disagreeing once "creation" is defined by the storage's own contents rather than by a
UI event** — but the storage's contents are **not** enough on their own, and the first version of
this rule got that wrong. The rule:

> **The first startup that finds a reachable path with no `quince-storage.json` at its root **and
> no row in `storages` for that config entry** is that storage's creation moment.** quince probes
> the backend then, writes the marker, and never probes for selection again.
>
> **A reachable path with no marker, for a storage the DB already knows, is a MISSING MEDIUM** —
> refuse, exactly as a mismatched marker refuses. Never re-create, never re-probe.
>
> Every later startup and every pre-backup check **reads** the marker and **compares** — it does
> not re-select.

**The second clause exists because the first clause alone silently writes backups to the system
disk** (quince#381 review, architect `arch1`). An unmounted mountpoint is a reachable path with no
marker: `/mnt/backup-disk` is an ordinary readable, empty directory on the root filesystem while
the disk is unplugged, because the marker is on the disk and the disk is not there. Under
contents-only the rung would then (1) probe and get **`copy`** rather than the disk's `zfs`,
(2) write a marker with a **new UUID** into the mountpoint, (3) have that marker **shadowed**
rather than deleted the moment the real disk mounts over it — so it returns on the next unmount —
and (4) accept backups **onto the root filesystem**, filling the system disk while the user
believes they are going to the removable one.

*Wrote to the mountpoint instead of the mount* is a classic, it is silent, and this rung's own
first sentence names *"a fast internal pool and a removable disk"* as the motivating case. It also
breaks two rules the *Rule check* clears: the backend comes out `copy` by a **silent downgrade** —
precisely what gap 4 point 2 refuses when a marker is present and mismatched, with no equivalent
guard on the absent-marker path — and the storage reports itself created, reachable and healthy
while the medium is absent, which is a **state-honesty** failure.

**The discriminator was already in the rung and unused.** Story 1 puts a `storages` table in the
DB and *Rung-ruled decision 4* makes `storage_id` a marker UUID, so **the DB already knows whether
a storage has ever been created.** *Path reachable, no marker* stops being one state and becomes
two.

**Keyed on the config entry's `name`, not its `path`** — stated because it is load-bearing. A
path moves (a disk remounted elsewhere); the name is the stable user-facing label. When the medium
**is** present the marker is authoritative and reconciles the rest: a known `storage_id` found at a
new path is a **move**, recorded, not a new storage.

**The residual, stated rather than engineered away.** The very first startup after declaring a
storage whose medium is absent — no marker *and* no DB row — is still indistinguishable from a
genuine creation. The rule is therefore accompanied by a written requirement: **declare a storage
with its medium present.** Closing it mechanically would mean recording an expected filesystem or
device id at creation and comparing it; that is deliberately **not** in this rung, and is noted in
*Known gaps* as the thing to cost if the residual ever bites.

**One mitigation is taken, because it costs nothing and the residual is the one silent case left.**
Creation is a **loud, user-visible event** — logged and surfaced with the path, the probed backend
and the reason, never an ordinary startup line. A storage that quince believes it just created is
exactly the thing a user must be able to contradict.

That gives all three of the epic's requirements at once: selection at creation (point 2),
immutability (bullet 2), and a health check before each use (point 2 again) — without inventing
a creation UI this rung has ruled out of scope.

It also makes the remount case a **refusal** rather than a silent downgrade. If the marker says
`zfs` and the probe now says `copy`, quince does not back up to that storage and says exactly
why. Silently accepting the new backend would write versions the marker misdescribes; silently
refusing would be a fallback. Neither is permitted.

**And it supplies the companion to `backend: "unknown"` that story 5 was already reaching for.**
*Known storage, medium absent* is a distinct state from *never yet reached*, which is the
`unreachable` vs `artifact gone` distinction story 5 exists to make representable — the pieces
were in the rung and the rule simply had not used them.

### Reachability is checked, never assumed, and never queued

Per epic point 5, an unreachable storage is an honest *"can't right now"*, not a background
retry — queuing fights the assisted model (stack D13, and `CLAUDE.md`'s *no unattended mode, no
auto-retry*). An unreachable storage is **listed** with `reachable: false`, must **not** block a
backup to any other storage, and a backup aimed at it fails immediately and actionably.

This is also the distinction epic point 7 is blocked on: *storage unreachable* and *artifact
gone* become separately representable, so the continuous-reconciliation rung becomes buildable.
It is not built here.

### What does NOT change

- `layout.go` — not one line (interface fact 1).
- The commit lifecycle, the journal phases, roll-forward, the atomic exchange. A second root
  runs the same machinery.
- `quince-version.json` and its contents.
- Retention semantics, beyond acting per storage.

---

## Contract and design changes — ALL FOUR RULED 2026-07-31

**The four `PROPOSED (gap)` blocks are in canon and are now ruled** (relayed on quince#378; the
summary table is at the top of this document). The sections below are kept **as they were put to
the Operator** — question, options, recommendation — with the ruling recorded against each, rather
than rewritten into the answer.

**Kept rather than collapsed on purpose.** A ruling is only meaningful against the option set it
chose from, and one of these went **against** the recommendation. Editing the alternatives away
would leave a future reader unable to see what was decided, or that anything was — which is the
same reason `decisions/0006` annotates rather than rewrites.

**Each implementing PR flips its block in the canon doc to decided text**, citing the ruling.

### Gap 1 — `Version` gains a storage; does `backend` stay? (contracts §2)

`Version.storage_id: string` is added — a field addition, non-breaking by the header's own rule.
The question is `Version.backend`, which the epic names as the symptom of the modeling error.

- **(a) Keep `backend`, denormalized.** It is already on the wire, clients render it, and it is
  still a true statement about the version (a version made on a zfs storage *is* a snapshot). The
  modeling error is fixed where it actually bites — the DB gains `versions.storage_id` and the
  backend is read from the storage — and the wire keeps a convenience copy.
- **(b) Remove `backend`; clients join through `Storage`.** Cleaner, and breaking.

**Recommendation: (a)**, with the cost stated plainly rather than hidden: it leaves the epic's
symptom visible on the wire on purpose, and a future rung that wants (b) pays a breaking change
then rather than this rung paying it now for a field nothing is yet confused by.

`Version.browse_root` also stops being universally `/backups/<udid>/…`. That is a **documentation**
change, not a shape change — it is already computed per request from the root (`browseRoot`,
`layout.go:82`), so only the literals in the contract text are wrong once storages are plural.

> **RULED — keep it, and REDOCUMENT it.** Not (a) exactly. The field stays and its **meaning
> changes**: `Version.backend` is *"how this version was made"*; `Storage.backend` is *"what this
> storage uses now"*. Two different facts that agree permanently, because a storage's backend is
> immutable by gap 4's own rule.
>
> **That is better than what this spec asked for**, and worth understanding rather than just
> implementing: (a) framed the field as a denormalized copy kept for compatibility, carrying an
> implied future breaking removal. The redefinition makes it a **distinct, permanently true
> field** that never needs removing — so the epic's *"symptom"* framing turns out not to apply to
> the wire at all, only to the DB, where `versions.storage_id` fixes it.
>
> **And the load-bearing argument is NOT compatibility, which was checked rather than assumed.**
> Gap 3's ruling retired an implicit path precisely because *"keep it for compatibility"* is how
> permanent cruft arrives, so *"keep `Version.backend`"* deserved the same scrutiny and got it.
> It survives on a different footing: **a version can outlive its storage.** Remove-a-storage is
> out of this rung but coming, and *detach-and-forget* is one of its candidate semantics — once the
> storage row is gone, `storage_id` dangles and the backend is **not recoverable by join**. The
> field is therefore not derivable in all futures, which makes it a genuine fact about the version
> rather than a cached copy of somebody else's. No appeal to compatibility anywhere in that.
>
> **Work this adds:** contracts §2 gets the **redefinition**, not merely the new field.

### Gap 2 — a `Storage` object, and how a job picks one (contracts §1 and §2)

```jsonc
Storage: {
  "id": "01J...",              // the UUID from quince-storage.json; stable across replug
  "name": "pool",              // from config.yml; the label the UI shows
  "path": "/backups",
  "backend": "zfs" | "reflink" | "hardlink" | "copy" | "unknown",  // unknown = never yet reached
  "default": true,
  "reachable": true,
  "unreachable_reason": null   // set when reachable is false; shown, never thrown
}
```

```
GET  /api/storages                                    → {storages: Storage[]}
POST /api/jobs {udid, transport, storage_id?, retry_of?}  → 202 Job
```

Three sub-questions, each with a recommendation:

1. **`storage_id` omitted.** → the storage marked `default`, of which there is exactly one.
   Recommended over a 422 because it keeps every existing single-storage client working
   unchanged, which is what makes this additive.
2. **The chosen storage is unreachable.** → **409**, not 422. It is a state conflict the user can
   act on (plug the disk in), not a malformed request — the same reading `POST
   /api/devices/{udid}/pair` already uses for *not present on USB*. A 202-then-queue is
   explicitly refused.
3. **Unknown `storage_id`.** → 404, matching unknown-device.

`Job` gains `storage_id` (non-null, the resolved concrete storage — never the word "default",
exactly as `transport` stores the resolved `usb`/`wifi` and never `auto`).

**And the full-transfer claim, which is a contract surface rather than UI copy.** quince#378 is
explicit that *"the first backup to a new storage is always full"* is a state-honesty surface
that must be said **before** the transfer starts. The server owns that answer, because only it
knows whether a `(device, storage)` pair has a prior version. Proposed: `Storage` carries, per
the device being asked about, `"will_be_full": true` — or, if the review prefers not to make
`GET /api/storages` device-scoped, a `?udid=` query parameter that adds the field. **Flagged as
the shapeliest sub-question in this gap**, because both forms work and they differ in whether a
storage list is a device-independent resource.

> **RULED as recommended, and the sub-question settled: the `?udid=` parameter.** All three
> sub-answers stand — omitted `storage_id` → the `default` storage; unreachable → **409**; unknown
> → **404**; `Job.storage_id` records the resolved concrete storage, never `"default"`; no queue.
>
> **`GET /api/storages` stays device-independent.** `will_be_full` appears only when `?udid=` is
> passed. The reason is worth keeping: *will the next backup be full* is a property of a
> **(device, storage) pair**, not of a storage — and the storage-cards rung that follows this one
> wants a device-independent list. Putting a pair fact on the storage resource would distort it
> for the next rung's benefit.

### Gap 3 — storages in `config.yml`, against `QUINCE_BACKUPS` (contracts §6)

Contracts §6 says bootstrap env is *"deployment topology only"* and *"Everything else:
`/data/config.yml`"*. A storage's path is topology by that reading — but there are now N of them,
env vars hold lists badly, and D12 requires every setting to be in `config.yml` and UI-editable.
Interface fact 3 shows there is no storage location in YAML today at all.

- **(a) A list in YAML, with `QUINCE_BACKUPS` as the implicit fallback.**
  ```yaml
  storage:
    backend: auto        # unchanged; describes the IMPLICIT storage only
    zfs: {...}           # unchanged; describes the IMPLICIT storage only
    retention: {...}     # unchanged
    storages: []         # empty = synthesize one implicit storage at QUINCE_BACKUPS
  ```
  A non-empty list carries `{name, path, default}` per entry, and the entry's backend is
  **discovered and frozen at its creation moment** (see *Identity* above) rather than declared.
- **(b) Retire `QUINCE_BACKUPS`; every storage is declared.** Breaks every existing deployment,
  and leaves a fresh install with no storage until an onboarding that does not exist.

**Recommendation: (a).** It is the only option under which this rung does not depend on a future
rung, and every deployment in the field keeps working with an unchanged `config.yml`.

> **RULED (b) — HARD RETIRE `QUINCE_BACKUPS`. This recommendation was OVERRULED.** Every storage is
> declared: no env var, no implicit storage, no synthesized fallback. A middle path was offered
> after the ruling — migrate once at first start, writing a `storages:` entry from the current
> `QUINCE_BACKUPS` into `config.yml` and never reading the env var again, feasible because
> `config/service.go:89-110` already writes the file atomically — and the Operator considered it
> and held (b).
>
> **The dissent was then RETRACTED on the Operator's reasoning** — it had priced a field of
> deployments that does not exist, and an implicit env-var path is permanent cruft bought for
> nothing. *Rung-ruled decisions* #5 carries the full record. What survives the retraction is
> **G7's inversion**, on a **state-honesty** footing rather than a compatibility one: a silent
> zero-storage start that looks healthy is the outcome to prevent.

**The second half of this gap, which (a) does not settle:** `backend`, `zfs` and `retention` are
today singular keys describing the one storage. Under (a) they describe the *implicit* storage.
Whether they move **into** each list entry now — per-storage retention, per-storage zfs settings
— or stay global until a later rung is a separate call. **Recommendation: keep them global for
this rung** and let a declared entry inherit them, because per-storage zfs settings only start
mattering when a second zfs storage exists, and this rung cannot create one. Interface fact 4 is
why this cannot simply be assumed: zfs intent is config-declared and never probed, so a
config-global zfs setting silently applies to every declared storage.

**Restart.** A change to `storage.storages` requires a restart in this rung, because the backend
is selected and probed at startup and the registry holds one live `Backend` instance per storage.
D12 permits a restart *"unless the spec says why"* — this is the spec saying why. It is recorded
as rung-ruled below rather than buried here.

### Gap 4 — `quince-storage.json`, and the offsite rule it needs (design §5)

The marker, modelled on `marker.go` (interface fact 8) and living at the **storage root**:

```jsonc
{ "storage_id": "01J...", "backend": "zfs", "created_at": "...",
  "app_version": "...", "checksum": "..." }     // self-checksummed, no companion file
```

Three decisions inside this one gap:

1. **May it be written into today's `/backups`, which already holds committed versions?**
   **Recommended yes**, on the measurement in interface fact 5 rather than on argument: it is
   invisible to `scanJournals`, `reconcileUDIDs`, `Scan` and `Verify`. It sits **above** every
   device dir and therefore above every version, so *never mutate a committed version* is not
   touched — a near-miss stated deliberately rather than waved through. Written idempotently on
   first startup after upgrade.
2. **A present-but-mismatched marker.** → **refuse**, loudly, and do not back up to that storage.
   Not a re-write, not a guess. This is the remount guard.
3. **Offsite.** Interface fact 6 measures that the file would be **synced**. Recommended: add a
   third anchored rule, `- /<subdir>/quince-storage.json`.

   **Why exclude and not include** — epic point 3 leans that offsite is a *replication* of a
   storage, not a storage. If the marker rides along, the replica claims its source's identity,
   and two places assert one UUID. That is precisely the question the file exists to answer.
   Excluding it keeps the epic's open fork open; including it would quietly decide it.

> **RULED — accepted as written, including the creation-moment fix and the offsite exclude.**
> Creation requires **no marker AND no `storages` row**; an absent marker for a storage the DB
> already knows is a **missing medium** and refuses — never re-probe, never re-create — keyed on
> the config entry's `name` rather than its `path`.
>
> **The exclude is named as the load-bearing half**: a replicated marker makes two places assert
> one UUID, which silently decides the epic's still-open point 3. The creation residual stays
> carried by a written requirement plus the loud creation event, with the mechanical option in
> *Known gaps* #4.

---

## Story 5's contract surface — specified before code, per the 2026-08-01 ruling

The ruling on quince#435 settled **behaviour** and said explicitly that the wire shape was not
decided and belonged here. Three things follow, and the first is a defect the ruling introduced.

### "probe" now means two different operations, and a CI gate forbids one of them

The 2026-07-31 rulings say, twice, that a missing medium must **"never re-probe, never re-create"**,
and **G5b asserts it**: *"it must NOT re-probe, must NOT write a second marker, and must NOT accept
a backup."* The 2026-08-01 ruling says **"reachability may change without a restart (re-probe on
demand)"**. Read with one meaning of the word, the second repeals the first and G5b becomes
unbuildable.

They are different operations and the spec needs two words:

| | **backend selection probe** | **reachability check** |
| --- | --- | --- |
| asks | *which backend does this filesystem support?* | *is this storage present right now?* |
| when | **once**, at the creation moment | any time, on demand |
| writes | the marker | **nothing** |
| cost | filesystem feature detection | a stat |
| on a bare mountpoint | **FORBIDDEN** — it is what would let an unplugged disk be re-selected and re-created | **required** — it is how the answer becomes `missing_medium` |

**G5b stands unchanged and is not weakened.** What it forbids is the *selection* probe, and the
reason survives the new ruling intact: re-selecting a backend for a path whose marker is gone is
what turns an unplugged disk into a new empty storage. The reachability check writes nothing and
selects nothing, so it cannot cause that failure.

**Proposed wording change, so no later session has to rediscover this:** the two RULED blocks keep
their text — they are rulings — and this table is the definition they are read against. New text
says **"never re-select a backend"** where it means the creation probe.

### PROPOSED (gap): the re-probe surface does not exist in the contract

*"Plug the disk in and press the button"* is the ruled behaviour. **There is no button.** Nothing in
contracts §1 re-checks a storage's reachability; the nearest neighbour is `POST
/api/devices/rescan`, which is about muxers.

```
POST /api/storages/{id}/recheck   → 200 {storage: Storage} | 404 unknown storage
```

Recommended over the alternatives, with the reasons, because this is a contract surface and the
choice should be arguable rather than assumed:

- **against re-checking inside `GET /api/storages`** — it would make a list read do filesystem I/O
  per storage on every poll, and the UI polls. A read that can be slow and can change state is the
  wrong shape.
- **against a global `POST /api/storages/recheck`** — the user plugged in *one* disk. Per-storage
  keeps the action's blast radius equal to the user's intent, and the response carries the one
  object that changed.
- **`200` rather than `202`** — the check is a stat, not a job. A `202` would imply something to
  poll, and this rung has no queue by ruling.
- **it never creates or selects.** An unreachable storage that becomes reachable is `opened`, or it
  is `missing_medium` / `backend_mismatch` and stays refused. The one thing this endpoint must not
  do is the thing G5b forbids.

**Not built until ruled.**

### PROPOSED (gap): `unreachable_reason` — a code, prose, or both

Gap 2's ruled shape carries `"unreachable_reason": null`, and the 2026-08-01 ruling requires
`missing_medium` and `unreachable` to be **distinguishable by text**. Prose alone cannot be
branched on; a code alone cannot be shown. Recommended: **both, with the code authoritative.**

```jsonc
"reachable": false,
"unreachable_code": "missing_medium" | "unreachable" | "backend_mismatch",
"unreachable_reason": "the path is readable but carries no quince storage marker …"
```

The alternative — one field the UI maps to copy — was considered and is rejected on this project's
own precedent: the muxer surface already ships state plus human detail, and the log/UI split is
where invented copy would otherwise appear. **Both fields are null when `reachable` is true**, never
absent, for the reason `Version.storage_id` is null-not-omitted: a present null is a fact, an absent
key is a version-skew question.

**Not built until ruled.**

---

## Stories

Each is independently checkable.

1. **Storages exist as entities, and every one of them is declared.** `storage.storages` in the
   config schema, validated; exactly one storage is `default`. **`QUINCE_BACKUPS` is retired** —
   no implicit storage, no synthesized fallback, no env var read. A `config.yml` with no
   `storages:` key **refuses to start**, names the key and prints what to write (G7); the env var
   is gone rather than merely unused (G7b). **Ships with an upgrade note** — see the boundary.

   *(Rewritten by gap 3's ruling. This story previously read "an empty list synthesizes exactly
   one implicit storage at `QUINCE_BACKUPS`" and promised behaviour identical to today. The
   Operator ruled the opposite; both agent seats had recommended the fallback and both were wrong,
   for the same reason — the argument priced a field of deployments that does not exist. See
   *Rung-ruled decisions* #5 for the record and the retraction.)*
2. **Each storage carries its identity.** `quince-storage.json` written at the creation moment,
   self-checksummed; read and compared at every startup and before every backup; a mismatch
   refuses; a corrupt marker refuses. The new anchored offsite exclude rule ships with it.
   **Creation requires no marker AND no `storages` row for the config entry** — an absent marker
   for a storage the DB knows is a **missing medium**, which refuses rather than re-creating.
   Creation is a loud, user-visible event naming the path, the probed backend and the reason.
3. **The storage layer resolves a root.** `buildStorage` returns a registry of one `Backend` per
   storage; `Manager` resolves per `(device, storage)`. `layout.go` is unchanged — a diff
   touching it is a finding against this story.
4. **Versions know where they live.** Migration `0006_storage` adds a `storages` table and
   `versions.storage_id` — **purely additive, no row rewritten**. `storage_id` is **nullable**, and
   `null` means *not yet attributed*: the value is a UUID from the storage's `quince-storage.json`,
   which does not exist until the creation moment (story 2's second half), so there is nothing
   truthful to backfill with. `Version.storage_id` crosses to the wire; `browse_root` resolves under
   the version's own storage root.

   *(Rewritten twice. It first said "backfilling every existing row to **the implicit storage**",
   which gap 3's ruling falsified — there is no implicit storage — and the nullability ruling
   falsified again: there is no backfill at all. Both corrections are recorded rather than quietly
   applied, because this line described a migration nobody could write.)*
5. **Unreachable is a state, not an error.** `GET /api/storages` lists an unreachable storage with
   `reachable: false` and a reason, and a backup to a *different* storage is unaffected.
6. **A backup names a storage.** `POST /api/jobs {storage_id?}` — resolved to `default` when
   omitted, 409 unreachable, 404 unknown; `Job.storage_id` records the resolved concrete storage.
7. **The pre-backup check.** Reachable, the marker present, and the probed backend still matching
   it, before any transfer begins. Three distinguishable, actionable failures — **unreachable**,
   **missing medium** (path readable, marker gone, DB row present) and **backend mismatch** — never
   a downgrade and never a re-create. `missing medium` is the state that keeps an unplugged disk's
   mountpoint from being treated as an empty new storage.
8. **The cost is stated before it is paid.** For a `(device, storage)` pair with no prior version,
   the API reports that the next backup will be full, and the committed version's `kind` is
   `full`.
9. **The user can choose.** A storage selector on *Back up now*, showing each storage's name,
   backend and reachability, with the full-transfer warning attached to the option that carries
   it — before the button is pressed, not after.
10. **The acceptance case.** One device backed up to storage A, then to storage B: both commit
    and verify, the second is a full transfer and said so first, and neither storage's committed
    versions are perturbed.

---

## Gates

Beyond `make gates`.

| id | story | what it proves | where |
| --- | --- | --- | --- |
| **G1** | 10 | Two temp roots; the replay-transcript harness backs one device up to each. Both commit and verify. `latest/` under A is byte-identical before and after B's commit. **This is quince#378's gate, and it needs no second disk — two directories on one filesystem are two storages.** | CI |
| **G2** | 8 | A `(device, storage-B)` pair with no prior version reports full **before** start, and the committed version's `kind` is `full`. Asserted on the API answer *and* on the marker, so a UI-only claim cannot pass it. | CI |
| **G3** | 2 | Marker round-trip; a hash-mismatched marker refuses; a backend-mismatched marker refuses. **And the invisibility claim, asserted rather than assumed — all four walks, not two**: with `quince-storage.json` present at a fixture root, `reconcileUDIDs`, `scanJournals`, `Scan` and `Verify` each return exactly what they returned without it. `Scan` and `Verify` are invisible *by construction* (they start a level or more below the marker), so their assertions are regression guards against a future refactor that moves either up a level, not discoveries. Stated because the *Rule check* row claims a measurement, and G3 named two walks while the row credited four (quince#381 review). | CI |
| **G4** | 2 | `PathExcluded("<subdir>/quince-storage.json", AnchoredFilterRules("<subdir>"))` is true, and the two existing rules still behave — the D5a anchoring hazard is re-proven, not trusted. | CI |
| **G5** | 5 | An unreachable storage (a root made unreadable mid-run) is **listed** with a reason, and a backup to another storage completes. Nothing queues. | CI |
| **G5b** | 2, 7 | **The unmounted-mountpoint gate.** A storage is created at a fixture root; the root is then emptied to simulate the medium being absent (marker gone, path still readable, DB row present). quince must report **missing medium** and **refuse** — it must NOT re-probe, must NOT write a second marker, and must NOT accept a backup. Asserted on all three, because the failure this guards is silent and its symptom is a full system disk. | CI |
| **G5c** | 2, 7 | **The nonexistent-path gate — the case G5b structurally cannot reach** (quince#415). A declared path that **does not exist at the moment of decision** must refuse, and must leave **nothing** behind: asserted on the refusal, on `os.Stat` of the path, and on `ls -A` of its **parent**. **And on the PROBE NOT BEING CALLED**, which is what pins the ordering rather than the symptom — `probeNamespace` does `os.MkdirAll`, so any arrangement where a probe runs before the guard re-creates the bug by a new route, and a stricter `reachable()` alone would not. Driven against the real image: `exit 1`, zero entries created. | CI |
| **G6** | 4 | A pre-`0006` database **holding a committed version** is migrated, and **every pre-existing field comes back byte-for-byte** with `storage_id` **NULL** — not `""`, not fabricated. Plus additive-only (every prior column same name/type/notnull/default, exactly one new) and idempotency. **The property is "the migration ran and the data is untouched", not "the migration ran"** — contracts' cheapness clause does not reach data at rest, so this is the gate that limit exists for. Ran against the staging stand's **real** database on a copy, 19 versions, all fields and `missing` flags identical. *(Was "every existing version resolves to **the implicit storage**" — falsified by gap 3's ruling, which retired it, and by the nullability ruling, which removed the backfill the line assumed.)* | CI |
| **G7** | 1 | **REPLACED by gap 3's ruling — it now asserts the opposite of what it did.** A `config.yml` with **no `storages:` key REFUSES TO START**, names the missing key, and prints what to write. Asserted on all three: the refusal, the key named, and the remedy printed. **The refusal is the whole of the safety here.** The old G7 asserted an implicit storage synthesized from `QUINCE_BACKUPS` and byte-identical behaviour to `main`; the ruling retires that env var, so the old gate is not merely wrong, it is **unwritable**. The one outcome this must never have is a **silent zero-storage start that looks healthy** — an instance that comes up, shows no error, and quietly cannot back anything up. | CI |
| **G7b** | 1 | `QUINCE_BACKUPS` is **gone**, not merely unused: set it to a valid path, start with a declared `storages:` list pointing elsewhere, and assert every version lands under the **declared** root. A retired variable that is still silently honoured somewhere is the failure this gate exists to catch. | CI |
| **G8** | 9 | The selector renders both storages, disables the unreachable one with its reason, and shows the full-transfer warning on the storage that has no prior version. | ui-e2e |
| **G9** | 10 | **OWED — hardware, Operator, a lab day.** A real device backed up to two real storages, the second a genuine full transfer of tens of gigabytes over the real transport. G1 proves the mechanism; only hardware proves the cost. | lab |
| **G10** | all | `make privacy-check REF=origin/main...HEAD TEXT=<body>` over diff, commit messages and PR text. Storage **paths** are this rung's sharpest privacy surface — see *Rule check*. | host |

**G9 is declared owed with its owner named, per state honesty.** No PR in this rung claims the
hardware leg until it has run.

---

## Fixtures

- **Two-root transcript fixture** — the existing replay transcripts under
  `core/internal/backup/testdata/transcripts/` replayed against two roots. No new device data;
  the fixture is the *harness* change, not new captures.
- **`quince-storage.json` fixtures** — valid, hash-corrupt, and backend-mismatched. Synthetic
  UUIDs.
- **A pre-`0006` DB fixture** — for G6. Built from the existing migration chain, not copied from
  any real deployment.
- **Demo provider** — two storages, one deliberately unreachable, so the demo deploy exercises
  story 5 without needing a disk to unplug.

No fixture in this rung contains a real UDID, a real path, or anything from the private layer.

---

## Rule check

Written before building. Every rule this rung touches **or comes near**, including near-misses.

| rule | how this plan complies |
| --- | --- |
| **Don't improvise architecture** | The four decisions that are not this rung's go to the Operator as `PROPOSED (gap)` blocks with options and recommendations. Each is a recommendation, explicitly not a decision. Nothing is built on a pending proposal. |
| **Contracts are stop-and-ask** | Gaps 1, 2 and 3 are contract surfaces (§2, §1/§2, §6); gap 4 is storage semantics (design §5). **No code lands before the verdicts.** |
| **Never mutate a committed version** | **The rung's sharpest near-miss, and it is answered by measurement rather than by argument.** `quince-storage.json` is written into a root that already holds committed versions. It sits above every device dir, hence above every version; interface facts 5 and 6 name the four walks it is invisible to and the one rule that would otherwise have shipped it offsite. **G3 asserts all four** — two by observation (`reconcileUDIDs`, `scanJournals`) and two as regression guards over paths that are invisible by construction (`Scan`, `Verify`), which is stated in G3 rather than left as an unqualified "measured". `latest/` is still changed only by the marker-guarded exchange, under each root independently. |
| **No silent caps or fallbacks** | A backend/identity mismatch **refuses**; it never downgrades to what it found. An unreachable storage is listed with a reason, never hidden and never queued. The `copy`-backend warning path is unchanged and now fires per storage. |
| **State honesty** | The full-transfer claim is stated **before** the transfer, from the server's knowledge of prior versions, and G2 asserts it against the committed marker so a UI-only claim cannot pass. `backend: "unknown"` on a storage never yet reached means quince does not know — not a guess. G9 is declared owed with an owner rather than quietly skipped. |
| **Config tidiness (D12)** | Storages live in `config.yml` with no secrets, and gap 3's ruling makes this **stronger** than the spec first proposed: with `QUINCE_BACKUPS` retired, nothing about where a backup lands is implied by an env var invisible in the file. **Two near-misses, both declared.** (1) A storage-list change needs a **restart**, which D12 permits only if the spec says why — gap 3 says why, *Rung-ruled decisions* #1 records it. (2) **`storages` has no usable default and that is deliberate** — D12 says every setting has a sane default, and there is no sane default for *where the user's backups live* now that the implicit one is retired. The honest form of "no default" is a **refusal that names the key and prints what to write** (G7), never a guess and never a silent start. |
| **No UI-only state** | The selector renders `GET /api/storages`; reachability and the full-transfer claim are both server answers. The UI stores neither. |
| **Privacy is a commit-time gate** | **The sharpest surface in this rung is storage paths**, because the whole feature is about naming places on the Operator's own machines. Every path in this spec, in the fixtures, in the demo provider and in the config examples is `/backups` or an obvious placeholder. No lab topology, no real mount point, no dataset name from the private layer enters any of it. G10 sweeps diff, commit messages and PR text before every push. |
| **Secrets discipline** | **Near-miss by adjacency.** This rung adds no secret — a storage path is not one — and must not acquire secret machinery. Backup passwords are untouched; nothing new reaches argv, env or logs. |
| **Subprocesses** | No new subprocess pattern. The zfs hook is unchanged; per-storage means the existing invocation runs under a different root, not a different shape. |
| **Every hardware bug becomes a fixture** | Anything G9 finds becomes a replay fixture before it is fixed. |
| **Docs are part of the diff** | contracts §1/§2/§6 and design §5 get their `PROPOSED (gap)` blocks in the same PR as this spec; each is flipped to decided text in the PR that implements it, alongside stack D5/D12. |
| **Coverage declared** | Every code PR carries `go test -cover` plus an explicit known-untested list. Expected standing entries: the hardware-only paths (G9) and the zfs-hook branch under a second storage, which no CI box can exercise. |
| **A rung starts from a spec** | This document, reviewed before any code exists. |
| **A rung's goal is provable at rung close** | G1–G8 all run in CI or ui-e2e at rung close; none depends on a later rung. The one thing that cannot is G9, declared owed rather than claimed. The onboarding dependency the epic implies is removed by *Sequencing*. |
| **Approver ≠ author** | Implementer authors; the architect reviews and merges. Unchanged. |

---

## Rung-ruled decisions

Recorded per the gap protocol — rung-local, inside the boundary, changing no contract surface
beyond the four declared above.

1. **A storage-list change requires a restart, in this rung.** The backend is selected and probed
   at a storage's creation moment and the registry holds one live `Backend` per storage; picking
   up a new storage without a restart means building live registry reconfiguration, which is
   strictly more than this rung. D12 allows a restart when the spec says why; this is that. It is
   recorded here rather than only inside gap 3 so it is visible to anyone reading the decisions
   rather than the proposals.

2. **The offsite exclude is a decision this rung makes, not one it defers.** Interface fact 6 is a
   measured defect the moment story 2 lands: without a third rule the marker is replicated, and a
   replica inherits its source's UUID. Excluding it is the smallest choice that keeps epic point
   3's fork — *is offsite a storage or a replication of one?* — genuinely open. Including it would
   decide that fork silently, which is what the gap protocol forbids.

3. **Every storage in this rung is `managed`.** The epic's `mode` (`managed` | `external-readonly`,
   point 6) is not modelled — not even as a field defaulted to one value. A field with one legal
   value invites a reader to assume the second is nearly there; it is not, and it belongs to the
   external-readonly rung with the semantics that make it mean something.

4. **`storage_id` is a UUID from the marker, not the config `name`.** A name is a label the user
   may change and a path is a thing that moves on replug; only a UUID written into the storage
   answers *"is this the same storage?"*. This is epic challenge 1 taken as given rather than
   re-litigated.

5. **NOT rung-ruled — an OPERATOR ruling recorded here with its dissent, because the rung must
   carry its consequences.** Gap 3 was ruled **(b): hard-retire `QUINCE_BACKUPS`.** Every storage
   is declared; no env var, no implicit storage, no synthesized fallback.

   **The architect recommended against it, was overruled, and then RETRACTED the dissent on the
   Operator's reasoning.** Recorded in full because the retraction is the useful part.

   The objection was *"this breaks every deployment in the field"* — and **there is no field.**
   There is one instance, the Operator owns it, and the cost is editing one YAML file once. The
   argument had silently priced a population of installs that does not exist. Against that, an env
   var with a built-in default that quietly conjures a storage is a **permanent implicit path**:
   cheap to add now, expensive to remove later, and load-bearing by the time anyone wants it gone.

   > *"I am the only user atm and I see no reason to accumulate backward-compatibility garbage like
   > Windows."* — Operator

   **So the ruling is right on the merits, not merely the Operator's to make.** The argue-then-defer
   record (quince-devlog#49) asks which seat was right; the answer is available now rather than
   after an incident, and it is the Operator. An earlier draft of this entry carried a *"what would
   settle who was right"* clause — **struck**, because it framed as pending evidence something the
   reasoning already settles.

   **What it buys:** every storage is explicit, and nothing about where a backup lands is implied
   by a deployment variable invisible in `config.yml`.

   **Two things survive from the dissent, and neither is a compatibility argument:**

   - **G7's inversion is mandatory** — a config with no `storages:` key must **refuse loudly**,
     name the key and print the remedy. Not to protect a field, but because a silent zero-storage
     start that *looks healthy* is a **state-honesty** failure. That reason is stronger than the
     compatibility one it replaces.
   - **The staging stand still needs its `storages:` entry written before this rung lands** — one
     edit, the Operator's, blocked by nothing.

   Two further consequences stand and the rung carries both: the *Sequencing* section's claim that
   this rung depends on no future onboarding is **partly false** and is corrected there rather than
   left standing, and an **upgrade note** is a named deliverable in the boundary.

   **The generalisation, worth more than the instance:** an argument that reasons about *"every
   deployment"* on a project with one deployment is not conservative, it is imaginary — and it costs
   a permanent implicit path to buy nothing.

---

## Known gaps and open questions

1. **The four `PROPOSED (gap)` blocks** — gaps 1, 2, 3 and 4 above. All four are open until the
   Operator rules. They are tracked in the devlog `progress.md` open-questions list per the gap
   protocol.
2. **The shapeliest sub-question, called out so review does not skip it:** whether the
   full-transfer claim makes `GET /api/storages` device-scoped (a `?udid=` parameter) or is
   carried elsewhere. Both work; they differ in whether a storage list is a device-independent
   resource, and that is a judgement about the API's shape rather than about this rung.
3. **Not a gap, recorded so a later rung does not rediscover it:** epic point 7's continuous
   reconciliation becomes *buildable* the moment story 5 lands, because *unreachable* and
   *artifact gone* stop being one state. It is deliberately not built here.
4. **RULED, not open — an unreachable storage is a LISTED STATE, not a refusal to serve.**
   Operator ruling 2026-08-01 on quince#435; the full text is the RULED block in design §5. Raised
   because story 5's first sentence *is* this question while the merged `buildStorage` answered it
   the other way — `Resolution.OK()` admits only `created` and `opened`, so `unreachable` and
   `missing_medium` both stopped the daemon. Right for one declared storage, wrong for several.

   **quince serves in every case**, with reachability as data. The default unreachable → serve, and
   a job naming no `storage_id` is **refused with a reason naming the default**, never redirected.
   All storages unreachable → serve. `missing_medium` and `unreachable` get the **same behaviour
   and different text**. Reachability may change **without a restart** (re-probe on demand); the
   storage *list* still needs one, per decision 1. The one surviving hard refusal is a config
   declaring **no storages at all** — G7, unchanged.

   **The invariant that makes serving safe: a storage whose `Resolution` is not `OK()` never
   accepts a job.**

   **Slices 5, 6 and 7 are unblocked.** Two implications to carry rather than rediscover:
   `Resolution` is now a *current* fact, so anywhere it is cached the cache is a claim about the
   past; and `slotFor`'s `(Slot, bool)` seam gains a second *reason* for `!ok` — declared but not
   reachable now, versus not configured — to be distinguished in the message, not the control flow.

   **Still not decided:** the wire shape of `GET /api/storages` (field names; reason as code or
   prose). Contracts territory, and it belongs in the story 5 spec reviewed before code.

5. **The creation residual, left open on purpose.** The very first startup after declaring a
   storage whose medium is absent has neither a marker nor a `storages` row, so it is
   indistinguishable from a genuine creation. Carried by a **written requirement** — *declare a
   storage with its medium present* — plus the loud creation event, rather than by a mechanism.
   **What to cost if it ever bites:** recording an expected filesystem or device id at creation and
   comparing it before accepting the path. Deliberately out of this rung; recorded here so the next
   session finds the option already named instead of rediscovering the case.

---

## PR slicing

Each code PR **flips its `PROPOSED (gap)` block in canon to decided text**, citing the ruling
relay on quince#378.

| PR | claim | state | hardware |
| --- | --- | --- | --- |
| 1 | this spec + the four `PROPOSED (gap)` blocks in canon | **merged** (quince#381) | no |
| — | **rulings** — no code PR opened before this | **done** 2026-07-31 | — |
| 1b | this amendment — the rulings recorded, G7 inverted, *Sequencing* corrected, story 1 rewritten | — | no |
| 2 | stories 1 + 3 — the **declared** config list, the registry, the **loud refusal**, the upgrade note; flips contracts §6 (G7, G7b) | — | no |
| 3a | story 2, first half — the marker **as an artifact**: round-trip, checksum and backend-mismatch refusals, the offsite exclude (G3, G4) | — | no |
| 4a | gap 1's **`backend` redefinition** — contracts §2 only, no code | — | no |
| 4b | story 4's **DB half** — migration `0006`, `versions.storage_id` nullable, the `storages` table (G6). No wire, no contracts | — | no |
| 4c | story 4's **wire half** — `Version.storage_id`; flips the rest of gap 1 in contracts §2. **After 4a** | — | no |
| 3b | story 2, second half — the creation-moment **rule** and its gates: `ResolveStorage`, missing-medium, the unmounted-mountpoint refusal (G5b), the `storages` table access layer and the no-permanent-nulls helpers. **Built and UNWIRED**; design §5 is NOT flipped here. **After 4b** — it needs the `storages` table | — | no |
| 3c | the **wiring** — `buildStorage` resolves the declared storage through `ResolveStorage`, **refuses to serve** on a bad resolution, records the `storages` row and attributes pre-`qn.6c` versions; **flips design §5**. Driven against the real image: created → opened → refused-on-absent-medium (exit 1, nothing written) | — | no |
| 5 | stories 5 + 6 + 7 — the API, reachability, the pre-backup check; flips contracts §1 (G5) | — | no |
| 6 | story 8 — the full-transfer claim, behind `?udid=` (G2) | — | no |
| 7 | story 9 — the selector (G8) | — | no |
| 8 | story 10 — the acceptance case (G1) | — | no |
| 9 | G9 written back into this spec | — | **yes** |

**Story 2 is split because it stopped being one claim, not to route around a review queue**
(ruled on quince#378 after the ordering was reported). The **gap-4 fix made at spec review** — an
absent marker for a storage the DB already knows is a *missing medium* — gave the creation rule a
dependency on the `storages` table, which story 4 creates. That inverted the dependency through
the slice: PR 3 needed what PR 4 built.

It also separated two claims that had been one. **The marker as an artifact** (format, checksum,
mismatch refusals, the offsite exclude — G3, G4) needs no table and no lifecycle. **The marker as a
lifecycle decision** (*when is a creation moment*, missing-medium, G5b) is the part that needs the
row. Before the fix, (2) was "write it on first sight" and rode along with (1); after it, they are
separable claims about separable code, so the split is the slicing catching up rather than a
workaround. **Swapping 3 and 4 would not have unblocked anything either** — story 4 flips
contracts §2, so it needs the code owner exactly as story 2's design §5 flip does.

**PR 1b exists because the ruling falsified parts of a spec that was already merged**, and a
merged spec asserting what the Operator overruled is the defect class this project files most.
It is separated from PR 2 so the correction is not buried inside a code diff — and because PR 2's
own scope grew: gap 3's ruling turned *"synthesize an implicit storage"* into *"refuse loudly, and
ship an upgrade note"*.
