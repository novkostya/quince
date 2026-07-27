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
git clone --depth 1 https://github.com/novkostya/quince-devlog.git /tmp/quince-devlog
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
for r in $(sed 's/#.*//' .claude/forge-set | grep -v '^[[:space:]]*$'); do
  gh issue list --repo "$r" --state open
  gh pr list   --repo "$r" --state open
done
```

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

No `gh` auth? Say so and use the web URLs; do not treat an empty list as "no open work".

## 5. Verify the tooling — and be honest about what is missing

Check, and report the result rather than assuming it:

| Check | Meaning if absent |
| --- | --- |
| `make help` prints "Runtime detected: …" | no container runtime → **no gate can run here**. Don't install anything: this box is a driver, gates belong on a container host (`deploy/dev.md`). |
| `git --version`, `gh auth status` | can't push or open PRs from here |
| `make privacy-check` exits 0 or 2 | **`2` means the gate cannot sweep on this box** and every sweep you owe is owed, not done — sanitize by hand against `CLAUDE.md` and say so with the head named. It no longer exits 0 in that state (quince#41). A provisioned box should have the private layer already (quince#44) |
| `~/.config/quince/quince-bot.token` exists | absent → you cannot act as `quince-bot`; work from a fork or ask for the credential, never invent one |

## 6. Report — and stop

At most ~20 lines, no preamble:

1. What quince is, in two sentences.
2. Where the frontier is: the current rung/PR, what is proven, what is owed and by whom.
3. Open questions awaiting a ruling (from the dashboard) and open issues/PRs.
4. The next action you would take, and which of the six skills starts it.
5. **What you could not verify** — missing tooling, unauthenticated `gh`, absent private
   layer, docs that should exist and don't.

Then stop. `/onboard` never starts the work; `/kickoff` does.
