# quince — agent entry point

Self-hosted iPhone/iPad backup server. Go core daemon + swappable vault sidecar (Python
today, reusing OSS encrypted-backup decryption; lazy session-scoped reads, no persistent
index) + React/TS UI (Tailwind v4 tokens, vendored shadcn-style components, Zustand;
device-centric IA — Devices + Settings only), REST + one WebSocket, SQLite app DB,
never-mutate-committed versioned storage — **one lifecycle across all backends since
qn.5b** (design §5): `idevicebackup2` writes only into a per-job `working/<udid>` seeded
from `latest/` at job start (reflink where the filesystem allows it, else copy — never
hardlink, which would alias the committed tree), and commit is verify →
`renameat2(RENAME_EXCHANGE)` of `working/<udid>` into `latest/` → snapshot (zfs) or archive
of the displaced tree into `versions/<ts>/`. `latest/` is a real directory on every backend,
never unoccupied, and is the sole whole-tree rclone offsite-sync source; a version is a
`@quince-*` snapshot on zfs (one child dataset per device, browsed via
`.zfs/snapshot/<snap>/latest/`, so between backups the dataset holds only `latest/`) or a
`versions/<ts>/` dir on the reflink (FICLONE) / hardlink / copy backends. Commit is
journaled and startup reconciliation is first-class. Wi-Fi backup is the PRIMARY use case
under the ASSISTED model — iOS requires on-device passcode entry per backup, so there is
no unattended mode and no auto-retry: opportunity signal → push → one unlock+confirm;
failures become `user action required`. Core value: Plex-grade setup, OpenWrt/PVE-grade
config — one tidy hand-editable `config.yml`, the UI edits the file, no secrets in it
(stack D12). Photos viewer is parked, lowest priority.

**Status: working, pre-release** — hardware-proven over both transports, soaking under
real daily use. Development is agent-driven.

## Where state lives

Product code and canon live here. The **process journal lives in a separate public
repo** — <https://github.com/novkostya/quince-devlog>:

| Where | What |
| --- | --- |
| devlog `progress.md` | one-line state, per-rung dashboard, the full decisions log |
| devlog `roadmap.md` | milestones and rungs (`qn.N`) |
| devlog `program/quince.program.md` | gate ladder, spec shape, gap protocol, review protocol |
| devlog `proposals.md` | improvement-proposal ledger (accepted + declined, with reasons) |
| `docs/quince.stack.md` | tech decisions + alternatives considered (`D<N>` ids) |
| `docs/quince.design.md` | architecture, job state machine, storage, security model |
| `docs/contracts.md` | frozen interfaces: REST/WS, vault RPC, cache rules |
| `docs/ui.design.md` | frontend taste |
| `docs/specs/qn.N/` | per-rung specs |
| `deploy/dev.md` | how to build and run the gates anywhere |

**Where this file and the program doc disagree about *process*, this file wins** — the
program doc still describes the retired loop (worktrees, rsync from a workstation,
commit-only-when-asked). Its *engineering* content — gate ladder, spec shape, gap
protocol, perf budgets, the handoff-review dimensions — is current and binding.

## How work runs

**The forge is the substrate.** PRs are how agents communicate, issues are the tracker,
branch protection is the authority model, and an approval is a literal PR approval. This
repo is not a message bus, and no human is an RPC layer.

1. **One issue or rung → several small PRs, each carrying ONE reviewable claim.** Review
   is triggered early and often; a PR nobody can review in one sitting is mis-scoped.
2. **Fresh clone per unit of work.** `git clone https://github.com/novkostya/quince.git`
   into a scratch dir, branch `<rung-or-pr>/<short-title>`. No worktrees, no long-lived
   checkout, no rsync from a workstation.
3. **Gates run on a container host, never on the driving workstation** (`deploy/dev.md`):
   `make gates` / `make image` / `make gates-ui-e2e`, each inside a pinned toolchain
   container. A gate that seems to need a tool installed locally means you are in the
   wrong place.
4. **Privacy sweep, then commit and push as the bot, then `gh pr create`.** Fill in the
   PR template; state explicitly what you did NOT prove.
5. **Approver ≠ author, always.** Implementer sessions author; the **architect** reviews,
   approves and merges code PRs; the **Operator** approves architect-authored docs/canon
   PRs. Nobody pushes to `main`: protection requires a PR, one approval, the `gates` /
   `image` / `e2e` checks, linear history, no force pushes, admins included. (The
   Operator may flip protection off in an emergency; doing so obligates a note in the
   decisions log.)
6. **Merge = rebase-and-merge** (`gh pr merge --rebase`); squash allowed; merge commits
   disabled.
7. **Definition of done** — CI green · privacy swept · review approved · a dev-deploy URL
   in the PR · a ≤5-line what-to-click list · a devlog journal entry. The deploy is
   automatic (`devct deploy`, and `/report` runs it by default), and the URL is the
   **convention name** — an address never enters PR text. When there is no URL, exactly one
   of two sentences is true and it must be the one written: **`deploy: not applicable — no
   runnable change`** (docs, config, spec) or **`deploy: unavailable — <reason>`** (no
   container, build failed, demo never answered). The second exists so the first cannot
   quietly cover for it.
8. **A rung starts from a spec.** If the frontier rung has none, the spec (program-doc
   shape, with its `Rule check` section filled in) is the first PR, reviewed before any
   code exists.

### Identity and credentials

- Implementer sessions act as **`quince-bot`**: git author `quince bot
  <quince-bot@users.noreply.github.com>`, credential = the token the session host holds
  at `~/.config/quince/quince-bot.token`. Never print a token or put one in argv, a
  remote URL, a commit, or a PR body; feed it to git through a credential helper and to
  `gh` through `GH_TOKEN`.
- The token is scoped to this repo and has **no `workflow` scope** — and **neither does the
  architect's**. Measured (quince#113): a `PUT` under `.github/workflows/**` returns `403
  Resource not accessible by personal access token`, while an ordinary contents write to the
  same branch succeeds. **Only the Operator can push a workflow.** Put the file verbatim in
  the PR thread, say plainly in the PR that the check is built and **unwired**, and open an
  issue for the wiring — rather than working around it, or escalating to a seat that cannot
  do it either.
- The architect reviews/approves/merges as the repo owner; the Operator is admin of last
  resort and the approver for architect-authored docs.

### Issues

- product bugs and feature work → issues **here**, **sanitized at filing** (no LAN IPs,
  hostnames, serials, UDIDs, personal paths — the commit privacy gate shifted left);
- process and workflow friction → issues in the **devlog** repo, label `process`;
- labels: `bug`, `enhancement`, `soak-finding`, `documentation`, `process`.

### The journal

Journal entries are **date-anchored and cite PR/issue numbers**, which GitHub allocates
race-free:

```
- 2026-07-25: **the claim, one sentence in bold** — what changed, what was proven, what
  is owed. ([#12](https://github.com/novkostya/quince/pull/12))
```

Lettered entries `(a)`–`(do)` are **retired**: they stay forever as citations from docs
and git history — never mint a new one.

## Skills — the workflow as commands

Project skills live in `.claude/skills/`; invoke them by name.

| Command | What it does |
| --- | --- |
| `/architect` | become the architect session: assert the identity boundary, load state, sweep both repos, arm the review loop |
| `/onboard` | resume the project cold: read the state, verify tooling, report where things stand |
| `/kickoff [issue\|rung]` | take one unit of work: read its context, fresh clone, branch, plan |
| `/report` | turn finished work into a PR description + a devlog journal entry |
| `/review-pr [number\|all]` | the reviewer protocol; `all` sweeps every open PR |
| `/land [number]` | verify, rebase-merge, tidy up, flip the devlog state line |
| `/qa` | dev deploy + click-list (placeholder until the dev-CT tooling lands) |

Permission allowlists are layered: the committed `.claude/settings.json` carries the
generic entries plus the documented reference environment; machine-specific bindings live
in the gitignored `.claude/settings.local.json` (see `.claude/README.md`).

## Hard rules

- **Privacy is a commit-time gate, not a docs rule.** Public history is forever.
  Operator-private facts — hostnames, LAN IPs, MACs, network topology, hardware sizing,
  device UDIDs/serials, personal names and paths, lab-log excerpts — never enter
  committed files, **commit messages**, branch names, tags, PR/issue text, or fixtures.
  Before every push run `make privacy-check`; before a merge re-run it over the whole
  branch — `make privacy-check REF=origin/main...HEAD TEXT=<pr-body>`, which covers the
  diff, the commit messages and the PR text in one command. **Exit `0` clean · `1` a match
  · `2` DID NOT RUN**, and a `2` is never a clean result: on a box with no usable pattern
  list the gate refuses instead of exiting 0, so the sweep is *owed*, with the head named,
  rather than silently ticked (quince#41). A leak that reaches history is an incident:
  rewrite plus a new pattern.
- **State honesty.** Nothing — job engine, API, UI, logs, PR text, journal entry — claims
  more than was proven. A backup is `succeeded` only after verify+commit; a failed
  adapter says so; an unrun gate is declared unrun, with its owner named.
- **Interface facts and version pins are looked up live, never remembered.** Model
  training data is systematically stale. Before pinning a version, or relying on a tool's
  flags / env vars / API shape, check the live source (registry tags, releases page,
  `--help`, the vendor's own docs) and make that lookup part of the PR's evidence.
  Pinning anything other than the newest stable needs a one-line why.
- **Never mutate a committed version.** `idevicebackup2` writes only into the per-job
  `working/<udid>`. `latest/` changes only by the marker-guarded
  `renameat2(RENAME_EXCHANGE)` at commit, after structural verify has passed; `versions/<ts>/`
  dirs and `@quince-*` snapshots are immutable once written. `latest/` is not scratch space —
  it *is* the newest committed version's content, and it is what browse and restore read for
  that version (older versions come from snapshots or version dirs). A failed job **keeps**
  its dirty `working/` so a retry resumes without re-transferring, while a seed killed
  mid-flight is caught by the seed-in-progress sentinel and re-seeded rather than resumed.
  Roll-forward: once verify has passed and the immutable artifact exists, recovery completes
  the remaining commit phases — it never unwinds them, because a commit failure must not
  destroy a multi-hour Wi-Fi transfer. Any storage-touching change re-proves these
  invariants.
- **No silent caps or fallbacks.** Degraded modes (copy backend, wifi-off,
  adapter-failed, cache-dropped, truncated list) are surfaced in the UI and the logs.
- **Config tidiness is a feature** (stack D12): every setting lives in `config.yml` with
  a generated doc-comment and a sane default, is editable in the UI, and needs no restart
  unless the spec says why. No UI-only state, no secrets in the file.
- **Secrets discipline.** Backup passwords reach the tool over stdin/pty only — never
  argv, env, or logs. Test fixtures use the password `test`.
- **Subprocesses**: argv arrays, own process group, supervised, killed on job end.
- **Every bug found on hardware becomes a replay fixture** before it is fixed
  (`core/internal/backup/testdata/transcripts/`).
- **Docs are part of the diff.** A change that contradicts canon updates that canon in
  the same PR. Work also declares coverage: the `go test -cover` summary plus an explicit
  known-untested list, one line and reason each. Declared untested is accepted debt;
  undeclared untested behavior found by a reviewer is a finding.
- **Don't improvise architecture — and don't silently patch holes either.** Canon is
  decided-so-far, so gaps are expected, not exceptional. Rung-local detail: decide it
  within canon, write it into the spec, log one line. Anything touching contracts,
  storage semantics, security, or user-visible behavior: write a `PROPOSED (gap): …`
  block into the affected canon doc, file it as an open question, and stop that thread
  until it is ruled. Forbidden: building on an assumption you never wrote down, and
  re-litigating what is already ruled. Full protocol, plus the non-blocking proposals
  channel, in the program doc.

## The private layer

`local/` is gitignored: lab topology, the privacy pattern list, and personal transcripts.
It lives in the **private `quince-local` repository**, and **both session hosts hold a full
clone of it** — `deploy/runner/provision` places it and `preflight` refuses to start a box
that cannot reach it (quince#44, ruled 2026-07-27). This paragraph used to say the layer
existed *"only on the Operator's machines"*; that stopped being true the moment work moved
onto the boxes, and a document describing a narrower reality than the one that exists is the
defect class this project keeps filing.

**What that means, stated rather than implied:** each box carries the complete private
record — ~610 KB across 8 files, including the lab topology and the external review
transcripts — not merely the pattern list. Compromise of a box is compromise of all of it,
and `pr.6`'s credential-concentration boundary is owed a line saying so. The implementer
identity holds **write** on that repository by ruling, because the layer is a living document
an agent must be able to maintain without the Operator becoming a required hop; branch
protection is unavailable there (private repo, paid feature), so the guard against a
*weakened* pattern list lives here instead, as `deploy/privacy/patterns.floor`.

Public docs may reference the layer by path; they never quote its contents. Nothing in it is
required to resume this project — **the resurrection test**: a stranger who clones the public
repos and starts an agent must be able to continue. If something you need in order to resume
is missing from the public repos, that is a bug worth an issue, not a reason to reconstruct
it from a private file.
