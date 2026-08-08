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

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/config/service.go` | `Parse` returns the **declared set** alongside the config; `Loaded` carries it; `Service` holds it and updates it on every write; `Marshal` gains the declared-aware form and `replaceLocked` uses it. The `qn.6` doc-comment comments on `Marshal` and on the package are **false as of the ruling** and are corrected here |
| `core/internal/config/schema.go` | the `Config` type comment's *"qn.6 swaps Marshal for a yaml.Node encoder that also emits generated doc-comments"* — same correction. **No struct tag gains `omitempty`**; see D4 |
| `core/internal/config/add.go` | `AddStorage` marks the added entry's supplied keys declared, and materialises `default: true` on the incumbent when the list crosses from one entry to two (D3) |
| `core/internal/config/forget.go` | `ForgetStorage` drops the forgotten entry's declared keys |
| `docs/quince.stack.md` | **already landed** (quince#752). Nothing owed unless a decision here changes it |
| `docs/contracts.md` §6 | **only if open question 1 is ruled yes** — `GET /api/config` gains the serialized file text |
| `ui/src/features/settings/ConfigView.tsx` | **only if open question 1 is ruled yes** — the preview renders YAML instead of `JSON.stringify` |

**Out of scope, each with a why.**

- **File-watch live reload.** quince#727, post-`v0.1`, a separate rung by the same 2026-08-08 ruling
  — *"the two do not land together."* Its **doc-comment half is moot rather than deferred**, since
  the ruling deleted the thing it was staging toward.
- **Generated doc-comments.** **Cancelled**, not deferred. There is no smaller annotation to stage
  toward and nothing in this rung emits one.
- **Resolution semantics.** `schema.go`'s *"doing it in one place is what lets `- path: /backups`
  mean what the 2026-08-01 ruling says it means, without any consumer learning that a name might be
  empty"* survives untouched. `Parse` still resolves; the in-memory document is still the resolved
  one. **Only what gets MARSHALLED changes**, which is the ruling's own words.
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
   that was live**, which `replaceLocked` already has in hand as `old`.
3. **required to re-parse** — `storage[].path` on every entry (an entry without it is meaningless),
   and D3's `default: true`.

**A section is written only if something under it is.** Pruning is bottom-up, so a `backup:` with no
declared leaf does not appear as `backup: {}`.

**Mechanism.** `Marshal` keeps producing the full canonical document first, then **prunes the
`yaml.Node` tree** to the written set. Marshalling first is what preserves canonical key order for
free — struct field order is already the key order (`schema.go`'s `Config` comment), and a node prune
cannot reorder what it only removes. The same node walk, run over `old` and `next` in parallel, is
what computes clause 2. One traversal utility, two uses.

**The alternative — emit from a reflect walk over the struct, consulting `declared` per field — is
rejected** for the ordering reason above and for a second: it would have to re-derive the yaml key
names and nesting that `yaml.Marshal` already knows, which is a second implementation of the encoder
that can disagree with the first.

### D3 — `storage:` entries: keyed by name, and the one key quince must write that nobody set

**An entry's declared keys are keyed by its `name`, not its index.** Entries are added, forgotten,
and reordered; `DELETE /api/config/storage/{name}` already treats `name` as the identity, and
`config.AddStorage`/`ForgetStorage` splice by it. At parse time the name of a raw entry is
`raw["name"]` if present, else `raw["path"]` — the same rule `Resolved()` applies, computed one step
earlier.

**Open question 3 records that this is the detail most likely to be wrong.** A rename changes the
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
   (`backend: auto`, `zfs.mode: exec`, `zfs.seed: auto`, `name` = the path). It would remove
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
  worth more than G3 alone. See rung-ruled decision 3 for whether it also runs at runtime.
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
- **Interface facts are looked up live, never remembered.** Facts 1–8 were measured in this checkout
  at `09beb3a` today; fact 7 is the architect's measurement on quince#728, cited to it.
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
  spec settles are D1–D5 and are rung-local. The one thing that is **not** rung-local — the preview's
  contract edit — is open question 1 and is not built past.
- **Coverage is declared.** Each code PR carries its `go test -cover` line for
  `core/internal/config` and an explicit known-untested list.

---

## Rung-ruled decisions

1. **The declared set is derived, never persisted.** The file is its own record (D1). Rejected: a
   sidecar or an app-DB column, both of which introduce a second source of truth for a fact the file
   already carries, and both of which can disagree with a hand-edit.
2. **Prune a marshalled node tree; do not emit from a reflect walk.** Canonical key order is
   preserved for free and there is only ever one encoder (D2).
3. **PROPOSED, and the one thing here I would most like review to overturn or confirm: the
   round-trip check runs at RUNTIME, not only in tests.** `replaceLocked` re-parses the bytes it is
   about to write and compares them to the document it holds; on a mismatch it writes the **full
   resolved document** instead and returns a `Warning` naming the keys that did not survive. Cost is
   one in-memory parse per save, on a path already doing `fsync` + `rename`. What it buys is that
   every future marshaller bug in this class degrades into a fat file plus a visible warning, rather
   than into a config the daemon will not start on. **It is a fallback, so it is admissible only
   because it is surfaced** — which is exactly the test `no silent caps or fallbacks` sets. If review
   prefers tests-only, G4 stays and this decision is struck.
4. **`storage[]` declared keys are identified by `name`.** See D3 and open question 3.

---

## Known gaps and open questions

1. **The preview's contract edit — genuinely unruled, and not this seat's.** Either
   `GET /api/config` gains the serialized file text (a `docs/contracts.md` §6 change, code-owned by
   `@novkostya`), or the UI serializes YAML client-side. The issue argues the first is the honest
   one *because the server is the only thing that knows what it actually wrote*, and the architect
   agreed on quince#728; **it still needs a code-owner's yes.** This rung is built as though the
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

---

## PR slicing

Each PR branches from `main` and carries one reviewable claim. **Sequenced, never stacked** —
`CLAUDE.md` §1.

| | claim | proof |
| --- | --- | --- |
| **1** | **this spec** | architect review; `docs/specs/**` is not code-owned |
| **2** | **the declared set exists and round-trips.** `Parse` computes it, `Loaded` and `Service` carry it, the node-prune `Marshal` is written and unit-tested — **and nothing calls it.** `replaceLocked` still writes the full document, so `main` is unchanged in behaviour | G2, G4 against the new function directly |
| **3** | **the file stops inflating.** `replaceLocked` switches to the declared marshal; the diff against `old` supplies D2 clause 2; the three false `qn.6` doc-comment comments are corrected | G1, G2, G4, G7 |
| **4** | **the materialisation gate.** D3's `default: true` rule, plus `AddStorage`/`ForgetStorage` declared-set maintenance | G3, G4, G7 |
| **5** | **the wire stays complete** — story 6's reflect walk and the `omitempty` grep. Independent of 2–4 and could land first; it is placed here because it is the guard on what 3 and 4 changed | G5, G6 |
| **6** | *(open question 1 only)* **the preview is YAML** — contracts §6, the handler, `ConfigView.tsx` | G8, and the contract's own review |
| **7** | devlog: the `progress.md` row and the journal entry | DoD tail |

**PRs 3 and 4 both touch `replaceLocked`, and 4 is deliberately NOT stacked on 3** — it waits for 3
to land and branches from `main`. That costs one review cycle and is the trade quince#388's ruling
accepts, against the cost of an approved PR being closed by a `--delete-branch`.

**PR 2 exists so that PR 3 is a one-line switch with a large test.** If a reviewer can show that
splitting there ships something dishonest on `main` — a marshaller nothing calls is dead code for one
merge — the two collapse into one PR, and that is a fair thing to ask for at review.
