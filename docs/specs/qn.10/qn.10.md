# qn.10 — Messages: the Operator reads their conversations

## Goal

The Operator opens an unlocked backup and reads their messages — a chats list, a thread that
scrolls, attachments that open, and search that finds a conversation — served lazily from the
vault session, with nothing indexed that outlives the lock.

## Boundary

**In scope.**

- `core/internal/vault/messages/` — the domain reader over `qn.9`'s `parserfs`, and the
  session-scoped projection D2 rules for.
- `core/internal/httpapi/` — `GET /api/sessions/{id}/messages/chats`,
  `…/chats/{chat}/messages`, `…/search`, in contracts §1's frozen domain envelope.
- `docs/contracts.md` — `messages.*` appended, which §4 already anticipates
  (*"domain methods are appended here with their rungs (`qn.9+`)"*).
- `ui/src/features/messages/` — chats list, virtualized thread, attachment rendering.
- Fixtures under `core/internal/vault/messages/testdata/`.

**Out of scope, and why.**

- **`backup.FS`.** Built at `qn.9` (D7, quince#1456). See D1 — the scoping issue assigned it here.
- **The other five domains.** `contacts`, `calls`, `calendar`, `notes` already open on the same
  plumbing and are their own rungs.
- **Photos** — `qn.11`, parked.
- **Balloon payload decoding.** `ios-backup-parser` does not decode `payload_data` at v0.2.0.
  quince surfaces `BalloonBundleID` / `HasPayload` and names the limit; it does not guess.
- **Export, restore, reply.** quince reads.

---

## Interface facts — measured 2026-08-22/23, not recalled

Measurements are of three kinds and they are **not** interchangeable: sizes read off the
Operator's real backups on the staging stand; timings against a **synthetic** database; and
facts 4–7, which are **quince's own code path against the real backups**, read-only, in the
pinned `golang:1.26.5-alpine3.24`. Every figure says which it is.

**The personal data never left that box, AND NONE OF IT MAY ENTER THIS DOCUMENT.** What is
reproduced here is counts, byte sizes, timings and schema object names — the last are facts about
Apple's format, not about a device. **No message text, handle, phone number, group name,
attachment path, device identifier or bundle id appears below, and none may be added.** Devices
are called **A**, **B** and **C** throughout, and this rung is the one document in the repository
where that rule is worth the most, because its subject matter *is* personal content.

### 1. `sms.db` is large, and one real device is very large

Read on the stand across three device trees, by the format's public file id for
`HomeDomain-Library/SMS/sms.db` (no decryption, no password, nothing left the box):

| device | `sms.db` on disk | `-wal` | `-shm` | `-journal` |
| --- | --- | --- | --- | --- |
| A — unencrypted | 479,232 B | absent | absent | absent |
| B — encrypted | **468,078,608 B — 446 MiB** | absent | absent | absent |
| C — not a backup (fact 7) | absent | absent | absent | absent |

**These are sizes on disk**; for B that is the encrypted file. Fact 4 measures the decrypted
one and the difference turned out not to matter.

**Two consequences, and one non-consequence.** `Materialize` must decrypt and copy 446 MiB
before the first chat lists — measured in fact 4, and it is cheap. **No real backup here carries
a sidecar**, so `parserfs`'s sidecar copy — the code that keeps *never mutate a committed
version* true — is exercised by nothing, which is a fixture obligation (D8, G5) and not a reason
to remove it. **Device C is not evidence of anything**: it has no `Manifest.db` at all.

### 2. The parser's read API is a full scan, with no filter and no cursor

At the pinned `ios-backup-parser v0.2.0` (`core/go.mod`), the whole read surface is
`Open`, `Capability`, `Close`, `Chats()` and `Messages()`. `Messages()` streams *"every
`message` row in (date, ROWID) order"*. There is **no per-chat argument, no cursor, no offset,
and no by-id accessor**, and `enrich` issues per-row queries for chat membership and attachments.

**So, today, rendering any thread page means scanning the whole table and discarding almost
all of it** — and a page at the *end* of a thread costs the entire scan.

### 3. What the scan costs — synthetic, and the shape of the curve is the finding

Generated from the parser's own `messages.1` fixture, reusing its real `attributedBody`
typedstream blobs verbatim so decode cost is real decode cost. Every identifier invented.
70% of rows carry the body only in `attributedBody` (the modern-iOS shape); 10% carry an
attachment with `cache_has_attachments` set.

| database | `Open` | `Chats()` | full `Messages()` | per row |
| --- | --- | --- | --- | --- |
| 100k msgs, 200 chats, 22.4 MiB, iOS-shaped join indexes | 1.9 ms | 6 ms | **2.13 s** | 21.3 µs |
| 500k msgs, 400 chats, 114.1 MiB, iOS-shaped join indexes | 1.8 ms | 18 ms | **10.74 s** | 21.5 µs |
| 500k msgs, 400 chats, 121.3 MiB, **+10% attachments** | 1.7 ms | 19 ms | **12.69 s** | 25.4 µs |
| 100k msgs, 200 chats, 20.3 MiB, **NO join indexes** | 0.5 ms | 4 ms | **4 m 30.8 s** | 2707.7 µs |

**`Chats()` is cheap and `Messages()` is not.** The chats list is affordable today at any
size measured. The scan is linear at ~25 µs/row.

**The last row is the loudest.** Without indexes on the join tables the scan is **127× slower**,
because `fillChatIDs` and `fillAttachments` run one query per message against an unindexed
table. Real iOS ships those indexes; the parser's fixture does not, which is why this was
measurable at all. **It bounds a fixture rule (D8) and a refusal (D2).**

### 4. The real database, measured end to end — and the estimate this replaces was WRONG

Run on the stand through **quince's own path** — `vault.OpenEncrypted` → `Unlock` →
`parserfs.New` → `Materialize` → `messages.Open` — with `/mnt/quince` mounted **read-only**, so
the run could not perturb a committed version even in principle. Password read from a file into
memory; never in argv, never in an env var, never printed. Device B, iOS 26.6:

| | |
| --- | --- |
| `Unlock` | 1.926 s |
| **`Materialize`** | **928 ms for 468,078,608 B — 481 MiB/s** |
| `messages.Open` | 11 ms |
| capability | `schema=messages.1 supported=true` **`missing=[]`** |
| `Chats()`, all | **10 ms**, 390 chats (9 groups) |
| **`Messages()`, all** | **8.437 s, 254,949 messages — 33.1 µs/row** |
| `BodyUndecoded` | **0** |
| row errors | **0** |
| attachments | **21,777 yielded vs 21,777 join rows** |
| tapbacks / balloons | 5,878 / 4,811 |
| bytes per message | **1,836 B** |

**An earlier revision of this section predicted 0.5M–1.6M messages and a 13–40 s scan, by
extrapolating synthetic bytes-per-row. It was wrong in both.** Real rows are 1,836 B against the
synthetic 254 B — 95 columns, not 26 — so the real message count is **a quarter of the low end of
that range**, and the scan is 8.4 s rather than 13–40.

**The guard, which is why the failed estimate is recorded rather than deleted: do not size an
iOS table from a synthetic row.** The column count alone was a 7× error, and it moved the answer
in the direction that flatters the design. The measurement was cheap; the estimate was not worth
making.

**`Materialize` is not the problem, and that was worth checking rather than assuming.** 446 MiB
of decrypt and copy costs under a second — below the `Unlock` that precedes it.

### 5. Real iOS ships the join indexes. The 127× is a FIXTURE hazard, and now a measured one

The real database carries **82 indexes** over 95 message columns, among them
`chat_message_join_idx_chat_id`, `chat_message_join_idx_message_id_only` and
`message_attachment_join_idx_message_id` — exactly the ones fact 3's slow run lacked.

**So the 127× cannot happen on a real backup and can happen in CI**, because the parser's own
fixture has none of them. That makes it a fixture rule (D8), not a runtime risk.

### 6. The attachment cache was ACCURATE on the real backup — the check stays anyway

`enrich` calls `fillAttachments` only when `message.cache_has_attachments` is non-zero.
Measured with a control on a synthetic database: five valid, non-dangling
`message_attachment_join` rows with the column at its default `0` yields **1000 messages, 0 with
attachments** — the join is never read.

**On the real backup there is no shortfall**: 17,294 messages carry the flag, and all **21,777**
join rows surfaced as 21,777 attachments. **No defect was found in the Operator's data**, and this
paragraph must not be read as reporting one.

The reconciliation in D5 stays because it costs one `COUNT(*)` per session and converts a
would-be silent drop into a named warning. **It is a guard, not a fix.**

### 7. The third tree is NOT a backup, and the second one is the empty case

- **Device C has no `Manifest.db`** — `vault: manifest is unreadable`. It is a 512-byte
  directory, not a backup with no messages. **An earlier revision of this spec cited it as
  proof that *no messages in this backup* is a real case. That was wrong.**
- **Device A is UNENCRYPTED** (`iosbackup: backup is not encrypted`) and needs
  `OpenUnencrypted`. Through that path: `Unlock` 103 ms, `Materialize` 8 ms, schema
  `messages.1`, `supported=true`, `missing=[]` — and **0 messages, 0 chats**.

**Device A is the case story 7 needs, and it is a better one**: a valid, supported, *empty*
messages database. *You have no messages* and *quince cannot read your messages* are different
sentences, and this is the backup that can tell them apart.

### 8. `backup.Capability` has no notion of search

`Capability` is `{Domain, Supported, Schema, Missing}`. The envelope's advertised
`"search"` capability is **quince's**, not the library's — so it is quince's to build and
quince's to gate (D4).

---

## Design

### D1 — This rung is the VIEWER. The seam was built at `qn.9`

quince#1483 scoped `qn.10` as *"the vault becomes a `backup.FS`, proved by a domain"*, and
that work has merged: `core/internal/vault/parserfs` implements `Materialize`, `Exists`,
`ReadDir` and the sidecar copy, and `messages.Open` is already a wired prober in
`core/internal/vault/capability`. **Its three "needs a ruling before code" questions are
answered by shipped code** — the `FS` exists, `ReadDirFS` is implemented, and materialized
copies live in session scratch reclaimed by `qn.8`'s existing teardown.

Recorded as a decision because a reader arriving from that issue will otherwise plan the
seam twice, and because the issue's §8 split — *"prove the plumbing on a cheap domain first"* —
had exactly one purpose, which is now served.

### D2 — ONE scan per session builds a projection, taken on FIRST MESSAGES USE, never at unlock

Fact 2 says the parser cannot page a thread; fact 4 says one full scan of the Operator's real
254,949-message database is **8.437 s**. Three routes were considered and the measurement chose
between them:

- **Full scan per page.** Refused. **8.4 s to open a conversation, and 8.4 s again for a page at
  the far end of it.** That is not a user interface at any scan speed this API can reach.
- **quince opens the materialized `sms.db` with its own SQL.** Refused. It duplicates the
  schema-fingerprint and `attributedBody` work the library exists to own, and puts two
  parsers of one format in the project.
- **A per-chat cursored accessor upstream in `ios-backup-parser`.** Good, and **not
  sufficient alone**: search (D4) requires reading every message's text once regardless, so a
  full scan happens once per session either way.

**The ruling: quince performs ONE `Messages()` scan per session and writes a session-scoped
projection into the session's own scratch** — a small SQLite database holding the fields the
surfaces need (message id, guid, chat ids, date, direction, handle, text, attachment refs,
association and edit markers). Chats come from `Chats()`, which is **10 ms on the real backup**
and cheap enough to read live.

**WHEN the scan happens is a separate decision from HOW the data is read, and it is ruled
separately: the scan is LAZY, taken on the first read of a `messages` surface.** Not at unlock.

**Because `qn.8`'s file browser is shipped and in use, and an unlock is not a request for
messages.** A scan at unlock would put ~9.4 s on **every** unlock — including one whose only
purpose is to download a single file, a surface that costs a user nothing today. That is a
user-visible regression on shipped behaviour, and it buys nothing: **the projection is read by
Messages surfaces and by nothing else.** *(Ruling requested and given at spec review,
quince#1491 — the finding was that D2 named the scan three times and never weighed its timing.)*

**Nothing is lost by deferring, and this is the part worth checking rather than assuming.**
*Does this backup have messages at all* — story 7 — is answered by `qn.9`'s capability prober,
which already opens the domain at unlock and does **not** scan; fact 4 measures `messages.Open`
at **11 ms** against the 8.437 s scan. So the chats list, the capability report and the *no
messages in this backup* answer are all reachable without it.

**The measured cost, once, when the user asks for Messages: 18.256 s** — 815 ms to materialize,
15.944 s to scan *and write the projection*, 252 ms of indexes, 1.235 s to build FTS5 — **paid once
per session instead of once per page**, against a projection of 63.1 MiB in session scratch.

**This paragraph said "about 9.4 s" until slice 2a, and that was the SECOND cost in this spec
stated from a measurement of something narrower than the claim.** 9.4 s was `Materialize` plus the
**bare** 8.437 s scan; the scan that also *writes* the projection costs 15.944 s. The first
instance was fact 4's withdrawn row-count estimate, and the guard generalises from both: **measure
the thing you are claiming, not a component of it.** Both errors flattered the design.

**What the projection buys, measured on the same backup — this is the half that held:**

| | through the parser | off the projection |
| --- | --- | --- |
| a thread page at the far end of a conversation | **8.437 s** | **265 µs** |
| a thread page, newest 50 | — | 189 µs |
| the chats list | 10 ms (`Chats()`) | 22.9 ms |
| search over 254,949 messages | not possible | 67–478 µs |

**The chats list is the slowest query and it is the FIRST screen**, because it aggregates over all
236,372 chat links. 22.9 ms is fine; if it ever is not, a per-chat summary row written during the
scan turns it into a seek. Named here so slice 3 decides it with the surface in front of it rather
than pre-emptively.

**This is not a persistent index and does not become one.** It lives in the session scratch
`qn.8` already wipes on lock, TTL and shutdown; nothing version-keyed, nothing on the app DB,
nothing surviving the lock. That is the same rule the roadmap states for FTS5 —
*"session-scratch FTS5 search"* — arriving one layer earlier. **Ruled at spec review not to be a
storage-semantics change** (quince#1491): it is derived, session-scoped and self-destroying, and
the versioned lifecycle it must not touch — `latest/`, `working/`, `versions/`, `@quince-*` — it
does not touch.

**The scan is surfaced, never hidden.** 18 s is long enough to need a progress report and a
named failure state, per *no silent caps or fallbacks*, and short enough that no background-job
machinery is warranted. **Deferring makes that report better as well as cheaper**: the user has
just asked for Messages and is waiting for something they requested, rather than waiting at
unlock for a reason the screen cannot explain. The upstream accessor stays worth doing as a later
optimisation and is **not** this rung's dependency.

**THE TRIGGER IS FINER THAN "A MESSAGES SURFACE", AND SLICE 2B MEASURED WHY.** `Chats()` is
answerable **live** — 23 ms for 390 conversations on the real backup, with no projection — so the
chats list, which is the *first* Messages screen a user sees, costs nothing and does not build.
The scan is triggered by opening an actual conversation. **This is D2's own rule (*nothing needs
it until something reads it*) applied one level finer**, and it means the deferral survives a user
who browses Messages and never opens a thread.

Measured through the reader on the real backup: `Available` 808 ms and `Chats` 23 ms with **the
projection absent after both**, then 15.5 s on the first `Thread`. `Available` is not free —
it materializes the 446 MiB database — but `qn.9`'s capability prober already pays that at
unlock, so this adds nothing to an unlock that was happening anyway.

**AT 18 s, STORY 10'S PROGRESS REPORTING IS LOAD-BEARING RATHER THAN A COURTESY** — architect,
quince#1496: it *"becomes the thing that decides whether this feels broken"*, and **its absence at
the surface slices is reviewable as a defect, not an omission.** The reader emits progress every
10,000 messages, which is about four times a second at the measured rate. `Progress.Total` is
deliberately absent: the parser does not count rows up front, so a percentage would be invented.

### D2b — The vault is held for `Materialize` ONLY. The scan runs outside the session lock

`vault.Registry.With` is **exclusive and non-blocking** — `TryLock`, else `ErrSessionBusy` — and
the house pattern for per-session derived state (`vaultsvc.capabilityReport`) wraps its whole
build in it. Doing that here would refuse every other call on the session for the ~16 s the
projection takes: a browse, a file download, and the user's own second click.

**Measured on the real backup, both arms, slice 3:**

| arm | build | concurrent vault calls during it |
| --- | --- | --- |
| build inside `With` | 16.331 s | **ok=0, BUSY=808** |
| **materialize inside `With`, scan outside** | 1.118 s + 12.144 s | **ok=531**, BUSY=56 |

The first arm is the **control**: it proves the probe can detect contention, without which the
second arm's 531 successes would be a probe that was never testing anything. The 56 refusals in
the second are exactly the 1.118 s materialize window, and that residue is unavoidable — the vault
must decrypt.

**So there is no trade-off here and nothing to rule on.** This was raised as a question needing a
decision and answered by measuring instead.

**SCANNING OUTSIDE THE LOCK IS SAFE BECAUSE `parserfs` MEMOISES, AND THAT IS A SAFETY PROPERTY
RATHER THAN A SPEED ONE.** If the memo missed, the scan's own `Materialize` would reach the vault
outside `With`, concurrently with another request — a race, and **a race need not produce an
error**, so *"the arm ran without failing"* would not have been evidence. Measured directly: a
second `Materialize` of the same file is **1 µs against 806 ms** and returns the **identical**
path. A miss would re-decrypt 446 MiB and, since each copy carries its own sequence number, hand
back a different path.

**THE MEMO ONLY HELPS FOR THE KEY THAT WAS PRE-MATERIALIZED, SO THE PROPERTY IS NOW CHECKED
RATHER THAN DOCUMENTED.** A scan that materializes any *other* file misses the memo, reaches the
vault outside `With`, and races — and by the argument above, that race need not announce itself.
Today the property holds because `messages` asks for exactly one file and the parser asks for the
same one. **Slice 5 joins attachments, which live under `MediaDomain`** — so the slice most likely
to break this is already on the roadmap (architect, quince#1498).

`msgfixture.CountingFS` records every materialize request, and
`TestScanMaterializesNothingBeyondThePreMaterializedFile` asserts the scan asks for nothing beyond
the pre-materialized key, with a control that it asked for *something*. The guard was **proven to
fail**: an off-key `Materialize` injected into the real scan path was caught and named, then

**AND THAT GUARD WAS NARROWER THAN THE HAZARD — MEASURED, AND IT COST A REAL DEFECT.** Counting
`Materialize` answers *what did the scan ask the filesystem for*. The property that has to hold is
*how many times did the scan reach the vault*, and those came apart exactly where the memo did:
`Materialize` was memoised and `Exists` was **not**, so `Exists` → `lookup` → `vault.List` ran on
every call. **The scan was making an unsynchronised vault call, outside `registry.With`, from the
moment slice 2b merged** — against this seam's own rule that *"`vault.Vault` makes no concurrency
promise and the session registry serializes access."*

Reproduced by disabling the memo read and running the guard: `the scan reached the vault 1
time(s): [List]`.

**The fix is at `parserfs`, not at the caller: `lookup` is memoised, including MISSES.** A
committed version's manifest is immutable by hard rule, so caching a path→entry answer is sound by
construction rather than by luck; a *failed* lookup is not cached, because that failure is about
the moment rather than about the file. Both `Exists` and `Materialize` go through `lookup`, so one
memo covers both.

**And the guard now sits at the seam where the property is true or false.**
`msgfixture.CountingVault` counts what reaches the **vault**, and
`TestScanReachesTheVaultZeroTimes` asserts zero — with a control that phase 1 reached it, or the
test would pass against a stub. **This is the fourth instance in this rung of measuring a
component and reporting it as the whole, and the first where the narrow check was one built to
prevent that class.**
reverted. **A slice that needs a second file must hold the vault for it, or pre-materialize it too
— and this test is what will say so.**

**Session scratch peaks around 762 MiB** on device B: decrypted `Manifest.db` 252.9 MiB +
materialized `sms.db` 446 MiB + projection 63 MiB. Not a constraint on any host quince targets,
and worth knowing before several sessions open Messages at once.

### D3 — A thread pages by `(date, ROWID)` cursor, in the frozen envelope

`page.next_cursor` per contracts §1, opaque, ordered by `(date, ROWID)` — the parser's own
order, so a cursor means the same thing in the projection and in the source. `limit` clamps and
**discloses** its clamp exactly as `browse` does. Newest-first for the thread, because that is
where a reader starts.

**A CURSOR QUINCE DID NOT ISSUE IS A 400, NOT A 500.** `ErrBadCursor` is its own error at the
reader, so *"that page marker is not one quince issued"* and *"could not read this conversation"*
stay apart — **reload the page** and **this backup is damaged** are different remedies, and
*troubleshooting is actionable* names collapsing them as a defect even when both sentences are
true. Slice 4 found this by writing the service against an `ErrBadCursor` that did not exist yet:
the reader was returning an unwrapped error, so a mistyped cursor would have been reported as an
unreadable backup.

**THE THREAD ROUTE IS THE ONE THAT PAYS FOR THE PROJECTION, AND IT BLOCKS.** ~18 s on the first
conversation opened in a session; 265 µs for every page after. The server's write timeout is 120 s
(`core/cmd/quince/main.go`), so the request completes — **checked rather than assumed**, because a
handler that cannot finish inside the server's own deadline fails in a way no test of the handler
would show.

**Progress is NOT reported on this route and cannot be**: a synchronous JSON response has nowhere
to put it. The reader takes an `onProgress` callback and slice 4 passes `nil` deliberately.
Delivering it is the surface slice's, over the WebSocket that already carries job progress — the
callback is the seam it attaches to. Named here so that *"the route reports no progress"* is a
recorded decision rather than something slice 7 discovers.

**AND IT IS NOT AN EITHER/OR — THE SENTENCE ABOVE ALREADY CHOSE, WHICH A LATER SLICE FORGOT.** 7c
was raised as *"indeterminate spinner versus a WS event"* and routed for a ruling; the architect's
answer was that the question was settled here, in slice 4, by the words *"over the WebSocket that
already carries job progress"*. **An indeterminate spinner INSTEAD would contradict a decision
already in this spec, and story 10 with it. Build the event** (ruled 2026-08-23, quince#1483).

**The distinction that made it look open, stated so it stops doing so:** `Progress` deliberately
has no `Total` — the parser does not count rows up front, so any percentage would be invented — and
the wait state is therefore **indeterminate in its RENDERING** while still carrying a live count.
*Indeterminate rendering* and *no event at all* are different things, and conflating them is what
turned a settled decision back into a question.

**What is genuinely open is the event's SHAPE and SCOPE CLASS, and those go to PR review rather
than to a ruling.** The architect's reading, offered so 7c is not blocked on it: the event describes
one session, a session belongs to one device, so it takes **`scopedOwnDevice`** — the class every
other messages route takes. A holder scoped to their own device should see their own scan's
progress; nobody else should learn a scan is running at all. **If the precedents point both ways,
name the two and it will be ruled on the PR.**

**BUILT IN 7c-1, AND HERE IS WHAT THE SHAPE AND SCOPE CLASS CAME OUT AS.** The event is
`messages.indexing`, carrying `{session_id, udid, messages}`, classified **device-bearing** in
`wire.EventDevice` — the class the architect's reading proposed. The precedents did **not** turn out
to point both ways: `session.locked` is global because a client that misses it is left *wrong*
(showing decrypted views of a dead session), where a client that misses a progress frame is merely
uninformed. That distinction is now written into `eventscope.go` and `contracts.md` §3, because the
next session-shaped event will look at `session.locked` and see only its subject.

**Throttled at the PUBLISHER, at 500 ms, matching `job.updated`'s ≤2/s.** The reader's
`progressEvery` is a row count and the contract's promise is a rate; a row count cannot hold a rate,
because the per-row cost is what varies between machines. At the measured ~25 µs/row, 10,000 rows is
about four frames a second — twice what the table promises.

### D4 — Search is FTS5 over the projection, session-scoped, and it is a CAPABILITY that can be off

An FTS5 table built alongside the projection in the same scan. It advertises `"search"` in the
envelope's `capabilities` **only when it was actually built**; if FTS5 is unavailable or the
build failed, `search` is absent from `capabilities` and a `warnings` entry says why. The
surface then hides the search box rather than offering one that returns nothing.

**BUILT IN 7e — AND "THE SURFACE HIDES THE BOX" CANNOT BE IMPLEMENTED AS WRITTEN.** No envelope
carries the answer before a search happens: `MessagesChats` and `MessagesThread` both report
`["threads", "attachments"]` and nothing else, and **only the search response reports `search`**.
Nor is that an oversight to fix by widening them — the FTS table is written during the one
projection scan, so before any conversation has been opened in a session there is genuinely no
index to advertise, and a chats envelope claiming either way would be guessing.

**So the box is OFFERED and the ANSWER decides.** A first search pays for the same ~18 s scan a
first conversation pays for, narrated by the same `messages.indexing` count; then the response's
`capabilities` says whether the index exists, and `warnings` says why not. **The hiding D4 asks for
happens on the result screen rather than before the question** — which keeps the promise that
matters (never report *no results* for an index that was never built) and drops the one that
cannot be kept (knowing in advance).

**The four outcomes stay apart** (`SearchResults`): `unsupported_reason`, no `search` capability,
zero hits, and hits. Reporting the second as the third tells somebody they never wrote a word they
may well have written.

**`searchable` reads the CAPABILITY, never `items.length`** — an index that exists can legitimately
return nothing. That distinction is pinned by a test asserting the discriminating case, hits present
with the capability absent; without it the assertion passes under either implementation, which was
**measured** rather than assumed.


**BUILT AS AN EXTERNAL-CONTENT INDEX (`content='msg'`), so the bodies are not duplicated.** FTS5
stores the terms and points back at `msg.id` for everything else — which matters on a rung whose
scratch already peaks near 762 MiB.

**Failing to build is NOT an error and must never lose the reader.** A SQLite without FTS5, or a
rebuild that fails, leaves the chats list and every thread working; `buildSearchIndex` returns
nothing and records a warning, and the capability is derived from whether the index exists rather
than asserted. Slice 6 measured the build at **1.235 s** over 254,949 real messages, against the
15.9 s the scan itself costs.

**A blank query and an unparsable one are both the CALLER's, and both are 400s** —
`ErrEmptyQuery` and `ErrBadQuery`. Answering either with an empty result would state *nothing
matched*, which is a claim about the user's messages that nobody checked. The term reaches FTS5 as
a **bound parameter**; it is still interpreted by the matcher, which is why an unparsable one gets
its own message rather than an error about the backup.

**Each hit carries its `chat_ids`.** The only useful action on a search result is *open the
conversation this came from*, and a hit with no home cannot offer it.
**A missing capability is a fact about quince and is reported as one**, never as an empty
result set — `capability.go`'s own rule, one layer up.

### D5 — `cache_has_attachments` is trusted for READING and verified for REPORTING

quince does not second-guess the parser: attachments come from the records `Messages()`
yields. But fact 5 makes a stale cache column a silent drop, so the scan **also** counts
`message_attachment_join` rows and compares that total against the attachments actually
yielded. A shortfall becomes a `warnings` entry naming both numbers.

**This is the cheapest possible check** — one `COUNT(*)` on the materialized copy, once per
session — and it converts an invisible failure into an actionable one, which is what
*troubleshooting is actionable* asks for.

### D6 — Attachments reuse `qn.8`'s download path. No new file-serving surface

`Attachment.File` is a `*backup.FileRef` into `MediaDomain` — a file `qn.8` already streams
and already has a download route for. This rung builds the **join**, not a second way to serve
bytes.

**`File` is nil when `attachment.filename` is NULL** — not downloaded, purged, or iCloud-only.
The parser reports that rather than fabricating a path, and the surface says *not in this
backup* rather than offering a link that 404s.

**BUILT IN 7d, AND "INLINE WHERE THEY ARE IMAGES" NEEDED NARROWING.** The test is not *is this an
image* but *will this browser draw it*. **iOS backups are full of `image/heic`, which no browser
except Safari renders** — so a `startsWith("image/")` check points an `<img>` at a file the browser
cannot decode and shows a broken-image icon, which is the surface asserting the photo is damaged
when it is fine and simply not displayable here. Attachments are therefore drawn from an
**allowlist** of formats browsers reliably render, and everything else — HEIC included — becomes a
named link, which always works. An `onError` fallback covers the allowlist being wrong about a
particular browser.

**No blob fetch and no object URL.** The API is cookie-authenticated and `same-origin`, and CSRF is
required only for mutating methods, so a bare `<img src>` on `qn.8`'s file route authenticates
itself. The join is the whole of this slice, as D6 says.

**MEASURED, BECAUSE THE ALLOWLIST IS DOWNSTREAM OF A QUESTION NOBODY HAD ANSWERED** (quince#1521
review). `handleSessionFile` serves every file as `application/octet-stream`, with
`Content-Disposition: attachment` and `X-Content-Type-Options: nosniff` from `securityHeaders` — so
an `<img>` is being asked to decode bytes declared as a non-image type by a server that has said *do
not guess*. If browsers refused, the allowlist would be decorative and every attachment a link.

**They do not refuse.** 2026-08-23, Playwright, a 1×1 PNG served with that exact header triple, each
engine with an `image/png` control alongside:

| engine | control | as the file route serves it |
| --- | --- | --- |
| chromium | decodes | **decodes** |
| firefox | decodes | **decodes** |
| webkit | decodes | **decodes** |

`nosniff` governs script and style MIME checking; that it does not block an image decode is what was
measured, not why. **`ui/e2e/story13-attachment-decodes.spec.ts` keeps it true** — the failure mode
it guards is invisible, because `onError` would turn a total failure into "every attachment is a
link" with every unit test still green.

**AND IT DOES NOT REOPEN quince#1397, which ruled `inline` OUT.** That ruling is about serving backup
content with a *real content type* inside quince's origin, where an SVG or HTML file executes script
with the session cookie in scope. **No header changes here**: the type stays `application/octet-stream`
and the disposition stays `attachment`. An `<img>` decodes bytes and executes no script — not even
for SVG, which is script-disabled in that context — and the allowlist is raster-only, so SVG never
reaches it. The stored-XSS surface quince#1397 closed stays closed.

### D7 — `Missing` maps onto the envelope, field by field, and an empty field is never silently empty

**"NO NEW FILE-SERVING SURFACE" FORBIDS A SECOND WAY TO STREAM BYTES, NOT A SECOND WAY TO NAME A
FILE** — ruled at quince#1483, and slice 5 builds it as a second parameter shape on the existing
route rather than a sibling route. The test the ruling applies: **does a byte ever leave quince
through code that is not the existing handler's?** It does not — same handler, same stream, same
headers, same short-read detection, same scope class.

**Measured, which is why the shape is this one.** Resolving one path to a file id costs **51 ms**;
a page of 50 messages carries 11 present attachments, so resolving per page would cost **562 ms of
exclusive session lock** on every newly-scrolled page. Resolving **at download time**, for the one
file the user actually clicked, costs one lookup. A bulk walk was measured at **15.769 s** and
rejected — it would take first-open from ~18 s to ~34 s, under the lock D2b exists to keep short.

**Two constraints came with the ruling and both are structural.** The lookup is a **vault call**,
so it happens inside the held session — doing it in the caller would reintroduce quince#1501's
unsynchronised call one slice after fixing it. And it is **one acquisition**: `OpenStreamByPath`
resolves and opens inside a single `busy` hold, because resolving then calling the id route leaves
a gap in which a lock or a TTL sweep can end the session.

**The match is exact, never by prefix.** A prefix matches `a.jpg` for `a` and would serve a
different file's bytes than the one asked for — the worst available failure on a download route.
It also means this design leans on **no** prefix selectivity, which is why quince#1505's
unexplained figure does not block it.

`backup.Capability.Missing` names the units this schema cannot provide. Each maps to the
envelope: absent `chats` → no chats list and an `unsupported_reason`; absent `handles` → no
participants, said so; absent `attachments` → no attachment rows, said so.

**"ABSENT CHATS" IS ITS OWN CAUSE AND MUST NOT BORROW THE UNSUPPORTED ONE.** The reader carries
two distinct errors: `ErrUnsupported` — *this backup has no readable Messages database* — and
`ErrChatsUnavailable` — *the database is readable and its schema has no conversations*. Messages
still stream in the second case; only the grouping is gone.

**Slice 2b found this by writing the test wrong.** The test asserted one error for both, the code
returned two, and **the code was right**: collapsing them is what *troubleshooting is actionable*
names as a defect *even when every word is true*, because the two have different screens and
different remedies. The test now asserts they are distinguishable, which is the assertion worth
having.

**`BodyUndecoded` is the sharp one.** `Text == ""` with `BodyUndecoded` set means the body is
**unknown**, not empty, and the thread renders it as unknown. Rendering it as an empty bubble
is the wrong-but-plausible failure the parser's own charter forbids, and *state honesty*
forbids here.

### D8 — Fixtures carry the join indexes and a live `-wal`, and every identifier is invented

Two obligations fact 1 and fact 3 create:

- **Indexes.** A fixture without join indexes runs 127× slower and would mis-measure every
  timing this spec rests on. Fixtures carry iOS-shaped indexes on `chat_message_join` and
  `message_attachment_join`.
- **A live `-wal`.** No real backup here has one, so `parserfs`'s sidecar copy — the code that
  keeps *never mutate a committed version* true — is covered by a fixture or by nothing.

**Every identifier is invented, not anonymised**: no real handle, phone number, group name,
message text or attachment path. Precedent quince#1425, *"INVENTED PATHS, ALWAYS"*, and
`qn.9` D8.

### D9 — The surface

Chats list (name or participants, timestamp, group marker), a thread **whose DOM is bounded by
paging**, attachments inline where they are images and as named links otherwise, participants for
a group, and a search box **when D4 says there is one**. Tapbacks, edits and unsends are rendered
as what they are; app-message balloons say which app and that quince does not decode the payload.

**"LAST MESSAGE" WAS STRUCK FROM THAT LIST, AND IT ASKED FOR THE THING D2 FORBIDS.** A preview
needs message data, which needs the projection, whose ~18 s scan D2 defers **precisely so the chats
list costs nothing**. So this section described a feature the same spec's ruling rules out — and D9
is what a session building the chats list would read. **D2 wins and it is not close**: D2 is backed
by measurement (23 ms live against ~18 s), and D9's phrase predates that cost being known. Ruled by
the architect, 2026-08-23 (quince#1483), who also notes it is theirs: they approved the spec and
ruled the deferral that contradicts it.

**It is the fourth instance on this rung of a document describing a reality one step from the
code's, and the FIRST that would have produced a wrong FEATURE rather than a wrong sentence** — the
others cost a stale heading, a superseded figure and an overstated guard.

**"VIRTUALIZES" IS NARROWED TO "the thread's DOM is bounded, by paging rather than by a
virtualizer"** — ruled 2026-08-23 (quince#1483), and a narrowing of an approved spec rather than an
implementer's choice, which is why it was routed.

**The cursor already bounds it.** The route pages at 50 and caps at 200, so nothing renders that has
not been fetched. **Virtualization guards against rendering what you already hold; the cursor guards
against holding it at all** — a second bound on the same axis is what a virtualizer buys, and it is
worth measuring before paying for.

**The alternatives were refused on cost and reversibility.** A library adds a runtime dependency to
a bundle already warning at **621.64 kB**, on a project whose primary client is a phone over Wi-Fi.
Hand-rolled windowing adds code whose bugs — scroll anchoring, variable heights, position after
prepending — are **invisible in component tests**, the worst pairing of cost and undetectability
available. **Paging can become a virtualizer in one PR; neither of the others can become paging
without deleting work.** What is ruled is the ORDER: the cheapest reversible thing first.

**THE MEASUREMENT IS OWED, NOT WAIVED, AND IT IS A GATE ON THIS RUNG CLOSING.** *"A user would have
to scroll a long way"* is an assertion. **Render N rows, time interaction, and record the number
here.** If a plausible scroll makes it bad, a virtualizer lands and this narrowing is spent —
without re-litigating the order.

**BUILT IN 7c-2b, AND THE MEASUREMENT IS STILL OWED.** The thread renders what the cursor fetched —
50 a page, "load older" walking backwards — and holds no scroll state of its own, so swapping in a
virtualizer stays a one-PR change if the number says it should. **`Thread` takes rows and renders
them**, deliberately, to keep that door open.

**Nobody has rendered N rows and timed interaction yet, and this rung does not close until somebody
has.** It cannot be done from a session box: there is no browser, and `gates-ui-e2e` runs against
`--demo`, which carries no unlocked backup and therefore no thread to scroll. **So it belongs to
G6's walk on the stand**, with the number recorded here — not to a component test that would measure
jsdom rather than a phone.
not decode the payload.

**Per `read user-facing text as a user`:** no `unsupported_schema`, no `RowError`, no
`attributedBody` on any screen.

---

## Stories

1. The Operator unlocks a backup, opens Messages, and sees their conversations listed with the
   most recent first.
2. Opening a conversation shows its messages, oldest reachable by scrolling, without the page
   freezing.
3. A group conversation shows its participants and its name.
4. A message with a photo shows the photo; tapping it downloads the file through the existing
   download route.
5. An attachment whose file is not in the backup says so, and offers no broken link.
6. Searching a word finds the conversations containing it, or the search box is absent and the
   report says why.
7. A backup whose messages database is present, supported and EMPTY (device A) reports *no
   messages in this backup* — not an error, not a bare empty list. A tree that is not a backup
   at all (device C) is a different sentence again.
8. A message whose body cannot be decoded is shown as unknown, never as empty.
9. Locking the session removes the projection, the FTS index and every materialized file.
10. Opening Messages on a large backup reports progress while the one-time scan runs, and if it
    fails says what failed and what to do. **Unlocking a backup and using only the file browser
    never pays that cost** (D2).

## Gates

Beyond `make gates` and `make gates-ui-e2e`:

- **G1** — fixture test: a chats list, a threaded page and a cursor round trip over the
  invented fixture; the last page is terminal.
- **G2** — fixture test: a schema missing the `chats` unit reports `unsupported_reason` and
  does not render an empty list.
- **G3** — fixture test: `BodyUndecoded` renders as unknown (story 8), asserted at the surface,
  not only in the reader.
- **G4** — fixture test: a database whose `cache_has_attachments` is stale produces the D5
  warning naming both counts. **This gate is the control for fact 6** and fails if the check is
  removed.
- **G5** — fixture test: a fixture carrying a live `-wal` opens through `parserfs` and the
  committed bytes are unchanged afterwards (hash before/after).
- **G6 — the SIZING half is CLOSED (fact 4); the CORRECTNESS half is owed.** M7's rung gate is
  *renders the Operator's real backup correctly, spot-checked against iMazing*, and it has two
  halves that this spec deliberately separates. **Closed:** the real counts, the scan time and the
  `Materialize` time are measured — a session can now size this rung without the Operator.
  **Owed:** whether the 254,949 messages render *correctly* is a human comparison against iMazing
  that no measurement substitutes for. **Owner: the Operator**, on a dev-deploy build with a
  click-list. It is not a park — the build ships with slice 7.
- **G7** — `make privacy-check` over the branch, and by eye over every fixture, before merge.


### G6's walk — what to do, and what to write down

**Slice 7f. The build is shipped and the list is here rather than in a PR body, because a PR body is
not where somebody looks six weeks later.** Two things are owed on hardware and neither can be done
from a session box: there is no browser, and `gates-ui-e2e` runs against `--demo`, which carries no
unlockable backup and therefore no conversation, no attachment and no thread to scroll.

**1. CORRECTNESS — the half M7's gate actually names.** Unlock a real backup, open Messages, and
compare against iMazing on the same device:

1. **The conversation list** — are the same conversations there, named the same way? Group chats
   should show a participant count rather than the word *Group*.
2. **One busy thread** — open the largest conversation. The first open takes **~18 s** and must show
   a climbing count, not a bare spinner. Later conversations open instantly.
3. **Ten messages against iMazing** — sender, time, and text. **Tapbacks, edits and unsends must read
   as what they are**, never as an empty bubble.
4. **One photo, one non-photo attachment** — a photo should render inline, a HEIC or video should be
   a named link. **A broken-image icon is a defect, not a quirk** (D6).
5. **Search a word you know is in an old message.** If the box says the index could not be built,
   that sentence and its reason are the finding.

**2. VIRTUALIZATION — the number D9's narrowing is conditional on.** In the busiest thread, press
*Load older messages* until several thousand rows are in the DOM, then scroll and type. **Record the
row count at which interaction stops feeling immediate.** If a plausible amount of scrolling reaches
it, a virtualizer lands and the narrowing is spent — without re-litigating the order.

**What to write down either way.** The rung does not close on *it looked fine*: record the device,
the message count, the first-open time, and the row count from (2). **Every disagreement with iMazing
becomes a replay fixture before it is fixed**, per the hard rule — the transcript matters more than
the diagnosis.
## Fixtures

**Built at TEST TIME by `core/internal/vault/messages/msgfixture`, not committed as binaries.**
`Spec` selects the variant; `Build` returns the database bytes, ready to hand to
`ios-backup-crypt/fixture` as a `File`'s `Data`.

| variant | what it exercises |
| --- | --- |
| `Spec{}` | 1:1 and group chats, sent and received, a tapback, an edited message, an unsent one, an attachment that resolves, an attachment with `File == nil`, and a `BodyUndecoded` body |
| `Spec{NoChats: true}` | the `chats` unit absent — degrade, do not fail (G2) |
| `Spec{NoAttachedCache: true}` | valid join rows with `cache_has_attachments` at 0 (G4) |
| `Spec{Messages: n}` | padding for paging, leaving the named cases undisturbed |
| `BuildWithWAL` | a **live** write-ahead log (G5) |

**This section named committed `testdata/*.sms.db` files until slice 2a, and generating them was
the better answer** — it is the house pattern (`vault/capability`, `vault/conformance`), and on a
rung whose fixtures model personal message content it means **there is no binary in git to review
for leakage at all.** The privacy obligation moves from *inspect these blobs* to *read this code*.

**Provenance.** The `messages.1` structure is derived from `ios-backup-parser`'s schema spec and
the observed real fingerprint; **every identifier, body, number and group name is invented.** No
byte of any fixture comes from a device.

**Two of these need a control to mean anything, and both have one.** `NoAttachedCache` is asserted
against the healthy fixture yielding a non-zero attachment count — otherwise it would pass on a
database that simply has none. `BuildWithWAL` is asserted by opening the main file **without** its
log and showing the parser sees fewer messages — otherwise an already-checkpointed database with an
empty `-wal` beside it would look exactly like a live one.

## Rule check

- **Never mutate a committed version** — nothing here writes to storage. All reads go through
  `parserfs`, which materializes a private copy into session scratch precisely so SQLite cannot
  checkpoint a `-wal` into the committed tree. **G5 is the assertion, not the assumption.** The
  projection (D2) is written to session scratch, never beside the backup.
- **State honesty** — D7: `BodyUndecoded` is unknown, not empty; a missing capability is
  reported as quince's limit, not as the user having no data; D5 turns a stale cache into a
  named warning instead of a quiet shortfall.
- **No silent caps or fallbacks** — the `limit` clamp discloses (D3); a failed FTS5 build
  removes the `search` capability and says why (D4); the projection scan is visible work with a
  named failure (D2, story 10), and it is not charged to an unlock that never asks for messages.
- **Troubleshooting is actionable** — *no `sms.db` in this backup*, *schema not recognised*,
  *session locked* and *scan failed* are four states with four remedies, never one sentence.
  Device C makes the first a real case.
- **Privacy is a commit-time gate** — the sharpest rule here, because this rung's subject matter
  IS personal content. Every fixture identifier invented (D8); no message text, handle, phone
  number, group name or attachment path from a real device in any committed file, test, PR body
  or screenshot; measurements in this spec are counts, byte sizes and milliseconds only. G7.
- **Secrets discipline** — the backup password reaches nothing via argv, env or logs; G6's
  hardware run does not print it and the projection never stores it.
- **Interface facts looked up live** — every parser fact above was read from the `v0.2.0` tree,
  and the pin from `core/go.mod`. The 127× index finding and the `cache_has_attachments` gate
  were **measured with controls**, not inferred; facts 4–7 are quince’s own code path against the
  Operator’s real backups, read-only, rather than an extrapolation — which is what caught the
  extrapolation this spec had to withdraw.
- **Docs are part of the diff** — `contracts.md` gains `messages.*` in the slice that builds it.
  Each PR declares `go test -cover` plus a known-untested list.
- **Every bug found on hardware becomes a replay fixture** — anything G6 turns up becomes a
  fixture before it is fixed.
- **Config tidiness (near-miss)** — if the scan or the projection needs a bound, it gets a sane
  default and UI editability with no restart, and no UI-only state. Preference is to derive both
  rather than add a key.
- **Don't improvise architecture (near-miss)** — D2 chooses between three routes rather than
  inventing a fourth, and it is a rung-local decision written into this spec because it changes
  no contract. **If review reads the session projection as a storage-semantics change, that is a
  ruling to take at this spec, before any code exists** — which is what this section is for.

## Slices

Sequenced from `main`, never stacked (`CLAUDE.md` §1). Each carries one reviewable claim.

| | claim | state |
| --- | --- | --- |
| **1** | this spec | **merged** — quince#1491 |
| **2a** | `msgfixture` — the fixture builder every later slice reads from, with G5 asserted | **merged** — quince#1496 |
| **2b** | the domain reader + the session projection and its one scan (D2) | **merged** — quince#1497 |
| **3** | `GET /api/sessions/{id}/messages/chats` (D3, contracts §1) | **merged** — quince#1498 |
| **3b** | the scan's materialize key set asserted, not documented (D2b) | **merged** — quince#1499 |
| **4** | `GET …/chats/{chat}/messages`, cursored (D3) | **merged** — quince#1500 |
| **5a** | `parserfs` memoises `lookup`; the scan reaches the vault zero times (D2b) | **merged** — quince#1501 |
| **5** | attachments: the join to `qn.8`'s download route (D6) | **merged** — quince#1506 |
| **6** | FTS5 search and its capability gate (D4) | **merged** — quince#1503 |
| **7a** | the chats list — the first Messages screen, which builds nothing (D9) | **merged** — quince#1507, roles corrected in quince#1508 |
| **7b** | `MessageRow` — the five states that all look like an empty bubble (D7, D9) | **merged** — quince#1509 |
| **7c-1** | `messages.indexing` — the scan's progress over the WebSocket, device-scoped (D2, D3) | **merged** — quince#1515 |
| **7c-2a** | the Messages route — session, unlock, and the chats list reachable (D2, D9) | **merged** — quince#1517, follow-ups in quince#1519 |
| **7c-2b** | the thread view, its paging, and the `messages.indexing` wait state (D3, D9) | **merged** — quince#1520 |
| **7d** | attachments in the thread: inline images, named links, the absent state (D6) | **merged** — quince#1521 |
| **7e** | the search box, and the four outcomes that all look like "nothing found" (D4) | **merged** — quince#1522 |
| **7f** | G6 to the Operator — the build, and the walk written into this spec | **open** — this PR |

**This table is a second part describing the whole, so it is stale by default after every
merge** — quince#409. Update it in the diff that changes what it describes.
