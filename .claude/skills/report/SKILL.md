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

```sh
make privacy-check                                    # staged diff
git diff main...HEAD | grep '^+' | grep -inEf local/privacy-patterns.txt   # whole branch
git log main..HEAD --format='%s%n%b'                  # commit messages are public too
```

Then re-read the PR text and journal entry you are about to write against the same rule:
no hostnames, LAN IPs, MACs, topology, hardware sizing, UDIDs/serials, personal paths, or
lab-log excerpts. On a box without the pattern file the target no-ops — sanitize by hand.

## 3. Deploy + click list (DoD)

The default is a dev deploy of the built image in demo mode with the URL in the PR, plus a
≤5-line what-to-click list. Until the dev-CT tooling exists, `/qa` is a placeholder — so:

- run the demo locally on the container host and click it yourself (`/qa` has the exact
  command), then post the click list with the honest line **"no dev-deploy URL — tooling
  pending (pr.4)"**;
- or, for a change with nothing runnable in it (docs, config), write **"deploy leg not
  applicable: no runnable change"**. Not applicable and skipped are different words; use
  the true one.

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
