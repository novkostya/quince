# qn.6e — adding a storage: the probe, the form, and the first-run path

**Rung issue:** [quince#502](https://github.com/novkostya/quince/issues/502), scoped by the Operator
2026-08-07. This spec is the rung's first PR (canon §8); no code exists yet.

**Goal.** A user types a path, quince tells them what is there and what backend it would use
**without changing anything on that path**, and one click writes a **concrete** backend into
`config.yml`. On a fresh install this is reachable before any storage exists.

---

## Why this rung is small, and where its engineering actually is

`qn.6g` landed the runtime half. `contracts.md:1031`:

> | `storage[]` membership | **live** | The storage applier — `qn.6g`'s first consumer. An **add** also reconciles, so a disk that already holds backups shows them without a restart. |

So the *effect* of an add already works: `live.go:274-290` runs `storageMgr.Reconcile(ctx)` when
`addedStorage(before, after)` is true. `qn.6d` shipped the whole write path in mirror image —
`DELETE /api/config/storage/{name}`, server-side splice, three refusals, live apply
(`handlers_config.go:74-132`, `forget.go:63-141`). **Add is that mirror and does not exist**:
`server.go:90-118` registers no `POST` counterpart.

What is unbuilt is **the probe**, and the whole of this rung's engineering is that today's probe
cannot be pointed at a path a user typed:

```go
// core/internal/storage/probe.go:83
if err := os.MkdirAll(backups, 0o755); err != nil {
```

`probeNamespace` **creates the directory it probes**, then writes into it. Correct for a declared
storage at startup; behind a form it would silently create a typo and then report it healthy. The
codebase already knows this and already routes around it — `live.go:356-368`:

> THE GUARD RUNS BEFORE ANYTHING TOUCHES THE PATH (quince#415). … a declared path that did not exist
> was CREATED by the probe, so by the time ResolveStorage looked, the path was reachable, markerless
> and unknown — a textbook creation moment, at a path the user had typo'd. … **NOBODY CREATES A
> STORAGE ROOT. A declared path must already exist.**

So the rung's central requirement is not new law. It is the same law, reached from the other side:
`resolveSlot` made the probe lazy so a *refusal* never reaches it. A form needs something stronger —
a probe that **reports** rather than resolves, and never creates.

---

## Boundary

**In.** `core/internal/storage/` (a new non-creating inspection), `core/internal/httpapi/`
(two probe routes and one add route), `core/internal/config/` (the add mutation),
`core/internal/wire/`, `ui/src/features/storage/`, `ui/src/pages/DashboardPage.tsx`,
`docs/contracts.md` §1/§2, `docs/quince.design.md` §9, `deploy/storage.md`.

**Out, and each was descoped by a ruling or by a measurement:**

- **Discovery** — enumerating disks the host can see. Operator, 2026-08-02: *"no, descope discovery
  — I'm not interested in this now."* A path is typed.
- **Changing a storage's path.** Descoped from `qn.6d`, not picked up here.
- **Retention / prune settings in the UI.** Operator: *"IN FUTURE… out of scope now."*
- **Forget.** Shipped in `qn.6d`.
- **Deriving `zfs.parent_dataset` from a path.** The issue asked for it. **Descoped on interface
  fact 3** — see Design; the replacement is stronger, not weaker.
- **The other §9 guided checks.** design §9 names four — *backups dir writable; backend probe with a
  plain-language explanation of what was picked and why; usbmuxd reachable; optional Wi-Fi toggle*.
  This rung builds **the second one only**. The usbmuxd and Wi-Fi checks are accepted proposals **P1
  and P1b** in the devlog ledger, homed at "qn.6" and **not at a letter**; they are not this rung's,
  and this rung must not grow an onboarding framework on their behalf.
- **`PUT /api/config`'s missing `CheckStorageBackends` — out of scope, and a DEPENDENCY rather than
  a hole this rung leaves open.** [quince#683](https://github.com/novkostya/quince/issues/683) was
  **ruled 2026-08-07T10:01Z**, ninety minutes before this spec opened: the check goes in
  **`replaceLocked`**, beside the existing `CheckStorages` call — not in `Validate`, which would make
  `Load` discard the config and fall back to `Default()`, reproducing quince#508. The ruling says
  *"land this before or with `qn.6e`."* See PR 5.

---

## Interface facts — measured at `de9951d`, not recalled

Canon: *interface facts and version pins are looked up live*. The issue named three to verify. All
three are answered below; **one is answered NO and one could not be measured at all**, and both
change the design.

**1. The ZFS `statfs` f_type IS observable from inside the runtime image, on a bind-mounted host
directory.** Measured by running the shipped image with a bind mount of a host directory whose
filesystem is ZFS:

```
$ nerdctl run --rm --entrypoint /bin/sh -v <host-zfs-dir>:/probe quince:staging \
    -c 'stat -f -c "%t" /probe; stat -f -c "%t" /tmp'
2fc12fc1        # the bind-mounted host ZFS directory
794c7630        # the image's own overlay root
```

Method validated on the same box against filesystems whose magic numbers are documented: `tmpfs`
→ `0x01021994`, `proc` → `0x9fa0`, `sysfs` → `0x62656572`. The signal survives the bind mount and
discriminates. **This was asserted from training data in the scoping conversation and is now
measured.**

`statfs` is already one field away from code that runs on every storage —
`storage/space.go:20-27` calls `unix.Statfs(path, &st)` for capacity and reads `st.Bavail`,
`st.Blocks`. The tier-1 signal is `st.Type`.

**Owed at build time, not now:** whether `golang.org/x/sys/unix` (pinned `v0.47.0`, `core/go.mod:10`)
exports a ZFS magic constant. Look it up live in PR 2; if it does not, define one and cite this
measurement as its evidence rather than a remembered number.

**2. Whether `zfs list -H -o name <path>` accepts a PATH — NOT MEASURED, and unmeasurable from any
seat this project holds.** There is no `zfs` userland on the session boxes and none in the image
(fact 3). Declared owed rather than assumed. **Nothing in this spec rests on it**, because fact 3
makes the question moot — see Design.

**3. `zfs` is NOT in the container image, and this is the fact that reshapes the rung.**
`deploy/Dockerfile:145-153` is the only runtime `apk add`:

```
ca-certificates tzdata usbmuxd libplist libimobiledevice-glue libusbmuxd libtatsu openssh-client python3
```

No `zfs`, no `zfs-utils`. Measured in `quince:staging`: `command -v zfs` → absent, `command -v
zpool` → absent. The only `zfs` mentions in the Dockerfile are the comments at `:139-143`, and they
are about `openssh-client` — *the transport for `hook` mode*.

**And `Resolved()` defaults `zfs.mode` to `exec`** (`schema.go:142-144`), which execs `zfs`
directly (`zfscli.go:34-38`, `:47-53`). **So the schema's default zfs mode cannot work in the
shipped image.** That is not a defect this rung introduces and not one it fixes; it is the reason
the form's zfs branch defaults to `hook` and says why.

> **SINCE FIXED — the default is `hook` and `exec` is GONE** (Operator ruling 2026-08-10,
> quince#697, executed on quince#793). `hook` is the only legal value, and a config still carrying
> `exec` is refused by path. The measurement above stands — the image has no `zfs` binary — but do
> not read this fact as describing the current default.

**4. zfs is NEVER probed today — it is pure intent.** `probe.go:31-32`:

```go
wantZFS := opts.Backend == BackendZFS ||
	(opts.Backend == "auto" && (opts.ZFSParent != "" || opts.ZFSHookCmd != ""))
```

Point today's auto-probe at a ZFS dataset and it answers **reflink** (2.2 block cloning) or
**hardlink**, and never says the word zfs. The same predicate is duplicated verbatim at
`storagereq.go:191-192`. The duplication is **semantic rather than literal** — `probe.go` spells it
`BackendZFS`, `storagereq.go` spells it `"zfs"` — which is worse, not better: two spellings of one
predicate will not be caught by a grep for either. **A third copy in the add path would make three**
— factor it in PR 2.

**5. The marker overrides the selector, and the comparison already exists.**
`storagemarker.go:38-41`: backend is *"recorded at the storage's creation moment and is IMMUTABLE
thereafter … A later probe that disagrees with this field is a remount, not a re-selection."*
`Mismatch(probed)` (`:118-126`) returns the refusal sentence, and **an empty probed backend is not a
mismatch** (`:119`) — *"could not determine"* and *"changed"* are different states.

**6. Both namespace probes clean up after themselves and write only dotfiles.**
`clonetree.ReflinkProbe` (`clonetree.go:172-191`) and `hardlinkProbe` (`probe.go:96-113`) each
`defer` removal of two `.quince-*` files. So an inspection of an **existing writable** directory is
safe to run from a form. It is still a write, and the form's control is therefore *Check*, not a
preview.

**7. The forget handler is a complete template for the add, including the refusal ORDER.**
`handlers_config.go:78-101` hands the liveness reason in **as a closure** rather than checking it
first, so permanent refusals outrank transient ones — the ruling in contracts §1. Outcomes:
`500 internal` / `404 no_such_storage` / `422 {errors:[{path,message}]}` / `200 {config, warnings,
source}` (`:102-130`). `config.ForgetStorage` does the whole read-modify-write under `s.writeMu` and
calls `replaceLocked`, never `Replace` — which would deadlock (`forget.go:69-70`, `:133`).

**8. The helper's guard fall-through is `exit 1`, and that is what makes a hook test meaningful.**
`deploy/storage.md`, last three lines of the script:

```sh
esac
echo "quince-zfs-helper: refused: $SSH_ORIGINAL_COMMAND" >&2
exit 1
```

Every **path-guarded** arm — `create`, `snapshot`, `destroy`, `list`, `seed` — is
`case "$target" in "$PARENT"…`, so a target outside the baked-in `$PARENT` falls through to that
refusal. So `list <typed parent>` **does** discriminate: it proves the key, the forced command, and
that the operator's `$PARENT` matches what the user typed. (**`capacity` is the exception and has no
guard**, because it takes no caller argument at all — the next bullet is about exactly that, and an
earlier draft said *"every arm"* while asserting the exception two lines later.)

**Two traps in that, both measured off the script rather than reasoned:**

- **`list` on a correct, fresh parent returns exit 0 and EMPTY stdout.** `list` runs
  `zfs list -t snapshot -H -o name -r "$target"` (`storage.md:80`), and a storage with no backups
  yet has no `@quince-*` snapshots. **Empty output is SUCCESS**, and a test that reads emptiness as
  failure would report a correctly-installed helper as broken on the one day it matters — first run.
- **`capacity` takes NO caller argument** (`storage.md:103-106`), so it proves reachability with
  unambiguous non-empty output but says nothing about the typed parent. It was added by `qn.6d` and
  carries an explicit **operator migration**, so an un-migrated helper refuses `capacity` while
  answering `list`.

**9. A zero-storage install renders NOTHING on Home.** `DashboardPage.tsx:57-64` hides the whole
Storage section when the list is empty or the query failed; there is no empty-state placeholder (the
Devices section has one, `:41-47`). So `+ Add storage` cannot live *inside* a section that vanishes
in exactly the state it exists for.

**10. There is no onboarding state machine, no step enum, and no `GET /api/onboarding` index.** Two
unlinked signals: `OnboardingHTTPS.complete`, computed per request from the connection itself
(`handlers_onboarding.go:46-56`), and the first-run auth state — `needs_setup` / `needs_login` /
`authenticated`, which on the Go side is **three bare string consts and no type**
(`auth/service.go:23-25`). `AuthState` is the **TypeScript** union (`ui/src/lib/types.ts:136`); the
two are not the same artifact and only one of them is checked by a compiler. `authExempt` is **five
exact method+path strings** (`middleware.go:75-79`) with no prefix support.

**11. The onboarding path names its SUBJECT, not a position** — Operator ruling 2026-08-02,
contracts §1. And per [quince#558](https://github.com/novkostya/quince/issues/558), **§9 names no
numbered steps at all**; the ordinal is informal. So the issue's *"onboarding step 3"* is a concept
name. The route is `/onboarding/storage` and the endpoint, if one is needed, is
`GET /api/onboarding/storage`. **`step3` would anchor the product's one pre-auth precedent to an
ordinal nobody has fixed.**

---

## Design

### 1. `storage.Inspect` — a report, not a resolution

New, in `core/internal/storage/`. It **never creates**, **never mints a marker**, and **never
constructs a `Backend`**. `Select` **creates and constructs** — `probeNamespace`'s `MkdirAll`
(`probe.go:83`) and a live `Backend` on every return path — which is why it is the wrong tool.

**`Select` does NOT mint a marker, and an earlier draft of this line said it did.** Measured:
`WriteStorageMarker` has exactly one non-test caller, `creation.go:206` inside `ResolveStorage`, and
`probe.go:30-69` contains no marker reference at all. The correction matters beyond the sentence —
an implementer who believes markers are minted on the probe path will model storage creation
wrongly, and that is storage semantics rather than prose (quince#687 review).

```go
// Inspect reports what a candidate storage path IS, without changing it.
func Inspect(path string, zfs InspectZFS) Report
```

`Report` carries, at minimum: the path as given and as cleaned; `exists`, `is_dir`, `writable`;
`marker` (present / absent / corrupt, and the `StorageMarker` when readable); `recommended` backend
with the **existing reason sentence**, which already names the path it probed (quince#514,
`probe.go:74-81`); the ZFS tier (below); and `free`/`total` from `FilesystemSpace`.

Order, and the order is the contract:

1. `Clean` and require absolute — the same two rules the config enforces, so the form's refusal and
   the config's refusal cannot disagree. They are **separate sites**: the absolute check is
   `validate.go:99-100`, and `filepath.Clean` is `:102`, in the `default:` arm that also does the
   duplicate-path comparison.
2. `Stat`. Missing → **refuse and stop.** Not a directory → refuse and stop. This is where
   `MkdirAll` would have been, and it is deliberately not there.
3. `ReadStorageMarker`. Present and valid → **adopt branch**; the recommendation is not offered at
   all, per fact 5. Corrupt → its own branch, reporting `ErrStorageMarkerCorrupt` rather than
   collapsing into "absent".
4. Writability, by attempting the probes rather than by reading mode bits — the probes *are* the
   writability test, and a mode-bit answer would disagree with them under ACLs and user namespaces.
5. The namespace probes, reused verbatim: `ReflinkProbe` → `hardlinkProbe` → copy.
6. The ZFS tier.

**Adopt short-circuits before the probes run.** A path holding a marker is not offered a backend
selector — its backend was decided at its creation moment. This is also the replug story for free:
the disk you forgot, plugged back in, re-added.

### 2. The ZFS tiers — three signals, one hard constraint

| tier | signal | what it means |
| --- | --- | --- |
| 1 | `statfs` `f_type` is the ZFS magic (fact 1) | **this path is ZFS** → recommend `zfs` |
| 2 | `/proc/filesystems` contains `zfs` | the module is loaded on the shared kernel → *this host has ZFS* hint |
| 3 | neither | say nothing |

**THE CONSTRAINT: tier 3 must NEVER render as "ZFS not supported."** In hook mode the container has
no `zfs` binary at all (fact 3) and zfs works perfectly through the host helper, so a negative probe
is a **guaranteed false negative for an entire supported topology** — in fact for *the* supported
containerised topology. *"Not detected"*, or silence. Never a capability claim. This is the
no-silent-caps rule pointed the other way: **do not assert an absence you cannot observe.** G-tier3
asserts the string is absent from the built UI bundle, not merely from one rendered state.

**Tier 2 is a hint with no action attached** — *"This host has ZFS. A storage on a ZFS dataset gets
snapshot versioning with no copy at commit — see `deploy/storage.md`."* Worth showing because it is
the largest difference between backends and nothing else in the product would ever mention it.

**`zfs list` is not a tier**, and this is where fact 3 lands. The issue's tier 2 was *"`zfs list`
succeeds → zfs reachable and `mode: exec` will work"*. In the shipped image `zfs list` can never
succeed, so that tier would be dead code that always answers no — and, worse, it would be the
evidence behind a rendered negative. Dropped. `/proc/filesystems` replaces it: it answers the
question tier 2 was actually for (*does this host have ZFS at all*) and it works in the shipped
image.

### 3. The zfs branch of the form: `hook` first, and `parent_dataset` asked rather than derived

Only one of `ZFSConfig`'s four keys is a real user choice.

- **`seed` does not go in the form.** `auto|reflink|copy`, and its own schema comment says *"In hook
  mode the host-side `seed` verb does the reflink and this is moot"* (`schema.go:178-181`).
  Config-file-only advanced key.
- **`mode` defaults to `hook` in the form, against `Resolved()`'s `exec`** (facts 3, 8 — the
  disagreement ended when quince#793 made `hook` the loader's default too). The form
  says why in one sentence, and does not present `exec` as a peer: *quince can't run `zfs` from
  inside the container. That's normal — it will call a helper on the host instead.*
- **`parent_dataset` is ASKED, and validated by firing the helper.** The issue wanted it derived.
  Derivation needs `zfs` in the container (fact 3) or an unmeasurable flag behaviour (fact 2), so it
  is descoped — **and its replacement is stronger.** Derivation would prove that the *filesystem*
  agrees. Firing `list <typed parent>` proves that **the helper agrees**, which is the thing that
  actually breaks: the key, the forced command, and the operator's baked-in `$PARENT` all have to
  line up, and none of them is observable from the path.

**`Test helper` fires TWO verbs, and the pair is what makes the answer specific** (fact 8):

| `capacity` | `list <typed parent>` | what the form says |
| --- | --- | --- |
| ok | ok (incl. empty) | **helper reachable, parent matches.** Ready. |
| refused | ok | **helper works but is not migrated** — add the `capacity)` case (`deploy/storage.md`), or storage cards will read *free space unavailable*. |
| ok | refused | **the parent dataset does not match the helper's `$PARENT`.** The typed value is wrong, or the helper's is. |
| both refused / transport error | — | **the key, the forced command or the host is wrong.** Show the transport's own stderr. |

**Empty `list` output is success.** Stated in the spec and asserted by a gate, because getting it
backwards fails on first run and only on first run.

**This partly answers [quince#591](https://github.com/novkostya/quince/issues/591)'s complaint** —
*"we can't ask users to place this script on their host."* We still ask. The form now tells them
immediately whether it worked, instead of a failed multi-hour transfer telling them at commit time.

### 4. The three branches off the probe

| probe result | form |
| --- | --- |
| **marker present** | **adopt** — no selector; show backend, `created_at`, and what it holds |
| **exists, writable, no marker** | recommend from the probe; selector overridable; zfs fields only when zfs is chosen |
| **missing / not a dir / unwritable** | **refuse with the reason.** *Create it* is a separate, explicit click — never a side effect of checking |

**The refusal is where the container sentence belongs.** Nothing quince can do fixes a disk that is
not mounted into the container, and the probe's failure message is the only place a user is looking
when it happens.

**Two fields deliberately not asked for.** **Name** — `Resolved()` defaults it to `Path`
(`schema.go:136-138`, ruled 2026-08-01, quince#504); offered as an optional disclosure for
multi-disk users, never required. **Default** — `ResolveStorages` implies it for a single entry
(`schema.go:167-169`), and a later storage must not steal it. Both already decided; nothing to ask.

### 5. The endpoints

Three routes, mirroring shapes that exist.

```
POST /api/storages/probe        {path}                        → 200 {probe} | 422
POST /api/storages/probe/hook   {parent_dataset, hook_cmd}     → 200 {result} | 422
POST /api/config/storage        {name?, path, backend, zfs?}   → 200 {config, warnings, source} | 422
```

**Why the add is a narrow route and not `PUT /api/config`** — the identical argument that decided
Forget (contracts §1, gap B): it splices **server-side**, so it cannot drop a sibling entry's `zfs:`
or `retention:` keys, which no UI surface renders and a reconstructed full document silently resets.
`config.AddStorage` mirrors `ForgetStorage`: read-modify-write under `s.writeMu`, delegating to
`replaceLocked` (fact 7).

**Refusals, and the order is inherited rather than re-decided.** Declaration refusals outrank
transient ones — contracts §1, *"THE ORDER IS PART OF THE CONTRACT"*. Here: schema validation
(`validate.go:78-142`: absolute path, duplicate name, duplicate cleaned path, backend enum, zfs mode
and seed enums, exactly-one-default) → `CheckStorageBackends` (`storagereq.go:181-218`: zfs intent
with no parent dataset, and **two storages on one parent dataset**) → the write. All `422
{errors:[{path,message}]}` at `storage[i].<field>`, the shape a client already renders.

**The add endpoint does NOT get its own `CheckStorageBackends` call — it inherits one, and this
paragraph asserted the opposite until quince#687's review.** `AddStorage` mirrors `ForgetStorage`,
which delegates to `s.replaceLocked(next)` (`forget.go:133`), and **quince#683's ruling puts the
check in `replaceLocked`**. So the add path is covered the moment that lands, `PUT /api/config` is
covered by the same edit, and **both doors become one door**.

The draft said the add endpoint runs the check itself and that *"this rung closes it for one route;
quince#683 stays open"*. That was written from the issue and not from the ruling, which had landed
ninety minutes earlier — a spec asserting an outcome a ruling had already changed. Recorded rather
than silently corrected, because the same mistake is available to any PR in this rung: **the issue
is the input, the ruling is the state.**

**The dependency, stated so PR 5 is buildable either way.** If quince#683's fix has landed when PR 5
is written, PR 5 adds no check and asserts the inherited one. If it has not, **PR 5 lands the
`replaceLocked` edit itself** — that is what *"before or with `qn.6e`"* permits, and it is one call
beside an existing one rather than a new mechanism. Either way the ruling's condition travels with
it: the message must name **both** storages and the shared dataset, matching the duplicate-name and
duplicate-path messages in shape and tone, which means adapting `CheckStorageBackends`' bare strings
to `wire.ConfigError{Path, Message}` rather than mechanically wrapping them.

**A per-route call as defence in depth was considered and is NOT wanted.** Two call sites for one
invariant is how they diverge, and the reason `main.go:208` keeps its startup call is that it covers
a path `Replace` never sees — a hand-edited file — not that duplication is good.

### 6. `auto` — RULED: absorbed, not removed

Operator, 2026-08-07 (quince#502): *"I don't care as long as I'm the only user of quince, do whatever
is the easiest."*

**The loader does not change.** `Resolved()` keeps defaulting `Backend` to `"auto"`
(`schema.go:139-141`). What changes is that **the add flow writes the concrete value it just
showed** — never the empty string, never `auto`. The form has a selector, so an override must be
representable, and omitting the key would silently discard it.

`auto` is already gone from everywhere a user can see: `ConfigEditor.tsx:149-151` (the global
backend select *"no longer exists"*) and `types.ts:246`, where the **wire** `Storage.backend` cannot
express it. Only the **config** type admits it.

**What is NOT reachable, stated so nobody builds it later:** *"no `auto` in `config.yml`"*. The
startup refusal teaches the one-line form itself (`storagereq.go:138-145`):

```
    storage:
      - path: /backups
```

— which **is** `auto`. Removing it would break the shortest declaration in the product, taught by
quince's own error message.

**THE REACHABLE GOAL IS NARROWER THAN THIS SPEC FIRST STATED, and the difference is measured.** It
read: *"quince never writes `auto`; a human still may."* **The first half is false**, and it was
false before this rung began.

`replaceLocked` marshals the whole **resolved** document (`Marshal(c)` over `s.Current()`, which
`Parse` already ran `ResolveStorages` on), so **any save materialises every default for every entry,
including ones the user never touched**. A hand-written minimal declaration —

```yaml
    storage:
      - path: /backups
```

— acquires `backend: auto` and `zfs.seed: auto` the first time *anything* writes the config. Measured
through `AddStorage`; the path is `replaceLocked` → `Marshal`, which `ForgetStorage` and
`PUT /api/config` share identically, so this is not the add flow's doing and predates it.

**What is actually reachable, and what the rung genuinely delivers:** *a storage ADDED through the
flow records the concrete backend that was probed and shown, and an omission is refused rather than
defaulted.* `validateAddition` refuses an empty or `auto` backend outright, so an omission cannot
become an `auto` by the back door — which is the property that mattered. The broader sentence was a
proxy for it, and the proxy was wrong.

**Nothing is broken and nothing is proposed here.** The materialised value is identical in meaning to
the omitted one and `auto` is legal by ruling, so this is a false claim rather than a defect.

**THAT LAST JUDGEMENT WAS OVERTURNED AND THE BEHAVIOUR ABOVE NO LONGER HAPPENS.** Operator ruling
2026-08-08 on [quince#728](https://github.com/novkostya/quince/issues/728): `config.yml` contains
**only what was set**. So *"any save materialises every default for every entry"* became a **defect
to fix** rather than a true-but-harmless observation — semantic equivalence was the wrong test,
because D12 makes the file's content the product's stated interface. `qn.6j` implements it; the
measurement above is kept unedited, because it is correct and it is what the ruling was taken on.

**`TestSavingMaterialisesAutoForPreexistingEntries` is deleted in the same diff as this edit**, which
is what its own failure message demanded: *"if quince genuinely stops materialising `auto`, delete
this test AND fix the qn.6e sentence in the same diff."* It failed on the `qn.6j` PR that stopped the
behaviour, which is the test working exactly as designed rather than a test that went stale.

**The narrower goal this spec claimed still stands and is untouched**: a storage ADDED through the
flow records the concrete backend that was probed and shown, and `validateAddition` refuses an empty
or `auto` backend outright. `qn.6j` changes nothing about the add door — `docs/contracts.md` §6 says
why the two doors take opposite policies.

**This discharges the placeholder's one homed deferral by absorption.** Do not open a removal PR
against it. Two canon sites assert the deferral is still live and both are corrected in PR 8:
`contracts.md:1339-1344` (*"homed on quince#502"*, *"nothing is built to compensate"*) and
`schema.go:76-81` (*"that half is DEFERRED to `qn.6e` … Do not remove it here."*).
`contracts.md:1407-1409` names *"whether `auto` removal ultimately sits in `qn.6e` or travels with
quince#443's add-storage flow"* as **not ruled** — it is ruled now, and that sentence goes with it.

**The placeholder's caller concern is discharged with it.** quince#502's first comment warned that
`demo.StorageEntries` generates `{Name: "shuttle", Path: "/mnt/shuttle", Backend: "auto"}` and that
removing `auto` would make the demo refuse to start, taking `make gates-ui-e2e` with it. Because the
loader is unchanged, **there is nothing to do** — recorded rather than silently dropped, because a
warning that stops applying should say so where it was filed.

### 7. The two buttons

**`+ Add storage` at the foot of the Storage section**; **`Rescan` MOVES from the page header
(`DashboardPage.tsx:32`) to the foot of the Devices section** — Operator-ruled 2026-08-07. The page
header keeps only *"Home"*.

**Why Rescan moves rather than merely restyling.** It concerns Devices only, Home is two sections
now, and Storage has its own re-probe (`POST /api/storages/{name}/recheck`, surfaced at
`StorageProblem.tsx:49-59`) — so a page-level *Rescan* invites the reading that it does both.

**The idiom exists twice; reuse it verbatim including the `-ml-3`:**
`WifiSyncControl.tsx:61-72` and `JobHistory.tsx:64-72` (`Show all N`, ghost + `self-start`, no
`-ml-3` because it is a flex-column child, not aligned against a text margin).

**`WifiSyncControl` is the CONDITIONAL form and the values must be read off its `on` branch, not
copied flat.** All three props are ternaries — `variant={on ? "ghost" : "outline"}`,
`size={on ? "sm" : "md"}`, `className={on ? "-ml-3" : undefined}` — because that control deliberately
changes weight with direction. **A section-foot button has no such branch**, so it takes the ghost
half unconditionally: `variant="ghost" size="sm" className="-ml-3"`. The `-ml-3` is what the comment
at `:63-68` explains — a ghost button has no background, so its text sits at the size's `px-3` inset
while every neighbour's visible left edge is at the margin, and it reads as a stray indent.

**Do not put them in one row.** Rescan is idempotent and free; Add storage is a config write. Same
weight and size is right; adjacency implies they are the same kind of act.

**`RescanButton`'s hard-won behaviour must survive the move**: the label stays `Rescan` throughout
and only the icon spins, because *"Rescanning…"* is wider than *"Rescan"* and swapping it moved the
layout (quince#325, `RescanButton.tsx:22-71`). Note it is `variant="outline"` today; the move to a
section foot is where its variant is reconsidered, and G10 asserts the label rule regardless.

**`+ Add storage` must render when the storage list is EMPTY** (fact 9), which today hides the whole
section. That is the state the button exists for on a fresh install, so the section gains an
empty state rather than the button moving out of it.

---

## RULED (was `PROPOSED (gap)`): ANY zero-storage start IS the onboarding state — option (a)

**Operator ruling, 2026-08-07**, relayed by architect session `arch1` on
[quince#502](https://github.com/novkostya/quince/issues/502) — cite the issue and the self-declared
role rather than the login (quince#47). **PR 9 is no longer held.**

**What was decided.** quince may **serve with zero storages**, refusing every API except auth,
onboarding and config, and render the storage step instead of Home. **The hard refusal stays for
every other case.** No persisted onboarding flag, no step enum, no UI-only state: **the zero-storage
condition *is* the state**, which is why it needs nothing to remember it.

**The cost was named and ACCEPTED rather than discovered.** *Onboarding* and *misconfigured* are
byte-identical at startup, so a daemon started from a hand-edited `config.yml` whose `storage:` list
someone emptied gets the friendly page rather than the refusal. **Ruled not a state-honesty
downgrade**, because the page is true in both cases — there is no storage, and here is how to add one
— and the daemon becomes fixable from a browser instead of a shell.

**What the ruling does NOT license**, carried from the relay so it is not re-derived later:

- **Not a general relaxation of the startup gate.** Every other refusal in `CheckStorages` /
  `Explain` stands, the malformed-config case included (quince#508).
- **It does not make zero storages reachable through the API.** Forgetting the default is refused
  `422`, so only a hand-edit gets there — which is why the accepted edge is a hand-edit edge.
- **It says nothing about `auto`**, already ruled absorbed, or about interface fact 2, which stays
  unmeasured by design.

**The question as it was asked is kept below, because the reasoning that lost is what makes a ruling
checkable later.** (b) and (c) were rejected on this spec's own analysis of them.

**The blocking fact.** quince refuses to **start** with no storage. `main.go:201-204` →
`config.CheckStorages` → `StorageRequirement.Explain` (`storagereq.go:100-159`), closing:

> *"REFUSING to start. A quince that comes up with nowhere to put backups looks healthy and silently
> protects nothing, which is worse than one that did not start."*

And `/data/config.yml` does not exist on a fresh install. `deploy/compose.nas.yml:44-47` **bind-mounts
a host directory** — `./quince/data:/data` — rather than declaring a named Docker volume, and it
creates no config file in it; `Default()` deliberately carries no storage block
(`schema.go:293-297`). **So today's genuine first run is:** start container → **it exits** → read
stderr → hand-write YAML into `./quince/data/config.yml` beside the compose file → start again →
*then* onboarding.

**The bind mount is the one mercy in that sequence and is worth stating precisely**, because it is
what makes the hand-edit *possible* at all: the file is on the host filesystem next to
`compose.yml`, not inside a volume the user would have to `docker cp` into. An earlier draft called
it a Docker volume, which would have made the workaround materially worse than it is (quince#687
review). That is not the Plex bar design §9 promises, and it means
the storage step cannot be appended to onboarding: it must run **while quince has no storage**,
which is precisely the state the refusal exists to forbid.

**The proposed shape.** Serve with zero storages, refusing every API except auth, onboarding and
config, and render the storage step instead of Home. Keep the hard refusal for every other case.

**The argument.** The refusal defends against a *long-running* zero-storage daemon that looks
healthy. An onboarding daemon is not that: it already serves pre-auth with no password and no
session (`OnboardingHTTPSPage`, route outside every guard at `router.tsx:26`), and it would say, in
the UI, exactly what the refusal says on stderr. The exit message and the onboarding banner become
one claim in two channels rather than two policies.

**The sharp edge, stated rather than smoothed.** *Onboarding* and *misconfigured* are
indistinguishable at startup. A daemon started from a hand-edited `config.yml` whose `storage:` list
someone emptied is byte-identical, at the moment of the decision, to a fresh install — there is no
persisted onboarding flag and nothing to distinguish them (fact 10). Three ways out, and the choice
is the ruling:

- **(a) Any zero-storage start is the onboarding state.** No flag, no persisted step, no UI-only
  state — the zero-storage condition *is* the state, which is why it needs nothing to remember it.
  The cost: a misconfiguration is converted into a friendly page rather than a refusal. Arguably
  that is an improvement, since the page says the same thing louder and the daemon can be fixed from
  a browser instead of a shell. **The Operator's ruling would be that this is not a downgrade in
  state honesty, and that is the whole question.**
- **(b) Only when `auth.Status()` is `needs_setup`** — genuinely first run, nothing configured. Narrow
  and unambiguous. But design §9 puts the guided checks **after** the password, so the storage step
  would have to run in a window that closes the moment the password is set, and a user who sets a
  password and then reloads gets the refusal.
- **(c) Only when the config file does not exist**, as opposed to existing with an empty list.
  Distinguishes the two cases exactly, at the price of a rule about a file's existence rather than
  its content — and `Load()` already falls back to `Default()` on a parse failure, so *file absent*
  is not as clean a signal as it looks.

**One consequence is already closed and worth knowing:** the API cannot take a running quince back
to zero storages. Forgetting the default is refused with `422`, and on a single-storage install the
only storage *is* the default (contracts §1, `forget.go:95-109`). Only a hand-edit can, which is why
the edge above is a hand-edit edge.

**What the hold bought, recorded because it was the point of holding.** PR 9 was held and PRs 2–8
were written not to depend on it, so the rung was never blocked while the question was open — the
probe, the endpoints, the form and the buttons are all reachable on an install that already has a
storage, which is every existing install. **PR 9 is now buildable and the rung no longer ships with
its fresh-install half missing.**

---

## Stories

1. **A path that does not exist is refused, and still does not exist afterwards.** The probe reports
   the reason; nothing is created.
2. **A path that exists and is empty gets a recommendation with the reason that names it** — the
   probe's own sentence, e.g. *"FICLONE clone-sharing probe passed on /mnt/…"*, and the two probe
   dotfiles are gone when it returns.
3. **A path holding a `quince-storage.json` is an ADOPT** — the form shows the recorded backend and
   creation date and offers no selector, even when a fresh probe would say something else.
4. **A path on ZFS is recommended `zfs`**, from `statfs` alone, with no `zfs` binary present.
5. **A host with ZFS but a non-ZFS path shows the hint** — and a host with no signal shows nothing.
   **Nowhere does the product say ZFS is unsupported.**
6. **`Test helper` distinguishes the four outcomes of fact 8**, and an empty `list` on a correct
   parent is reported as success.
7. **Adding a storage from the form makes it live** — it appears in `GET /api/storages`, a job can
   target it, no restart. If its root already holds committed backups, they are visible.
8. **The entry written to `config.yml` carries a concrete backend**, never `auto` and never absent —
   asserted on the file.
9. **A second storage on the same `zfs.parent_dataset` is refused `422`** before anything is
   written, naming the collision.
10. **The buttons are where the ruling puts them** — `+ Add storage` at the foot of Storage
    (including when there are no storages), `Rescan` at the foot of Devices, page header only
    *"Home"*, and Rescan's label never changes while it spins.
11. A fresh install with no `config.yml` serves the storage step, and adding
    a storage from it leaves a running quince with a working storage and no restart.

---

## Gates

| id | what it proves | where |
| --- | --- | --- |
| **G1** | `Inspect` on an absent path refuses **and the path is still absent afterwards** — asserted with `os.Stat` on the filesystem, not on the report. The anti-`MkdirAll` gate; the rung's central claim. | CI (Go) |
| **G2** | `Inspect` on an existing writable dir returns a recommendation whose reason names that dir, and **the directory is byte-for-byte as it was** — no `.quince-*` residue. | CI (Go) |
| **G3** | `Inspect` on a dir holding a valid marker returns adopt, with the marker's backend, **even when the live probe disagrees**; a corrupt marker is its own outcome, not "absent". | CI (Go) |
| **G4** | `statfs` tier: a fake/injected `f_type` yields the zfs recommendation with no `zfs` binary on `PATH`. Plus a **host gate** re-running the fact-1 measurement in the built image. | CI (Go) + host |
| **G5** | `POST /api/config/storage` → `GET /api/storages` lists it in one process, a job can target it, and a root holding committed versions has them visible. No restart. | CI (Go) |
| **G6** | The add's refusals, **each asserted on the config FILE being unchanged**, not on the response: non-absolute path, duplicate name, duplicate cleaned path, bad enum, and **two storages on one `zfs.parent_dataset`** — the last of which is quince#683's own owed reproduction, reached through `Replace` and therefore covering `PUT /api/config` in the same assertion. A refusal that still writes is what this gate exists to catch. | CI (Go) |
| **G7** | The written entry's `backend` is one of `zfs\|reflink\|hardlink\|copy` — read back from the YAML, for every branch including an explicit user override. | CI (Go) |
| **G8** | `Test helper` against **the real `quince-zfs-helper` script**, with `zfs` stubbed: all four outcomes of fact 8, and **empty `list` output reported as success**. | CI (Go) |
| **G9** | **The string "ZFS not supported" (and its variants) appears nowhere in the built UI bundle** — asserted against the build output, not against a rendered state, because tier 3 is the state that never renders in a test. | CI (UI) |
| **G10** | The buttons: `+ Add storage` at the foot of Storage **with zero storages declared**, `Rescan` at the foot of Devices, page header text is only *"Home"*, and Rescan's accessible name is unchanged while pending. **SPLIT with PR 7: the Rescan half is `story10-home-actions.spec.ts` and is GREEN; the add half travels with PR 6.** | ui-e2e |
| **G11** | The add flow end to end in the demo: type a path, Check, see a recommendation and its reason, save, see the card. | ui-e2e |
| **G12** | A daemon started with no storage serves the storage step and refuses the other APIs; adding one from it needs no restart. | CI (Go) + ui-e2e |
| **G13** | `make privacy-check REF=origin/main...HEAD TEXT=<file under $HOME/scratch/<runner>/>` | host |

**G1 and G6 are the gates this rung stands or falls on**, and both assert on the **filesystem**
rather than on a response. The two ways this rung can be wrong are *the probe changed the disk* and
*a refusal still wrote the config*, and in both the API answer looks right.

**G8's design is a direct answer to `qn.6d`'s sharpest finding.** quince#593 read zfs capacity by
sending flags through a forced command, which discards them and exits 0 — and **no gate could have
caught it, because the tests stub the transport, so they assert the argv SENT and the stub answers
whatever the test chose: *a mirror, not a peer***. So G8 does not stub the helper. It runs the real
script with a stubbed `zfs`, which means the forced-command semantics — `$SSH_ORIGINAL_COMMAND`
splitting, last-arg targeting, the `case` guards, the `exit 1` fall-through — are genuinely
exercised.

**The script is EXTRACTED from `deploy/storage.md` at test time**, not copied into `testdata/`.
A second copy would drift, and the doc is the artifact operators actually install. The cost is that
the extraction depends on the fenced block's position; **the gate fails loudly when extraction finds
nothing** rather than skipping, so a doc edit that breaks it is a red build and not a silent pass.
**Rung-local, and reversible to a `testdata/` copy if extraction proves brittle** — recorded because
the tempting version is the copy.

**No hardware gate is owed, and this is declared up front** (as `qn.6g` did, and as `qn.6c`'s G9 and
`qn.6d`'s question 4 did not). No device is driven. **One host measurement is owed** — G4's
re-measurement of fact 1 in the built image — and it runs on a session box, not on hardware.

**Interface fact 2 stays unmeasured at rung close**, by design: nothing in this spec depends on it.
If a later rung wants derivation, that measurement is its first gate and its owner is whoever has a
box with `zfs` userland.

---

## Fixtures

**New: `Inspect` fixtures over `t.TempDir()`** — absent path, empty dir, dir with a valid marker, dir
with a corrupt marker, unwritable dir. No transcripts; this rung drives no device.

**New: the helper harness (G8)** — the script extracted from `deploy/storage.md`, run under `/bin/sh`
with a stubbed `zfs` on `PATH` and `SSH_ORIGINAL_COMMAND` set, wired to the hook-test code path as
its transport. This is the fixture that is a **peer rather than a mirror**.

**Demo:** the add form's ui-e2e (G11) needs a path the demo container can probe. `--demo` fabricates
its storages, and `qn.6d`'s requirement is inherited verbatim — *a fixture that fabricates a value
the live code never produces makes its gate a lie.* G11 therefore probes a **real directory inside
the demo container**, so the report is produced by `Inspect` and not by the demo provider. The demo's
own `/mnt/shuttle` entry stays unreachable and untouched: it is how a visitor sees the unreachable
card.

---

## Rule check

Written before building. Every rule this rung touches **or comes near**, near-misses included.

| rule | how this plan complies |
| --- | --- |
| **A rung starts from a spec** | This document, PR 1, reviewed before any code exists. |
| **Don't improvise architecture** | One gap — the zero-storage startup refusal — went up as `PROPOSED (gap)` with three options and no preference asserted as ruled, with **PR 9 held** on it and PRs 2–8 written not to depend on it. **RULED 2026-08-07, option (a)**; nothing was built on it while it was pending, and the flip narrowed the heading in the same diff that changed the body (quince#408). Descoping `parent_dataset` derivation is not a gap: it is a measurement (facts 2, 3) recorded in the spec, rung-local. |
| **Contracts are stop-and-ask** | **Three new routes** — this is the rung's largest contract surface and it is deliberate, not incidental. They land in contracts §1 in the PRs that build them, and `wire` gains a probe object (§2). Shapes and refusal ORDER are inherited from Forget rather than re-invented, so no ruled question is reopened. PRs 3, 4, 5 and 8 were therefore code-owned when this rung ran; **of those only PR 8 would be today**, for its `design.md` §9 half — `contracts.md` left `CODEOWNERS` on 2026-08-14 (quince#953). |
| **Never mutate a committed version** | **The rung's sharpest near-miss.** The probe writes to a path the user typed. `Inspect` never creates (G1), refuses before touching anything absent, and the two dotfile probes clean up (G2, fact 6). It **never mints a marker** — creation stays `ResolveStorage`'s, at the creation moment, once. An adopt path is never re-probed into a new backend (fact 5, G3). No `latest/`, `versions/` or `working/` tree is read or written by anything in this rung. |
| **State honesty** | The recommendation reports what was probed, with the probe's own path-naming sentence. Tier 3 says *not detected*, never *not supported* (G9). `Test helper` distinguishes four outcomes rather than ok/failed, and an empty `list` is success rather than a false negative. The zfs branch says `exec` cannot work in the shipped image instead of offering it as a peer. |
| **No silent caps or fallbacks** | A `copy` recommendation stays the loudly-surfaced degraded mode it already is (`probe.go:62-64`). The un-migrated-helper outcome is named rather than folded into "failed". quince#683 is declared as **still open** rather than implied closed by G6. |
| **Config tidiness (D12)** | **No new key.** The rung writes existing keys with concrete values and adds no UI-only state — the probe result is computed, never stored. Every field the form writes is already documented in `schema.go` with a doc comment and a default. |
| **Never mutate a committed version → config's analogue** | Every refusal is asserted against the **config file**, not the response (G6). A `422` that still wrote is the failure mode this rung could most plausibly ship. |
| **Privacy is a commit-time gate** | **Storage paths and a hook argv are the sharp surfaces.** Fixtures use `t.TempDir()`, `/backups`, `/mnt/shuttle`; no lab topology, no dataset name, no real mount point, and the fact-1 measurement above names no host directory. **`hook_cmd` contains `user@host` and must never be echoed into a response body, a log line, an error message or a test fixture** — the hook test reports the transport's *stderr* and its own verdict, not the argv it ran. `TEXT=` takes a **path** to a body file under `$HOME/scratch/<runner>/`, never inline prose and never a fixed `/tmp` path. |
| **Secrets discipline** | No backup password is on any path here. **Declared near-miss:** `POST /api/storages/probe/hook` **executes a request-supplied argv.** It adds no capability an authenticated admin lacks — `PUT /api/config` already stores a `hook_cmd` that quince execs at the next job — but it shortens the loop from *next backup* to *now*, and that is worth stating rather than discovering. It is behind `authGuard` and `csrfGuard` (`server.go:120`), **adds nothing to the five-entry exempt set** (`middleware.go:75-79`), and the argv is never logged. If the Operator wants it narrowed to the saved config's `hook_cmd`, that is a spec-review call and it costs the form its usefulness before the first save. |
| **Subprocesses: argv arrays, own process group, supervised, killed on job end** | The hook test spawns one. It reuses `zfsCLI`'s existing argv-array execution — **never a shell string** — with a bounded timeout and a killed process group, so a hung `ssh` cannot leak a goroutine or a child per click. |
| **Every hardware bug becomes a fixture** | No hardware gate, so nothing is owed — stated rather than left blank. |
| **Docs are part of the diff** | contracts §1/§2 land with the routes; `schema.go:76-81`'s *"do not remove it here"* and `contracts.md:1339-1344`/`:1407-1409`'s deferral land in the PR that makes them false; design §9 gains the storage check; `deploy/storage.md` gains a line that the form fires `capacity` and `list` and what each proves. |
| **Coverage declared** | Every code PR carries `go test -cover` plus a known-untested list. Expected standing entries: the corrupt-marker branch under a concurrent write, and the hook-test transport-error branch, which needs a real unreachable host. |
| **A rung's goal is provable at rung close** | G1–G11 run in CI or ui-e2e at rung close. G12 is held with PR 9. G13 is per-PR. **Interface fact 2 is declared permanently unmeasured here**, with its owner named. |
| **Approver ≠ author** | Implementer authors. **PRs 3, 4, 5 and 8 were code-owned when this rung ran** — they touch `docs/contracts.md` and `docs/quince.design.md`. **Only `design.md` is an owned path now**, one of `CODEOWNERS`' five: `contracts.md` left it on 2026-08-14 (quince#953), so of those four only PR 8 would need `@novkostya` today, and an App verdict still cannot satisfy that because an App cannot be a code owner. **This spec and PRs 2, 6, 7 are the architect's to approve**: `/docs/specs/**` is deliberately not owned. |

---

## Rung-ruled decisions

1. **`Inspect` is a new function, not a flag on `Select`.** `Select` constructs a `Backend`, and a
   form must not. Threading a `dryRun bool` through a constructor is the version that looks cheaper
   and leaves the `MkdirAll` one boolean away from a user-typed path.
2. **The `wantZFS` predicate is factored once** and the two existing copies (`probe.go:31-32`,
   `storagereq.go:191-192`) call it. Recorded because the cheap move is a third copy, and three
   copies of a predicate that decides which backend a disk gets is how they silently diverge.
3. **The onboarding route names its subject: `/onboarding/storage`, never `step3`** — contracts §1's
   2026-08-02 ruling, and quince#558's finding that §9 numbers nothing.
4. **`+ Add storage` renders in an empty Storage section**, which means the section gains an empty
   state. The alternative — hoisting the button out of the section — puts a config write in the page
   header, which is the placement this rung is removing Rescan from.
5. **The G8 helper script is extracted from `deploy/storage.md`, not copied into `testdata/`**, and
   the extraction fails loudly rather than skipping. Reversible.
6. **The hook test fires `capacity` then `list`, in that order.** `capacity` takes no argument, so a
   failure there is unambiguously about reachability; running it first means the second call's
   failure can only be about the parent.

---

## Known gaps and open questions

1. **The zero-storage startup refusal** — **RULED 2026-08-07, option (a)**: any zero-storage start
   IS the onboarding state. No longer open, and **PR 9 is no longer held**. The block above carries
   the ruling, what it does not license, and the losing options as they were argued.
2. **A non-empty directory with no marker** — someone else's data. Legal today (quince creates
   `<udid>/` beneath it) and the probe would recommend a backend for it happily. Warn and allow, or
   refuse? **Rung-local, settled in PR 2**, flagged here because the obvious implementation does
   neither: it says nothing. Note the pre-`qn.6c` upgrade case is the reason "refuse" is not
   obviously right — a path holding real backups from before storage markers existed has no marker.
3. **Interface fact 2 is unmeasured and stays unmeasured.** Named so a later rung that wants
   `parent_dataset` derivation starts by measuring it rather than by reading this spec's descope as
   a verdict on the flag.
4. **quince#683 is RULED, not open, and this rung depends on it rather than working around it.**
   The check goes in `replaceLocked`, so the add path and `PUT /api/config` close together. PR 5
   lands that edit if it has not landed already. This item read *"quince#683 stays open — G6 closes
   the hole for the add route only"* until the review pointed at a ruling that predated the spec.
5. **`zfs.mode: exec` is undeployable with the shipped image** (fact 3) while `Resolved()` still
   defaults to it. This rung works around it in the form and does **not** change the default, which
   would be a config break. **FILED AS [quince#697](https://github.com/novkostya/quince/issues/697).**
   This line said *"worth its own issue; filed with PR 2"* while no issue existed and PR 2 had
   merged — quince#320's defect in miniature, since a deferral aimed at nothing is one nobody can
   pick up. The issue carries the two measurements and three candidate shapes, and rules none.
   **CLOSED 2026-08-10 — none of the three.** The Operator removed `exec` (quince#793); `hook` is
   the only value and the default. The config break this item declined to take was taken
   deliberately, as a refusal that names the key rather than a silent re-default.

---

## PR slicing

Each carries one reviewable claim and its own proof. **Branch each from `main` — do not stack**
(canon §1).

| | claim | proof | owner |
| --- | --- | --- | --- |
| **1** | **This spec.** | review | architect |
| **2** | **`storage.Inspect`** — non-creating inspection, adopt branch, recommendation, the `statfs` ZFS tier, and the factored `wantZFS`. No HTTP. | G1, G2, G3, G4 | architect |
| **3** | **`POST /api/storages/probe`** + the wire object. contracts §1/§2. | Go handler tests | **`@novkostya`** |
| **4** | **`POST /api/storages/probe/hook`** — the two-verb helper test and its four outcomes. contracts §1. | **G8** | **`@novkostya`** |
| **5** | **`POST /api/config/storage`** — `config.AddStorage` mirroring `ForgetStorage`, the refusal order, a concrete backend always. **Carries quince#683's `replaceLocked` edit if that has not landed first**; adds no per-route check either way. contracts §1. | G5, G6, G7 | **`@novkostya`** |
| **6** | **The add form** — one field, Check, three branches, the zfs sub-form. | G9, G11 | architect |
| **7** | **`Rescan` moves** to the foot of Devices; the page header keeps only *"Home"*. **MERGED** as quince#695. Independent of 2–6, and it went first. | G10, Rescan half | architect |
| **7b** | **`+ Add storage`** at the foot of Storage, empty state included. **MOVED INTO PR 6** — see below. | G10, add half | architect |
| **8** | **Canon: `auto` is absorbed** — `contracts.md:1339-1344` and `:1407-1409`, `schema.go:76-81`, design §9's guided-check sentence, `deploy/storage.md`'s note on what the form fires. | review | **`@novkostya`** |
| **9** | **The first-run path** — the zero-storage serve mode and the onboarding storage step, at `/onboarding/storage` (decision 3). **Released 2026-08-07 by the ruling**; was held. | G12 | architect |

**Order.** 2 → 3 → 4 → 5 → 6. **7 was independent and went first**, which was worth doing: smallest,
entirely UI, and it settled the placement the form lands into. 8 lands with or after 5, because it
must not claim `auto` is absorbed before the code that absorbs it exists — `CLAUDE.md`'s most-filed
defect, in the direction that matters least but is still wrong.

**WHY PR 7 SPLIT, recorded because the split contradicts what this table said when it was written.**
The row read *"the two buttons move"*, and quince#695 moved one. **`+ Add storage` has nowhere to go
until the form exists**, and a button that opens nothing is a dead affordance — in a rung whose whole
subject is not asserting what the product cannot support. The empty state it needs is also
undesignable in isolation, because its copy points at the button.

The original argument for bundling them — that PR 7 *"settles the placement the form has to land
into"* — still holds for the half that has somewhere to go, and that half landed. So the cost is a
row in this table, not a design decision.

**Sequencing, not stacking, is why 4, 5, 8 and 9 wait.** Each touches `server.go` or
`docs/contracts.md`, and PR 3 (quince#696) touches both — so branching any of them from `main` while
it is open guarantees a conflict, and branching from *its* head is stacking, which quince#388 ruled
against. Canon accepts exactly this trade: sequencing costs one review cycle, stacking can cost the
pull request. Recorded here because the table reads as a set of independent items and four of them
are not.

**9 no longer gates the rung and no longer risks it either.** This read *"if the gap is unruled at
close, the rung ships with the add flow working on every install that already has a storage, and
says plainly that the fresh-install path is the half that is missing."* The ruling landed on
2026-08-07, so that contingency is spent — but the slicing it produced is what made it survivable,
and that is the part worth keeping: **9 was written last and depended on by nothing**, so an unruled
gap would have cost the rung one story rather than the rung.
