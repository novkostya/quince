# `forge-watch` fixtures

Observations for the pure halves of `bin/forge-watch`. Most files are a **pair** — two consecutive
observations, asserting which events the second must produce. A file marked `"kind": "sequence"` is an
initial state plus N observations replayed in order through one state file, for the defects that live
in what the state *remembers* between ticks. A file marked `"kind": "watch"` drives the liveness
classifier instead of the event one. A file marked `"kind": "loop"` is the one impure shape: it runs
the real `watch` verb against a stub `gh` and asserts what the loop *did*.

**Provenance matters here more than usual**, because these fixtures exist to stop two real defects
becoming folklore — and a fixture that overstates its own origin would be the same class of defect
it is meant to catch. So, precisely:

- **field shapes and values are real**, captured from the forge with `gh pr view <n> --json
  number,state,updatedAt,statusCheckRollup,reviews` on 2026-07-26;
- **the "before" observation in each pair is derived**, not recorded: the failures happened before
  anyone thought to record them, so the prior state is reconstructed as *the same query with the
  not-yet-existing PR removed* (`pr17`) or *with the check conclusions as they were before the run
  finished* (`pr16`). The transition is what the fixture tests, and it is faithful; the claim that
  someone recorded it live would not be.

Both defects come from the night of 2026-07-25/26, from **two independent monitors written by two
different sessions** — which is the useful part of the story: same class, different bug, neither
author having seen the other's code.

| Fixture | Whose monitor | The bug | What it swallowed |
| --- | --- | --- | --- |
| `pr17-empty-queue-transition.json` | the architect's | emitted only when the *previous* state was non-empty, so an empty→non-empty transition produced nothing | #17 sat unnoticed while the queue was reported clear |
| `pr16-red-unreviewed.json` | the implementer's | watched reviews but not check conclusions | #16 sat red for ~an hour; "no review yet" and "unreviewable, CI is red" printed identical silence |

The implementer's monitor carried a second, **latent** defect worth a fixture even though nothing
was observed to fall through it: it enumerated the PR list at arm time, so a PR opened afterwards
was outside its watch. `pr20-opened-after-arm.json` covers it. Recorded as latent, not as a miss —
it never demonstrably swallowed anything.

## The second round: the enumerated model deadlocked the loop (quince#43)

Later on 2026-07-26 the defect class came back, this time not as an under-report but as a **stop**.
Two agent sessions sat on opposite sides of one transition — *changes requested → fixed → awaiting
re-review* — for over two hours, each waiting for the other, both watches reporting healthy. Four
independent blind spots meet on that transition: a push is not an event, green checks were
deliberately not an event, a comment is not an event, and `reviewDecision` does not move across a
fix so no review fires either. A fifth, found while fixing those, is not covered by the `updatedAt`
backstop at all.

| Fixture | The blind spot | What it cost |
| --- | --- | --- |
| `pr37-fix-goes-green.json` | a push that goes green produces no typed event whatsoever | two PRs deadlocked >2 h; three Operator nudges to unstick the run |
| `pr36-comment-only.json` | a comment is not an event | a held, approvable PR waited **64 min** for a confirmation that had already been posted |
| `pr37-approved-after-fix.json` | `review` had **no fixture at all** — it could have stopped working silently | nothing observed; a gap in the harness, found while writing the others |
| `behind-after-foreign-merge.json` | a PR made unmergeable by someone **else's** merge (illustrative, see below) | not yet observed to cost anything; quince#46 is the policy half |
| `updated-unattributable.json` | `updatedAt` says WHEN and never WHO (illustrative) | a session assumed the latest activity was its own and reported nothing owed, with three items owed |
| `mergeability-negative-space.json` | what mergeability must *not* report (illustrative) | — |

**Provenance for this round, to the same standard as the first.** `pr37-*` and `pr36-*` carry **real
timestamps, logins and verdicts** from quince#36 and quince#37, read back off the forge on
2026-07-26 with `gh pr list --json number,state,updatedAt,mergeStateStatus,reviews,comments,commits,mergedBy`;
their `before` halves are derived the same way as the first round's, by rolling the recorded payload
back one act. The three fixtures **numbered 100–105 are illustrative, not recorded** — the numbering
says so on purpose. They encode shapes derived from how the forge behaves (lazy mergeability
computation, strict up-to-date protection) rather than from a captured pair, and each names in its
own note the single thing it asserts. A fixture that dressed a derivation up as an observation would
be the very defect this directory exists to stop.

**Two amendments to the first round**, both narrowings rather than changes of behaviour:
`pr16-red-unreviewed.json` now holds `updatedAt` **identical** across the pair — whether a check-run
completion moves a PR's `updatedAt` is not a question that fixture was recorded to answer, and it was
quietly asserting an answer; and `pr20-opened-after-arm.json` gains the merge in its `activity` list,
so it exercises attribution positively (`actor=novkostya kind=merge`) instead of landing on
`unattributed` by omission.

**The fixtures have teeth, and that was checked rather than assumed.** Replayed against the
pre-quince#43 `bin/forge-watch`: the six new fixtures fail, and of the two amended ones `pr20` fails
while `pr16` passes either way — correctly, since `pr16` was narrowed rather than changed. `pr17` also
passes either way, for the same reason.

**One more, found in review of this round and fixed with a third fixture shape.** `UNKNOWN` was treated
as "no answer" for reporting — correctly — and then *stored* as though it were a state, so a PR already
`BEHIND` that saw one `UNKNOWN` tick and came back `BEHIND` re-announced a condition that never
changed. `mergeability-unknown-flap.json` pins it, and it needs **three ticks through one state file**,
which is why fixtures may now be `"kind": "sequence"` — an initial state plus N observations replayed
in order. A pair cannot reach this: a pair whose `before` already held the carried-forward value would
be asserting the behaviour under test. Its teeth were checked the same way, but more narrowly — the
current harness run against the *old* state-write, so the only thing that differs is the one hunk:
tick 2 emits the duplicate, and nothing else in the directory changes.

## The third round: the restart path, and a check that could only pass

A third fixture kind, `"kind": "watch"`, drives the liveness classifier from a state file, a frozen
`now` and a `pid_alive` answer, because that classifier is exactly as prone to reporting what it never
checked as the event half is — and a restart path with no fixture is a promise.

| Fixture | What it pins |
| --- | --- |
| `watch-absent-cold-start.json` | no state at all → `absent`, the only case where seeding is right |
| `watch-dead-not-absent.json` | state on disk, process gone → `dead`, **re-arm, do not reseed**. The case the original requirement could not check: its state lived in a session scratchpad, and the failure it defended against destroys the scratchpad, so it would have looked in an empty room |
| `rearm-emits-what-accrued.json` | re-arming from that state emits the two PRs that opened during the 44-minute gap — not `queue-empty`, not silence |
| `watch-live-nothing-to-do.json` | live pid, fresh heartbeat → `live`, and it *says* so; a healthy answer that is silence cannot be seen to have run |
| `watch-wedged-stale-heartbeat.json` | live pid, ticks stopped → `wedged`. A pid check alone calls this healthy |
| `watch-starting-is-neither-dead-nor-wedged.json` | armed, first tick unfinished → `starting` (exit 9). Measured: the pre-change tool called this exact state `wedged` and said "run `forge-watch stop`" — the NULL-age arm needs no elapsed time, so ordering is the whole safety |
| `watch-starting-is-bounded.json` | the same record past its bound → `dead reason=never_ticked`. An unbounded `starting` would be a state that cannot fail |
| `watch-orphaned-owner-is-gone.json` | live pid, owner session gone → `orphaned` (exit 10). The state the deliberate-SIGKILL experiment produced and nothing could name: `status` said `live` for four minutes about a watch that could wake nobody. Evaluated BEFORE `starting`, because a session that dies seconds after arming leaves an orphan, and `starting` is the class that says "do not arm a second one" |
| `watch-orphan-unknown-owner-is-not-orphaned.json` | the same record with no owner on it → `live`. The conservative direction and the migration case: `orphaned`'s remedy is "stop it", so an unverifiable owner must never yield it, and every state file written before the field existed classifies exactly as it did |
| `watch-hand-tick-is-not-a-watcher.json` | a hand-run tick must not be able to make a dead watcher look alive |

**One design correction earned during this round, and worth recording because it is the same shape
again.** The first version confirmed the recorded pid by grepping `/proc/<pid>/cmdline` for
`forge-watch` — and reported `pid_verified=yes` for the shell that had just *run* forge-watch, whose
command line contained the string. A check whose positive answer can be produced by the act of asking
is not a check. It is gone; `live` now requires the pid to exist **and** the heartbeat to be fresh,
which is strictly stronger and cannot be fooled that way.

## The fourth round: everything here was green while the watch was deaf (quince#62)

The armed loop had no exit condition — `while :; do tick; sleep 60; done` — and a session is woken by a
background task *completing*, so it detected everything and delivered nothing for fifty minutes. **Every
fixture above passes on that watcher**, and so does every liveness check: fresh heartbeat, state
rewritten every 60 s, `status` reporting `live`. Health was never what was broken, so a fourth kind was
needed that asserts **termination**.

`"kind": "loop"` runs the real `watch` verb as a subprocess against a generated stub `gh` that answers
the Nth call with the Nth recorded payload and then repeats the last one forever. It is the only impure
shape in this directory — a subprocess, a clock and real sleeps — and it earns that by being the only
way to ask the two questions that separate a working watch from a deaf one.

| Fixture | What it pins |
| --- | --- |
| `watch-exits-on-the-event.json` | a verdict arriving on tick 3 **ends the loop**, with the events on stdout and exit 0 — and, because those events are in the expectation at all, that the loop did not exit on ticks 1–2 |
| `watch-silence-keeps-watching.json` | nothing changing does **not** end it; the run ends at the declared `--max-wait` bound, announcing `event=watch-idle` rather than exiting quietly |
| `watch-baseline-is-not-a-wake.json` | `first-observation` and the `queue-empty` beside it are the baseline, not news — waking on them would make arming a watch a busy circle of arm-exit-arm |
| `watch-rearm-does-not-wake-on-its-own-gap.json` | the first tick after re-arming from a `dead` watch emits `tick-overdue` **by definition** — the dead watcher's `due` is in the past — and that is a fact about the watch that ended, not news. It is printed and does not wake. A later one still does |

`watch-rearm-…` was **found by arming the verb on a live PR**, not by reading the code: the first real
arm re-armed from a dead state and exited on its own gap. It carries an `"initial"` state, which is what
makes a re-arm expressible in a loop fixture at all; its teeth were checked by deleting the one rule
that implements it, whereupon the fixture exits 0 on tick 1 instead of reaching the idle bound.

**Teeth, and the one place where the standard phrasing does not fit.** Replayed against the shipped
hand-rolled loop, `watch-exits-on-the-event.json` does not *fail* — it **hangs**, which is the defect
stated exactly. Measured rather than asserted: the same stub, the same three payloads, `timeout 20 sh -c
'while :; do forge-watch tick …; sleep 2; done'` printed `event=review pr=61
verdict=CHANGES_REQUESTED` and was still running when the bound killed it (exit 143), while the verb
exited 0 on tick 3 carrying the same line. So the harness bounds every loop fixture with `timeout`, and
says out loud when `timeout` is not installed rather than running unguarded.

`elapsed=` and `ticks=` are normalised before comparison: they are facts about the box, not about the
behaviour, and a fixture that pinned them would fail on a loaded machine and teach everyone to ignore
it. The **exit class** is asserted, as a line, because it is what a caller reads when it ignores stdout.

## The fifth round: the session that armed nothing (quince#62, second half)

An hour after the deaf watcher, the implementer half produced the complementary failure: **no loop at
all** — no watcher, no state file, no fallback — ending a turn on *"the ball is back with the
reviewer"* four minutes before its verdict landed. `forge-watch owed` is the predicate that makes that
absence detectable, and `"kind": "owed"` drives its **pure** half: given which repositories have open
PRs and what class their watches are in, what does the session owe.

| Fixture | What it pins |
| --- | --- |
| `owed-an-unwatched-pr-is-owed.json` | one watched repository does not excuse an unwatched one — every repository is reported separately and the exit carries the worst case |
| `owed-dead-and-wedged-need-opposite-remedies.json` | `dead` says re-arm without reseeding; `wedged` says stop the running process first. One message for two situations is the defect that split those classes apart to begin with |
| `owed-a-starting-watch-owes-nothing.json` | a starting watch beside a dead one: `ok` for the first, still exit 8 for the second |
| `owed-a-lone-starting-watch-is-not-owed.json` | every entry starting → exit 0. The only shape that can prove `starting` does not contribute |
| `owed-a-live-watch-owes-nothing.json` | the satisfied case **says so**. A gate whose passing answer is silence cannot be seen to have run |

## The sixth round: the state a PR spends most of its waiting in (quince#65)

quince#63 was approved, green and mergeable, and **sat unmerged for sixteen minutes** behind a live,
quiet watch. The sixth blind spot, and the first one that did not hide in code: it hid in a
**justification**. `event=checks` fires only on `FAILURE`, and the note explaining why that was free
said *"the push preceding those checks moves `updatedAt`, so `updated` carries the transition"* — true
where a push exists (fix → re-review), false where none does (approved, then CI finishes).

Both halves were measured live on quince#66 rather than reasoned about, in a window with no push,
comment or review:

| what | reading |
| --- | --- |
| `updatedAt` across two check completions | **frozen** at `21:07:11Z`, fourteen samples at ~10 s |
| the same PR, approved at `21:14:47Z`, CI running | `ms=BLOCKED` … `ms=BLOCKED` … **`ms=CLEAN` at `21:19:39Z`**, `updatedAt` still `21:14:47Z` |

So the only field that moves when a PR becomes landable is `mergeStateStatus`, and the `updated`
backstop is structurally blind to it — nothing happens *to* the PR, which is exactly why its timestamp
does not move.

| Fixture | What it pins |
| --- | --- |
| `pr63-approved-then-ci-goes-clean.json` | the pure transition: identical `updatedAt` in both halves, so `updated` must **not** appear, and `mergeability … status=CLEAN` must |
| `watch-wakes-when-a-pr-becomes-landable.json` | the **live path** — the real verb, three ticks, `updatedAt` identical throughout, and the loop ends on the third |

The second one exists because the first is not enough, and that is the lesson rather than a detail: a
pure fixture for green checks **already passed** while the live path delivered nothing for sixteen
minutes. Teeth, measured: against the classifier as it stood, the pure fixture emits **nothing at all**
and the loop fixture runs to its idle bound and exits 6 — the sixteen-minute silence, reproduced in
twelve seconds.

## The seventh round: the issue channel — the authority a blocked session waits on (quince#80)

Rulings land on **issues**, not PRs — a blocked session's unblock arrives as a comment on the issue it
is stuck on, and most of those issues have no PR at all. So the event half learned to watch a declared
set of issues and, half two, any issue an open PR references without being asked. These fixtures pin
both the true-positive channel and the silence that keeps it worth reading; three of them are
`"kind": "sequence"` because the property lives in what the state remembers across a failed read.

| Fixture | What it pins |
| --- | --- |
| `issue80-a-ruling-lands-on-a-declared-issue.json` | half one: a declared-blocking issue gains a comment while the session holds a stale read — an event fires **and names the issue**. `issue-comment` is the typed event a blocked session acts on; `issue-updated` fires beside it as the backstop and carries an actor |
| `issue80-an-undeclared-issue-is-silent.json` | half two, the false-positive side: an issue in **neither** set produces nothing — a channel that woke for every issue in the repo is one a session learns to ignore, which is indistinguishable from unwatched |
| `issue80-a-referenced-issue-needs-no-declaration.json` | an issue an open PR references is watched **automatically** (`via=referenced`), so a session need not remember to declare the issue its own PR is for |
| `issue80-a-closed-blocker-is-the-news.json` | an issue **close** wakes for the declared set — a closed blocker is exactly the news a blocked session wants — and carries **no actor**, because `gh issue view` does not report who closed it (naming the last commenter would be a bystander, the defect `updated-unattributable` already caught) |
| `issue80-status-shows-a-declaration-and-its-age.json` | a declaration **survives** a session — it lives in the state file, which outlives the session — and its **age is displayed**, so a successor can see it is inheriting someone else's blocked list rather than re-deriving one silently |
| `issue80-a-failed-read-must-not-swallow-a-ruling.json` | sequence: the baseline is **per-issue** (adding one issue mid-watch does not re-baseline the set), and an issue **missing from a failed observation keeps its stored record** — so a ruling that lands during a `gh issue view` outage is not swallowed by the next tick |
| `issue80-three-rulings-are-not-one.json` | `count=N` when several comments land in one interval — a re-arm after an outage diffs the whole gap at once — so a woken session knows whether it is reading one ruling or three, while `at=`/`actor=` still point at the newest |
| `issue80-a-cross-repo-title-ref-is-not-ours.json` | regression guard for a defect the tool found in **itself**: a repo-qualified `quince#87` in a devlog PR title must not be fetched against the devlog. It carries no issue payloads, so any errant fetch surfaces as an `issue-fetch-failed` line the expectation does not contain |

**Fixtures folded in from adjacent work.** Not a round of their own — each rode in with a nearby change
and belongs beside it, listed here so the map is complete, and (since quince#107) kept complete by a
gate rather than by memory:

| Fixture | What it pins | From |
| --- | --- | --- |
| `owed-an-empty-reviewer-queue-still-owes-a-watch.json` | an empty queue is **not** a legitimate finish for the reviewer — `owed --all` reports a watch owed with reason `declared`, measured against two overrides that were right and one obedience that went dark | quince#71 |
| `owed-a-reviewer-at-rest-is-armed-not-stopped.json` | the reviewer's correct rest is **watch armed and idle**, and the satisfied answer must say so with the right reason — not "queue empty, therefore stopped" | quince#71 |
| `owed-an-orphaned-watch-names-its-remedy.json` | `orphaned` fell to the catch-all and was reported as *"the watch class could not be read (10)"* — a **wrong sentence**, not a missing one, since the class was determined perfectly. Paired with `wedged`, which shares its remedy but not its diagnosis, so folding the two arms together fails here | quince#195 |
| `watch-a-hand-tick-must-not-erase-the-watcher.json` | the **mirror** of the hand-tick property: `step()` must carry `.watch` forward, or a hand tick erases a live watcher's record and the next arm puts a **second** watcher on one file (quince#50's race). Asserts **state**, not events, which is why all prior fixtures passed against the defect | quince#103 |
| `first-observation.json` | the sentinel discharging: `never observed` must not read as `observed empty` — the first tick reports itself and invents no changes | baseline |
| `first-observation-nonempty.json` | a first tick against a **non-empty** queue reports itself and the count, and must **not** replay the standing queue as opened/merged/review events — found live emitting sixty events for work finished hours earlier | baseline |
| `hint-the-architect-is-not-handed-repo.json` | the `owed` arm-hint hands an architect the `--all` form (its repos collapse to one line), never the implementer's per-repo `--repo` — a hint is copied verbatim, and the `--repo` form would arm a watch smaller than the declared set (quince-devlog#3) | quince#66 |

**The half that no fixture here can cover is the hook itself**, because it is a claim about the
*harness* rather than about this code: that a `Stop` hook in project settings runs, that exit 2 blocks
the stop, and that `stop_hook_active` bounds the block to once. That was verified by running real
headless sessions (spec §4f) — including the one that was told *"reply with the single word PING and do
not use any tools"* and tried to arm a watch instead.

