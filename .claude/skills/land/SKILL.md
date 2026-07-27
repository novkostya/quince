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

## 1. Preconditions, checked not assumed

```sh
gh pr view <n> --repo novkostya/quince \
  --json number,title,author,reviewDecision,mergeStateStatus,mergeable,isDraft,files
gh pr checks <n> --repo novkostya/quince
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
bin/gh-bot pr checkout <n> --repo novkostya/quince
git fetch origin main
make privacy-check REF=origin/main...HEAD

# in quince-devlog — no Makefile exists there. Run the product checkout's script FROM the devlog
# clone; do NOT pass --patterns, which defaults to ./local and so finds this clone's own symlink.
bin/gh-bot pr checkout <n> --repo novkostya/quince-devlog
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

```sh
gh pr merge <n> --repo novkostya/quince --rebase --delete-branch
```

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
- Close the issue this PR resolved (`gh issue close <n> --comment "landed in #<pr>"`), and
  file follow-ups for anything the review deferred, so nothing survives only in a session.
- Delete the scratch clone. The next unit of work starts from a fresh one.
- Report in one line: what landed, the commit range, what is still owed and by whom.
