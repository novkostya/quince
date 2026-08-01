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
| devlog `progress.md` | one-line state + per-rung dashboard — **current state only**, guarded by `bin/dashboard-size` |
| devlog `decisions/` | the decisions log, **one file per decision** (`NNNN-slug.md`) |
| devlog **`journal` branch** | the narrative journal, **one file per entry** — never merged into `main`, never protected |
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

   **SEQUENCE them; do NOT STACK them** — Operator ruling, 2026-07-31 (quince#388). Branch each PR
   from `main`. A PR based on another PR's branch is *stacked*, and the cost of that is not the
   review, it is the merge: **merging the base with `--delete-branch` — the documented normal thing
   to do — silently CLOSES the stacked PR.** One second later, no warning, nothing in the merge
   output, and `forge-watch` reports the dependent as `DIRTY`, which is the shape §5 tells the
   merging seat to leave alone.

   **It becomes irrecoverable the moment the author pushes**, which is the natural next action for a
   branch whose base has just landed. Two `422`s close the door in sequence: `state cannot be
   changed. The <base> branch has been deleted`, then `Cannot change the base branch of a closed pull
   request`. Recreating the base ref clears the first and reveals the last one — `the <head> branch
   was force-pushed or recreated` — and no seat can get past that. **The commits survive; the pull
   request and its verdict do not.** Measured on quince#384, which was approved, and had to be
   reopened as quince#387.

   **"Discouraged", not "never", and the difference is where the cost lands.** quince#375's slice
   stacked for a real reason: 1a and 1b both edited `bin/wrapper-boundary-test` and would have
   conflicted. Sequencing that costs **one review cycle** — land 1a, then branch 1b from `main` and
   resolve nothing. That is the trade the ruling accepts, and it is cheap against losing an approved
   PR. When you stack anyway, say so in the PR body so the merging seat knows before it merges the
   base — the forge shows the base branch, and it is easy to miss.
2. **Fresh clone per unit of work.** `git clone https://github.com/novkostya/quince.git`
   into a scratch dir, branch **`<runner>/<short-title>`** — the runner name this session declared,
   then the topic. No worktrees, no long-lived checkout, no rsync from a workstation.

   **The prefix is LOAD-BEARING, not stylistic: it is what `forge-watch`'s `wake_filter` reads**
   to decide whether an event belongs to this session or to another runner on the same box
   (quince#174). `b` is the branch and the test is a prefix match, so a branch that does not carry
   its runner's name cannot be attributed to anyone. **Do not "tidy" this back to a topic-only
   name.** This line said `<rung-or-pr>/<short-title>` until quince#230, two lines from `/kickoff`
   printing the same thing and from the tool printing *"this session owns branches `r4/…`"* — and
   five of six PRs open on one afternoon carried no runner prefix, so a live guard was resting on a
   habit. It survived only because its failure mode is the harmless one: an unattributable branch
   makes `wake_filter` fail **open**, which looks like ordinary noise. A prefix filter in a
   fail-*closed* position — `owed`, as proposed on quince#227 — would have reported nothing owed
   for every one of them.

   **It is not a trade against readability.** `r4/pr-title-refs-usage-exits` is runner-prefixed
   *and* topic-readable; the prefix is one short token and the title follows it unharmed.

   **Going forward only.** Branches already open under the old convention are not migrated —
   `quince-devlog#127` is one and is deliberately stranded (devlog#129).
3. **Gates run on a container host, never on the driving workstation** (`deploy/dev.md`):
   `make gates` / `make image` / `make gates-ui-e2e`, each inside a pinned toolchain
   container. A gate that seems to need a tool installed locally means you are in the
   wrong place.
4. **Privacy sweep, then commit and push as the bot, then `gh pr create`.** Fill in the
   PR template; state explicitly what you did NOT prove.
5. **Approver ≠ author, always.** Implementer sessions author; the **architect** reviews,
   approves and merges code PRs; the **Operator** approves architect-authored docs/canon
   PRs.

   **The seat that merges rebases a `BEHIND` branch. It asks only for a `DIRTY` one**, and the
   forge draws that line for you:

   - **`BEHIND`** — mechanical, tree-preserving, no decision in it. **The merging seat does it**,
     with `gh pr update-branch --rebase`. Asking is a wasted round trip.
     **But only when the branch is awaiting the MERGING SEAT** — approved, or approved-pending-CI
     (quince#207). `BEHIND` is a fact about the branch; **whose turn it is** is the fact that decides,
     and a PR can be `BEHIND` *and* `CHANGES_REQUESTED` at once. **The merging seat may move a branch
     it is about to consume, not one somebody else is still holding.** Awaiting the author —
     `CHANGES_REQUESTED`, draft, or a conflict — leave it; their next push rebases it anyway
     (quince#203 — a rebase discarded six minutes later, one CI run on a head nothing would merge).
     **Unless the author has retired**: then it is a successor implementer's to adopt, not the
     merging seat's to move (quince-devlog#129).
   - **`DIRTY`** — a conflict, which is an *edit*: someone must choose which lines survive.
     **The author does it.** A merging seat that resolves a conflict in a file it did not write
     and then approves its own resolution has broken `approver ≠ author` by following this rule,
     which is why `never` would be wrong here.

     **UNLESS THE `DIRTY` IS A STACKED ORPHAN, IN WHICH CASE IT IS NOT A CONFLICT AND THE AUTHOR IS
     THE ONE ACTOR WHO MUST NOT TOUCH IT** (quince#400). If the PR's base branch was another PR's
     head and **that base has merged AND its branch was deleted**, the forge reports `DIRTY` and
     **the PR is already closed** — GitHub closes a dependent whose base branch is gone rather than
     retargeting it. Nothing is owed to a resolver, because there are no lines to choose between.
     **The deletion is the trigger, not the merge** — `gh pr merge --rebase` without
     `--delete-branch` leaves the base standing and the dependent open, which is why §6's guard is
     written against `--delete-branch` specifically. Stated precisely because the only measurement
     confounds them: quince#384 closed one second after quince#377 merged with `--rebase
     --delete-branch`, a single act. Canon behaves as though deletion is the cause because that is
     what §6 guards; nobody has separated them experimentally, and the instruction below is correct
     either way (quince#402 review).
     **The author's natural next action destroys it**: rebasing onto the new `main` is correct for a
     stacked branch whose base landed, and it force-pushes the head, after which no seat can reopen
     the PR — `state cannot be changed. The <head> branch was force-pushed or recreated`. Routing
     this row's default at a stacked orphan hands the problem to the one actor whose correct-looking
     response is fatal, inside a window of minutes.
     **Check `state` before treating any `DIRTY` as a conflict.** If it is `CLOSED`, this is §1's
     stacking case, the recovery is *recreate the base ref → reopen → retarget* in that order, and it
     is the **merging seat's**, because the merging seat caused it. `approver ≠ author` is untouched:
     reopening a PR chooses no lines and casts no verdict, which is why the carve-out that would be
     wrong for a real conflict is right here.

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

   **BEFORE `--delete-branch`, CHECK FOR PRs STACKED ON THIS ONE** (quince#388). Rung 1 above says
   do not stack; this is what the merging seat does when somebody did anyway, or when a stack
   predates the ruling. One command, and it costs nothing when the answer is empty:

   ```sh
   gh pr list -R <repo> --json number,baseRefName \
     -q '.[] | select(.baseRefName=="<this PR head branch>") | .number'
   ```

   **If it returns anything, retarget those PRs to `main` FIRST** — `gh api -X PATCH
   repos/<owner>/<repo>/pulls/<n> -f base=main` — and only then merge. Retargeting is legal while
   the dependent is still open and impossible one second after it is not, which is the whole
   window. Deleting the branch is not the defect and branch hygiene is worth keeping; doing it
   while a dependent is open is.
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
   automatic (`make demo`, which is what `/report` runs by default), and the URL is the
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

**PROPOSED (gap): a third forge identity for the supervisor seat — `quince-analyst`.** Filed under
the gap protocol because it touches the authority model, which canon says is not a rung-local
decision. **Nothing below is in force.** Ruling wanted from the Operator; the architect is asked to
review it as a design.

**The gap.** The supervisor seat produces exactly two kinds of forge artifact — an issue, or a
comment on one — and has no identity to sign them with. Everything it filed on 2026-07-29/30
(quince#237–#246, quince-devlog#139, #141, and corrections on four of those) went out as
`app/quince-coder`, borrowed over ssh from the implementer box, with a hand-written relay banner
carrying the truth the API could not. That misattributes supervisor reasoning to the seat that
writes code, and quince-devlog#139 filed it.

**The fact that decides the shape, and it is not convenience.** The seat holds root ssh to both
working boxes, so it already reaches **all three** identities — measured 2026-07-31:
`bin/gh-coder` on the implementer box returns `app/quince-coder`, and on the architect box
`bin/gh-review` answers `/installation/repositories` and `bin/gh-arch api user` returns
`novkostya`. **So "the supervisor cannot author or approve" is already false.** What bounds this
seat today is its own restraint — unbounded, unattributed and unrecorded. quince-devlog#139
understated this and is owed a correction.

A scoped identity therefore **adds no capability**. What it removes is the *reason* to borrow —
after which any borrowing is a signal that something is wrong, rather than the documented way this
seat speaks.

**The proposal — RULED on 2026-07-31 (quince#375), WIDER than it asked for, and partly built.** The
argument below is kept as it was made, because the ruling turned on it. What it *asks for* has been
superseded, and each superseded clause says so inline rather than being quietly rewritten: a reader
arriving from a citation must be able to see both what was proposed and what was decided.

- A **GitHub App**, `quince-analyst`, installed on both repositories, granted **`issues: write`
  and nothing else** — no `contents`, no `pull_requests`, no `workflows`, no `administration`.

  **SUPERSEDED.** The ruling granted **`contents: write` + `pull_requests: write` + `issues: write` +
  `metadata: read`** on **five** repositories — `quince`, `quince-devlog`, `ios-backup-crypt`,
  `ios-backup-parser` and the private `quince-local` — because the seat **authors pull requests** as
  well as filing issues. The wide grant was ruled *not* a widening, on the argument this block already
  makes: root ssh to a box that holds a credential **is** that credential, so it adds no reachable
  capability and only removes the reason to borrow. Per-repo narrowing was rejected as theatre for
  the same reason. Ceremony done the same day — App id `4449080`, permissions **probed** rather than
  read off a settings page, including a measured negative under `.github/workflows/**` (403). So
  quince#113's rule holds for a third identity: **no agent seat can push a workflow.**
- **An App, not a personal access token, and not the Operator's.** A classic PAT's scopes are
  coarse, and this project has its own incident from exactly that granularity: `quince-bot` could
  delete any discussion in the devlog because the permission *"arrived with the classic `repo`
  scope the token already held, the moment Discussions was enabled"* — a grant nobody decided.
  Using the Operator's own token would be worse still: it authors as `novkostya`, which is the
  confusion `bin/gh-coder` refuses in as many words, and quince#47 is the record of what a shared
  login costs. In this project an Operator-authored artifact carries ruling weight, and a
  supervisor finding wearing that login could be read as a ruling by a session with no way to tell.
- The name follows the convention the other two set — **the act, not the host.** The boxes are
  `quince-runner` and `quince-arch`; the identities are `quince-coder` and `quince-review`.
  `quince-supervisor` would be the first identity named after its machine, and that word already
  carries three senses here (the seat, the OpenRC service, and `supervise-daemon` — the status line
  reads *"quince-supervisor: supervisor up"*). `analyst` names what the seat does in both its
  modes: it studies the system and files what does not match (every issue above), and it works a
  problem space into specified work items (rung scoping).

**What it costs, stated rather than discovered later.** `deploy/runner/preflight`'s `supervisor`
arm asserts the seat's rule today as a clean denylist — any of four credential files present,
refuse — and its comment calls that *"the whole of its security property."* Granting this identity
turns that into an allowlist: this file may be present, those may not. Still checkable, and
**softer to audit**, because "absence of every credential" is a sentence anyone can verify and
"absence of every credential except one" is a list that can drift. That is the price, and it is
`decisions/0007`'s territory — a control that is easy to state is worth more than one that is
merely correct.

**That price is now owed rather than paid, and the gap is live.** The ceremony placed the key before
the allowlist was built, so `preflight`'s supervisor arm still scans the four *old* credential files
and does not know `quince-analyst.pem` exists. On the supervisor box it therefore prints *"role
supervisor: no forge credential present — this seat can neither author nor approve, as required"*
about a box that now holds an authoring credential. Two claims in that arm are false until the
allowlist lands: that `ok` line, and `preflight`'s *"this seat … commits nothing"*. The allowlist is
quince#375's PR 1c.

**The analyst IS a seat in the watch sense, and its name is `analyst<N>`.** This paragraph argued the
opposite and the argument was sound on its premise: seat names exist for branch ownership, the state
directory, the scratch root and the container namespace, and **`issues: write` forecloses opening a
pull request**, so a seat that owns no branch needs no attribution. **The premise was withdrawn when
the ruling widened the grant** — the seat authors pull requests, so it owns branches, and a branch
nobody can attribute wakes every watch on every box. The conclusion inverts with it: quince#379 made
`SEAT_PATTERN` `^(r|arch|analyst)[0-9][0-9]*$` (was `^(r|arch)[0-9][0-9]*$`, quince#333).

**The ordinal is required — `analyst1`, never bare `analyst`** — and this is the one place the
original paragraph's caution survives intact. quince#330 named *"a prefix that is NOT a seat being
read as one"* as the price of moving from a list to a pattern, and `analyst` is not a hypothetical
topic prefix: **quince#344 was filed on branch `analyst/propose-identity`**. Under a bare-word
pattern that branch is seat-shaped, so `wake_filter` would attribute a topic branch to a seat and
suppress wakes for it. It is **not migrated** — the convention is going-forward only, and that branch
is the bootstrap artefact of an identity that did not yet exist.

**Adding an ORDINAL is free; adding a KIND is a one-line PR.** `r8` and `arch3` need nothing —
quince#330 deleted the `.claude/seats` list, so there is no enumeration to update. Widening the
alternation itself is the cost, and `analyst<N>` is the first time it has been paid.

**What does not change — and one thing that does.** `approver ≠ author` is untouched, and it is
untouched *mechanically* rather than by restraint: GitHub refuses an approval from a pull request's
own author, and `.github/CODEOWNERS` routes canon to `@novkostya`, which no App can satisfy. **What
changed is the sentence this paragraph used to carry** — *"the seat still cannot author code, open a
pull request, cast a review verdict, or merge"*. It can now author code and open pull requests; it
holds no `administration` and cannot merge. Corrected rather than deleted, because it was the
load-bearing reassurance the ruling was taken against.

**Authored by the seat it would grant, and the ruling has since been taken.** This block was written
by the supervisor seat, proposing capability for itself, which is worth naming rather than leaving
for a reader to notice. Both approvers were independent of it — the Operator ruled, the architect
reviewed — so the check held; the asymmetry was real and the ruling was taken knowing it.

**Sequence — ruled yes, and here is where it actually stands.** The ceremony created the App and
placed its key on the supervisor box (done). The mechanism is quince#375's PR 1, sliced into five:

| | | |
| --- | --- | --- |
| **1a** | `bin/gh-analyst`, refusing beside every other seat's credential, covered by `bin/wrapper-boundary-test` | quince#377, open |
| **1b** | the other four wrappers refusing beside the **analyst** key — added to the checklist after review, because a box holding `quince-analyst.pem` beside `quince-review.pem` can author as one and approve as the other | not open |
| **1c** | `preflight`'s supervisor arm becomes an allowlist — **the live gap above** | not open |
| **1d** | `SEAT_PATTERN` admits `analyst<N>` | quince#379, **merged** |
| **1e** | `deploy/runner/provision` knows the analyst credential | not open |

The original clause said *"a second PR flips this block from proposed to actual"*, and this is that
edit. Its reasoning still holds and is why the block was not written earlier: **writing canon before
the App exists documents a state that does not exist**, the defect quince#318 corrected and
quince#320 refused to repeat. The same rule is why the rows above say `not open` rather than
describing what those PRs will contain.

**The ruling, on every question this block left open.** `issues: write` was **too narrow** — the seat
authors pull requests, so it was granted `contents: write` and `pull_requests: write` besides. The
identity installs on **five** repositories, not two and not one. **`quince-devlog#139` closes with
this.** The name **`quince-analyst` stands.**

**Whatever is granted must be PROBED at grant time, not read off the App's settings page.** This
project's one affirmative token-scope answer arrived unasked — `quince-bot` could delete any
discussion in the devlog, a grant nobody decided, discovered by trying it rather than by reading a
scope list (devlog#30). The ceremony should therefore record what the identity can actually do,
measured, including at least one attempt at something it is meant to be unable to do.

**What each identity cannot do — and, marked `CAN`, what one can that nobody granted it.** These
have been discovered one at a time, by hitting them, each costing a session the time to work out
that the failure was structural rather than local (devlog#48). Consult this before designing
around a refusal. The table runs in one direction with **three** exceptions, all marked; read the
`CAN` rows as blast radius rather than as convenience.

| identity | cannot |
| --- | --- |
| **`quince-bot`** — implementer, on the runner | push under `.github/workflows/**` (no `workflow` scope, quince#113) · `gh pr edit` (needs `read:org`, whichever flag you pass; use `gh api -X PATCH`, which works because REST does not consult the org-scoped GraphQL fields the porcelain resolves — devlog#23) · `--add-reviewer`, i.e. **re-request a review** (same root, devlog#48) · **re-run a workflow run** — it alone could, and that ended when the identity moved to an App; see below (quince#141) · **`CAN` delete any discussion in `quince-devlog`** — RETIRED 2026-07-31, Discussions disabled; see below (devlog#30) |
| **architect** — on the arch box | push under `.github/workflows/**` (same 403) · register a review verdict on a PR the Operator authored (shared login, quince#47) · `git pull` the private layer, until its clone is wired to the credential it already holds (quince#121) · **re-run a workflow run** — `run rerun` answers `Resource not accessible by personal access token` (quince#141) |
| **`quince-review[bot]`** — the reviewer, a GitHub App | be a user: `api user` returns `403 Resource not accessible by integration`, because an installation token has no user context. That is not a broken credential and the check that answers "can this box cast a verdict" is `api /installation/repositories` · **re-run a workflow run** — same refusal, worded for an integration; the installation has no `actions: write` (quince#141) |
| **`quince-analyst`** — the supervisor seat, a GitHub App | push under `.github/workflows/**` — measured at the ceremony, 2026-07-31: `403` while an ordinary write to the same branch succeeded seconds later, so quince#113's rule holds for a third identity · **merge** — it holds no `administration`. It **`CAN`** author code and open pull requests (`contents: write` + `pull_requests: write`, quince#375), which is what distinguishes it from every earlier description of this seat |
| **Operator** | — **`CAN`** always push a workflow: an SSH push consults no OAuth scope; since 2026-07-27 `quince-review[bot]` can too, holding `workflows: write` |

**NO AGENT SEAT CAN RE-RUN A WORKFLOW RUN, and `workflows:` is not `actions:`.** All three refuse.
**`quince-analyst` is a fourth agent identity and is `(unmeasured)` here** — its declared grant has no
`actions:` at all, so it almost certainly refuses too, but nobody has run it and "almost certainly" is
not what the rest of this table means. Measured 2026-07-29, both remaining agent identities, within
minutes of each other:

```
bin/gh-coder  run rerun 30467245714 --failed   → Resource not accessible by integration   (exit 1)
bin/gh-review run rerun 30467245714 --failed   → Resource not accessible by integration   (exit 1)
```

**This paragraph used to say the opposite, and it was the last `CAN` standing.** `quince-bot` really
could re-run — a classic `repo`-scoped PAT carries `actions`, and quince#141 measured `run_attempt`
incrementing with the failed attempt preserved. That capability was a property of **the token type**,
not of the seat, so it did not survive `decisions/0014` moving the implementer identity to a GitHub
App: an installation has `actions: write` only if granted, and this one was not. The account is
suspended besides. Two permissions still sit a few lines apart in this table and still look like
opposite claims about one word: **`workflows: write` pushes workflow *files*** (the Operator row),
**`actions: write` operates workflow *runs***. Different grants, neither implying the other.

**So "ask the implementer to re-run it" is an instruction nobody can follow**, and it is the shape
this project keeps paying for — a document naming a mechanism that has since moved. It survived
because the capability was recorded against a *seat* when it belonged to a *credential*; when the
credential changed, nothing pointed here.

**What to do instead, and it is better than a re-run was:** `gh pr update-branch --rebase` clears
`BEHIND` **and** re-triggers the workflow on the new head. One command, both problems, available to
every seat — measured on quince#216, 2026-07-29, where it produced a fresh `gates` run without
anybody re-running anything. **Who does it is responsibility and not exclusivity** — the merging
seat must, so that nothing sits, and the author may and should when its own work is what is
blocked. The full ladder is in §5 above, and rung 1 is the one that matters: **classify before
retrying**, because a retry reached for first is how a genuine failure gets papered over.

**It only helps a branch that is `BEHIND`.** On a branch already current the rebase is a no-op, and
the retry is §5's rung 3 — close and reopen, which re-triggers CI with no commit and no history
(`ci.yml` declares `pull_request:` with no `types:`, so the default set applies and it includes
`reopened`).

Note the rebase moves the head: re-read the range-diff before letting an approval stand, because
GitHub does **not** necessarily dismiss the approval (it did not on quince#216 — verified pure by
identical patch hashes, not assumed).

Only the **Operator** can re-run a run *in place*, preserving its number and its failed attempt.
Nothing above does that; they all produce a new run. Escalate only if neither a rebase nor a reopen
is right for the PR.

**The old trap here said `gh run rerun` exits `0` while printing its refusal. It exits `1`** from
both wrappers above. Do not swap one memorised exit code for another: read the output. That the
recorded code was wrong *and* the recorded capability was wrong, in the same three lines, is the
argument for reading rather than remembering.

**The newest `CAN` was a hazard rather than a convenience, and it is now RETIRED: `quince-bot`
could DELETE any discussion in `quince-devlog`, and the Operator disabled Discussions on the
repository on 2026-07-31, which is the only thing that removes it.** Measured while it was live
on 2026-07-28, both identities, create *and* delete, each probe removing its own artifact
(devlog#30); confirmed gone by `has_discussions=false`. **Nobody granted it.** It arrived with the
classic `repo` scope the token already held, the moment Discussions was *enabled* — a permission
decision nobody made, produced by a container choice.

**Kept rather than deleted, because the shape outlives the instance.** Enabling a GitHub feature
can hand every `repo`-scoped token a capability nobody reviewed, and disabling the feature — not
revoking a grant — is what takes it back. Revoking `discussions: write` from the App would not have
touched this, and the App's grant was a separate thing revoked separately.

**The permission is real and nothing is at stake behind it, and that second clause is new.** This
paragraph used to read *"devlog#30 moves the journal into Discussions … afterwards it is a set of
discussions the implementer can remove with one API call, and nothing on the forge records that a
discussion was deleted."* **That migration did not happen.** It was ruled and then reversed on
2026-07-30 (devlog#30), on evidence that decides it: when `quince-bot` was suspended, **196 of its
commits stayed readable and 0 of its issues and PRs did** — a commit's author is metadata, an
issue's author is a visibility key. The journal would have been authored by agent identities, and
this project had already lost one.

So the journal went to a git branch instead (devlog#152), Discussions is empty, and the delete
permission now guards nothing. **Revoking `discussions: write` from the App would not remove it**
either: it came with classic `repo` scope the moment Discussions was *enabled*, so only disabling
Discussions on the repository does. Kept in this table as the one `CAN` row that is a hazard rather
than a convenience, and as the record of a permission nobody decided to grant.

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

**The journal is the `journal` branch of the devlog, one file per entry** — not `progress.md`,
which since 2026-07-30 holds current state only (devlog#152; the shape was ruled on devlog#30,
reversing an earlier decision to use GitHub Discussions). The branch is **never merged into `main`
and never protected**, because a journal is append-only immutable events with no shared mutable
state, so a PR per entry charges git's full coordination cost for a data structure that needs none.

```
<YYYY-MM>/<YYYY-MM-DD>-<short-slug>.md      one entry, H1 = "# <date> — <the claim>"
```

Entries stay **date-anchored and cite PR/issue numbers**, which GitHub allocates race-free. The
claim appears twice by design — as the H1 the generated index reads, and as the entry's own bold
lead. Regenerate that index with `bin/journal-index` after every append.

**The three tools live on two different branches, and that is easy to get wrong.** `bin/journal-index`
is **on the `journal` branch** — it reads the entry tree, so it ships with it. `bin/dashboard-size`
and `bin/pre-push-journal` are **on `main`**: the first guards `progress.md`, and the second is kept
behind branch protection deliberately, because a control living on the unprotected branch could be
rewritten by the identity it guards (`decisions/0007`).

**A journal entry is ANNOTATED, never rewritten** (`decisions/0006`). An entry that turns out to be
wrong is corrected by addition, with the original left standing: a citation is only worth something
if the text it points at is the text that was there, and a quietly corrected entry destroys the
evidence that anyone was misled.

**Nothing mechanically prevents a rewrite, and nothing reliably detects one either.** Every box holds
a clone, so a rewritten branch *can* be contradicted by another copy — but only by a copy that is
**current**, and **nothing names which commit a journal clone holds.** `preflight` asserts the private
layer's clone **can** fetch, deliberately not that it **has** (quince#121). A stale clone agrees with a
rewrite exactly as readily as a fresh one agrees with the truth.

**The precedent is the argument, and it runs the other way from what it looks like.** When this
project actually needed clone freshness to be *detectable* — quince#220, two boxes running materially
different privacy gates for hours with neither able to tell — replication did not supply it. Somebody
had to build a mechanism: quince#281 made `privacy-check` print `pattern source <commit> <date>`, so a
reader can now see **which list** a sweep used. That is the point. Currency became checkable for the
pattern list because a tool was written to check it, not because clones exist.

So clone replication is **evidence that may survive**, not an integrity control. Against what it
replaced — branch protection, a required approval, linear history, every edit in a diff forever — it
is weaker, and the rule rests on the discipline above rather than on the substrate. The journal branch
has no equivalent of quince#281 and until it does, *"somebody might still hold a copy that disagrees"*
is the honest statement of its backing.

**THE PRIVACY GATE IS THE ONLY GUARD ON THAT BRANCH.** There is no pull request and therefore no
reviewer. Run `make privacy-check REF=origin/journal...HEAD` before every append; `bin/pre-push-journal`
enforces it on the push path and **cannot** see the API path that a box without a git credential
helper must use. The branch's own `README.md` carries both routes.

Lettered entries `(a)`–`(do)` are **retired**: they stay forever as citations from docs
and git history — never mint a new one. Two hazards in that id space, found by the migration and
recorded in `letters.md` on the branch: `(j)`, `(n)` and `(o)` were **never minted**, so the
sequence is not dense and a missing id does not mean a lost entry; and **`(ag)` was minted twice**,
so a citation to it is ambiguous and always was.

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
It lives in the **private `quince-local` repository**, and **the two WORKING boxes hold a full
clone of it** — `deploy/runner/provision` places it and `preflight` refuses to start a box
that cannot reach it (quince#44, ruled 2026-07-27). This paragraph used to say the layer
existed *"only on the Operator's machines"*; that stopped being true the moment work moved
onto the boxes, and a document describing a narrower reality than the one that exists is the
defect class this project keeps filing.

**There are THREE seats and the third deliberately does NOT hold it** — Operator ruling
2026-08-01, relayed at [quince#375](https://github.com/novkostya/quince/issues/375#issuecomment-5147380898).
This sentence said *"both session hosts"* while the supervisor box existed, which was accurate
only because that seat could not publish. quince#375 gave it `contents: write`, and the question
of whether it must therefore carry the layer was ruled **no**.

**The reasoning matters more than the outcome, because the obvious answer was the other one.**
Requiring the layer would make `privacy-check` **runnable** on that box without making it **run**:
nothing invokes the gate on a push except `bin/pre-push-journal`. And the hook needs the pattern
list, so on a box that lacks one it **refuses the push** — fail-closed, which is the direction this
project wants. So the hook alone closes the exposure, and requiring the layer would have bought a
satisfied checklist item plus a third complete copy of the private record on the box that already
holds root ssh into the other two — `pr.6`'s concentration concern, paid for nothing.

**What that ruling makes load-bearing is the journal pre-push hook — and ON THE SUPERVISOR BOX THERE
IS NO WAY TO INSTALL IT.** `refs/heads/journal` has no pull request and therefore no reviewer, and
the paragraph above says the privacy gate is the only guard on it. The analyst's pull-request path
was never the exposure — a PR has a reviewer, and the reviewer sweeps. **The journal path is the
exposure, and its guard is currently undeliverable on the seat that now publishes.**

```
deploy/runner/provision:27
case "$ROLE" in implementer|arch) ;; *) printf 'provision: unknown role %s\n' "$ROLE" >&2; exit 2 ;; esac
```

**`provision` accepts two roles. `preflight` accepts three.** So the supervisor box has never been
provisioned by this script and cannot be: the hook, the git template and `quince.privacy-check` all
arrive through `provision`, and it refuses that role with exit 2. quince#383 is the PR that makes
that refusal *honest* rather than silent; it does not add the role.

**This paragraph asserted the opposite and it was WRONG — caught by the Operator, not by me.** It
read *"§4c … is NOT role-gated — it installs a git template on every box whatever seat it is, so a
provisioned supervisor box gets the guard exactly as the other two do."* Both halves of the evidence
were real and neither was sufficient: §4c genuinely sits outside any `case $ROLE`, and its own
comment genuinely says *"UNCONDITIONAL ACROSS ROLES, including supervisor."* Unconditional **within
a script that never runs for that role** is not unconditional. I read the section and never checked
whether the script would accept the argument, which is the same defect one level up: a claim about
reachable behaviour, verified only at the destination.

**What is therefore true, stated so no one plans against the wrong version:** the fail-closed half
works — a box with no `quince.privacy-check` **refuses** journal pushes rather than making them
unswept — so nothing is currently leaking. But the ruling's chosen control is **unbuilt on the box it
was chosen to protect**, and *"re-run `provision`"* is an instruction nobody can follow there. Either
`provision` learns `--role supervisor`, or the ruling needs a different delivery mechanism. Tracked
on quince#375.

**The gap is FRESHNESS, not a missing mechanism, and this paragraph said otherwise until it was
checked.** It read *"quince#308 is the control, not provisioning hygiene … until `provision` places
the hook on every box"* — which implies work outstanding. **quince#308 is CLOSED, `COMPLETED`,
2026-07-31T09:46:29Z**, and `provision` has done all of this since. What is actually true is that a
box which has not been **re-provisioned** since does not have it: measured on the architect box,
where `preflight` reports `no journal pre-push hook in a git template` and `quince.privacy-check is
unset`. That is the same story `provision` tells about itself for the launchpad (quince#322,
quince#324) — *"provision has not been RE-RUN; that is the same freshness story as everything else
here."*

**Recorded rather than quietly fixed, because the error is this file's own defect class arriving
inside the correction for it.** A ruling was carried into canon citing an issue that had been closed
seven hours earlier, by a session that had read the closed-issue event and not connected it.

**And the correction was itself wrong, in the same paragraph, one revision later.** It said the
remedy was *"an Operator action — re-run `deploy/runner/provision` on each box — not a new work
item."* **That is true for two boxes and impossible for the third**, per `provision:27` above. The
honest split:

- **implementer and arch** — re-run `provision`. A freshness problem, an Operator action, no new work.
- **supervisor** — **there is no command.** The role does not exist in that script. This is a new
  work item, not a re-run, and it is the box the ruling was about.

**Three revisions, three wrong remedies, each more specific than the last** — *build the hook* (it
existed), *re-run provision* (not on that box), and now *provision needs a role it does not have*.
Left standing as a sequence rather than collapsed into the final answer, because the pattern is the
lesson: each version was checked against the thing it named and none was checked against the box it
was for.

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
