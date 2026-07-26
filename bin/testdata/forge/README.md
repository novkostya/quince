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
