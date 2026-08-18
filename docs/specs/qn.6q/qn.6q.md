# qn.6q — a hand-edit of `config.yml` applies without a restart

**Goal.** Someone edits `/data/config.yml` by hand — over SSH, in an editor, from a file manager —
and the change takes effect the way the same change made through the UI already does: no restart. A
*bad* hand-edit changes nothing, leaves quince running on last-good, and says so on screen.

Tracker: [quince#1094](https://github.com/novkostya/quince/issues/1094). This is D12's last unbuilt
half — *"edited by the UI and by hand equally"*.

---

## Status — ALLOCATED, INSIDE v0.1, and built

- **`qn.6q`, inside v0.1** — Operator ruling 2026-08-17, taken in session and recorded on
  [quince#1094](https://github.com/novkostya/quince/issues/1094): *file-watch is in v0.1, build it.*
  **Relayed by the implementer seat, not posted by the Operator** — quince-devlog#254's open question,
  and the shape quince-devlog#274 was filed about — so it is recorded on the forge and the docs PR
  carrying it asks for the Operator's own artifact.
- **It supersedes the *post-v0.1* ruling (quince#727) and the *unallocated* one (quince#1130 item 4).**
  Split out of `qn.6g` by Operator ruling 2026-08-04, option (a), on
  [quince#577](https://github.com/novkostya/quince/issues/577#issuecomment-5182609911).
- **The prerequisite is discharged.** quince#764 closed 2026-08-09.
- **quince#1130's tracker job is DISCHARGED** — all five questions are ruled, the last of them
  (question 3) on 2026-08-17, and this file records them. It should not outlive the transcription.
- **What is still owed is HARDWARE, not a ruling.** G5 and G6 are owed to the Operator; that is a
  different thing from blocked, and the distinction is worth keeping because *allocated* reads like
  *finished*.

**The letter, checked rather than counted.** `docs/specs/` holds `qn.6a … qn.6k`, `qn.6m`, `qn.6n`,
`qn.6o`, `qn.6p`. **`qn.6l` is a deliberate hole**, spoken for by quince#726 at `qn.6k.md:29` and by
quince#841, and `qn.6m` explicitly declined to take it for that reason — so the next free letter is
`qn.6q`. Confirmed by the architect on quince#1094.

**A rung with no allocation gets a topic directory, not a guessed letter** — `public-demo/`,
`dev-deploy/`, `devct/`, `rung-loop/` and `runner/` are the precedent, and naming a rung nobody has
agreed to is *"this file asserting a plan that does not exist"* (D12).

---

## Everything below was MEASURED at `ebc8219`, 2026-08-17

The issue that filed this rung says, of its own three questions: *"Nothing here is measured. The
inotify/bind-mount caveat is stated as a known class, not as an observation on this stand. No watcher
was written."* That is what this section is for. Each probe was a throwaway Go test run in the pinned
toolchain container (`make gates-go GO_TEST_ARGS=…`) and deleted; none of it is committed, because a
probe that proves a design decision once is evidence, and a probe kept forever is a test of the
kernel.

### F1. There is still no reload path, and three comments in the tree say so

`Load` runs once, in `NewService` (`core/internal/config/service.go:446`), and nothing re-reads the
file. The code already documents its own absence in three places, which is why this rung is small
rather than speculative:

- `service.go:301` — *"THERE IS NO RELOAD PATH AT ALL. `Load` runs at construction and nothing
  re-reads the file, so a hand-edit is invisible to a running quince — quince#727, post-`v0.1`."*
- `service.go:837` — `FileText` reads at request time and is never cached, *for this reason*.
- `docs/contracts.md:2819` §6 — *"Until file-watch lands, a hand-edit is picked up at the next
  start."*

### F2. The propagation seam is complete, and a watcher is a second producer into it

`Applier` / `Subscribe` / `notify` (`service.go:392`, `:398`, `:417`) are `qn.6g`'s, and its ruling predicted
exactly this: *"a file-watcher later becomes a second producer feeding the same appliers."* That
holds at this head. `Applier` takes `(old, next Config)` and **cannot refuse** — which is already the
right shape for a reload, where there is no request to fail and the file is the truth by definition.

**So this rung adds a producer. It adds no consumer, no route, and no new propagation mechanism.**

### F3. inotify needs NO new dependency — the issue's question is missing its third option

quince#1094 frames the choice as *"adding `fsnotify` versus polling `stat`"*. There is a third:

`golang.org/x/sys` is already a **direct** requirement of `core/go.mod` (v0.47.0), and
`golang.org/x/sys/unix` exports `InotifyInit1`, `InotifyAddWatch` and `SizeofInotifyEvent`. The
probes below used them and needed no `go.mod` edit at all. The repo also already carries the
build-tag pattern this would need, three times: `exchange_linux.go`/`exchange_other.go`,
`ficlone_linux.go`/`ficlone_other.go`, `fiemap_linux.go`/`fiemap_other.go`.

Recorded because it changes the shape of the D-level call rather than the answer: **the dependency
argument does not separate the options.** What separates them is F6.

### F4. quince's own write is indistinguishable from an editor's — measured, not assumed

A directory watch (`IN_ALL_EVENTS`) over `AtomicWrite`, twice in a row:

```
CREATE      .config-354853348.tmp
OPEN        .config-354853348.tmp
MODIFY      .config-354853348.tmp
ATTRIB      .config-354853348.tmp
CLOSE_WRITE .config-354853348.tmp
MOVED_FROM  .config-354853348.tmp
MOVED_TO    config.yml
```

And the same watch over a **host-side** `printf > .tmpwrite; mv .tmpwrite config.yml`:

```
CREATE      .tmpwrite
OPEN        .tmpwrite
MODIFY      .tmpwrite
CLOSE_WRITE .tmpwrite
MOVED_FROM  .tmpwrite
MOVED_TO    config.yml
```

**The two differ in the temp file's name and in nothing else.** The issue calls self-write
suppression *"the defect every file-watch implementation ships first"*; this is why. There is no
field on the event, no actor, no cookie that survives the rename, and matching on the temp-file
pattern would suppress any editor that happened to pick a similar name while missing quince's own
write the day the pattern changes. **The event stream cannot answer the question, so the design must
not ask it of the event stream** — see D2.

### F5. A watch on the FILE PATH is not degraded after the first write — it is dead

Same probe, watch added on `config.yml` itself rather than its directory:

```
write #1  → ATTRIB, DELETE_SELF, IGNORED
write #2  → (nothing)
in-place append to the new file → (nothing)
```

`IN_IGNORED` is the kernel saying the watch is gone. The issue's *"a naive watch on the path stops
firing after the first edit"* understates it slightly in a way worth correcting: it stops firing for
**every** subsequent change including in-place ones, because the descriptor is not stale, it is
removed. **Watch the directory. Filter by name.**

### F6. inotify crosses the container boundary on a bind mount — and here is exactly what that covers

Watcher **inside** a container, writer **on the host**, over a host directory bind-mounted in. All
three editor shapes fire:

| host action | events seen inside the container |
| --- | --- |
| in-place append (`>>`) | `OPEN`, `MODIFY`, `CLOSE_WRITE` on `config.yml` |
| write-then-rename (vim, quince) | `CREATE`/`MODIFY`/`CLOSE_WRITE` on the temp, then `MOVED_FROM` + `MOVED_TO config.yml` |
| delete + recreate | `DELETE`, then `CREATE`/`OPEN`/`MODIFY`/`CLOSE_WRITE` on `config.yml` |

**What this does NOT establish, stated rather than left to be assumed** — because the whole reason
the issue asked for a measurement is that the class is known and the instance was not:

- **One kernel only.** A bind mount shares the inode, so inotify works. It says nothing about a
  **Docker Desktop** deployment on macOS or Windows, where the bind mount crosses a VM boundary and
  the host's writes are relayed by a file-sharing layer rather than performed on the watched inode.
- **Local filesystems only.** inotify does not see writes made by another host, so a `/data` on
  NFS or SMB reports nothing. This is a caveat about the *filesystem*, not about containers.
  **It does NOT decide D1** — see D1 leg 3. Kept as a measured limit of what F6 covers; it is not an
  argument for anything.
- **Not measured on the target hardware.** The probes ran on a session box, not on the low-end
  Synology the product targets.

### F7. Polling the whole file costs 12.19 µs

Per iteration, over a realistic 218-byte `config.yml`, 100 000 iterations:

| approach | cost per tick |
| --- | --- |
| `os.Stat` only | **2.33 µs** |
| `os.ReadFile` + `bytes.Equal` | **12.19 µs** |

At one tick per second, reading the entire file every time costs **12 µs/s — about 0.0012% of one
core**. Ten times slower on weaker hardware is still 0.012%. **The cheap thing and the correct thing
are the same thing here**, which is what makes D1 easy.

---

## Boundary

**In scope.**

| tree | what changes |
| --- | --- |
| `core/internal/config/` | a `Reload` path on `Service`, the poller that drives it, and the last-bytes record that suppresses self-writes |
| `core/cmd/quince/live.go` | start the poller at the wiring seam that already registers the appliers; stop it on shutdown |
| `docs/contracts.md` | §6's *"a hand-edit still needs a restart"* paragraph is discharged; §1's `discarded` definition widens **as ruled** (question 1) — recorded as an **amendment to quince#849**, not a wording change |
| `ui/src/lib/types.ts` | `:280`'s *"quince is running on its defaults"* goes false with the same ruling — the widening is client work too, not only canon work |
| `docs/quince.stack.md` | D12's *"file-watch pickup"* stops being a destination |
| `core/internal/config/*_test.go` | the reload suite: valid edit, invalid edit, self-write, no-op edit, delete, restore |

**Out of scope, each with a why.**

- **The live-apply table itself** (`docs/contracts.md` §6). A reload feeds the *same* appliers a UI
  save feeds, so every key's verdict is unchanged by construction. A hand-edit of a **restart**-bin
  key (`devices.*`, `tls` paths, `sessions.allow_insecure_transport`) is picked up into the snapshot
  and still needs a restart to take effect — **exactly as a UI save of that key does today**. That
  symmetry is the deliverable; changing any verdict is not.
- **The keys nothing reads** (`sessions.ttl_minutes`, `automation.*`). quince#656 and `qn.12`'s.
  Live-reload cannot make an unread field take effect, and folding it in would let this rung claim a
  key works when nothing consumes it.
- **~~A WebSocket event for config changes.~~ OVERTURNED — it is IN SCOPE and built.** Operator
  ruling 2026-08-17, [quince#1162](https://github.com/novkostya/quince/issues/1162), option C:
  `config.updated`, empty payload, fired on a write, an applied hand-edit and a **refused** one.
  contracts §3 carries it.

  **The rung declined it on a premise that was never true** — *"the Settings page already re-reads on
  focus"*. It does not: `refetchOnWindowFocus` is `false` app-wide (`ui/src/lib/queryClient.ts`),
  `useConfig` sets no `refetchInterval`, and there was no event. So **nothing refetched an open page**
  and a hand-edit applied while the screen kept showing the old document. `staleTime: 5_000` refreshes
  on remount, so navigating away and back was current; **staying on the page was the case that never
  updated** — which is exactly what testing a hand-edit looks like, and how the Operator found it.

  **Kept as an overturned bullet rather than deleted**, because this rung shipped without the event
  and a reader comparing `qn.6q` against the code needs to know which side of the ruling each part
  landed on.
- **The lost-update interleaving.** Operator hand-edits while a UI save is in flight: the save wins
  and the hand-edit is overwritten. That is true **today** and is a property of two writers with no
  lock between them, not of the watcher. **RULED as an acceptance** (question 3 below): named in
  contracts §6, with the window stated, and deliberately not built for.
- **`quince config validate` as a pre-flight.** D12 lists it; it is a CLI surface with its own scope
  and is not made necessary by this rung.

---

## Design

### D1 — POLL. Do not watch. (rung-ruled, and it needs no D-level ruling)

**Read `config.yml` on a fixed interval and compare the bytes.** No inotify, no `fsnotify`, no
platform split.

The dependency argument does not decide this — F3 shows inotify is free of one, so it applies
symmetrically and separates nothing. **Which leg carries the conclusion is stated explicitly, because
a merged spec is citable and the next builder needs to know which one to defend:**

1. **It costs 12 µs a second** (F7), and the whole mechanism is one function with no fd, no
   requeue-on-`IN_IGNORED`, no `IN_Q_OVERFLOW` path, and nothing to do differently on darwin.
   **This leg alone is sufficient.** A mechanism this cheap does not need a second reason.
2. **The content comparison is required either way** (D2). inotify does not remove it; it adds an
   event source on top of it. So polling is the *subset*, not the alternative — and the machinery it
   would sit on is not small: F5 shows the naive shape is silently dead, so a correct inotify
   implementation is a directory watch plus name filtering plus requeue handling, in front of the
   comparison you were going to write anyway.
3. **A tiebreaker, and thin — do not lean on it.** inotify reports nothing when the write happens on
   another host, so a `/data` on NFS or SMB sees no events. **The Operator raised the objection, in
   full: *"who on Earth would place a config on nfs/smb though? but anyway"*** (2026-08-17, relayed
   by the architect on quince#1094 after quince#1126 merged). It is fair. `config.yml` lives beside
   the app DB in the data volume, and an operator who puts *that* on a network share has larger
   problems than inotify semantics.

   **`but anyway` is quoted deliberately, and it is the half that says what this was.** The
   objection was raised and the thing was waved through — no change was demanded, and **no ruling
   was given.** The chain has three links and no verdict in it: *Operator raises an objection →
   architect agrees and re-weights on quince#1094 → this spec writes it down.*

**This spec argued (3) as a `no silent caps or fallbacks` point and ordered it first.** That reads
stronger than it is for this file in this deployment, and the objection above — with the architect's
re-weighting on quince#1094, which is where the reasoning actually happened — is why it now reads
last. D1 itself is rung-local and was never re-ruled. Corrected rather than deleted: a reader who
takes the network-filesystem argument for the load-bearing one would think D1 falls if that argument
is rebutted, and it does not — **poll because it is 12 µs and needs nothing, not because of network
filesystems.**

**The cost is honest and bounded: latency up to one interval.** For a hand-edit — a human at an
editor — one second is not a perceptible difference from instant, and it is a **guaranteed** ceiling
rather than inotify's *usually instant, sometimes never*.

**Interval: 2 s, not configurable.** A config key controlling how fast config is re-read is a knob
whose own changes are subject to itself; nobody needs it, and D12's *"every setting has a sane
default"* is better served by there being no setting. Stated as rung-ruled so a reviewer can object
to the number rather than discover it.

**Which options this forecloses, so a later rung can reopen it deliberately:** if a deployment ever
needs sub-second pickup, the poller is where an inotify *accelerator* goes — an event source that
triggers an early comparison, with the interval still underneath it as the floor. That is strictly
additive and is not this rung's.

### D2 — the signal is the CONTENT, never the event

`Service` records the exact bytes of the document it last **read or wrote**. Every tick reads the
file and compares. Equal → nothing happened. Different → this is a change quince did not make, and
it reloads.

**This is what answers quince#1094's question 1** — the tracker's numbering, not this spec's —
**and it answers it without a debounce.** F4 proves the
event stream cannot identify the writer. A timing window ("ignore events for 200 ms after our own
write") is a guess that is wrong in both directions: it drops a hand-edit that lands inside the
window, and it lets a slow write through outside it. Content comparison has no window.

Three properties worth stating because they are the reason to prefer it:

- **A hand-edit that reproduces quince's own bytes is suppressed, and that is correct** — it is a
  no-op edit, and reloading it would apply nothing.
- **It coalesces for free.** However many writes happen between two ticks, the tick reads the file
  once and decides once. There is no burst to debounce.
- **It is self-healing across a restart**, because the record is seeded from the same `Load` the
  process starts on.

`replaceLocked` sets the record under the same `mu` it swaps `s.cfg` under, from the `data` it just
wrote — the fallback-widened `data`, not the tidy attempt, or the guard's own degradation would look
like a hand-edit on the next tick.

### D3 — the invalid-edit contract: last-good, and the reason on screen

Explicitly left to this rung by `qn.6g` — *"the invalid-edit contract — a bad hand-edit keeps
last-good and shows a UI banner. That is D12's other half with its own `Warning`-surfacing
question."* D12's own wording is *"an invalid edit never crashes the app (keep running on last-good,
show a UI banner naming the bad key)"*.

A tick that finds changed bytes calls `Load`. On `!OK`:

- **`s.cfg` is NOT touched.** The running configuration stays what it was. No applier is notified —
  there is nothing to apply, and calling appliers with `old == next` would be a lie about what
  happened.
- **`s.warnings` becomes the load's warnings**, which is where `Load` already puts the cause on all
  three of its discard paths.
- **`s.discarded` becomes `true`** — under the **ruled** widened definition (question 1): *the file on
  disk was refused; the running configuration is not what the file says.*
- **`s.source` is updated** to the new mtime, so `GET /api/config` does not claim the running config
  came from a file that has since changed.
- **The record from D2 is updated to the bad bytes.** Otherwise every subsequent tick re-reads the
  same broken file, re-`Load`s it and re-logs, once per poll, forever.

**Recovery is symmetric and needs saying:** fixing the file makes the next tick's bytes differ again,
`Load` returns `OK`, and the config applies. Nothing needs restarting to escape the banner, which is
the property that makes the banner tolerable.

### D4 — reload takes `writeMu`, and writes nothing

The poller calls a `Service` method that takes `writeMu` for the whole read-parse-swap-notify
sequence, exactly as `replaceLocked` does. That gives one ordering for both producers and costs
nothing: the lock is held for a 12 µs read plus a parse.

**It must never write.** A reload that "tidied" the file — re-marshalling it through
`MarshalDeclared` — would rewrite the operator's document behind their back, and would do it on a
path with no request and no response to carry the warning. `Replace` writes; reload does not. This
is the one line of this design most likely to be violated by a well-meaning refactor, which is why
it is a numbered decision rather than a comment.

**An `Applier` must not trigger a reload**, for the same reason it must not call `Replace` — the
existing doc comment on `Applier` gains this producer by name.

### D5 — `declared` comes from the file, which is the easy half

`Loaded.Declared` is derived from the document that was parsed, so a reload gets the right answer for
free: after a hand-edit, *what the user set* is what the hand-edited file says. This is the one place
the reload path is simpler than the write path, where `replaceLocked` has to union the previous
declared set with the changed keys because a `PUT` carries a full document with no record of intent.

**A hand-edit therefore RE-TIDIES the file at the next UI save**, and that is correct rather than
surprising: keys the operator deleted by hand stay deleted.

---

## Stories

1. **A hand-edit applies.** With quince running, change `backup.require_encryption` in `config.yml`
   by hand. Within one interval the running engine enforces the new value — no restart, and no UI
   action.
2. **A hand-edit of a storage applies.** Add a `storage:` entry by hand; the storage subsystem takes
   it through the same applier a UI add uses, including the reconciliation trigger.
3. **quince's own write does not reload.** A UI save produces exactly one apply — the write path's —
   and the following tick is silent. Observable as a log line count, not inferred.
4. **A no-op hand-edit does not reload.** Rewriting the file with identical bytes (`touch`, or a save
   that changes nothing) applies nothing.
5. **A bad hand-edit keeps last-good.** Break the YAML. The running config is unchanged, `GET
   /api/config` reports `discarded` with the cause in `warnings`, and the daemon keeps serving.
6. **Fixing it recovers, with no restart.** Repair the file; the next tick applies it and the banner
   clears.
7. **A deleted `config.yml` keeps last-good.** Removing the file is not an instruction to fall back
   to defaults — it is almost certainly an accident mid-edit, and defaults would silently disable
   every declared storage. quince keeps running on last-good and says the file is gone.
8. **A restart-bin key is picked up but still needs a restart.** Hand-edit `devices.usbmuxd_socket`;
   `GET /api/config` shows the new value, and the muxer supervisor is unchanged — the same answer a
   UI save gives, which is the symmetry this rung is for.

---

## Gates

Beyond `make gates`:

| G | what | how |
| --- | --- | --- |
| G1 | stories 1–7 as Go tests against a real temp file, driving `Service` directly with a short interval | `make gates-go` |
| G2 | story 3 asserted by **applier call count**, not by absence of a symptom | in G1 |
| G3 | the self-write record survives the round-trip guard's full-document fallback (`replaceLocked`'s degradation path) | in G1 |
| G4 | story 8 end-to-end over the API | `make gates` (httpapi) |
| G5 | **on hardware / on the real stand: a hand-edit over SSH into the bind-mounted `/data`, applied without a restart** — F6 measured the kernel mechanism, not the product | **OWED, Operator** |
| G6 | the interval's cost on the target NAS, measured rather than extrapolated from F7 | **OWED, Operator** |

G5 and G6 are named as owed with an owner because F6 says plainly what the box measurements do not
cover. A rung that reports green on G1–G4 and calls file-watch proven would be claiming more than was
shown.

---

## Fixtures

No new transcript fixtures — this rung touches no device path. The reload suite writes real files
into `t.TempDir()`, which is what the existing config tests already do (`config_test.go`,
`writedeclared_test.go`), and the bad-edit cases reuse the malformed documents `Load`'s existing
tests already carry.

---

## Rule check

| rule | how this plan complies |
| --- | --- |
| **No silent caps or fallbacks** | The invalid-edit path is the *whole* of D3, and it surfaces through `warnings` + `discarded` rather than a log line. The poll interval is a stated ceiling, not a hidden one. **This row does NOT rest on D1** — D1's load-bearing leg is cost, not the network-filesystem case (see D1 leg 3). |
| **State honesty** | A reload that fails applies nothing and says so; `s.cfg` is not touched on `!OK`. `source.mtime` is updated on both paths so `GET /api/config` never implies the running config came from the file currently on disk when it did not. |
| **Docs are part of the diff** | contracts §6's *"a hand-edit still needs a restart"* paragraph and §1's `discarded` definition, and D12's *"file-watch pickup"* — all three named in Boundary, all three land with the code. |
| **Don't improvise architecture** | Every architectural call was routed and **all are RULED** (Operator, 2026-08-17, quince#1130): the `discarded` widening, whether the poll choice owes a `D<N>`, and the lost-update acceptance. **Nothing on this rung waits on a ruling.** D1/D2/D4/D5 are rung-local — inside `core/internal/config`, changing no contract surface. |
| **Interface facts looked up live** | F1–F7 are measured at `ebc8219` on 2026-08-17. F3 corrects the issue's own framing; F5 sharpens it. Nothing here is recalled. |
| **Config tidiness (D12)** | The interval is deliberately **not** a config key — D1. Reload adds no key, and D4 forbids the reload path from writing, so a hand-edited file is never re-tidied behind the operator's back. |
| **Secrets discipline** | Untouched. `config.yml` carries no secrets by D12 and this rung adds none; the poller reads a file it already has open at startup. |
| **Privacy** | The spec cites container paths (`/data/config.yml`) and repo-relative paths only. No host paths, no topology, no hardware sizing. |
| **Never mutate a committed version** | Not touched — no storage path, no `latest/`, no `working/`. Named because a config reload *reaches* the storage applier: it feeds `ApplyStorages`, the same entry point a UI save feeds, which re-resolves slots and creates nothing (`live.go:517`, quince#415). |
| **Every hardware bug becomes a replay fixture** | No hardware path. G5 is the hardware obligation and is declared owed. |
| **Coverage declared** | The build PR declares `go test -cover` for `internal/config` plus a known-untested list; **already known to be on it: G5 and G6**, both environment-bound. |

---

## ALL FIVE QUESTIONS ARE RULED — nothing on this rung waits on a ruling

Operator ruling, 2026-08-17, relayed by the architect on
[quince#1130](https://github.com/novkostya/quince/issues/1130). **quince#1130 is this rung's tracker
until a rung exists** — recorded because *unallocated* reads like *unowned*.

**1. RULED — `discarded` WIDENS. The recommendation was accepted as written.** Keep the name, keep
the consumer rule (*"branch the HEADLINE on this, render `warnings` either way"*), and restate the
definition as **the file on disk was refused; the running configuration is not what the file says.**

No second boolean: *"the two situations differ in what the operator should do, and `warnings` already
carries that — a boolean that no client branches on differently is a distinction that costs every
client and buys nothing."*

The `AddStorage` consequence was accepted as a **strengthening**, and named the strongest argument in
the issue: under the widened definition a bad hand-edit means an add is refused rather than splicing
defaults over an unparseable file — quince#852's hazard arriving through a new door and already
guarded, for free.

**Two consequences the issue did NOT name, found while ruling, and they make this larger than a
definition edit:**

- **It AMENDS A PRIOR OPERATOR RULING.** `docs/contracts.md:1641` reads `RULED and IMPLEMENTED:
  discarded — THE FILE ON DISK WAS REFUSED AT LOAD` (Operator, 2026-08-12, quince#849), spelling the
  definition as *"quince is running on `Default()` and nothing the file declares is in effect"*. The
  first clause is what goes false. **The rung's PR must record this as an amendment to quince#849**,
  not as a wording change — the same ruling, revisited because a new door was opened.
- **It FALSIFIES LIVE UI COPY.** `ui/src/lib/types.ts:280` says *"the file on disk was REFUSED at
  load, so quince is running on its defaults and …"*. That second clause becomes wrong the moment
  file-watch ships. **So the widening creates work in the rung, not only in `contracts.md`**, and
  whoever slices it carries both.

**2. RULED — no new `D<N>`. A paragraph under D12, and it must carry the MEASUREMENT.** The
dependency argument is what would have made this D-level, and F3/F7 close it: no option costs a
dependency, `golang.org/x/sys` is already a direct requirement, and the `_linux.go`/`_other.go`
pattern is in the tree three times. *"A `D<N>` is for a choice that constrains the stack; this one
constrains nothing."*

**The condition on that paragraph is not optional.** *"We chose poll"* invites reopening on exactly
the dependency grounds the measurement already closed. It must carry **12.19 µs per tick** over a
218-byte config, and **F5** — a watch on the file *path* is dead after write #1, so a correct inotify
implementation is a directory watch plus name filtering plus requeue handling in front of the content
comparison you were writing anyway. Those two facts are the whole argument.

**3. RULED — name the lost-update interleaving in contracts §6, and do NOT build for it.** Operator,
2026-08-17, on quince#1130. The recommendation was accepted as written. A hand-edit made while a UI
save is in flight is overwritten by the save's `AtomicWrite`; the next tick then sees quince's own
bytes and suppresses (D2), so nothing surfaces. **This is true at this head, with no watcher anywhere
near it** — the save overwrites the hand-edit either way — so file-watch neither causes it nor worsens
it. Closing it means a file lock, or an mtime precondition on `PUT`, several times the size of this
rung.

**THE RULING ATTACHED A CONDITION TO THE SENTENCE, and it is what makes it worth writing:** *"pre-existing"
alone is true and useless. §6 must say that quince can lose a hand-edit made **while a save is in
flight**, and name that window — because **file-watch does not cause the loss but does create the
expectation that makes it surprising.** Before this rung nobody expected a hand-edit to be picked up
without a restart, so the loss read as *how it works*; after, it reads as a defect.

**Why it needed a ruling at all, given the recommendation was obviously right:** writing down that
quince can silently lose an edit and shipping anyway is an acceptance under `no silent caps or
fallbacks`, and this project rules those rather than assuming them.

**4. RULED — unallocated, then SUPERSEDED the same day.** The 2026-08-17 in-session ruling on
quince#1094 put file-watch inside v0.1, which allocated `qn.6q` and moved this file — which is what
the unallocated ruling's own sentence said would happen when a rung existed.

---

### A fifth question was asked here that the spec never had, and the swap is worth recording

quince#1130 carried **four** of this spec's **five** questions. Its slot 3 was *"where does the
`PROPOSED (gap)` block go, and when"* — a question invented while filing, which displaced the
lost-update question above. **That question is answered: it DISSOLVED**, because ruling question 1
removed its subject — there is no gap block to place, because there is no gap. Nothing goes into
`contracts.md` §1 now; what is owed is the ruled definition, and it lands when the behaviour lands,
under *docs are part of the diff*.

**The swap is recorded rather than quietly repaired, because of how it hid.** The count matched at
four, the numbering matched, and slot 3 was occupied — **a substitution leaves no gap to notice**,
where an omission is caught by counting. The check when carrying a document's open questions to a
tracker is therefore not *are there four*, it is ***are they the same four***.

---

## PR slicing

**PR 1 is this spec** (canon §8: reviewed before any code exists) — the PR you are reading.

The build, when the rung is scheduled, is three:

| | | |
| --- | --- | --- |
| **2** | `Service` gains `Reload` + the last-bytes record; `replaceLocked` sets it. No poller, nothing wired — the seam and its tests | stories 3–7 |
| **3** | the poller, started and stopped at the `live.go` wiring seam | stories 1, 2, 8 |
| **4** | the doc discharge: contracts §6 + §1, `stack.md` D12 | — |

Sequenced from `main`, never stacked (`CLAUDE.md` §1). PR 4 is separate rather than folded into 3
because it is the PR a reviewer should be able to read as prose against the shipped behaviour, and
because it is the obligation most likely to be skipped — `qn.6g`'s own file-watch block said the same
thing about the same paragraph.
