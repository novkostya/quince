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

If the author is the identity you are acting as, **stop**: say who must approve instead
(architect-authored docs/canon → the Operator; bot-authored code → the architect). Also
stop if the branch touches `.github/workflows/**` and was pushed by the bot — that push
should not have been possible; flag it.

## 1. Read the claim

The PR body, the linked issue or spec, and the diff:

```sh
gh pr diff <n> --repo novkostya/quince
```

Ask first: **is this one reviewable claim?** A PR carrying three unrelated claims is
mis-scoped — say so early rather than reviewing it anyway.

## 2. Run it — reading produces opinions, running produces findings

```sh
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
- Privacy: `git diff main...HEAD | grep '^+' | grep -inEf local/privacy-patterns.txt`, plus
  the commit messages (`git log main..HEAD --format='%s%n%b'`), plus the PR text itself. On
  a box without the pattern file, read for it by hand.
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
