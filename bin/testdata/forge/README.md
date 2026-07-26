# `forge-watch` fixtures

Observations for the pure halves of `bin/forge-watch`. Most files are a **pair** — two consecutive
observations, asserting which events the second must produce. A file marked `"kind": "sequence"` is an
initial state plus N observations replayed in order through one state file, for the defects that live
in what the state *remembers* between ticks. A file marked `"kind": "watch"` drives the liveness
classifier instead of the event one.

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
| `watch-wedged-stale-heartbeat.json` | live pid, ticks stopped → `dead`. A pid check alone calls this healthy |
| `watch-hand-tick-is-not-a-watcher.json` | a hand-run tick must not be able to make a dead watcher look alive |

**One design correction earned during this round, and worth recording because it is the same shape
again.** The first version confirmed the recorded pid by grepping `/proc/<pid>/cmdline` for
`forge-watch` — and reported `pid_verified=yes` for the shell that had just *run* forge-watch, whose
command line contained the string. A check whose positive answer can be produced by the act of asking
is not a check. It is gone; `live` now requires the pid to exist **and** the heartbeat to be fresh,
which is strictly stronger and cannot be fooled that way.

