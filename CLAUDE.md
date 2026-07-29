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
   PRs.

   **Responsibility, never exclusivity.** The rule below says who must act so that nothing sits —
   it does not say who *may*. **An author may always rebase its own PR, and should, the moment its
   own work is what is blocked.** Measured: `gh-coder pr update-branch <n> --rebase` succeeds as the
   authoring App and re-triggers CI. Written because the first version of this rule read as
   exclusive, and a session obeying it literally at 03:00 waits for a seat that is not looking —
   which is the round trip the rule exists to kill, arriving through the rule itself.

   **A red check is not a reason to stop and ask.** The Operator's standard is that a story given
   at night is deployed by morning, so every rung here is one a seat can take alone:

   1. **Classify first.** Re-running is for infrastructure and flakes. A genuine failure wants a
      fix, and reaching for a retry is how a real one gets papered over.
   2. **Rebase onto fresh `main`.** It re-triggers CI *and* fixes the most common cause — a branch
      open across several merges is failing against a base nobody tests. Productive, not hopeful.
   3. **Close and reopen the PR** when the branch is already current. `reopened` is in the default
      `pull_request` trigger set and the App holds `pull_requests: write`. Measured: runs went 3→5,
      the PR returned `OPEN`, and the review verdict survived. Pure retry, no commit, no history.
   4. **Report and keep going.** Say what you tried and what it did, and move to other work.
      **Never `gh run rerun`** — no agent seat can (quince#141): the bot's old ability came from a
      `repo`-scoped PAT carrying `actions`, which was a property of the credential, not of the seat.
      And never an empty commit: it works and leaves a meaningless commit in the record forever.

   **The seat that merges rebases a `BEHIND` branch. It asks only for a `DIRTY` one**, and the
   forge draws that line for you:

   - **`BEHIND`** — mechanical, tree-preserving, no decision in it. **The merging seat does it**,
     with `gh pr update-branch --rebase`. Asking is a wasted round trip.
   - **`DIRTY`** — a conflict, which is an *edit*: someone must choose which lines survive.
     **The author does it.** A merging seat that resolves a conflict in a file it did not write
     and then approves its own resolution has broken `approver ≠ author` by following this rule,
     which is why `never` would be wrong here.

   **`--rebase` is mandatory, not stylistic.** `update-branch` defaults to updating with a *merge
   commit*, and `required_linear_history: true` on this repo means protection then rejects the
   result. A session told not to ask anyone, reaching for the obvious command, gets the wrong one.

   `strict: true` means every merge to `main` puts every other open PR `BEHIND`, so a stale branch
   is the steady state with more than one PR in flight, not an exception worth a message. The
   merging seat holds `contents: write` and can do it itself; asking instead turns a two-second
   action into a round trip across the forge — approve, request, notice, rebase, wait for CI,
   notice again, merge — and every crossing is one a session can miss. On
   2026-07-29 two approved, green, correct PRs sat for hours behind exactly that: the request
   was made, as a footnote at the bottom of a long approval, and neither seat treated it as
   work. A merge queue would collapse this automatically and is **unavailable** — it is an
   org-only feature and this repository is owned by a user account (measured, `owner_type:
   User`), which is why the rule has to be carried by a seat instead.

   Nobody pushes to `main`: protection requires a PR, one approval, the `gates` /
   `image` / `e2e` checks, linear history, no force pushes, admins included. (The
   Operator may flip protection off in an emergency; doing so obligates a note in the
   decisions log.) **That sentence describes `novkostya/quince`.** `novkostya/quince-devlog`
   matches it in every clause but one: it has **no required checks**, because it has no CI —
   repo-specific rather than drifted, and the one difference deliberately left standing. Its
   `enforce_admins` and linear-history clauses were *not* in force until 2026-07-27 and are
   now (devlog#53). Stated per-repository because the prose form was true of one branch, read
   as a property of the project, and the divergence survived unnoticed since the devlog split
   out — no session could have checked it, since only the App can read that endpoint (the
   architect PAT gets `403`, the bot `404`). A gate to assert it is quince#139.
6. **Merge = rebase-and-merge** (`gh pr merge --rebase`); squash allowed; merge commits
   disabled. Merges go through the reviewer — `bin/gh-review` — and that is demonstrated
   rather than aspirational: every merge since the App's first — quince#135 at
   `2026-07-27T21:53:23Z`, then #138, #142 and devlog#54, #57 — reads `mergedBy:
   app/quince-review`. Bounded at that timestamp on purpose: everything merged **earlier** that
   day, quince#134 included, was merged by `novkostya`.
   **This ladder is the ARCHITECT's, and only the architect can climb it.** Both wrappers are
   architect-seat tools and both refuse on the implementer box — `gh-arch` because a bot token is
   present (devlog#7: the box that authors must not hold the identity that approves), and
   `gh-review` because it carries the same assertion. **An implementer session that meets those
   refusals is seeing the boundary work, not a broken box, and must stop rather than repair it** —
   installing the missing tool or placing a key would put the reviewer credential on the authoring
   host and dissolve `approver ≠ author` entirely, which §5 states and this clause must not appear
   to override (devlog#61).
   **Architect, on a refused merge: retry once; if it is refused again, merge through `bin/gh-arch`
   and say so on the PR.** The fallback exists because the harness classifier refuses the merge
   verb *intermittently* and leaves no trace on the forge, so the next session to meet it
   would otherwise conclude the App cannot merge and escalate — which is the pattern named
   under "a refusal is not a reason to escalate to another seat". `gh-arch` rather than an
   Operator merge, because **a merge carries no verdict**: the judgement is the approval,
   structurally the App's, and the merge only executes it — so merging as `novkostya` costs a
   timestamp, not an authority (devlog#52).
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
  same branch succeeds. **No PAT seat can push a workflow** — neither the implementer's nor the
  architect's. The Operator always can (an SSH push consults no OAuth scope). `quince-review[bot]`
  **declares `workflows: write`** — measured 2026-07-29 by asking `GET /app` with a JWT signed from
  the reviewer key, which returns the App's permission set; note that `bin/gh-review` cannot make
  that call, because it mints *installation* tokens and the endpoint wants a JWT. That is the
  **declared grant**; whether a write under `.github/workflows/**` actually succeeds as the App is
  **(unmeasured)** — the only definitive test is performing one, and nobody has. Distinguished
  because every other capability in the seat table below carries a measurement, and resolving a
  contradiction toward an unsourced claim is not the same as knowing. This sentence read
  "only the Operator can push a workflow" until 2026-07-29, contradicting the seat table thirty
  lines below it that had already recorded the App's grant — canon disagreeing with itself, in the
  paragraph a session consults before deciding it is blocked. Put the file verbatim in
  the PR thread, say plainly in the PR that the check is built and **unwired**, and open an
  issue for the wiring — rather than working around it, or escalating to a seat that cannot
  do it either.
- The architect reviews/approves/merges as the repo owner; the Operator is admin of last
  resort and the approver for architect-authored docs.

**What each identity cannot do — and, marked `CAN`, what one can that nobody granted it.** These
have been discovered one at a time, by hitting them, each costing a session the time to work out
that the failure was structural rather than local (devlog#48). Consult this before designing
around a refusal. The table runs in one direction with **three** exceptions, all marked; read the
`CAN` rows as blast radius rather than as convenience.

| identity | cannot |
| --- | --- |
| **`quince-bot`** — implementer, on the runner | push under `.github/workflows/**` (no `workflow` scope, quince#113) · `gh pr edit` (needs `read:org`, whichever flag you pass; use `gh api -X PATCH`, which works because REST does not consult the org-scoped GraphQL fields the porcelain resolves — devlog#23) · `--add-reviewer`, i.e. **re-request a review** (same root, devlog#48) · **`CAN` re-run a workflow run** — and the architect and the App cannot; see below (quince#141) · **`CAN` delete any discussion in `quince-devlog`** — see below (devlog#30) |
| **architect** — on the arch box | push under `.github/workflows/**` (same 403) · register a review verdict on a PR the Operator authored (shared login, quince#47) · `git pull` the private layer, until its clone is wired to the credential it already holds (quince#121) · **re-run a workflow run** — `run rerun` answers `Resource not accessible by personal access token` (quince#141) |
| **`quince-review[bot]`** — the reviewer, a GitHub App | be a user: `api user` returns `403 Resource not accessible by integration`, because an installation token has no user context. That is not a broken credential and the check that answers "can this box cast a verdict" is `api /installation/repositories` · **re-run a workflow run** — same refusal, worded for an integration; the installation has no `actions: write` (quince#141) |
| **Operator** | — **`CAN`** always push a workflow: an SSH push consults no OAuth scope; since 2026-07-27 `quince-review[bot]` can too, holding `workflows: write` |

**Re-running CI is a `CAN`, and `workflows:` is not `actions:`.** Most entries above describe
something the implementer lacks and a more privileged seat holds; here **`quince-bot` can re-run a
workflow and the architect and the App cannot** (measured,
quince#141 — `run_attempt` increments, the failed attempt preserved). So on a red check the ask is
*"re-run it"*, addressed to the implementer, rather than a push or an escalation. Two permissions
sit within a few lines of each other in this table and look like opposite claims about one word:
**`workflows: write` pushes workflow *files*** (the Operator row), **`actions: write` operates
workflow *runs*** (this one). They are different grants, and neither implies the other. One more
trap worth naming: **`gh run rerun` exits `0` and prints its refusal** — read the output, not the
exit code.

**The newest `CAN` is a hazard rather than a convenience, and it is the only one of the three
that is: `quince-bot` can DELETE any discussion in `quince-devlog`.** Measured 2026-07-28,
both identities, create *and* delete, each probe removing its own artifact (devlog#30). **Nobody
granted this.** It arrived with the classic `repo` scope the token already held, the moment
Discussions was enabled on the repository — a permission decision nobody made, produced by a
container choice.

That matters because **devlog#30 moves the journal into Discussions.** Today the journal is
`progress.md`: branch protection, a required approval, linear history, and every edit visible in
a diff forever. Afterwards it is a set of discussions the implementer can remove with one API
call — and **nothing on the forge records that a discussion was deleted.** The Operator has
accepted that risk and the decision stands; what is not acceptable is it going unwritten, which
is why this paragraph exists. The permission is real **today**; what the migration changes is
what is at stake behind it, not the grant.

**One more reason this row is worth its space: it is the first token-scope question in this
project that came back *yes*.** The previous four were refusals — `read:org` twice (devlog#23,
devlog#48), `workflow` (quince#113), `actions` (quince#141) — and the habit they built is
*assume the narrow token cannot*. Here that habit would have been wrong in both directions at
once: it would have cost a credential-widening request nobody needed, and it would have hidden a
capability nobody wanted. **Probe, do not infer** — the same lesson quince#141 taught from the
opposite side.

**The reviewer's approval satisfies branch protection on its own** — Operator ruling, 2026-07-27
(quince#130). One approving review is required and an App's counts, which is the point of it
existing: quince#47 established that the architect and the Operator share a login, so GitHub
refuses an architect verdict on an Operator-authored PR, and that is the one class of PR the
Operator structurally must author. An identity that is not a person is what breaks the deadlock.

**Which identity must approve a class of PR is written in `.github/CODEOWNERS`, not only here.**
Canon — this file and the four docs it names as canon, plus `CODEOWNERS` itself — is owned by
`@novkostya`, the human account. **A GitHub App cannot be a code owner**: code owners must be users
or teams with write permission, and an installation is neither. That refusal is the mechanism rather
than an obstacle, because it means an architect verdict *structurally cannot* satisfy a code-owner
requirement on those paths. It became expressible only when quince#134 moved verdicts to the App;
before that, naming `@novkostya` distinguished nothing, because the architect approved as
`@novkostya`. If the architect ever reviews as a user account again, that file silently stops
separating the two seats.

**The file is LIVE, and this paragraph used to say the opposite.** *"Require review from Code
Owners"* was enabled on 2026-07-27, the last step of the sequence set out below. Measured
2026-07-28:

```
require_code_owner_reviews      true
required_approving_review_count 1
enforce_admins                  true
```

**Of the two AGENT identities, only the App can read that endpoint** — the architect PAT gets `403`
and the bot `404`. The **Operator reads it fine**: it is an admin endpoint and the Operator is the
admin. The distinction is load-bearing rather than pedantic, because the next paragraph tells you to
go and read it, and a reader who takes the agent-seat limit for a universal one concludes they
cannot.

So an approval on an owned path **must** come from `@novkostya`. Neither an architect verdict nor
the App's can satisfy it, by the rule directly above: an App cannot be a code owner.

**What stood here before was *"the file enforces nothing until the toggle is enabled … committed
unwired on purpose"*, and it was believed after it went false.** An architect session reasoned from
it that an App approval *would* satisfy protection, and escalated a routing question to the Operator
as an unruled gap — when the answer was one `GET` away and had been for a day. A document describing
a narrower reality than the one that exists is this project's most-filed defect, and the routing
question it produced is what it costs. **Read the endpoint rather than this paragraph**:
branch-protection changes leave no trace in git, so a committed claim about protection is a claim
about the past.

**The sequence was an Operator ruling** (quince#137, 2026-07-27) and all three steps are now done:
the architect **authors canon through the App** so the author is `quince-review[bot]` → `@novkostya`
approves it as code owner, a different principal, so GitHub counts it → *then* the toggle goes on.
Flipping first would have blocked exactly the class the file protects, because GitHub does not count
an author's approval of their own PR and the sole code owner would *be* the author. **So
`bin/gh-review` is the authoring path for canon, not only the verdict path** — the clause is in
`/architect` §1, and quince-devlog#51 vs #53 is what a missing instruction rather than a missing
capability looks like.

**Some citations in this file currently resolve to nothing, and that is a property of the forge
rather than a typo.** `quince-bot` was suspended on 2026-07-28; GitHub hides everything a suspended
account authored, so every issue and PR it opened returns `Not Found` to **every** identity —
`novkostya` and the App alike — until the appeal is decided. quince#137 and the incident above are
both in that set. **The rulings they carry are written out here in full for exactly that reason**:
where a citation has become a dead end it is provenance, not the load-bearing copy. If a reference
in this file leads nowhere, check whether its author is suspended before concluding the reference
was wrong.

**Break-glass canon repair from the Mac.** The Mac authors as `novkostya`, which is the one author a
code-owner requirement on `@novkostya` cannot clear — so a `novkostya`-authored PR touching an owned
path cannot merge from any seat: GitHub refuses the self-approval, an App cannot be a code owner, and
`enforce_admins: true` closes the bypass. Prefer the App: if the arch box is up, author canon through
`bin/gh-review` and none of this applies. This is for when it is not. Operator ruling, 2026-07-28
(quince#137); a second code owner was the alternative and was rejected, because it would satisfy the
requirement on *every* canon PR forever and dissolve the asymmetry that makes the file mean anything.

1. **Author and open normally.** The PR reads `BLOCKED` / `REVIEW_REQUIRED`. That is correct, not a fault.
2. **Get a real verdict first** — the architect reviews and approves as the App. It does not satisfy
   the code-owner requirement and is not meant to; it is the independent read, and skipping it turns
   a lockout escape into an unreviewed merge.
3. **The Operator lowers the requirement, by hand:**
   ```sh
   gh api -X PATCH repos/novkostya/quince/branches/main/protection/required_pull_request_reviews \
     -F require_code_owner_reviews=false
   ```
4. **Merge.**
5. **Raise it again and READ IT BACK** — the `GET` must return `true`. A re-enable that is assumed
   rather than verified is how a temporary window becomes permanent.
6. **Record both timestamps on the PR.** Nothing else does.

**Untested end to end.** Step 5's read is verified; step 3's `PATCH` is its documented counterpart
and nothing here has run it. First use should correct this paragraph rather than trust it.

**That exception is narrow and does not generalise.** Routing authorship through the App would
collapse author and approver into one principal anywhere the App also approves — the thing
`approver ≠ author` exists to prevent (quince#136). It does not collapse here, and only here,
because the approver for canon is the **Operator**. It licenses nothing for any class the App also
approves.

**No per-verdict disclosure is required, and the reason is structural rather than trusting.** The
signing key exists in one place — the arch box — so the sessions that can cast an App verdict are
architect sessions, which review before approving. A rule making every verdict declare its
provenance would be guarding a case the key's location already makes rare. Two things keep that
true and are worth knowing if either changes: **root on the arch box can sign** (so the claim is
"whoever holds the box", not "whoever ran a review"), and the first App approval ever cast —
quince#123 — was a **relay** of two other seats' verdicts rather than the caster's own reading. If
the key is ever copied off that box, revisit this paragraph before assuming it still holds.

Two rules follow, and both are about the asker rather than the holder.

**A refusal is not a reason to escalate to another seat.** Check the table first: on quince#113
a session escalated a workflow push to the architect, which cannot do it either.

**Never ask an identity for an action it cannot perform.** The ask looks answerable, the failure
is silent from the asker's side, and both parties then wait correctly for a signal that cannot
arrive. Ask for something the holder *can* emit — a comment is always available to everyone.

**The Operator's Mac is the deliberate break-glass host, and its exemption is a design decision
rather than an unfinished lockout** (`pr.6` constraint 6). `pr.6` turns every remaining root path
into a forced-command wrapper, and the natural reading of *every* is that the Mac should be narrowed
with them. It is not, on purpose: **a lockout that leaves no host outside itself has no recovery
path.** Both boxes are supervised by a service that a bad provision can stop from starting —
`preflight` refuses rather than degrades, by design — and the seat that repairs a box which will not
start cannot be a seat that lives on it.

So the Mac keeps capability the boxes deliberately do not, and the identity table above already
records the sharpest instance: an SSH push consults no OAuth scope, so **the Operator can always
push a workflow** where neither agent identity can. Read that row as break-glass, not as an
inconsistency nobody got around to closing.

**What it costs, stated rather than implied.** The Mac is a host that can bypass the authority
model, and it sits outside the two-box identity boundary the rest of this section builds. That is
accepted, which is why the exemption is **narrow**: it is a recovery seat, not a third work seat.
Work happens on the boxes. The moment routine work moves back to the Mac the exemption stops being
break-glass and becomes an ordinary hole — and the private-layer section below is this project's own
record of what it costs when a document describes a different reality from the one that exists.

**That boundary is a norm, and it is one deliberately, because no mechanism distinguishes recovery
from work.** Both look like `novkostya`-authored activity on the forge, which is indistinguishable
from the Operator's ordinary rulings and canon approvals — so the obvious tripwire does not separate
the two cases, and none of the others examined did either. Stated rather than left implied, because
this paragraph sits ten lines from a file arguing that a sentence nobody can falsify eventually
stops being true, and an acknowledged norm ages better than an implied guarantee.

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
| `/retire` | end a session so nothing it knows dies with it: prove the boundary, flush to the forge, declare the ephemeral, record what could not be recorded |

Permission allowlists are layered: the committed `.claude/settings.json` carries the
generic entries plus the documented reference environment; machine-specific bindings live
in the gitignored `.claude/settings.local.json` (see `.claude/README.md`).

## Hard rules

- **Privacy is a commit-time gate, not a docs rule.** Public history is forever.
  Operator-private facts — hostnames, LAN IPs, MACs, network topology, hardware sizing,
  device UDIDs/serials, personal names and paths, lab-log excerpts — never enter
  committed files, **commit messages**, branch names, tags, PR/issue text, or fixtures.
  Before every push run `make privacy-check`; before a merge re-run it over the whole
  branch — `make privacy-check REF=origin/main...HEAD TEXT="$BODY"` where `BODY` is per-runner under `$HOME/scratch/<runner>/` and NEVER a fixed `/tmp` path (two runners on one box overwrite each other there, and the sweep then covers the wrong text — quince-devlog#123), which covers the
  diff, the commit messages and the PR text in one command. **`TEXT=` takes a PATH to a file
  holding the body, never the body itself** — pass the prose and it word-splits, so the gate
  refuses naming the first word of your own PR as an unreadable filename (quince#105).
  **Exit `0` clean · `1` a match
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

**`preflight`'s "reach" is presence, not freshness — and this paragraph committed the same
defect one size up (quince#121).** Measured on the architect box: a clone at an HTTPS remote
with no credential helper. Present, readable, and unable to advance. `preflight` was satisfied,
the box started, and the last privacy sweep before a merge therefore ran a matcher with neither
the canary nor the case-sensitive list — while reporting `clean`. The two boxes were running
materially different privacy gates and neither could tell.

Until `preflight` asserts that the clone can **fetch** and not merely exist, a box's private
layer can be silently stale. **Read the gate's own banner rather than its exit code**: it names
the lists it loaded and whether the canary proved the matcher, and those lines are the only
thing that distinguishes *swept* from *compiled the lists and matched nothing*.

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
