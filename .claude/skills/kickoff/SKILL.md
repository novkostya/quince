---
name: kickoff
description: Take one unit of quince work from issue to merged — resolve the target, read the state and canon it touches, take a fresh clone and branch as quince-bot, produce the PR-slicing plan, then open the PRs and work them through review until they land. Use at the beginning of an implementer session, and when an event wakes a session onto an existing PR.
argument-hint: "[issue-number | qn.N | pr.N | free-text scope]"
disable-model-invocation: true
---

# /kickoff $ARGUMENTS

Take one unit of work and carry it to merged. Self-onboard, plan, **post the plan and keep going** —
the plan is a checkpoint, not the deliverable.

## 0. A watch may already exist, and it may already be dead

If this session is resuming work — a `/kickoff <pr>` woken by an event, or a session whose process
restarted — ask before arming anything, and **say which of the four answers you got**:

```sh
bin/forge-watch status --repo novkostya/quince   # 0 live · 3 dead · 4 absent · 5 wedged
```

`live` → nothing to do, and do not arm a second watcher. **`dead` → re-arm from that state and do NOT
reseed it**; the next tick emits everything that accrued while nothing was watching, and you say that a
watch was found dead. **`wedged` → a watcher is still running and has stopped ticking: run
`bin/forge-watch stop --repo <r>`**, then re-arm — never a bare `kill`, because the pid is only known
to be *ours* while its heartbeat is fresh and `wedged` means it is not. `absent` → cold start.

Collapsing `dead` into `absent` turns a restarted watch into a fresh one that has "seen nothing changed"
since a beginning it invented; collapsing `wedged` into `dead` tells you to start a second watcher
beside a live one. Reasoning: [`../../loop-protocol.md`](../../loop-protocol.md).

## 1. Resolve the target

**Use `bin/gh-bot`, not bare `gh`.** It is *the* tool for every forge call on this side: an allow rule
never matches past a leading `VAR=value`, so `GH_TOKEN=$(cat …) gh …` is unallowlistable by
construction and prompts every time. Both implementer sessions in the first two-box run opened with
bare `gh`, had it denied, and rediscovered the wrapper — so it is named here rather than left to be
found.

- A number → `bin/gh-bot issue view <n> --repo novkostya/quince --comments` (try the devlog repo
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
- prior art: `bin/gh-bot pr list --repo novkostya/quince --state merged --limit 20` and
  `git log --oneline -- <the tree you will touch>`;
- the devlog `proposals.md` declined list, so you don't rebuild a refused idea.

**Open question or unruled gap in your path? Stop there.** Write the `PROPOSED (gap): …`
block per the gap protocol and report; do not build past it.

## 3. Fresh clone + branch, as the bot

One clone per unit of work, from GitHub, in a scratch directory — never a long-lived
checkout, never a worktree, never an rsync from a workstation:

```sh
git clone https://github.com/novkostya/quince.git <scratch>/quince && cd <scratch>/quince
git config user.name  "quince bot"
git config user.email "quince-bot@users.noreply.github.com"
git config credential.helper '!f() { echo username=quince-bot; echo "password=$(cat $HOME/.config/quince/quince-bot.token)"; }; f'
git checkout -b <qn.N|pr.N>/<short-title>
```

The credential helper keeps the token out of argv, the remote URL, and `.git/config`. If
the token file is absent, stop: you cannot author as the bot — say so instead of pushing
under another identity.

On the Operator's machine only, so `make privacy-check` works in the fresh clone:

```sh
ln -s <path-to-the-private-layer>/local local   # the path is gitignored; git can never commit it
```

## 4. Verify where the gates will run

`make help` names the detected container runtime. No runtime → gates cannot run in this
clone; the work has to execute on a container host (`deploy/dev.md`). Do not install a
toolchain anywhere, and do not plan a PR you cannot prove.

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

"I finished a PR" is not a stop. Neither is "over to the architect", which is how the first run ended
one session that then had to be restarted by hand. Record the repo and number of every PR you open —
that is your watch set, self-describing, and it needs no declared file because you know what you
opened — then keep going on what the forge says:

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
are on has no private layer, `make privacy-check` prints `skipped` and exits 0 having checked nothing:
do **not** tick the box, declare the sweep owed and name the head. Then say when the head is final, so
whoever runs the sweep is not racing you.

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
