# rung-loop — a rung that runs without a human carrying "review posted"

> Status: **SPEC — ruled on first review (sequencing + attribution), awaiting re-review.** No code exists beyond fixtures. Takes
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

Events are lines: `event=review pr=19 verdict=CHANGES_REQUESTED`, `event=checks pr=18
conclusion=FAILURE name=gates`, `event=merged pr=19`, `event=queue-empty`, `event=stalled pr=19
since=3h`, `event=first-observation` (the sentinel discharging).

On the runner, a systemd timer runs `tick` and pipes events to a dispatcher that starts **one fresh
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

## Gates

- **G1 (fixtures, no network)** — `forge-watch step` replayed over `bin/testdata/forge/*.json`
  covers stories 1–4 and 6. Each fixture is a recorded pair of consecutive observations; the two
  regression fixtures are named for the PRs that produced them (`pr17-opened-after-arm.json`,
  `pr16-red-unreviewed.json`).
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
3. **loop modes + runner supervision** — skills and the systemd unit, G2–G5. **After pr.5.**
