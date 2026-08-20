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

**Everything on this seat goes through `bin/gh-review`** — reads, writes and the merge alike
(`/architect` §1). There is nothing to choose.

**ONE CREDENTIAL MEANS ONE WRAPPER.** `bin/gh-arch` is retired (quince#676, Operator ruling
2026-08-07), so every read and every write on this seat goes through `bin/gh-review`. There is no
read-versus-write choice to get wrong, and the error that choice used to permit — approving, merging
or commenting through the PAT, re-creating quince#47 *invisibly, because the output looks
identical* — can no longer be committed here.

**`bin/gh-coder` is NOT an option here, and it used to appear in §2.** It is the implementer wrapper
and it **refuses outright** on this box: it dies if a reviewer App key or an architect token is
present, which is the definition of the architect box. **That refusal is the two-seat boundary
working**, so a session meeting it must not "fix" it by moving a credential — it must use
`bin/gh-review`.

**This skill is written for the ARCHITECT seat**, which is whose action merging is. An implementer
session uses `bin/gh-coder` for everything (`/kickoff` §1) — so do not read the wrapper name as
universal; read it as *this seat's*.

## 1. Preconditions, checked not assumed

```sh
bin/gh-review pr view <n> --repo novkostya/quince \
  --json number,title,author,reviewDecision,mergeStateStatus,mergeable,isDraft,files
bin/gh-review pr checks <n> --repo novkostya/quince
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
bin/gh-review pr checkout <n> --repo novkostya/quince
git fetch origin main
make privacy-check REF=origin/main...HEAD

# in quince-devlog — no Makefile exists there. Run the product checkout's script FROM the devlog
# clone; do NOT pass --patterns, which defaults to ./local and so finds this clone's own symlink.
bin/gh-review pr checkout <n> --repo novkostya/quince-devlog
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
bin/gh-review pr list -R novkostya/quince --json number,baseRefName \
  -q '.[] | select(.baseRefName=="<this PR head branch>") | .number'
```

**If it returns anything, retarget those PRs to `main` FIRST** — `bin/gh-review api -X PATCH
repos/novkostya/quince/pulls/<n> -f base=main` — and only then merge. Retargeting is legal while the
dependent is still open and **impossible one second after it is not**, which is the whole window.
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

**On a refusal the ladder is AUTO-MERGE, then the OPERATOR** — Operator ruling 2026-08-07
(quince#676), replacing devlog#52's `bin/gh-arch` fallback, which named a credential that no longer
exists:

1. **`bin/gh-review pr merge <n> --repo <r> --auto --rebase`** — GitHub merges when required checks
   pass;
2. **the Operator merges**, when auto-merge cannot be enabled or is not appropriate.

A ladder exists because the harness classifier refuses the merge verb *intermittently* and leaves no
trace on the forge, so without one a session concludes the App cannot merge and escalates.
**Auto-merge keeps devlog#52's reasoning rather than overturning it** — that ruling avoided an
Operator merge because *a merge carries no verdict*, the judgement being the approval, which is
structurally the App's; auto-merge executes it **as the App**, so the attribution is preserved on the
primary path and spent only on the backstop.

**Two things to know before reaching for it.** `allow_auto_merge` is **`true` on BOTH
`novkostya/quince` and `novkostya/quince-devlog`** — the Operator enabled the devlog's on 2026-08-07,
so there is no repo left where the path is retry-then-Operator. And **the App CAN enable auto-merge — measured
2026-08-07 on quince#692**, `enabledBy: quince-review`, `mergeMethod: REBASE`, read back through the
API rather than inferred from an exit code. **It also FIRED**, merging 4m23s later with no session
awake, which is the half worth knowing: the ladder's primary rung works unattended.

**Pick the target deliberately — the probe made this the useful part.** Arm it on a PR that is
**approved with checks running**; that is the case it exists for. A **`BEHIND`** branch is the one to
avoid: auto-merge does **not** rebase, so under `strict: true` an auto-merge armed on one waits
forever. Rebase first, then arm — which also binds your approval to the head that will actually merge
(§4). A PR that is already `CLEAN` is not a probe target either; just merge it.

**Run §2's stacked-PR check at ENABLE time, not at merge time.** Auto-merge fires later and
unattended, and `delete_branch_on_merge = true` repo-wide means the branch goes on every merge
regardless of flags (quince-devlog#214). A PR stacked after you enable and before it fires is
covered by no guard.

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
- Close the issue this PR resolved (`bin/gh-review issue close <n> --comment "landed in #<pr>"`),
  and file follow-ups for anything the review deferred, so nothing survives only in a session.
- Delete the scratch clone. The next unit of work starts from a fresh one.
- Report in one line: what landed, the commit range, what is still owed and by whom.
