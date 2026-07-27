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
the four answers you got**:

```sh
bin/forge-watch status --all      # declared set; exits 0 live / 3 dead / 4 absent / 5 wedged
```

`bin/forge-watch watch` (§6) runs this same check before arming and **refuses** on `live` and on
`wedged`, so neither of the two answers that must not lead to a second watcher depends on this section
being read. Ask anyway: the tool can refuse to arm, but it cannot report on your behalf that a watch was
found dead.

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
bin/gh-arch api user -q .login                                                # must answer, and not with the bot
```

**Do not use `gh auth status` for this.** An architect host is expected to show *unauthenticated*
there: its credential is a token file read by `bin/gh-arch` at point of use, never a `gh auth
login` session — that is deliberate, so the credential cannot leak into an ambient session. The
question is not "is `gh` logged in" but "can this box cast a real verdict", and `bin/gh-arch api
user` is what answers it. (This skill originally asserted `gh auth status`, written before
`bin/gh-arch` existed; the first architect session on a real arch box hard-stopped on a correctly
configured host. A protocol that checks the wrong thing fails closed, which is the right direction
to fail — but it still fails.)

- **Bot token present on an architect host** → say so and stop. A box that can author *and*
  approve dissolves the property the whole identity ruling protects (devlog#7). Reviewing from a
  host that holds both is a finding about the host, not a detail to work around.
- **`bin/gh-arch` cannot answer** (no credential file, or it refuses) → stop, and quote its own
  message: it names the path and how to place it. An architect session that cannot submit a real
  review verdict is a session pretending to be one.
- **It answers as the bot** → stop. Same reason, worse: the identities have been crossed.
- Verdicts and merges in later steps run through **`bin/gh-arch`**, not bare `gh`, for the same
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
tool, not to you:

```sh
git -C "$PWD" pull --ff-only          # the watch set is this checkout's copy
bin/forge-watch watch --all --gh "$PWD/bin/gh-arch" --interval 60
```

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
| **1** | **REFUSED** — already `live`, or `wedged`, or a bad argument | **do not re-arm.** `live` needs no second watch; `wedged` needs `forge-watch stop` first. Re-arming loops forever. |
| **6** | `--max-wait` idle bound, `event=watch-idle` | nothing happened, which is a report and not a silence — **re-arm** |
| **7** | `--fail-after` failing ticks in a row | fix the cause the events name, then **re-arm** |

`status` answers a different question with its own codes: exit **0** live · **3** dead · **4** absent
· **5** wedged. An exit of **2** is not the tool's at all — it is an underlying tool (jq) failing and
the script aborting, so read the error rather than looking it up here.

**Re-arm on 0, 6 and 7. On 1, read what it refused and why**: a watch that exited is a watch that is
not watching, but a watch that *refused to start* is usually one that was not needed.

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
normal state is asleep with a watch armed, not polling, and not finished. Finishing is: the queue empty,
or a decision that is the Operator's, or an unruled gap, and in the last two cases say exactly what
would unblock you.
