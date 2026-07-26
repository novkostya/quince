# rung-loop — a rung that runs without a human carrying "review posted"

> Status: **PARTLY BUILT.** `bin/forge-watch` exists — the pure `step()`, `tick`, `replay`, and the
> complete event model of §4b (quince#43). What remains: restart safety, the declared watch set, the
> loop modes in the skills, and the runner supervision unit. Takes
> [devlog#4](https://github.com/novkostya/quince-devlog/issues/4); feature-named because the
> deliverable outlives the issue. **Implementation is gated on pr.5 (runner host)** — see
> *Sequencing*; this PR settles the design the issue asked to settle before building.

## Goal

An implementer session and a reviewer session behave as coroutines over the forge: each arms a
watch, sleeps, and is resumed by a **fresh session** when its counterpart acts — so a rung advances
without a human relaying "review posted" across the gap, and stops loudly the moment it needs a
ruling.

## Boundary

**In scope:** `bin/forge-watch` (the event source and its state machine), loop modes in
`.claude/skills/kickoff/SKILL.md` and `.claude/skills/review-pr/SKILL.md`, the runner-side
supervision unit, `docs/specs/rung-loop/`, fixtures under `bin/testdata/forge/`.

**Out of scope:** the runner host itself (pr.5); any change to what review *means* (`/review-pr`'s
protocol is untouched — only its triggering); auto-merge (landing stays `/land`, deliberate);
product code (frozen); anything under `.github/workflows/`.

**Expected contract changes: NONE.**

## The four questions the issue asked to settle

### 1. Auto-resume wakes a FRESH session against the PR thread

Ruled as the issue proposes, and the reasoning is worth keeping because it inverts the obvious
design: **not** a long-lived session that stays alive waiting. The PR carries the diff, the review,
the evidence and the thread, so `/kickoff <pr>` continues cold — which dodges long-session drift and
compaction entirely.

The property this buys is the valuable part: **if a fresh session cannot continue from the PR alone,
that is a defect in the PR**, not in the loop. It is the resurrection test at rung scale, enforced
mechanically instead of by good intentions.

### 2. Loops escalate; they never improvise

pr.2 produced three decisions that were genuinely the Operator's — `keyctl` being root-only, the
widening to `pct exec`, and the deploy-URL/privacy collision. **A coroutine that guesses those is
worse than no automation**, because it converts a question into a fait accompli with a commit
attached.

So the loop stops on the gap protocol's own triggers (contract surface, storage semantics, security
posture, user-visible behaviour) and on any review comment it cannot satisfy without a ruling. The
gap protocol already says stop; the loop makes stopping *mechanical* rather than a matter of
noticing.

**A stop is recorded on the substrate, not only in a notification:** a PR comment stating what was
hit and what is needed, because a push that nobody reads leaves no trace and the forge is the
memory.

### 3. Watchdog: silence must never be indistinguishable from progress

The polite deadlock — implementer waiting for review, reviewer waiting for a push, one signal
missed, both asleep looking busy — is defeated by making the *absence* of movement an event.

Each loop tracks the PR's `updatedAt`. No movement for `stall_hours` (default **3**) → **post
`rung stalled at PR #<n>` as a PR comment first**, naming what it was waiting for; the push is
secondary. A stall at 02:00 that only pushes gets read at 09:00 either way, while a stall that
comments is legible to whoever opens the PR next — including a fresh `/kickoff` session, which is
the reader this whole design is built around. A loop that cannot say why it is still waiting has
nothing to wait for.

### 4. The monitor bug becomes a fixture, not folklore

**Origin — two independent monitors, two different bugs, one class.** The first draft of this spec
collapsed both into "I was the buggy monitor", which was wrong twice over: it took someone else's
defect and it hid the more interesting fact that two sessions wrote two different versions of the
same mistake without seeing each other's code.

| Monitor | The bug | What it cost |
| --- | --- | --- |
| the **architect's** | emitted only when the *previous* state was non-empty, so an empty→non-empty transition produced nothing | **#17 sat unnoticed** while the queue was reported clear |
| the **implementer's** | watched reviews but not check conclusions | **#16 sat red for ~an hour**; "no review yet" and "unreviewable, CI is red" were the same silence |
| the implementer's, **latent** | bound its PR list at arm time, so a PR opened afterwards was outside the watch | nothing observed — recorded as latent, not as a miss |

Both realised bugs are the class this project now has canon for (*an error message is a claim* —
program doc): the silence asserted "nothing happened" while meaning "I never looked". That two
authors produced it independently, in one night, on the same substrate, is the argument for encoding
it in a state machine with fixtures rather than in anybody's care.

### 4b. Enumerating the events was itself the bug (quince#43)

Everything in §4 was right and insufficient. The list of typed events **is a claim about what can
matter**, and on 2026-07-26 it was wrong four times over on one transition — *changes requested →
fixed → awaiting re-review* — which deadlocked two agent sessions for over two hours, each waiting
for the other, both watches reporting healthy. A push is not an event; green checks were deliberately
not an event; a comment is not an event ([devlog#16](https://github.com/novkostya/quince-devlog/issues/16)
rule 2, and 64 minutes of a held approvable PR); and `reviewDecision` does not move across a fix, so
no `review` fires either.

**Where §4's own reasoning went wrong, kept verbatim because the shape recurs:** the code's note read
*"SUCCESS is not an event. Red is what changes who must act."* That is true while a PR awaits its
**first** review and false the moment it awaits a **re**-review — after a changes-requested fix,
green is exactly what changes who must act. One signal, two meanings, decided by whose turn it is,
and the classifier has no notion of turns.

The fix is not to teach it turns. It is a **backstop that does not classify**:

- **any change to a PR's `updatedAt` emits `updated`** — unconditionally, and *alongside* whatever
  typed event also fired. A backstop that only fires when the classifier came up empty inherits the
  classifier's blind spots, which is the bug wearing a hat. Typed events stay, for classification.
- **the event names WHO, not only WHEN.** `updatedAt` is a timestamp; a session read one, assumed the
  latest activity was its own, and reported "nothing owed from me" with three items owed. Attribution
  uses only activity **strictly newer than the previous observation** — the only acts that could
  explain the move — and says `actor=unattributed` when nothing does, rather than naming a bystander.
  **`unattributed` is not a bug, and the commonest way to reach it is worth naming:** a commit is dated
  by `committedDate`, not by when it was pushed, so a commit held locally or an older commit
  cherry-picked forward sorts *older* than the previous observation. The event still fires — that is
  the part that matters — and degrades to `unattributed` rather than guessing. A label change, a title
  edit or a base change lands there too, appearing in no activity list at all.
- **volume, since an unconditional backstop invites the worry that it becomes an unactionable stream:**
  `updatedAt` does not move on check completion, which is the highest-volume signal in the system. It
  moves on comments, reviews, pushes, labels and merges — acts by a human or an agent, roughly one
  event per act. That is a stream a session can act on.
- **compare against last-acted-on state, not state-at-arm.** A hand-rolled mitigation during the
  incident baselined against *current* heads at arm time, so both pushes it existed to catch were
  already in its baseline. `step()` diffs against the stored observation, which is what makes a
  re-armed watch produce the events that accrued while nothing was watching (§4c).
- **mergeability is watched separately, because the backstop does not cover blind spot five.** A PR
  that goes `BEHIND` or `DIRTY` because something **else** merged has had nothing happen to it: its
  own `updatedAt` does not move, and no amount of watching that timestamp would ever show it. Under
  strict up-to-date protection the architect's own merges invalidate every other open PR this way.
  Reported on transition into a *known* non-clean state only: `UNKNOWN` is GitHub's "no answer yet",
  and reporting it would be reporting our own ignorance as the PR's condition. **The state therefore
  remembers the last *known* mergeability, not the last observed one** — the first version treated
  `UNKNOWN` as no-answer for reporting and then wrote it into state as though it were a state, so
  `BEHIND → UNKNOWN → BEHIND` re-announced a condition that never changed. Reachable rather than
  theoretical: the recompute that returns `UNKNOWN` is triggered by the base moving, and the base
  moving is what makes a PR `BEHIND`, so two merges into an open queue produce it. Overwriting
  knowledge with an absence is how a recompute flap becomes an event. The **policy** half —
  whether to adopt a merge queue so this stops happening — is [quince#46](https://github.com/novkostya/quince/issues/46)
  and is deliberately not decided here.

**Declared debt:** `stalled` (story 6) is specified and **not implemented** — it needs a wall clock,
which the pure half deliberately does not have. It was advertised in `--help` while unimplemented,
which is a tool making the kind of claim this tool exists to stop; it is off the advertised list until
it can be produced.

Therefore, mechanically:

- state initialises to a **sentinel**, so `never observed` ≠ `observed empty`;
- **queue-empty transitions are reported explicitly** (`queue empty` is an event, not the absence of
  one);
- the watch enumerates the queue **each tick** rather than binding a list at arm time;
- **check conclusions are watched beside reviews**, since a red PR is unreviewable and that is
  progress information;
- every emitted event carries the observation that produced it (`#18 gates:FAILURE`), never a bare
  "something changed".

## Design

`bin/forge-watch` is a **pure state machine plus a thin fetch**: `step(previous_state, observation)
→ (new_state, events)`. The fetch is `gh` output; the step function is what the fixtures test. That
split is the whole reason this can be tested at all without a forge.

```
forge-watch tick   --state <file>            # one poll; emits events on stdout
forge-watch step   --state <file> --observation <json>   # pure, no network — what the fixtures drive
forge-watch arm    --state <file> --watch pr:19 --watch queue:bot-open
```

Events are lines: `event=updated pr=19 at=2026-07-26T12:32:21Z actor=quince-bot kind=commit` (the
backstop), `event=review pr=19 verdict=CHANGES_REQUESTED`, `event=checks pr=18 conclusion=FAILURE
name=gates`, `event=mergeability pr=19 status=BEHIND`, `event=merged pr=19`, `event=queue-empty`,
`event=first-observation` (the sentinel discharging). `event=stalled` is specified by story 6 and not
yet implemented — see the declared debt in §4b.

On the runner, an OpenRC-supervised timer runs `tick` and pipes events to a dispatcher that starts **one fresh
session per event** — `/kickoff <pr>` for the implementer side, `/review-pr <n>` for the reviewer
side. In a laptop session the same binary runs in-session as a fallback, and says which mode it is
in, because a loop that dies with the lid must not look like a loop that is waiting.

**Pacing (cost):** the event monitor does the waking; fallback heartbeats are **20–30 min**, not
minutes. Idle ticks are cheap; wake-and-review cycles are not, and two chatty loops spend the weekly
allowance faster for no benefit.

## Stories

1. `forge-watch tick` on a queue that has never been observed emits `first-observation` and does not
   invent a change.
2. The queue going empty→non-empty emits (the **#17** defect — the architect's monitor emitted only
   when the previous state was non-empty), **and** a PR opened after arming is picked up on the next
   tick (the implementer's *latent* list-binding defect). Two different bugs, one fix: enumerate the
   queue every tick.
3. A check turning `FAILURE` emits an event carrying the check name (the **#16** defect — the
   implementer's monitor watched reviews only).
4. An empty queue emits `queue-empty` once, not silence, and re-emits when it becomes non-empty.
5. A review verdict emits with the verdict, and a fresh session started from it can continue from
   the PR alone.
6. A PR untouched for `stall_hours` emits `stalled` with what it was waiting for, and the loop stops.
7. A gap-protocol trigger stops the loop and leaves a PR comment naming what needs ruling.
8. `/kickoff --loop` and `/review-pr --loop` arm the watch and say so; without `--loop` both behave
   exactly as today.
9. A changes-requested PR whose only change is a push that goes green emits an event (quince#43).
10. A PR whose only change is a new comment emits an event (devlog#16).
11. An update that nothing in the payload explains emits `actor=unattributed`, never a bystander.
12. A PR made `BEHIND` by someone else's merge emits `mergeability`, while its own `updatedAt` — and
    therefore the backstop — stays correctly silent.

## Gates

- **G1 (fixtures, no network)** — `forge-watch replay bin/testdata/forge/*.json` covers stories 1–4
  and 9–12. Story 6 is **not covered because it is not implemented** (§4b). Each fixture is a pair of
  consecutive observations; the recorded ones are named for the PRs that produced them
  (`pr17-…`, `pr16-…`, `pr36-…`, `pr37-…`) and the illustrative ones are numbered 100+ to say so.
  G1 also requires the fixtures to have teeth: replayed against the previous `forge-watch`, every
  fixture added for a newly-fixed blind spot must FAIL.
- **G2 (live, one PR)** — arm on a real PR, push a commit, observe the `checks` event; post a
  review, observe the `review` event with its verdict.
- **G3 (the coroutine, end to end)** — an implementer loop and a reviewer loop run against one
  scratch PR: review posted → implementer session wakes cold → fix pushed → reviewer session wakes →
  approves. **Nobody types "review posted".**
- **G4 (stop, don't guess)** — a review comment requiring a ruling stops the implementer loop with a
  PR comment naming the question; no commit is made.
- **G5 (watchdog)** — with `stall_hours` set to minutes, an idle PR produces `stalled` and both
  loops stop.
- `make gates-sh` covers the new script.

## Fixtures

`bin/testdata/forge/` — recorded `gh` JSON observations, hand-trimmed and site-neutral (PR numbers
and check names only; no URLs, no hosts). Two are regressions from this session's own monitors,
which is the issue's "fixture, not folklore" requirement taken literally.

## Rule check

- **State honesty** — the entire rung is about a monitor whose silence lied; every event carries its
  observation, and `never observed`, `observed empty` and `observed unchanged` are three distinct
  states. The program doc's *an error message is a claim* is the governing rule here.
- **No silent fallbacks** — in-session mode announces that it dies with the session; a stopped loop
  says why on the PR.
- **Escalation over improvisation** — the gap protocol is the stop condition, mechanically.
- **Privacy** — fixtures carry PR numbers and check names, nothing site-shaped; the loop never
  echoes an address into a PR comment.
- **Secrets** — the watcher uses `bin/gh-bot`; no token in argv, none in state files.
- **Boundary** — no product code, no contracts, no workflow files. Review *semantics* are untouched;
  only the trigger is new.
- **Approver ≠ author** — the loop wakes sessions; it never approves or merges. `/land` stays
  deliberate and human-triggered.
- **Cost** — pacing is specified rather than left to a default, because the failure mode is a
  quietly expensive loop.

## Sequencing — RULED: pr.5 first, then all of this

The first draft proposed splitting `forge-watch` out as landable-now, since it needs no runner.
**Ruled otherwise (architect), and on a better axis than technical merit: critical path.** pr.5 is
the only remaining item that needs the *Operator* — five minutes of `claude /login` on the runner —
so every hour it waits is an hour nobody can schedule it, while `forge-watch` blocks nobody. With
parallel sessions the split would have been right; with one implementer it is not.

So: **pr.5 next, this rung after it, whole.**

What is captured now, because it is the one thing that decays: the **fixtures**
(`bin/testdata/forge/`). Reconstructing "#16 sat red for an hour" from a journal entry in a week is
exactly the folklore this rung exists to prevent, so the observations are recorded while they are
still exact — including which monitor produced which defect, and which defect was never actually
observed to swallow anything.

## PR sequence

1. **this spec + the fixtures** — the four design notes settled, and the evidence captured before it
   decays.
2. **`forge-watch`** — the pure `step()` and its `tick` wrapper; G1 replays the fixtures. **After
   pr.5.**
3. **loop modes + runner supervision** — skills and the OpenRC service, G2–G5. **After pr.5.**
