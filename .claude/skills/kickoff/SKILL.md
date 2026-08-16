---
name: kickoff
description: Take one unit of quince work from issue to merged — resolve the target, read the state and canon it touches, take a fresh clone and branch as the coder App, produce the PR-slicing plan, then open the PRs and work them through review until they land. Use at the beginning of an implementer session, and when an event wakes a session onto an existing PR.
argument-hint: "[issue-number | qn.N | pr.N | free-text scope]"
disable-model-invocation: true
---

# /kickoff $ARGUMENTS

Take one unit of work and carry it to merged. Self-onboard, plan, **post the plan and keep going** —
the plan is a checkpoint, not the deliverable.

## 0. Declare the runner, THEN ask whether a watch already exists

**Declaring RELOCATES the state directory, so a `status` read before it answers about the wrong
path** (quince#241). Until `runner set` runs, state resolves to the undeclared top-level path rather
than `…/forge-watch/<name>` — so a session **resuming a name that has state** is told `absent` when
the truth is `dead`, and that is the one substitution this whole section exists to prevent: `dead`
carries an accrued observation to re-arm from, `absent` says nothing was ever armed. §3 already says
to declare first; it just sat three sections below the read.

```sh
bin/forge-watch runner set <name>   # ONCE per session. `r<N>` — a pattern (r/arch/analyst), not a list.
```

**The name must LOOK like a seat — `r<N>` for an implementer, `arch<N>` for the architect, `analyst<N>` for the analyst — and `runner set` REFUSES one that does not** (quince#265, re-founded on a pattern by quince#330; third seat quince#375). That
shape is what lets a watch on the OTHER box attribute your branches. §3 carries the rest — what the
name does to the scratch root, and the guard against an empty one — and nothing there changes except
that this step has already happened by the time you reach it.

**Now ask about the watch.** If this session is resuming work — a `/kickoff <pr>` woken by an event,
or a session whose process restarted — ask before arming anything, and **say which of the six answers
you got**:

```sh
bin/forge-watch status --repo novkostya/quince   # 0 live · 9 starting · 3 dead · 4 absent · 5 wedged · 10 orphaned
```

**`orphaned` (exit 10) → a watcher is running and the session that armed it is GONE** (quince#111),
so it can wake nobody. This is the answer a restarted process is most likely to get, because the
watcher is a child of the session and a single-pid kill reparents it rather than ending it. `stop`
it first, THEN re-arm from that state without reseeding — the running process is still writing to
it. **If `stop` refuses (exit 1), do NOT arm** (quince#221): on this path the watcher is still
running *by definition*, so a second one beside it is quince#50's race — and an unwatched turn you
have *reported* beats two watchers you have not. Say which happened. Never treat this as `live`:
`live` would tell you nothing is owed while nothing can wake you.

`live` → nothing to do, and do not arm a second watcher. **`starting` → a watch was armed and its
first tick has not landed yet: nothing is owed and nothing is wrong. Wait.** **`dead` → re-arm from
that state and do NOT reseed it**; the next tick emits everything that accrued while nothing was
watching, and you say that a watch was found dead. **`wedged` → a watcher is still running and has
stopped ticking: run `bin/forge-watch stop --repo <r>`**, then re-arm — never a bare `kill`, because
the pid is only known to be *ours* while its heartbeat is fresh and `wedged` means it is not.
`absent` → cold start.

**`stop` can REFUSE, and then you do NOT arm** (quince#221). Exit 0 means no live watcher of ours
remains — established by waiting for the process to exit, not by `kill` returning, which only means
the signal was queued. Exit 1 means one may still be live, and arming beside it is quince#50. Read the
success line too: `exited on SIGTERM` is ordinary, **`REQUIRED SIGKILL` is a finding** worth reporting
rather than a detail.

**`starting` exists because `dead` used to cover it, and the two want opposite remedies** (quince#95).
A watch reads `starting` from arming until its first tick lands — ~4 s with nothing declared, 17–18 s
against a 20-issue set, since a first tick is one `gh pr list` plus one `gh issue view` per declared
issue. During that window the `Stop` hook read `dead` and handed sessions the command to arm a
*second* watcher onto one state file, which is quince#50's race reached by obeying a guard. It was
measured at **one false block in two** on the architect box. It is bounded at one interval and
degrades to `dead reason=never_ticked`, so a watcher that is armed and never ticks cannot sit healthy
forever.

Collapsing `dead` into `absent` turns a restarted watch into a fresh one that has "seen nothing changed"
since a beginning it invented; collapsing `wedged` into `dead` tells you to start a second watcher
beside a live one. Reasoning: [`../../loop-protocol.md`](../../loop-protocol.md).

`bin/forge-watch watch` (§6) runs this same check before arming and **refuses** on `live` and on
`wedged`, so the two answers that must not lead to a second watcher no longer depend on this section
being read. Ask anyway: the tool can refuse to arm, and it cannot report on your behalf that a watch was
found dead.

## 1. Resolve the target

**Use `bin/gh-coder`, not bare `gh`.** It is *the* tool for every forge call on this side: an allow rule
never matches past a leading `VAR=value`, so `GH_TOKEN=$(cat …) gh …` is unallowlistable by
construction and prompts every time. Both implementer sessions in the first two-box run opened with
bare `gh`, had it denied, and rediscovered the wrapper — so it is named here rather than left to be
found.

- A number → `bin/gh-coder issue view <n> --repo novkostya/quince --comments` (try the devlog repo
  too if it isn't there; process work lives there). **Read the comments, not only the body** — a
  correction comment can invert a requirement, and building the uncorrected version reproduces the
  bug the issue was filed about.
- `qn.N` → the rung: its row in the devlog `progress.md` dashboard, its entry in
  `roadmap.md`, and `docs/specs/qn.N/` if it exists.
- `pr.N` → process work; the ruling behind it, then the sequence note in the devlog.
- Nothing → read the devlog one-line state, name the frontier, and propose it. Don't guess
  between two plausible frontiers: ask.

## 2. Read the state and the canon this work touches

If you have not run `/onboard` this session, read the devlog `progress.md` state line +
this work's dashboard row + the open questions, and `program/quince.program.md`. Then:

- the rung's spec (if any) — stories and acceptance gates are your contract;
- the canon docs the work touches: `docs/contracts.md` for any API surface,
  `docs/quince.design.md` for storage/job semantics, `docs/quince.stack.md` for a
  decision's history, `docs/ui.design.md` for frontend work;
- prior art: `bin/gh-coder pr list --repo novkostya/quince --state merged --limit 20` and
  `git log --oneline -- <the tree you will touch>`;
- the devlog `proposals.md` declined list, so you don't rebuild a refused idea.

**Open question or unruled gap in your path? Stop there.** Write the `PROPOSED (gap): …`
block per the gap protocol and report; do not build past it.

## 3. Fresh clone + branch, as the coder App

One clone per unit of work, from GitHub, in a scratch directory — never a long-lived
checkout, never a worktree, never an rsync from a workstation.

**The scratch root is `$HOME/scratch/<runner>`**, and naming it is not tidiness: `<scratch>` was a
placeholder for months, so there was no place to point a reaper at and 33 clones accumulated in two
days. `bin/scratch-reap` reaps that root and only that root, so one runner never touches another's
trees (quince#45, quince#111).

**The runner is already declared — §0 does it, before it reads any state** (quince#241). The
`runner set` line is repeated below so this block stays readable on its own, and running it twice is
harmless: **re-declaring your OWN name from the same session is a clean no-op** — measured, exit 0,
same state directory, no reseed. (A name held by a *live* other session is refused; one whose holder
is provably gone is **reclaimed** rather than refused, reusing its state directory without resetting
it — quince#211.) What matters is that the declaration has **already** happened by the time you
reach this block.

Nothing else in this repository ever called `runner set`, so `runner get` failed and this block
computed `$HOME/scratch/` with an empty name component — every session cloning into one directory,
sharing a watch state, owning no branches. It failed silently, as an empty string in a path, which is
why `$HOME/scratch` did not exist on any box. One host is *meant* to run several implementers
concurrently; the name is what keeps them apart.

**The name must LOOK like a seat — `r<N>` for an implementer, `arch<N>` for the architect, `analyst<N>` for the analyst — and `runner set` REFUSES one that does not** (quince#265, re-founded on a pattern by quince#330; third seat quince#375). That
shape is what lets a watch on the OTHER box attribute your branches: the branch namespace is global,
the local registry is not, so a name that is not seat-shaped wakes every watch on every box for every
event it produces.

**A new ORDINAL is free; a new KIND is a one-line PR** — and this paragraph said the opposite until
quince#375. It read *"the refusal names the file and lists the known seats. Adding a seat is a PR"*,
which was true of the committed `.claude/seats` list that quince#330 **deleted**: there is no file and
no enumeration, so `r8` and `arch3` need nothing. What still costs a PR is widening the alternation
itself, which quince#375 did for `analyst<N>`, and that is the intended cost — a third seat kind is a
decision, where a seventh implementer is not.

```sh
bin/forge-watch runner set <name>        # ONCE per session. `r<N>` — a pattern (r/arch/analyst), not a list.
RUNNER=$(bin/forge-watch runner get) || { echo "no runner declared — stop"; exit 1; }
[ -n "$RUNNER" ] || { echo "runner name is empty — stop"; exit 1; }
SCRATCH="$HOME/scratch/$RUNNER"
mkdir -p "$SCRATCH"
git clone https://github.com/novkostya/quince.git "$SCRATCH"/quince && cd "$SCRATCH"/quince
git config user.name  "quince-coder[bot]"
git config user.email "310563582+quince-coder[bot]@users.noreply.github.com"
git config credential.https://github.com.helper "$PWD/bin/git-coder --credential-helper"
# `$RUNNER/`, not the topic alone (quince#230). The prefix is what `wake_filter` prefix-matches to
# decide whose event an update is, so a branch without it cannot be attributed to any session.
# `$RUNNER` is already in hand three lines up — use it rather than retyping the name.
git checkout -b "$RUNNER"/<short-title>
```

**The guard is not ceremony.** `runner get` exits non-zero and prints nothing when undeclared, so
without it the failure is an empty path segment rather than an error — and a session would discover
it by colliding with another session's clone, not by being told.

**The identity is the App, not the bot.** This block named `quince-bot` and its token until
2026-07-29; that account was suspended on 2026-07-28, so it instructed every new session to
configure a credential that cannot authenticate and an author that no longer resolves. See
`quince-devlog` `decisions/0014`.

**The helper points at `bin/git-coder`, and is not hand-rolled here.** An earlier version of this
block inlined `password=$(gh-coder auth token)` — which is character for character the defect
quince#198 fixed hours earlier: a failing substitution still emits a syntactically valid
credential with an EMPTY password, so git reports `authentication failed` while the real reason,
a boundary refusal naming the offending file, is stranded on stderr. The helper exits 0.

In a skill that is worse than in a wrapper: it is re-created in every fresh clone, in a
.git/config nobody re-reads, on the one path where the hidden failure is a BOUNDARY refusal.
`bin/git-coder` already handles it — non-zero on refusal, silent on stdout, and scoped to
github.com so the token is never offered to another host.

If `gh-coder` cannot mint, stop: you cannot author — say so rather than pushing under whatever
identity happens to be configured, which on the Operator's own machine means pushing as the
Operator (quince#47, and quince-devlog#118 is what it looks like when it happens).

Then link the private layer — **in every clone, on every box**, not just the Operator's. Without it the
privacy gate exits `2` (DID NOT RUN) rather than pretending it swept — `make privacy-check` in quince,
and the product checkout's `deploy/privacy/privacy-check` in the devlog, which has no Makefile
(quince#78; the form is in `/report` §2):

```sh
# `local` is gitignored, so git can never commit it. The path comes from the same variable
# `deploy/runner/provision` and `deploy/runner/preflight` both read, so a box provisioned with an
# overridden layer is honoured rather than silently linked to somebody else's default.
ln -sfn "${QUINCE_PRIVATE_LAYER:-/root/quince-local}" local
```

This line used to say *"On the Operator's machine only"*. That was true when the Mac was where work
happened; once work moved to the boxes it applied nowhere, so the gate was inert everywhere and
every PR in that cycle hand-declared that its sweep had not really run (quince#44). The layer is now
a property of a provisioned box — `preflight` **refuses to start** one that cannot reach it — so if
you have a session at all, the layer is there.

`quince-devlog` needs the same symlink. It now carries `/local` in its `.gitignore`; before that it
did not, and a review caught the symlink one `git add` away from a public repo.

## 4. Verify where the gates will run

`make help` names the detected container runtime. No runtime → gates cannot run in this
clone; the work has to execute on a container host (`deploy/dev.md`). Do not install a
toolchain anywhere, and do not plan a PR you cannot prove.

**This box is Alpine: BusyBox `ash`, and NO PYTHON** (quince#246). `command -v python3` is empty on
every session box, so reaching for it costs a cycle to an exit `127` — three such instances in one
afternoon, on two seats, and once the defensive `python3 … || { sed … }` form failed *differently*
and cost more than the original. **Use `jq` for JSON, and do not assume GNU flags**: `${PIPESTATUS[0]}`,
`ls --time-style` and `find -newermt` all work in CI and all fail here. Python is absent from **the
box** deliberately, and from the release image too — and BusyBox is what that image
ships as its shell, so the fix is never to install something on the host, it is to write the
portable form. Full statement, with what was measured and the two traps quince#246 got wrong, in
[`deploy/dev.md`](../../../deploy/dev.md), *What a session box actually is*.

**Take the gate lane explicitly: say out loud that you are starting a ladder.** The container, network
and cache-volume names are fixed rather than per-run, so two ladders on one box destroy each other —
and the damage does not present as "two ladders collided", it presents as **a flake**, which is the
most expensive possible disguise on a project that has spent days chasing real ones.

## 5. Plan, then proceed

Produce, in the session (not as a commit):

1. **Scope** — the one-sentence outcome, and what is explicitly out of scope.
2. **PR slicing** — the sequence of small PRs, each with ONE reviewable claim and its own
   proof. If the rung has no spec, PR 1 *is* the spec (program-doc shape, `Rule check`
   filled in) and it is reviewed before any code exists.
3. **Proof plan** — the exact gate commands per PR, plus the spec's own acceptance gates,
   plus anything only real hardware can prove (name it as owed, with the owner).
4. **Rule check** — every hard rule this work touches *or comes near*, one line each,
   stating how the plan complies. Near-misses included; a plan about to break a rule
   cannot fill this in truthfully, which is the point.
5. **Unknowns** — what you had to assume, and what you would need to stop for.

Then **post the plan and keep going.** You do not need approval to open PRs; that is what review is
for. This section was headed *"Plan, then stop"* and said "otherwise start building" in its last line,
and the heading is the instruction that landed: in the first two-box run it took three Operator nudges
to restart sessions that had posted a plan and ended the turn — *"Go ahead. You don't need my approval
to open PRs"*, *"keep running loop that checks your PRs"*, *"Proceed. Post the plan and keep going"*.
Two models, two clients, same stop: that is the skill, not the model.

**Stop for a go only** if the scope is ambiguous, if the work touches contracts / storage semantics /
security / user-visible behaviour, or if the Rule check has a line you cannot write honestly — and when
you stop, **say exactly what would unblock you**. The DoD in `CLAUDE.md` is what "done" means, so plan
the deploy + click-list and the journal entry now, not at the end.

## 6. The implementer half of the coroutine — after opening PRs the session does NOT end

"I finished a PR" is not a stop. Neither is "over to the architect", nor **"the ball is back with the
reviewer"** — which is the sentence one session ended on at `19:41Z` with no watcher, no state and no
fallback armed, four minutes before the verdict it was waiting for landed (quince#62). Rewriting the
default from *stop* to *proceed* did not stop a session finding a new sentence for stopping.

**So: once you have a PR open, you owe a watch — and you arm it LAST, as the final action of the turn,
after a foreground catch-up tick.** Record the repo and number of every PR you open; that is your watch
set, self-describing, and it needs no declared file because you know what you opened.

```sh
# 1. do all the work first: every push, every comment
# 2. consume the catch-up SYNCHRONOUSLY, where you can read it       (FOREGROUND — one pass, returns)
bin/forge-watch tick --repo <owner/name> --gh "$PWD/bin/gh-coder"
# 3. arm, last, against a now-current observation                    (BACKGROUND task)
bin/forge-watch watch --repo <owner/name> --gh "$PWD/bin/gh-coder" --interval 60
```

**THE ARM MUST BE A SINGLE, UNCOMPOUNDED INVOCATION — nothing before it, nothing after it, no `;`,
no `&&`, no `&`** (quince#282). "Run it in the background" is not enough on its own: the architect
seat wrote it compounded twice in one session, and both times the arm silently did not survive —
`status` said `dead` seconds later, and the second left an **`orphaned`** watcher that refused the
next clean arm until it was `stop`ped.

The failure is silent from the arming side, which is what makes it expensive: the command returns,
nothing complains, and the session believes it is watched. Backgrounding is a property of **how the
harness runs the call**, so a `&` inside it backgrounds a *child of the wrong process* and the
watcher's owner is gone the moment the compound statement finishes.

**`eval` is NOT banned**, and this list says so because its first version got that wrong: `eval "exec
bin/forge-watch watch …"` is how a declared set is expanded, and it appears in every arm that WORKED
as well as in both that failed. The trailing `&` and the `;` are what discriminate. A rule that
forbids what you were doing correctly is a rule that gets ignored wholesale.

**This section used to say "the moment your first PR is open, ARM THE WATCH" and never said when in the
turn**, and the natural reading of that — arm as soon as you know you need one — is the broken one
(quince#100). **An implementer's last act is almost always a push or a comment**, which is precisely an
event on a PR it is watching. Self-caused wake suppression covers some of those and not others, so a
watch armed before that act is still dead by the time the turn ends more often than not, and the
`Stop` hook below is telling the truth when it says so. That is worse for you than for the architect,
whose self-caused events are approvals and merges — occasional — where yours are *how a turn ends*.

**Suppressed means NOT WOKEN ON, never NOT SEEN.** Every event is still printed on every tick;
quince#242's filters decide only whether the loop *ends*. This paragraph read *"self-caused events are
deliberately not suppressed"* until quince#309, eight days after that stopped being true. **On your
seat specifically:**

- your own **push to a `<runner>/…` branch you own** does not wake you, and neither does a review,
  merge or issue close you performed — the actor arm and the per-runner ledger respectively;
- **your own issue comment DOES wake you**, deliberately. `quince-coder` is one App shared by every
  runner, so `actor=quince-coder` names the **seat** and not the **session**: another runner
  commenting on an issue you declared is indistinguishable from you doing it (quince#227,
  quince#307), and the arm declines rather than guesses;
- a **rebase of your branch by the merging seat** wakes you — `committer=` differs from `actor=`,
  which is the point of the test;
- `actor=unattributed`, and any branch without a `<runner>/` prefix, always wake. Not every open PR
  carries the prefix, so this is the ordinary case rather than the edge one.

**Do not reason forward from "suppression handles it, so I can arm whenever."** The advice above is
unchanged; only its reason has been corrected. Correct advice resting on a false mechanism outlives
either error alone, because the advice keeps working and nobody re-reads the justification.

**Arming last is necessary and not sufficient, which is what step 2 is for.** A re-arm from `dead`
correctly emits what accrued, and what accrued is your own actions from the turn just finished — so the
first tick exits immediately and reaching a *quiet* watch takes two arms, only the second of which can
survive the end of the turn. The foreground tick eats that catch-up where you can see it, instead of it
arriving as a task notification after your turn is already over. Measured on this side: three
`Stop`-hook firings before the tick step, none after.

**Why step 2 is safe there — and it is a two-directional claim.** A hand-run `tick` leaves the
liveness verdict exactly as it found it: it never refreshes `.watch.last_watcher_tick`, so it cannot
make a **dead** watch look **alive** (quince#49), and `step()` carries the watcher record forward, so
it cannot make a **live** watch look **dead** (quince#103). **The second direction is the one that was
broken**, and the one that matters here: `watch` refuses to arm beside a live watcher by reading that
record, so a tick that erased it turned step 3 into a *second* watcher on one state file — quince#50's
race, reached through the guard rather than around it. This paragraph asserted only the first half
until quince#103 landed, and the first half is the half that could not fail.

**And DECLARE WHAT YOU ARE BLOCKED ON, in the same command.** Your PR set is self-describing; your
*blocked* set is not, and the channel that carries authority here is an **issue** — an Operator ruling
is a comment on one. A watch that sees only PRs cannot see a ruling land (quince#80):

```sh
bin/forge-watch watch --repo <owner/name> --gh "$PWD/bin/gh-coder" --interval 60 \
  --issue 71 --issue 80          # the issues you said you were waiting for
```

`--issue` replaces the declared set, `--no-issues` clears it, and **passing neither keeps what is on
disk** — so a re-arm does not have to restate it. Under `--all` an issue must be `owner/name#n`; a
bare number is refused, because issue numbers collide across repositories. `status` prints the
declared set **with its age**, which is how you tell an inherited declaration from your own: if you
did not declare it, somebody who is no longer here did, and you either adopt it or clear it.

**If you park something, declare its issue before you stop.** A park recorded on the forge that
nothing is watching is a stop you cannot be woken out of.

**The loop must exit when it finds something; a loop that cannot exit cannot wake you.** You are woken
by a background task *completing*, so `while :; do tick; sleep 60; done` detects everything and delivers
nothing — that shape ran for fifty minutes on the architect box with every liveness signal green and a
deaf session behind it (quince#62). `watch` owns the loop and ends it. Do not run it in the foreground,
where it blocks the session it exists to wake.

**Every exit `watch` can return, and which are re-arms.** This listed only 0, 6 and 7 and said every
exit is a re-arm — false on the one it omitted, and following it there loops forever (quince#75):

| exit | means | what to do |
| --- | --- | --- |
| **0** | events found, on stdout | handle them, then **re-arm** |
| **1** | **REFUSED** — already `live`, or `wedged`, or a bad argument | **read `status`, then act on the answer** (quince#88): `live` → **leave it running**, no second watch is wanted — do *not* run `forge-watch stop` · `wedged` → `forge-watch stop`, then arm · `dead`/`absent` → **arm again.** Bounded at **two arm attempts per turn** — a third refusal is a report, not a loop. |
| **6** | `--max-wait` idle bound | nothing happened, which is a report and not a silence — **re-arm** |
| **7** | `--fail-after` failing ticks in a row | fix the cause the events name, then **re-arm** |

`status` is a different question with its own exit codes: **0** live · **9** starting · **3** dead · **4** absent · **5** wedged.
An exit of **2** is not the tool's — it is jq failing underneath and the script
aborting, so read the error rather than looking it up here.

**0, 6 and 7 are re-arms; 1 sends you to `status` before you decide.** A watch that exited is a watch
that is not watching, and four forgotten re-arms are already on the record (quince#43). This paragraph
used to end *"re-arming into a refusal loops (quince#75)"* and stop there — true of quince#75's
**unconditional** loop, and it over-corrected into never re-arming at all: five losses of the watch in
one session came from *not* arming because something was live, none from arming when nothing should
have been (quince#88). The `status` read terminates on `live` and on `wedged`, which is exactly what
quince#75's loop lacked, and the two-attempt bound is belt over braces.

**Arm unconditionally — never gate an arming behind a shell pre-check.** `watch`'s refusal is atomic
with the act it guards; a conditional beside it is check-then-act across a window in which the watcher
can die. Both `status …; exec watch …` (which gates nothing, because `;` sequences rather than
conditions) and the correctly-composed `if` form were measured failing. §0's duty is unchanged: read
`status` and **say which of the six answers you got**. The window is narrowed, not closed — the `Stop`
hook is the declared backstop, which is not a resolution.

Arm the `ScheduleWakeup` fallback too, at ≥1200 s — but
**do not treat it as the floor.** On the architect box it has delivered nothing across every arming
measured (quince#62). On **this** box it has delivered **once, about an hour after it was due**, which
is the only measurement there is here and is not a cadence you can plan against. The floor under you is
`watch`'s own `--max-wait`, which is measured to fire.

The earlier version of this paragraph said it *"has delivered nothing across every arming measured to
date"* — unscoped, from architect-box measurements, in the file the implementer reads. It was
falsified on the runner within the hour, by the very heartbeat it described. **A measurement carries
the box it was taken on**; strip that and it becomes a claim about a machine nobody tested.

**Your harness will report exits 6 and 7 as "failed".** A background task that exits non-zero is
rendered as *"failed with exit code 6"*, and 6 is the tool's designed idle heartbeat. Read the last
line of its output, which names the exit and the reason, before concluding anything broke.

**You are not trusted to remember this.** Ending a turn with an open PR you authored and no live watch
is blocked — once — by a `Stop` hook that runs `bin/forge-watch owed` and hands you the exact command;
end the turn again and it stops blocking and tells the Operator instead. It exists because a session
that had this instruction in prose armed nothing at all and stopped on *"the ball is back with the
reviewer"*, four minutes before its verdict landed (quince#62). If you have genuinely finished, the
second attempt goes through — the gate makes an unwatched stop visible, not impossible.

Then keep going on what the forge says:

| What you see | What you do |
| --- | --- |
| `CHANGES_REQUESTED` | fix it, push **rebased onto current `main`**, and reply on the PR saying what changed. A fix nobody can find is a fix that gets re-reviewed from scratch. |
| a comment with **no verdict** | answer it. Do not wait for a review that the commenter may be waiting on you to earn. |
| red checks | classify before acting: infrastructure, a known flake with an issue, or real. An unclassified red is an unread claim. |
| `APPROVED` | **keep watching until MERGED.** An approval is not a landing: a rebase can still be asked for, and a merge elsewhere can put you `BEHIND`. |
| merged | next PR in the slice; when the slice is done, the DoD tail — deploy line, click-list, journal entry — then report. |
| nothing, for a long time | that is a stall, and a stall is reported with what you were waiting for. |

Three things that look like signals and are not:

- **`reviewDecision` does not move when you fix something.** It still reads `CHANGES_REQUESTED` until a
  new review lands. Waiting for it to change is waiting for something that cannot happen.
- **`updatedAt` says WHEN, never WHO.** Fetch the actor before concluding anything about turns; a
  session read a bare timestamp, assumed the latest activity was its own, and reported "nothing owed
  from me" with three items owed.
- **Green checks after a changes-requested fix are exactly what changes whose turn it is** — the
  opposite of what they mean while a PR awaits its first review.

**Rebase when the ball is yours; hold still when a verdict is in flight.** Do not force-push while a
review may be being written, or the approval can attach to a commit nobody read. But a push in response
to changes-requested goes out rebased, and so does a branch that went `BEHIND` because something else
merged — the ball came back to you without anyone handing it over.

**A privacy sweep is a claim about a specific head, and it expires on your next push.** If the box you
are on has no private layer the gate exits **`2` — DID NOT RUN**, saying *"this is NOT a clean result.
Nothing was swept."*: do **not** tick the box, declare the sweep owed and name the head. Then say when
the head is final, so whoever runs the sweep is not racing you.

*(This paragraph used to say the gate "prints `skipped` and exits 0 having checked nothing" — the
behaviour quince#41 removed, and the direct opposite of what §3 of this same skill says. One skill
asserting both the pre- and post-fix behaviour is devlog#54's drift, inside a single file. Measured on
a layer-less clone before correcting it: exit **2**, and the first attempt to measure it read a
`tail` pipeline's exit `0` instead of the script's — devlog#27's class, on the third occasion today.)*

**Count the stalls.** When the host client drops, `Read`/`Write` fail after exactly ten minutes with
`PreToolUse hook did not respond before its timeout` while `Bash` keeps working — one session lost ~84
minutes of 140 that way, retrying the same tool. After the first one, read with `cat`/`sed -n`/`awk` and
write with a heredoc, and state **"lost N minutes to M unanswered hook calls"** in your report. Absorbed,
it looks like a slow machine, and the first hypothesis it produces is wrong.

The only legitimate stops: everything merged and the tail done; a decision that is the Operator's; or an
unruled gap. In the last two, say exactly what would unblock you — **and record the park on the PR
itself, not only in your report.** A stop that cannot be seen from the forge is how a held, approvable
PR waited 64 minutes for a confirmation that had already been posted: the reviewer must be able to
discover *your* stop without you, which is the whole reason the forge is the memory. Full protocol and
the reasoning behind all of the above: [`../../loop-protocol.md`](../../loop-protocol.md).
