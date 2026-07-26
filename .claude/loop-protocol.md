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

   **The loop must exit when it finds something; a loop that cannot exit cannot wake you.** That is
   the whole of quince#62, and it is stated here because a reader who hand-rolls anyway needs the
   constraint: a session is woken by a background task *completing*, so `while :; do tick; sleep 60;
   done` — which is what a watching loop looks like everywhere else, and which the previous version of
   this file printed — detects everything and delivers nothing. It ran for fifty minutes with a fresh
   heartbeat, updating state, `status` reporting `live`, and a deaf session behind it. Every check
   available agreed it was healthy, because health was never the thing that was broken.

   Run it in the **background**. In the foreground it blocks the session it exists to wake.

   It exits 0 on events (they are on its stdout), **6** at the `--max-wait` idle bound (default 1200 s,
   `event=watch-idle`), **7** after `--fail-after` failing ticks in a row. It refuses to arm beside a
   `live` or `wedged` watch, so step 0's four answers are enforced where they matter. **Every exit is a
   re-arm**: a watch that exited is a watch that is not watching.

   **6 and 7 are not crashes, and your harness will call them crashes.** A background task that exits
   non-zero is reported as *"failed with exit code 6"* — verified, not predicted — and 6 is the tool's
   *designed heartbeat*. Read the last line of the output, which says which exit it was and why; a
   session that reads its own heartbeat as a malfunction will either raise a false alarm or start
   distrusting the mechanism, and both are worse than the stall this replaced.

2. **`ScheduleWakeup` stays as a fallback heartbeat, ≥1200 s — and it is NOT cover.** Its record
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

3. **Arming is not optional, and it is not on your honour.** `bin/forge-watch owed` asks whether there
   are open PRs here with no live watch, and a `Stop` hook in `.claude/settings.json` runs it when a
   turn ends. If a watch is owed, the turn is **blocked once** with the exact command to run; end the
   turn again and it stops blocking and tells the **human** instead. It exists because the previous
   version of this file said *arm a watch* and a session simply did not — no watcher, no state, no
   fallback — and ended on *"the ball is back with the reviewer"* four minutes before the verdict
   landed. A rule that tells a session to do something is satisfied by a session that does not do it.
   If it cannot check — no credential, forge unreachable — it says the question was **not checked**,
   which is not the same as *nothing is owed*, and neither blocks nor reassures.

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
