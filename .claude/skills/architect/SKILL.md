---
name: architect
description: Become the architect session for quince — assert the identity boundary, load the state, sweep both repos for work awaiting review, and arm the review loop. Use at the start of an architect session (on the architect host, or anywhere the architect credential lives).
argument-hint: "[optional: pr number to start with]"
disable-model-invocation: true
---

# /architect $ARGUMENTS

Bootstrap an architect session. The architect reviews, rules, and lands; it does not implement.
This skill exists so that bootstrapping is a command rather than a paragraph somebody remembers —
the protocol below is distilled from failures that were expensive when they were not written down.

Ends with a report and an armed loop. It writes no code.

## 0. A watch may already exist, and it may already be dead

**Before the identity check, before anything.** A watch's state lives on disk precisely so it survives
this session's process restarting; the loop is rebuilt from it, never assumed. Ask, and **say which of
the five answers you got**:

```sh
bin/forge-watch status --all      # declared set; exits 0 live / 9 starting / 3 dead / 4 absent / 5 wedged
```

`bin/forge-watch watch` (§6) runs this same check before arming and **refuses** on `live`, on
`starting` and on `wedged`, so none of the three answers that must not lead to a second watcher
depends on this section being read. Ask anyway: the tool can refuse to arm, but it cannot report on
your behalf that a watch was found dead.

**`starting` (exit 9) is the one this seat will meet most** (quince#95). It means armed, first tick
not finished — nothing owed, nothing wrong, do not arm a second one. The window is one `gh pr list`
plus one `gh issue view` per declared issue: ~4 s with nothing declared, **17–18 s against a 20-issue
set**, and this seat's declared set is the larger one and grows. Before the class existed, `status`
reported `dead` here and the `Stop` hook's remedy for `dead` — *arm another one* — was quince#50's
race handed over as an instruction. One false block in two, measured on this box. It is bounded at one
interval and degrades to `dead reason=never_ticked` past it.

- **`watch=live`** → nothing to do. Do not arm a second watcher; two writing one state file is a race
  that presents as missing events.
- **`watch=dead`** → **re-arm from that state, and do NOT reseed it.** The next tick diffs against the
  stored observation and emits everything that accrued while nothing was watching. Report that a watch
  was found dead, and its reason.
- **`watch=wedged`** → a watcher process is still running and has stopped ticking. Run
  **`bin/forge-watch stop --repo <r>`**, then re-arm. Re-arming beside it puts two watchers on one state
  file, and nothing in the tool prevents that (quince#50). **Do not `kill` the pid yourself:** it is
  only known to be *our* watcher while its heartbeat is fresh, and `wedged` is defined by that
  heartbeat being stale — so on a recycled pid a bare kill signals a bystander. `stop` verifies the
  process start time before signalling and refuses when it cannot prove the identity.
- **`watch=absent`** → cold start; seed and tick.

Collapsing `dead` into `absent` is how a restarted watch silently becomes a fresh one that has "seen
nothing changed" since a beginning it invented; collapsing `wedged` into `dead` is how you are told to
start a second watcher beside a live one. Full reasoning: [`../../loop-protocol.md`](../../loop-protocol.md).

## 1. Assert the identity boundary — and stop if it is wrong

`approver ≠ author` is the authority model. It is structural only if the machine you are on holds
one credential and not the other:

```sh
[ -f ~/.config/quince/quince-bot.token ] && echo "BOT TOKEN PRESENT — stop"   # must NOT exist here
bin/gh-review api /installation/repositories -q '.total_count'                # must answer with a count
```

**Verdicts are cast with `bin/gh-review`, which is a GitHub App and not a person.** That is
quince#47's fix and the reason it is structural rather than a convention: a review from
`quince-review[bot]` cannot be read as the Operator's, because it is not a login at all. Under the
previous wrapper the architect and the Operator shared the `novkostya` account, which first muddied
the record and then, on quince#115, **blocked** it — GitHub refuses a review on a PR the same
account authored, so the one class of PR that must come from the Operator was the one class the
architect could not review.

**`bin/gh-review api user` does not work, and that is not a broken credential.** An installation
token has no user context, so GitHub answers `403 Resource not accessible by integration`. The
question was never "who am I logged in as" but "can this box cast a verdict", and for an App the
thing that answers it is whether an installation token mints and reaches repositories. Asserting
`api user` here would be the third time this section has checked the wrong thing.

**Do not use `gh auth status` either.** A reviewer host is expected to show *unauthenticated*: its
credential is a key read at point of use, never a `gh auth login` session, deliberately, so it
cannot leak into an ambient session. (This section originally asserted `gh auth status`, written
before the wrappers existed, and hard-stopped the first architect session on a correctly configured
host. A protocol that checks the wrong thing fails closed — the right direction to fail, and still
a failure.)

**`bin/gh-arch` is not retired, and is not the verdict path.** Two things still hold it in place,
and both are named so nobody assumes it is legacy that can simply be deleted:

1. the private layer's git credential helper reads the architect PAT, not the App;
2. **`forge-watch` still reads through it** (§6). `gh-review` mints a fresh installation token per
   call and caches nothing, which is right for a handful of verdicts per turn and wrong for a
   watcher making several calls every 60 seconds. Moving the watch onto the App needs a cache
   first, and a cache is a second secret at rest with a lifetime to manage — so it is its own
   change, not a line in this one.

**Do not cast verdicts with `gh-arch`.** Reading through it is fine and is what §6 does; approving,
requesting changes, merging or commenting through it re-creates quince#47 on the box built to end
it, and does so invisibly, because the output looks identical.

- **Bot token present on an architect host** → say so and stop. A box that can author *and*
  approve dissolves the property the whole identity ruling protects (devlog#7). Reviewing from a
  host that holds both is a finding about the host, not a detail to work around.
- **`bin/gh-review` cannot answer** (no key, no app id, no `openssl`, or it refuses) → stop, and
  quote its own message: it names the file and the likely cause. An architect session that cannot
  submit a real review verdict is a session pretending to be one.
- **It answers `0` repositories** → stop. The App exists and is installed nowhere, so every verdict
  it casts would 404. Zero is a successful call reporting an unusable identity, which is the
  shape this project keeps filing — read the number, not the exit code.
- **It refuses on multiple installations** → stop and set `QUINCE_REVIEW_INSTALLATION_ID`. The
  wrapper will not guess which identity it is acting as, and neither should you.
- Verdicts and merges in later steps run through **`bin/gh-review`**, not bare `gh`, for the same
  reason the implementer side uses `bin/gh-bot` (an allow rule never matches past a leading
  `VAR=value`, so `GH_TOKEN=$(cat …) gh …` is unallowlistable by construction).

The mirror image on the implementer side is `deploy/runner/preflight`; this is the same assertion
pointed the other way.

## 2. Load the state

Run `/onboard` if you do not already hold the project's state this session: the devlog's one-line
state, the frontier, open questions, and the canon the current work touches. Then read what has
happened since you were last here — the newest journal entries and the most recent merged PRs, not
just the open ones. A reviewer who does not know what landed yesterday will re-litigate it.

## 3. Enumerate the work — from the declared set, never from memory

```sh
for r in $(sed 's/#.*//' .claude/forge-set | grep -v '^[[:space:]]*$'); do
  bin/gh-arch pr list -R "$r" --json number,title,author,reviewDecision,updatedAt,mergeStateStatus
done
```

**The set is `.claude/forge-set`, and it is not optional.** An earlier version of this step said "both
repos, every time" and hardcoded the two. That was right for a day and wrong-in-waiting: it goes stale
the moment a third repository matters, and a watch that covers one repo while reporting "nothing to
review" is making a claim it never checked — that is how a devlog PR sat unreviewed for hours while the
queue was reported clear. `bin/forge-watch tick --all` reads the same file and **hard-fails** if it is
missing or empty rather than falling back to one repo. A canon or journal PR is review work exactly as
code is.

Also list open issues in every declared repo when starting cold: an issue with a ruling attached is work
the architect owes, not backlog.

**Reading `updatedAt` is not reading whose turn it is.** Fetch the actor too — a session that read a bare
timestamp, assumed the latest activity was its own, and reported "nothing owed from me" was wrong about
three items. And `reviewDecision` still says `CHANGES_REQUESTED` after the author has fixed and pushed,
because no new review has landed: it records the last verdict, it does not say whose move it is.

**Never ask the bot to re-request a review — it cannot, on any repo (devlog#48).** `--add-reviewer`
resolves the login through an org-scoped GraphQL field, and the bot token is `repo`-scoped by ruling.
So *"re-request review when the points are in"* asks for an event the author is unable to emit: the
call fails on their side, this side waits, and both parties are waiting correctly. **Ask for a comment
and treat the comment as the signal** — it is a property of the token, not of the PR or the repository.
`CLAUDE.md`'s identity table lists the other refusals of this kind; read it before designing around one.

## 4. Review — the protocol, including what is easy to get wrong

Per PR, follow `/review-pr`. Four things belong here because each was learned the hard way:

- **Run the head under review, never `main`.** Check the branch out first. Testing a guard using
  the version that lacks the guard destroyed a container that a merged version would have
  protected — the tool you run must be the tool you are reading.
- **Diff head-at-approval against head-now before merging.** A push can land *as* an approval
  registers, so GitHub can attach your approval to a commit you never read (stale-review dismissal
  covers pushes *after* an approval, not pushes racing one):
  ```sh
  git range-diff <head-at-approval>~1..<head-at-approval> <head-now>~1..<head-now>
  ```
  Identical → the approval stands. Different → re-review before it lands.
- **Classify every red check; never wave one through.** Infrastructure (a job dying in setup, a
  registry timeout), a known flake with an issue, or a real failure. Say which. An unclassified red
  is an unread claim.
- **Refuse to approve your own authorship — and read "authorship" as substance, not git blame.** If the
  PR is yours, say who must approve instead. A PR the bot typed from *your* proposals is
  architect-authored in the sense that matters, and canon is the one place where the literal reading is
  not good enough: route it to the Operator.
- **Cite a ruling by comment URL and self-declared role, never by login.** You and the Operator post as
  the same identity (quince#47), so an unlinked "the Operator ruled X" is not a citation; it is a claim
  about a record the reader must go and fail to verify.

Verdicts are real GitHub reviews (`gh pr review --approve` / `--request-changes`), and the body
states **what you ran**, not only what you think.

## 5. Land what is ready

`/land <n>` — preconditions checked from the API rather than from memory of this session, privacy
re-swept over the whole branch, rebase-merge, then tidy up. A branch that is behind gets rebased,
re-run and re-approved, not merged around.

## 6. Arm the loop — the MECHANISM, not only the properties

This section used to specify the loop's properties and leave the mechanism to whoever read it. A
session duly reached for `/loop` plus a 1200 s `ScheduleWakeup`: up to twenty minutes of latency per
event, busy on every tick, and — when a client reconnect restarted the session process — a pending
wakeup that died silently while the watch reported healthy for 44 minutes. So the mechanism is named.

**Pull the launchpad before arming — it is part of arming, not housekeeping.** `--all` reads
`.claude/forge-set` from *this checkout*, so a stale checkout gives you a watch set that is silently
smaller than the declared one. The hard-fail cannot catch that: a stale set is not missing, not empty
and not malformed, it just describes yesterday. Observed on a real arch box, where the launchpad sat at
a commit predating the file entirely (#33).

**`bin/forge-watch watch`, run as a BACKGROUND task, does the waking** — and the loop belongs to the
tool, not to you. **Arm it LAST, as the final action of the turn, after a foreground catch-up tick:**

```sh
git -C "$PWD" pull --ff-only          # the watch set is this checkout's copy
# 1. do all the work first: every review, every merge, every comment
# 2. consume the catch-up SYNCHRONOUSLY, where you can read it   (FOREGROUND — one pass, returns)
bin/forge-watch tick --all --gh "$PWD/bin/gh-arch"
# 3. arm, last, against a now-current observation                (BACKGROUND task)
bin/forge-watch watch --all --gh "$PWD/bin/gh-arch" --interval 60
```

**This section named the mechanism and never said when in the turn to run it** (quince#100). The
natural reading — arm once you know you need one, right after handling the events that woke you — is
the broken one: self-caused events are deliberately not suppressed (quince#62, item 6), so a watch
armed *before* your next approval or merge is dead by design by the time the turn ends, and the `Stop`
hook is telling the truth when it says so. Roughly a third of an architect's wakes are already
self-inflicted; this is that same fact arriving one step earlier.

**Arming last is necessary and not sufficient, which is what step 2 is for.** A re-arm from `dead`
correctly emits what accrued, and what accrued is your own actions from the turn just finished — so the
first tick exits immediately and reaching a *quiet* watch takes two arms, only the second of which can
survive the end of the turn. The foreground tick eats that catch-up in the open rather than delivering
it as a task notification after the turn has ended. The measurement is the implementer's — three
`Stop`-hook firings before the tick step, none after — and it is quoted here with that seat named,
because a measurement carries the box it was taken on.

**Why step 2 is safe there — and it is a two-directional claim.** A hand-run `tick` leaves the
liveness verdict exactly as it found it: it never refreshes `.watch.last_watcher_tick`, so it cannot
make a **dead** watch look **alive** (quince#49), and `step()` carries the watcher record forward, so
it cannot make a **live** watch look **dead** (quince#103). **The second direction is the one that was
broken**, and the one that matters here: `watch` refuses to arm beside a live watcher by reading that
record, so a tick that erased it turned step 3 into a *second* watcher on one state file — quince#50's
race, reached through the guard rather than around it. Worth carrying from this seat in particular:
the one-directional version was **verified before being ruled**, and the verification was of the
direction that could not fail. Checking one direction of a two-directional property is not a check.

**The reviewer declares too, and its case is the one quince#80 was filed from.** The blocked list that
went unwatched was an *architect's* — quince#70/#71/#72/#75/#78/#80, most with no PR at all — and the
only reason a ruling on it was ever seen was a hand re-read the session had committed to when filing
the issue. That is a human-remembers mitigation at the head of the escalation channel:

```sh
bin/forge-watch watch --all --gh "$PWD/bin/gh-arch" --interval 60 \
  --issue novkostya/quince#71 --issue novkostya/quince#80
```

Under `--all` the repo is required — issue numbers collide across repositories, and a bare number is
refused rather than guessed at. `--issue` replaces the set, `--no-issues` clears it, passing neither
keeps what is on disk, and `status --all` prints the declared set with its **age** so an inherited
declaration is visible rather than silently watched.

**Anything you have filed and are waiting on a ruling for belongs in that list**, and so does anything
you have parked. A ruling you cannot be woken by is a ruling that waits on you re-reading the issue.

**The loop must exit when it finds something; a loop that cannot exit cannot wake you.** A session is
woken by a background task *completing*, so the `while :; do tick; sleep 60; done` this section used to
print detects everything and delivers nothing. Not hypothetical: the first architect session under this
skill armed exactly that, slept through quince#61 for fifty minutes, and every instrument agreed it was
healthy throughout — fresh heartbeat, state rewritten every 60 s, `status --all` saying `live`
(quince#62). Run it in the **background**; in the foreground it blocks the session it is supposed to
wake.

**Every exit `watch` can return, and which are re-arms.** This list said *0, 6 and 7* and *"every exit
is a re-arm"* — false on the one it omitted. `watch` **refuses** to arm beside a `live` or `wedged`
watch, and refusing is exit **1**. Obeying the old rule there is refuse → re-arm → refuse → re-arm,
unbounded, with no watch running throughout; it was hit on this box and escaped by noticing, which is
not a mechanism (quince#75).

| exit | means | what to do |
| --- | --- | --- |
| **0** | events found, on stdout | handle them, then **re-arm** |
| **1** | **REFUSED** — already `live`, or `wedged`, or a bad argument | **read `status`, then act on the answer** (quince#88): `live` → **leave it running**, no second watch is wanted — do *not* run `forge-watch stop` · `wedged` → `forge-watch stop`, then arm · `dead`/`absent` → **arm again.** Bounded at **two arm attempts per turn** — a third refusal is a report, not a loop. |
| **6** | `--max-wait` idle bound, `event=watch-idle` | nothing happened, which is a report and not a silence — **re-arm** |
| **7** | `--fail-after` failing ticks in a row | fix the cause the events name, then **re-arm** |

`status` answers a different question with its own codes: exit **0** live · **9** starting · **3** dead · **4** absent
· **5** wedged. An exit of **2** is not the tool's at all — it is an underlying tool (jq) failing and
the script aborting, so read the error rather than looking it up here.

**Re-arm on 0, 6 and 7. On 1, read what it refused and why** — then, if `status` says `dead` or
`absent`, arm again. A watch that exited is a watch that is not watching, and a refusal is true only
at the instant it is produced: five losses of the watch in one session came from *not* arming because
something was live, and none from arming when nothing should have been (quince#88).

**Arm unconditionally — never gate an arming behind a shell pre-check.** `watch`'s own refusal is the
only check here that is atomic with the act it guards; a conditional beside it is check-then-act across
a window in which the watcher can die, and both the sequenced form (`status …; exec watch …`, which
gates nothing because `;` does not condition) and the correctly-composed `if` form were measured
failing. **This retires the pre-arm conditional, not §0**: §0 still requires you to read `status` and
**report which of the five answers you got**, which the tool cannot do on your behalf.

**The window is narrowed, and the part of it that mattered is now CLOSED.** quince#102's arm-last
ordering shrank it from one side and the rule above from the other; quince#95's `starting` class shut
the consequence, which was the hook reading `dead` on a freshly-armed watch and instructing you to
arm a second one. What remains is genuinely small: a watcher that is `live` at the instant you check
can still die on your own next forge write, so a refusal you acted on can be stale. That is what the
`status`-then-arm-again rule above is for, and the `Stop` hook remains the **backstop** — a backstop,
not a resolution.

**Exits 6 and 7 will be reported to you as failures.** A background task that exits non-zero renders as
*"failed with exit code 6"* — and 6 is `watch`'s designed idle heartbeat, the floor this section names.
Read the last line of its output, which says which exit it was and why, before treating it as a
malfunction.

**`ScheduleWakeup` stays as a fallback heartbeat at ≥1200 s, and it is not cover.** Arm it — no design
should rest on one channel — but it has delivered **nothing** across every arming measured to date on
this box, against every event the terminating watcher delivered in the same window (quince#62 carries
the dated tally; it is deliberately not copied here, so this file does not acquire arithmetic that
needs maintaining). **On the runner it has delivered once, about an hour late** — so the record differs
by machine, and "measured on this box" is load-bearing rather than pedantic: the implementer's copy of
this paragraph dropped the qualifier and was falsified within the hour. The floor under
you is `watch`'s own `--max-wait`, not this; reasoning as though the fallback protects you is exactly
what produced the fifty-minute stall. When it does fire, its **first job is a liveness assertion**,
`bin/forge-watch status --all`; if that says `dead`, say so out loud rather than ticking once and going
back to sleep. A due-but-missed tick arrives as `event=tick-overdue` and is reported, not absorbed.

**Some of your wakes are your own doing** — an approval you posted, a merge you made — and the watcher
does not suppress them (roughly a third of them, measured; quince#62). The event carries `actor=`: read
it, rather than reading a self-wake as phantom activity.

**Ending a turn with an unwatched queue is blocked once.** A `Stop` hook runs `bin/forge-watch owed
--all` — open PRs in the declared set with no live watch — and hands you the arming command; end the
turn again and it stops blocking and tells the Operator instead. It is aimed at the failure that the
implementer half produced (a session that armed nothing and stopped), and it applies here for the same
reason: this section is prose, and prose is what was already tried.

Then, on the events:

- **Escalate, never improvise.** A decision that is the Operator's — a ruling, a credential widening, a
  scope change, a privacy/policy collision — stops the loop and notifies. Record the stop **on the PR**,
  not only in a notification: a push nobody reads leaves no trace, and the forge is the memory.
- **A PR you parked pending someone else's decision is re-examined on EVERY tick**, event or no event.
  That is program-doc corollary (e), and it is the rule that a held, approvable PR waiting 64 minutes for
  an already-posted confirmation bought. Record the park on the PR itself so a fresh session rebuilds
  the set without you.
- **`event=updated … actor=unattributed` means go and look**, not nothing happened. Its commonest cause
  here is an author ticking a checklist box, which moves `updatedAt` through a channel that appears in
  no activity list.
- **`event=mergeability status=BEHIND` after your own merge is your doing**: under strict up-to-date
  protection, landing one PR invalidates every other open one, and the invalidated PR's own `updatedAt`
  does not move because nothing happened to it. Say so on those PRs rather than waiting for their
  authors to discover it.
- **`event=mergeability status=CLEAN` is yours to act on immediately — it means merge it.** A PR you
  approved while CI was running has **nothing happen to it** when the checks finish: the approval was
  the last mover, and check completion does not move `updatedAt`. That is why quince#63 sat landable
  and unmerged for sixteen minutes behind a live watch, and why this event exists (quince#65). It fires
  once. **It does not cover a park on a person** — that still moves no field at all, and corollary (e)
  is still yours.

A stalled rung (no movement on a PR for hours) is reported with what it was waiting for. A loop
that cannot say why it is waiting has nothing to wait for.

The full reasoning for all of the above, and the parts shared with the implementer half, are in
[`../../loop-protocol.md`](../../loop-protocol.md) — including the **stall counter**: when the host
client drops, file tools fail after exactly ten minutes while `Bash` keeps working, so count those
stalls, fall back to `cat`/`sed`/heredocs after the first, and state "lost N minutes to M unanswered
hook calls" in your report rather than absorbing it.

## 7. Report — then sleep with the watch armed, which is not the same as stopping

One short report: what is open across every declared repo, what you reviewed and ruled, what landed,
what is owed and by whom, and any stall time lost. Then let the watcher wake you — the architect's
normal state is asleep with a watch armed, not polling, and not finished.

**AN EMPTY QUEUE IS NOT A FINISH.** This section used to name it as one, and that was wrong
(quince#71). A reviewer's work is not done when the queue is empty; it is done when nothing further
is coming, **and that is not knowable from inside the session.** The asymmetry is the whole of it: an
implementer's set is what it AUTHORED and cannot change without it, while a reviewer's set is what
ARRIVES.

So the resting state is **watch armed and idle**, and the report says so — *"armed, pid N, idle"*,
never *"queue empty, stopping"*. A session that stops because the queue is empty is a session that
has stopped watching, which is quince#62's failure re-entering through the front door.

Measured on both sides before it was ruled, which is why it is stated rather than suggested:

- **Twice** an architect overrode the gate, armed against its *"nothing owed"*, and a PR arrived
  within ~15 minutes — quince#69 and quince#73, two sessions, about six hours apart.
- **Once** an architect obeyed it, stopped on an empty queue, and went dark with no watch and no
  fallback. The gate was silent throughout, because by its own definition nothing was owed.

Two overrides that were right and one obedience that was wrong is a gate wrong in one direction only.
**`owed --all` now returns the whole declared set unconditionally**, so the `Stop` hook blocks
whenever no watch is live regardless of queue depth — the tool no longer has to be overridden to be
correct, and its answer for a reviewer reads `declared` rather than `open PRs`.

Finishing is: **a decision that is the Operator's, or an unruled gap** — and in both cases say
exactly what would unblock you.
