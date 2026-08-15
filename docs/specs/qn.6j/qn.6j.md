# qn.6j — `config.yml` contains only what was set, and Settings shows it in its own language

**Goal.** A user who hand-writes three lines of `config.yml`, changes one thing in Settings and
presses Save gets back their three lines plus the one thing they changed — not a 30-line document
carrying every optional key at its default, including a `zfs:` block on a storage that has no zfs.

Rung issue: [quince#728](https://github.com/novkostya/quince/issues/728), Operator-reported from a
live screen and **ruled its own rung, pre-`v0.1`, on 2026-08-08** — relayed by the architect seat on
that issue. Two rulings the same day decide the shape and are cited rather than re-litigated: the
file contains **only what was set**, and it carries **no generated annotation at all**. Canon holds
both in `docs/quince.stack.md` D12 as of [quince#752](https://github.com/novkostya/quince/pull/752),
merged `2026-08-08T19:00:09Z`.

**Everything measured below was measured in this checkout at `09beb3a`, 2026-08-08**, in the pinned
Go toolchain container. Where a claim is *not* measured it says so in those words.

---

## Why `qn.6j`, and what it does not fix

**The letter.** `qn.6i` is [quince#731](https://github.com/novkostya/quince/issues/731) by Operator
ruling on that issue — *"this is `qn.6i`"* — and is the last allocated. `qn.6j` appears nowhere:
not under `docs/specs/`, not elsewhere in `docs/`, not in the devlog `roadmap.md` or `progress.md`.
The architect inferred the letter on quince#728 and asked that nothing be written against it *"until
somebody else has read this paragraph"*; this section is that read, and it checked the inference
rather than adopting it.

**This rung fixes a live user-visible defect, and it is cosmetic in exactly one place and not in the
others.** A `zfs:` block written onto a `hardlink` storage is not redundant, it is **misleading** —
it reads as configuration that applies, on a backend where none of it does. `tls: {cert_file: "",
key_file: ""}` on a deployment with no TLS reads the same way. And a file that pins every default is
a file whose next `git diff` is noise, which is the property D12 exists to protect.

**What it does not fix.** Nothing here makes a backup faster, more reliable, or more likely to
succeed. If it is not built, quince keeps working exactly as it does today and keeps writing the
file it writes today.

**And what it costs, which is DECIDED rather than dropped.** This rung shrinks the file, and the same
2026-08-08 ruling cancelled the generated annotation — so a user opening `config.yml` no longer learns
that TLS exists by finding an empty key for it. **Discovery moves to Settings and the docs.** Both
halves are the Operator's, neither is reopened here, and the cost is written down so the next reader
can see it was accepted. `docs/quince.stack.md` D12 records the same acceptance; the *user-facing*
key reference that has to exist for it is quince#726's, not this rung's.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/config/service.go` | `Parse` returns the **declared set** alongside the config; `Loaded` carries it; `Service` holds it and updates it on every write; `Marshal` gains the declared-aware form and `replaceLocked` uses it. The `qn.6` doc-comment comments on `Marshal` and on the package are **false as of the ruling** and are corrected here |
| `core/internal/config/schema.go` | the `Config` type comment's *"qn.6 swaps Marshal for a yaml.Node encoder that also emits generated doc-comments"* — same correction. **No struct tag gains `omitempty`**; see D4 |
| `core/internal/config/add.go` | `AddStorage` marks the added entry's supplied keys declared, and materialises `default: true` on the incumbent when the list crosses from one entry to two (D3) |
| `core/internal/config/forget.go` | `ForgetStorage` drops the forgotten entry's declared keys |
| `core/internal/config/service.go` — `replaceLocked` | **NEW, and it comes first: `c.Storage` is resolved at the top, before `Validate`.** This is quince#754, filed off this review, and it is a prerequisite rather than a companion — see D2a |
| `docs/quince.stack.md` | **already landed** (quince#752). Nothing owed unless a decision here changes it |
| `docs/contracts.md` §6 | **only if open question 1 is ruled yes** — `GET /api/config` gains the serialized file text |
| `ui/src/features/settings/ConfigView.tsx` | **only if open question 1 is ruled yes** — the preview renders YAML instead of `JSON.stringify` |

**Out of scope, each with a why.**

- **File-watch live reload.** quince#727, post-`v0.1`, a separate rung by the same 2026-08-08 ruling
  — *"the two do not land together."* Its **doc-comment half is moot rather than deferred**, since
  the ruling deleted the thing it was staging toward.
- **Generated doc-comments.** **Cancelled**, not deferred. There is no smaller annotation to stage
  toward and nothing in this rung emits one.
- **Resolution semantics — WHAT an omitted key means.** `schema.go`'s *"doing it in one place is what
  lets `- path: /backups` mean what the 2026-08-01 ruling says it means, without any consumer
  learning that a name might be empty"* survives untouched, and `Resolved()` is not edited.
  **Only what gets MARSHALLED changes**, which is the ruling's own words.
  **This bullet said *"`Parse` still resolves; the in-memory document is still the resolved one"* and
  that second clause was FALSE on the write path** — the architect's review of quince#753 named it
  and the measurement in facts 9 and 10 confirms it. **WHERE resolution runs is therefore in scope**
  and is D2a; what it *means* is not.
- **quince#493 — `PUT /api/config` zeroes any key the client omits.** Independent, and the analyst's
  direction table on quince#728 is why: marshal runs server→disk, the decoder runs wire→server, and
  fixing one cannot fix the other. This rung owes it a **guard**, not a fix — story 6 — because the
  obvious implementation of this rung is what would turn quince#493 from latent into live.
- **The `storage:` requirement and the backend-collision refusal.** `CheckStorages` and
  `CheckStorageBackendErrors` keep their place at the top of `replaceLocked` and are not reached by
  anything here.
- **`quince config validate`** — reads, never writes.

---

## Interface facts — measured at `09beb3a`

**Fact 1 — the round trip inflates 50 bytes into 641, and the issue's diagnosis needed no
correction.** The issue explicitly declared this measurement owed: *"I did not perform a save and
diff the file, which is the one measurement that would confirm the round trip end to end and should
be the first thing a fix does."* Performed in a throwaway test: write the minimal declaration,
`NewService`, set `ui.theme = "dark"`, `Replace`, read the file back.

```
BEFORE — 50 bytes, 3 lines            AFTER — 641 bytes, 30 lines
storage:                              backup: preferred_transport: usb, require_encryption: true
  - path: /backups                    storage[0]: name, path, default, backend,
    backend: hardlink                             zfs{parent_dataset:"", mode, hook_cmd:"", seed},
                                                  retention{keep_recent, keep_daily, keep_weekly}
                                      devices: manage_muxer, usbmuxd_socket, netmuxd_addr
                                      tls: cert_file: "", key_file: ""
                                      sessions: ttl_minutes, allow_insecure_transport
                                      automation: staleness_days, reminder_cooldown_hours
                                      ui: theme: dark
```

**12.8×.** Every key the issue predicted appears, `zfs:` on a `hardlink` storage included.

**Fact 2 — `default: true` is written TODAY, and the tidy file would lose it.** The measurement
shows it on a lone storage, put there by `ResolveStorages`' implication. So the sharp edge the ruling
relay names is not hypothetical: it is a key the current file **has**. See D3.

**Fact 3 — there is exactly one `Marshal` call site on the write path.** `service.go:121` defines it
and `service.go:413` (inside `replaceLocked`) is the only caller.

**Fact 4 — every write funnels through `replaceLocked`.** `Replace` (PUT), `AddStorage`
(`add.go:86`) and `ForgetStorage` (`forget.go:133`) all reach it, and all three hold `writeMu`
across the read-modify-write. There is no fourth door.

**Fact 5 — `Parse` already walks the raw document against the struct's yaml tags.** `unknownKeys`
(`service.go:57`) recurses into nested structs *and* into slices of structs with an index
(`storage[0].`). The declared set is the same walk with a different accumulator, so this rung adds no
new traversal concept.

**Fact 6 — `Parse` and `Marshal` have no callers outside the package.** The only external entry
point is `config.Load` (`core/cmd/quince/main.go:532`). Signature changes are therefore cheap and
contained.

**Fact 7 — the yaml and json struct tags are two independent keys on one line.**
`yaml:"cert_file,omitempty" json:"cert_file"` is expressible and each encoder reads only its own key.
The architect established this on quince#728 correcting an earlier reading. **The risk is discipline,
not impossibility**, which is why story 6's guard is a test rather than a mechanism.

**Fact 8 — `grep -c omitempty core/internal/config/schema.go` → 0.** Unchanged from the issue.

**Fact 9 — resolution does NOT run on the `PUT` path, and the wire is therefore STRICTER than the
file.** `handlers_config.go:48-55` decodes the body into a zero-valued `config.Config` and calls
`Replace`; nothing between them touches `Resolved()`. Measured — `Replace` with the minimal storage
the startup refusal itself teaches returns three `422`s for keys the file accepts:

```
storage[0].zfs.mode  invalid value ""; must be one of [exec hook]
storage[0].zfs.seed  invalid value ""; must be one of [auto reflink copy]
storage              exactly one storage must be marked `default: true` …
```

> Transcribed as measured. The first line now reads `must be one of [hook]` — `exec` was removed on
> quince#793 (Operator ruling, quince#697). The refusal itself is unchanged.

An empty `backend` is refused the same way. **`validateStorages` asserts the opposite in its own
comment** (`validate.go:133`): *"A LONE STORAGE IS ALREADY MARKED DEFAULT by ResolveStorages, so
`defaults == 0` here can only mean several storages and none chosen."* True on the load path, false
on this one.

**Fact 10 — `name` and `retention` are not caught by `Validate`, so a successful `PUT` writes
`name: ""` and leaves it in the running process.** Measured, with the most minimal entry the wire
will accept:

```
LIVE SNAPSHOT after the PUT: name="" backend="hardlink" retention_nil=true
FILE after the PUT:  storage: - name: ""   …   retention: null
```

The **file** self-heals at the next `Load`; the **running process** does not, and `GET /api/config`
reports the empty name until a restart. Filed as **quince#754**, because it is a defect on `main`
rather than something this rung introduces. It is quoted here because facts 9 and 10 together are why
D2a exists.

---

## Design

### D1 — The declared set, and the file is its own storage

**`declared` is a set of dotted key paths present in the document as it was read.** `Parse` computes
it from the same `map[string]any` walk that already produces unknown-key warnings (fact 5), and
`Loaded` carries it out to `Service`.

**Nothing is persisted, and that is the point.** The declared set is *derived from the file at every
`Load`*, so it survives restarts by construction — the file **is** the record of what was set. There
is no second document on disk, no sidecar, and no migration. What `Service` holds is a cache of a
fact the file already carries, updated in-process between writes so the next write does not have to
re-read what it just wrote.

**This answers the architect's *"that tracking has to survive the write path or every UI save
re-inflates the file"*** — it survives because the write path writes the declared document, so
re-parsing it yields the same set. Story 1 is that fixed point stated as a test.

**When there is nothing to derive it from, the declared set is EMPTY, and the four ways that happens
are not one case.** `Load` returns `Default()` in four branches, and they split two and two:

- **No file yet** (`service.go:156`, `OK: true`). Empty declared set, and the first save therefore
  writes the storage entry and almost nothing else. **That is the first `config.yml` every user ever
  sees**, so it is story 10 rather than a consequence nobody looked at. It is also the ruling
  working: a fresh file that says only what onboarding chose is exactly *"only what was set"*.
- **Unreadable, invalid YAML, or fails `Validate`** (all `OK: false`). The in-memory config is
  `Default()` and **not the user's file**, so a save discards what they wrote. **That is
  pre-existing and this rung does not fix it** — but it gets *less* visible here, because the result
  now looks deliberately minimal instead of obviously machine-written, where before a user could at
  least see their file had been replaced by a full dump. Named as a near-miss in the Rule check and
  as open question 4; not built past, because it is D12's last-good semantics rather than this
  rung's.

### D2 — The write rule

> **A key is written iff it is `declared`, OR this write changes its value, OR the file could not be
> re-parsed without it.**

Three clauses, and each earns its place:

1. **`declared`** — what the user wrote stays written, even when its value equals today's default.
   This is the clause that makes the rule *"only what was set"* rather than *"only what differs from
   the default"*, and D5 is the argument for why the difference matters.
2. **changed by this write** — **not optional.** `PUT /api/config` arrives as a *complete* JSON
   document; the request cannot distinguish a key the user touched from one the UI merely echoed
   back. So "what was set" on this path is recoverable only as a **diff against the configuration
   that was live**, which `replaceLocked` already has in hand as `old`. **Both sides of that diff
   must be RESOLVED — see D2a, which is a prerequisite and not a detail.**
3. **required to re-parse** — `storage[].path` on every entry (an entry without it is meaningless),
   and D3's `default: true`.

**A section is written only if something under it is.** Pruning is bottom-up, so a `backup:` with no
declared leaf does not appear as `backup: {}`.

**THE DECLARED SET ONLY EVER GROWS, and nothing un-declares a key except `ForgetStorage`.** A user
who switches `ui.theme` to `dark` and back to `system` leaves `theme: system` in the file
permanently, at its default value. **That is correct under *"only what was set"*** — they did set it,
twice — and it is stated here so the next reader recognises it as decided rather than filing it. The
alternative, un-declaring a key when it returns to its default, is D5's rejected rule wearing a
different hat: it would delete an explicit choice the moment it happened to agree with today's
default.

**Mechanism.** `Marshal` keeps producing the full canonical document first, then **prunes the
`yaml.Node` tree** to the written set. Marshalling first is what preserves canonical key order for
free — struct field order is already the key order (`schema.go`'s `Config` comment), and a node prune
cannot reorder what it only removes. The same node walk, run over `old` and `next` in parallel, is
what computes clause 2. One traversal utility, two uses.

**The alternative — emit from a reflect walk over the struct, consulting `declared` per field — is
rejected** for the ordering reason above and for a second: it would have to re-derive the yaml key
names and nesting that `yaml.Marshal` already knows, which is a second implementation of the encoder
that can disagree with the first.

### D2a — Clause 2 diffs two RESOLVED documents, and `replaceLocked` is where that becomes true

**The question, as the architect put it: which document does clause 2 diff, and if the answer is
"both resolved", where does the resolution happen?**

**Answer: both resolved, and the resolution happens at the top of `replaceLocked`, before
`Validate`.** One line — `c.Storage = ResolveStorages(c.Storage)` — and it is the FIRST code PR of
this rung rather than part of the marshaller's.

**Why it is needed at all.** Facts 9 and 10: resolution runs at `Parse` and inside `AddStorage`, and
never on the `PUT` path. So `replaceLocked`'s two sides are normalized differently, and clause 2
would compare a resolved `old` against a raw `next` and call every unfilled key *changed*.

**The review's example, checked rather than accepted, and it splits three ways.** For a `PUT` that
omits storage keys:

| key | what actually happens today | reaches the file? |
| --- | --- | --- |
| `zfs.mode`, `zfs.seed`, `backend` | `Validate` **refuses** the write — `invalid value ""` | **no** |
| `default` on a lone entry | `Validate` **refuses** — `defaults == 0` | **no** |
| `name` | `Validate` does not check it | **yes — `name: ""` is written** |
| `retention` | not checked | **yes — `retention: null` is written** |

**So the hole is narrower than the example and worse than it looks.** Narrower, because `Validate`
accidentally catches four of six. Worse, because the two it misses are caught by *nothing* and one of
them is `name` — the identity `DELETE /api/config/storage/{name}` addresses by. And the four it does
catch, it catches by **refusing a document the file would accept**, which is quince#754's first half.

**`old` is not reliably resolved either, and that is the same defect from the other end.** `old` is
`s.cfg`, which is post-`Parse` after a load but is *whatever `Replace` was handed* after a `PUT`. So
"the live snapshot is resolved" holds today only because `Validate` happens to refuse most unresolved
documents — an invariant resting on a coincidence. Resolving in `replaceLocked` makes it an invariant
resting on a line of code.

**It is a behaviour change to `PUT /api/config`, and it is recorded as a decision rather than left to
PR 3.** It is **strictly more permissive** — bodies that `422` today would succeed, and no existing
client sends those bodies, because the UI re-sends the complete document `GET` handed it. Two things
follow and are owed rather than assumed:

- **`docs/contracts.md` §6 documents the `422`.** If the set of documents that earn one shrinks, that
  text moves in the same PR (`Docs are part of the diff`).
- **It is a slice of quince#493's territory** — what `PUT` does with an omitted key — and this rung
  scoped that out. The slice taken here is **normalization only**, server-side: it does not stop the
  decoder zeroing a non-storage key, which is quince#493's actual subject and stays open.

**A third field changes its written form, and the issue lists two.** `Resolved()` fills `Retention`
as well (`schema.go:157`), so this also stops `retention: null` reaching the file and starts writing
a full `retention:` block. Harmless, closer to what the file resolves to anyway, and it makes D2's
diff *cleaner* rather than harder: with both sides resolved, an untouched `retention` is equal on
both sides and D2 drops it. The pointer still distinguishes absent from zero **at parse**, which is
where D4's argument actually lives.

**THE ADD PATH KEEPS REFUSING WHAT THIS PATH WILL NOW ACCEPT, AND THE ASYMMETRY IS RULED CORRECT.**
Architect ruling on quince#754, 2026-08-08. `POST /api/config/storage` refuses an empty `backend`
rather than defaulting it, deliberately, and `add.go:118` says so:

> `// EMPTY IS REFUSED RATHER THAN DEFAULTED. Resolved() would turn it into `auto`, and the whole`
> `// point of the add flow is that quince writes the concrete backend it just showed the user`
> `// (quince#502). An omission here is a client bug, and defaulting it would hide one.`

**The reason the two doors differ is stateable, which is why the difference is kept rather than
flattened.** `PUT /api/config` is a full-document replace of `config.yml`, so **it must mean what the
file means**. `POST /api/config/storage` is a narrow add whose caller has just watched quince probe a
path and name a concrete backend — **an omission there really is a client bug**, and quince#502's
argument is recent and holds. **Do not "fix" `add.go` to match**, and note the ordering makes this
free: `validateAddition` runs *before* the list is resolved (`add.go:84`), so the add's own gate
still fires first and `replaceLocked`'s resolve is an idempotent second pass behind it.

**That is the one line `contracts.md` §6 owes** — why the two doors differ — and PR 2 writes it.
Without it the difference reads as accidental in both places, which is how a later reader collapses
it.

**Open question 5 is RULED: it lands HERE, as PR 2**, citing quince#754 as the defect it closes.
Architect, on quince#753, **reversing** the *"its own PR, not folded into the rung"* ruling posted on
quince#754 about a minute earlier — the two crossed. The reversal's reason is that the objection was
about the defect becoming invisible, and quince#754 exists, is measured and is findable, so nothing
is buried; meanwhile a prerequisite living outside the spec that depends on it is how sequencing gets
lost. **Recorded with both rulings named** because a reader arriving from quince#754 will otherwise
find the earlier one and follow it.

### D3 — `storage:` entries: keyed by name, and the one key quince must write that nobody set

**An entry's declared keys are keyed by its `name`, not its index.** Entries are added, forgotten,
and reordered; `DELETE /api/config/storage/{name}` already treats `name` as the identity, and
`config.AddStorage`/`ForgetStorage` splice by it. At parse time the name of a raw entry is
`raw["name"]` if present, else `raw["path"]` — the same rule `Resolved()` applies, computed one step
earlier.

**Open question 2 records that this is the detail most likely to be wrong.** A rename changes the
identity, and the declared keys of the old name are then orphaned. The proposed handling is that a
rename arrives as a *changed* `name` on an entry whose `path` is unchanged, so `path` is the join —
but path is also editable in principle, and nothing today renames a storage through any surface.

**`default: true` is materialised when the list crosses from one entry to two, and this is the one
case where quince must write a value the user did not set.** `ResolveStorages` implies `default` on
a **lone** entry only. Under D2 a lone entry's `default: true` is undeclared and therefore dropped —
correct, because the implication re-supplies it. Add a second storage and the implication stops
applying, so unless the incumbent's `default: true` is written at that moment, the next parse finds
two storages and no default and `validate.go:139` refuses:

> `exactly one storage must be marked `default: true` — a backup that names no storage resolves to
> it, and there is no sane guess. It is implied only when there is exactly one storage`

That is a config the daemon will not start on — quince#683's class, reintroduced from the other
direction. **So: when `len(storage) > 1`, the entry carrying `default: true` always writes it.**
Stated as a rule of D2's clause 3 rather than as a special case in `AddStorage`, because
`PUT /api/config` can grow the list too.

**quince#712 is unaffected and must stay so.** It fixed *"the first storage could never be added"* by
resolving the in-memory list before writing (`next.Storage = ResolveStorages(&list)`); a lone entry
written without `default:` still re-implies at parse, so the single-storage case is untouched.
Story 4 pins it.

### D4 — The wire stays complete: `omitempty` reaches no `json:` tag, and no `yaml:` tag either

**No struct tag in `schema.go` gains `omitempty` in this rung.** Two reasons, and the second is the
stronger one:

1. **On the `json:` side it is a data-loss path.** A sparse wire representation makes
   `GET /api/config` drop keys → `ConfigEditor.tsx` spreads a partial document → `PUT` sends it →
   the decoder zeroes every absent key → **`devices.manage_muxer` becomes `false` and quince stops
   supervising its muxers.** That is quince#493's latent defect made live by a tidying change. Fact 7
   says the two tags are separable, so this is a discipline hazard; story 6 makes it a gate.
2. **On the `yaml:` side it cannot deliver the ruling and would obscure that it hadn't.**
   `omitempty` drops **zero** values; the inflating fields are **non-zero after `Resolved()`**
   (`backend: auto`, `zfs.mode: hook`, `zfs.seed: auto`, `name` = the path). It would remove
   `tls.cert_file: ""` — visibly progress — and leave the `zfs:` block the issue leads with. Worse,
   it would break two fields on purpose-built semantics: `backup.require_encryption: false` is a
   **deliberate setting** that `omitempty` erases, and `storage[].retention.*` is a pointer
   *precisely* so absent differs from zero.

D2's rule handles all four correctly without a tag: `require_encryption: false` differs from the
default `true`, so it is written; an untouched `retention:` is undeclared, so it is not.

### D5 — Why not the cheaper rule, recorded so it is not rediscovered as a simplification

**The rejected alternative: "omit any value equal to its default."** Stateless, no declared set, no
diff against `old`, and it happens to handle every case in the issue's table correctly — including
`require_encryption: false` (kept, because the default is `true`) and the lone-storage `default:
true` (dropped, because the implication is the default).

**It is rejected on default drift.** It deletes a key the user *explicitly wrote* because today's
default happens to match it. The moment a default changes in a later version — and `qn.6g` and
quince#654 are both records of settings being re-decided — that user's configuration silently changes
meaning, with no diff and nothing to point at. **Preserving an explicit choice as an explicit choice
is the whole difference between the two rules**, and it is the difference the word *"set"* in the
ruling is carrying.

**The cost of rejecting it is real and is accepted here**: D2 needs a declared set and a diff, where
the cheaper rule needs neither.

### D6 — The preview is NOT built until the contract is ruled

`ConfigView.tsx:42` renders `JSON.stringify(data.config, null, 2)` under a panel titled *Current
configuration*, beside a subtitle saying *"You can edit the file by hand instead"*. The two languages
disagree, and this rung makes the mismatch worse rather than better: the file is about to become
short and the JSON dump stays long, so the panel would show a document the file no longer resembles.

**The fix is one of two things and neither is this rung's to choose** — see open question 1. It is
**not built past**: if the question is unruled when the code PRs land, the preview stays JSON and
finding 1 is re-filed with this spec's argument attached.

---

## Stories

1. **A minimal file survives a save.** `storage:` / `- path: /backups` in, `ui.theme` changed to
   `dark`, save → the file is those two lines plus `ui:` / `theme: dark`. Nothing else appears.
2. **A canonical file is a fixed point.** `Load` → `Replace(Current())` → the file is byte-identical.
   Twice in a row.
3. **An explicitly-set default survives.** A file containing `ui:` / `theme: system` — the default —
   still contains it after an unrelated save.
4. **A second storage materialises the first's default.** One storage, then `POST
   /api/config/storage` → the written file carries `default: true` on the incumbent, and re-parsing
   it Validates.
5. **A forgotten storage takes its keys with it.** `DELETE /api/config/storage/{name}` → no key of
   that entry remains in the file, and no other entry's keys are disturbed.
6. **The wire stays complete.** `GET /api/config` carries every field of `config.Config`, walked by
   reflection rather than listed by hand.
7. **A degraded setting is still written.** `sessions.allow_insecure_transport: true` is not a
   default and is not dropped; its `degradedModeWarnings` warning still fires after a round trip.
8. **Nothing about resolution changed.** The existing `flatten_test.go` and
   `storages_validate_test.go` suites pass untouched.
9. *(open question 1 only)* **The preview is the file.** The panel's text equals the bytes on disk.
10. **The first file a user ever gets is minimal.** No `config.yml` on disk, onboarding adds one
    storage → the written file is that storage and nothing else. This is the fresh-install path and
    it is the file most users will ever read.
11. **A `PUT` that omits an optional storage key succeeds and writes a resolved value** (D2a). The
    body that returns three `422`s in fact 9 returns `200`, the file gets `zfs.mode: hook` only if
    it was declared or changed, and the live snapshot's `name` is the path — never `""`.

---

## Gates

Beyond `make gates`:

- **G1** — story 1 as a committed test, written as the **inverse of fact 1's measurement**: the same
  setup, asserting the small file rather than printing the large one.
- **G2** — story 2, the fixed point, asserted on bytes.
- **G3** — story 4, and the assertion is `Parse` + `Validate` on the **written bytes**, not on the
  in-memory document. The failure this gate exists to catch is a file that will not load.
- **G4** — **the general round-trip invariant**, over a table of documents: for every case, the
  configuration parsed back from what was written is **deeply equal** to the configuration that was
  written. This is the gate that catches D2 clause-3 omissions the enumeration missed, which is
  worth more than G3 alone.
  **It shares ONE comparator with the runtime check of decision 3 — the architect's condition, and
  it is a build constraint rather than a style note.** A gate and a runtime guard that can disagree
  about the same invariant is worse than either alone: the gate goes green, the guard fires in
  production, and the two are debugged as separate problems. One exported function, two callers,
  and G4 fails if a second implementation of "deeply equal" appears.
  **G4 CANNOT catch D2a's hole, and that is why D2a is a design section rather than a gate.**
  `Resolved()` restores `name`, `zfs.mode` and the rest at the next parse, so a file written with
  `name: ""` round-trips *deeply equal* and G4 passes green on the exact file quince#728 was filed
  about. Stated here because a reader who trusts G4 to cover everything will not look for D2a.
- **G4a** — story 11: the `PUT` body from fact 9 returns `200`, and the live snapshot's storage
  `name` is the path. The measurement in facts 9 and 10 inverted, in the same way G1 inverts fact 1.
- **G5** — story 6's reflect walk over `Config`, failing on any field absent from a
  `GET /api/config` body. Also the standing guard against `omitempty` reaching a `json:` tag.
- **G6** — `grep -n omitempty core/internal/config/schema.go` → still 0 at the end of this rung
  (D4). Cheap, and it is the one-line statement of the rung's sharpest trap.
- **G7** — `make privacy-check REF=origin/main...HEAD TEXT=<file under $HOME/scratch/<runner>/>` on
  every head.
- **G8** *(open question 1 only)* — the e2e story: open Settings, read the panel, and it is YAML.

**Owed to nobody on hardware.** This rung touches no device, no storage tree and no subprocess. Said
explicitly so the absence is a claim rather than an oversight.

---

## Fixtures

No new testdata files. The cases are short YAML documents written inline in the test, because the
thing under test *is* the text and a fixture file one directory away makes a byte-comparison harder
to read rather than easier. The table for G4 is the closest thing to a fixture and lives beside its
test.

---

## Rule check

- **Config tidiness is a feature (D12).** This rung **is** that rule being enforced; it does not come
  near it, it discharges it. Canon carries both rulings as of quince#752 and this spec cites rather
  than restates them.
- **No silent caps or fallbacks.** The one value quince writes that nobody set — `default: true`
  when the list crosses to two — is D3, a gate (G3), and a story. It is not a quiet special case.
  **Near-miss named:** rung-ruled decision 3 proposes a runtime fallback to the full document if the
  written bytes do not round-trip; it is only admissible **because it warns**, and if review does
  not want the warning it should not want the fallback either.
- **State honesty.** G1 is fact 1's measurement inverted, so the claim *"the file stopped
  inflating"* rests on the same measurement that established it was inflating. No gate above is
  ticked on another's behalf, and story 9 / G8 do not exist unless open question 1 is ruled.
- **Docs are part of the diff.** `schema.go` and `service.go` carry three comments promising a
  `qn.6` doc-comment encoder; the ruling cancelled it, and they are corrected in the PRs that touch
  those files. `contracts.md` §6 moves in the preview PR if there is one.
- **Interface facts are looked up live, never remembered.** Facts 1–6 and 8–10 were measured in this
  checkout at `09beb3a` today; fact 7 is the architect's measurement on quince#728, cited to it.
  **Facts 9 and 10 exist because a review named a mechanism and the spec measured it rather than
  adopting it** — which is how the narrow half (four keys `Validate` catches) and the sharp half
  (`name: ""`, which nothing catches) were separated.
- **Privacy is a commit-time gate.** No host, path, address or device identifier enters this spec or
  any PR under it. `/backups` is quince's own documented default and already appears throughout
  canon.
- **Secrets discipline.** Untouched and **near-missed**: this rung changes what `config.yml`
  contains, and D12's *"no secrets in the config file, ever"* is the invariant a config-writing
  change could most easily erode. It cannot here — the rung only ever writes a **subset** of what is
  written today, so no value reaches the file that did not already.
- **Never mutate a committed version.** Untouched — no storage tree is read or written. Named
  because `storage:` entries are the noisiest thing the file carries and a reader will look for it.
- **Subprocesses.** None added.
- **Every bug found on hardware becomes a replay fixture.** Standing; nothing found on hardware here.
- **Don't improvise architecture.** The shape is the Operator's ruling. The design decisions this
  spec settles are D1–D5, D2a included, and are rung-local. **Two things are NOT** and neither is built past:
  the preview's contract edit (open question 1), and **D2a's change to `PUT /api/config`'s accepted
  bodies** — user-visible API behaviour, so it is recorded as rung-ruled decision 5, filed as
  quince#754, and open question 5 asks the architect where it should land. It is named here rather
  than left in the Design section because *"user-visible behaviour"* is the gap protocol's own
  trigger word and this is the near-miss.
- **Coverage is declared.** Each code PR carries its `go test -cover` line for
  `core/internal/config` and an explicit known-untested list.

---

## Rung-ruled decisions

1. **The declared set is derived, never persisted.** The file is its own record (D1). Rejected: a
   sidecar or an app-DB column, both of which introduce a second source of truth for a fact the file
   already carries, and both of which can disagree with a hand-edit.
2. **Prune a marshalled node tree; do not emit from a reflect walk.** Canonical key order is
   preserved for free and there is only ever one encoder (D2).
3. **CONFIRMED by architect review (quince#753, 2026-08-08): the round-trip check runs at RUNTIME,
   not only in tests.** `replaceLocked` re-parses the bytes it is about to write and compares them to
   the document it holds; on a mismatch it writes the **full resolved document** instead and returns
   a `Warning` naming the keys that did not survive. Cost is one in-memory parse per save, on a path
   already doing `fsync` + `rename`. What it buys is that every future marshaller bug in this class
   degrades into a fat file plus a visible warning, rather than into a config the daemon will not
   start on — quince#683's class, which this project has paid for once. **It is a fallback, so it is
   admissible only because it is surfaced**, which is exactly the test `no silent caps or fallbacks`
   sets.

   **Two conditions came with the confirmation and both are binding.**

   **(a) One comparator, two callers** — the runtime check and G4 must not be two implementations of
   *deeply equal*. See G4.

   **(b) It could not ship before D2a, and D2a is why.** The check compares re-parsed bytes — which
   are resolved — against the document `Service` holds, which on the `PUT` path is **not** resolved
   (facts 9, 10). So **every partial `PUT` would mismatch**, the fallback would fire on an ordinary
   request, and the user would get a fat file plus a warning about keys that were never wrong. That
   is a guard that cries on the happy path, which is worse than no guard: it trains a reader to
   ignore it. D2a's resolution makes it evaporate, which is the second reason D2a lands first.
4. **`storage[]` declared keys are identified by `name`.** See D3 and open question 2.
5. **Resolution moves to `replaceLocked`, and `PUT /api/config` becomes more permissive as a
   consequence.** D2a. Recorded as a decision rather than an implementation detail because it changes
   a documented `422`.

---

## Known gaps and open questions

1. **The preview's contract edit — genuinely unruled, and not this seat's.** Either
   `GET /api/config` gains the serialized file text (a `docs/contracts.md` §6 change — **not**
   code-owned since the Operator ruling of 2026-08-14, quince#953, so the architect can approve it),
   or the UI serializes YAML client-side. The issue argues the first is the honest one *because the
   server is the only thing that knows what it actually wrote*, and the architect agreed on
   quince#728. **What is still owed is a ruling on WHICH option, not an approval.** This rung is built as though the
   answer may be no: the preview PR is last, independent, and its absence costs nothing else.
   **A note for whoever rules it:** this rung makes the second option strictly worse. A client-side
   serializer must now reproduce not only Go's quoting and ordering but its *omission* decisions,
   which are the whole subject of D2.
2. **What a rename does to an entry's declared keys** — see D3. Nothing renames a storage through
   any surface today, so this is a correctness question with no live caller. It should be answered
   rather than left, because the first thing that renames one will find it.
3. **Whether `qn.6i` and this rung owe each other a check.** They touch disjoint trees —
   `config/` here, `reconcile.go` there — so the answer is expected to be no. Recorded because
   `qn.6h` left exactly this item for `qn.6i` and it cost a spec revision to notice.
4. **A save over an invalid or unreadable file discards the user's document** — D1's `OK: false`
   branches. Pre-existing, D12's last-good semantics rather than this rung's, and **not fixed here**.
   Raised because this rung makes it *less* visible: the replacement now looks deliberately minimal
   rather than obviously machine-written. Worth its own issue; not filed yet, because the right
   remedy is probably a UI refusal to save while the banner is up, which is a `qn.6`-family question
   rather than a config-package one.
5. ~~**Whether D2a lands in this rung or on quince#754 alone.**~~ **RULED 2026-08-08 (architect,
   quince#753): here, as PR 2.** Kept rather than deleted because it was ruled *both ways within a
   minute* — quince#754 said "its own PR, not folded into the rung" and quince#753 reversed it — so a
   reader who finds the earlier ruling needs this entry to know it was superseded. D2a carries the
   reasoning.

---

## PR slicing

Each PR branches from `main` and carries one reviewable claim. **Sequenced, never stacked** —
`CLAUDE.md` §1.

| | claim | proof |
| --- | --- | --- |
| **1** | **this spec** | architect review; `docs/specs/**` is not code-owned |
| **2** | **`replaceLocked` resolves before it validates** (D2a), closing quince#754: the `422`-stricter-than-the-file half, the `name: ""` half, and `retention: null` as a third. **Nothing about marshalling changes.** This slice was planned to need an Operator approval on top of the architect's, because `docs/contracts.md` was code-owned when the rung was written; **it is not since 2026-08-14 (quince#953), so a slice like this one now takes an architect approval alone.** The contingency planned here — split the contracts line into its own tiny PR if the wait bites — no longer has a wait to answer to | G4a, G7, a `curl` reproduction through the running handler, and the existing config suites untouched |
| **3** | **the declared set exists and round-trips.** `Parse` computes it, `Loaded` and `Service` carry it, the node-prune `Marshal` is written and unit-tested — **and nothing calls it.** `replaceLocked` still writes the full document, so `main` is unchanged in behaviour | G2, G4 against the new function directly |
| **4** | **the file stops inflating.** `replaceLocked` switches to the declared marshal; the diff against `old` supplies D2 clause 2; the runtime round-trip guard of decision 3 lands with it, sharing G4's comparator; the three false `qn.6` doc-comment comments are corrected | G1, G2, G4, G7 |
| **5** | **the materialisation gate.** D3's `default: true` rule, plus `AddStorage`/`ForgetStorage` declared-set maintenance, plus story 10's fresh-install path | G3, G4, G7 |
| **6** | **the wire stays complete** — story 6's reflect walk and the `omitempty` grep. Independent of 2–5 and could land first; it is placed here because it is the guard on what 4 and 5 changed | G5, G6 |
| **7** | *(open question 1 only)* **the preview is YAML** — contracts §6, the handler, `ConfigView.tsx` | G8, and the contract's own review |
| **8** | devlog: the `progress.md` row and the journal entry | DoD tail |

**PR 2 is new since the first revision and it is FIRST for two independent reasons**, either of which
would be enough: D2 clause 2 is undefined until both sides of its diff are normalized, and decision
3's runtime guard would otherwise fire on every partial `PUT`. It is also the only PR here that fixes
something users can hit today.

**PRs 4 and 5 both touch `replaceLocked`, and 5 is deliberately NOT stacked on 4** — it waits for 4
to land and branches from `main`. That costs one review cycle and is the trade quince#388's ruling
accepts, against the cost of an approved PR being closed by a `--delete-branch`. The same applies to
2 → 4.

**PR 3 exists so that PR 4 is a one-line switch with a large test, and review has CONFIRMED the
split** (quince#753): *"dead code for one merge is honest as long as the PR says so, and collapsing 3
into 4 would put a new encoder and the switch to it in one diff — which is the shape that makes a
bisect useless when the file comes out wrong."* PR 3's body must say it in those terms.
