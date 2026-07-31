# qn.6c — storage becomes plural

**Goal.** A user with more than one place to keep backups — a fast internal pool and a
removable disk, say — can declare both to quince and aim a device's backup at either, with the
cost of that choice (a first backup to a new storage is a **full** transfer) stated before it
starts.

**Status: PROPOSED — awaiting the spec-review gate.** No code exists. This is the first rung of
the multi-storage epic (`roadmap.md`, *"Storage as a first-class entity"*, captured 2026-07-22),
which has never had rung numbers assigned; this is number one of them. Scoped by the Operator on
2026-07-31 as [quince#378](https://github.com/novkostya/quince/issues/378).

**Four decisions in this rung are NOT the rung's to make.** They touch contracts §1/§2/§6 and
design §5, which are frozen. They are written into those documents as `PROPOSED (gap)` blocks in
the same PR as this spec, and **no code implements any of them before a ruling.** They are
restated in *Contract and design changes* below so this document is readable alone.

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
  synthesis of an implicit storage from `QUINCE_BACKUPS` when the list is empty.
- `core/internal/storage` — a storage **registry** holding one `Backend` per storage rather
  than one globally; `quince-storage.json` (a new `storageMarker`, modelled on `marker.go`); the
  pre-backup reachability + backend check; one new anchored offsite exclude rule.
- `core/internal/store` — migration `0006_storage`: a `storages` table and `versions.storage_id`,
  backfilled.
- `core/internal/backup` — the engine resolves a storage for a job and records it.
- `core/internal/httpapi` + `core/internal/wire` — `GET /api/storages`, `storage_id` on `POST
  /api/jobs`, `Storage` and `Version.storage_id` on the wire.
- `core/internal/demo` — two fixture storages, one of them deliberately unreachable.
- `ui/` — a storage selector on *Back up now*, carrying the full-transfer warning.
- `docs/` — contracts §1/§2/§6, design §5, stack D5/D12.

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
UI event:**

> **The first startup that finds a reachable path with no `quince-storage.json` at its root IS
> that storage's creation moment.** quince probes the backend then, writes the marker, and never
> probes for selection again. Every later startup and every pre-backup check **reads** the
> marker and **compares** — it does not re-select.

That gives all three of the epic's requirements at once: selection at creation (point 2),
immutability (bullet 2), and a health check before each use (point 2 again) — without inventing
a creation UI this rung has ruled out of scope.

It also makes the remount case a **refusal** rather than a silent downgrade. If the marker says
`zfs` and the probe now says `copy`, quince does not back up to that storage and says exactly
why. Silently accepting the new backend would write versions the marker misdescribes; silently
refusing would be a fallback. Neither is permitted.

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

## Contract and design changes — declared, and NOT built until ruled

Four `PROPOSED (gap)` blocks land in canon in the PR that carries this spec. Each states the
question, the options, and a recommendation. **A recommendation is not a decision** — none of
these is implemented before an Operator ruling, per the gap protocol and `CLAUDE.md`.

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

---

## Stories

Each is independently checkable.

1. **Storages exist as entities.** `storage.storages` in the config schema, validated; an empty
   list synthesizes exactly one implicit storage at `QUINCE_BACKUPS`; exactly one storage is
   `default`. A `config.yml` from before this rung produces one storage and behaviour identical
   to today.
2. **Each storage carries its identity.** `quince-storage.json` written at the creation moment,
   self-checksummed; read and compared at every startup and before every backup; a mismatch
   refuses; a corrupt marker refuses. The new anchored offsite exclude rule ships with it.
3. **The storage layer resolves a root.** `buildStorage` returns a registry of one `Backend` per
   storage; `Manager` resolves per `(device, storage)`. `layout.go` is unchanged — a diff
   touching it is a finding against this story.
4. **Versions know where they live.** Migration `0006_storage` adds a `storages` table and
   `versions.storage_id`, backfilling every existing row to the implicit storage. `Version.storage_id`
   crosses to the wire; `browse_root` resolves under the version's own storage root.
5. **Unreachable is a state, not an error.** `GET /api/storages` lists an unreachable storage with
   `reachable: false` and a reason, and a backup to a *different* storage is unaffected.
6. **A backup names a storage.** `POST /api/jobs {storage_id?}` — resolved to `default` when
   omitted, 409 unreachable, 404 unknown; `Job.storage_id` records the resolved concrete storage.
7. **The pre-backup check.** Reachable, and the probed backend still matches the marker, before
   any transfer begins. A mismatch is a distinguishable, actionable failure — never a downgrade.
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
| **G3** | 2 | Marker round-trip; a hash-mismatched marker refuses; a backend-mismatched marker refuses. **And the invisibility claim, asserted rather than assumed**: with `quince-storage.json` present at a fixture root, `reconcileUDIDs` and `scanJournals` return exactly what they returned without it. | CI |
| **G4** | 2 | `PathExcluded("<subdir>/quince-storage.json", AnchoredFilterRules("<subdir>"))` is true, and the two existing rules still behave — the D5a anchoring hazard is re-proven, not trusted. | CI |
| **G5** | 5 | An unreachable storage (a root made unreadable mid-run) is **listed** with a reason, and a backup to another storage completes. Nothing queues. | CI |
| **G6** | 4 | A pre-`0006` DB fixture opens; every existing version resolves to the implicit storage; `browse_root` is unchanged for every one of them. | CI |
| **G7** | 1 | A `config.yml` with no `storages:` key produces exactly one storage at `QUINCE_BACKUPS` and byte-identical behaviour to `main`. The no-regression gate for every deployment in the field. | CI |
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
| **Never mutate a committed version** | **The rung's sharpest near-miss, and it is answered by measurement rather than by argument.** `quince-storage.json` is written into a root that already holds committed versions. It sits above every device dir, hence above every version; interface facts 5 and 6 name the four walks it is invisible to and the one rule that would otherwise have shipped it offsite. G3 asserts the invisibility rather than assuming it. `latest/` is still changed only by the marker-guarded exchange, under each root independently. |
| **No silent caps or fallbacks** | A backend/identity mismatch **refuses**; it never downgrades to what it found. An unreachable storage is listed with a reason, never hidden and never queued. The `copy`-backend warning path is unchanged and now fires per storage. |
| **State honesty** | The full-transfer claim is stated **before** the transfer, from the server's knowledge of prior versions, and G2 asserts it against the committed marker so a UI-only claim cannot pass. `backend: "unknown"` on a storage never yet reached means quince does not know — not a guess. G9 is declared owed with an owner rather than quietly skipped. |
| **Config tidiness (D12)** | Storages live in `config.yml` with defaults and no secrets; the empty-list default reproduces today's behaviour exactly (G7). **Near-miss, declared:** a storage-list change needs a **restart**, which D12 permits only if the spec says why — gap 3 says why, and *Rung-ruled decisions* #1 records it. |
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

---

## PR slicing

| PR | claim | hardware |
| --- | --- | --- |
| 1 | this spec + the four `PROPOSED (gap)` blocks in canon | no |
| — | **rulings** — no code PR opens before this | — |
| 2 | stories 1 + 3 — the config list, the implicit storage, the registry (G7) | no |
| 3 | story 2 — the identity marker and the offsite rule (G3, G4) | no |
| 4 | story 4 — migration `0006`, `Version.storage_id`, `browse_root` (G6) | no |
| 5 | stories 5 + 6 + 7 — the API, reachability, the pre-backup check (G5) | no |
| 6 | story 8 — the full-transfer claim (G2) | no |
| 7 | story 9 — the selector (G8) | no |
| 8 | story 10 — the acceptance case (G1) | no |
| 9 | G9 written back into this spec | **yes** |
