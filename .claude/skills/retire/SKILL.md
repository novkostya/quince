---
name: retire
description: End a quince session so nothing it knows dies with it — prove the boundary, flush every unfiled thing to the forge where its subject lives, declare the ephemeral state, then record what could not be recorded at all. Use when a session is stopping: a restart window, an exhausted context, a box being rebuilt, or an Operator instruction to stop.
argument-hint: "[optional: reason — restart | context | rebuild | instruction]"
disable-model-invocation: true
---

# /retire $ARGUMENTS

**Flush to the forge, not to the successor, and not to the Operator.**

A session ending holds things that exist nowhere else: why a PR is parked, a finding noticed but
not filed, a ruling it made and where, what it was about to do next. All of it has to land
somewhere durable, and the tempting shape — a handoff note addressed to whoever comes next — is
the wrong one. A note is read once, by one successor, and then rots; it invites *narrative*
("I was in the middle of…") where the forge wants *records* ("this PR is parked pending X, stated
on the PR"). Worse, it contaminates the resurrection test: a successor reading a letter is not
reading the record, so nothing is exercised and the gap stays hidden.

**A record is filed where its subject lives** — on the PR it is about, on the issue it rules, in
the journal if it is history. **A note is addressed to a person.** This skill makes the first easy
and the second awkward, on purpose.

Distilled from six retirements performed by hand across both seats (quince#55), not written from
first principles. Where a step looks fussy, it is the shape of something that went wrong.

## 1. Prove the boundary — and this is a LOOP, not a check

```sh
<gh> pr list --repo <owner/name> --state open --json number,title,author,reviewDecision,mergeStateStatus
```

`<gh>` is your seat's wrapper — `bin/gh-coder` on the runner, `bin/gh-review` on the architect box.
**It is the same wrapper in §2, which posts**, and this line used to say otherwise: the architect
box held two credentials, so §1's read and §2's write were different tools. quince#676 retired
`bin/gh-arch`, so the substitution now carries straight through. `/retire` is the first skill *both*
seats run,
so §1 cannot name one wrapper: an architect host must never hold
`~/.config/quince/quince-bot.token` — `/architect` §1 hard-stops if it finds one and `preflight`
asserts its absence for `role arch` — so a hardcoded `bin/gh-coder` here is a command that cannot
authenticate on half the boxes that reach this step (quince#133). `forge-watch` takes the same
per-seat *read* wrapper as `--gh`, and only the read case.

Per repo in `.claude/forge-set`. Ask, against the API rather than from memory:

- an open PR you authored, and what it is waiting on;
- a verdict you gave that is not recorded on the forge;
- a review in flight, or a comment awaiting your answer;
- work you started that exists only as an unpushed branch.

**If it is not clean, that is fine — retire anyway, but say exactly what is outstanding and who it
is waiting on.** A retirement is not an abandonment when nothing outstanding *requires anything you
know and have not written down*. That sentence is the only claim a retiring session can make that
matters, and it is what the rest of this procedure makes checkable.

**Then flush (§2), then come back and run this again.** The forge does not pause while you write.
Measured twice in one retirement: a PR merged between the record being posted and the boundary
being re-asserted, creating a DoD tail that did not exist at the first check; and a correction
landed on an open PR *during* the flush, which would have sat in a thread contradicting a merged
record. A session that asserts a clean boundary, flushes for ten minutes, and stops has asserted
something that stopped being true while it was being useful.

**Re-assert with `<gh> pr list` — §1's per-seat read wrapper, not a bare `gh` — never with `forge-watch tick`.** A tick consumes accrued events out
of a state file the successor may inherit — a retiring session reads the forge without writing to
the watch state. That distinction is not obvious and was found only because one session's watchers
were already stopped when it re-checked.

## 2. Flush, item by item, to where each item belongs

Nothing addressed to "the next architect". For each thing you hold:

| It is | It goes |
| --- | --- |
| a parked PR | a comment on **that PR**, naming what unblocks it and who owns that |
| a finding | an **issue**, with the evidence, not a sentence in a retirement note |
| a ruling you made | the **issue it rules**, in citable form — the URL and the self-declared role, never the login (quince#47) |
| history worth keeping | the **devlog journal**, date-anchored, citing PR/issue numbers |
| a branch built and unopened | a comment on the **issue it serves**, naming the branch — this is the item successors most often lose |
| an issue your merged work FIXED | **that issue, closed**, with the merge quoted — `bin/stale-refs-report` is how you find it (below) |

**Post through your seat's wrapper — `bin/gh-coder` on the runner, `bin/gh-review` on the architect
box.** It is the **same** wrapper §1 reads with, which is new: this paragraph used to insist the two
were different, because the architect box held a PAT that could read but must never post — posting
through it re-created quince#47 invisibly, since the output looks identical. quince#676 retired that
wrapper, so there is one credential and nothing to get wrong here.

**Owed work is written, not bequeathed.** If the flush reveals a DoD tail — a journal entry, a
click-list — write it and open it for review. An entry nobody wrote is debt handed to a successor;
an entry filed and awaiting review is a record on the forge, which is this whole rule.


**Run the stale-refs sweep before you decide you are finished** — it is the one item on that table
you cannot find by remembering, because the thing to notice is an issue you did **not** think about:

```sh
make stale-refs-report                      # this repo; REPO= for the devlog
```

It lists issues that are **open**, referenced by a **merged** PR that used a non-closing reference,
with **no comment since that merge**. Exit `0` means it looked, whatever it found; **`2` is NO
VERDICT and is never clean** (a partial sweep says so and withholds the verdict rather than
reporting nothing to review).

**`Refs` is correct and is exactly what goes stale.** A PR writes it because the author is not sure
the fix is total, or because the PR is one of N and no single one may honestly claim the issue —
and then **the last of those N has no way to know it was the last.** Nothing revisits it afterwards,
so the issue reads as live work and gets re-taken. Measured: quince#529 sat fixed and open for five
days, quince#849 for five, quince#1069 for two.

**Two prescribed actions, and only these two.** For each candidate, either **close it**, quoting the
merge that fixed it — or **comment saying why it stays open**, which is a legitimate state (a
sentinel) and also removes it from every future sweep. What you must not do is leave it untouched,
because the next session cannot tell your silence from nobody having looked.

**It is a REPORT, not a gate: it cannot know intent, so it must not fail anything and it will not.**
It has a known false-positive shape — a PR body that mentions an issue in prose or a table, rather
than citing it as a trailer, is read as a reference (quince#1002). Dismiss those by reading; do
**not** comment on an issue purely to quiet the tool, which would be the tracker's noise made worse
by the thing built to reduce it.

**THIS STEP EXISTS BECAUSE THE TOOL WAS BUILT AND INVOKED BY NOTHING.** `bin/stale-refs-report` and
its make target have existed since quince#1234, and no skill step, hook or service ever ran it — so
it caught things only when a session happened to be doing a deliberate sweep. That is quince#823's
finding one subsystem over: *a reaper nobody invokes is a reaper that does not exist*, and §7 below
is the same move for scratch clones (quince#1247).

## 3. Declare the ephemeral state

Say which watchers ran and where their state lives. **Describe here; stop them in §6, not now** —
a live watcher is the instrument that tells you §1 needs re-running, and this step changes nothing:

```sh
bin/forge-watch status --all      # quote the output
```

Three things the successor needs and cannot infer:

- **The declared issue set outlives the session** and will be inherited **stale**. Say so, and say
  in which directions — issues that have closed, issues filed since. A successor should
  **re-declare from the open issues rather than adopt it**.
- **`status` cannot say why a watch ended.** A watcher that exited on an event, one deliberately
  stopped, and one that crashed all read `dead` / `reason=no_process`, all carrying the note *"RE-ARM
  from this state"* — right after an event, wrong after a retirement. If you stopped one on purpose,
  **that fact exists only in what you write here.** (One missing field, observed four ways across
  these retirements: crash-vs-retire, exited-on-event-vs-retire, the misleading note, and §5's hook.)
- **What the watcher proved by silence.** Idle cycles — `watch-idle elapsed=… ticks=…` — are the
  strongest evidence the loop works and they exist only in session scratch. Quote the counts.

## 4. Record what could NOT be recorded — this is the whole point

Steps 1–3 are mechanical and could be automated. **This one cannot, and across six retirements it
is the only step that produced anything new.** The list it makes is the specification for the next
version of the forge.

Do not write "nothing". Nobody has yet retired with an empty item 4. Three questions that reliably
find something, because **everything unrecordable is a negative or a rate**, and the forge is
excellent at events while having no vocabulary for non-events:

1. **What did not happen?** Silence that proved a mechanism; a check run and found clean; a
   catch-up that reported nothing missed — and whether "nothing was missed" is even provable
   (a PR opened *and* closed inside an outage leaves no trace in a diff of current state).
2. **How often were you wrong?** The instances are on the PRs; the **rate** is nowhere, and the rate
   is what tells you whether the two-seat review is working. Count corrections in both directions.
3. **What did you do that no tool asked for?** A gate overridden and right, a measurement taken
   because a previous one came back negative, an ordering chosen for a reason. Judgement that
   produced a correct outcome leaves no record of having been exercised.

State each with its *forge fix* where one exists, and say plainly where none does.

## 5. The `Stop` hook will fire, and you must override it

Ending the turn, `bin/forge-watch owed` blocks:

```
A WATCH IS OWED AND IS NOT LIVE — you are about to end a turn with nothing that can wake you.
```

**Every word of it is true, and obeying it would be wrong.** `owed` answers *"can anything wake
this session?"* — and at retirement the correct answer is that **nothing should**. Arming a watcher
now creates quince#111's Face 1 deliberately: a watcher orphaned seconds later by a session that no
longer exists.

**State the comparison honestly, because both branches end at `dead`.** Arming does not lose you an
`absent`: `absent` means *no state file*, and the only way to reach it is to delete one — which
reseeds, discards the accrued observation, and is exactly what §3 and quince#49 forbid. **`absent` is
not available at retirement, and a retirement that produced it would have destroyed something.** So
the real choice is:

| if you arm | if you do not |
| --- | --- |
| `dead` / `no_process`, **plus** an orphaned process and a backlog the successor is told to replay | `dead` / `no_process`, quiet, with the accrued observation intact |

Same class, same note, and the difference is entirely the orphan and the replay. That is enough to
decide it — and it is less than the first draft of this section claimed, which asserted the
successor would cold-start and emit `first-observation`. It will not. It will read `dead`, and the
note it reads will tell it to re-arm, which after a retirement is only right because there *is* a
successor coming and the accrued events are genuinely owed to it.

The hook's own escape names *"everything merged, or a stop that is the Operator's"*. **Retirement is
a third state and is in neither**, so a correctly-retiring session must override a gate that is
telling the truth.

**The override is conditional on the flush being complete, not on feeling done.** A session that
arms nothing *and* has not flushed is the exact failure the hook exists for. A session that has
flushed to the forge is the one case where an unwatched queue is correct. Say which you are.

## 6. Stop the watchers deliberately — not before this, and before §7

```sh
bin/forge-watch stop --all                   # every watcher in the declared set (quince#118)
# or, to stop just one:  bin/forge-watch stop --repo <owner/name>
```

**Not earlier.** A live watcher is what makes §1's loop work: in the retirements on quince#55 it is
the watcher firing that caught a PR merging mid-flush and a correction landing on an open PR while
the record was being written. One of those sessions says plainly that it re-asserted *"by accident,
because the watcher fired"*. Stop it at the start and you remove the instrument that tells you §1
needs running again.

**Stop it rather than leaving it to die with the session.** A watcher left running is quince#111's
Face 1 reached by doing nothing — an orphan reporting `live` to a successor while able to wake
nobody. On some harnesses background watchers die with the session and it is self-correcting; **that
is an observation about a harness, not a property anyone has established**, and a retirement should
not rest on it.

`stop` verifies the recorded pid is still ours before signalling and refuses if it cannot prove it
(quince#49) — so it is safe in the one case a bare `kill` is not. Under `--all` it verifies each pid
the same way and refuses **as a whole** if any one cannot be proven, naming the repo, so a partial
stop cannot leave a watcher live while reporting success. **Then record that you stopped
them on purpose**, because §3's ambiguity means the state itself cannot say so: deliberately
stopped, exited on an event, and crashed all read `dead` / `no_process`.


**"Not before" is unchanged; "last" is no longer true, and §7 is why.** Stopping the watchers is now
the second-to-last step: §7 reaps this session's clones, and a live watcher holds a path *into* one
of them (`--gh "$PWD/bin/gh-coder"`). Reaping first can delete the wrapper a running watcher is
about to exec. The reason for "not before" is untouched — a live watcher is what makes §1's loop
work — and only the tail moved.
## 7. Reap this session's finished clones

```sh
bin/scratch-reap --prune                     # own root only — the default is $HOME/scratch/<runner>
```

**Ruled by the architect on quince-devlog#286**, after an Operator direction to propose it. The
reaper has existed since quince#45 and nothing had ever invoked it: 51 runner roots and **21.1 GB**
accumulated on one box before the architect hit `Quota exceeded` mid-clone at 99% of a 32 GB volume.
A by-hand sweep of the dead roots removed **109 clones and recovered ~15.5 GB**.

**`--prune`, not a report.** The ruling is explicit and the evidence is the reason: *"a reaper that
has existed since quince#45 and has never been invoked let 51 roots accumulate, and a report at
retirement is a thing nobody acts on for the same reason nobody ran the tool."* The safety rule
carries the weight, and it is measured at scale rather than assumed — across 459 clones on the
architect root, 240 detached HEADs were clean with every commit already in `main` by patch and
**zero were dirty**.

**OWN ROOT ONLY.** No `--root`, ever, in this step. The default resolves to `$HOME/scratch/<runner>`
precisely so one runner never touches another's trees (quince#45, quince#111) — and a retiring
session cannot know whether another seat's clone is finished. The cross-root sweep that recovered
the 15.5 GB was an **Operator instruction** and stays exceptional.

**AFTER §1's boundary proof, never before and never instead of it.** §1 asks whether any work exists
only as an unpushed branch; this deletes clones. Inverting them makes a deletion the thing that
decides whether work existed. The reaper's own rule refuses a dirty tree and an unmerged branch, but
that is a second line of defence, not the argument — the argument is that §1 has already run.

**AND AFTER §6, WHICH IS WHY THIS IS §7 AND NOT §5.** A live watcher holds a path *into* a scratch
clone: both skills arm it as `--gh "$PWD/bin/gh-coder"`, and `$PWD` is the working clone. Reap while
a watcher is live and you can delete the wrapper it is about to exec — a watcher that reports `live`
and cannot fetch. §6 stops the watchers; only then is the tree safe to remove.

**REPORT ALL THREE NUMBERS — `N reaped, M kept, K unjudged`** — which is what the summary line now
prints, and the ruling required it: *"five 'could not compare' clones is exactly quince#41's shape —
a tool that looked at something, could not answer, and lets the number vanish."* `kept` means it
looked and decided no; `unjudged` means it could not answer.

**SAY "REDUCED", NOT "CLEARED".** A retirement does not empty its root and must not claim to. The
reaper correctly keeps a clone whose branch is still open or whose commits are not upstream — 49 of
90 keeps on the by-hand sweep were exactly that. A session reading *"root cleared"* about a root
still holding clones learns the wrong thing about its own disk.

**It does not block the retirement.** The reaper KEEPs whatever it cannot judge, so the failure
direction is safe, and a retirement that stops on an unjudgeable clone is a retirement that does not
happen. Report the number and carry on.

**On the architect seat this buys little TODAY, and that is known rather than discovered.** 244 of
459 clones there (54%) sit on a detached HEAD — `gh pr checkout` leaves them that way once the PR's
branch is deleted — and the reaper refuses to judge a detached HEAD, having no branch to ask either
question of. The extension that fixes it is quince#823's open half: ask the same patch-id question
of `HEAD`. Run this step anyway; it is correct, and it stops the growth on the implementer seat now.

## 8. Then stop

No new work. No approvals. **Anything done after §2 is by definition unrecorded** — and if
something does land after it, that is §1's loop firing, so go back and re-assert rather than
appending to a record you have already closed.

Sign the retirement the way every other record is signed: the self-declared role and this URL,
never the login (quince#47).
