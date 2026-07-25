---
name: kickoff
description: Start one unit of quince work — resolve the issue or rung, read the state and canon it touches, take a fresh clone and branch as quince-bot, and produce the PR-slicing plan. Use at the beginning of an implementer session.
argument-hint: "[issue-number | qn.N | pr.N | free-text scope]"
disable-model-invocation: true
---

# /kickoff $ARGUMENTS

Self-onboard onto one unit of work and set the workspace up. Ends with a plan, not with
code.

## 1. Resolve the target

- A number → `gh issue view <n> --repo novkostya/quince --comments` (try the devlog repo
  too if it isn't there; process work lives there).
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
- prior art: `gh pr list --repo novkostya/quince --state merged --limit 20` and
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

## 5. Plan, then stop

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

**Stop for a go** if the scope is ambiguous, if the work touches contracts / storage
semantics / security / user-visible behavior, or if the Rule check has a line you can't
write honestly. Otherwise start building — and remember the DoD in `CLAUDE.md` is what
"done" means, so plan the deploy + click-list and the journal entry now, not at the end.
