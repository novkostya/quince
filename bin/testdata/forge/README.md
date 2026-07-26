# `forge-watch` fixtures

Observation pairs for the pure `step(state, observation) → (state, events)` function. Each file is
two consecutive observations; the test asserts which events the second must produce.

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
pre-quince#43 `bin/forge-watch`, every fixture in this round fails and both amended ones fail;
`pr16` and `pr17` pass either way, which is correct — their behaviour did not change.
