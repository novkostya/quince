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
# in quince
make privacy-check REF=origin/main...HEAD TEXT=/tmp/pr-body.md

# in quince-devlog — THERE IS NO MAKEFILE THERE. Run the product checkout's script FROM the
# devlog clone, so that the pattern directory resolves to this clone's own `local` symlink:
/path/to/your/quince/deploy/privacy/privacy-check --ref origin/main...HEAD --text /tmp/pr-body.md
```

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

## 3. Deploy + click list (DoD)

**Deploy by default. Don't ask, don't wait to be asked:**

```sh
deploy/devct/devct deploy --ref <this branch>     # add --create if no container is running
```

It builds the production image on a dev container, serves it in `--demo` mode, and prints
three lines. **Exactly one of them goes in the PR:**

- **the convention URL** (`http://quince-dev-N:8080/`) — this is the one to paste;
- the address — **never**; it is Operator-private and the tool labels it session-only;
- the `ssh -L` line — paste it *below* the URL as the address-free path for a reader who
  has the alias but not the LAN.

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
export GH_TOKEN=$(cat ~/.config/quince/quince-bot.token)
gh pr create --repo novkostya/quince --base main --head <branch> \
  --title "<qn.N|pr.N>: <claim>" --label <process|bug|enhancement> --body-file <file>
```

Updating a body afterwards: `gh pr edit` fails on the bot's `repo`-scoped token (its
GraphQL query asks for org fields). Use REST instead:

```sh
gh api -X PATCH repos/novkostya/quince/pulls/<n> -f body="$(cat <file>)"
```

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
