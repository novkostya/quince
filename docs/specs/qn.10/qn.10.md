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

**The measured cost, once, when the user asks for Messages: about 9.4 s** — 928 ms to
materialize plus 8.437 s to scan — **paid once per session instead of once per page.** Fact 4 is
what makes this a cheap decision rather than a reluctant one; an earlier revision argued the same
ruling from an estimate of 13–40 s that turned out to be wrong in the direction that flattered it.

**This is not a persistent index and does not become one.** It lives in the session scratch
`qn.8` already wipes on lock, TTL and shutdown; nothing version-keyed, nothing on the app DB,
nothing surviving the lock. That is the same rule the roadmap states for FTS5 —
*"session-scratch FTS5 search"* — arriving one layer earlier. **Ruled at spec review not to be a
storage-semantics change** (quince#1491): it is derived, session-scoped and self-destroying, and
the versioned lifecycle it must not touch — `latest/`, `working/`, `versions/`, `@quince-*` — it
does not touch.

**The scan is surfaced, never hidden.** ~9 s is long enough to need a progress report and a
named failure state, per *no silent caps or fallbacks*, and short enough that no background-job
machinery is warranted. **Deferring makes that report better as well as cheaper**: the user has
just asked for Messages and is waiting for something they requested, rather than waiting at
unlock for a reason the screen cannot explain. The upstream accessor stays worth doing as a later
optimisation and is **not** this rung's dependency.

### D3 — A thread pages by `(date, ROWID)` cursor, in the frozen envelope

`page.next_cursor` per contracts §1, opaque, ordered by `(date, ROWID)` — the parser's own
order, so a cursor means the same thing in the projection and in the source. `limit` clamps and
**discloses** its clamp exactly as `browse` does. Newest-first for the thread, because that is
where a reader starts.

### D4 — Search is FTS5 over the projection, session-scoped, and it is a CAPABILITY that can be off

An FTS5 table built alongside the projection in the same scan. It advertises `"search"` in the
envelope's `capabilities` **only when it was actually built**; if FTS5 is unavailable or the
build failed, `search` is absent from `capabilities` and a `warnings` entry says why. The
surface then hides the search box rather than offering one that returns nothing.

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

### D7 — `Missing` maps onto the envelope, field by field, and an empty field is never silently empty

`backup.Capability.Missing` names the units this schema cannot provide. Each maps to the
envelope: absent `chats` → no chats list and an `unsupported_reason`; absent `handles` → no
participants, said so; absent `attachments` → no attachment rows, said so.

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

Chats list (name or participants, last message, timestamp, group marker), a thread that
virtualizes, attachments inline where they are images and as named links otherwise,
participants for a group, and a search box **when D4 says there is one**. Tapbacks, edits and
unsends are rendered as what they are; app-message balloons say which app and that quince does
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

## Fixtures

`core/internal/vault/messages/testdata/`, all generated by a committed generator so they can be
rebuilt and reviewed:

- `messages.basic.sms.db` — 1:1 and group chats, a tapback, an edited message, an unsent
  message, an attachment with a resolvable `File`, an attachment with `File == nil`, a message
  with `BodyUndecoded`, iOS-shaped join indexes.
- `messages.stale-cache.sms.db` — valid join rows with `cache_has_attachments` at 0 (G4).
- `messages.no-chats.sms.db` — the `chats` unit absent (G2).
- `messages.wal.sms.db` + `-wal` — a live write-ahead log (G5).

**Provenance.** Shapes are derived from `ios-backup-parser`'s own `messages.1` fixture and from
the schema in `docs/schemas/`; the `attributedBody` blobs are the parser fixture's, which are
themselves invented. **No byte of any fixture comes from a real device.**

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
| **1** | this spec | **open** |
| **2** | the domain reader + the session projection and its one scan (D2) | not open |
| **3** | `GET /api/sessions/{id}/messages/chats` (D3, contracts §1) | not open |
| **4** | `GET …/chats/{chat}/messages`, cursored (D3) | not open |
| **5** | attachments: the join to `qn.8`'s download route (D6) | not open |
| **6** | FTS5 search and its capability gate (D4) | not open |
| **7** | the surface (D9), then G6 to the Operator | not open |

**This table is a second part describing the whole, so it is stale by default after every
merge** — quince#409. Update it in the diff that changes what it describes.
