# qn.6d — storage becomes visible: cards on Home, a storage page, and Forget

**Goal.** Someone opens quince and can see where their backups are — how much room is left, how
much is there, and which disk is not plugged in — and can remove a disk they no longer use without
being afraid the button deleted their backups.

Rung issue: quince#443, Operator-scoped 2026-08-01. **The IA ruling this spec builds on lives on
that issue and is cited rather than re-litigated:** no third nav tab, `Devices` is renamed `Home`,
and storage lives there beside devices. What is new here is the *how* — what the daemon must learn
to measure, where Forget's seam is, and the two contract decisions that are not this rung's to
take.

**It runs after `qn.6c`, whose model it consumes.** Both of the issue's stated blockers cleared
before this spec was written: quince#435 (an unreachable *default* is a listed state) ruled and
closed 2026-08-01, and `qn.6c` itself closed 2026-08-02 with all four gaps ruled. `qn.6c`'s one
ask of this rung — `name` optional, defaulting to the path — is **already discharged**
(`core/internal/config/schema.go:105`), so nothing here depends on it.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/storage/` | per-storage capacity and counts; whatever Forget's seam turns out to be |
| `core/internal/store/` | a per-storage version count and a per-storage device count — **both new** |
| `core/internal/wire/` | `Storage` gains space and counts (gap A) |
| `core/internal/httpapi/` | the Forget surface (gap B) |
| `core/internal/demo/` | the fixture storages gain the new fields |
| `ui/src/components/Sidebar.tsx` | `Devices` → `Home`; `HardDrive` is handed to storage |
| `ui/src/features/storage/` (new) | the card, the details page, the Forget dialog |
| `ui/src/routes/router.tsx` | `storage/:name` |
| `ui/src/features/settings/ConfigEditor.tsx` | reconcile the read-only line that already defers here |
| `docs/` | contracts §1/§2; `ui.design.md` principle 4; `design.md` §8 |
| `ui/e2e/` | `story7-storage.spec.ts`, and `story8-forget-storage.spec.ts` for Forget |

**Out of scope**, per quince#443 and not revisited: **Add a storage** (`qn.6e`); **changing a
storage's path**; **estimated-full projection and a usage sparkline** (both need periodic
free-space samples that nothing collects); **per-version physical bytes on the card** (quince#442 —
the field is wrong on every backend, so there is no honest ratio to render); **prune / retention
settings**; **marker-based discovery**; **Forget a device**.

**Also explicitly out: live registry reconfiguration.** Interface fact 4 — it does not exist,
`qn.6c` rung-ruled that a storage-list change needs a restart, and this rung does not build it.
Gap B ruled that Forget does **not** need it: Forget is a config mutation, so the restart stays and
is surfaced. **General config live-apply is a SEPARATE RUNG** — see the gap B section — and nothing
here builds toward it.

**And out: fixing quince#569 and quince#570.** Both were found while writing this spec and both are
in this rung's blast radius (interface fact 9). They are contract-surface defects with their own
decisions, and folding them into a UI rung would bury them.

---

## Interface facts — measured in this checkout at `0ba993d`, 2026-08-02, not recalled

**1. `Storage` carries NO capacity, and no counts.** `core/internal/wire/objects.go:193-238` and
its mirror `ui/src/lib/types.ts:229-247` are exactly `{id, name, path, backend, default, reachable,
unreachable_code, unreachable_reason, will_be_full}`. **A fill bar is new wire, not new rendering** —
which is why gap A is a contract gap and not a UI detail.

**2. Free space exists in the tree and is in the wrong package, and it is only half the number.**
`core/internal/backup/sampler.go:99-107`:

```go
func statfsFree(path string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}
```

It is **unexported**, it lives in `package backup` (device-facing), and it returns `Bavail` only.
The card says *"1.2 TB free of 3.6 TB"*, so **total is a number nothing in the tree computes**.
`grep -rn 'unix.Statfs' core/` returns this function and nothing else; `storage.reachable()`
(`creation.go:178-189`) is a `Stat` + `Open`, deliberately not a capacity probe.

**3. No per-storage count exists, and the index for it already does.**
`store.CountUnattributedVersions` (`store/storages.go:108`) is the only `COUNT(*)` over `versions`
in the tree. `ListVersions(udid)` has no storage predicate and `UDIDsWithVersions()` groups by
`udid` with none either. The migration already ships
`CREATE INDEX idx_versions_storage ON versions (storage_id, created_at)`
(`store/migrations/0006_storage.sql`), so the query is cheap — it is simply unwritten. **The
`Registry` interface the Manager consumes** (`storage/subsystem.go:39-48`) needs the method too, not
just `*store.Store`.

**Two counting semantics are already split in the tree and this rung must not pick by accident.**
`Slot.hasVersionFor` (`storages_api.go:59-75`) **excludes** `missing` rows; `UDIDsWithVersions`
deliberately **includes** them, because *"a device whose artifacts all vanished still has a history
the user should see, rendered dead"*. Rung-ruled decision 3 settles it.

**4. The registry is FIXED at construction; only reachability moves.** `NewManager`
(`subsystem.go:99-110`) **panics** on an empty slot list. The only runtime mutation anywhere is
`RecheckStorage` replacing **one existing slot in place** by id (`storages_api.go:111-122`), which
carries this comment — the assumption Forget is about to break, written down before anything broke
it:

> Re-find by id rather than trusting idx across the unlocked window. Nothing else mutates the list
> today, and a position captured before an unlocked gap is precisely the assumption that stops
> holding the moment something does.

The restart rule is stated in four places: `handlers_storages.go:27-28`, `live.go:205-207`,
`contracts.md` §6, and `qn.6c.md:734-739` (rung-ruled decision 1).

**5. Removing a storage from the config file ALREADY WORKS, and already has the refusal G6 wants —
for a different reason.** `PUT /api/config` is a full-document replace, and
`config.Service.Replace` (`config/service.go:262-268`) runs `CheckStorages` before writing:

```go
	if req := CheckStorages(c, nil, nil); !req.OK() {
		msg := "at least one storage must be declared — saving this would leave quince unable to back anything up, and it would refuse to start"
```

So **you may already remove any entry but the last**, and the last is a `422` on path `storage`.
On a single-storage install the only storage *is* the default, so **half of G6 is standing code**.
It does **not** cover forgetting the *default* when two storages are declared, which is the half
this rung owes.

**6. A partial round-trip through `PUT /api/config` silently wipes keys.** `handleConfigPut`
(`handlers_config.go:29-54`) decodes into a **zero-valued** `config.Config`, so an omitted key
becomes the Go zero value rather than its default. `ConfigEditor.tsx:46` and `:129-130` carry the
standing warning. **For Forget the failure is concrete: a flow that reconstructs the storage list rather
than splicing a fetched one drops `zfs:` and `retention:` on every surviving entry.** Story 6 owes
a test that a Forget preserves the survivors byte-for-byte.

**7. After a Forget, `recheck` on the forgotten storage returns STALE state rather than a 404.**
The `Refresher` closure reads `cfgSvc.Current().Storage` **live** (`live.go:209-218`), so a deleted
entry makes `refresh(name)` return `ok=false` — and `RecheckStorage` then reports the slot it
already had (`storages_api.go:101-107`) rather than saying the storage is gone. **Gap B ruled this
the CORRECT behaviour**: recheck reports **runtime truth**, and the card marks it pending with
*"forgotten · restart to apply"*. A `404` would be a lie while the process still serves the disk.

**8. Storage identity is THREE different keys, and they do not agree on which storages have one.**

| identity | value | where |
| --- | --- | --- |
| wire / API `id` | the marker UUID | `Slot.StorageID` → `wire.Storage.ID` (`storages_api.go:39`) |
| config + DB key | `name` | `store.StorageRow` PRIMARY KEY; `Refresher` resolves **by name** |
| path | informational only | `StorageRow.Path` — *"last known; informational, never an identity"* |

**The re-probe path already picked one, and said why**, which is worth reading before gap B is
decided — `live.go:209`: *"It re-resolves by NAME, which is the identity the config carries."*

**9. An UNREACHABLE storage has an EMPTY id — measured, and it produced a RULING that this rung now
inherits.** A targeted `go test` against `ResolveStorage`, this checkout:

```
unreachable    → resolution="unreachable"    storage_id=""
missing_medium → resolution="missing_medium" storage_id=""
```

`st.StorageID` is set on **only** two of six resolutions (`creation.go:119`, `:171`);
`ResolutionMissingMedium` puts the known UUID in the prose reason and not in the field. Filed as
**quince#570**, with its consequence for the existing Re-check button.

The same run confirmed **quince#569**: `live.go:298` does `UnreachableCode: string(state.Resolution)`,
so an unmounted disk emits `unreachable` where `contracts.md` §2, `wire/objects.go:221`,
`ui/src/lib/types.ts:243` and `qn.6c.md:583` all declare `path_unreachable`; `corrupt_marker` is in
no union at all.

**Both are latent today and stop being latent here**, because a storage card is the first surface
that branches on the code to pick an icon and a remedy, and Forget is the first surface that must
address a storage the user cannot reach.

**quince#570's ADDRESSING half was ruled on 2026-08-02 at `20:02:51`, and it lands ON this rung
rather than beside it.** *"The API addresses a storage by its config `name` … and `qn.6d`'s Forget
the same. Not the marker UUID."* Three consequences this spec must carry, all from the ruling text:

- **`POST /api/storages/{id}/recheck` becomes `POST /api/storages/{name}/recheck`** — an existing
  route changes key. Deferred out of this rung, then **built by quince#610** once open question 4
  measured what the old key cost: an idless storage made the client send `/api/storages//recheck`,
  which path-cleans to a `307` and a silent `404`.
- **The cost is accepted, not discovered later, and belongs in `contracts.md` beside the route:**
  renaming a storage changes its API address, and because `name` defaults to the path (quince#504),
  **editing a path renames the storage implicitly.** The key is stable-by-configuration, never
  stable-forever.
- **`Storage.id` on the wire is EXPLICITLY NOT settled, and the ruling hands it to this rung** —
  *"whether `id` should still be emitted at all, and what it means when empty, belongs with
  `qn.6d`'s card work."* Carried as open question 3.

**10. The demo provider FABRICATES storages, and that bounds what ui-e2e can prove.**
`core/internal/demo/provider.go:157-214` returns two hand-built `wire.Storage` values — `internal`
(`/backups`, `reflink`, default, reachable) and `shuttle` (`/mnt/shuttle`, `unknown`, permanently
unreachable, `missing_medium`) — with stable **non-empty** ids that the live code never produces for
an unreachable storage (fact 9). `will_be_full` is emitted only when `udid != ""`, so a
device-independent fetch returns `null` for both.

**Consequence, and it corrects a gate in quince#443 rather than discovering it in a slice:** G1's
claim — *two storages on one disk must not each claim its free space as their own* — **cannot be
proven in ui-e2e**, because nothing in the demo shares a filesystem and both numbers are invented.
A green e2e there would answer *does the card render two numbers*. G1 is therefore split; see
Gates.

**11. The UI has no storage surface yet, and one deliberate placeholder points here.** There is no
`features/storage/`, no route, and `useStorages.ts:78` is the only reader of `GET /api/storages` —
always device-scoped. `ConfigEditor.tsx:103-111` renders a read-only line whose comment names this
rung by number:

> Editing a storage is quince#443's surface (`qn.6d` — storage becomes visible), and a read-only
> list here would be a second place to keep in step with it.

**12. `Sidebar.tsx:6-9` is one `NAV` array rendered two ways** — `flex-row` top bar under `sm:`,
`flex-col` sidebar above it — with `HardDrive` on **Devices**. There is no drawer and no overflow,
so a third entry lands directly on the phone top bar, which is the ruling's reason 1.

**13. The pieces the card needs already exist.** `Progress`
(`ui/src/components/ui/progress.tsx`) takes a **percent**, not bytes, and its fill is `bg-accent`
with **no warn tone** — a nearly-full disk needs a tone variant or a `className` override.
`formatBytes` (`ui/src/lib/format.ts:7`) is decimal SI and returns `"—"` for negative/NaN.
`Badge` (`components/ui/badge.tsx`) has `neutral | accent | ok | warn | danger`.

**14. `contracts.md` §1's code block is already behind the built API.** It lists `GET
/api/storages` and `POST /api/jobs`, and **omits both `?udid=` and `POST /api/storages/{id}/recheck`**
— they exist only in prose at `:282-284` and `:536`. §2 likewise documents **one** field
`unreachable_reason` where the wire has **two**. Gap A's block corrects §1's listing in the same
diff, because this rung is adding to it.

**15. Go 1.25.0** (`core/go.mod`); `lucide-react` 0.469.0 (`ui/package.json:23`).

---

## Design

### The card

```
/backups                                        [Default]
1.2 TB free of 3.6 TB
[▓▓▓▓▓▓▓▓▓▓▓░░░░░░]  67%
14 backups · 2 devices
```

Mirrors `DeviceCard`'s anatomy (`features/devices/DeviceCard.tsx`) — title, status lines, and the
action slot devices use for *Back up now*, which **stays empty in this rung**. `DeviceCard`'s
`h-full` + `mt-auto` idiom is kept so the two card kinds align in one grid.

Four content decisions, each with its reason, all from quince#443 and reproduced here as the build
target rather than re-argued:

**Free space leads, and it is the one number that does not care about the backend.** `statfs` is
ground truth on zfs, reflink, hardlink and copy alike, where every other size fact currently is not
(quince#442).

**It is not attributed to the storage ON THE WIRE, and the CARD carries no caveat at all** — gap A,
ruled 2026-08-03. Fact 2's `statfsFree` reports the **filesystem**, so two storages that are two
directories on one disk report identical figures; the field names `filesystem_free_bytes` /
`filesystem_total_bytes` say so to an API client. **The card always renders plain *"1.2 TB
free"***, because the conditional wording this spec first proposed cannot be implemented — equal
byte counts do not prove a shared filesystem, and the two fields that would have carried filesystem
identity were offered to the Operator and declined. The accepted cost is written out in the
retirement of rung-ruled decision 2.

**Backend is NOT on the card** — permanent, and it matters, but it is not a glance fact; it is on
the details page. **`copy` is the exception** and keeps a caution pill: a degraded backend is a
degraded mode, and `CLAUDE.md` requires those be surfaced.

**`Default` shows only when there is more than one storage.** On a single-storage install it labels
nothing. This mirrors `StorageSelect`, which already returns `null` below two storages.

**An unreachable card is LISTED, states why, and CLAIMS NO SIZE — while its counts stay.** ~~*and
DATES its counts. Counts come from the DB and were true at last contact; the card must not present
them as current.*~~ **That premise is false and the clause went with it** (quince#588): the counts
**are** the DB — a `COUNT(*)` over `versions` — and the DB is reachable whether or not the disk is,
so they are true now and there is nothing to date. What the card must not claim is **capacity**,
which is why that is `null` rather than `0` on the wire. This is `qn.6c` story 5 made visible and it
is the case removable media exists for — quiet when healthy, loud and self-explaining when the disk
is out. The existing precedent to copy is `VersionList`'s missing row
(`VersionList.tsx:62-76`): dashed, `opacity-80`, a `danger` badge, and **no size claim**.

### The details page

**Status header** — path, backend, reachability, the marker UUID. It is what makes a storage an
object rather than a path. `qn.6c` story 7's three distinguishable failures each
land here with their remedy — **subject to quince#569, which is why the page must branch on the
code and fall back to `unreachable_reason` prose rather than assuming the code is one of three**.

**EACH FACT APPEARS ONCE, and "the header" is all of it rather than the definition list at the
bottom of it.** Path is the subtitle under the title; backend and reachability are badges; the
marker UUID is the one fact nothing else carries, so it is the only `<dl>` row. The first build
rendered path and backend *again* as `<dl>` rows below the badges that already showed them — and in
a two-column grid the leftover `Backend zfs` sat alone against the right edge, so the duplication
read as a layout bug (Operator, on the staging stand, 2026-08-03). Do not restore those rows to
"complete" the list against the sentence above: that sentence is about the header, not about the
grid.

**Space** — the card's bar, larger. No per-version physical until quince#442 is real.

**Device cards, filtered to this storage** — the same component Home uses, with every counter scoped:
last backup *here*, N backups *here*. Two things fall out:

- **`Back up now` on this page means back up to THIS storage.** That is `qn.6c` story 9's selector
  answered by context instead of a dropdown — the job is created with this storage's `storage_id`,
  and G3 asserts on the created job, not on what is rendered.
- **Devices with nothing here are SHOWN, not filtered out**, carrying story 8's warning inline:
  *no backups here yet — the first will be a full transfer*. The action this page exists to make
  easy is *start backing that device up here too*, which is the entire 3-2-1 argument, and it is
  invisible if those devices are hidden.

**Version list, filtered to this storage** — the same `VersionList` the device page uses.
Cross-linked both ways: a row here names its device, a row on the device page names its storage.

**Forget**, at the bottom, destructive styling.

**The action-row rule applies here too.** `StorageSelect.tsx:9-18` records quince#325 — the action
row holds **controls only**, prose goes below it — and notes it has been re-broken once already.

### Forget

**Detach-and-forget. The data is not deleted.** Ruled on quince#443, and the reasoning is the
peer-entity frame: you do not delete a device, you unplug it and its backups stay. Deleting data is
a separate, explicit act that must not share a button with this one.

The confirm text carries the ruling in as many words: *"Forget removes it from quince. The backups
on the disk are not deleted."* Without that sentence a user will assume the button wiped their
backups.

**You cannot forget the default storage. One rule, and it subsumes the other case.** Exactly one
storage is `default`, so on a single-storage install the only storage *is* the default — and
`qn.6c`'s G7 makes a zero-storage config refuse to start, so an unguarded button could brick the
instance. Fact 5 shows the single-storage half is **already enforced** by `Service.Replace`; the
two-storage default case is this rung's to add. To forget the default, make another storage default
first.

**What happens to the versions attributed to it is NOT a UI question, and this spec does not
improvise it.** Rows in `versions` carry `storage_id`; forgetting the storage does not delete them.
Rung-ruled decision 4 settles what they render as; it changes no contract because
`Version.storage_id` is already nullable-with-meaning and already has a stated rule against reading
`null` as "no storage".

### What does NOT change

The job engine, the commit lifecycle, `latest/`/`working/`, the marker, retention, and every
storage invariant. **This rung reads and it deletes one config line.** The one write it performs is
a `config.yml` edit that already has a validated path (fact 5).

---

## Contract and design changes — BOTH GAPS RULED 2026-08-03

Both touched frozen interfaces, so both shipped as `PROPOSED (gap)` blocks in `docs/contracts.md`
(quince#573) and were tracked on the devlog dashboard. **The Operator ruled both on 2026-08-03**,
relayed on quince#443; the blocks are flipped to decided text in the same PR as this section, per
quince#408 and quince#409. The decided text lives in `contracts.md` §1 and §2 and is **cited here,
not restated** — where this section and `contracts.md` disagree, `contracts.md` is right.

### Gap A — `Storage` gains space and counts (contracts §1 and §2) — RULED

**The fields land as proposed:** `filesystem_free_bytes`, `filesystem_total_bytes`, `backup_count`,
`device_count`. The sub-questions were taken as recommended — prefixed names kept, capacity `null`
(never `0`) when unreachable with counts still populated, and counts as properties of the storage
rather than device-scoped.

**One thing the block did not ask, which the ruling settled: the card renders NO filesystem
caveat.** Rung-ruled decision 2 is **retired** — see its entry, which carries the accepted cost in
full. Two storages on one disk will each show the same figure with nothing saying it is the same
space. `filesystem_id` and a `filesystem_shared` boolean were both offered and both declined.

**Consequence for this rung's own gates:** G1a still proves the *wire* claim — two storages on one
filesystem report the same figures — and no longer has a UI half to check, because there is no
conditional wording left to render. G1b is unchanged.

### Gap B — Forget's shape, and the restart question was INSIDE it — RULED

**Forget is a config mutation, not a resource-delete**, via a narrow endpoint:

```
DELETE /api/config/storage/{name}  → 200 {config, warnings, source} | 404 | 422
```

Taken as recommended, on the behaviour grounds rather than on the withdrawn empty-`id` evidence: no
live deregistration (the class `qn.6c` declined stays declined), server-side splicing so a
sibling's `zfs:` / `retention:` keys cannot be dropped, `422` refusing the default, and the restart
**surfaced** through the existing `warnings` channel rather than silently.

**The loose end this spec flagged is ruled too — recheck reports RUNTIME TRUTH, marked pending.**
`POST /api/storages/{name}/recheck` keeps answering for the slot the process is still serving, and
the card carries *"forgotten · restart to apply"*. It is the only answer true under both the current
model and a future live-apply one.

**General config live-apply is a SEPARATE RUNG and explicitly out of `qn.6d`.** The Operator wants
hot add/remove without a restart; it is scoped as project-wide config→runtime propagation with
storage as its first consumer. The measured blocker is the config layer rather than the storage
registry — `config.Service` has no `Apply`, `Reload`, `onChange` or `Subscribe` at all, so
restart-to-apply is the status quo for **every** setting. **Nothing in this rung builds toward it,
and story 8's UI copy must not promise it.**

## Stories

1. **`Devices` is `Home`.** The nav entry reads `Home` with a home icon; `HardDrive` moves to
   storage; `/devices` still resolves so a bookmark does not break. `ui.design.md` principle 4 and
   `design.md` §8 say so too.
2. **Every declared storage is a card on Home**, showing free-of-total, a fill bar, a backup count
   and a device count.
3. **A `copy`-backend storage carries a caution pill** on its card.
4. **An unreachable storage is listed and says why** — never hidden, and it claims no size, because
   capacity is `null` rather than `0` when the disk cannot be read. **Its counts are still true**:
   they are a `COUNT(*)` over `versions` and the DB is reachable whether or not the disk is.

   **The genuinely stale thing already has a field**: whether those versions still exist on disk is
   `Version.missing`, set by reconciliation and rendered as a dead row with no size claim.
5. **A storage has a details page** at `storage/:name` with the marker rendered as a status header.
6. **The details page's device list, version list and `Back up now` are scoped to that storage.**
7. **A device with no versions on this storage is shown there, with the full-transfer warning.**
8. **Forget removes a storage from the declaration and deletes nothing on disk.**
9. **Forget refuses on the default storage and says why** — including the single-storage case,
   where the default is the only storage.

---

## Gates

| id | what it proves | where |
| --- | --- | --- |
| **G1a** | Two storages that are **two directories on one filesystem** report the same `filesystem_free_bytes` / `filesystem_total_bytes`, under those prefixed names. **A wire claim only** — the card renders no caveat (gap A ruling), so there is no UI half to assert. | **CI (Go)** — see below |
| **G1b** | Two declared storages each render a card with free-of-total, a fill bar and counts. | ui-e2e |
| **G2** | The unreachable storage's card is **listed**, states why, and **claims no size** — while its counts are still shown, because they are the DB's answer. The reachable one is unaffected. | ui-e2e |
| **G3** | On a storage details page, the device list, the version list and `Back up now` are scoped to that storage — **asserted on the job the button creates**, not only on what is rendered. | ui-e2e |
| **G4** | A device with no versions on this storage is **shown**, with the full-transfer warning. | ui-e2e |
| **G5** | Forget on a non-default storage removes it from the declaration and **leaves every file on disk** — asserted on the tree, not only on the API. | CI |
| **G5b** | Forget preserves surviving entries' `zfs:` and `retention:` keys **byte-for-byte** (fact 6). | CI |
| **G6** | Forget on the default storage **refuses**, and says why. Asserted on the single-storage case too. | CI |
| **G7** | `make privacy-check REF=origin/main...HEAD TEXT=<file>`. Storage **paths** are this rung's sharpest privacy surface — every screenshot and fixture shows one. | host |

**G1 is SPLIT, and quince#443's version of it could not have passed honestly.** The issue asks
ui-e2e to prove that two storages sharing a filesystem do not each claim its free space. Fact 10
measured that the demo provider **fabricates** both storages and both numbers, and that its two
paths share no filesystem — so a green ui-e2e would prove the card renders two numbers and nothing
about the claim. **G1a moves the real claim to a Go test over two directories in one temp dir**,
where `statfs` genuinely returns the same figures; **G1b keeps the rendering half in ui-e2e**, which
is what ui-e2e can actually answer.

This is `qn.6f`'s recorded lesson applied before it cost anything: *a thing can run and still answer
a narrower question than the one asked.*

**No hardware gate, and that is stated rather than implied.** Every gate above is CI or ui-e2e. **A
real removable disk being pulled and replaced is not proven by any of them and is not claimed.**

---

## Fixtures

**The demo provider's two storages already exist** (`core/internal/demo/provider.go:157-214`) and
are extended, not replaced: the fixture gains free/total and counts, with the **unreachable** one
carrying populated counts and **null** capacity so G2 has the asymmetry to assert. (It gained a
freshness stamp too, and that field was removed again — quince#588; the counts were never stale.)

**New: a two-directories-on-one-filesystem fixture for G1a** — two storage roots under one
`t.TempDir()`, which is the only way to get two storages that genuinely share a filesystem without
a mount.

**No new transcripts.** This rung drives no device.

**A demo fixture must not paper over a live defect again — and this is now a REQUIREMENT rather
than an observation.** Fact 10 is the record of it happening: the fixture's hardcoded non-empty ids
are why story6 passes over quince#570. quince#570's ruling makes the remedy explicit and puts it in
this rung rather than in a follow-up:

> **The fixture must produce what the live resolver produces** for an unreachable storage —
> including an empty `id` if that is what remains true after quince#569 lands.

*"A fixture that fabricates a value the live code never produces makes its gate a lie."* So PR 3's
fixture work is not merely *add the new fields*: the unreachable storage's `id` must match whatever
open question 3 settles, and PR 7's e2e must not depend on a value the daemon cannot emit.

---

## Rule check

Written before building. Every rule this rung touches **or comes near**, including near-misses.

| rule | how this plan complies |
| --- | --- |
| **A rung starts from a spec** | This document, reviewed before any code exists. |
| **Don't improvise architecture** | The two decisions that are not this rung's go to the Operator as `PROPOSED (gap)` blocks with options and recommendations, each explicitly a recommendation. **Nothing is built on a pending proposal**; PR 1 is followed by a declared park. |
| **Contracts are stop-and-ask** | Gaps A and B are both contract surfaces (§1/§2). **No code lands before the verdicts.** |
| **Never mutate a committed version** | **The rung's sharpest near-miss.** Forget is *detach-and-forget* and must touch no tree. **G5 asserts on the filesystem**, not on the API, because an API-only assertion would pass over a deletion. `latest/`, `working/`, `versions/` and every marker are untouched; this rung's only write is one `config.yml` edit. |
| **State honesty** | An unreachable card **dates** its counts instead of presenting them as current, and its capacity is `null` rather than `0`. `backend: "unknown"` stays *quince does not know*. Free space is a **filesystem** fact and gap A's naming exists so the payload cannot imply otherwise. The two defects found while writing this are **filed** (quince#569, quince#570), not quietly worked around. |
| **No silent caps or fallbacks** | `copy` keeps its caution pill. Forget's restart requirement is surfaced in the UI, not hidden behind a card that quietly lingers. An unreachable storage is listed with a reason and never hidden. |
| **Config tidiness (D12)** | Forget is a `config.yml` edit with no secrets and no UI-only state. **Two near-misses, declared.** (1) A storage-list change needs a **restart**, which D12 permits only when the spec says why — `qn.6c` rung-ruled decision 1 says why and gap B decides whether this rung inherits it. (2) Fact 6's full-document replace could drop a surviving entry's keys; **G5b** exists for exactly that. |
| **No UI-only state** | Space, counts, reachability and freshness are server answers; the UI stores none of them. The details page's `Back up now` sends a real `storage_id`. |
| **Privacy is a commit-time gate** | **Storage paths are this rung's sharpest surface** — the whole feature names places on the Operator's machines. Every path in this spec, in the fixtures, in the demo provider and in every screenshot is `/backups`, `/mnt/shuttle` or a `t.TempDir()`. No lab topology, no real mount point, no dataset name from the private layer. `TEXT=` takes a **path** to a body file under `$HOME/scratch/<runner>/`, never inline prose and never a fixed `/tmp` path. |
| **Secrets discipline** | **Near-miss by adjacency only.** A storage path is not a secret; this rung adds none and must acquire no secret machinery. Backup passwords are untouched. |
| **Subprocesses** | None added. The zfs hook is untouched. |
| **Every hardware bug becomes a fixture** | No hardware gate here, so nothing is owed — stated rather than left blank. |
| **Docs are part of the diff** | contracts §1/§2 in PR 1; `ui.design.md` principle 4 and `design.md` §8 in the PR that renames the tab and would otherwise make them false. |
| **Coverage declared** | Every code PR carries `go test -cover` plus an explicit known-untested list. Expected standing entry: the `statfs` failure branch on a path that becomes unreadable between listing and measuring, which no CI box can stage reliably. |
| **A rung's goal is provable at rung close** | G1a–G6 run in CI or ui-e2e at rung close; none depends on a later rung. G7 is a host gate run per PR. |
| **Approver ≠ author** | Implementer authors. **The rename PR touches code-owned canon and needs `@novkostya`** — `ui.design.md` and `design.md` — and an App cannot be a code owner, so it must not be routed to the architect. **PR 1 does not**: its canon is `contracts.md`, which is not an owned path (quince#953). The architect approves the rest. |

---

## Rung-ruled decisions

Rung-local: inside this rung's boundary, changing no contract surface, no storage lifecycle and no
behaviour beyond this rung. Recorded here per the gap protocol.

1. **The details page route is `storage/:name`.** This read *"`storage/:id`, and `:id` is whatever
   gap B rules the identity to be"* until quince#570 was ruled — **gap B no longer decides the
   identity, so a decision that keeps deferring to it points at nothing.** The API addresses a
   storage by its config `name`, so a route keyed on anything else would have to translate, and for
   a storage that never came up there is nothing to translate *from*. Written here so PR 5 does not
   re-derive it. Open question 3 is a different question — whether `Storage.id` is still *emitted* —
   and does not reopen this one.
2. ~~**The card attributes free space to the filesystem in PROSE regardless of gap A's field
   names**~~ — **RETIRED by the Operator's gap-A ruling, 2026-08-03.** This committed the card to
   *"1.2 TB free on this filesystem"* when storages share one and plain *"1.2 TB free"* when they do
   not. **That branch is not implementable with the ruled fields**: equal byte counts do not prove a
   shared filesystem, and no field carries filesystem identity. A `filesystem_id` and a
   `filesystem_shared` boolean were both offered and both declined.

   **The card always says plain *"1.2 TB free"*.** The accepted cost, written here rather than left
   to be rediscovered: two storages that are two directories on one disk each show the same figure
   with nothing in the UI saying it is the same space, and a user may read 1.2 + 1.2 as 2.4 TB. That
   is `qn.6c`'s own G1 fixture. **This is a ruled acceptance — do not "fix" it by reintroducing the
   distinction, and do not file it as a bug.** The wire names stay prefixed regardless, so the
   contract remains honest for API clients even where the card renders no caveat.

   Kept struck rather than deleted, because G1b and the card copy were specified against the retired
   version and a reader comparing them needs to see what changed.
3. **Counts INCLUDE `missing` versions, and the card does not distinguish them.** Fact 3 shows the
   tree already splits on this. `UDIDsWithVersions`' reasoning wins — a version whose artifact is
   gone is still history the user should see — and the card is a glance surface, so a second number
   for *present* versions belongs on the details page if anywhere. `Slot.hasVersionFor` keeps
   excluding them, because *"will the next backup be full"* is a different question and its answer
   depends on a usable artifact.
4. **Forgetting a storage leaves its versions in the DB, attributed and rendered as unreachable —
   never deleted, never re-attributed, never silently hidden.** This follows from *the data is not
   deleted*: a version row whose storage is no longer declared is exactly a version whose artifact
   quince cannot currently reach, which the model already represents. **It changes no contract** —
   `Version.storage_id` stays non-null and keeps its meaning; nothing reads `null` as "no storage".
5. **`/devices` keeps resolving after the rename**, as a redirect to `/`. The nav label is what
   changes; breaking a bookmark to make a rename tidy is a cost with no benefit.

---

## Known gaps and open questions

1. ~~**Gaps A and B** — open until the Operator rules.~~ **BOTH RULED 2026-08-03**, relayed on
   quince#443. The `contracts.md` blocks are flipped to decided text in the same PR as this line,
   and the devlog dashboard's open questions 2 and 3 are cleared alongside it. **The park is lifted
   by that PR landing** — not by the ruling comment, and not by quince#573 having merged.
2. **quince#569 is OPEN. quince#570's ADDRESSING half is RULED and the dependency runs the OTHER
   way from what this section first said.** An earlier draft had gap B's ruling answering quince#570;
   it is the reverse — quince#570 was ruled on 2026-08-02 at `20:02:51`, six minutes before this
   spec's PR opened, and **it constrains gap B** rather than waiting on it. Both candidates in gap B
   are now `{name}`-addressed and the question narrows to *resource-delete versus config mutation*.
   Neither issue is fixed here (see Boundary). What remains open on quince#570 is the
   `ResolutionMissingMedium` field-carrying half and the `Storage.id` question below.
3. ~~**`Storage.id`'s fate is THIS rung's**~~ — **SETTLED, keep-and-document-empty** (quince#582,
   quince#583). `id` is still emitted; it means *the identity minted at this storage's creation
   moment*; **empty means never created**, not *not currently readable*. The decided text is in
   `contracts.md` §2 beside the field.

   **What settled it was a defect, not a preference.** `contracts.md` already promised the id is
   *stable across replug* — and it was not: an unplugged disk resolved with `id: ""` and got its
   UUID back on replug, so the field failed across exactly the transition it exists to survive. The
   fix carries the DB's UUID onto `missing_medium`, which makes the standing promise true rather
   than narrowing it. Addressing keys on `name` regardless (quince#570); `id` is for **attribution**,
   which is what makes the empty case matter — `Version.storage_id` joins on it, and a storage that
   was never created correctly matches no versions.
4. **quince#570's HTTP claim is still unreproduced, and the ruling asks for it.** The resolver
   measurement is solid; *"the claim that the button cannot work is currently an inference from
   `ServeMux` behaviour nobody has watched."* Owed against a running daemon with a genuinely
   unmounted declared path — not by this PR, which builds nothing.
5. **`Progress` has no warn tone** (fact 13). Whether a nearly-full disk turns the bar amber, and at
   what threshold, is deliberately **not** decided here: a threshold with no measurement behind it
   is a guess rendered as a warning, and the projection work that would justify one is out of scope.
   Recorded so a later rung finds the question named.
6. **Not a gap, recorded so a later rung does not rediscover it:** once counts are per-storage, the
   estimated-full projection the epic wants needs only a periodic sample of the same numbers. This
   rung deliberately does not collect them.
7. **The demo cannot produce two storages on one real filesystem**, which is why G1 is split. If a
   future rung gives the demo provider a real temp-dir-backed storage, G1a could move to ui-e2e;
   until then the split is the honest shape.

---

## PR slicing

**The flip clause changed, and the change is recorded rather than silent.** This read *"each code PR
flips its `PROPOSED (gap)` block in canon to decided text"* — the `qn.6c` sequence, where the
rulings arrived after the spec merged. **That is not what happened here.** quince#573 merged on
2026-08-03 carrying both blocks live, the Operator ruled both hours later, and **one PR flips both
blocks before any code opens.** So PRs 3 and 6 implement against decided canon with nothing to flip.

A slicing table is a **status table** (quince#409) — a second part describing the whole, with the
same staleness property and no defence but somebody reading it. That is why this clause is corrected
in the same diff as the flip rather than in a follow-up.

| PR | claim | approval |
| --- | --- | --- |
| 1 | this spec + the two `PROPOSED (gap)` blocks in contracts §1/§2 | **`@novkostya`** |
| — | **rulings on gaps A and B** — 2026-08-03, quince#443 | Operator |
| 1b | **both blocks flipped to decided text**; rung-ruled decision 2 retired; this table corrected. **The park is lifted by this landing.** | **`@novkostya`** |
| 2 | `Devices` → `Home`, the icon swap, and the `ui.design.md` / `design.md` amendments | **`@novkostya`** |
| 3 | `Storage` gains space + counts + freshness; the two new store queries; the demo fixture | architect |
| 4 | the card on Home — stories 2, 3, 4 (G1a, G1b, G2) | architect |
| 5a | the details page — stories 5 and 7, story 8's warning (G4); settles open question 3 in canon | **`@novkostya`** |
| 5b | `Back up now` scoped to this storage — story 6 (G3) | architect |
| 6a | Forget — the endpoint `DELETE /api/config/storage/{name}`; stories 8 and 9 server-side (G5, G5b, G6) | **`@novkostya`** |
| 6b | Forget — the button, the confirm copy, the pending marker | architect |
| 7 | `story7-storage.spec.ts` — the ui-e2e half | architect |

**No status column** — `qn.6c`'s table recorded why, and its one self-exempting cell went stale by
the event it was waiting for. A PR number is immutable; a reader who wants status has the forge.

**THE APPROVAL COLUMN NAMES WHO APPROVED EACH PR, NOT A ROUTING RULE.** The rule is one line:
`.github/CODEOWNERS` owns `CLAUDE.md`, `design.md`, `stack.md`, `ui.design.md` and itself — five
paths, and `docs/contracts.md` is not among them (quince#953). Only row **2** would need
`@novkostya` under it.

**PR 6 split into 6a and 6b for the same reason 5 did, and the table says so at the split rather
than afterwards.** An endpoint and a destructive button are different claims with different proofs —
6a's are three Go gates over the config layer, 6b's is what a user is told before they press it —
and they carry different proofs, because 6a adds a route line to `contracts.md` and 6b touches no
canon. **Neither is code-owned:** `contracts.md` is not an owned path (quince#953), so keeping §1's
listing current costs no approval hop. Fact 14 records what it used to cost — §1's listing was
already behind the built API in two places when this rung started, each an ordinary PR that declined
to pay a hop that no longer exists.

**PR 5 split into 5a and 5b while it was being built, and the table says so rather than keeping the
plan.** `useBackup` is keyed by udid and hooks cannot be called in a loop, so *`Back up now` scoped
to this storage* needs a component per device — a different claim from *the page exists and is
correctly scoped*, and a different approval path at the time, since 5a touched `contracts.md` — then
code-owned, not since (quince#953) — and 5b touched no canon.

**It is recorded because a slicing table is a status table** (quince#409). The split was announced in
5a's PR body and then **dropped out of a review summary** that counted the stories 5a delivered
rather than the ones the rung still owed — which would have taken **G3** with it, the one gate that
asserts on the job the button creates rather than on what is rendered. The arithmetic was right at
every step and the total went wrong. Writing the split into the table is what makes it survive the
next summary.

**Why 3 is separated from 4 rather than folded in.** The ordering test `qn.6c` recorded is *which
one's absence makes the other wrong*, not which is smaller. A card built before the wire fields
exist has to invent them, and gap A's ruling is what says which fields exist. The test still holds
now that it is ruled: PR 4 renders `filesystem_free_bytes` and `backup_count`, so it is wrong before
PR 3 puts them on the wire.

**Why 2 is alone.** It touches two code-owned canon files and nothing else, and the rename is
independently reviewable and independently revertable. Folding it into the card PR would put a nav
rename and a new data surface behind one approval from a seat that must read both.
