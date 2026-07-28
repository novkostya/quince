---
name: review-pr
description: Review a quince PR as the architect — run it, pass the four named dimensions, check the process gates, and submit a real GitHub verdict (approve / request changes). Pass a PR number, or "all" to sweep every open PR and end with one consolidated verdict list.
argument-hint: "[pr-number | all]"
disable-model-invocation: true
---

# /review-pr $ARGUMENTS

An approval here is the authority model, not a courtesy: branch protection turns your
approval into a merge. Approve only what you ran and would build on.

`all` → `gh pr list --repo novkostya/quince --state open --json number,title,author` and run
this protocol per PR (cheapest diff first), posting a verdict on each; finish with one
consolidated summary: PR · verdict · the one-line reason. Never batch-approve a set you
reviewed as a set — each PR gets its own run.

## 0. Approver ≠ author

```sh
gh pr view <n> --repo novkostya/quince --json author,files,title,body,labels,reviewDecision
```

**The identity that matters is the one that CASTS, and since quince#134 that is
`quince-review[bot]`.** You also hold `gh-arch` and read through it, but reading is not a verdict,
so the login you read with has no bearing on this rule. The question is only ever *did the App
write this?* — almost never, so the answer is almost always no and you proceed.

**`novkostya` on the author field is not a reason to stop.** That login covers three seats: the
Operator, the architect through `gh-arch`, and the Operator's Mac acting as the break-glass seat
(CLAUDE.md, "the Operator's Mac is the deliberate break-glass host"). The forge cannot tell them
apart, and neither can you — so do not infer authorship from it. This paragraph replaces one that
read *"if the author is the identity you are acting as, stop"*, which was correct while the
architect's only identity was `gh-arch` = `novkostya`.

**Read what it actually cost, because the failure mode is not the obvious one.** On
[quince#158][158] — a Mac-authored repair of the gh wrappers — the rule did not block the review.
It **taxed** it: the reviewer met `novkostya`, reported it as "both the architect and the Operator
seat", and then spent an unbounded investigation (reboot timing, `/etc/init.d`, quince#134 and
#136 on attribution) before settling the question from the PR's *prose* — a sentence only a
non-architect could have written. It reached the right answer and approved. **A rule that blocks
gets noticed and fixed; a rule that reliably charges an inference the forge cannot settle does
not.** It had charged two sessions before anyone wrote this paragraph.

| author | who approves |
| --- | --- |
| `quince-bot` — code | you, as the App |
| `novkostya` — code, no owned path | you, as the App |
| `quince-review[bot]` — canon | the Operator, as code owner |
| `novkostya` — **any owned path** | nobody can — see below |

**The one refusal that is real is structural, and it is not about you.** Read the author and the
file list together:

```sh
gh pr view <n> --repo novkostya/quince --json author,files \
  -q '.author.login, (.files[].path)'
```

If the author is `novkostya` **and** any path is owned in `.github/CODEOWNERS`, the PR cannot merge
and your approval will not change that: GitHub refuses a self-approval outright, an App cannot be a
code owner, and `enforce_admins: true` closes the bypass. Say so and stop — not because approver =
author, but because the merge is unreachable from every seat you hold. The escape is an Operator
act (CLAUDE.md, canon repair from the Mac); name it and hand it over rather than approving into a
block.

Also stop if the branch touches `.github/workflows/**` and was pushed by the bot — that push
should not have been possible; flag it.

[158]: https://github.com/novkostya/quince/pull/158

## 1. Read the claim

The PR body, the linked issue or spec, and the diff:

```sh
gh pr diff <n> --repo novkostya/quince
```

Ask first: **is this one reviewable claim?** A PR carrying three unrelated claims is
mis-scoped — say so early rather than reviewing it anyway.

## 2. Run it — reading produces opinions, running produces findings

**Where you check out is not a detail.** `gh pr checkout` needs a clone to already be in, and this
skill used to say nothing about which — so sessions improvised into `/tmp`, where **58 review
clones accumulated in a single day, 161.9 MB, invisible to `bin/scratch-reap`** because they were
outside every root it knows. The reviewer's seat is the heavier user: it clones per PR reviewed,
sometimes twice when a head moves. Use the same root the implementer does, so the reaper covers
both seats (quince#45):

```sh
SCRATCH="$HOME/scratch/$(bin/forge-watch runner get)"
mkdir -p "$SCRATCH" && cd "$SCRATCH"
git clone -q https://github.com/novkostya/quince.git "pr-<n>" && cd "pr-<n>"
gh pr checkout <n> --repo novkostya/quince
make gates                      # + make image / make gates-ui-e2e when the diff earns them
```

Plus the spec's own acceptance gates, and the demo click-through for anything
user-visible. **A docs/config PR still gets run**: follow its instructions literally —
open every link, check every path exists, execute every command it tells a reader to
execute, and load the skills/settings it adds. An instruction that doesn't work is a
defect in a docs PR exactly as a failing test is in a code PR.

## 3. The four dimensions — a deliberate pass over each

1. **Seams** — the surfaces other work will consume behave as documented, proven by
   running them.
2. **Coverage** — verify the PR's known-untested declaration (is it true? is it complete?),
   then hunt untested error and edge branches in the code being added. Every spec story
   should have a test that fails if its behavior breaks.
3. **State honesty** — does anything claim more than was proven? States, logs, UI copy, the
   PR body, the journal entry, a ticked checklist box. This is the dimension most often
   violated by text rather than code.
4. **Contracts** — spot-check the frozen shapes in `docs/contracts.md` that the change
   serves or touches. A contract change that wasn't approved before the PR is blocking.

## 4. Process gates

- CI: `gh pr checks <n> --repo novkostya/quince` — all required checks green.
- Privacy: one command over the diff, the commit messages and the PR text — **and the form differs
  by repository, because `quince-devlog` has no Makefile** (quince#78):
  ```sh
  make privacy-check REF=origin/main...HEAD TEXT=/tmp/pr-body.md               # in quince
  /path/to/your/quince/deploy/privacy/privacy-check \
      --ref origin/main...HEAD --text /tmp/pr-body.md                          # in quince-devlog
  ```
  **`TEXT=` / `--text` take a PATH to a file holding the body, never the body itself.** Write the
  body out first. Passing the prose word-splits it and the gate refuses with a `2` naming the first
  word of the PR as an unreadable filename — which reads like a corrupted invocation rather than a
  wrong argument type (quince#105).

  Run it **from the repository being swept** — `--ref` resolves against the current directory's git
  repo — and do **not** pass `--patterns`: it defaults to `./local`, which both clones carry, and
  handing it a file instead of a directory produces a `2`. Prefer **your work clone's** copy of the
  script over the launchpad's, since a stale one is exactly the one that exits `0` having checked
  nothing (quince#41) and the launchpad has been measured stale (quince#33). Full reasoning in
  `/report` §2. **Exit `2` is DID NOT RUN, not clean**: treat it as an owed sweep, never as a ticked
  box (quince#41). A clean run names the matcher and the pattern count, so you can tell a real sweep
  from a vacuous one. **And if a devlog PR's author reported `make privacy-check`, that sweep did not
  happen** — the command does not exist there, so it is a finding rather than a tick.
- DoD: CI green · privacy swept · review approved · deploy URL (or an honest
  not-applicable/pending line) · ≤5-line click list · journal entry written or handed over.
- Canon: does the change contradict a doc it didn't update? Is there an unruled gap being
  built on?

## 5. Findings triage

- **Blocking** — canon violation, a defect, an unproven claim, or a missing approval →
  `--request-changes`, with the specific fix, file and line.
- **Material but not blocking** → a review comment; if it is a better idea rather than a
  defect, route it to the devlog `proposals.md` channel instead of expanding this PR.
- **Taste** → dropped silently.

## 6. Submit the verdict

```sh
gh pr review <n> --repo novkostya/quince --approve         -b "<what you ran + what you checked>"
gh pr review <n> --repo novkostya/quince --request-changes -b "<blocking findings, numbered>"
gh pr review <n> --repo novkostya/quince --comment         -b "<observations, no verdict>"
```

The body states **what you ran**, not just what you think. If you could not run the gates
(no container runtime in this session), say that in the body and use `--comment`: an
approval that skipped the run is exactly the failure this protocol exists to prevent.

Merging is a separate, deliberate step: `/land`.
