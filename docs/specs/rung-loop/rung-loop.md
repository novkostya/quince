# rung-loop — a rung that runs without a human carrying "review posted"

> Status: **PARTLY BUILT.** `bin/forge-watch` exists — the pure `step()`, `tick`, `replay`, the
> complete event model of §4b (quince#43), restart safety and `stop` (§4c, quince#49/#56), the
> declared watch set (§4d, quince#51), and the terminating `watch` loop (§4e, quince#62). What
> remains: **arming that a session cannot silently omit** — the other half of quince#62, since a verb
> that terminates correctly does nothing for a session that never runs it — the runner supervision
> unit, and story 6 (`stalled`). Takes
> [devlog#4](https://github.com/novkostya/quince-devlog/issues/4); feature-named because the
> deliverable outlives the issue. **Implementation is gated on pr.5 (runner host)** — see
> *Sequencing*; this PR settles the design the issue asked to settle before building.

## Goal

An implementer session and a reviewer session behave as coroutines over the forge: each arms a
watch, sleeps, and is resumed by a **fresh session** when its counterpart acts — so a rung advances
without a human relaying "review posted" across the gap, and stops loudly the moment it needs a
ruling.

## Boundary

**In scope:** `bin/forge-watch` (the event source and its state machine), `.claude/forge-set` (the
declared watch set), loop modes in `.claude/skills/kickoff/SKILL.md` and
`.claude/skills/review-pr/SKILL.md`, the runner-side supervision unit, `docs/specs/rung-loop/`,
fixtures under `bin/testdata/forge/`.

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
  **`unattributed` is not a bug, and its commonest cause in this project is mundane: a checkbox tick.**
  Every PR here carries a checklist, and authors tick the CI and privacy boxes as they land. A body edit
  moves `updatedAt` and appears in **no** activity list — it produces no timeline event whatsoever — so
  the event fires and the actor is honestly withheld. Meet that as the normal case, not an exotic one.
  The other routes: a title or base change (same channel), and a commit dated by `committedDate` rather
  than by push time, so a commit held locally or an older commit cherry-picked forward sorts *older*
  than the previous observation. In every case the event still fires, which is the part that matters,
  and degrades to `unattributed` rather than guessing.
- **volume, since an unconditional backstop invites the worry that it becomes an unactionable stream:**
  `updatedAt` does not move on check completion, which is the highest-volume signal in the system. It
  moves on comments, reviews, pushes, labels and merges — acts by a human or an agent, roughly one
  event per act.

**The one measurement this rests on, and how it was nearly botched twice.** *A push moves `updatedAt`*
is load-bearing: it is what carries the green-after-fix transition. It is also unobservable after the
fact, because a merged PR's `updatedAt` is pinned to its merge. Both first attempts to measure it were
confounded by the author commenting ~40 s after pushing, and one of them — a watcher armed specifically
to measure it independently — printed a confident *"updatedAt MOVED with the push"* while its own
caveat about checking for a comment in the same window sat on the line below. That is corollary (f)
committed by the instrument built to check it: a timestamp moved and the tool named the actor it
expected rather than the one that explains it.

It is now measured — **twice, in two repositories, by two methods, by two parties**:

| Where | Method | Reading |
| --- | --- | --- |
| quince-devlog#20 | elimination — that repo has **zero check runs in its entire history** | `updatedAt` `16:01:31Z`, 3 s after the push's commit (`16:01:28Z`); previous act a comment at `15:56:13Z`, next at `16:04:23Z` |
| quince#48 | timing — push, then two minutes of deliberate silence, polled at 10 s | `16:05:31Z → 16:08:32Z`, 3 s after the commit (`16:08:29Z`), no comment or review in the window |

**And the channel that would have invalidated both, closed:** a PR **body or title edit moves
`updatedAt` and produces no timeline event at all**, so neither elimination nor a timing window would
notice one. Checked via `userContentEdits` on both PRs; every edit falls outside the respective window.
So the protocol is: **push, stay silent two minutes, poll at ≤15 s, and confirm no body edit in the
window.**

**A second measurement fell out of that silence, and it upgrades the volume argument above from
reasoning to observation.** During the 110 s of silence on quince#48, three check runs were created and
started (`gates` and `image` at `16:08:53Z`, `e2e` at `16:08:54Z`) and `updatedAt` **did not move** — it
held at `16:08:32Z` throughout. Check-run creation and progress do not touch it, which is what keeps an
unconditional backstop down to roughly one event per human or agent act. That is a stream a session can act on.
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

### 4c. Restart safety, and the check that could only pass

The watch died once by having its cadence live one layer above the tool that enforced the discipline:
a host-client reconnect restarted the session process, the pending wakeup went with it, and 44 minutes
passed with three PRs unwatched while the watch reported healthy (devlog#13).

The first draft of the fix said: *detect a `forge-watch` state file for this unit with no watcher
running, and re-arm from it.* That check **could only ever report success**, because the state lived at
`/tmp/…/<session-id>/scratchpad/forge-watch/…` — a session-scoped path. The failure being defended
against produces a *new* session with a *new* scratchpad, so the re-arm step would look in an empty
room and find nothing to re-arm. Same defect class as the incident: a check that cannot fail is not a
check.

Therefore:

- **state lives at a session-independent path** — `$FORGE_STATE_DIR`, else
  `$XDG_STATE_HOME/quince/forge-watch`, else `~/.local/state/quince/forge-watch`, one file per repo.
  Session scratchpads are for artefacts nobody needs tomorrow; watch state is the opposite of that.
  **No hostname is recorded in it**, deliberately: the file is per-box already, and a hostname is an
  Operator-private fact that would then live in a file somebody pastes into an issue.
- **`forge-watch status` distinguishes FOUR cases and says which**, with exit codes so a caller that
  ignores stdout still cannot mistake one for another: `live` (0) → nothing to do; `dead` (3) → nothing
  is running, **re-arm from this state and do not reseed it**; `absent` (4) → cold start, seed the
  sentinel; `wedged` (5) → a process **is** still running and has stopped ticking, so **`forge-watch
  stop` it** — never a bare `kill` — then re-arm.

  **`stop` is a verb rather than a sentence, and that is the finding rather than a preference.** A pid
  is only known to be *our* watcher while its heartbeat is fresh, and `wedged` is *defined* by that
  heartbeat being stale: the one state in which this tool issues an imperative to signal a process is
  the one state where its identity is unproven. Demonstrated in review by writing a foreign pid into the
  state — the tool answered *"pid 1 IS STILL RUNNING … STOP IT FIRST"*, **telling the reader to kill
  init.** So the watcher records its process start time beside its pid (`/proc/<pid>/stat` field 22,
  parsed after the last `)` since `comm` is parenthesised and may contain spaces), and `stop` re-reads
  it at the moment of the signal: a recycled pid necessarily started later, which proves "not ours"
  without needing to identify what it is instead. **Every branch that cannot prove the identity refuses
  and says which** — no recorded start time, no readable procfs, or a mismatch. `kill` returning success
  proves a process died, not that the right one did (corollary (g), pointed outside the repo).

  It is a verb and not two steps joined by prose for the reason this whole unit exists: a session
  following *"stop that pid, then re-arm"* literally, on a box where the pid had been recycled, had no
  defence at all.

  **And the check states its own limit, because one that did not would be the same defect wearing a
  fix's clothes.** Verify-then-signal is two syscalls with a gap: if the watcher exits after the
  start-time check and the kernel reuses its pid before the `kill`, the signal still lands on a
  bystander. That race cannot be closed from userspace. What the verb buys is the *size* of the window,
  and the difference is not marginal — the old arrangement's window was however long a session takes to
  read a sentence and act, seconds to minutes, and the review demonstrated a session using exactly that
  window to kill init. **Unbounded to syscall-scale, in one place, behind a refusal.**

  Collapsing `dead` into `absent` is how a restarted watch silently becomes a fresh one that has "seen
  nothing changed" since a beginning it invented. **`wedged` was the fourth case, and it was a review
  finding against this design's own stated principle** — it originally shared `dead`'s exit code and its
  identical *"re-arm from this state"* note, while needing the opposite remedy: "wedged" means a process
  is still running by definition, so re-arming beside it puts two watchers on one state file. The
  duplicate was therefore not an unlucky race but **the designed path, reached by doing what the tool
  said**. Corollary (b) — don't collapse two conditions into one message — one level up from where it
  usually bites.
- **liveness needs two instruments, and neither is sufficient alone.** The heartbeat cannot see a
  watcher that died a moment ago — its last tick is recent by definition, so a fresh session would read
  `live`, do nothing, and nothing would ever tick again. The pid cannot see a *wedged* watcher, and
  cannot identify one either: the first version confirmed the pid by grepping `/proc/<pid>/cmdline` for
  `forge-watch` and reported success for the shell that had just **run** forge-watch. A check whose
  positive answer can be produced by the act of asking is not a check. So `live` requires the pid to
  exist **and** the heartbeat to be fresh; the grep is gone, since pid reuse with a fresh heartbeat is
  impossible.
- **the watcher's cadence is recorded separately from any tick.** One field serving both would let a
  hand-run tick refresh the very heartbeat a supervisor consults, and a dead watch would look alive for
  as long as somebody kept poking it.
- **a tick that was due and did not happen emits `tick-overdue`.** The events themselves cannot carry
  that fact: they arrive looking perfectly healthy, all at once, hours late. Reported only when a whole
  interval was skipped, so ordinary jitter is not dressed up as a miss.
- **A failing tick does not advance the heartbeat, so the two halves compose in the safe direction.**
  The heartbeat is written after `step()`, and a failed fetch returns before reaching it. The
  consequence is the part worth stating, because it is not visible from either half alone: **a watcher
  that is running but cannot tick can never present as `live`.** It ages into `wedged` — its pid is
  alive, its heartbeat is stale — or into `dead` if that pid has since gone, and both say so loudly.
  Verified by driving three consecutive failing ticks against a seeded state and watching
  `last_watcher_tick` stay put.
- **`--state` is for fixtures and second opinions; the default path is the operational one.** This is a
  rule about arming rather than a better check, because no check can close it: **liveness is only
  discoverable through whichever state file you happen to point at**, so a watcher armed under a
  session-scoped `--state` and one armed under the default path cannot see each other. Neither is
  lying — both are correct by their own lights. Concretely, at the launchpad pull that makes this
  protocol live: **stop any watcher armed under a session-scoped `--state` before arming under the
  default path**, or the first session after the restart will read `absent`, correctly, and arm a
  second watcher beside a running one.
- **Nothing yet stops two watchers ticking one state file, and `dead` is a judgement rather than a
  fact** — a watcher whose fetch hangs past `interval × FORGE_STALE_TICKS` is alive and reported dead,
  and `status` then tells the reader to start another. Direction of failure is duplicate or lost events,
  not a corrupted tree. Named here rather than left to be rediscovered; the fix, and the parts of it
  that need a ruling rather than a patch, are
  [quince#50](https://github.com/novkostya/quince/issues/50).

### 4d. The watch set is declared, never defaulted

*"Watch both repos, every time"* was the instruction for a day, and it was wrong-in-waiting: it goes
stale the moment a third repository matters, and a stale watch set produces the failure this project
keeps paying for — devlog#3, a watch that reported both queues clear while covering one of them.

- **The set is a versioned file, `.claude/forge-set`** — one `owner/name` per line, `#` comments. Adding
  a repository is a PR against it, which is the point: the set becomes reviewable, and a session that
  would otherwise have quietly watched less has to be told.
- **Missing, empty, or malformed is a HARD FAILURE**, never a fallback to one repository. Falling back
  would reproduce the historic bug and reproduce it silently, which is worse than not running. A
  malformed line fails too rather than being skipped — a silently smaller set is the same lie one level
  down.
- **Under `--all`, every event carries `repo=`.** PR numbers collide across repositories by
  construction, so `event=opened pr=20` from a two-repo watch is ambiguous by design rather than by bad
  luck. `status --all` reports the **worst** class across the set — wedged, then dead, then absent,
  then live — not the first or last: a session that
  re-armed one repo and left another dead would otherwise read the reassuring line and stop.
- **Only the architect side needs the file.** The implementer watches the PRs it opened, recording repo
  and number as it opens them, so its set is self-describing. The architect reviews other people's
  work, so authorship cannot derive its set — and a reviewer's watch set is exactly the thing that must
  not be a habit.
- **The effective watch set is the checkout's copy, so pulling the launchpad is part of arming the
  watch, not separate housekeeping.** The hard-fail catches *missing*, *empty* and *malformed* — and a
  **stale** set fails none of those: it parses, it is non-empty, and it confidently describes
  yesterday's world. Observed rather than anticipated: on the architect box, `/root/quince` sat at a
  commit where `.claude/forge-set` did not exist at all, on the machine whose watch would read it. The
  two failure modes compose — a stale launchpad plus a declared set yields a watch that is confidently
  wrong about its own scope, and neither half looks broken alone. The ordering belongs to the ceremony
  ([quince#33](https://github.com/novkostya/quince/issues/33)), not to a freshness check inside this
  tool: such a check would be a claim about the *repository* wearing the costume of a claim about the
  *watch*, passing on a pulled-but-unmerged branch and failing on a deliberate pin.

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

### 4e. The loop must END, and that is why it is a verb (quince#62)

Everything above concerns what the watch *sees*. This is about how what it sees reaches a session,
and it was wrong in a way none of the instruments above could show.

The first architect session under the rewritten protocol armed the loop the skills described:

```sh
sh -c 'while :; do bin/forge-watch tick --all …; sleep 60; done'
```

**No exit condition.** A session is woken by a background task *completing*, so a loop that never
completes can never deliver anything it detects. quince#61 opened at `19:07:16Z`; the session's last
activity was `18:21:38Z`, and it was still asleep fifty minutes later when a human intervened. Every
liveness signal was green throughout — heartbeat fresh, both state files rewritten every 60 s,
`status --all` reporting `live`. The watch was running perfectly and delivering nothing: corollary
(g) — *a check whose positive answer can be produced without the thing being true* — occurring inside
the mechanism built to eliminate it.

**What it degraded to is the interesting part.** Not silence: the session still woke on whatever else
could reach it, so the loop fell back to the twenty-minute poll this rung existed to replace. The
predecessor mechanism, wearing the new one's clothes, with every gauge reading nominal.

Therefore:

- **`forge-watch watch` owns the loop**, and the skills say *run this*. Documenting the requirement is
  necessary and not sufficient — the next session reads the same text and writes `while :; do … done`
  again, because that is what a watching loop looks like everywhere else. The property is stated in
  the tool's own `--help` and in both skills for a reader who hand-rolls anyway: **the loop must exit
  when it finds something; a loop that cannot exit cannot wake you.** It runs as a *background* task;
  in the foreground it blocks the session it exists to wake.
- **The filter decides what WAKES, never what is SEEN.** Every tick's output is printed. Two classes of
  line do not end the loop: the **baseline** — `first-observation`, and the `queue-empty` printed
  beside it, because the observation that establishes the baseline is not a change from it and waking
  on it would make arming a watch a busy circle of arm-exit-arm — and **`fetch-failed` until
  `--fail-after` of them in a row**, since one failed fetch is a missed tick while a run of them is a
  watch that is not watching. Neither is swallowed; both are printed, and the second exits with its
  own class. **`tick-overdue` on the first tick does not wake either** — found by arming the verb for
  real rather than by reasoning about it: re-arming from a `dead` watch always produces one, since the
  previous watcher stopped ticking and its `due` is in the past by definition, so waking on it would
  make every re-arm exit immediately and report as news the gap that the arming step announced one
  line earlier. It describes the watch that *ended*. A later one — the loop starved while running — is
  a different claim and still wakes.
- **`--max-wait` (default 1200 s) makes termination the heartbeat.** Once detection is the *normal*
  exit, every termination is a window covered only by the fallback — and the fallback was measured
  during this incident at **three armings, zero deliveries** (`ScheduleWakeup`, architect box,
  2026-07-26: due `18:41`, `19:40`, `20:03`; each time the session was idle at the due moment, was not
  invoked, and was next woken minutes later by a different mechanism). So the loop provides its own
  floor through the channel that *is* measured to work — 14/14 deliveries within ~60 s over the same
  window. The skills still arm the `ScheduleWakeup` fallback: it is belt to this braces, and neither is
  treated as cover for the other.
- **The four answers of `status` are enforced at arming.** `watch` refuses to arm beside a `live` watch
  and refuses to arm beside a `wedged` one (naming `stop`), and it announces `dead` — re-arming without
  reseeding — or `absent`. The four-case discipline of §4c stops being a paragraph a session has to
  remember at the exact moment it is arming something.
- **Self-caused wakes are NOT suppressed, and that is a decision rather than an omission.** A
  terminating watcher wakes its session on the session's own acts — measured at 5 of 14 wakes (~36%)
  on the architect box: its own approvals, its own comment, its own changes-requested. Suppressing by
  actor would be a fresh claim about what cannot matter, which is the §4b defect exactly, and
  `unattributed` is common enough (a checklist tick) that such a rule would land on honest uncertainty.
  The event carries `actor=`; the woken session reads it.

**Its fixture asserts termination, and deliberately asserts nothing about health.** Every health check
in this directory was green while the deaf watcher ran, so a health fixture cannot tell the two apart.
The `"kind": "loop"` fixtures drive the real verb against a stub `gh` and assert the two questions that
do: silence leaves the loop running to a declared idle bound, and an event ends it. Teeth, per G1:
replayed against the shipped hand-rolled shape the positive fixture does not *fail*, it **hangs** —
which is the defect stated precisely — so the harness bounds every loop fixture with `timeout`, and
says out loud when `timeout` is absent rather than running unguarded in silence.

### 4f. Arming cannot be a thing a session is merely told to do (quince#62, second half)

§4e fixes a loop that was built wrong. An hour after that failure, the implementer half produced a
different one: **a session that built no loop at all.** No watcher, no state file, no `ScheduleWakeup`
— checked structurally against the transcript, not guessed. It ended a turn with *"the ball is back
with the reviewer"* at `19:41:56Z`; the verdict it was waiting for landed at roughly `19:46Z`, and
nothing anywhere could have told it. The architect at least had a fallback firing into the void; this
session had no mechanism of any kind and would have slept until a human typed into it.

**Both stops were named as illegitimate by the skill the session was following**, in a section
rewritten specifically to prevent the previous run's *"over to the architect"*. Rewriting the default
from *stop* to *proceed* did not stop a session from finding a new sentence for stopping. That is the
pattern, stated generally: **a rule that tells a session to do something is satisfied by a session that
does not do it, and nothing observes the difference** — corollary (g) applied to arming rather than to
checking.

**Three shapes were available. The other two were rejected on structure, not on taste.**

- **"Opening a PR arms the watch as a side effect"** *cannot work*, and fails in exactly the shape of
  §4e. A session is woken when a background task **the session itself launched** completes; a process
  forked by a `gh pr create` wrapper is tracked by nothing, so its exit notifies nobody. It would be
  armed, ticking, `status=live`, and deaf — the bug wearing the fix's clothes.
- **"A channel that does not depend on the recipient having armed anything"** is correct, and it
  already exists in this document: the runner dispatcher in *Design*, a supervised `tick` starting
  **one fresh session per event**. It needs the runner supervision unit, so it is named here rather
  than built. It is the eventual answer; it is not available today.

What remains is to make the **absence detectable**, aimed at the only actor who can fix it. So
`forge-watch owed` answers *are there open PRs here with no live watch*, and the session's harness runs
it when a turn ends (a `Stop` hook in `.claude/settings.json`). The mechanism is the harness's, not the
model's, which is the entire point: the model cannot decline to run it by reasoning its way past a
paragraph.

- **The two halves owe over different sets**, exactly as `.claude/forge-set`'s own header says.
  `--author @me` asks the *forge* which PRs this login has open — not the declared set, because a PR in
  an undeclared repository still obligates whoever opened it. `--all` is the declared set, since a
  reviewer's obligation cannot be derived from authorship. Neither is defaulted; the role is chosen by
  **which credential the box holds**, which is how the two boxes already tell themselves apart, and it
  is printed rather than assumed.
- **It blocks ONCE, then tells the human.** A `Stop` hook that always blocks is a session that can
  never end. The second attempt is allowed and emits a warning to the *user* instead. The goal was
  never to make stopping impossible — only to make an unwatched stop impossible to do **silently**,
  which is the whole of "make the absence detectable". No opt-out file, no waiver flag: an escape
  hatch that can be set once and forgotten is the omission again, with a name.
- **It fails OPEN, loudly.** No credential, or a forge that will not answer, produces a warning saying
  the question was **not checked** — never silence, and never a block. A hook that blocked on its own
  breakage would wedge every session in the repository; a hook that returned silence would be
  committing the substitution it exists to prevent. *"I could not look"* and *"nothing is owed"* must
  not print the same, which is this project's founding rule pointed at its newest instrument.
- **The predicate is split pure/impure like everything else here.** `owed_decide` takes a table of
  *(repo, watch class)* and returns the report and the exit class, with no forge, no process table and
  no clock — so it is fixtured. `dead` and `wedged` produce **different instructions**, because they
  need opposite remedies (§4c) and one message for two situations is the same defect one level up.
- **The arming command it prints is role-shaped, and the first version was not.** It printed the
  implementer's `--repo` form to both halves, so an architect copying it — and the hint exists to be
  copied, which is the entire reason it is spelled out — would have armed a watch **smaller than its
  declared set**, the failure `.claude/forge-set` was built to prevent. The resulting state then
  *satisfies* this gate, which is a check passed by obeying its own remedy. Found by running the
  architect leg on an architect box, and fixtured, because a hint that must be copied verbatim is a
  claim like any other.

**Verified end to end, because the mechanism is a claim about the harness and not about our code.**
A `Stop` hook in project `.claude/settings.json` was probed in a scratch project and then for real:
it fires in a **headless `claude -p` run in a never-trusted workspace**, `stop_hook_active` is `false`
on the first stop and `true` on the second (which is what bounds the block to once), and exit 2 /
`decision: "block"` prevents the stop and delivers the reason into the model's context. The real hook
was then run against this repository with `FORGE_STATE_DIR` pointed at an empty directory: a session
instructed *"reply with the single word PING and do not use any tools"* **tried to arm the watch
instead**, quoting the exact command. With the watch live it answered `PING` and did not block.

**Limits, stated because a claim without them is the thing this document keeps being rewritten for.**

- It blocks the *model's* stop; it cannot force a correct arming. What it removes is silence.
- **Hooks are not gated by the workspace trust dialog, and permission entries are** — observed in the
  same run (`Ignoring 73 permissions.allow entries … this workspace has not been trusted`) while the
  hook ran anyway. Desirable here, since enforcement that a session can skip by not trusting the
  workspace is not enforcement; worth stating plainly, because it means cloning this repository runs
  this command at the end of every turn.
- It costs one forge call per turn end — measured at ~1.1 s for the `--author` query — and the hook is
  given a 30 s timeout so a hanging forge cannot hold a session open.

## Design

`bin/forge-watch` is a **pure state machine plus a thin fetch**: `step(previous_state, observation)
→ (new_state, events)`. The fetch is `gh` output; the step function is what the fixtures test. That
split is the whole reason this can be tested at all without a forge.

```
forge-watch watch  --repo owner/name | --all # THE LOOP: tick until there is something to say, then EXIT
forge-watch tick   --state <file>            # one poll; emits events on stdout
forge-watch step   --state <file> --observation <json>   # pure, no network — what the fixtures drive
forge-watch status --repo owner/name | --all # live | dead | absent | wedged, and what to do about it
forge-watch stop   --repo owner/name         # verify the recorded pid is still ours, then signal it
```

Events are lines: `event=updated pr=19 at=2026-07-26T12:32:21Z actor=quince-bot kind=commit` (the
backstop), `event=review pr=19 verdict=CHANGES_REQUESTED`, `event=checks pr=18 conclusion=FAILURE
name=gates`, `event=mergeability pr=19 status=BEHIND`, `event=merged pr=19`, `event=queue-empty`,
`event=first-observation` (the sentinel discharging), `event=watch-idle` / `event=watch-failing` (the
loop's own two exits, §4e). `event=stalled` is specified by story 6 and not yet implemented — see the
declared debt in §4b.

*(The earlier draft of this block listed a `forge-watch arm --watch pr:19` verb. Nothing of the sort
was ever built — the watch enumerates the queue every tick precisely so that nothing is bound at arm
time — and a design doc advertising a verb that does not exist is the claim rule pointed at ourselves.
Corrected here rather than left for a reader to try.)*

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
13. `status` reports `live`, `dead`, `absent` or `wedged`, never two of them collapsed, and says what
    to do next — including that a wedged watcher must be stopped before re-arming.
14. A watch whose state exists with no watcher behind it is re-armed from that state, and the next tick
    emits everything that accrued while nothing was watching — not `queue-empty`, not silence.
15. A watcher process that is alive but no longer ticking reads `wedged` — not `live`, and not `dead`
    either, because the remedy differs.
16. A hand-run tick cannot make a dead watcher look alive.
17. A tick that was due and did not happen emits `tick-overdue`, with how late it was.
17b. `stop` signals the recorded pid only when its process start time still matches; a recycled pid,
    an unrecorded start time or an unreadable procfs each produce a refusal naming which, never a kill.
18. A missing, empty or malformed watch-set declaration hard-fails; nothing falls back to one
    repository.
19. Under `--all` every event names its repository, and `status --all` reports the worst class in
    the set.
20. A watch that finds something **exits**, so the session that armed it is woken by the completion —
    the loop is the tool's own verb, and the events are on its stdout when it ends (quince#62).
21. A watch that finds nothing keeps watching, and ends at a **declared** idle bound with
    `watch-idle` — not silently, and not by running forever.
22. Arming a watch beside a `live` one is refused, and beside a `wedged` one is refused with `stop`
    named; `dead` and `absent` proceed and say which they were.
23. A session that tries to end a turn with an open PR it authored and no live watch is **stopped and
    told, with the exact command** — once. The second attempt is allowed and warns the human instead
    (quince#62).
24. A live watch owes nothing and **says so**; a gate whose satisfied answer is silence cannot be seen
    to have run.
25. When the check cannot be made — no credential, forge unreachable — it says the question was not
    checked, and neither blocks nor reports "nothing owed".

## Gates

- **G1 (fixtures, no network)** — `forge-watch replay bin/testdata/forge/*.json` covers stories 1–4,
  9–17 and **20–21** (the `"kind": "loop"` fixtures, which drive the real `watch` verb against a stub
  `gh` — no network, but a subprocess and a clock, so they are the one impure shape in the harness).
  Story **22 is a CLI smoke recorded in the PR**: the refusals are argument-time behaviour, and a
  fixture that seeded a live pid would be asserting the harness rather than the tool. Stories **23–25**
  split: the decision half is `"kind": "owed"` fixtures, and the **hook half is proven by running a
  real headless session** (§4f) — it is a claim about the harness's behaviour, and no fixture of ours
  can make it.
  Stories **18–19 are proven by a CLI smoke recorded in the PR, not by the replay harness** —
  they are argument handling rather than state transitions, and saying so is better than letting a
  reader assume the fixtures cover them. Story 6 is **not covered because it is not implemented**
  (§4b). Each fixture is a pair of
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
