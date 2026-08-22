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

Measurements are of two kinds and they are **not** interchangeable: sizes read off the
Operator's real backups on the staging stand, and timings taken against a **synthetic**
database in the pinned `golang:1.26.5-alpine3.24`. Every figure says which it is.

### 1. `sms.db` is large, and one real device is very large

Read on the stand across three device trees, by the format's public file id for
`HomeDomain-Library/SMS/sms.db` (no decryption, no password, nothing left the box):

| device | `sms.db` on disk | `-wal` | `-shm` | `-journal` |
| --- | --- | --- | --- | --- |
| A | 479,232 B | absent | absent | absent |
| B | **468,078,608 B — 446 MiB** | absent | absent | absent |
| C | absent | absent | absent | absent |

**These are encrypted sizes.** Plaintext will be close but is not measured; nothing below
depends on the difference.

**Three consequences, and they are this rung.** `Materialize` must decrypt and copy 446 MiB
into session scratch before the first chat lists. Device C proves *no messages in this backup*
is a real case, not a hypothetical for the capability report. And **no real backup here carries a
sidecar** — `parserfs`'s sidecar handling is correct and, on this evidence, exercised by nothing,
which is a fixture obligation (D8), not a reason to remove it.

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

### 4. Extrapolating to device B — a range, honestly

The synthetic rows are ~254 B each. Real `message` rows carry ~100 columns and larger blobs,
so real bytes-per-row is higher and the real row count correspondingly lower. At 300–900 B/row,
446 MiB is **roughly 0.5M–1.6M messages**, and one full scan at 25.4 µs/row is **roughly
13–40 s**.

**The exact figure needs the real row count, which needs the backup password**, and is
therefore owed to the Operator (Gate G6). **The range is enough to decide the design**: at
either end, a full scan per page is not a user interface.

### 5. The parser trusts `message.cache_has_attachments`

`enrich` calls `fillAttachments` only when `row.cacheHasAttachments != 0`. Measured with a
control: a database with five valid, non-dangling `message_attachment_join` rows and the cache
column at its default `0` yields **1000 messages, 0 with attachments** — the join is never read.

**This is correct for a healthy database and is a silent-drop risk for a stale one**, which
*no silent caps or fallbacks* forbids. D5 says what quince does about it.

### 6. `backup.Capability` has no notion of search

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

### D2 — ONE scan at unlock builds a session projection. Every UI read hits the projection

Fact 2 says the parser cannot page a thread; fact 3 says a scan costs ~25 µs/row; fact 4 says
device B is 13–40 s of scan. Three routes were considered and the measurement chose between them:

- **Full scan per page.** Refused. 13–40 s per page at the far end of a thread.
- **quince opens the materialized `sms.db` with its own SQL.** Refused. It duplicates the
  schema-fingerprint and `attributedBody` work the library exists to own, and puts two
  parsers of one format in the project.
- **A per-chat cursored accessor upstream in `ios-backup-parser`.** Good, and **not
  sufficient alone**: search (D4) requires reading every message's text once regardless, so a
  full scan happens at unlock either way.

**The ruling: quince performs ONE `Messages()` scan per session, at unlock, and writes a
session-scoped projection into the session's own scratch** — a small SQLite database holding
the fields the surfaces need (message id, guid, chat ids, date, direction, handle, text,
attachment refs, association and edit markers). Chats come from `Chats()`, which is cheap
enough to read live.

**This is not a persistent index and does not become one.** It lives in the session scratch
`qn.8` already wipes on lock, TTL and shutdown; nothing version-keyed, nothing on the app DB,
nothing surviving the lock. That is the same rule the roadmap states for FTS5 —
*"session-scratch FTS5 search"* — arriving one layer earlier.

**The scan is surfaced, never hidden.** It is work the user waits for, so it reports progress
and its failure is a named state, per *no silent caps or fallbacks*. The upstream accessor
stays worth doing as a later optimisation and is **not** this rung's dependency.

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
7. A backup with no `sms.db` (device C) reports *no messages in this backup* — not an error,
   not an empty list without explanation.
8. A message whose body cannot be decoded is shown as unknown, never as empty.
9. Locking the session removes the projection, the FTS index and every materialized file.
10. The unlock scan reports progress and, if it fails, says what failed and what to do.

## Gates

Beyond `make gates` and `make gates-ui-e2e`:

- **G1** — fixture test: a chats list, a threaded page and a cursor round trip over the
  invented fixture; the last page is terminal.
- **G2** — fixture test: a schema missing the `chats` unit reports `unsupported_reason` and
  does not render an empty list.
- **G3** — fixture test: `BodyUndecoded` renders as unknown (story 8), asserted at the surface,
  not only in the reader.
- **G4** — fixture test: a database whose `cache_has_attachments` is stale produces the D5
  warning naming both counts. **This gate is the control for fact 5** and fails if the check is
  removed.
- **G5** — fixture test: a fixture carrying a live `-wal` opens through `parserfs` and the
  committed bytes are unchanged afterwards (hash before/after).
- **G6 — OWED TO THE OPERATOR, on hardware.** M7's rung gate: renders the real backup
  correctly, spot-checked against iMazing. Also the only way to close fact 4's range —
  the real message and chat counts, and the real `Materialize` time for 446 MiB. **Owner:
  the Operator**, on a dev-deploy build with a click-list. It is not a park: the build ships
  with the slice.
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
  removes the `search` capability and says why (D4); the unlock scan is visible work with a
  named failure (D2, story 10).
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
  were **measured with controls**, not inferred.
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
