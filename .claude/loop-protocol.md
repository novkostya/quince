# The coroutine loop — both halves

Normative for `/architect` and `/kickoff`. Each skill inlines the commands it needs, so a session that
never opens this file still does the right thing; what lives here is the **why**, which is what stops
the next session from redesigning the loop from scratch.

Every rule below was earned by a specific failure on 2026-07-25/26. Where a rule looks pedantic, that
is the shape of the failure it prevents.

## Step 0 — the watch may already exist, and it may already be dead

**Before anything else**, ask whether a watch is running for this unit. State on disk survives the
process restart; the loop must be rebuilt from it, never assumed:

```sh
bin/forge-watch status --all                # architect: the declared set
bin/forge-watch status --repo <owner/name>  # implementer: a repo you have PRs in
```

Four answers, four different actions, and **say out loud which one you got**:

| Answer | Exit | What you do |
| --- | --- | --- |
| `watch=live` | 0 | nothing. Do not arm a second watcher — two watchers writing one state file is a race that presents as missing events. |
| `watch=dead` | 3 | nothing is running. **Re-arm from that state, and do not reseed it.** The next tick diffs against the stored observation and emits everything that accrued while nothing was watching. Report that a watch was found dead, and why (`no_process`, `no_watcher_record`). |
| `watch=absent` | 4 | cold start. Seed and tick; the first tick emits `first-observation`. |
| `watch=wedged` | 5 | a watcher is still running and has stopped ticking. Run **`bin/forge-watch stop --repo <r>`**, then re-arm — **never a bare `kill`**. Nothing in the tool prevents two watchers on one state file (quince#50). |

**Why `stop` and not `kill`.** The pid is only known to be *our* watcher while its heartbeat is fresh,
and `wedged` is *defined* by that heartbeat being stale — so "the pid exists" and "the pid is ours" come
apart in exactly the state where the tool asks you to signal it. On a box where the kernel has recycled
that pid, a bare kill hits a bystander. Demonstrated in review: with a foreign pid in the state, an
earlier version told the reader **to kill init**, in the imperative, with a plausible explanation
attached. `stop` records the process start time at arming and re-checks it at the moment of the signal;
every branch that cannot prove the identity refuses instead of guessing.

**Why these must never collapse.** Reseeding a *dead* watch turns it into a fresh one that has "seen
nothing changed" since a beginning it invented — the accrued events are silently swallowed at the exact
moment they matter most. And treating *wedged* as *dead* hands you the wrong instruction with a straight
face: "wedged" means a process is still running, by definition, so "re-arm from this state" starts a
second watcher beside a live one. The duplicate is then not an unlucky race, it is the designed path,
reached by doing what the tool said.

`bin/forge-watch watch` runs this same check before it arms and **refuses** on `live` and on `wedged`,
so the two answers that must not lead to a second watcher no longer depend on the paragraph above being
read. Ask anyway, and say which answer you got: the tool can refuse to arm, and it cannot report on your
behalf that a watch was found dead.

Both were found the same way, and neither by reasoning. The first version of this check looked for state
in the *session scratchpad*, which the very failure it defended against destroys, so it could only ever
report `absent`; the second gave `wedged` and `dead` one exit code and one note. **A check that cannot
fail is not a check, and one instruction for two situations is the same defect one level up.**

## The mechanism — a watcher process, not a scheduled wakeup

The properties are not enough; sessions reach for a poll when the mechanism is left unnamed, and one
did: `/loop` plus a 1200 s `ScheduleWakeup`, giving up to twenty minutes of latency per event and a
session that shows busy on every tick. Worse, a client reconnect restarted the session process, the
pending wakeup died with it, and the watch reported healthy for 44 minutes.

**And the command is not enough either, if it is run at the wrong moment.** Both skills said a session
must arm a watch and neither said **when in the turn**. The natural reading — arm once you know you
need one, which is right after handling the events that woke you — is the broken one, and it was
measured six times in a single session before anybody read it as an ordering defect rather than as
forgetfulness (quince#100).

**Arm as the LAST action of the turn, after a foreground catch-up tick:**

```sh
# 1. do all the work first: every push, every comment, every review, every merge
# 2. consume the catch-up SYNCHRONOUSLY, where the session that caused it can read it
bin/forge-watch tick --all --gh "$PWD/bin/gh-arch"                  # architect
bin/forge-watch tick --repo <owner/name> --gh "$PWD/bin/gh-bot"     # implementer
# 3. arm, last, against a now-current observation                     (BACKGROUND task)
bin/forge-watch watch --all --gh "$PWD/bin/gh-arch" --interval 60
```

`tick` is one pass and returns, so it belongs in the foreground; it is `watch` — the loop — that must
never run there.

**Why last.** Self-caused events are deliberately not suppressed (quince#62), and a session's last act
is almost always a push, a comment, a review or a merge — which is precisely an event on something it
is watching. So a watch armed *before* that act is dead by design by the time the turn ends, and the
`Stop` hook is telling the truth when it says so. The rate is worst on the implementer side, whose
self-caused events are *how a turn ends*, where an architect's approvals and merges are occasional.

**Why the tick, and why arming last is necessary but not sufficient.** A re-arm from `dead` correctly
emits what accrued — and what accrued is the session's own actions from the turn just finished. So the
first tick exits immediately, and reaching a *quiet* watch takes two arms, only the second of which can
survive the end of the turn. The foreground tick absorbs that catch-up in the open, rather than as a
task notification arriving after the turn has already ended.

**Why the tick is safe to put there, in both directions.** A hand-run `tick` leaves the liveness
verdict exactly as it found it: it never refreshes `.watch.last_watcher_tick`, so it cannot make a
**dead** watch look **alive** (quince#49); and `step()` carries the watcher record forward, so it
cannot make a **live** watch look **dead** (quince#103). **Both halves are load-bearing, and the
second is the one that was broken.** It is also the one that matters more, because `watch`'s refusal
to arm beside a live watcher *reads that record*: erasing it did not merely mislabel a watch, it
disabled the guard, so the very next arm — the one this ordering prescribes — put a second watcher on
one state file, which is quince#50's race reached **through** the guard rather than around it.

That is worth stating as a rule and not just as history: **a safety argument that checks one direction
of a two-directional property has not been checked.** The first version of this section asserted only
that a hand tick cannot make a dead watch look alive and called it *"the one way this ordering could
have been unsound"*. It was not the one way. It was the way that happened to be true, and the ordering
was ruled and nearly landed on it.

Measured on the implementer side: three `Stop`-hook firings before the tick step was adopted, none
after (quince#100). The ordering itself needed **no** change to `watch` or `tick`, which is how
quince#100 was filed and it was right; making it *sound* did need one, and quince#103 is it. A skill
change and a tool change, and the skill change was not safe to land alone.

So:

0. **Pull the launchpad first — it is part of arming, not housekeeping.** `--all` reads
   `.claude/forge-set` from *this checkout*, so a stale checkout arms a watch whose set is silently
   smaller than the declared one. The tool's hard-fail cannot catch it: a stale set is not missing, not
   empty and not malformed, it merely describes yesterday. Seen on a real arch box, at a commit
   predating the file entirely (quince#33).

1. **`bin/forge-watch watch`, as a BACKGROUND task, does the waking — and the loop is the tool's, not
   yours.**

   ```sh
   bin/forge-watch watch --all --gh "$PWD/bin/gh-arch" --interval 60     # architect
   bin/forge-watch watch --repo <owner/name> --gh "$PWD/bin/gh-bot" --interval 60   # implementer
   ```

   **And the DECLARED BLOCKING SET, which is the other half of what a watch is for** (quince#80). A
   session's PR set is self-describing; what it is *blocked on* is not, and in this project the
   channel that carries **authority** is an issue — an Operator ruling is a comment on one. A watch
   that sees only PRs cannot see a ruling land, and the measured case is exact: the quince#44 ruling
   landed on an issue while the architect's blocked list was quince#70/#71/#72/#75/#78/#80, most with
   no PR at all, and the only thing that caught it was a hand re-read the session had promised itself.

   ```sh
   bin/forge-watch watch --all --gh "$PWD/bin/gh-arch" --interval 60 \
     --issue novkostya/quince#71 --issue novkostya/quince#80      # architect: owner/name#n required
   bin/forge-watch watch --repo <owner/name> --gh "$PWD/bin/gh-bot" --interval 60 \
     --issue 71                                                   # implementer: bare n, one repo
   ```

   `--issue` replaces the declared set, `--no-issues` clears it, and **passing neither keeps what is
   on disk** — a re-arm must never depend on remembering to restate something, which is what the four
   forgotten re-arms of quince#43 are worth. The declaration **outlives the session**, deliberately:
   dying with it would force a restatement on every re-arm and forgetting would be silent. The cost —
   a successor inheriting a watch on something nobody is waiting for — is paid by `status` printing
   the set **with its age**, so a stale declaration is a question rather than an invisible watch.

   **The loop must exit when it finds something; a loop that cannot exit cannot wake you.** That is
   the whole of quince#62, and it is stated here because a reader who hand-rolls anyway needs the
   constraint: a session is woken by a background task *completing*, so `while :; do tick; sleep 60;
   done` — which is what a watching loop looks like everywhere else, and which the previous version of
   this file printed — detects everything and delivers nothing. It ran for fifty minutes with a fresh
   heartbeat, updating state, `status` reporting `live`, and a deaf session behind it. Every check
   available agreed it was healthy, because health was never the thing that was broken.

   Run it in the **background**. In the foreground it blocks the session it exists to wake.

   Every exit `watch` can return, and which are re-arms. This document named the refusal in prose
   while still asserting *"every exit is a re-arm"* — which is the same false instruction the skills
   carried, reached by a different route, and following it on a refusal loops forever with no watch
   running (quince#75):

   | exit | means | what to do |
   | --- | --- | --- |
   | **0** | events found, on its stdout | handle them, then **re-arm** |
   | **1** | **REFUSED** — already `live`, or `wedged`, or a bad argument | **read `status`, then act on the answer** (quince#88): `live` → **leave it running**, no second watch is wanted — do *not* run `forge-watch stop` · `wedged` → `forge-watch stop`, then arm · `dead` or `absent` → **arm again.** Bounded at **two arm attempts in one turn**; a third refusal means something other than this session is arming and dying, which is a report, not a loop. |
   | **6** | `--max-wait` idle bound, default 1200 s, `event=watch-idle` | nothing happened, which is a report and not a silence — **re-arm** |
   | **7** | `--fail-after` failing ticks in a row | fix the cause the events name, then **re-arm** |

   So step 0's answers are enforced rather than recited: `live`, `starting` and `wedged` refuse, `dead` and
   `absent` proceed and the tool says which.

   `status` exit codes: **0** live, **9** starting, **3** dead, **4** absent, **5** wedged,
   **10** orphaned.

   **`orphaned` (exit 10) means the watcher is RUNNING and the session that armed it is gone**
   (quince#111). It is neither live nor dead, and both of those answers are actively harmful here:
   `live` tells you nothing is owed while nothing can wake you, and `dead` tells you to re-arm from a
   state a running process is still writing. The remedy is `forge-watch stop` **first**, then re-arm
   from that state without reseeding it. You will meet this after a session is killed mid-watch —
   the watcher is a child of the session, so a single-pid kill reparents it and it keeps ticking. A
   watch whose owner cannot be *verified* gone never reports `orphaned`; it stays whatever it was.

   **`starting` (exit 9) is armed-but-not-yet-ticked, and it is NOT owed** (quince#95). A watch reads
   this from the moment it is armed until its first tick lands — measured at 4 s with nothing declared
   and 17–18 s against a 20-issue set, because a first tick is one `gh pr list` plus one `gh issue
   view` per declared issue. Do not arm a second one; `watch` refuses if you try. It is bounded at one
   interval from arming and degrades to `dead reason=never_ticked` past it, because an unbounded
   `starting` would be a state that cannot fail while nothing is watched — quince#62 in a new place.

   An exit code of **2** is not the tool's at all — it is jq failing underneath and the script
   aborting, so read the error rather than looking it up here.

   A watch that exited is a watch that is not watching; a watch that *refused to start* is usually one
   that was not needed.

   **Arm unconditionally. Never gate an arming behind a shell pre-check** (quince#88). The row above
   used to read *"do not re-arm"*, on the reasoning that the refusal is quince#50's guard working. The
   guard is working — but a refusal is true only at the instant it is produced, and the rule turned it
   into a durable one. Measured across one architect session: **five** losses of the watch came from
   *not* arming because something was live; **none** came from arming when nothing should have been.
   The costs are not comparable — a wrong arm is one exit 1 and no lost watcher, a wrong stand-down is
   being unwatched while two notifications both read as success.

   The deciding property is not the tally, though. **`watch`'s own refusal is the only check here that
   is atomic with the act it guards.** Every conditional written beside it is check-then-act across a
   window in which the thing checked can die, so it subtracts rather than adds:

   ```sh
   bin/forge-watch status --all | grep -c 'watch=live'; exec bin/forge-watch watch --all …   # gates NOTHING: `;` sequences
   if [ "$(… | grep -c 'watch=live')" -eq 0 ]; then exec … ; fi                              # composes correctly, STILL races
   ```

   The second form was measured failing: the check was right, and ten seconds later the watcher exited
   on the session's own approval. Neither belongs in a session's hands. Note that this is a rule about
   *gating*, not about *asking* — `/architect` §0 and `/kickoff` §0 still require you to read `status`
   and **report which of the six answers you got**, which is an obligation the tool cannot discharge
   on your behalf. What is retired is the pre-arm conditional, which nothing ever prescribed; it was
   invented from §0's tone.

   **The window is narrowed, and its worst consequence is now closed — but it is not gone, and this
   file must not claim otherwise.** Between the `status` read and the arm, a live watcher can still
   die on your own forge write, so a refusal you acted on can be stale by the time you act. quince#102's
   arm-last ordering shrinks that from one side and the rule above from the other.

   What quince#95's `starting` class closed is the *other* direction, which was the damaging one: a
   watch that had just been armed correctly read as `dead`, and the `Stop` hook's remedy for `dead` is
   *arm another one*. That is how a guard came to hand out quince#50's race as an instruction. The
   class is a distinct exit (**9**) precisely so the hook, which reads the code and not the prose,
   sees the difference. The `Stop` hook remains the **backstop** for what is left — a backstop, which
   is not the same thing as a resolution.

   **6 and 7 are not crashes, and your harness will call them crashes.** A background task that exits
   non-zero is reported as *"failed with exit code 6"* — verified, not predicted — and 6 is the tool's
   *designed heartbeat*. Read the last line of the output, which says which exit it was and why; a
   session that reads its own heartbeat as a malfunction will either raise a false alarm or start
   distrusting the mechanism, and both are worse than the stall this replaced.

2. **`ScheduleWakeup` requires `/loop` dynamic mode, and a session not started that way cannot arm
   it at all.** Say the second channel is absent rather than reporting a fallback that was never
   armed — an architect session hit exactly that on 2026-07-29. The watcher's own `--max-wait` is
   the floor this protocol already relies on and is unaffected; redundancy is lost, not the loop.

   **Where it is available, it stays a fallback heartbeat, ≥1200 s — and it is NOT cover.** Its record
   **differs by box, which is the whole reason to write the box down**: on the architect box it has
   delivered nothing across every arming measured, and on the runner it has delivered **once, roughly
   an hour after it was due** (quince#62 carries the dated measurements; do not copy the numbers here,
   or this file acquires arithmetic nobody has scheduled to maintain). Neither of those is a cadence to
   plan against. Arm it anyway — it costs nothing and no design should rest on one channel — but do not
   reason as though it protects you, which is the mistake that produced the fifty-minute stall. **The
   floor under a terminating watch is its own `--max-wait`, not the fallback.** When the fallback does
   fire, its **first job is a liveness assertion** — `forge-watch status` — and if the answer is `dead`
   it says so out loud instead of quietly ticking once and going back to sleep.

   **What the late delivery does and does not establish.** The session was continuously busy from
   arming until the moment the wakeup arrived — every turn re-invoked by a watcher exit — and it landed
   on the first turn with nothing else in flight. *"Fired late"* and *"deferred until the session was
   idle"* are **not distinguishable from one observation**, and the difference matters: the second
   would mean the fallback cannot rescue a session that is stuck rather than sleeping, which is the
   case it exists for. Recorded as an open question rather than resolved by the reading that suits us.

3. **Arming is not optional, and it is not on your honour.** `bin/forge-watch owed` asks whether a
   live watch is owed, and a `Stop` hook in `.claude/settings.json` runs it when a turn ends. If one
   is owed, the turn is **blocked once** with the exact command to run; end the turn again and it
   stops blocking and tells the **human** instead. It exists because the previous version of this
   file said *arm a watch* and a session simply did not — no watcher, no state, no fallback — and
   ended on *"the ball is back with the reviewer"* four minutes before the verdict landed. A rule
   that tells a session to do something is satisfied by a session that does not do it. If it cannot
   check — no credential, forge unreachable — it says the question was **not checked**, which is not
   the same as *nothing is owed*, and neither blocks nor reassures.

   **The two halves are asked different questions, and only one of them is about the queue**
   (quince#71). `--author` asks the forge which repositories that login has open PRs in: an
   implementer's set is what it AUTHORED, so an empty queue genuinely means nothing is owed.
   `--all` returns **the whole declared set unconditionally**, with no queue query at all: a
   reviewer's set is what ARRIVES, and **an empty queue is not a finish** — the work is done when
   nothing further is coming, which is not knowable from inside. So a reviewer at rest is *armed and
   idle*, never *stopped because the queue was empty*, and the tool's answer says `declared` rather
   than `open PRs` so the reason cannot be a lie attached to a true verdict.

   That change also takes the forge off the hook's path for the reviewer half: whether a watch is
   live on a declared set is answerable from local state alone, so `owed --all` can no longer be
   wedged by an unreachable forge.

4. **A tick that was due and did not happen is reported, not absorbed.** `forge-watch` emits
   `event=tick-overdue due=… late=…` for it. The events themselves cannot carry that fact: they arrive
   looking perfectly healthy, all at once, hours late.

## Whose turn is it — the question the event model cannot answer

`bin/forge-watch` tells you *that* something happened. It does not tell you whose move it is, and the
four blind spots of quince#43 all live in that gap. So, when deciding whether you owe something:

- **`updatedAt` says WHEN, never WHO.** Fetch the actor. A session read a bare timestamp, assumed the
  latest activity was its own, and reported "nothing owed from me" with three items owed. `event=updated`
  carries `actor=`; when it says `actor=unattributed` that is honest — commonly a checklist box being
  ticked, which moves `updatedAt` through a channel no activity list can see — and it means *go and
  look*, not *nothing happened*.
- **`reviewDecision` does not move across a fix.** A PR you fixed still reads `CHANGES_REQUESTED` until
  a new review lands. It is not a signal about whose turn it is; it is a record of the last verdict.
- **Green checks mean different things depending on turn.** Awaiting first review, red is what changes
  who must act. Awaiting *re*-review, green is. Do not read either as "nothing to do".
- **A PR parked pending someone else's decision is re-examined on EVERY tick**, whether or not an event
  fired (program doc, corollary (e)). Being wrong about what can matter costs most exactly where you are
  blocked on someone else — a held, approvable PR waited 64 minutes for a confirmation that had already
  been posted. Record the park **on the PR**, so a fresh session can rebuild the set without you.
- **`event=mergeability pr=N status=CLEAN` means the ball is the merger's**, and it is the one park the
  tool now covers for you. A PR that is approved and whose CI then completes has **nothing happen to
  it** — the approval was the last mover, and check completion does not move `updatedAt` (measured) —
  so the backstop is structurally blind there and quince#63 sat landable for sixteen minutes behind a
  live, quiet watch (quince#65). **This does not retire corollary (e).** It mechanises the *CI* park,
  which is the commonest one and the only one where a field moves; a park on a human decision moves
  nothing at all and is still yours to re-examine every tick.

## Rebase discipline — when to move the branch under a verdict

- **Rebase when the ball is yours.** A fix pushed in response to changes-requested goes out **rebased
  onto current `main`**, so the reviewer reads the fix against the tree it will land in.
- **Hold still when a verdict is in flight.** Do not rebase or force-push while a review may be being
  written: the reviewer's approval can attach to a commit they never read.
- Under strict up-to-date protection, **someone else's merge silently invalidates every other open PR**.
  That is `event=mergeability status=BEHIND` and it does *not* move the invalidated PR's `updatedAt` —
  nothing happened to it. Rebase then, too; the ball came back to you without anyone handing it over.

## Citing a ruling

**Comment URL plus self-declared role, never a login.** The architect and the Operator post as the same
identity (quince#47), so "the Operator ruled X" with no link is not a citation — it is a claim about a
record the reader must go and fail to verify. And an instruction that contradicts a written ruling is
**mirrored to the forge before you act on it**: ten seconds of posting, against a held PR and a round
trip through a third party.

## The stall counter — losing time invisibly

These boxes depend on an attached host client. When it drops, `Read`/`Write` fail after **exactly ten
minutes** with `PreToolUse hook did not respond before its timeout (host client may be unreachable)`
while `Bash` keeps working. One session lost ~84 minutes of 140 that way, retrying the same tool three
times before finding the shell fallback unaided.

- **Count them, and state the total in your next report**: "lost N minutes to M unanswered hook calls".
  A stall that is absorbed looks like a slow machine, and the first hypothesis it produces is wrong.
- **After the first occurrence, stop using the file tools**: `cat`, `sed -n`, `awk` to read, a heredoc
  to write. Each retry of the same tool costs another ten minutes.
- **Do not build a probe for this.** The counter is the instrument; ordinary use over days answers how
  often it happens.

## Stopping

The loop stops on the gap protocol's triggers — contracts, storage semantics, security posture,
user-visible behaviour — and on any review comment it cannot satisfy without a ruling. A stop is
**recorded on the PR**, naming what was hit and what is needed, because a push nobody reads leaves no
trace and the forge is the memory.

**"I finished a PR" is not a stop.** Neither is "over to the architect", and neither is **"the ball is
back with the reviewer"** — the sentence a session ended on with nothing armed at all, four minutes
before the verdict it was waiting for landed (quince#62). The list of illegitimate stops is not the
defence; it has been extended twice, each time by a session inventing the next sentence. The defence is
that ending a turn with an unwatched PR now blocks once and then tells the human.

The legitimate stops are: everything merged and the tail done; a decision that is the Operator's; or an
unruled gap — and in the last two cases, say exactly what would unblock you.
