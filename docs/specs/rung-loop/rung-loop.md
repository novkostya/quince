# rung-loop — a rung that runs without a human carrying "review posted"

> Status: **SPEC — proposed, not approved.** No code exists. Takes
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

Each loop tracks the PR's `updatedAt`. No movement for `stall_hours` (default **3**) → post
`rung stalled at PR #<n>` with what it was waiting for, notify, and stop. A loop that cannot say
why it is still waiting has nothing to wait for.

### 4. The monitor bug becomes a fixture, not folklore

**Origin, first-hand:** tonight's in-session poller enumerated the PR list at arm time and compared
against a baseline captured then. A PR opened *after* the arm — or the first one after the queue
emptied — was invisible to it, which is how **#17 sat unnoticed**. Its sibling defect: a watcher
that observed reviews but not checks let **#16 sit red for an hour**, because "no review yet" and
"unreviewable, CI is red" printed the same silence.

Both are the class this project now has canon for (*an error message is a claim* — program doc): the
poller's silence asserted "nothing happened" while meaning "I never looked".

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
2. A PR opened *after* arming is picked up on the next tick (the #17 defect).
3. A check turning `FAILURE` emits an event carrying the check name (the #16 defect).
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

## Sequencing — why this is a spec now and code later

**Implementation waits for pr.5 (runner host).** A loop in a laptop session dies with the lid;
supervised on the runner it is durable and the monitor is a systemd unit rather than an in-session
poller. Building it before the runner means building it twice, and the second build would throw away
the first.

What lands now is the design, and one thing that needs no runner at all: the **fixtures**, which can
be recorded from this session's real failures while they are fresh.

## PR sequence

1. **this spec** — the four design notes settled before code.
2. **`forge-watch` + fixtures** — the state machine and G1. Can land before pr.5; it is a pure
   function with a `tick` wrapper.
3. **loop modes + runner supervision** — skills and the systemd unit, G2–G5. **Gated on pr.5.**
