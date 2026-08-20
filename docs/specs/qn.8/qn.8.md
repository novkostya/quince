# qn.8 — Vault: unlock a version, browse it lazily, download one file, lock

**Goal.** A person who holds the backup password can open one committed version, walk its file tree
by domain, download a single file, and lock — and nothing derived from that version's content
survives the lock.

Rung issue: **quince#270**, whose §9-1 was ruled by the Operator on 2026-08-20: **take option 3 —
`vault.Vault` as the Go interface now, with an in-process implementation behind it; sidecar-vs-in-core
deferred to a measurement.** This spec is written under that ruling. It owes two things the ruling
asked for by name: it **specifies the spike** (D10) and it **proposes the threshold before the number
exists** (D10.3), for the Operator to confirm at spec review.

**quince#184 is answered here** (D5, G1). It has been open since the conformance suite was first named
as a shipping gate, and it is the one blocker this rung inherits rather than creates. D5 records why
it could not have been authored before today, and what one upstream release costs to unblock it.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/vault/` (new) | the `vault.Vault` interface, the in-process implementation over `ios-backup-crypt`, the session registry and its TTL/lock/wipe lifecycle |
| `core/internal/vault/conformance/` (new) | the golden suite — quince#184 — driven against **any** `vault.Vault`, plus the fixture backups it runs on |
| `core/internal/httpapi/` | `POST /api/versions/{id}/unlock`, `POST /api/sessions/{id}/lock`, `GET /api/sessions/{id}/browse`, `GET /api/sessions/{id}/file/{file_id}` — the four already frozen in contracts §1 |
| `core/internal/config/` | one new section, `vault:`, carrying the session TTL (D9) |
| `ui/src/features/browse/` (new), `ui/src/pages/` | the unlock dialog, the domain/prefix file browser, single-file download |
| `docs/contracts.md` | §4 — the amendment this rung owes (D1, D8), and §2's `FileEntry` (D4) |
| `docs/quince.design.md` | §7 — the seam as built, and §6's key-handling claim reworded to what is true (D11) |
| `docs/quince.stack.md` | D4 — the open process-model paragraph replaced by the ruling and the number |

**Out of scope**, each decided out rather than missed:

- **Domain viewers.** `overview.*` and `messages.*` are qn.9/qn.10 (roadmap M7). *"Browse domains"* in
  this rung's gate means **walk the file tree filtered by domain** — `list {domain, prefix}` — not
  render a typed view of one.
- **`ios-backup-parser`.** It parses an already-decrypted backup into typed domain records, which is
  qn.9's job. It is not a dependency of this rung and is not added to `core/go.mod` here.
- **Capability discovery on the wire** (quince#270 §7(b)). The recommendation is accepted and its
  reasoning stands — enumerating a method family per domain guarantees a contract amendment per
  domain, forever. It is deferred to **qn.9's spec**, which is where the first consumer exists: a
  wire shape designed with no consumer is designed on speculation, and `Capability` already carries
  JSON tags, so nothing is foreclosed by waiting.
- **Search / FTS.** Design §7 puts session-scratch FTS5 under the messages domain; it arrives with it.
- **Derived caches.** Contracts §5 stays dormant — this rung derives nothing persistent.
- **Enforcing the scratch jail** (`chroot`/`unshare`/`bwrap`/credential-dropping). D11 says what the
  rung gate may claim instead, and why enforcement is not free work this rung can absorb.
- **Restore.** Reading a version is not writing one back to a device.

---

## Interface facts — measured 2026-08-20 in this checkout at `f2e7d30`, not recalled

Canon binds interface facts to a live lookup. Each fact says how it was established.

**1. `ios-backup-crypt` is at `v0.1.1`; `ios-backup-parser` at `v0.1.0`.** Unchanged since quince#270
was filed. `GET /repos/novkostya/<repo>/tags`.

**2. The zero-new-third-party-surface claim still holds, and one indirect version differs.** Both
libraries declare `go 1.25.0`, matching `core/go.mod`. Direct deps: crypt needs `howett.net/plist
v1.0.1` + `modernc.org/sqlite v1.54.0`, parser needs `modernc.org/sqlite v1.54.0` — **both already in
`core/go.mod` at exactly those versions.** Their indirect `golang.org/x/sys` is `v0.46.0` where core
is at `v0.47.0`; minimal version selection keeps core's, so this adds nothing. Read from each
repository's `go.mod`.

**3. The decryption library's API maps onto contracts §4 almost method for method** — `Open`,
`Unlock`, `List`, `Stat`, `DecryptFile`, `DeviceInfo`, `Close` — and `DecryptFile(fileID string, w
io.Writer)` **streams**. That signature is what D1 turns on. Read from `backup.go` at `main`.

**4. `List` returns `iter.Seq[FileEntry]` ordered by domain then relativePath — a lazy iterator with
no cursor.** Contracts §1 promises `{entries, next_cursor}`. D3 bridges them. Same source.

**5. The library's `FileEntry` carries `FileID`, `Domain`, `RelativePath`, `Flags` — and NOT `size`
or `mtime`.** Contracts §2's `FileEntry` promises both. D4. Same source.

**6. The fixture generator is `internal/`.** `internal/builder/builder.go`, whose own doc comment says
it *"never ships in the library's public API (hence internal/)"* and the README calls it *"a
**test-only** builder"*. Go's internal rule makes it unimportable from quince. D5. Read from the
repository tree and that file.

**7. The library decrypts `Manifest.db` into `os.CreateTemp("", …)`** — `$TMPDIR`, not a caller-chosen
directory — and removes it in `Close()`. D6. Read from `decryptManifestDB` in `backup.go`.

**8. The library refuses an unencrypted backup** with `ErrNotEncrypted`, and quince has a permitted
class of them: contracts §2 has `Version.encrypted` with *"unencrypted versions are permanently badged
incomplete"*, and `backup.require_encryption` — **default `true`**, which fails preflight actionably —
*"permits unencrypted backups behind persistent UI"* when set false. So the class is off by default
and modelled, not hypothetical. D7. Read from `backup.go` and `docs/contracts.md`.

**9. The library exports five errors**, of which one reports a **partial success**:
`ErrIncompleteFile` is raised *after every recovered byte has already been written*, and its own doc
comment says callers *"typically keep the partial and flag it"*. Contracts §4's frozen taxonomy has
no code for it, nor for `ErrNotAFile`. D8. Same source.

**10. `vault/` no longer exists in this repository — zero tracked files.** quince#268 is `CLOSED` and
the Python stub, `gates-vault`, `python3` and `uv` are gone from the tree, the Makefile and the
Dockerfiles (`grep` over each returns nothing). **quince#270 §5 describes a tree that has since
changed**; nothing in this rung removes Python, because there is none left to remove.

**11. `core/internal/vault/` does not exist.** Greenfield, as quince#270 §6 says. `ls`.

**12. `sessions:` in `config.yml` is the LOGIN-session namespace** (`allow_insecure_transport`,
qn.6f). The vault session is a different object with the same word. D9 keeps them apart. Read from
contracts §6.

---

## Design

### D1 — The seam is a Go interface shaped for STREAMING, and `materialize` is a wire detail of one implementation

This is the decision the rest of the rung hangs on, and option 3 is what makes it takeable.

Contracts §4 specifies `materialize {file_id} → {handle, rel_path, size}`: the vault decrypts into
scratch, the core resolves the path, streams it, unlinks. **That shape exists only because a process
boundary cannot carry an open file.** quince#270 §6 names the cost — every file read becomes
decrypt → scratch → read → unlink, double I/O on the slowest disks quince targets.

If `materialize` is put on the **Go interface**, an in-process implementation pays that cost for a
boundary it does not have. So it is not on the interface:

```go
// Vault reads one committed version. Keys live only between Unlock and Close.
type Vault interface {
    Unlock(ctx context.Context, password string) (Info, error)
    List(ctx context.Context, q Query) (Page, error)
    Stat(ctx context.Context, fileID string) (FileEntry, error)
    Open(ctx context.Context, fileID string) (io.ReadCloser, error)  // NOT materialize
    VerifyCanary(ctx context.Context) error
    Close() error                                                     // == lock
}
```

- **In-process** `Open` returns a reader that decrypts as the caller reads — one pass, no scratch
  file, no unlink. `DecryptFile(fileID, w)` (fact 3) is a `Writer` sink, so the implementation runs it
  into an `io.Pipe` and hands back the read half; back-pressure is the pipe's.
- **An RPC implementation** `Open` calls `materialize` internally and returns a reader over the
  scratch file **whose `Close` unlinks it**. The double I/O is paid by the implementation that needs
  it, and by nothing else.

**The seam is the interface, not the process** — which is what `contracts.md` §4 already says in as
many words. **Contracts §4 is amended to say so explicitly**: `materialize`, `handle` and `rel_path`
are the RPC's wire shape for `Open`, not part of the seam, and an implementation is conformant if it
answers the interface. That amendment is this rung's, per quince#270 §7 — a diff, not a
recommendation.

**What this costs, stated rather than discovered later.** The conformance suite (D5) then tests the
interface rather than the wire, so a future RPC implementation gets its *framing* covered by nothing
here. G1 records that as declared, not as covered.

### D2 — Method by method: contract, interface, library

| contracts §4 | `vault.Vault` | `ios-backup-crypt` | note |
| --- | --- | --- | --- |
| `initialize {password, backup_path}` | `Unlock(password)` — the backup path is constructor state | `Open(dir)` + `Unlock(password)` | `Open` needs no password (fact 3), so a wrong-password unlock never re-reads the tree |
| → `{protocol_version, device_name, ios_version, file_count, manifest_sha256}` | `Info` | `DeviceInfo()` gives the first three | `manifest_sha256` is quince's: SHA-256 over the **on-disk (encrypted) `Manifest.db`**, so it is computable before unlock and identifies the version rather than the decryption |
| `list {domain?, prefix?, cursor?, limit}` | `List(Query) (Page, error)` | `List(domain, prefix) iter.Seq` | D3 |
| `stat {file_id}` | `Stat` | `Stat` | direct |
| `materialize {file_id}` | `Open(file_id) io.ReadCloser` | `DecryptFile(fileID, w)` | D1 |
| `verify_canary {}` | `VerifyCanary` | `DecryptFile` of a chosen entry | D2.1 |
| `lock {}` | `Close()` | `Close()` | D9 |

**D2.1 — the canary is chosen, not fixed.** Design §4 makes content verification *"decrypt a canary
file"*, feeding `content_verified_at`. No single relative path exists in every backup, so a hardcoded
one is a silent failure on the backup that lacks it. **The canary is the first entry the manifest
yields whose record has an encryption key and whose recorded size is under 64 KiB**, taken in the
library's stable order (fact 4) — deterministic per backup, present in any backup with one readable
file, and cheap. If no such entry exists, `VerifyCanary` **fails with a reason naming that**, rather
than passing vacuously.

### D3 — Cursor pagination over a cursorless iterator, holding no server-side state

Facts 4 and 1: the library iterates lazily in a **stable total order** — `(domain, relativePath)` —
and the contract wants `{entries, next_cursor}`.

The cursor is the **last `(domain, relativePath)` returned**, base64url of a JSON pair, and the next
page re-queries from it. No iterator is parked between requests, so:

- a session that sits idle between pages holds no goroutine, no open statement and no memory;
- a cursor survives a daemon restart within a live session as well as the session does — no better,
  no worse;
- **the order is the library's, so the cursor is only meaningful against the same backup.** A cursor
  is rejected with `not_found` if its session is gone, which the session id already decides.

**`limit` is server-clamped and the clamp is DISCLOSED**, per *no silent caps*: the response carries
the effective limit when it differs from the one asked for. Default 500, max 2000 — design §7's own
batch range, reused rather than reinvented.

### D4 — `FileEntry` is short of `size` and `mtime`, which contracts §2 already promises

Fact 5. Both values live in the NSKeyedArchiver record the library **already parses** to bound
`DecryptFile`'s output; they are simply not on the struct it returns.

Two routes were considered. Decoding the record a second time inside quince duplicates the library's
own parser against an Apple format, so that the two could disagree about a file's size — rejected.
**The library gains `Size int64` and `MTime time.Time` on `FileEntry`**; it is quince's own library
and this is one release. Named in **Slices** as an upstream dependency of the slice that needs it.

Until then `list`/`stat` **cannot answer `size`**, and *state honesty* forbids guessing. The slices
are ordered so that no shipped surface ever serves a fabricated one (see **Slices**).

### D5 — quince#184: the suite could not have been authored before today, and one `internal/` keyword still stops it

Canon names the golden conformance suite as a shipping gate in five places and it does not exist.
Two things blocked it, and only one has cleared:

1. **There was nothing to test.** The seam had no implementation and no ruled process model, so a
   *golden* suite would have been goldens of nothing. Option 3 clears this: the suite drives the
   **interface** (D1), which exists the moment the interface does.
2. **There is nothing to test it ON.** The roadmap makes the fixture generator *"come from the Go
   library's encrypt/builder side (or a documented lab-only gate if unavailable)"*, and quince#270 §1
   reads that hedge as satisfied. **It is not: the generator is `internal/` (fact 6), so quince cannot
   import it.** The hedge is live and nobody had recorded it.

**Proposed: `ios-backup-crypt` exports the builder** — the same code moved to an importable package —
in the release D4 already needs. Its doc comment's reasoning (*"it never ships in the public API"*)
is a library-hygiene default, and quince is the case it was not written against: the *encrypt* side is
exactly what makes the *decrypt* side testable by somebody else.

**The alternative is real and is worse.** Without it, the suite runs only on lab-built fixtures, so CI
covers the seam not at all — the roadmap permits that outcome and this rung should not choose it while
a one-release fix exists.

The suite itself: recorded request/response pairs against fixture backups, replayed against a
`vault.Vault`, asserting equality. It is a **table over the interface**, so the same file gates an
in-process implementation today and an RPC one whenever the number (D10) asks for one.

### D6 — The decrypted `Manifest.db` must land inside the tree quince wipes

Fact 7: the library writes it to `$TMPDIR` and removes it in `Close()`. That is plaintext user data —
the whole file index of somebody's phone — outside the session scratch root, and `Close()` covers a
clean exit but not a crash.

In-process there is no jail being violated, so this is not a broken promise; it is a **wipe-on-lock
promise quince cannot keep for a file it does not know the path of.** `Open` gains a temp-directory
option in the same release (D4, D5). quince passes the session scratch root, and then contracts §5's
*"session scratch lives in `/cache/scratch/<session_id>/` and is wiped on lock"* covers it — including
after a crash, because the next start wipes the tree rather than hoping a `defer` ran.

**Not deferred to the sidecar decision.** It is true in-process and true over RPC, and it is the one
place this rung writes decrypted content to disk at all.

### D7 — An unencrypted version is browsable, and the same implementation serves it

**Flagged for spec review: this is user-visible and decides whether a permitted class of version can
be opened at all.**

Fact 8: quince permits unencrypted versions and badges them incomplete; the decryption library refuses
them outright. So `POST /api/versions/{id}/unlock` on such a version cannot go through that library —
and there is no password to ask for either.

Three options were weighed. **Refusing to browse them** is honest and cheap, but it makes a whole
class of committed version unopenable in the rung whose entire purpose is opening versions, and the
refusal would have to say *"quince cannot read a backup it made"*. **Extending the library** to a
format it declares out of scope drags a no-crypto path through code whose value is that it is
crypto-only.

**Proposed: a second `vault.Vault` implementation, passwordless, and it is genuinely small.** An
unencrypted backup's `Manifest.db` is plain SQLite, its `Files` table is the same shape, and its blobs
sit at `<backup>/<fileID[:2]>/<fileID>` as plaintext — so `List`, `Stat` and `Open` are the same
methods with the decryption steps absent. Design §4 already branches structural verify on
`Manifest.plist`'s `IsEncrypted`; this is the same branch one layer up, and the selection reads the
same flag.

Consequences, stated:

- **`unlock` on an unencrypted version ignores the password and says so** rather than accepting any
  string as if it had been checked — a password field that silently validates nothing is the
  collapsed-diagnostic shape the troubleshooting rule forbids. The UI does not offer the field.
- **The conformance suite covers both implementations** (G1), which is the honest reading of *browse a
  version*.
- `content_verified_at` is still meaningful: the canary read proves the blobs resolve, which is the
  claim, and design §4 already defers exactly that sampling from the passwordless verify.

### D8 — The error taxonomy is short by two codes and misclassifies a partial success

Contracts §4 freezes `data.code ∈ {bad_password, corrupt_manifest, io, not_found, unsupported_ios}`.
Against fact 9, mapping the library's five errors:

| library | code | why |
| --- | --- | --- |
| a wrong password (unwrap fails) | `bad_password` | as frozen |
| `ErrFileNotFound` | `not_found` | as frozen |
| `ErrNotAFile` | **`not_a_file`** — NEW | the entry **exists**; it is a directory or symlink with no content. Answering `not_found` for something the browse listing just showed collapses two causes with different remedies, which the troubleshooting rule names as a defect even when every word is true |
| `ErrLocked` | **`locked`** — NEW | a method called before `Unlock`. In-process it is a bug; over RPC it is a real ordering condition on the wire, and a seam that cannot express it makes the RPC implementation lie |
| `ErrIncompleteFile` | **not an error** — D8.1 | the bytes were already written |
| `ErrNotEncrypted` | **unreachable** under D7 | it becomes an implementation-selection fact, not a failure |

**`unsupported_ios` is left untouched and unused by this rung.** quince#270 §7(a) argues convincingly
that it is the wrong framing for a schema-fingerprint failure — the same iOS can ship a changed
schema. That failure belongs to `ios-backup-parser` and arrives at **qn.9**, so qn.9 owns the
correction. Redefining a frozen code here, for a consumer this rung does not add, would be designing
on speculation.

**D8.1 — a truncated file is a successful read of an incomplete artifact, and the user learns which.**
`ErrIncompleteFile` means the backup itself holds fewer bytes than the manifest records — a file that
was being written while the backup ran. Every recovered byte is already delivered. It is therefore
**not** a failure code, and it must not be silent either.

`Open`'s reader surfaces it as a terminal condition the caller can test after the copy. The HTTP
download declares `Content-Length` from `stat` (D4) and, on a short read, **fails the transfer rather
than padding it** — a client that receives fewer bytes than declared reports a broken download, which
is true. quince records the condition on the session and the browse listing marks that entry
incomplete on any subsequent view, with the sentence that makes it actionable: **the file is
incomplete in the backup, and retrying will not change that.**

### D9 — Session lifecycle: one TTL, one config key, and a name that does not collide

A session is `{id, version_id, expires_at}` (contracts §2). It is created by `unlock`, ends by `lock`,
by TTL, or by daemon exit — and in all four cases the same teardown runs: `Close()` the vault, drop
the keys, wipe `/cache/scratch/<session_id>/`, then **`debug.FreeOSMemory()`**. That last step is
D10.3's clause (c) as code rather than as a hope about the scavenger's schedule; it is a
stop-the-world cost paid at a rare event, never in a hot path, and never during a backup.

- **The config key is `vault.session_ttl`, default 15m, live-editable, no restart.** Not under
  `sessions:` — fact 12: that namespace is the **login** session's, and two different objects under
  one word in one file is a config a person cannot read. Changing it does not extend a session already
  running; the expiry is stamped at unlock, which is what makes `expires_at` a fact rather than a
  recomputation.
- **A session belongs to the daemon, not to a browser session.** It is addressed by id, and every
  route is behind `authGuard` like the rest of `/api`. Scoped per-principal access is the
  delegated-access rung's (contracts §1), and this rung adds nothing that assumes one principal
  beyond what already holds.
- **Locking is idempotent** and a lock on an expired session answers `204`, not `404`: the state the
  caller wanted is the state that exists.
- **On daemon start, `/cache/scratch/` is wiped whole.** Nothing there survives a restart by design,
  and a leftover directory is the crash case D6 exists for.

### D10 — The spike: what is measured, on what, and against which bar

The ruling makes the spike's deliverable **a number**, and warns that it must not arrive as an
implementation to bless.

**D10.1 — the harness is standalone and throwaway, and it drives the LIBRARY, not quince's vault.**
Peak RSS is held by keybag derivation, the `Manifest.db` decrypt and the file stream — all
`ios-backup-crypt`'s. A harness that drives the library directly measures the thing that holds the
memory, is written before the in-process implementation exists, and **cannot become work anybody is
reluctant to throw away.** That is what keeps the ruling free.

**D10.2 — what is measured.** Three curves, not one figure, because a single number is a claim about
whichever backup happened to be to hand:

1. **unlock** — keybag + `Manifest.db` decrypt + index open, over manifests of growing row count;
2. **full walk** — every entry listed at the D3 page size, same manifests;
3. **stream** — one file read end to end, over files of growing size.

Reported as peak RSS against input size for each, plus the same three on a real backup when hardware
is available (G4). Synthetic manifests come from the fixture generator (D5), which is what makes the
curve runnable on a session box at all.

**D10.3 — the threshold, PROPOSED here for Operator confirmation, before the number exists.**
quince#270 §6 offers *"comfortable"* and *"near the ceiling"*; neither is a bar, and an undefined bar
means the number arrives and each reader supplies their own.

> **In-process stands if ALL THREE hold — and (c) is a RECOMMENDATION with a live alternative
> (quince#1344): (a) peak RSS attributable to the vault stays under 256 MB
> across all three curves; (b) none of the three curves grows with input size beyond a flat streaming
> constant; and (c) RSS returns to within 32 MB of the pre-unlock baseline within 60 s of `lock`.**
>
> **The sidecar earns its complexity if ANY fails.**

The reasoning, so the bar can be argued with rather than merely met:

- **256 MB** is anchored on the two public figures this project already has. D1 records the daemon
  idling at ~30 MB RSS; the program doc budgets *"vault peak RSS < 2 GB on the reference backup"*.
  That 2 GB is the *does-this-work-at-all* bar for a process that exits at lock. It is the wrong bar
  for memory added **permanently to the address space of a daemon that must survive a multi-hour
  Wi-Fi transfer** on a weak NAS. 256 MB is an order of magnitude over idle and still small enough in
  absolute terms that the daemon's survival does not depend on what else the box is running.
- **Clause (b) is the load-bearing half of the two ORIGINAL clauses.** A curve that grows with
  manifest rows or file bytes makes any fixed number a statement about the test input, and an
  unbounded curve on small-RAM hardware is precisely what an `rlimit` on a separate process exists to
  bound. A flat curve says the streaming design holds; a rising one says it does not, whatever
  today's peak was.
- **Clause (c) exists because (a) and (b) together still miss the thing the sidecar was being credited
  with** (`quince-analyst`, quince#1343). Both were written about **peak**. The sidecar's advantage
  was never only a bounded peak — it is that the process **exits at lock and gives everything back**,
  where in-process the allocations land in the daemon's own heap and Go returns freed memory to the
  OS on the scavenger's schedule rather than at `lock`. As written without (c), *a daemon that peaks
  at 200 MB and settles back to ~30 MB, and one that peaks at 200 MB and stays there, both passed* —
  materially different outcomes on the box the bar is set for.
  **32 MB** is about the daemon's entire idle footprint, so the clause reads as *the vault leaves
  behind less than the daemon itself weighs*. **60 s** is deliberately generous: it is long enough
  that ordinary scavenger lag cannot fail the clause, so what it catches is retention rather than
  timing.

**D10.3a — clause (c) is a DESIGN REQUIREMENT, not a hope about the runtime, and that is what makes
it fair to impose.** Go exposes `debug.FreeOSMemory()`, which the session teardown (D9) can call at
`lock` — a stop-the-world cost paid at a rare event rather than in any hot path. So the clause is
something the in-process implementation can be *built* to satisfy, not merely something it is
measured against and might lose to the scheduler.

**Whether it is SUFFICIENT is owed to measurement, not asserted here.** Returned pages are not the
whole of RSS — heap fragmentation can hold an address space open regardless — which is exactly why
the clause is written as *within 32 MB of baseline* rather than *back to baseline*. If the mechanism
turns out not to reach the bar, that is clause (c) doing its job and the sidecar earning its
complexity, which is the outcome the threshold exists to decide rather than a defect in it.

**And (c) does NOT simply hand the decision to in-process.** It names a risk with a candidate remedy;
it does not assume the remedy works. That distinction is the whole of the paragraph above.

**D10.3b — clause (c) is measured by a SECOND harness, and D10.1's cannot do it.** A standalone
process that exits has no post-lock RSS, so the harness that produces the number for (a) and (b) is
structurally blind to (c) — the finding that raised it says so, and it is why this is settled here
rather than discovered at slice 2.

So (c) is observed **in-process, across a lock**, on the implementation slice: baseline RSS before
unlock, peak during, and RSS at `lock + 60 s`. **The ordering the ruling requires is untouched** —
(a) and (b) still come from the standalone harness **before any vault code exists**, and (c) is a bar
that code is then written to meet. What must not happen is (c) being *chosen* after its measurement,
which is why its numbers are fixed here with the other two.

**D10.3c — the alternative is LIVE, and this clause is a RECOMMENDATION, not a ruling.** Open
question: **quince#1344**, labelled `needs-operator`.

The other honest answer is to **accept retention and record why** — perhaps `lock` is rare enough, or
the daemon restarts often enough, that a high-water mark is tolerable. **That is defensible and it is
not the lesser option**; the architect filed quince#1344 declining to rule between the two, and
clause (c) has exactly the status (a) and (b) already have — proposed here, the Operator's to confirm.

The argument for recommending (c) rather than acceptance, so it can be weighed against the argument
for the other: the daemon this is added to must survive a multi-hour transfer on a weak NAS, so a
permanent high-water mark set by a browse session is **paid by the backup that runs afterwards** —
and the remedy costs one call at a rare event. **What is not defensible is neither**: an absence
decides the question by default, at slice 2, after the number has been taken.

**If the Operator takes acceptance instead, what changes is small and is named here so the swap is
mechanical:** clause (c) and G7 come out, D9 keeps or drops `debug.FreeOSMemory()` as a cheap
courtesy rather than as a bar, and the acceptance is written into this section with its reason. The
threshold stays a two-clause bar and nothing else in the rung moves.

**D10.4 — if the sidecar wins, this rung does not build it.** The interface is unchanged (D1), the
suite is unchanged (D5), and the RPC becomes a second implementation with its own rung. The number
is what makes that a scheduled decision instead of an open gap.

### D11 — `provably confined` is not true today, and the gate says what is true instead

The roadmap's gate reads: *"keys provably confined to the vault process (no password/keys in core
logs, env, argv, or disk — and nothing persisted after lock)."* quince#270 §6 deflates the first
clause: there is no `chroot`, `unshare`, `bwrap` or credential-dropping anywhere, so confinement is
what the vault is *told*, never what it is *confined to*. §8 forbids letting the gate pass on that
convention. Under option 3 the clause is worse than unenforced — **in-process there is no vault
process to confine keys to**, so it claims something that cannot be true.

**Proposed, and ruled together with the process model per the ruling — not before, and not at gate
time:**

> Keys and the password exist only between unlock and lock, and are **provably absent** from logs,
> argv, env, `config.yml`, the app DB and disk; nothing derived from them survives the lock.

Every clause of that is testable, and G3 tests it. The parenthetical the original gate already
carried is kept whole — it is the part that was always checkable. What is dropped is the word
`process`, which under option 3 names nothing.

**If the number (D10) sends the vault to a sidecar, the original wording returns and enforcement
becomes that rung's to build or to decline in writing.** Either way this rung ships a gate that claims
only what it proves — which is the point of raising it before the gate runs rather than at it.

---

## Stories

1. **Unlock.** With the correct password, a committed encrypted version opens: the UI shows device
   name, iOS version and file count, and a session with an `expires_at`. (D2, D9)
2. **A wrong password is refused as a wrong password.** `bad_password`, no session created, no
   partial state, and the version is re-openable immediately. (D8)
3. **Browse by domain and prefix.** The file tree pages through `list {domain, prefix}` in a stable
   order; every page returns a cursor that yields the next, and the last page returns none. (D3)
4. **A clamped limit says so.** Asking for more than the maximum returns the effective limit in the
   response rather than silently fewer rows. (D3)
5. **Download one file.** A single file streams decrypted to the browser with its recorded size, and
   nothing is written under the scratch root by the in-process implementation. (D1)
6. **A file that is incomplete in the backup says so.** The transfer fails against its declared length
   and the entry is marked incomplete with the sentence that a retry will not help. (D8.1)
7. **A directory is not a missing file.** `Open` on a directory or symlink answers `not_a_file`, not
   `not_found`. (D8)
8. **An unencrypted version browses without a password**, and the UI does not ask for one. (D7)
9. **Lock wipes.** After `lock`, the scratch dir for that session is gone, the keys are gone, and the
   session id answers `404` on browse. (D9)
10. **TTL locks it for you.** A session past `vault.session_ttl` is torn down by the same path as an
    explicit lock, with the same wipe. (D9)
11. **A crash does not leave plaintext.** A daemon killed mid-session leaves no decrypted
    `Manifest.db` outside `/cache/scratch/`, and the next start wipes what is inside it. (D6, D9)
12. **The canary verifies content.** A successful unlock plus canary read records
    `content_verified_at` on the version; a backup with no eligible canary entry fails with that
    reason rather than passing. (D2.1)
13. **The suite gates the seam.** The conformance suite passes against the in-process implementation
    and fails a deliberately broken one. (D5)

---

## Gates

Beyond `make gates` / `make image`:

- **G1 — the conformance suite, and what it does NOT cover.** quince#184's goldens, replayed against
  each `vault.Vault` (encrypted and unencrypted, D7). **Declared uncovered:** RPC framing, because D1
  puts the seam at the interface and no RPC implementation exists to frame. A negative control is part
  of the gate — a mutant implementation must fail it, or the suite proves nothing (an all-pass from a
  suite nobody has seen fail is an untested instrument).
- **G2 — the cursor is total and stable.** A property test over a generated manifest: paging the whole
  tree at every page size from 1 to the max yields exactly the entries a full walk yields, once each,
  in the same order.
- **G3 — no key, no password, anywhere.** In the shape design §6 already uses for the backup password:
  a test asserting the password and derived keys reach no log line, no argv, no env, no `config.yml`,
  no app-DB column and no file under the scratch root; plus a post-lock assertion that the session's
  scratch tree is gone. **This is what D11's reworded gate rests on.**
- **G4 — OWED TO HARDWARE, owner named.** *(a)* The rung gate itself — unlock a **real** version,
  browse, download, lock — which is what closes quince#270 §3's validation asymmetry, since the
  decryption library has never been run against a real device backup. *(b)* D10's three curves on a
  real backup on the smallest target box. **Owner: the Operator**, as operator-local work; **declared
  unrun** until it is run. No story above depends on it, and the spike's synthetic curves (D10.2) are
  what unblock the ruling in the meantime.
- **G5 — the temp file is inside the tree we wipe.** An assertion that no decrypted `Manifest.db`
  appears outside `/cache/scratch/<session_id>/` during a session, and that a `SIGKILL`ed daemon
  leaves nothing outside it. (D6)
- **G6 — perf budgets, one inherited and one adopted.** The program doc's *"session unlock (keybag +
  Manifest decrypt) narrated and < 30 s on the reference backup"* binds this rung directly. Its
  *"first page of any domain after its first-use load < 300 ms"* is written for a **domain viewer**
  (qn.9), so it does not bind here — this rung **adopts the same 300 ms for the first browse page**,
  which is the closest thing it ships, rather than inventing a second number or claiming a budget it
  is not under. Reported as `/usr/bin/time -v` notes where a test is not cheap.
- **G7 — memory goes back after a lock.** D10.3's clause (c), and the one gate D10.1's harness
  structurally cannot supply: baseline RSS before unlock, peak during, and RSS at `lock + 60 s`,
  observed **in-process across a lock**. It runs on the implementation slice, not on the spike, and
  the bar it asserts is fixed in D10.3 rather than read off its own result.

---

## Fixtures

- **Synthetic encrypted backups from the exported generator** (D5), built at test time rather than
  committed: a small one for the goldens, and the growing-manifest series D10.2 needs. Their
  provenance is code in a repository this project owns, so nothing comes from a device.
- **Synthetic UNENCRYPTED backups**, built the same way with the encryption steps skipped (D7).
- **Fixture password is `test`**, per the hard rule.
- **No real backup, no real manifest, no captured file record — ever.** A real `Manifest.db` is the
  complete file index of somebody's phone. G4 is run against real data **on the Operator's own
  hardware** and reports only figures and pass/fail; nothing it touches enters the repository.
- **No new `idevicebackup2` transcripts.** This rung spawns no device tooling.

---

## Rule check

Every hard rule this rung touches *or comes near*, written before building.

| rule | how this complies |
| --- | --- |
| **Privacy is a commit-time gate** | No hardware sizing, hostname, UDID or path enters this spec — D10.3's bar is anchored on the two figures **already public in canon** (D1's ~30 MB idle, the program doc's 2 GB budget), not on the Operator's box, which is named only as *the smallest target box* in a gate that reports figures and nothing else. No fixture derives from real data. `make privacy-check REF=origin/main...HEAD TEXT=<path under $HOME/scratch/r62/>` before push. |
| **State honesty** | D4 is its sharpest instance: until the library carries `Size`, `list` **cannot** answer it, so the slice order is set such that no surface ever serves a guessed one. G1 declares RPC framing uncovered; G4 is declared **unrun** with its owner named; D11 reworks a gate clause that would otherwise pass on convention. `content_verified_at` is written only after a canary actually decrypts. |
| **Interface facts looked up live** | All twelve facts above carry how and when they were established, in this checkout at `f2e7d30` and against each library's `main`. Two module versions are added and both are looked up, not recalled (fact 1); pinning anything other than the newest is not needed — the newest tag is what the rung takes. |
| **Never mutate a committed version** | **The rung's central near-miss.** The vault is a **reader** and opens `browse_root` **read-only**; it never opens a storage backend for write, creates no working copy, and takes no part in commit. On zfs `browse_root` goes through `.zfs/snapshot/…`, read-only by nature. Nothing here changes when quince#591's in-place ruling is built, because a reader of `latest/` reads the newest committed version either way. The one thing this rung writes to disk is the decrypted manifest, which D6 confines to `/cache/scratch/` — **never** into the version tree. |
| **No silent caps or fallbacks** | D3 discloses the clamped limit. D8.1 refuses to pad a truncated file and names why a retry will not help. D8 refuses to collapse `not_a_file` into `not_found`. D7 refuses to accept an ignored password as if it had been checked. G1 names what the suite does not cover instead of implying totality. |
| **Troubleshooting is ACTIONABLE** | D8's whole table is this rule: each code names a distinguishable cause with a different remedy. D2.1's canary failure names the absence rather than passing vacuously. D7's unencrypted path says what it is doing with the password field instead of removing it silently. |
| **Config tidiness (D12)** | **One key added** — `vault.session_ttl` — with a default, UI-editable, live, no restart. It goes under a **new `vault:` section** rather than the existing `sessions:` (fact 12), because two different objects under one word is a file a person cannot hand-edit. No secret enters `config.yml`: the backup password is never stored anywhere, which is what makes the whole rung passwordless-at-rest. |
| **Secrets discipline** | The password arrives in a `POST` body over the authenticated API, reaches `Unlock` in memory, and reaches **no** argv, env, log or disk (G3). It is not persisted between sessions by construction — contracts §1 already says *"the password is never persisted — unlock is per-session, always."* Under D1 there is no subprocess to feed it to; if the sidecar ever wins (D10.4), the stdin-only rule in contracts §4 governs and is unchanged. |
| **Subprocesses** | **None.** Option 3 spawns no process this rung. The rule binds again only if D10 sends the vault to a sidecar, whose rung inherits it whole. |
| **Every hardware bug becomes a replay fixture** | No device tooling is touched. G4 is where hardware meets this rung, and anything it finds becomes a fixture through the generator (D5) rather than through a captured backup — which is what makes that possible at all here. |
| **Docs are part of the diff** | Contracts §4 (D1's seam clarification, D8's codes) and §2's `FileEntry`; design §7 (the seam as built) and §6 (D11's wording); stack D4 (the ruling and the number). Each rides the slice that makes it true (see **Slices**) — not one canon PR at the end. Coverage plus a known-untested list rides every build slice. |
| **Don't improvise architecture** | The process model was **ruled** (quince#270 §9-1) and this spec builds to it rather than around it. Three decisions that touch contract surfaces — D1's seam shape, D7's unencrypted class, D8's new codes — are **proposed in the spec and reviewed before code**, which is what CLAUDE.md §8 asks of a rung's first PR; D7 is flagged in its own heading as user-visible. Nothing is deferred to *"we will amend later"*: the slice table attaches each amendment to the PR that makes it true. |
| **Approver ≠ author** | This spec is authored by an implementer session as `quince-coder`; the architect reviews and approves. The contracts and design edits are **not** code-owned (`docs/contracts.md` is deliberately unowned, Operator ruling 2026-08-14); `docs/quince.design.md` and `docs/quince.stack.md` are owned by `@novkostya`, so the slices touching them carry a code-owner round trip by design. |

---

## Slices

Each is one PR carrying one reviewable claim, **sequenced from `main`, not stacked**.

| | claim | needs the upstream release? |
| --- | --- | --- |
| **1** | **this spec** | no |
| **2** | **the spike** — the standalone harness, the three curves on synthetic manifests, the number for clauses (a) and (b), and stack D4's open paragraph replaced by it (D10). Clause (c) is **not** measurable here (D10.3b) | **yes** (D5, for the generator) |
| **3** | `vault.Vault` + the conformance suite and its negative control, against the in-process encrypted implementation — quince#184 (D1, D2, D5, G1, G2) | **yes** (D4, D5, D6) |
| **4** | the unencrypted implementation and the selection on `IsEncrypted` (D7) | yes |
| **5** | the session registry, `vault.session_ttl`, teardown and the scratch wipe, plus G3, G5 and **G7 — D10.3 clause (c), the one measurement the spike cannot take** (D6, D9, D10.3b) | no |
| **6** | the four REST endpoints, the error taxonomy and contracts §4/§2's amendment (D3, D8) | no |
| **7** | the UI — unlock dialog, browser, download, and the incomplete-file surface (D8.1) | no |
| **8** | design §7 and §6 rewritten to the seam as built, including D11's gate wording, ruled with the number from slice 2 | no |

**One upstream release gates slices 2–4**, and it is one release rather than three: `ios-backup-crypt`
needs `FileEntry.Size`/`MTime` (D4), an exported fixture generator (D5), and a caller-chosen temp dir
(D6). It is quince's own library under the same Operator ruling as this rung, so this is scheduling,
not a dependency on anybody outside the project — but it is **named as a precondition** rather than
assumed, because quince#270 §1 assumed the second of the three and it was not true.

**Slice 2 comes before any vault code exists**, which is the ruling's own condition: the measurement
must not arrive as an implementation to bless.
