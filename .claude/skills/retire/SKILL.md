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

`<gh>` is your seat's wrapper **for reading the forge** — `bin/gh-coder` on the runner, `bin/gh-arch`
on the architect box. §1 only *reads* (a `pr list`), which is the one forge call both architect
wrappers may make; **do not carry this substitution into §2, which posts** — on the architect box
the posting wrapper is a different tool, named there. `/retire` is the first skill *both* seats run,
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

**Re-assert with `gh pr list`, never with `forge-watch tick`.** A tick consumes accrued events out
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

**Post through your seat's *attributed-write* wrapper — `bin/gh-coder` on the runner, `bin/gh-review`
on the architect box, never `bin/gh-arch`.** This is a *different* wrapper from §1's read: §1 asked
only which tool reads the queue, and on the architect box that is `gh-arch`; every row above *posts*
— a comment, an issue, a ruling — and commenting or ruling through `gh-arch` re-creates quince#47 on
the box built to end it, invisibly, because the output looks identical (`/architect` §1). Reading
answered one question; posting is the other.

**Owed work is written, not bequeathed.** If the flush reveals a DoD tail — a journal entry, a
click-list — write it and open it for review. An entry nobody wrote is debt handed to a successor;
an entry filed and awaiting review is a record on the forge, which is this whole rule.

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

## 6. Stop the watchers deliberately — LAST, and not before

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

## 7. Then stop

No new work. No approvals. **Anything done after §2 is by definition unrecorded** — and if
something does land after it, that is §1's loop firing, so go back and re-assert rather than
appending to a record you have already closed.

Sign the retirement the way every other record is signed: the self-declared role and this URL,
never the login (quince#47).
