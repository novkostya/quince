# qn.6r — pairing records belong to the muxer

**Goal.** quince stops behaving as the custodian of pairing records it does not own, and stops
telling anyone a device is paired when the muxer did not record the pairing.

Tracker: [quince#1309](https://github.com/novkostya/quince/issues/1309). This rung implements
rulings A, B and C of the architect's 2026-08-20 ruling on that issue, and settles the one question
ruling D left to a spec.

---

## Status

- **Rulings A, B, C are in force**; D is discharged by measurement. Rulings 3 and 4 of the original
  issue shipped as [quince#1310](https://github.com/novkostya/quince/pull/1310) and
  [quince#1311](https://github.com/novkostya/quince/pull/1311) and are not re-opened here.
- **The rung letter is not allocated.** `qn.6r` is the next free one and is used so the work can
  proceed; whether this is a rung of its own and whether it is inside v0.1 is the Operator's.
  Open question: [quince#1309](https://github.com/novkostya/quince/issues/1309).
- **One design decision below asks for a ruling rather than assuming one** — D3, which loses half
  of what qn.6p D7 was ruled to deliver. It is marked in place, not buried here.
- **That question is WITH THE OPERATOR.** The architect stopped the thread on
  [quince#1309](https://github.com/novkostya/quince/issues/1309) rather than ruling it under ruling
  C, because qn.6p D7 is an Operator ruling and retiring half of it is user-visible; the issue
  carries `needs-operator`. **Nothing else here waits on it** — D3's *mechanism* is graded on being
  correct, not on whether a post-check is the right shape.

---

## Boundary

**In scope**

- `core/internal/deviceops/lockdown.go` and its tests — the whole file goes.
- `core/internal/deviceops/manager.go` — the pair precondition and the pair outcome.
- `core/internal/deviceops/deviceops.go` — the package comment still says the pairing record is
  *"persisted 0600 under `$QUINCE_DATA`"*, which D1 and D2 both deny.
- `core/internal/muxd/` — one request/response exchange beside the existing Listen stream.
- `core/internal/httpapi/` — the `pairing` object on `GET /api/devices`.
- `ui/src/features/devices/PairDialog.tsx` — the disabled-Pair branch.
- `deploy/compose.yml`, `deploy/compose.host-muxer.yml` — where the muxer's store lives.
- `docs/quince.stack.md` (D2's netmuxd bullet), `docs/contracts.md` (the devices payload and the
  pair route).

**Out of scope, explicitly**

- **The `SystemBUID` regeneration finding** — [quince#1314](https://github.com/novkostya/quince/issues/1314).
  It is a property of the same directory and a different defect; keeping it out means a ruling here
  implies nothing about it.
- **Whether sharing the store between two muxers is safe.** qn.6p D7 declined to rule it and nothing
  here changes the question.
- **Wi-Fi matching behaviour.** This rung moves who writes the records, not how the muxer uses them.
- **`qn.6p` G8.** The hardware gate stays owed to that rung; this one adds to what it would prove
  and does not discharge it.

---

## What was measured, and where

Every interface fact below was read from source fetched at the pinned ref on 2026-08-20, not from
memory. The refs are named so a reviewer can re-fetch them.

**netmuxd `ac8da97`** — the commit the compose comment attributes the pinned image digest to.

| | |
| --- | --- |
| **M1** | The request `match` in `src/main.rs` has six arms — `ListDevices`, `Listen`, `ReadPairRecord`, `SavePairRecord`, `ReadBuid`, `Connect` — and **no wildcard**. A foreign enum that was non-exhaustive would not compile without one, so those are all the variants there are. **`DeletePairRecord` is not among them.** It decodes as `UnknownMessageType`, and with no upstream configured the arm is `warn!(…); continue` — **no reply is written**. `pairing_file.rs:217` has `remove_pairing_record`; nothing calls it. |
| **M2** | `SavePairRecord` writes `<plist_storage>/<udid>.plist` **unconditionally**, and answers `Result(0)` on success, `Result(1)` on a write error. |
| **M3** | `ReadPairRecord` for a record that is **not there** does `return` — it closes the connection **with no reply at all**. Absence is an EOF, not an error code. usbmuxd answers the same question with a `Result` carrying a non-zero number, so a client must accept both. |
| **M4** | `pairing_file.rs:108` `update_cache` walks **every** file in the store, parses it, and adds it to `paired_udids`. A stray file that parses becomes a phantom paired device. |
| **M5** | `ReadBuid` → `get_host_identity` writes only when a field is missing, and that write is best-effort — a failure is a `warn!` and the identity is returned anyway. |

**libimobiledevice `1.4.0`** — `versions.env:43`.

| | |
| --- | --- |
| **M6** | `common/userpref.c`: `userpref_read/save/delete_pair_record` are each a `usbmuxd_*` message. **There is no filesystem fallback.** |
| **M7** | `src/lockdown.c:1024` calls `userpref_save_pair_record(…)` and **discards the return value**. `ret` stays `LOCKDOWN_E_SUCCESS`, so `tools/idevicepair.c:442` prints `SUCCESS: Paired with device <udid>` and exits `0` **even when the muxer refused to save**. |
| **M8** | `common/userpref.c:230` `userpref_get_paired_udids` **does** read the config directory from the filesystem — the one pair-record-adjacent call that is not a muxer message. It backs `idevicepair list`, which quince does not use. Recorded so a reviewer meeting it does not read it as contradicting M6. |

**Not established, and it stays not established through this rung:** no frame has been sent to a
running netmuxd, and nobody has verified the pinned image digest was built from `ac8da97`. Both are
`qn.6p` G8's to close.

---

## Design

### D1 — the store is the muxer's, and quince's container does not touch it

Ruling A, restated because everything else rests on it. `/var/lib/lockdown` is the **daemon's**
store; M6 is why. Before qn.6p quince supervised the daemon, so the daemon's store was quince's, and
the distinction cost nothing. The split moved the store and the model did not follow.

The empirical settlement is the macOS socket-proxy stand: both lockdown directories empty, device
works. No theory in which quince's directories matter can explain that.

**Consequence:** quince mounts no pairing-record path, reads none and writes none. That is already
true of both shipped examples since quince#1310; this rung makes it true of the code as well.

### D2 — `LockdownStore` retires

Ruling B. `Restore`-at-startup and `Backup`-after-pair implement *quince as custodian of pairing
records*, which D1 says quince is not. Repointing them at another directory would preserve the role
the model deletes, so the file goes rather than moves.

**`syncPlists` and `copyFile` go with it**, including the `os.SameFile` guard quince#1310 added.
That guard was correct and is not being reverted: it defends a same-file copy, and after this rung
there is no copy. Deleting a guard whose hazard no longer exists is not the same act as deciding the
hazard was acceptable, and the regression tests go with the code they cover.

### D3 — the check that cannot lie is a POST-check, and the pre-check cannot be saved

**This is the decision that asks for a ruling.** qn.6p D7 was an Operator ruling with a stated
purpose: *"a pairing that cannot be recorded is not a pairing"* — refuse **before** somebody walks
to the phone and taps Trust. Half of that is no longer reachable, and the spec says so rather than
shipping something that looks like it.

**Why no pre-check works.** The only message that answers *can the muxer record a pairing* is
`SavePairRecord`, and M2 says it overwrites unconditionally:

- against a **real** UDID, the probe destroys that device's record;
- against a **sentinel** UDID, it leaves a file that M1 says nothing can remove over the protocol,
  and M4 says the muxer will then cache as a phantom paired device. That is the same class as the
  `SystemConfiguration.plist` shadowing both compose files already warn about.

`ReadBuid` is not an alternative (M5): it writes only when something is missing, and a failed write
is not reported. `ReadPairRecord` is read-only and answers a different question.

**So `Writable()` is deleted and nothing takes its position.** It probes a directory quince no
longer owns; there is no directory to repoint it to.
**What replaces it is a BEFORE-AND-AFTER comparison against the muxer's store.** Presence alone is
not enough, and the reason is the case the whole rung exists for.

**Presence answers *is there a record*; the rung needs *was one just written*.** Those come apart on
any stand that has been running a while: a device whose record is stale — the phone was reset, or
trust was revoked — still has a **file** in the store. `contracts.md:1109` is explicit that
`paired` means *"a pairing is CONFIRMED valid right now"*, which is a lockdown validation and not
record presence, so quince offers Pair for exactly that device. If the store is then unwritable,
M7 makes `idevicepair` print `SUCCESS` anyway, and a presence check finds the **stale** record and
reports the pairing recorded. That is story 2 failing by the mechanism story 2 is written against,
in the more likely of the two cases rather than the rarer one.

So quince asks **twice**:

1. **Before** running `idevicepair`, `ReadPairRecord(udid)` — and keeps a **hash** of the reply body,
   or the fact that there was no record.
2. **After** `SUCCESS: Paired`, the same request again.

| before → after | verdict |
| --- | --- |
| absent → present | recorded |
| present → **different** hash | recorded (a fresh pair mints new keys and a new `HostID`, so the bytes cannot be the old ones) |
| present → **same** hash | **not recorded** — this is the stale-record case, and the one presence alone gets wrong |
| absent → absent | not recorded |

**A hash, not the bytes, and that is D4's constraint doing work rather than being recited.** The
pre-read body is reduced immediately and dropped, so quince holds 32 bytes across the pair instead
of a private-key-grade record for the length of a user's walk to the phone.

**Two residuals, named rather than left to be found.** A save that reproduced the previous record
byte-for-byte would read as *not recorded* — unreachable in practice, since `lockdown.c:867`
generates a fresh `HostID` per pair and the certificates are new, and the error is in the
conservative direction (quince under-claims). And another writer changing the record between the
two reads would read as *recorded*; there is no lock over the muxer's store and this rung does not
invent one.

Absent-after, or unchanged-after, means the op **fails** with an actionable message naming the
muxer and its store. quince does not claim a pairing that does not exist.

That is the same shape as the rest of the product: *a backup is `succeeded` only after
verify+commit*. And it is the only thing that catches M7, which is a live **state honesty**
violation this rung inherits rather than introduces — `idevicepair` reports success when the save
failed, so quince does too.

**What is lost, stated plainly:** the user can still walk to the phone and tap Trust for a pairing
that will not be recorded. quince finds out immediately afterwards and says so, but it cannot spend
that walk on the user's behalf any more. **If the Operator reads D7 as requiring the refusal rather
than the truthfulness, this rung stops here and the answer is a different one.**

**One case is still refusable before the walk, and it is kept:** if the muxer is unreachable, the
pairing certainly cannot happen. That answer already exists in the muxer health state (qn.6p D5) and
costs no new probe. It is a narrower guarantee than D7's and is not offered as a substitute for it.

### D4 — the probe reduces the record to a hash and keeps nothing else

`ReadPairRecord` returns the record itself, which is private-key-grade (design §6): the host identity
and a device record together let the holder talk to the phone as a trusted host. There is no lighter
message in the protocol, so this is a constraint rather than a choice.

**The record BODY is never logged, served, persisted or parsed.** quince reduces it to a hash for
D3's comparison and drops it; the length is not reported either, since a size is a fact about a
secret.

**"The body", not "the message" — D5 needs the envelope read.** Telling a `PairRecord` reply from a
`Result` reply means reading the message type, which is a parse of the framing. The security
property is about the record's contents, and it is what this section binds.

### D5 — three answers, one of which is a closed connection

M3 makes this a real design point rather than error handling. Asking the muxer for a record has
**three meanings and four shapes on the wire**, and quince must not collapse the meanings:

| what happens | what it means |
| --- | --- |
| a `PairRecord` reply | the record is there |
| a `Result` with a non-zero number | the record is not there — this is usbmuxd's answer |
| a clean EOF with no reply, **and a re-dial succeeds** | the record is not there — this is netmuxd's answer (M3) |
| the dial fails, the re-dial fails, or the deadline expires | quince does not know, and says that |

The third is not the second. *"The pairing was not recorded"* and *"quince could not reach the
muxer to ask"* are different sentences with different remedies, and *Troubleshooting is ACTIONABLE*
is what forbids merging them.

**THE RE-DIAL IS WHAT MAKES THE EOF ARM HONEST.** M3 says netmuxd answers *no record* by closing
with no reply — but a muxer that **dies** mid-request closes exactly the same way, and so does one
restarted underneath the exchange. Read naively, an EOF is *the record is not there* wearing
*quince cannot tell*'s clothes, which is the collapse the paragraph above forbids. So on EOF quince
dials again: reachable means the muxer is alive and its silence was its answer; unreachable means
the muxer went away and quince does not know. One extra dial, on the failure path only.

The exchange is a **short-lived connection of its own**, dialled from the existing endpoint and
bounded by a deadline. It is not multiplexed onto the Listen stream, which is a long-lived
subscription whose framing has no request/response discipline.

### D6 — the muxer's store becomes the muxer's own directory

Ruling D's leftover, which the architect left to this spec with the note that the measurement does
not force it.

**Decision: it moves out of `./quince/data/`.** Three reasons, in order of weight:

1. **The writer owns the volume.** After D1 the muxer is the only process that reads or writes those
   files. A directory inside quince's data volume that quince never touches is a name that says the
   opposite of what is true — and the architect already flagged that *"the name will lie before the
   spec lands"*.
2. **`./quince/data` is documented as *"config, database, quince's own state — keep this"*.** A
   third party writing into it contradicts the line the user is told to trust when deciding what to
   back up or delete.
3. **It removes the aliasing hazard by construction**, rather than by remembering not to re-add the
   mount. quince#1310 fixed the instance; a separate directory removes the shape.

**What it costs, and it is user-visible: an existing install must move its records or re-Trust every
device.** One `mv` before `compose up`, documented in the examples and in the release notes. quince
is pre-release, which is what makes this affordable, and it will not be later.

**The alternative was to keep the path and correct only the comment.** Rejected: it leaves a
directory whose name asserts an ownership the model denies, and the next reader has to re-derive
this whole issue to know that.

**IT AFFECTS ONE EXAMPLE.** `compose.host-muxer.yml` mounts no pairing-record store at all: its
muxer is the **host's** usbmuxd, whose store is the host's own `/var/lib/lockdown` and is not a
compose volume. So D6 moves `compose.yml`'s sidecar store, and the other file already has the shape
D6 argues for.

### D7 — the wire loses a field rather than gaining a lie

`GET /api/devices` carries `pairing: {writable, reason?}` (`contracts.md:1085`), described there as
*"a SYSTEM capability"* and *"a HINT, NOT THE GUARD"*.

**It is removed.** The state it describes — *quince mounts the records read-only, so it cannot pair*
— is a configuration this rung deletes. A field that can no longer answer its own question is worse
than no field: the UI renders a confident reason that is about the wrong filesystem.

**Considered and rejected: keep the field, repointed at muxer reachability.** It would preserve the
shape at the cost of the meaning — `writable` would stop meaning writable, and the next reader would
have to find this paragraph to know. D5's reachability refusal lives on the pair route, where the
`409` already is, rather than in a field whose name would have to lie to survive.

`POST /api/devices/{udid}/pair` keeps its USB `409`, gains a muxer-unreachable `409`, and loses the
read-only-directory `409`. The `202` op is what carries the record-not-saved outcome, because that
is only knowable after the pair has run.

---

## Stories

1. **quince's container touches no pairing-record path.** No mount, no read, no write, and no code
   that would use one. (D1, D2)
2. **A pairing the muxer did not record is not reported as succeeded** — including when a **stale**
   record for that device is already in the store, which is the case presence alone gets wrong.
   The op fails. (D3, M7)
3. **The failure says what to fix.** It names the muxer, its store, and the remedy — and does not
   say the same thing when quince could not reach the muxer to ask. (D3, D5)
4. **A record that CHANGED confirms silently.** A successful pair is not made slower or noisier by
   the two reads, and no record's content appears in a log, a response or a file. (D4)
5. **Pairing is refused before the walk when the muxer is unreachable**, and only then. (D3, D5)
6. **The muxer's store is its own directory**, and quince mounts no store in either example.
   **Only `compose.yml` has a store to move** — `compose.host-muxer.yml` mounts none at all,
   because its muxer is the host's usbmuxd and the host's `/var/lib/lockdown` is already its own. (D6)
7. **The wire and the screen carry no precondition that no longer exists.** (D7)
8. **Canon follows in the same diffs** — stack D2's netmuxd bullet, the contracts payload, the UI
   copy.

---

## Gates

`make gates` throughout, plus:

| # | story | gate |
| --- | --- | --- |
| G1 | 1, 2 | `grep` proves no source file outside tests names `/var/lib/lockdown` or `LockdownStore`; the deleted file's tests are gone with it, and `go build` proves no caller survives |
| G2 | 2 | **Two fakes** — `idevicepair` faked to print `SUCCESS: Paired` and exit `0` (M7's shape, which is tool stdout), and the muxer socket scripted to **close with no reply** (M3's shape, which is the usbmuxd framing). Two components, two jobs; one fake cannot do both. The op must end `failed` |
| G2b | 2 | **the stale-record case, which presence alone gets wrong** — the muxer serves the SAME record body before and after, the tool reports success, and the op ends `failed` |
| G2c | 2 | the negative control — the muxer serves a **different** body after, and the op ends `succeeded`. A `failed` that is right for the wrong reason passes G2 and G2b alone |
| G3 | 3 | table test over D5's three meanings; each produces a **distinct** message, and the unreachable one is not the not-recorded one. **The EOF arm is exercised both ways** — EOF with the re-dial succeeding is *not recorded*, EOF with the re-dial failing is *quince cannot tell*. The assertion is on distinctness, not on wording — the rule's negative half is that a true message which collapses two causes is still a defect |
| G4 | 4 | the fake muxer serves a record whose bytes are a known sentinel; the test asserts that sentinel appears in **no** log line, response body or file written during the op |
| G5 | 5 | with the muxer endpoint unreachable, `POST …/pair` returns `409` naming the muxer and no `idevicepair` process is started |
| G6 | 6 | both compose files parse; quince's service mounts no lockdown path in either; `compose.yml`'s muxer store is outside `./quince/data`, and `compose.host-muxer.yml` mounts no store — asserted rather than skipped, so "no store" cannot pass by absence of a check |
| G7 | 7 | `make gates-ui-e2e`; the devices payload carries no `pairing` object and the Pair control renders without a disabled branch |
| G8 | 2, 3 | **OWED TO HARDWARE, owner the Operator.** A real device paired through the shipped stack, once with the store writable and once with it not, showing `succeeded` and the actionable failure respectively. This is `qn.6p` G8's territory and also closes the two caveats above: the live round trip, and the image digest's provenance |

**G8 is declared owed up front rather than discovered at the end.** Nothing below hardware can prove
a phone trusted anything, and the whole rung is source-read plus mount measurements until it runs.

---

## Fixtures

- **A fake muxer socket** in `core/internal/muxd/testdata` — a unix listener that speaks the framing
  in `protocol.go` and can be scripted to: reply `PairRecord` with a sentinel body; reply
  `PairRecord` with **the same** body on both reads (the stale-record case) or a **different** one
  (the recorded case); reply `Result` with a non-zero number; **accept the request and close without
  replying** (M3), with the re-dial then either accepted or refused; accept and never reply until
  the deadline; refuse the dial.
- **No device transcript is added.** The pair path is `idevicepair` plus a muxer exchange, and the
  existing `deviceops` fakes already cover the tool half. The replay-fixture rule
  (*every bug found on hardware becomes a replay fixture*) has nothing to bind to yet: no bug in
  this rung was found on hardware. If G8 finds one, it becomes a fixture before it is fixed.

---

## Rule check

| rule | how this rung complies |
| --- | --- |
| **State honesty** | The rung's whole point. M7 is a live violation — quince reports `succeeded` for a pairing the muxer refused to record — and story 2 is what makes the claim true. Everything in this spec that is source-read rather than run is labelled, including the two caveats G8 owns. |
| **Troubleshooting is ACTIONABLE** | D5 is written against the rule's negative half: three causes must not arrive as one sentence. G3 grades distinctness rather than wording, because a true message that collapses causes is the defect the rule names. The message carries the muxer, its store path and the remedy. |
| **No silent caps or fallbacks** | Removing the pre-check removes a guard, and the rung does not let that go quiet: the post-check reports the failure the pre-check used to predict, and D3 states in the spec what is no longer prevented. The one remaining pre-refusal (unreachable muxer) is narrower than D7's and is not offered as a substitute. |
| **Secrets discipline** | The sharpest near-miss here, and D4 exists for it. `ReadPairRecord` puts private-key-grade material in quince's memory, **twice per pair** under D3's before-and-after. The body is reduced to a hash immediately and dropped, so what survives across the user's walk to the phone is 32 bytes rather than a record; it is never logged, served, persisted or parsed, and its length is not reported. G4 is the gate, using a sentinel body so the assertion is mechanical rather than a reviewer's reading. |
| **Never mutate a committed version** | Not touched — this rung has no storage path. Named because *pairing records are immutable once written* and *versions are immutable once committed* use the same language, and a reader could think the storage invariants are in play. They are not. |
| **Config tidiness** | Not touched. No new key, no new default. The muxer's store is a compose mount, not quince configuration — which is the point of D1. |
| **Subprocesses** | Unchanged. `idevicepair` still runs as an argv array in its own process group with a Go-side deadline; this rung adds a socket exchange around it, not a process. |
| **Every bug found on hardware becomes a replay fixture** | Nothing in this rung was found on hardware — it came from source and mounts. Stated in *Fixtures* so the empty list is a declaration and not an omission. If G8 finds a bug, the fixture comes first. |
| **Docs are part of the diff** | `docs/quince.stack.md`'s D2 pair-record bullet and `contracts.md`'s devices payload both describe the model this rung changes, so each rides the PR that changes it — the bullet with `LockdownStore`'s retirement, the payload with the wire slice. Coverage summary plus a known-untested list on each PR. |
| **Privacy is a commit-time gate** | `make privacy-check REF=origin/main...HEAD TEXT=<file under the runner's own scratch>` before every push. No host, address, path, UDID or serial from any stand appears in this spec; the upstream refs, message names and file paths are public facts about public projects. |
| **Interface facts are looked up live** | Every claim in *What was measured* was fetched at the pinned ref on 2026-08-20. Two upstream projects were read; neither was recalled. The refs are named so the reviewer can repeat it rather than trust it. |
| **Don't improvise architecture** | A, B, C are ruled and D is discharged; this spec implements them. The three things canon does **not** settle are marked as decisions here rather than made in code — D3 (which asks for a ruling because it loses half of an Operator ruling), D6 (which ruling D left to the spec), D7 (the wire shape). The adjacent finding that would have been an improvisation is filed instead: quince#1314. |
| **Approver ≠ author** | The spec is PR 1 and is reviewed before any code exists, per the program doc. `quince.stack.md` is `CODEOWNERS`-owned, so the PR carrying its correction needs the Operator; that is named in the slicing rather than discovered at merge time. |

---

## Slicing

Sequenced, each branched from `main` — not stacked.

| PR | claim |
| --- | --- |
| **1** | this spec |
| **2** | `LockdownStore` retires (D1, D2), carrying the `quince.stack.md` correction — **needs the Operator as code owner** |
| **3** | the muxer answers the pair-record question (D3, D4, D5) |
| **4** | the wire and the screen follow (D7), and the store moves (D6) |
