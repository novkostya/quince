# qn.9 — Overview: what is IN this backup, rather than which files it holds

**Goal.** Opening a committed version shows what the backup *is* — the device it came from, when it
was taken, whether it is full, which apps are in it and how much space each takes — instead of a
file tree the reader has to interpret.

Rung issue: **quince#1432**. Three questions were open at scoping and **all three are ruled**; each
is transcribed in full at the decision it binds, because one of them was given in session and this
spec is the only place it survives.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `ios-backup-crypt` | the pre-unlock fields (D2), `FileCount`'s ambiguity (D5), and the aggregate scan (D4) |
| `core/internal/vault/` | the aggregate on the seam, both implementations, the capability report (D6) |
| `core/internal/vault/parserfs/` (new) | `backup.FS` over a vault session, so the parser can read this backup (D7) |
| `core/internal/httpapi/` | `GET /api/sessions/{id}/overview` and the pre-unlock read (D3, D11) |
| `core/internal/wire/` | the overview objects, inside the frozen domain envelope |
| `ui/src/features/overview/` (new) | the surface |
| `docs/contracts.md` | §1 the two routes, §4 the aggregate method and the capability shape |

**Out of scope**, each decided out rather than missed:

- **The file browser is NOT replaced.** See D9. This is stated as its own decision because the
  remark that opened the rung — *"this is just test UI, it's not going to be in the end product"* —
  reads as *remove it* and must not.
- **Domain viewers.** Rendering a typed view of messages/contacts/etc. is `qn.10+`. Overview names
  what a domain *could* serve; it serves none of it.
- **The browse-pagination index defect (quince#1444).** Found while measuring this rung and belonging
  to `qn.8`. D4 makes overview independent of it rather than waiting on it.
- **Search.** Unchanged from `qn.8`: session-scratch FTS5 arrives with messages.
- **Anything derived that outlives a session.** Contracts §5 stays dormant; D6's cache is in memory
  and dies with the session.

---

## Interface facts — measured 2026-08-22 on the staging stand, not recalled

Every figure below is from the Operator's two real backups, read on the stand inside
`golang:1.26.5-alpine3.24` (the pinned `GO_IMAGE`). **The personal data never left that box.** What
is reproduced here is counts, byte sizes, timings and plist *key names* — the last are facts about
Apple's format, not about a device. No identifier, path or bundle id appears in this document, and
none may enter one.

Two devices, called **A** (iPad) and **B** (iPhone) throughout.

**1. Three plists are readable with no password, and they cost three different amounts.**

| file | A | B | format | parse |
| --- | --- | --- | --- | --- |
| `Status.plist` | 189 B | 189 B | binary | **11–39 µs** |
| `Manifest.plist` | 267,943 B | 429,015 B | binary | **0.83 / 1.6 ms** |
| `Info.plist` | 904,456 B | **9,515,132 B** | XML | **10.5 / 99.3 ms** |

**2. `Status.plist` carries six keys**: `BackupState`, `Date`, `IsFullBackup`, `SnapshotState`,
`UUID`, `Version`. **`IsFullBackup` is a product fact quince cannot show today** and costs
microseconds.

**3. `Manifest.plist` — the file the decrypt path ALREADY parses — carries far more than the four
keys the library reads.** Top level: `Applications` (dict of **1203** / **1955**), `BackupKeyBag`,
`Containers` (**1205** / **1957**), `Date`, `IsEncrypted`, `Lockdown`, `SystemDomainsVersion`,
`Version`, `WasPasscodeSet`. `Lockdown` holds **seven scalar fields**:

```
BuildVersion  DeviceClass  DeviceName  ProductType  ProductVersion  SerialNumber  UniqueDeviceID
```

**`ios-backup-crypt` reads two of them** (`DeviceName`, `ProductVersion`). The other five are
available at **zero extra I/O** — same file, same parse, already on the pre-unlock path.

**4. `Info.plist` carries the user-installed app list and, on a phone, the sharpest identifiers in
the format.** Keys: `Applications`, `Installed Applications`, `Device Name`, `Display Name`, `Build
Version`, `Product Type`, `Product Version`, `Serial Number`, `GUID`, `Target Identifier`, `Target
Type`, `Unique Identifier`, `Last Backup Date`, `iTunes Files`, `iTunes Settings`, `iTunes Version`
— **and on B only: `IMEI`, `ICCID`, `Phone Number`.** `Installed Applications` is an array of
**21** (A) / **167** (B) bundle ids.

**5. The Files table has no index quince's browse ordering can use.**

```
CREATE TABLE Files (fileID TEXT PRIMARY KEY, domain TEXT, relativePath TEXT, flags INTEGER, file BLOB);
CREATE INDEX FilesDomainIdx ON Files(domain);  FilesRelativePathIdx ON Files(relativePath);  FilesFlagsIdx ON Files(flags);
```

No composite `(domain, relativePath)`. quince#1444.

**6. `FileEntry.Size` and `.MTime` are decoded from a per-row NSKeyedArchiver blob**, not read from a
column — `backup.go`'s `List` selects `file` and calls `decodeFileRecord`. So no SQL aggregate can
sum sizes; something must decode every row. **It is cheaper than it sounds — see fact 8.**

**7. `ios-backup-crypt` at `v0.4.0`, `ios-backup-parser` at `v0.1.0`, `fixture` at `v0.1.1`.**
`core/go.mod` already requires the first and third; **the parser is not yet a dependency** and this
rung adds it. `GET /repos/novkostya/<repo>/tags`.

**8. The complete group-by costs one second, and quince's pagination costs up to two minutes.**
Device A, 101,018 files / 1,264 domains / 3.40 GiB, and A's oldest snapshot, encrypted, 84,570 files:

| operation | cost |
| --- | --- |
| `COUNT(*)` | **7–9 ms** |
| `GROUP BY domain, COUNT(*)` (1,264 groups) | **33–36 ms** |
| full unordered scan of `domain, file`, no decode (60.7 MiB) | **150 ms** |
| **full unordered scan WITH decode — the whole group-by** | **1.08 / 1.06 / 1.08 s** (3 runs, **13 µs/row**) |
| the same aggregate walked through `vault.List` | **9.4 s** (unenc, `limit=2000`) … **2 m 05 s** (enc, `limit=500`) |
| `vault.List` pagination alone, decoding nothing | 8.2 s / 25.8 s |
| the same, after `CREATE INDEX ON Files(domain, relativePath)` | **0.41 s** |
| building that index | **0.32 s**, +11% file size |

`Unlock` itself: **89–103 ms** unencrypted, **1.72 s** encrypted.

**A synthetic control isolates the cause.** Same harness, fixture-built, 100,000 rows over **40**
domains: 8.6 s / 3.4 s, against the real 101,018 rows over **1,264** domains at 26.7 s / 9.4 s. Row
count is not the driver; **the missing index is**, and high domain cardinality is what exposes it.

**What this retires:** the scoping issue's §9 worried that *"the manifest is streamed lazily and a
group-by is not"*. The group-by is fine. **Paginating it was the defect**, and fact 8 separates the
two so the surface is not designed around the wrong number. It also retires a plausible misreading
of the encrypted row: the 2 m 05 s is **not** an encryption cost and **not** a cold-cache cost —
decrypt happens once, at `Unlock`, for 1.72 s.

**9. A domain has THREE states, not two.** The parser's `Open` returns `*UnsupportedSchemaError`
(present, schema unrecognised) **or** an error wrapping `fs.ErrNotExist` (the database is not in this
backup at all). Read from each domain package and `errors.go`. **On `main`. See fact 9b for how many the TAG has.**

**10. The parser exposes no domain registry.** No `Domains()`, no slice, no map — the packages
are reachable only by importing each. `grep` over the repository.

**9b. THE DOMAIN COUNT IS `main`'S, NOT THE TAG'S — FIVE, NOT SEVEN.** `v0.1.0` is the parser's
ONLY tag and therefore the only thing `core/go.mod` can require. It carries **`calendar`, `calls`,
`contacts`, `messages`, `notes`**. **`reminders` and `safari` are on `main` and in no release**, and
so is `backup.ReadDirFS`, which `reminders` needs (quince#1456). `git ls-tree v0.1.0` against
`git ls-tree origin/main`, 2026-08-22.

**THIS IS THE FOURTH TIME IN ONE SESSION THAT A LIBRARY'S `main` WAS READ AND A CONSUMER'S TAG
ASSUMED**, and it is the only one that reached a merged document: the scoping issue said seven,
this spec repeated it in four places, and both were describing a tree no `go.mod` here can
reference. The others were an unbuildable `require` (`ios-backup-crypt#13`), a release nothing
could consume (quince#1432), and a compile error the toolchain caught (quince#1456).

**The guard, because *interface facts are looked up live* did not prevent any of the four:**
*live* has to mean **the version the module will actually resolve**, not whatever `main` holds.
`git ls-tree <tag>` and `git grep <symbol> <tag>` are the reads; a clone checked out at `main`
answers a different question in the same words. **Only the compiler catches this**, and only
where a symbol is named — a COUNT, like this one, compiles perfectly and is simply wrong.

**11. The fixture generator wrote `Manifest.plist` and `Manifest.db` and nothing else — AT
SCOPING TIME. THIS RUNG CHANGED IT.** Read from `fixture/fixture.go` and
`internal/builder/builder.go` on 2026-08-22, and superseded the same day by
`novkostya/ios-backup-crypt#15`, which generates both other plists. **Kept in the past tense as
provenance**, because it is the measurement D8 was written from and a reader arriving at D8
needs to see the premise as well as its resolution. Do not read it as a present state.


**12. `iosbackup.Open` refuses an unencrypted backup** with `ErrNotEncrypted`, so the library's
single-pass `List` — the cheap aggregate of fact 8 — is reachable only on encrypted versions.
quince's `unencrypted.go` is quince's own code and has no equivalent. D4.

**13. `unencrypted.go` opens the committed `Manifest.db` `mode=ro&immutable=1`, deliberately**, so
SQLite cannot create a `-wal`/`-shm` sidecar inside a committed version. Its comment is correct and
this rung does not relax it. D4.

---

## Design

### D1 — Overview has TWO TIERS, and the pre-unlock one is bounded by the FORMAT, not by a policy

**RULED — Operator, 2026-08-22, verbatim:**

> *"what a session sees without presenting the backup password — whatever is possible technically"*

**Given in session and relayed to quince#1432 by the architect seat. It is on no forge artifact that
predates this file, and this paragraph is its durable home** — an issue is where a question is
decided, git is where the decision survives.

So there is no per-field judgement to make. **Whatever an iOS backup yields without the backup
password, overview may show**, and a field that becomes readable without a password later is in
scope by this ruling rather than needing a new one.

**What that covers is settled by facts 1–4 and it is not small.** The scoping issue reasoned this
rung was cheap because *"overview is not a parsed domain — it is a projection of data qn.8 already
streams."* **That is true of the post-unlock tier and false of the pre-unlock one**: `Info.plist` and
`Status.plist` are parsed nowhere in either library today, so reading them is new work. Not large,
and not free.

**One version's encryption state does not generalise to its device.** Measured on the Operator's iPad:
`IsEncrypted=false` on the head, `true` with a wrapped 44-byte `ManifestKey` on twelve older snapshots.
So a single device's version list can hold a version this tier serves **without a password** beside
twelve that need one — and `Info.Encrypted` is already the per-version field that says which
(`qn.8` D7). Named here because a tier that reads encryption off the DEVICE rather than off the
VERSION would be right on most stands and wrong on this one, which is the stand the rung gate uses.

### D2 — The pre-unlock tier is THREE reads at three costs, and it is built cheapest-first

| step | source | cost | what it adds |
| --- | --- | --- | --- |
| **a** | `Manifest.plist` `Lockdown` | **zero extra I/O** | `DeviceClass`, `ProductType`, `BuildVersion`, `SerialNumber`, `UniqueDeviceID` |
| **b** | `Status.plist` | **µs** | `IsFullBackup`, `BackupState`, `SnapshotState`, `Date`, `UUID`, `Version` |
| **c** | `Info.plist` | **10–99 ms** | `Installed Applications`, `Last Backup Date`, and B's `IMEI` / `ICCID` / `Phone Number` |

**(a) is the whole of the cheap win and it is a two-field struct change.** `Info` already exists,
`Manifest.plist` is already parsed, and five fields are being discarded at the point of parse.

**(c) is the only one that needs a budget.** 99 ms on a phone, scaling with the app count, and it is
the read that carries the app list — so it is on the path of the thing users came for. It is read
**once per version and memoised**, not per request.

**`ProductType` is a model identifier, not a marketing name**, and quince holds no mapping table.
The surface shows it as it is rather than guessing, and *"iPad (model `<ProductType>`)"* is honest
where an unmaintained lookup table would quietly go stale. `DeviceClass` supplies the human word.

### D3 — There are FOUR app counts. The surface names ONE as "apps" and reconciles the rest

Fact 3, fact 4 and fact 8 each yield a different number for the same backup:

| number (A) | source | pre-unlock? | means |
| --- | --- | --- | --- |
| **21** | `Info.plist` → `Installed Applications` | yes | apps the user installed |
| **1203** | `Manifest.plist` → `Applications` | yes | every bundle with a container, Apple services included |
| **1205** | group-by, `AppDomain*` | no | app domains actually holding files here |
| **1264** | group-by, all domains | no | every domain, system included |

**Ruled rung-local: "Apps" means the 21.** It is what a person means by the word, it is the only one
of the four a user could verify against their own home screen, and it is available before unlock.

**The other 1,243 domains are not dropped — they are aggregated into a named row**, so the per-app
sizes and the whole-backup total reconcile. Showing 21 apps whose sizes sum to a fraction of the
backup, with nothing accounting for the rest, is a silent cap: *"no silent caps or fallbacks"*
applies to a number that does not add up exactly as it applies to a truncated list.

**Naming the number is the whole discipline here.** *"1,205 apps"* and *"21 apps"* are both true of
this backup and answer different questions; a label that does not say which is the collapsed
diagnostic the *troubleshooting is actionable* rule forbids **even when every word of it is true**.

### D4 — The aggregate is ONE UNORDERED SCAN on the seam. It is never the browse pagination

This is the decision fact 8 forces, and it is the one that keeps this rung independent of quince#1444.

Overview needs a total over every row. It needs **no order and no pages** — those are the *browser's*
contract. Walking `vault.List` to build an aggregate pays 51–203 index-less sorts to produce
something one pass answers in **1.1 s**, an overhead of **29×–116×**.

**So `vault.Vault` gains an aggregate method** — one pass, no cursor, no ordering, returning
per-domain `{count, bytes}` plus the totals. Both implementations provide it; the conformance suite
gates it like everything else on the seam.

**This is a contract surface, so it is written into `docs/contracts.md` §4 in the same PR**, per
*docs are part of the diff*. It is a seam addition rather than an amendment to a frozen method.

**Why not just index the browse path and keep one code path?** Because fact 13: the unencrypted
implementation opens the committed file `immutable=1` on purpose, and indexing it would mean copying
117 MB into scratch on every unlock. The aggregate needs neither the index nor the copy. quince#1444
is still worth fixing **for the browser**, and this rung deliberately does not depend on it.

**Where the seam method gets its speed differs per implementation, and that is allowed** — encrypted
delegates to the library's own single-pass `List` (fact 8's 1.1 s); unencrypted must implement the
pass itself, because fact 12 puts the library's out of reach. Same contract, same conformance suite,
two bodies.

**1.1 s is not "instant" and the surface must not pretend otherwise.** Overview renders the device
summary and the app list from D2 immediately, and per-app **sizes** arrive when the aggregate lands,
with an explicit pending state. **What is gated is the SHAPE, not the clock: one pass, asserted by
counting** (G5). A wall-clock budget stood here and was withdrawn before any code was written — it
would have passed a change that reintroduced pagination but happened to land inside it, which is the
exact regression D4 exists to prevent. G5b keeps the number visible at a catastrophic-case bound.

### D5 — `FileCount`'s ambiguity is fixed in the LIBRARY

**RULED — architect, quince#1432:**

> `DeviceInfo().FileCount` returning `0` for *locked* and for *empty* is one value standing for two
> states, and the surface is the wrong place to fix it: **an ambiguity that can leave the call will
> eventually leave it through a caller nobody is reviewing.**

Take a `Known bool` or an error; the ruling expressed no preference *"so long as the zero value
cannot be mistaken for an answer"*. **This spec takes `Known bool`**, because an error would make the
ordinary pre-unlock read — which is now a *supported* call under D1, not a misuse — return one.

**D1 makes this load-bearing rather than tidy**, which is worth stating because it inverts the
cost/benefit the ruling was taken under. Before D1, `FileCount` was simply read after unlock and the
ambiguity was theoretical. After D1 there is a real screen where **every neighbouring field is known
and this one is not**, so an unguarded `0` renders as *"0 files"* beside a correct device name and a
correct date. That is the exact defect the rung was warned about.

### D6 — The capability report lives in overview, is LAZY, session-cached, and has THREE states

**RULED — architect, quince#1432:** in overview, because *"building it here means `qn.10` and
everything after inherit the surface instead of each adding one"*; **lazy**, because naming what a
backup cannot serve means opening one database per domain and *"eagerly paying it on every overview render
would put a fixed cost on a screen whose common case is 'which apps are in here'"*; and **cached for
the session, not for the version** — *"so nothing carries a report across a lock"*.

**The ruling assumed two outcomes per domain and there are three** (fact 9), which this spec settles
rung-locally within it:

| state | how it arrives | what the user is told |
| --- | --- | --- |
| **supported** | `Open` succeeds | the domain, its schema alias, and `Missing[]` |
| **unsupported** | `*UnsupportedSchemaError` | quince cannot read *this* schema — with the observed fingerprint, which is what a schema-support issue needs |
| **absent** | error wrapping `fs.ErrNotExist` | the database is not in this backup |

**Collapsing absent into unsupported would be the collapsed diagnostic again.** *"You have no Safari
data in this backup"* and *"quince cannot read your Safari database"* have different remedies, and
one of them is not a defect at all.

**The domain list is enumerated in quince** (fact 10), so a new library domain is a quince change
and not only a release. The scoping issue's *"a library release plus a rung, with no contract
amendment"* is right about the **contract** and wrong about the **code**; the enumeration lives in
one place with a comment saying so.

**It starts at FIVE** — `calendar`, `calls`, `contacts`, `messages`, `notes` — because that is what
`v0.1.0` carries (fact 9b). `reminders` and `safari` join it when a parser release does, in the same
change that bumps `core/go.mod` and restores quince#1456's `ReadDirFS` assertion. **The `absent` row
above is illustrated with Safari deliberately**: until that bump, Safari is not a domain quince can
report on at all, which is a THIRD thing to say about a domain and is exactly the collapse D6 forbids
— so the enumeration carries only what the dependency can serve, and a domain quince cannot reach is
absent from the report rather than reported absent.

### D7 — quince implements `backup.FS` over the vault session

The parser reads through `backup.FS` — `Materialize(domain, relativePath) → path` and `Exists`.
`DirFS` is built for a reconstructed directory tree; quince has a vault session, so quince writes the
implementation. `Materialize` decrypts into the session's existing scratch and **must copy sidecars**
(`-wal`, `-shm`, `-journal`) when present, because the seam's own contract requires a private,
mutation-safe copy.

**`reminders` additionally type-asserts for `ReadDirFS`**, because its per-account stores have
UUID-shaped names recorded in no manifest. Implementing it is cheap — `List` with a domain and prefix
already answers it — and a host that does not is served best-effort, which would silently under-report
one domain.

**So the scoping issue's *"there is no adapter to write"* is right about domain adapters and wrong
here.** This one is thin and real, and D6 runs on it.

**Lifetime.** Domains close before the FS that materialised them. The session teardown `qn.8` already
owns — one path for lock, TTL and shutdown alike — gains the domain closes, rather than growing a
second teardown beside it.

### D8 — Fixtures: the pre-unlock tier's fixtures come from upstream, and they have ARRIVED

Fact 11 recorded that the generator wrote `Manifest.plist` and `Manifest.db` only, so `Info.plist`
and `Status.plist` — D2(b) and D2(c), including the app list — could not be fixture-tested, against
an M7 gate of *"fixture tests in CI"*. **That was this rung's one dependency outside itself, and it
is met**: `novkostya/ios-backup-crypt#15` generates both, in the formats iOS actually writes them in
(XML `Info.plist`, binary `Status.plist`), and `#16` reads them back.

**So nothing here is declared untested any more.** The clause that stood here — cover D2(a), declare
the other two as accepted debt — is spent, and is recorded rather than deleted because a reader
arriving from a citation needs to see that the dependency was met rather than dropped.

**This heading asserted `cannot yet build` after it could**, and that is quince#408's signature
exactly: a heading describes the whole, so it is stale by default after every flip, and a stale
heading is read as authority. It survived four merges and two seats — the reviewer who approved every
one of them found it, not the author. **The habit it earns: a PR that closes a dependency sweeps
whatever DECLARED that dependency.** `make stale-refs-report` catches the issue side of this; nothing
catches the prose side.

**EVERY IDENTIFIER IN EVERY FIXTURE IS INVENTED.** Not trimmed from a real backup, not anonymised —
made up. A fixture carrying a real `IMEI`, `Serial Number`, `Phone Number` or bundle id puts device
content into public git forever, and D2(c) is precisely the tier that reads those fields. The
precedent is quince#1425: *"INVENTED PATHS, ALWAYS."*

### D9 — The file browser STAYS

The remark that opened this rung — *"this is just test UI, it's not going to be in the end product"* —
is about overview being the **primary** surface, not about deleting the browser. The browser is
`qn.8`'s gate, the escape hatch when a domain is unsupported (D6's middle row has no other remedy),
and **the only surface that can reach a file no viewer models.**

Written as its own decision because somebody will read the remark as *remove it*, and because
quince#1444's fix is worth making for a surface that is staying.

### D10 — The privacy rule bites at the FIXTURE, not at the screen

`Info.plist` carries `IMEI`, `ICCID`, `Phone Number` and `Serial Number` (fact 4) — the kind of
identifier the hard rules name Operator-private.

**Showing them to the Operator in their own quince is not a leak.** It is their data, on their
machine, behind their session; the privacy rule governs *"committed files, commit messages, branch
names, tags, PR/issue text, or fixtures."* Under D1 they are in scope, and this spec does not carve
them out.

**The exposure that is real is D8's**, and the second one is a scoped holder: D8 of the security model
gives them only their own device, so a pre-unlock tier shows them facts about the phone in their hand.

**A judgement this spec makes rather than inherits:** the surface does not put `IMEI` / `ICCID` /
`Phone Number` in its *default* view. Not because D1 forbids it — it does not — but because they are
never the answer to *"what is in this backup"*, and a screenshot is the most likely way any of this
leaves the Operator's machine. They belong behind an explicit *device details* disclosure. **This is
a taste decision inside a ruling, not a narrowing of it**, and it is flagged so review can overrule it
cheaply.

### D11 — Two routes, because one of them has no session

The post-unlock surface is `GET /api/sessions/{id}/overview`, in the **frozen domain envelope**
(contracts §1) — `capabilities`, `adapter_version`, `warnings`, `unsupported_reason`, `page`. D6's
report is what `capabilities` and `warnings` were frozen for; nothing here amends the envelope.

**The pre-unlock tier cannot use it, because there is no session** — that is the whole point of the
tier. It is a read on the **version**: `GET /api/versions/{id}/overview`, behind `authGuard` like
every other route, answering `unsupported_version` / `unavailable` from the same vocabulary. It is a
new route rather than a widening of `GET /api/versions`, so the list stays cheap for a page that
renders many.

---

## Stories

1. I open a version I have not unlocked and see the device it came from, when the backup was taken,
   whether it was full, whether it is encrypted, and which apps I had installed — **without typing a
   password**.
2. I unlock it and the same screen gains a file count, a total size, and a size and file count per
   app, without the app list disappearing and coming back.
3. The per-app sizes are visibly *pending* while they compute, and never render as zero.
4. Sizes shown per app plus the aggregated remainder equal the backup's total. Nothing is silently
   dropped.
5. I can see which domains this backup can serve, which quince cannot read, and which are simply not
   present — as three different answers.
6. I lock, and nothing derived from the version's content survives — including the capability report.
7. A locked version reports its file count as **unknown**, never as `0`.
8. I can still reach the file browser and download any single file.
9. A device whose history holds **both** encrypted and unencrypted versions renders correctly across
   the list: the unencrypted one needs no password and its neighbours do, and the pre-unlock tier
   looks the same on all of them.

## Gates

Beyond `make gates`:

- **G1** — story 1 against a fixture: every D2(a) field present, on a version never unlocked.
- **G2** — story 7: `Known == false` before unlock, `true` after; a test asserts no path renders an
  unknown count as a number.
- **G3** — story 4: per-app + remainder == total, asserted over a fixture with domains in every class.
- **G4** — story 5: three distinguishable outcomes, one fixture per state, including a domain built
  with a deliberately unrecognisable schema and one simply absent.
- **G5** — **D4's claim, asserted STRUCTURALLY**: the aggregate visits each row **once** and never
  walks the paginated path. Counted, not timed. **A wall-clock budget was specified here and
  withdrawn before any code was written** (architect, quince#1445): D4's claim is about control flow
  — one unordered pass instead of 51–203 index-less sorts — and a clock encodes it as a fact about a
  machine. The clock is the weaker gate as well as the flakier one, because **it can pass while the
  regression is present**: a change that reintroduced pagination but happened to land inside the
  budget would be waved through by the timer and caught by the pass count. Precedent for the flake
  half is quince#1372.
- **G5b** — wall-clock as a **reported number with a catastrophic-case bound**, failing only at
  **10×** the reference measurement (1.1 s on the real backup, so a bound of 11 s). It exists to
  catch an order-of-magnitude regression that is somehow still one pass, and it is labelled as that
  rather than as the budget. **The reference figure is from the staging stand; CI hardware is not
  pinned**, which is precisely why it is a loose backstop and not the gate.
- **G6** — story 6: after lock, the session's capability report and scratch are gone; the existing
  `qn.8` teardown assertion is extended rather than duplicated.
- **G7** — **owed to the Operator, and named as owed**: M7's rung gate — overview renders the real
  backup correctly, spot-checked against iMazing. Nothing in CI can prove this; it is not ticked
  until the Operator has clicked it.

## Fixtures

- A **large** fixture — 100,000 rows, ~1,200 domains — for **G5b**, the catastrophic-case bound.
  **Not for G5**, which became structural in quince#1448 and asserts a query count on a few hundred
  rows; that dependency was real only while G5 asserted seconds. Generated, not captured; shared with
  quince#1444. **BLOCKED, and not on this rung:** the generator inserts each row in its own implicit
  transaction, so this fixture costs **15 m 20 s** to build today — measured, and a 300,000-row build
  exceeded a 40-minute timeout without reaching the measurement it was for. One `BEGIN`/`COMMIT`
  makes it ~1.3 s. **FIXED** in `novkostya/ios-backup-crypt#12` — measured end to end at
  **1.844 s**, so the block is now only that no release tag carries it yet (quince#1432).
- **A version history holding BOTH encrypted and unencrypted versions of one device** — story 9.
  Not invented: measured on the Operator's iPad, whose head reads `IsEncrypted=false` while twelve
  older snapshots read `true` with a wrapped `ManifestKey`. It is the fixture shape most likely to
  catch a tier that assumes a device is uniformly one or the other.
- One fixture per D6 state: a readable domain, an unrecognisable one, an absent one.
- Pre-unlock fixtures **available** — `ios-backup-crypt#15` generates both plists and `#16` reads
  them back, so D2(a), D2(b) and D2(c) are all fixture-testable. See D8.
- **Every identifier invented.** See D8.

## Rule check

| rule | how this complies |
| --- | --- |
| **Never mutate a committed version** | Overview only reads. D4 chose the aggregate partly *because* indexing the browse path would mean writing beside a committed version or copying 117 MB; fact 13's `immutable=1` is preserved, not relaxed. D7's `Materialize` writes only into session scratch. |
| **Privacy is a commit-time gate** | D8: every fixture identifier invented, stated at the point the fields are listed. This document carries no identifier, path or bundle id — the measurements ran on the stand and only counts and timings came back. `make privacy-check` before every push. |
| **State honesty** | D5 (`Known`), story 7, story 3 (pending is not zero), G7 (the hardware gate is owed, not ticked). D6 reports *absent* and *unsupported* as different things. |
| **No silent caps or fallbacks** | D3's remainder row and G3's reconciliation; D6's `Missing[]`; the frozen envelope's `warnings`. A clamped page already discloses via `effective_limit` and overview does not paginate. |
| **Troubleshooting is ACTIONABLE** | D3 (naming which app count), D6 (three states, with the fingerprint an issue would need). Both are cases where every word of the collapsed version would be true. |
| **Docs are part of the diff** | D4 and D11 write contracts §1 and §4 in the PR that implements them. Coverage summary plus a declared known-untested list per PR. **D8 was that list's first entry and is now closed** — `ios-backup-crypt#15`/`#16` landed, so nothing in this rung is declared untested on fixture grounds. |
| **Interface facts looked up live** | Facts 1–13, all measured 2026-08-22, each saying how. Fact 7's versions from the tags endpoint. |
| **Don't improvise architecture** | The three ruled questions are transcribed at D1, D5, D6. Rung-local calls — `Known bool` over an error, "apps" meaning the 21, three capability states, D10's default view — are recorded here, in the spec, which is the cheapest durable home. **D4 touches a contract surface**, so it is written into contracts in the same PR rather than decided in code. |
| **Secrets discipline** | The backup password reaches `Unlock` and nothing else; overview never logs it and the pre-unlock route never receives one. Fixture password stays `test`. |
| **Every hardware bug becomes a replay fixture** | quince#1444 came out of this rung's measurement and carries that requirement; G5's fixture is the shape that expresses it. |
| **Config tidiness** | No new config key. D6's cache is the `qn.8` session's lifetime; D4 gates a shape (one pass), not a duration; G5b's bound is a gate constant, not a setting. |

**Near-miss, declared:** D2(c) reads `IMEI` / `ICCID` / `Phone Number`. Under D1 that is in scope and
not a privacy breach on the Operator's own screen, but it is one file-write away from being one, which
is why D8 and D10 exist and why this line is in the table rather than left implicit.

## Slices

Sequenced from `main`, never stacked (`CLAUDE.md` §1). Each carries one reviewable claim.

| | claim | state |
| --- | --- | --- |
| **1** | this spec | **merged** — quince#1445, and quince#1448 for G5/story 9 |
| **2** | `ios-backup-crypt`: `Lockdown`'s five extra fields + `FileCountKnown` (D2a, D5) | **merged** — `ios-backup-crypt#13` |
| **3** | `ios-backup-crypt`: `Status.plist` + `Info.plist` readers (D2b, D2c) | **merged** — `ios-backup-crypt#16` |
| **4** | `ios-backup-crypt/fixture`: generate both plists (D8) | **merged** — `ios-backup-crypt#15`, and taken BEFORE 3 so 3 landed with no declared gap |
| **5** | the aggregate on the seam, both implementations, conformance + G5 (D4) | in review |
| **6** | `GET /api/versions/{id}/overview`, the pre-unlock tier, contracts §1 (D11) | **blocked on a release tag** — quince#1432 |
| **7** | `parserfs` — `backup.FS` + `ReadDirFS` over a session (D7) | not open; needs `ios-backup-parser` at its existing `v0.1.0` |
| **8** | the capability report, three states, lazy + session-cached (D6) | not open |
| **9** | `GET /api/sessions/{id}/overview`, contracts §1/§4 (D4, D11) | not open |
| **10** | the surface (D3, D9, D10), then G7 to the Operator | not open; partly blocked with 6 |

**THIS TABLE IS A SECOND PART DESCRIBING THE WHOLE, so it is stale by default after every merge** —
quince#409's finding, which cost four of five rows in the PR that fixed the heading above them. The
`state` column exists so that going stale is visible rather than silent; **update it in the diff that
changes what it describes.**

**Slices 2–4 are upstream**, in a different repository, and all three have merged. **4 was taken
before 3**, reversing this list: the spec originally had 3 land with a declared gap and 4 close it
afterwards, and doing the fixture first meant there was no gap to declare. Nothing depended on the
original order.

**Slice 6 is the only one blocked**, and not on code: `main` of `ios-backup-crypt` carries slices
2–4 and no release tag does, so `core/go.mod` cannot reach them. Who cuts a release tag on the
sibling libraries is unwritten and is the Operator's — quince#1432.
