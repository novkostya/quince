---
name: land
description: Land an approved quince PR — verify the preconditions for real, re-sweep privacy, rebase-merge, then flip the devlog state and clean up. Use when a PR is approved and its checks are green.
argument-hint: "[pr-number]"
disable-model-invocation: true
---

# /land $ARGUMENTS

Merging is the architect's action, and `main` is protected: a PR lands only with an
approval that is not its author's, all required checks green, and linear history. Verify
each of those from the API — never from memory of what happened earlier in the session.

## 0. Which `gh` this skill means, because it is never the bare one

**Every command below runs through a seat wrapper. Bare `gh` is UNAUTHENTICATED on the box that runs
this skill** (quince#480), and that is correct rather than broken: the reviewer's credential is a key
read at the point of use, never an ambient `gh auth login` session, deliberately, so it cannot leak
into one. `/architect` §1 says a reviewer host is *expected* to look unauthenticated.

Which wrapper is decided by **read versus write**, not by convenience — `/architect` §1:

| | wrapper | why |
| --- | --- | --- |
| **reads** — `pr view`, `pr checks`, `pr list` | `bin/gh-arch` | *"Reading through it is fine"* |
| **writes** — `api -X PATCH`, `issue close --comment` | `bin/gh-review` | approving, merging **or commenting** through `gh-arch` re-creates quince#47 on the box built to end it, **invisibly, because the output looks identical** |
| **the merge itself** | `bin/gh-review` | with the one documented fallback, in §3 |

**`bin/gh-coder` is NOT one of the options here, and it used to appear in §2.** It is the implementer
wrapper and it **refuses outright** on this box — `bin/gh-coder:57,63` dies if a reviewer App key or
an architect token is present, which is the definition of the architect box, and its own refusal
text says *"run gh-arch on the architect box"*. That refusal is the two-seat boundary working, so a
session meeting it must not "fix" it by moving a credential; it must use the wrapper above.

**This skill is written for the ARCHITECT seat**, which is whose action merging is. An implementer
session uses `bin/gh-coder` for everything (`/kickoff` §1) — so do not read these wrapper names as
universal; read them as *this seat's*. Substituting the wrong one is not a style error: `gh-arch`
casting a write is the failure quince#47 exists to prevent.

## 1. Preconditions, checked not assumed

```sh
bin/gh-arch pr view <n> --repo novkostya/quince \
  --json number,title,author,reviewDecision,mergeStateStatus,mergeable,isDraft,files
bin/gh-arch pr checks <n> --repo novkostya/quince
```

Land only when all of these hold:

- `reviewDecision` is `APPROVED`, by someone who is not the author;
- required checks `gates` / `image` / `e2e` are green on the **current** head;
- `mergeable` is true and the PR is not a draft;
- the DoD legs are green or explicitly waived **in the PR thread** (a waiver that exists
  only in a chat session doesn't count);
- the journal entry for this work exists or is queued with the architect.

Anything unmet: stop and say which line failed. Don't "just merge" — the protection would
refuse anyway, and working around it is the disease the process was built to cure.

## 2. Privacy re-sweep of the whole branch

Conflict resolution and review fixes create committed content that never passed the
commit-time gate:

```sh
# in quince
bin/gh-arch pr checkout <n> --repo novkostya/quince
git fetch origin main
make privacy-check REF=origin/main...HEAD

# in quince-devlog — no Makefile exists there. Run the product checkout's script FROM the devlog
# clone; do NOT pass --patterns, which defaults to ./local and so finds this clone's own symlink.
bin/gh-arch pr checkout <n> --repo novkostya/quince-devlog
git fetch origin main
/path/to/your/quince/deploy/privacy/privacy-check --ref origin/main...HEAD
```

This is the command the protocol used to describe without providing: it sweeps the diff and
the commit messages together, and **exits non-zero rather than reporting clean when it could
not sweep at all** (quince#41).

**`make privacy-check` does not exist in `quince-devlog`** — this skill named only that form, so the
gate was unreachable in half the declared forge set (quince#78). Use **your work clone's** copy of the
script rather than the launchpad's: a stale privacy-check is exactly the one that exits `0` having
checked nothing, and the launchpad has been measured stale (quince#33). `cd` to the repository being
swept, not to the one holding the script — `--ref` resolves against the current directory's git repo.
Full reasoning in `/report` §2.

`0` clean · `1` a match — do not merge · **`2` DID NOT RUN**, which is not permission to
merge. A `2` means this box has no usable pattern list, so the re-sweep is **owed** and the
merge waits on someone who can run it, naming the head they swept.

## 3. Merge

**FIRST, CHECK FOR PRs STACKED ON THIS ONE** (quince#388). `--delete-branch` on the base of an open
stacked PR **silently closes it** — one second later, no warning, nothing in the merge output — and
`forge-watch` reports the dependent as `DIRTY`, which is the shape §5 of `CLAUDE.md` tells you to
leave alone. One command, and it costs nothing when the answer is empty:

```sh
bin/gh-arch pr list -R novkostya/quince --json number,baseRefName \
  -q '.[] | select(.baseRefName=="<this PR head branch>") | .number'
```

**If it returns anything, retarget those PRs to `main` FIRST** — `bin/gh-review api -X PATCH
repos/novkostya/quince/pulls/<n> -f base=main` — and only then merge. Retargeting is legal while the
dependent is still open and **impossible one second after it is not**, which is the whole window.
The retarget is a **write**, so it is `gh-review` and not `gh-arch`, even though the check one line
above is `gh-arch` — that is the split in §0, at the one place the two commands sit adjacent.

**It is irrecoverable the moment that author pushes**, which is their natural next action once the
base lands: two `422`s close the door in sequence, and recreating the base ref only reveals the
second one. The commits survive; the pull request and its verdict do not. Measured on quince#384,
which was approved and had to be reopened as quince#387.

Rung 1 of `CLAUDE.md` §*How work runs* says **sequence, do not stack**, so this should find nothing.
It is here for when somebody stacked anyway, or when a stack predates that ruling — and **the
merging seat is the one who cannot see it**, because a base branch that is not `main` is easy to
miss in `pr view` output. Deleting the branch is not the defect and branch hygiene is worth keeping;
doing it while a dependent is open is.

```sh
bin/gh-review pr merge <n> --repo novkostya/quince --rebase --delete-branch
```

**On a refusal: retry once, then merge through `bin/gh-arch` and say so on the PR** — Operator
ruling, devlog#52. This is the ONE place `gh-arch` may act where §0 otherwise forbids it, and it is
narrow: **merging only**, never approving, requesting changes, or commenting. It exists because the
harness classifier refuses the merge verb *intermittently* and leaves no trace on the forge, so
without it a session concludes the App cannot merge and escalates. `gh-arch` rather than the
Operator, because **a merge carries no verdict** — the judgement is the approval, which is
structurally the App's, and the merge only executes it.

Rebase-and-merge is the default; squash is acceptable for a noisy branch; merge commits are
disabled. If GitHub refuses because the branch is behind, rebase the **branch** on fresh
`main`, re-run the gates on the rebased tip (a textually clean rebase can still be
semantically broken by canon that moved underneath), push the branch, and merge again.
Never force-push `main`, and never resolve a refused fast-forward with a merge commit.

## 4. Verify the landing

```sh
git fetch origin main && git log --oneline --graph origin/main -5
```

Linear history, your commits on top, nothing unexpected in between. Note the landed commit
range — the journal entry cites it.

## 5. Aftercare

- Devlog: flip the **one-line state** and the rung's dashboard row; make sure the journal
  entry cites the merged PR number and the landed commit range (`/report` step 6 covers who
  can commit it and how to check).
- Close the issue this PR resolved (`bin/gh-review issue close <n> --comment "landed in #<pr>"` —
  a **comment**, so `gh-review`, not `gh-arch`), and file follow-ups for anything the review
  deferred, so nothing survives only in a session.
- Delete the scratch clone. The next unit of work starts from a fresh one.
- Report in one line: what landed, the commit range, what is still owed and by whom.
