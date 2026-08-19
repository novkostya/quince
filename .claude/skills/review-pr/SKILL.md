---
name: review-pr
description: Review a quince PR as the architect — run it, pass the four named dimensions, check the process gates, and submit a real GitHub verdict (approve / request changes). Pass a PR number, or "all" to sweep every open PR and end with one consolidated verdict list.
argument-hint: "[pr-number | all]"
disable-model-invocation: true
---

# /review-pr $ARGUMENTS

An approval here is the authority model, not a courtesy: branch protection turns your
approval into a merge. Approve only what you ran and would build on.

**Every command below runs through a seat wrapper; bare `gh` is UNAUTHENTICATED on this box**
(quince#480), which is correct rather than broken — the reviewer's credential is a key read at the
point of use, never an ambient `gh auth login` session. **Reads and verdicts both go through
`bin/gh-review`** — since quince#676 it is this seat's only credential, and the split that sent reads
through `bin/gh-arch` is retired with the wrapper (`/architect` §1). This skill is the
**architect's** — an implementer session uses `bin/gh-coder` (`/kickoff` §1), so read the name as
this seat's rather than as universal.

Where this file *discusses* bare `gh` — *"`gh pr review` has no `--commit-id`"* — it is a statement
**about the unwrapped API**, not an instruction. Those stay bare deliberately: wrapping them would
make the sentences false.

`all` → `bin/gh-review pr list --repo novkostya/quince --state open --json number,title,author` and run
this protocol per PR (cheapest diff first), posting a verdict on each; finish with one
consolidated summary: PR · verdict · the one-line reason. Never batch-approve a set you
reviewed as a set — each PR gets its own run.

## 0. Approver ≠ author

```sh
bin/gh-review pr view <n> --repo novkostya/quince --json author,files,title,body,labels,reviewDecision
```

**The identity that matters is the one that CASTS, and since quince#134 that is
`quince-review[bot]`.** The question is only ever *did the App write this?* — almost never, so the
answer is almost always no and you proceed.

**`novkostya` on the author field is not a reason to stop.** That login covers the Operator and the
Operator's Mac acting as the break-glass seat (CLAUDE.md, "the Operator's Mac is the deliberate
break-glass host"). The forge cannot tell them apart, and neither can you — so do not infer
authorship from it. **It covered a third seat, you, until quince#676 retired `bin/gh-arch`**, and
this paragraph replaces one that read *"if the author is the identity you are acting as, stop"* —
correct while the architect's only identity was `gh-arch` = `novkostya`. Now it is neither: you are
not on that field at all, so a `novkostya`-authored PR is never yours.

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
bin/gh-review pr view <n> --repo novkostya/quince --json author,files \
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
bin/gh-review pr diff <n> --repo novkostya/quince
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
# The private layer, WITHOUT WHICH THE PRIVACY SWEEP CANNOT RUN. `local/` is gitignored and lives
# in the private repo, so a fresh clone never carries it and `privacy-check` exits 2 — DID NOT RUN.
# That is the merge-blocking gate on the reviewer's own seat (quince#240). The path is the
# provisioned location; `provision` and `preflight` both read the same variable, so an overridden
# layer is honoured here too rather than being hardcoded away.
ln -sfn "${QUINCE_PRIVATE_LAYER:-/root/quince-local}" local
bin/gh-review pr checkout <n> --repo novkostya/quince
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

- CI: `bin/gh-review pr checks <n> --repo novkostya/quince` — all required checks green.
- Privacy: one command over the diff, the commit messages and the PR text — **and the form differs
  by repository, because `quince-devlog` has no Makefile** (quince#78):
  ```sh
  # PER-RUNNER, never a fixed /tmp path: one host runs several seats, and a shared body file means
  # the sweep can cover another session's text while its own goes unswept (quince-devlog#123).
  BODY="$HOME/scratch/$(bin/forge-watch runner get)/pr-body.md"
  make privacy-check REF=origin/main...HEAD TEXT="$BODY"                      # in quince
  /path/to/your/quince/deploy/privacy/privacy-check \
      --ref origin/main...HEAD --text "$BODY"                                 # in quince-devlog
  ```
  **`TEXT=` / `--text` take a PATH to a file holding the body, never the body itself.** Write the
  body out first. Passing the prose word-splits it and the gate refuses with a `2` naming the first
  word of the PR as an unreadable filename — which reads like a corrupted invocation rather than a
  wrong argument type (quince#105).

  Run it **from the repository being swept** — `--ref` resolves against the current directory's git
  repo — and do **not** pass `--patterns`: it defaults to `./local`, **which a clone carries only
  because somebody linked it there**, and handing it a file instead of a directory produces a `2`.
  §2 does that for the quince clone; **a `quince-devlog` clone needs the same line**, and this skill
  gives no clone block for one:

  ```sh
  ln -sfn "${QUINCE_PRIVATE_LAYER:-/root/quince-local}" local   # in the devlog clone too
  ```

  This clause used to read *"which both clones carry"*, flatly. That sentence is what stopped a
  reader noticing §2 had no symlink line at all: it asserted the postcondition of a step that was
  missing, so the gap read as satisfied (quince#240). Prefer **your work clone's** copy of the
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

**Note the head oid BEFORE you read the diff**, and pass it as `--commit-id`. It is the same value
`/architect` §4 calls `OLD` — one oid, noted once, used for both the verdict and the staleness check:

```sh
OID=$(bin/gh-review pr view <n> --repo novkostya/quince --json headRefOid -q .headRefOid)   # BEFORE reading

# The verdict body goes to a FILE, is swept, and is passed with --body-file.
BODY="$HOME/scratch/<seat>/bodies/<n>-verdict.md"
cat > "$BODY" <<'MD'
…what you ran, what you checked, and the verdict…
MD
make privacy-check TEXT="$BODY"

bin/gh-review pr review <n> --repo novkostya/quince --commit-id "$OID" --approve         --body-file "$BODY"
bin/gh-review pr review <n> --repo novkostya/quince --commit-id "$OID" --request-changes --body-file "$BODY"
bin/gh-review pr review <n> --repo novkostya/quince --commit-id "$OID" --comment         --body-file "$BODY"
```

**NEVER `-b "…"`, AND THE WRAPPER NOW REFUSES IT** (quince-devlog#249). This block modelled `-b`
three times — the single most-copied command in this workflow — and **backticks in a double-quoted
bash argument are command substitution**. Every body written in this project is dense with them:
identifiers, paths, fenced blocks. Hit twice in one session by `arch1`, and caught only because the
result was checked afterwards: bash substituted before `gh` saw anything, no comment was created,
and **the exit path did not look like a failure**.

`bin/gh-review` refuses `--body`/`-b` on every path including this one — measured, exit `2`, ahead
of even the boundary refusal — so following the old recipe now costs a cycle rather than a silent
non-post. **The refusal is the control; this block is no longer allowed to teach around it.**

A quoted heredoc delimiter (`'MD'`, not `MD`) is what stops substitution inside the file too, and
the file is what `make privacy-check TEXT=` can sweep — the one thing an inline body makes
impossible, since there is nothing to point at.

**`--commit-id` is REQUIRED and `bin/gh-review` refuses without it** (quince#110). `gh pr review` has
no such flag, and the REST endpoint it calls defaults the field to *"the most recent commit in the pull
request"* — so a verdict cast the old way binds to **whatever head exists at the instant of
submission**, not to what you read. The race is invisible from your side: the diff view stays rendered
from the commit you opened. Measured on quince#183 — author amended at `22:00:08Z`, the approval
registered at `22:00:25Z` against a commit seventeen seconds old the reviewer had never seen.

**Read the oid BEFORE the diff, not after.** Taking it afterwards re-creates the race the flag closes:
you would be pinning to a head that may already have moved while you read.

**On a `--comment` it is a declaration, not a compare-and-swap, and NOTHING validates it.** Measured
three ways on a throwaway PR (quince-devlog#261, since closed): a previous head still reachable, an oid
the branch had been force-moved off, and **an oid from another branch that had never been part of the
PR at all** — all three accepted, all three stored and rendered as the commit reviewed. So it *records*
what was read rather than *refusing* a mis-timed verdict, which is what makes §4's staleness comparison
mean anything. It also means a **wrong** pin is silent, and that is why the read-back below is not
optional.

**ON A VERDICT — `--approve` or `--request-changes` — A STALE PIN CAN BE REFUSED OUTRIGHT, AND THE
VERDICT IS THEN NOT RECORDED AT ALL.** Measured on quince#875 (quince#877): `422 … "This pull request
has been updated since you started reviewing"`, wrapper exit 1, and `/pulls/875/reviews` **empty**
afterwards. This paragraph read *"a non-head oid is accepted with no error"* flatly until 2026-08-16 —
a sentence a session follows while holding a verdict it turns out it cannot cast.

**And a stale pin has also been ACCEPTED on a verdict**, measured this session on quince#1063. So the
rule is not *"comments accept, verdicts refuse"* either: **two verdict castings with stale pins, one
refused and one accepted, and nobody has isolated what separates them.** Do not write a general rule
here from either one — that is how the sentence being corrected got here.

**SO READ THE REVIEW BACK. It is the one step that works under every result:**

```sh
bin/gh-review api repos/novkostya/quince/pulls/<n>/reviews \
  -q '.[] | "\(.user.login) \(.state) commit=\(.commit_id[0:8])"'
```

A refusal is loud, but it exits from a wrapper whose output you may have filtered; an acceptance at the
wrong oid is silent. One `GET` distinguishes *cast at what I read*, *cast at something else*, and **not
cast at all** — and only the third is recoverable by simply doing it again.

**The pin's SURVIVAL splits too, and NOT along the same line.** `gh pr update-branch --rebase`
re-points an auto-set `commit_id` to the new head where an explicit one stays put (quince#110). An
explicit pin on a **`COMMENT`** survives an append *and* a rebase-and-force-push unchanged
(quince-devlog#261 — three pins, two head moves). An explicit pin on a **`CHANGES_REQUESTED`** also
stays put across an author rebase-and-force-push (quince#1087, 2026-08-17: the verdict still reads
`commit=f642b789` after the head moved to `80a317d6`). But an explicit pin on an **`APPROVED`**
review has been measured **following the new head** after that same act (quince#775, on quince#774).

**So the line is not `verdict` vs `comment` — one verdict state moves and the other does not.** The
suspect is `dismiss_stale_reviews: true`, set on both repositories: dismissal is the only process
that re-touches an existing review when the head moves, and it applies to **approvals**. Three
states, two behaviours, and a mechanism that predicts the split — but nobody has tested it by
turning the setting off, so it stays a hypothesis here rather than a rule.

**Whatever that turns out to be, take `OLD` by hand and never from `reviews[].commit_id`.** Under the
case that moves, `OLD == NEW`, so §4's range-diff compares a commit against itself and returns a clean
`=` from a check that had nothing to compare — the vacuous-check failure this flag exists to close,
arriving through the path canon called safe.

The body states **what you ran**, not just what you think. If you could not run the gates
(no container runtime in this session), say that in the body and use `--comment`: an
approval that skipped the run is exactly the failure this protocol exists to prevent.

Merging is a separate, deliberate step: `/land`.
