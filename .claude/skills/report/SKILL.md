---
name: report
description: Turn finished quince work into the two artifacts that record it — a PR description carrying the evidence, and a date-anchored devlog journal entry citing the PR number. Use when a unit of work is built and proven and you are ready to open or update its PR.
argument-hint: "[optional: PR number to update]"
disable-model-invocation: true
---

# /report $ARGUMENTS

The build report *is* the PR description. There is no separate report document and no
report in chat: a reviewer must find everything in the PR, and a stranger must find the
claim in the journal.

**State honesty applies hardest here.** Every claim in these two artifacts is either
something you ran, or is labelled as not run with an owner. No fabricated URLs, no
"should work", no ticked box you didn't earn.

## 1. Gather the evidence

- Gate ladder, on the container host, from a clean tree: `make gates`, plus `make image`
  and `make gates-ui-e2e` when the diff touches the UI, the embed, or the image. Keep the
  exact commands and their results — a tail of real output beats a summary.
- The spec's own acceptance gates, one line of result each.
- Coverage: the `go test -cover` summary, plus the **known-untested list** — one line and
  reason each. Declared untested is accepted debt; undeclared untested behavior that a
  reviewer finds is a finding against you.
- Anything only hardware can prove: name it as **owed**, with the owner and the rung.

## 2. Privacy sweep — the whole branch, not just the last commit

One command, and it covers the branch diff, the commit messages **and** the PR text you are
about to post — write the body to a file first and hand it over:

```sh
# THE BODY PATH IS PER-RUNNER. Never a fixed path under /tmp — see below.
BODY="$HOME/scratch/$(bin/forge-watch runner get)/pr-body.md"

# in quince
make privacy-check REF=origin/main...HEAD TEXT="$BODY"

# in quince-devlog — THERE IS NO MAKEFILE THERE. Run the product checkout's script FROM the
# devlog clone, so that the pattern directory resolves to this clone's own `local` symlink:
/path/to/your/quince/deploy/privacy/privacy-check --ref origin/main...HEAD --text "$BODY"
```

**`/tmp/pr-body.md` was canon until 2026-07-29, and it is a collision.** One host runs several
implementers (quince#111), so a fixed path under `/tmp` is a **shared mutable file**. Measured that
day: `r1` went to write its body and found `r2`'s in-flight body for another PR already there,
minutes old. One write would have replaced it.

**The data loss is the smaller half.** The bigger one is that `TEXT=` would then have swept *the
wrong session's* text — reporting `clean` over a body the other session believed was covered, while
its actual body went unswept. A gate that answers about something it never saw is the failure this
project files more than any other, and here it arrives through a filename.

`$HOME/scratch/<runner>/` is the right home because it is already per-runner and already reaped by
`bin/scratch-reap`. **Not `mktemp`**: the path has to survive between the write and the sweep and the
`--body-file`, which are separate invocations, so it must be predictable as well as unique.

**`make privacy-check` does not exist in `quince-devlog`.** All three skills named only that, so the
gate was unreachable in half the declared forge set and was complied with in words for as long as
nobody tried it (quince#78). No Makefile was added there, deliberately: it would exist to wrap one
script, in a repository with no build, and be a second place for the invocation to drift from the tool.

Two things the form above gets right, both learned by getting them wrong:

- **Do not pass `--patterns`.** It defaults to `./local`, relative to the **current directory**, and
  the devlog clone carries the same `local` symlink. Handing it a *file* (`local/privacy-patterns.txt`)
  rather than the *directory* makes the gate exit **`2` — DID NOT RUN**, which is not a clean result.
- **`cd` to the repository being swept, not to the one holding the script.** `--ref` is resolved
  against the current directory's git repository; where the script lives is irrelevant to it.

**Which copy of the script: your work clone's**, not the launchpad's. Both are defensible and they fail
differently, so it is chosen rather than defaulted. A **stale** privacy-check is precisely the one that
exits `0` having checked nothing — the defect quince#41 fixed — and the launchpad has been measured
stale, at a commit predating a file entirely (quince#33). Your work clone's provenance is known: you
cloned it this session. If you are working **only** in the devlog and have no quince clone, the
launchpad's copy is the fallback — **and say which you used**, because the two are not interchangeable.

Then re-read the PR text and journal entry against the same rule: no hostnames, LAN IPs,
MACs, topology, hardware sizing, UDIDs/serials, personal paths, or lab-log excerpts.

**Read the exit code, not the silence.** `0` clean, and it names what it swept · `1` a
match, with source and line · **`2` DID NOT RUN** — no pattern list, an empty one, an
unusable matcher. A `2` is not a clean result and must never be reported as one: on a box
without the private layer that is exactly what you will get, and the sweep is then **owed**,
with the head named, not done (quince#41).

**And read WHAT it swept, not only that it was clean.** A clean run ends
`swept branch-diff commit-message branch-name text against N patterns` — that list is an
**assertion about coverage**, and a short one is a signal rather than a formality:

| ends with | means |
| --- | --- |
| `branch-diff commit-message branch-name text` | the whole claim: diff, messages, branch, PR body |
| `branch-name` **only** | **there is no diff and no commit** — you are sweeping an empty branch |
| no `text` | the PR body was never swept; `TEXT=` was omitted |

Found by getting it wrong: a failed rebase short-circuited an `&&` chain, the commit never ran,
and the sweep reported **clean** — truthfully, over nothing. The exit code was `0` and the
coverage list was one word. `0` answers *did anything match*; only the list answers *was
anything looked at*, and this project has already been bitten once by a gate whose satisfied
answer could not be distinguished from a gate that never ran.

## 2b. Closing-keyword sweep — TWO surfaces, and quoting the trap is itself an instance

**A closing keyword next to a bare `#N` auto-closes that issue on merge, and GitHub's parser is not
negation-aware.** Writing *"does not close #N"* links the PR as closing `#N`. So does explaining the
bug in a commit message.

Sweep both surfaces, in the same shape as the privacy sweep above — that gate already got this right
and this one should not have to relearn it:

```sh
KW='(close[sd]?|fix(e[sd])?|resolve[sd]?)[^.]{0,20}#[0-9]+'
git log --format=%B origin/main..HEAD | grep -inE "$KW"    # the commit messages — TWO dots
grep -inE "$KW" "$BODY"                                    # the PR body
```

Whatever either one prints, you either meant it or you did not — decide deliberately.

**TWO dots here, THREE in the privacy sweep, and the difference is not a slip.** `A...B` is the
symmetric difference, so it also reports commits already on `main` — and this check ran against its
own branch and flagged a **merged** commit, which is somebody else's problem and already history. The
privacy sweep wants that breadth, because sweeping too much costs nothing and missing a leak is
permanent. This one wants the opposite: a hit you cannot act on is noise, and a check that cries wolf
on merged work is a check people learn to skim. Measured on this branch — `...` reported 1 hit,
`..` reported 0, and 0 was the true answer.

**`closingIssuesReferences` is NECESSARY AND NOT SUFFICIENT.** It reflects the **PR body only**. A
commit message landing on the default branch closes issues too, and with `--rebase` every commit
message is in scope rather than just the merge subject. Measured: a PR whose body had been cleaned
still closed the issue on merge, from the message of the commit that did the cleaning, while
`closingIssuesReferences` read `[]` throughout.

**What is measured safe** — 2026-07-30, on a live open PR, body edited and re-read:

| form | links? |
| --- | --- |
| the keyword beside a bare `#N` in prose | **yes**, including inside *"does not …"* |
| the same in a commit message | **yes**, and invisible to `closingIssuesReferences` |
| backticked inline, or inside a fenced block | **no** |
| repo-qualified — the keyword beside `quince#N` | **no** |

Three consequences, and only the first is obvious:

- **To quote the trap safely, backtick it or qualify it.** This section does both throughout.
- **A repo-qualified reference never auto-closes.** The project's own citation convention is
  inherently safe — and it means a PR written that way must close its issue by hand, or use a bare
  reference on purpose.
- **THE TWO SURFACES CAN DISAGREE, AND THE FIELD ONLY SEES ONE.** A body written the qualified way
  and a commit message written the bare way is a PR that *will* close on merge while
  `closingIssuesReferences` reads `[]` the whole time. Measured on quince#291: qualified in the body,
  bare in the message, field empty, and the issue closes. So *"I checked the body"* and *"I checked
  the field"* are the same incomplete answer — which is how three people, twice, concluded a link was
  gone when it was not. **Read both, every time; they are not two views of one fact.**

**Knowing the keyword list does not protect you.** Three instances, each by someone who knew: a body
disclaiming the close; the body of the PR *fixing* that, written by the author of the diagnosis; and
the commit message of the commit that removed it from that body. Each time the knowledge was aimed at
the surface that had bitten last. The trap is not the list — it is that **reproducing the offending
text is itself an instance**, so write about it backticked or qualified, never bare.

## 3. Deploy + click list (DoD)

**Deploy by default. Don't ask, don't wait to be asked:**

```sh
make demo                                        # builds THIS branch and serves it, on this box
```

It builds the production image **on this box**, serves it in `--demo` mode, waits for
`/api/health` to actually answer, and prints what it verified. **One line goes in the PR:**

- **the convention URL** — `http://quince-runner:<port>/`, which the tool prints ready to
  paste. The port is ALLOCATED, not fixed: `demo` tries `8080` and increments until one
  binds, because two runners serving demos on one box is the ordinary case (quince#45), so
  do not write `8080` from memory — paste the line the tool printed.
- the loopback address it fetched (`127.0.0.1:<port>`) — **never** in PR text. It is what
  this box verified, not something a reader can open.

**This paragraph described `devct deploy` until 2026-07-29 and every sentence in it was
wrong.** It said the build happened *on a dev container*, gave the URL as
`http://quince-dev-N:8080/`, and told you to paste an `ssh -L` line — while `/qa` §2, added
in the same PR that replaced the tool, already recorded that **there is no `ssh -L` line any
more**. Two halves of one change, and only one of them was finished: the skill that
PRODUCES the PR text kept instructing sessions to paste output that no tool emits.

Then click it yourself and write ≤5 imperative lines a reviewer can follow. Demo mode
asks for an admin password first — that is line 1 of most click lists.

**When there is no URL, say which of these two is true.** They are different, and the
second one exists so the first cannot quietly cover for it:

- **`deploy: not applicable — no runnable change`** — docs, config, spec. Nothing to click.
- **`deploy: unavailable — <reason>`** — a container could not be had, the build failed,
  the demo never answered. Name the reason; it is a finding, not a formality.

An error message is a claim, and so is a missing one (program doc, *State honesty*).

## 4. Write the PR description

```markdown
**What & why**
<the one reviewable claim of this PR, and the problem it solves>

**Evidence**
<commands run and their results; the spec gates, one line each>

**Coverage**
<`go test -cover` summary> · known-untested: <one line + reason each>

**Deploy + what to click**
<URL or the honest not-applicable/pending line>
1. …  (≤5 lines)

**Not proven / not done here**
<owed gates with owners, deliberate omissions, anything a reviewer might assume is covered>

**Review ask**
<architect for code; Operator for architect-authored docs/canon — approver is never the author>

**Checklist**
- [ ] CI green (gates / image / e2e)
- [ ] Privacy swept — no LAN IPs, serials, UDIDs, or secrets in code, fixtures, or docs
- [ ] Contracts untouched, or the contract change was approved before this PR
```

Tick only what is true at the moment you write it. CI has not reported yet when you open
the PR — leave that box empty and tick it once the checks come back.

## 5. Open or update the PR

```sh
bin/gh-coder pr create --repo novkostya/quince --base main --head <branch> \
  --title "<qn.N|pr.N>: <claim>" --label <process|bug|enhancement> --body-file <file>
```

**Never `export GH_TOKEN=$(cat …)`.** This block did exactly that until 2026-07-29, against
`quince-bot.token` — an account suspended on 2026-07-28, so a session following it verbatim
exported a dead credential and discovered it at the one moment it was ready to publish. The
wrapper mints a fresh installation token per call and keeps it out of argv, which is also why an
allowlist rule can match it: a rule never matches past a leading `VAR=value`.

Updating a body afterwards: `gh pr edit` fails on a `repo`-scoped token (its GraphQL query asks
for org fields). Use REST instead — and the App has the same limitation for the same reason:

```sh
bin/gh-coder api -X PATCH repos/novkostya/quince/pulls/<n> -F body=@<file>
```

`-F body=@<file>` rather than `-f body="$(cat <file>)"`: a body read through command substitution
lands in argv, and a PR body is the one artifact most likely to contain something a privacy sweep
has just cleared.

Then watch the checks: `gh pr checks <n> --repo novkostya/quince`. Red checks are yours to
fix before asking for review.

## 6. The journal entry

Append to the devlog `progress.md` decisions log — at the **bottom**, newest last — in its
established bullet style, **date-anchored and citing PR/issue numbers**. Letters `(a)`–`(do)`
are retired; never mint one.

```
- 2026-07-25: **the claim in one bold sentence** — what changed, what was proven (name the
  gates), what is owed and by whom. ([#12](https://github.com/novkostya/quince/pull/12))
```

Also flip the **one-line state** at the top and the rung's dashboard row if the frontier
moved, and add any new open question to the dashboard's list.

Whether an implementer session can commit this itself depends on access that changes over
time, so check instead of assuming:

```sh
gh api repos/novkostya/quince-devlog -q .permissions           # is push true?
gh api repos/novkostya/quince-devlog/branches/main/protection  # 404 = not protected
```

Write access and an unprotected branch → clone the devlog, commit the entry under the same
bot identity, push. Otherwise → a devlog PR, or the exact text in the product PR thread for
the architect to commit. Never silently drop the entry, and never say it landed when it
hasn't.

## 7. Close the loop

Say in one line: which DoD legs are green, which are open, and who is expected to act
next.
