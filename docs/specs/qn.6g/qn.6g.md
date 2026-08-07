# qn.6g — config.yml applies without a restart: one propagation mechanism, all settings, storage first

**Goal.** Someone edits a setting in quince — adds a disk, changes retention, turns encryption
enforcement on — and it takes effect, without restarting the daemon and without having to know that
some settings work that way and some do not.

Rung issue: quince#577, Operator-scoped 2026-08-03 out of `qn.6d`, ruled 2026-08-04. **The ruling
lives on that issue and is cited rather than re-litigated:** one live-apply mechanism on
`config.Service` rather than a per-setting special case, storage as its first consumer, and the
interim *"restart required"* UI notice **declined**. What is new here is the *how* — where the seam
is, what each setting's answer actually is, and the one gap that is not this rung's to take.

**Everything below was measured in this checkout at `ab2d43a`, 2026-08-04.** Two of the issue's own
citations are corrected by that measurement; see interface fact 5.

---

## Why `qn.6g`, and why it runs before quince#591

**The letter.** `qn.6e` is taken by quince#502 — an explicit, still-unscoped placeholder holding
`qn.6c`'s deferrals — and `qn.6f` (quince#462) closed **2026-08-04 at `16:20:05Z`**, hours before
this spec was written. `qn.6g` is the next free letter. quince#591 takes `qn.6h`; it states its own
need for a number and names the same two taken letters.

**The order.** quince#577 first. It discharges a violation that is **live right now**: the UI edits
`config.yml`, nothing applies, and — because the interim notice was declined — nothing on screen
says so. Measured against `CLAUDE.md`'s *no silent caps or fallbacks*, that is a standing defect,
and the ruling records it as knowingly accepted only until this rung lands. quince#591 makes an
already-correct path faster and cheaper to operate; it fixes nothing that is currently wrong.

**They barely touch.** quince#591 rewrites the zfs **write lifecycle** (`seedWorking`, the exchange,
the helper's `seed` verb). This rung rewrites **registry membership** (`Manager.slots` and who may
mutate it). The one shared file is `core/internal/storage/subsystem.go`, and in different methods.
Neither blocks the other, and this ordering is **rung-ruled and reversible** — the Operator scoped
both on the same day and either sequence builds.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/config/` | the propagation seam: `Subscribe` + a post-write notify on `Replace` and `ForgetStorage` |
| `core/internal/storage/` | `Manager` learns to replace its slot list; every default-by-position site made safe against a shrinking list |
| `core/internal/backup/` | `Engine` learns to take a new `require_encryption` without a race |
| `core/cmd/quince/live.go` | the appliers, registered at the wiring seam that already exists |
| `core/internal/httpapi/` | no new routes — the existing `PUT`/`DELETE` gain their effect |
| `ui/src/features/settings/ConfigEditor.tsx` | *"restart quince to apply"* stops being unconditional |
| `ui/src/features/storage/ForgetStorage.tsx` | the restart copy goes, and the storage list is invalidated |
| `ui/src/pages/SettingsPage.tsx` | the page intro's *"changes apply on restart"* line |
| `docs/` | contracts §6 gains the per-setting table; design §8; `stack.md` D12's staged-delivery note |
| `ui/e2e/` | one story for the settings copy |

**Out of scope, each with a why.**

- **File-watch pickup of hand-edits** — the other half of D12's *"edited by the UI and by hand
  equally"*. **RULED out of this rung and into its own, 2026-08-04**, rather than descoped silently;
  the block below carries the ruling and the three obligations it lands.
- **`devices.*` live re-supervision.** A netmuxd restart tears a live Wi-Fi backup — that is why
  `muxsup.Spec.Rescan` is `false` for netmuxd (`supervisor.go:122`, design §4) — and Wi-Fi is the
  PRIMARY use case. Applying `devices.*` live means deciding what happens to a running transfer,
  which is a job-semantics question this rung does not need to answer to be useful.
- **`tls.*` on/off and the listen address.** Both need a socket rebind. The address is not even in
  `config.yml` (it is `QUINCE_LISTEN`, bootstrap env — contracts §6), so D12 does not reach it.
- **Fixing the fields nothing reads.** `backup.transport` is quince#654; `sessions.ttl_minutes` is
  quince#656, filed while writing this spec. Live-apply cannot make an unread field take effect, and
  folding the fix in here would let this rung's table claim a key works when nothing consumes it.
- **A new WebSocket event for config changes.** Rung-ruled decision 4.

---

## Interface facts — measured at `ab2d43a`, not recalled

**1. There is no propagation seam of any kind.** `config.Service` (`core/internal/config/service.go:207-213`)
has exactly four methods — `NewService` (:217), `Snapshot` (:229), `Current` (:236), `Replace` (:244)
— plus `ForgetStorage` (`forget.go:47`). No `Subscribe`, no `Apply`, no `Reload`, no watcher, no
setter injection. `Replace` validates, checks storages, marshals, writes atomically, re-`Stat`s for
the mtime, and swaps `s.cfg` under the write lock (:280-284). **Then it returns.** Nothing downstream
learns a write happened. No `fsnotify` dependency exists in `core/go.mod`; the only signal handling
is `SIGINT`/`SIGTERM` for shutdown (`main.go:164`).

**2. The consequence is written in the code, in the words the issue quotes** —
`core/internal/config/service.go:258-261`:

> the UI could remove the last storage, get a 200, and the user would discover backups were disabled
> at the next restart — an acceptance that is silent, which is what `no silent caps or fallbacks`
> forbids and what D12 makes reachable by making the UI the editing surface.

Six more places say the same thing about their own setting: `service.go:208-209`, `schema.go:244`
(*"there is no live config reload in schema v0"*), `schema.go:170`, `auth/service.go:85`,
`forget.go:42-46` (which names this issue), `forget.go:122-123` (the user-facing warning string).

**3. Exactly one thing already reads config live after startup, and it is the model to copy.**
`Manager.SetRefresher`'s closure (`live.go:212-219`) calls `cfgSvc.Current().Storage` on every
recheck and re-runs `resolveSlot`. Everything else is a value copy taken during `buildLiveStack`.
Only `httpapi.Deps.Config` (`deps.go:24`) holds a `*config.Service` past startup.

**4. `resolveSlot` is safe to re-run, and that is not an accident.** `live.go:258`: *"NOBODY CREATES
A STORAGE ROOT. A declared path must already exist."* The backend probe is a lazy closure precisely
so a refusal never reaches `storage.Select`, whose `probeNamespace` would `MkdirAll` the path
(`live.go:246-256`, quince#415). So re-resolving the whole list creates nothing, mutates no tree, and
is idempotent — which is what makes a whole-list apply cheap rather than dangerous.

**5. The default-by-position sites are SIX, not three, and two of the issue's line numbers are off
by four.** Verified by reading each:

| site | line | issue says | correct |
| --- | --- | --- | --- |
| `defaultSlot()` | `subsystem.go:119` | `subsystem.go:115` | ✗ off by 4 (:115 is a comment line) |
| `policyFor()` | `subsystem.go:494` | `subsystem.go:490` | ✗ off by 4 (:490 is `if storageID == ""`) |
| `ResolveChoice()` | `jobstorage.go:115` | `jobstorage.go:115` | ✓ |
| `jobSlot()` | `jobstorage.go:71` | — | not cited |
| `Storages()` | `storages_api.go:36` | — | not cited |
| `renderSlot()` | `storages_api.go:211` | — | not cited |

Two of the six are already safe, **by two different means, which is worth the extra words because
the arithmetic here is load-bearing**: `policyFor` *checks* `len(m.slots) == 0` (:491), and
`Storages()` *ranges* over the slice, so there is no index to be out of. **`defaultSlot`, `jobSlot`
and `ResolveChoice` are the three that are neither** — and `NewManager` panics on an empty list
(:105-107), so today the guard is the constructor. A live remove moves that guarantee from
construction time to every-call time, which is the actual shape of hazard 2. The sixth,
`renderSlot`, is a distinct hazard on the same function and is decision 5's, not this paragraph's.

**6. `renderSlot(idx)` has TWO unlocked windows in front of it, not one.** `RecheckStorage`
(`storages_api.go:154-195`) finds `idx` under RLock, **unlocks**, calls `m.refresh(name)` (filesystem
work), re-locks, re-finds by name, unlocks, then calls `renderSlot(idx)` — which itself runs a
database count **outside** the lock (:204) before reading `m.slots[idx]` (:209). A remove landing in
either window is an out-of-range panic. Its own comment is the prediction:

> Nothing else mutates the list today, and a position captured before an unlocked gap is precisely
> the assumption that stops holding the moment something does.

**This rung is the moment something does.** Note also that the `ok` branch re-finds by name and the
`!ok` branch does not, so a failed refresh carries the stale `idx` all the way to `renderSlot`.

**7. `m.refresh` is written without the lock.** `SetRefresher` (`storages_api.go:17`) assigns the
field with no `mu` held — benign today because it happens once at startup before any reader exists.
Any applier that re-registers it later is a data race on a func value. This rung must not create one.

**8. `require_encryption` is read PER JOB, not at construction.** `live.go:128` copies it into
`backup.Options.Config`, frozen into `Engine.cfg` (`engine.go:122`), and read at `engine.go:475` when
a job starts. So the value the Engine holds is consulted late — which makes it live-applicable by
swapping one field, and makes it a data race unless that swap is synchronized. A job already past
:475 keeps the answer it got, which is correct.

**9. Five settings have NO Go consumer at all.** `backup.transport` (quince#654 — the job's transport
comes from the POST body, `handlers_jobs.go:29`), `sessions.ttl_minutes` (the *vault-unlock* TTL per
`auth/service.go:4-6`; nothing reads it, and `ConfigEditor.tsx:106` labels it *"Session TTL
(minutes)"*, which reads as the login timeout), `automation.staleness_days` and
`automation.reminder_cooldown_hours` (declared for qn.12, `schema.go:248`), and server-side
`ui.theme` (applied by the browser from the PUT response, `ConfigEditor.tsx:35`, so it is already
live). All five are validated and editable.

**This is the fact that shapes the deliverable.** A per-setting table that answers only *live* or
*restart* would have to put four of these in one of those bins, and both would be false. The
`automation.*` pair is **declared debt** and fine; `sessions.ttl_minutes` is quince#654's twin and is
**quince#656**, filed while writing this spec and deliberately not fixed by it.

**10. The demo bounds what ui-e2e can prove here.** `--public-demo` **deletes its config at
startup** (contracts §6, quince#549) and every visitor can `PUT /api/config`, and the demo provider
fabricates its two storages (`demo/provider.go:157-214`) rather than resolving them. So a green
ui-e2e can assert **what the settings screen says**; it cannot assert that a real storage began being
served. That splits G8 from G1–G3 rather than being discovered in a slice — the lesson `qn.6d`
recorded from `qn.6f`: *a thing can run and still answer a narrower question than the one asked.*

**11. Three UI surfaces currently promise a restart, and one deliberately declines to refresh.**
`ConfigEditor.tsx:145` (*"Saved · restart quince to apply"*, unconditional), `SettingsPage.tsx:12`
(*"changes apply on restart (live reload lands later)"*), `ForgetStorage.tsx:75-76` and `:99`. And
`ForgetStorage.tsx:55-58` deliberately does **not** invalidate the storage list, with a comment
saying why — *"it still lists this storage, correctly — the process is still serving it"*. That
comment becomes false in this rung and must flip with it.

---

## Design

### The mechanism — refusal before the write, application after, and application cannot refuse

```go
// config/service.go
type Applier func(old, new Config) []Warning

func (s *Service) Subscribe(name string, apply Applier)   // wiring time only
```

`Replace` and `ForgetStorage` call the registered appliers, **in registration order**, after the
write and after the snapshot swap, holding no lock. Each returns warnings; they are logged and
folded into `s.warnings` so the endpoint's existing `warnings` channel carries them — the same
channel `qn.6d`'s Forget already uses for its restart notice.

**An applier cannot refuse, and this is the load-bearing decision.** By the time it runs, the file is
already written and the file is the source of truth. An applier that could fail the request would
leave the file and the process disagreeing, with a `500` to explain it. So anything that may refuse
runs **before** the write, in `Validate` / `CheckStorages` / `CheckTLS` — which is the shape the
codebase already has, not a new one. An applier that cannot complete its work says so in a warning
and leaves the subsystem on its last-good state, which is D12's own rule for a bad edit.

**Registration is wiring-time only** (`buildLiveStack`), so the applier list is never mutated
concurrently, and interface fact 7's unlocked-write hazard is not recreated.

### Storage — the first consumer

`Manager` gains one method:

```go
func (m *Manager) ApplyStorages(next []Slot) []Warning
```

It takes the whole resolved list under `mu.Lock()` and replaces `m.slots`. The applier in `live.go`
builds `next` with `declaredStorages(cfgSvc.Current().Storage)` + `resolveSlot` — the same two calls
`buildStorage` already makes at startup (interface fact 4 is why that is safe), so a live apply and a
restart **cannot disagree about what a storage is**, which is the property `SetRefresher`'s comment
already argues for.

**Hot add is an append plus a reconcile, and the reconcile is the part that is easy to forget.** A
newly declared disk may already hold committed backups — the adopt path exists. `buildStorage` runs
`storageMgr.Reconcile(ctx)` after `NewManager`; an add that skips it leaves those versions invisible
until a restart, which is the same defect in a new place. Story 5 and G7 exist for this.

**Hot remove is bounded by making every default-by-position site re-read under one lock.**
`defaultSlot`, `jobSlot` and `ResolveChoice` gain the `len(m.slots) == 0` guard `policyFor` already
has; `renderSlot(idx)` becomes `renderSlot(name)` and re-finds under the lock, which removes both of
interface fact 6's windows rather than narrowing them. Removing the current default is **already
refused** with a `422` by `qn.6d`'s ruling, so nothing can promote a different storage by removing
index 0 — that ruling is load-bearing for this rung and is cited, not rebuilt.

## RULED (was `PROPOSED (gap)`): a forget is REFUSED with a `422` while a backup runs on that storage

**Operator ruling 2026-08-06, option (b)**, relayed by architect session `arch1` —
[quince#577](https://github.com/novkostya/quince/issues/577#issuecomment-5208632711). Cited by
comment URL and self-declared role rather than by login, per quince#47.

`DELETE /api/config/storage/{name}` asks which jobs are bound to that storage and refuses before
writing: **`storage.Manager.JobsOn(storageID)`**, surfaced on `httpapi.StorageReader` and called in
`handleConfigStorageDelete`. **In the handler rather than in `config.Service`**, because putting it
there would make the config package depend on the storage subsystem — backwards, since this rung's
seam runs storage-subscribes-to-config.

**(a) was ruled out.** The Operator was content with (b) or a cancel-then-forget variant and asked
for whichever was easier — and that is (b), for a reason about correctness rather than effort:
cancellation is **asynchronous**, so cancel-then-forget must wait for every affected job to reach a
terminal state inside an HTTP handler, with a timeout whose expiry lands the forget mid-phase. **A
mechanism whose failure mode is the bug it fixes is the wrong mechanism.**

**(c) would have amended ROLL-FORWARD, not a retry footnote.** `VerifyWork`, `CommitJob` and
`Discard` all resolve through `jobSlot`; a forget landing between verify passing and commit
completing leaves `CommitJob` unable to resolve its slot, and restart-time recovery fails identically
because the storage is gone from the declaration. `Discard` needs it too, so even the cleanup path
disappears — a job stranded mid-commit with no way forward or back.

**The cost, accepted rather than discovered: this is the FIRST `422` on that endpoint about
LIVENESS.** Every other refusal there answers *is this a valid set of storages?*; this one answers
*is quince busy?*. Both seats named it as the real objection and the ruling was taken with it in
view. Written into contracts §1 so the next reader meets it as a decision.

**THE ORDER IS THE DESIGN, and the first implementation had it backwards.** The declaration refusals
— default, only-storage — run **before** the liveness check, because a **permanent** refusal must
outrank a **transient** one. Reversed, a user forgetting their default disk mid-backup is told *"wait
for it to finish, or cancel it"*, waits out a multi-hour transfer, retries, and is then told *"it is
the default"* — a remedy that was never going to work.

**Not a corner case: the default storage is where backups go**, so *default and busy* is the ordinary
state. **Every Go gate passed on the wrong order**; `story8` caught it on the first CI run that
dispatched, because `--demo` keeps a job running on `internal`, which is also its default. That is
this rung's clearest instance of ui-e2e answering a question the Go gates could not — and it is worth
noting that the spec's own G5 did not ask it either: the gate was written as *"a busy storage is
refused"*, which the wrong order satisfies.

**The check therefore lives INSIDE `ForgetStorage`, passed in as `busyReason func(string) string`.**
The handler supplies the sentence; `config` decides when to ask. That keeps the config package free
of the storage subsystem — it receives a string, never a job — while putting the ordering in the one
place that owns the sequence of refusals, rather than splitting it across a handler `if` and a
function nobody can see from there.

**Residual, stated rather than solved:** a job can bind between the check and the write. Moving the
check inside `writeMu` narrows the window from *the width of an HTTP handler* to *two statements*,
but does not close it — a job binds under `storage.Manager.mu`, which that lock knows nothing about.
Accepted, and if it is ever worth closing, the way is to make the two locks meet, never to add a
retry.

**Story 6 changes meaning, and G5 with it.** *A job running on a storage that is forgotten mid-job*
becomes *a forget that is refused while a job is running*. **The paragraph this block replaced —
*forgotten and no-longer-in-use differ for the duration of a job* — does NOT come back: under (b)
they never differ.**

---

### The question as it was asked, kept because the reasoning that lost is what makes a ruling checkable later

**This paragraph asserted the wrong thing, and PR 4 measured it.** It read:

> **An in-flight job keeps the storage it started on, and that is correct.** It holds a copied
> `Slot`, so it completes against the disk it began writing to … the storage leaves the list
> immediately, and the job that is using it finishes. Story 6 and G5.

**The engine does not hold a copied `Slot`.** Every phase — `Seed`, `PrepareWork`, `SeedWork`,
`CommitJob`, `Discard`, `VerifyWork` — re-resolves through `Manager.jobSlot(jobID)`, which looks the
storage up in the CURRENT list by the job's binding. Measured on this branch, `internal/storage`:

```
ApplyStorages([alpha, beta]); bind job-1 → beta; ApplyStorages([alpha])
jobSlot("job-1") → error: this job was started against storage "…" , which is no longer declared —
                          refusing rather than writing it to a different disk
```

So with the applier wired, **forgetting a storage mid-backup fails the running job at its next
phase.** That refusal is correct in itself and predates this rung — silently retargeting a backup to
a different disk would be far worse — but it was written for a config change observed at *restart*,
where no job can be in flight. Live apply is what makes it reachable mid-transfer.

**It contradicts a hard rule, which is why this is a gap rather than a scope call.** `CLAUDE.md`:
*"a commit failure must not destroy a multi-hour Wi-Fi transfer"*, and *"a failed job KEEPS its dirty
`working/` so a retry resumes without re-transferring."* A job killed this way loses the phase it was
in, and Wi-Fi is the primary transport under the assisted model — the case where hours are at stake.

**PR 4 was HELD** — the wiring was written and not merged behind this, because shipping the applier
alone turns an unreachable refusal into a reachable one, which is a regression introduced by the fix.
**RELEASED by the ruling above:** PR 4 carries the wiring and the `422` in one diff, so the reachable
refusal never exists in a merged tree.

- **(a) `ApplyStorages` retains a slot with in-flight jobs bound to it**, dropping it when the last
  finishes. The storage leaves the wire immediately (the user sees it forgotten) while the running
  job keeps resolving. **RECOMMENDED** — it is what this spec already claimed, and it makes
  *forgotten* and *no-longer-in-use* differ for exactly the duration the paragraph described. Cost:
  the Manager grows a rule that the slot list is "declared ∪ in-use", and something must drop the
  retained slot at job end.
- **(b) `DELETE /api/config/storage/{name}` refuses with `422` while a job is running on it**,
  naming the job. Simplest, and honest — but it makes a user wait out a multi-hour backup to remove
  a disk they may have already unplugged, and the existing `422` set is about *coherence of the
  declaration*, not about liveness.
- **(c) Accept it and say so.** Let the job fail, with the refusal reaching the UI. Cheapest, and it
  needs the hard rule above amended rather than merely noted — which is the Operator's, not mine.

**What is NOT in question:** the refusal itself. Retargeting a bound job to another disk is not on
the table under any option.

**Nothing is built on this while it is pending.** PRs 5–7 do not touch it; PR 4 resumes the moment it
is ruled, and under (a) or (b) its G5 becomes assertable rather than aspirational.

---

**The paragraph this block replaced does NOT come back.** It survived here for one draft as *"once
ruled, it becomes true again — forgotten and no-longer-in-use differ for the duration of a job"*, and
that sentence was written expecting (a). **(b) was ruled**, and under (b) the two never differ: a
forget either happens, in which case no job was running on it, or it is refused, in which case
nothing left the list. There is no interval to describe.

Kept as a correction rather than deleted, because *"it becomes true again"* is exactly the shape a
later reader would restore in good faith — a sentence the spec once asserted, marked as merely
pending. It is not pending; it lost.

### The per-setting answer — the actual deliverable

Every key in `config.yml`, with a verdict. Three bins, not two, because interface fact 9 makes two
bins impossible to fill honestly.

| key | verdict | why |
| --- | --- | --- |
| ~~`backup.transport`~~ | **THE KEY NO LONGER EXISTS** | This read *"nothing reads it — quince#654"*, true the day it was written. quince#654 **renamed** it `preferred_transport` and gave it a consumer four days later, and PR 5 wired it live. **The canonical table is contracts §6**, which is where PR 6 put it; this row stays struck rather than deleted because PR 5's own note points at it. |
| `backup.require_encryption` | **live** | Read per job (fact 8); the applier swaps one synchronized field. A running job keeps its answer. |
| `storage[]` (membership) | **live** | The ruled first consumer. |
| `storage[].path` / `.backend` / `.zfs.*` | **live** | Re-resolved by the same `resolveSlot` a restart uses (fact 4). What happens to an in-flight job is the `PROPOSED (gap)` above. |
| `storage[].retention.*` | **live** | Read only in `Prune` (`subsystem.go:240`), off the slot the applier replaces. |
| `storage[].default` | **live** | Re-ordering `declaredStorages` moves `slots[0]`. Safe only because removing the default is refused; a *re-designation* takes effect for the next unbound job. |
| `devices.manage_muxer` / `.usbmuxd_socket` / `.netmuxd_addr` | **restart** — *and D12 requires this sentence:* a netmuxd restart tears a live Wi-Fi backup (`supervisor.go:122`), so applying these live means ruling on what happens to a running transfer. Out of scope, named, not silent. |
| `tls.cert_file` / `.key_file` | **paths: restart. Contents: already live** | `tlsx.Keeper` re-reads the files on rotation (`keeper.go:79,109`) — renewals already need no restart (`OnboardingHTTPSPage.tsx:141`). Changing the *paths*, or turning TLS on or off, needs a rebind. |
| `sessions.ttl_minutes` | **nothing reads it** | Filed by this rung. It is the vault-unlock TTL and the label says *"Session TTL"*. |
| `sessions.allow_insecure_transport` | **restart** | Decides the plain-half handler once at bind (`main.go:300-303`). |
| `automation.staleness_days` / `.reminder_cooldown_hours` | **nothing reads it (declared)** | qn.12, `schema.go:248`. Declared debt, not a defect. |
| `ui.theme` | **already live** | Client-side, from the PUT response. |

This table goes into **contracts §6**, beside the schema it describes, because it is the answer a
reader needs at the same moment they read the key. `ConfigEditor`'s *"restart quince to apply"* then
becomes conditional on the fields the form actually changed, and means something.

---

## RULED (was `PROPOSED (gap)`): this rung builds PROPAGATION ONLY; file-watch becomes its own rung

**Operator ruling 2026-08-04, option (a) as recommended**, relayed by architect session `arch1` —
[quince#577, `issuecomment-5182609911`](https://github.com/novkostya/quince/issues/577#issuecomment-5182609911).
Cited by comment URL and self-declared role rather than by login, per quince#47.

`qn.6g` builds the `Subscribe` seam and the appliers. A file-watcher later becomes a **second
producer feeding the same appliers**, which is why the seam is the prerequisite under every option
and building it cannot be wrong.

**Not (c): D12's file-watch commitment is MOVED, not dropped.** Hand-editing `config.yml` stays a
path worth live-applying; it is simply not this rung's. **Which rung it lands in is unallocated** —
`qn.6h` is quince#591's — so D12 must say *unallocated* rather than name a rung nobody has agreed to.

**Three obligations follow, and PR 6 carries two of them:**

1. This block, flipped — heading and body in the same diff (quince#408), which `bin/gap-heading-check`
   enforces.
2. **`stack.md`'s staged-delivery paragraph is re-dated.** It says file-watch lands *"with qn.6"*,
   which is now false. **Deleting the line would be (c) by the back door** and is not what was ruled.
3. **Contracts §6 states the cost**, rather than leaving a reader to find it: *a setting changed
   through the UI applies immediately; the same setting hand-edited in `config.yml` still needs a
   restart until file-watch lands.* §6 repeats the *"file-watch pickup"* claim and needs the same
   treatment. **This is the obligation most likely to be skipped**, because it documents a limitation
   inside the rung that fixes the larger one — and it is the condition the recommendation was
   accepted on.

**Explicitly NOT settled, and not to be settled inside `qn.6g`:** the invalid-edit contract — *a bad
hand-edit keeps last-good and shows a UI banner*. That is D12's other half with its own
`Warning`-surfacing question, and (a) was chosen partly so it can be decided on its own evidence.

**PRs 2–7 are unchanged**, which the block below predicted and the ruling now settles.

---

### The question as it was asked, kept because the reasoning that lost is what makes a ruling checkable later

**Why this was a gap and not a scope call.** D12's *Staged delivery* paragraph
(`stack.md:571-577`) is a written plan with a rung attached: *"File-watch live reload, generated
doc-comments, and the full transparent-editor UX land with **qn.6**."* This is qn.6. Contracts §6
repeats it (*"file-watch pickup"*). The ruling on quince#577 is about `config.Service` telling the
running process when **it** changes the file — it does not mention detecting a change made by
somebody else, and the two are different mechanisms.

Leaving file-watch out therefore moves a dated commitment. `CLAUDE.md` forbids silently patching that
kind of hole, so it is asked rather than assumed.

**What is at stake.** With propagation only, a hand-edit of `config.yml` still needs a restart, while
the same edit through the UI does not. D12's *"edited by the UI and by hand equally"* becomes false in
a new way — a smaller lie than today's, but a lie with a sharper edge, because the two paths now
differ.

- **(a) Propagation only, file-watch is its own rung. — RECOMMENDED.** This rung builds the
  `Subscribe` seam, which is the prerequisite for file-watch either way; a watcher is then a second
  producer feeding the same appliers, and provably so. It also lets the *"invalid edit keeps
  last-good and shows a UI banner"* half of D12 — a separate user-visible contract, with a
  `Warning`-surfacing question of its own — be decided on its own evidence rather than at the tail of
  a rung about propagation. Cost: stated above, and it must be written into contracts §6 rather than
  left for a reader to notice.
- **(b) Build both here.** D12 lands whole and the hand-edit path stops being second class. Cost: a
  new dependency (`fsnotify` is not in `core/go.mod`), a debounce-and-coalesce policy, atomic-write
  semantics to get right (quince's own `AtomicWrite` renames, and so does every editor), and the
  bad-edit banner contract — roughly doubling the rung, on top of a slice that already touches four
  packages.
- **(c) Amend D12 to drop file-watch.** Only if the Operator's answer is that hand-edits are not a
  path worth live-applying. Cheapest, and the one that must not happen by default.

**Nothing is built on this while it is pending.** Under (a) or (c) the code slices below are
unchanged; under (b) a seventh PR is added. So PR 1 can be reviewed and merged before the ruling —
this block is a spec paragraph, not a blocker on the spec.

*(That last paragraph held: PR 1 merged at `36e1060` before the ruling arrived, and the ruling
changed no slice.)*

---

## Stories

1. **A storage added through the UI is being served before the page finishes reloading.** `PUT
   /api/config` with a new entry; `GET /api/storages` lists it; a backup can be started against it.
   No restart.
2. **A storage forgotten through the UI stops being served.** `DELETE /api/config/storage/{name}`;
   it leaves `GET /api/storages`; a job naming it is refused with the ordinary not-found answer.
   Nothing on the disk is touched.
3. **Forgetting the default is still refused**, with the same `422` and the same remedy — live-apply
   does not open a path around a standing ruling.
4. **A retention change takes effect at the next prune**, with no restart.
5. **A storage added while holding existing backups shows them.** Its versions are reconciled on
   add, not at the next restart.
6. **A forget is refused while a backup is running on that storage.** `422`, naming the job, and the
   storage stays in the list and stays declared — the config file is not written. The remedy is in
   the message: wait, or cancel the job, then forget. (Ruled 2026-08-06, option (b) — this story
   read *"a job … completes against that storage, and the storage is gone from the list the whole
   time"* until the gap block above was resolved, and that behaviour was never built.)
7. **`require_encryption` applies to the next job**, and not to one already running.
8. **The settings screen only says "restart to apply" when that is true** — for the fields in the
   restart bin, and never for the live ones. Forget's dialog and confirmation stop mentioning a
   restart at all.
9. **Nothing panics or tears while the list moves.** Concurrent readers — recheck, list, job
   resolution, prune — against a list being replaced.

---

## Gates

| id | what it proves | where |
| --- | --- | --- |
| **G1** | Add via `PUT /api/config` → `GET /api/storages` lists it and a job can target it, one process, no restart. | CI (Go) |
| **G2** | `DELETE /api/config/storage/{name}` → it leaves `GET /api/storages`, a job naming it is refused, and **the tree is asserted untouched** — on the filesystem, not on the API. | CI (Go) |
| **G3** | Forgetting the default still `422`s, single-storage case included. | CI (Go) |
| **G4** | A retention edit changes what the next `Prune` keeps, with no restart. | CI (Go) |
| **G5** | Forgetting a storage with a job bound to it is refused `422`, the message names the job, **and the config file is unchanged** — asserted on the file, not on the response, because a refusal that still writes is the failure this gate exists to catch. The same forget succeeds once the job reaches a terminal state. | CI (Go) |
| **G5c** | **A storage that is BOTH the default and busy is refused for being the DEFAULT**, and the *wait-or-cancel* remedy is not offered. Added after the fact: G5 as first written was satisfied by the wrong order, and `story8` caught what it missed. | CI (Go) |
| **G6** | `require_encryption` flipped mid-flight: the running job keeps its answer, the next job gets the new one. | CI (Go) |
| **G7** | A storage added hot, whose root already holds committed backups, has them **visible without a restart**. | CI (Go) |
| **G8** | The settings screen's restart copy matches the table: present for a restart-bin field, absent for a live one; Forget's copy carries no restart. | ui-e2e |
| **G9** | **`go test -race ./...`, plus a targeted harness**: goroutines calling `Storages`, `RecheckStorage`, `ResolveChoice` and `Prune` while another applies adds and removes. | CI (Go, `-race`) |
| **G10** | `make privacy-check REF=origin/main...HEAD TEXT=<file under $HOME/scratch/r17/>` | host |

**G9 is the gate this rung stands or falls on**, and it is stated first among equals rather than
last. All three of the issue's named hazards are races, and `Slot` holds a `Backend` **interface** —
so a torn value is an itab/data mismatch, a segfault rather than a wrong answer. That is the reason
`Manager.mu` exists (`subsystem.go:64-79`), proven under `-race` by quince#445, and this rung is the
first to write the list rather than one element of it.

**G8's limits are declared, not discovered.** Per interface fact 10 the demo fabricates its storages
and `--public-demo` deletes its config, so **ui-e2e proves the copy and nothing about a disk being
served**. G1–G7 carry that claim, in Go, against a real temp-dir storage.

**No hardware gate, and no live-apply claim is made about real removable media.** A disk physically
pulled while a live apply runs is proven by nothing here and is not claimed.

---

## Fixtures

**New: a live-apply harness** — a `*config.Service` over a `t.TempDir()` config file plus a `Manager`
over two temp-dir storage roots, driven through the real `PUT`/`DELETE` handlers rather than by
calling `ApplyStorages` directly. Calling the applier directly would prove the applier and skip the
seam, which is the half most likely to be wrong.

**New: the G9 race harness**, above.

**No new transcripts.** This rung drives no device.

**The demo provider is extended only if G8 needs it**, and its storages stay fabricated. `qn.6d`'s
requirement stands and is inherited verbatim: *a fixture that fabricates a value the live code never
produces makes its gate a lie.* G8 asserts UI copy, which the demo can honestly produce; it must not
be widened into a claim about serving a storage.

---

## Rule check

Written before building. Every rule this rung touches **or comes near**, near-misses included.

| rule | how this plan complies |
| --- | --- |
| **A rung starts from a spec** | This document, PR 1, reviewed before any code exists. |
| **Don't improvise architecture** | The one thing the rung ruling did not cover — file-watch versus D12's dated plan — was raised as a gap rather than decided, and is now **RULED (a), 2026-08-04**: propagation only. Nothing was built on it while it was pending, and the ruling changed no slice. The rung letter and the order against quince#591 were handed to the spec explicitly and are recorded as rung-ruled. **The ruling's two canon obligations are carried in PR 6, not treated as discharged by this flip** — a gap answered is not a gap acted on. |
| **Contracts are stop-and-ask** | No route changes, no wire object changes, no new event kind (decision 4). What *does* change is contracts §6's restart claim, and it changes in the PR that makes it false — which is the docs rule, not a contract gap. |
| **Never mutate a committed version** | **The rung's sharpest near-miss, and the one it got wrong on the first draft.** Forgetting a storage removes it from a list; it touches no tree. G2 asserts on the **filesystem**. This row read *"a job in flight keeps its slot and finishes its commit — roll-forward is preserved exactly"*, and that was **false**: every phase re-resolves through `Manager.jobSlot`, so a live forget strands a job mid-commit with `CommitJob` unable to resolve and `Discard` gone too. Roll-forward is preserved by **refusing the forget** (ruling (b), above), not by the engine holding a copy. G5 asserts the refusal. |
| **State honesty** | The per-setting table has **three** bins because two would force a false answer for five keys (fact 9). *"Restart to apply"* stops appearing where it is untrue and stays where it is true. An applier that cannot complete emits a warning rather than logging success. Two of the issue's own citations are corrected in fact 5 rather than repeated. |
| **No silent caps or fallbacks** | This rung exists to close one. Applier warnings ride the `warnings` channel the endpoints already return. The settings that stay restart-required are **named individually with a reason**, which is D12's *"unless the spec says why"* discharged per key rather than in the aggregate. |
| **Config tidiness (D12)** | The whole point. No new key, no secret, no UI-only state. **Near-miss declared:** `devices.*` and `tls.*` remain restart-required, and D12 permits that only with a stated why — both have one, in the table and in the Boundary. |
| **No UI-only state** | The restart-required verdict is a property of the key, published in contracts §6; the UI renders it and stores nothing. |
| **Privacy is a commit-time gate** | Storage **paths** are the sharp surface again — every fixture and screenshot names a place. Everything here is `t.TempDir()`, `/backups` or `/mnt/shuttle`; no lab topology, no dataset name, no real mount point. `TEXT=` takes a **path** to a body file under `$HOME/scratch/r17/`, never inline prose and never a fixed `/tmp` path. |
| **Secrets discipline** | Near-miss by adjacency: `tls.*` and `sessions.*` are in the table. No secret is read, moved, logged, or added — TLS **paths** are not key material, and no backup password is on any path this rung touches. |
| **Subprocesses** | None added. `devices.*` is out of scope precisely so no supervised subprocess is restarted by a config edit in this rung. |
| **Every hardware bug becomes a fixture** | No hardware gate here, so nothing is owed — stated rather than left blank. |
| **Docs are part of the diff** | contracts §6's table lands with the code that makes it true; `stack.md` D12's staged-delivery line and `design.md` §8's restart sentences change in the PR that falsifies them, not later. |
| **Coverage declared** | Every code PR carries `go test -cover` plus a known-untested list. Expected standing entry: the applier-failure branch for a storage whose root becomes unreadable between the write and the apply, which no CI box stages reliably. |
| **A rung's goal is provable at rung close** | G1–G9 run in CI or ui-e2e at rung close; none depends on a later rung. G10 is a host gate per PR. |
| **Approver ≠ author** | Implementer authors. **PR 6 alone is code-owned** — `contracts.md`, `stack.md` and `design.md` are three of `CODEOWNERS`' six owned paths, so it needs `@novkostya`, and an App verdict cannot satisfy it because an App cannot be a code owner. **Every other PR here, this spec included, is the architect's to approve.** `/docs/specs/**` is deliberately *not* owned, and the file says why: specs *"bind one rung, not the project, and routing them to the Operator would make every rung wait on the seat that is deliberately not in the loop."* |

---

## Rung-ruled decisions

1. **The letter is `qn.6g` and it runs before quince#591 (`qn.6h`).** Reversible; the reasoning is at
   the top, and the two rungs share one file in different methods.
2. **An applier cannot refuse.** Refusal runs before the write, where the codebase already puts it.
   A file written and a process that rejected it is a state with no honest report.
3. **Appliers are registered at wiring time only**, so the list is immutable at runtime and interface
   fact 7's unlocked-write hazard is not recreated.
4. **No new WebSocket event kind this rung.** The tab that made the edit refetches, which is what
   `ForgetStorage.tsx:55-58` will now do. A second tab, or a hand-edit, is exactly the population
   file-watch serves — so the event belongs with the gap's answer, not ahead of it. Recorded because
   *not* minting a `config.updated` kind is a decision, and the cheap-looking moment to mint one is
   the same PR that adds the notify.
5. **`renderSlot` takes a name, not an index.** Narrowing the two windows of fact 6 would leave a
   race that is rare rather than absent; re-finding under the lock removes them.

---

## Known gaps and open questions

0. **What happens to a job running on a storage that is forgotten** — **RULED 2026-08-06**, option
   (b): `DELETE /api/config/storage/{name}` refuses `422` while a job is bound to that storage,
   naming the job. Raised 2026-08-04 by PR 4 and **measured** rather than reasoned — a live forget
   makes the running job's next phase refuse — so the paragraph it replaced was wrong rather than
   merely incomplete. No longer open, and no longer blocking: PR 4 carries the ruling in the same
   diff that flips the block above. **It leaves one canon obligation** — the liveness `422` is a new
   *kind* of refusal on that endpoint and is written into contracts §1 here, in PR 4, rather than
   deferred to PR 6, because the code that emits it lands in this diff.
1. **File-watch** — **RULED 2026-08-04**, option (a): propagation only, file-watch its own rung, its
   letter unallocated. No longer open. The two canon obligations it leaves — re-dating D12 and
   stating the cost in contracts §6 — travel with **PR 6**, and the second is the one this spec
   flags as most likely to be skipped.
2. **Does an applier's warning survive the next `GET`?** `Replace` sets `s.warnings = nil` on every
   valid write (`service.go:281`), so an apply warning written into that field is cleared by the next
   save even if its cause persists. Rung-local, settled in PR 2, flagged here because the obvious
   implementation gets it wrong.
3. **`sessions.ttl_minutes`** — **quince#656**, quince#654's twin, filed while writing this spec and
   deliberately not fixed here. Its label says *"Session TTL"* on a page reached from a login, which
   is the half that makes it worse than quince#654 rather than merely equal to it.
4. **Ordering among appliers** is registration order, which is `buildLiveStack` order. Nothing today
   needs a second applier to observe a first one's effect. If something ever does, this becomes a
   dependency graph, and that is a decision this rung does not pre-empt.

---

## PR slicing

Each carries one reviewable claim and its own proof.

1. **This spec.** **Not** code-owned — `/docs/specs/**` is one of `CODEOWNERS`' declared omissions,
   so the architect approves it. PRs **4** and **6** need `@novkostya`; the rest do not.
2. **The seam** — `Applier`, `Subscribe`, notify from `Replace` and `ForgetStorage`, warning
   plumbing. No consumer yet, so the claim is *the mechanism exists and fires exactly once per
   write*. Proof: Go tests, including open question 2.
3. **`Manager` survives a moving list** — `ApplyStorages`, `renderSlot` by name, the three missing
   empty-list guards. **No wiring yet**, so this PR is provable in isolation. Proof: **G9**.
4. **Storage is wired, AND retention with it, AND the forget is refused while a job runs on it** —
   the applier in `live.go` including the reconcile on add, plus `Manager.JobsOn` and the `422`.
   Proof: G1, G2, G3, **G4**, G5, G7.

   **Three claims rather than one, and the bundling is forced.** Shipping the applier alone makes an
   unreachable refusal reachable mid-transfer — a regression introduced by the fix — so the `422`
   cannot follow in a later PR. **This makes item 4 code-owned** (it edits `docs/contracts.md` §1)
   and therefore `@novkostya`'s to approve, which the line under item 1 said only PR 6 would be.

   **Retention moved here from item 5, and it is a dependency rather than a preference.** It lives on
   `Slot.Retention` (`slot.go:29`) and `policyFor` reads it off the slot list (`subsystem.go:518`),
   so it reaches `Prune` only through `ApplyStorages` — which is this item. It cannot land earlier.
   **G4 travels with it.**

   ~~**HELD** on the in-flight-forget `PROPOSED (gap)` above.~~ **Released 2026-08-06 by the ruling.**
5. **`require_encryption` and `preferred_transport`** — the second and third consumers, which is what
   proves the mechanism is general rather than a storage hook wearing a general name: a different
   package, a different lock, a different shape of state. Proof: G6.

   **This item read "retention and `require_encryption`" until PR 5 was written.** Retention cannot
   land without item 4, so `preferred_transport` took its place — and it is the better partner
   anyway, because it **postdates this spec** (quince#654, merged 2026-08-04) and is the other
   `backup:` key that anything actually reads. **The per-setting table above still calls
   `backup.transport` "nothing reads it", which was true when written and is now false. Correcting
   it is item 6's**, since that table is canon.
6. **The per-setting table** — contracts §6, D12's staged-delivery line, design §8. Canon-owned;
   needs `@novkostya`. **It also carries the file-watch ruling's two canon obligations, and they are
   not optional extras to the table:** D12's staged-delivery paragraph is **re-dated** (naming the
   rung as *unallocated* — deleting the line is option (c) by the back door and was not ruled), and
   §6 states the cost in a sentence — *a setting changed through the UI applies immediately; the same
   setting hand-edited in `config.yml` still needs a restart until file-watch lands.* §6's own
   *"file-watch pickup"* claim needs the same treatment. **The cost sentence is the condition (a) was
   accepted on**, and the thing most likely to be dropped, because it documents a limitation inside
   the rung that fixes the larger one.
7. **The UI stops promising a restart** — `ConfigEditor`, `SettingsPage`, `ForgetStorage` including
   its list invalidation. Proof: G8.

PRs 2 and 3 are independent and can be reviewed in parallel; 4 needs both.
