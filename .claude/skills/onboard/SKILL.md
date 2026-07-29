---
name: onboard
description: Resume quince cold — read the public state (devlog progress, roadmap, program doc, canon), verify the tooling honestly, and report where the project stands and what to do next. Use when a session starts with no context, when asked to "pick the project up" or "continue quince", or before taking any work whose context you do not already hold.
argument-hint: "[optional: area to focus on]"
---

# /onboard — the resurrection test as a command

This command exists to be *the* answer to "if the Operator disappears, can someone clone
the public repos, start an agent, and continue?". Run it top to bottom. It is
**read-only**: no branches, no commits, no pushes, no edits.

If a document you need is missing, that is not something to work around — it is a
resurrection-test bug. Say so in the report and offer to file an issue.

## 1. Read the standing instructions

`CLAUDE.md` is already in context. Read `README.md` for the product framing. Note the two
repos: product + canon here, process journal in the devlog.

## 2. Read the state (public, no auth needed)

```sh
# A per-session path, never a shared one. One host runs several implementers, so a fixed
# /tmp/quince-devlog either fails with `destination path already exists` or silently wipes
# another session's clone (quince#239) — the collision /report was fixed for on the same day.
#
# THE FALLBACK IS NOT DECORATION: /onboard is the skill a COLD session runs, and a cold session
# has usually not declared a runner name yet. So it must degrade to something still UNIQUE. A
# silent reversion to the shared name would reintroduce the bug in exactly the case the skill is
# written for.
DEVLOG_DIR=$(bin/forge-watch runner get 2>/dev/null) &&
  DEVLOG_DIR="$HOME/scratch/$DEVLOG_DIR/quince-devlog" ||
  DEVLOG_DIR="$(mktemp -d)/quince-devlog"
mkdir -p "$(dirname "$DEVLOG_DIR")"
git clone --depth 1 https://github.com/novkostya/quince-devlog.git "$DEVLOG_DIR"
```

In that clone, in this order:

- `progress.md` — the **one-line state** at the top (the frontier, what is proven, what is
  owed), then the per-rung dashboard, then as much of the decisions log as the question at
  hand needs. It is long and append-heavy: read the top thoroughly, then search rather
  than scroll.
- `roadmap.md` — the milestones and the next rungs (`qn.N`).
- `program/quince.program.md` — the gate ladder, spec shape, gap protocol, review
  protocol, perf budgets. (On *process* — where work runs, branching, landing — `CLAUDE.md`
  supersedes it; on engineering it is binding.)
- `proposals.md` — skim the **declined** entries: they are the project's accumulated taste
  and they will stop you from re-proposing something already refused.

## 3. Read the canon you will touch

`docs/quince.stack.md` (decisions, `D<N>`), `docs/quince.design.md` (architecture),
`docs/contracts.md` (frozen interfaces — read the headings at minimum),
`docs/ui.design.md`, and the newest spec under `docs/specs/`. Don't read all of it: read
the index and then what the frontier rung touches.

## 4. Read the open work — from the declared set, never a hand-list

```sh
# GH is the READ WRAPPER FOR YOUR SEAT, and naming it that way rather than naming a command is
# the point — /onboard serves three audiences and only one of them can use bare `gh`.
#   on a session box   bin/gh-coder (implementer) or bin/gh-arch (architect) — whichever is there
#   with gh auth login bare `gh`
#   neither            web URLs, and say so
GH=$(command -v ./bin/gh-coder || command -v ./bin/gh-arch || printf 'gh')
for r in $(sed 's/#.*//' .claude/forge-set | grep -v '^[[:space:]]*$'); do
  "$GH" issue list --repo "$r" --state open
  "$GH" pr list   --repo "$r" --state open
done
```

**Bare `gh` fails on BOTH session boxes, and that is measured rather than assumed** (quince#149).
`gh` requires auth even for a public repository, so *"run cold on public repos"* is not a case bare
`gh` serves — it is a case where it fails:

| who | bare `gh` | what they hold |
| --- | --- | --- |
| a cold stranger — the resurrection test | fails, no credential | nothing; web URLs are genuinely the answer |
| the architect box | `exit=4`, empty stdout, *"please run: gh auth login"* | a working `bin/gh-arch` |
| **the runner box** | **`exit=4`, empty stdout, same message** (gh 2.93.0, 2026-07-29) | a working `bin/gh-coder` |

The runner row was open until 2026-07-29 — quince#149 filed it as *"unmeasured — I hold no bot token
and will not guess"*, correctly, because an architect box cannot hold the implementer identity. An
implementer session closed it, and the answer is the same on both.

The reason the wrapper is not named unconditionally is the **resurrection test**: a stranger who
clones the public repos must be able to run this, and hardcoding `bin/gh-coder` would assume a
session host and break the audience the skill exists for. Hence a seat-shaped fallback rather than a
command.

**Enumerating the repos by hand is the bug this closes** (quince#53). The old form hardcoded two and
filtered the devlog to `--label process`; the moment a third repo matters, `/onboard` reports less
open work than exists while looking exactly as authoritative — the quince-devlog#3 shape, a report
that covers a subset and says nothing about the subset. `.claude/forge-set` exists so that cannot
happen: `bin/forge-watch --all` and `/architect` §3 already read it and hard-fail when it is absent
rather than falling back to one repo. The `--label process` filter is dropped so every declared repo
is listed the same way — an unfiltered "what is open" is the honest report; the per-repo filter is
the special case that goes stale.

The devlog `git clone` in §2 is a **document location** (the journal lives in that repo
specifically), not an enumeration, and stays hardcoded on purpose — do not "fix" it into the loop.

No wrapper and no `gh` auth? Say so and use the web URLs; **do not treat an empty list as "no open
work"**. Keep that guard whatever else changes here: `gh` exits `4` with **empty stdout** and its
message on stderr, so a session reading only stdout gets a clean empty list from every repo in the
declared set — a report that covers nothing and says nothing about covering nothing. It is the only
thing standing between an unauthenticated box and a confidently empty onboarding report.

## 5. Verify the tooling — and be honest about what is missing

Check, and report the result rather than assuming it:

| Check | Meaning if absent |
| --- | --- |
| `make help` prints "Runtime detected: …" | no container runtime → **no gate can run here**. Don't install anything: this box is a driver, gates belong on a container host (`deploy/dev.md`). |
| `git --version` | can't push from here |
| `"$GH" api /rate_limit` answers (the `$GH` from §4) | your seat's wrapper cannot reach the forge. **Do not use `gh auth status`** — a session box is expected to show *unauthenticated*, deliberately: its credential is read at point of use rather than being a `gh auth login` session, so it cannot leak into an ambient one. `auth status` therefore reports a healthy box as broken |
| `make privacy-check` exits 0 or 2 | **`2` means the gate cannot sweep on this box** and every sweep you owe is owed, not done — sanitize by hand against `CLAUDE.md` and say so with the head named. It no longer exits 0 in that state (quince#41). A provisioned box should have the private layer already (quince#44) |
| `test -f ~/.config/quince/quince-coder.pem && echo present \|\| echo absent` | absent → you cannot author; ask for the credential, never invent one. `quince-bot.token` is the retired predecessor (decisions/0014) and its account is suspended. **Use exactly this form** (quince#245): that directory holds private keys and is deliberately unreadable — `Read(~/.config/quince/**)` is in `.claude/settings.json`'s `deny` block and `ls` of it is refused too, so the two natural moves both come back as a permission refusal. `test -f` answers the question the row actually asks — *does the file exist* — without reading or listing anything. **A refusal there is the guard working, not a broken box** |

## 6. Report — and stop

At most ~20 lines, no preamble:

1. What quince is, in two sentences.
2. Where the frontier is: the current rung/PR, what is proven, what is owed and by whom.
3. Open questions awaiting a ruling (from the dashboard) and open issues/PRs.
4. The next action you would take, and which of the six skills starts it.
5. **What you could not verify** — missing tooling, unauthenticated `gh`, absent private
   layer, docs that should exist and don't.

Then stop. `/onboard` never starts the work; `/kickoff` does.
