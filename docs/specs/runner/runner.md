# runner — a session host that outlives the lid

> Status: **SPEC — ruled on first review (credential concentration + two clarifications), awaiting re-review.** No code exists. Tracked as `pr.5` in the devlog's
> revamp sequence (R6); feature-named. Ruled next on
> [#20](https://github.com/novkostya/quince/pull/20)'s review: it is the only remaining item that
> needs the Operator, so it blocks scheduling while nothing blocks it.

## Goal

A persistent container hosts Claude Code sessions that the Operator can reach from a phone or a
browser, so implementer work survives a closed laptop — and the bot identity stops being a
discipline the session must remember and becomes a property of the machine it runs on.

## Boundary

**In scope:** `deploy/runner/` (provisioning script, systemd unit, the environment preflight),
a `devct` guard so the runner is not treated as disposable, `docs/specs/runner/`, and the
`deploy/dev.md` pointer.

**Out of scope:** the rung-loop implementation ([devlog#4](https://github.com/novkostya/quince-devlog/issues/4)
— explicitly after this); staging or product deployment; product code (frozen); anything under
`.github/workflows/`.

**Expected contract changes: NONE.**

## Interface facts — looked up live (2026-07-26), because R6 predates them

R6's design notes were doc-verified when written. Re-checked against the current Remote Control
documentation before speccing, which **confirmed three and corrected or added three**:

**Confirmed:** `claude remote-control` is server mode; the connection is **outbound HTTPS only, no
inbound ports, no VPN or port-forward** (it registers with the Anthropic API and polls); `--spawn`
takes `same-dir` | `worktree` | `session`; `--capacity` defaults to **32**; Remote Control requires a
claude.ai subscription login and **API keys are not supported**; workspace trust must be accepted
once by running `claude` in the project directory.

**Corrected or newly load-bearing:**

1. **`ANTHROPIC_BASE_URL` disables Remote Control** if it points anywhere other than
   `api.anthropic.com` (LLM gateway, proxy). R6's structural guard named only the API-key variables;
   it needs a third assertion.
2. **A `setup-token` / `CLAUDE_CODE_OAUTH_TOKEN` credential cannot establish Remote Control** —
   those can only make model requests, and the CLI reports *"requires a full-scope login token"*.
   So the runner **cannot be provisioned headlessly with a token**: the Operator's interactive
   `claude auth login` is not a convenience, it is the only path.
3. **An extended network outage (~10 min) times the session out and the process exits.** That makes
   supervision a correctness requirement rather than tidiness, and it means a restart produces a
   *new* session — which the design has to state rather than paper over.

## Design

### The runner is persistent, and `devct` must know that

It is built from the same template as the dev containers (so it arrives with git, make, a container
runtime and `gh`), but it is **not disposable**: it is named `quince-runner` rather than
`quince-dev-N`, and `devct destroy` refuses it unless `--force` is passed. One guard, no new
lifecycle — and the refusal names why, per canon.

### The systemd unit, and the guard that runs before it

```
ExecStartPre=  deploy/runner/preflight        # refuses to start on a mis-set environment
ExecStart=     claude remote-control --spawn same-dir --name quince-runner
Restart=always
```

**`deploy/runner/preflight` asserts, and names what it found:**

- `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `CLAUDE_CODE_OAUTH_TOKEN` — **unset**. An API key
  present takes precedence over the subscription login, which is R6's one billing trap; a
  setup-token cannot do Remote Control at all.
- `ANTHROPIC_BASE_URL` — unset, or exactly `api.anthropic.com` (fact 1 above).
- the bot token present at `~/.config/quince/quince-bot.token`, mode 600.

It refuses to start otherwise. A runner that silently comes up unable to do Remote Control, or
billing against an API key, is precisely the failure this project has canon about.

### `--spawn same-dir`, not `worktree` — deliberately

`worktree` mode is the obvious pick and it is the wrong one *here*: `CLAUDE.md` retired worktrees in
favour of **a fresh clone per unit of work**, and a session that begins in a worktree begins outside
the ruled workflow. `same-dir` plus the existing clone-per-unit rule keeps one story about where work
happens. Recorded as a decision, not an omission — if the reviewer prefers `worktree`, it is a
one-flag change and the workflow rule is what would need revisiting.

**The landing directory is a launchpad, never a workspace.** With `--capacity 32`, several sessions
can land in that same directory at once, and the ruled workflow only saves them because each clones
per unit of work before touching anything. So the runner's directory is a **pristine checkout used
to start sessions** — never edited in place, never a branch, never a build. A session that finds
itself editing there has already left the workflow, and the provisioning keeps it clean: read-only
in spirit, and any stray state is a finding rather than a habit.

### Restart produces a new session, and that is the correct behaviour

Because the process exits after ~10 minutes offline, `Restart=always` is required, and each restart
registers a **new** Remote Control session rather than resuming the old one. That fits the rung-loop
ruling exactly — resume means *a fresh session against the PR thread*, never a long-lived process
holding context — so the restart semantics and the loop design agree by construction rather than by
luck.

**How the Operator finds the live one.** `--name quince-runner` sets the session title in the picker
at claude.ai/code, so the runner is identifiable rather than an auto-generated
`hostname-graceful-unicorn`. What the docs do *not* state is whether superseded sessions expire on
their own: they note only that a pre-v2.1.200 reconnection bug "left extra sessions in the session
list", which implies they linger rather than self-prune. **Recorded as unverified, and G3 measures
it** — count the `quince-runner` entries before and after a restart, and report the number. If they
accumulate, the fix is a naming or pruning step and it belongs in PR 2 of this rung; a picker with a
dozen dead runners is a small failure but a daily one.

### What the Operator does once

1. `claude auth login` on the runner (full-scope claude.ai login — the only path, per fact 2).
2. Run `claude` once in the repo directory to accept **workspace trust**.
3. `/config` → enable **Push when Claude decides** and **Push when actions required**, so the
   rung-loop's escalations reach a phone.

~5 minutes, once. Everything else provisions itself.

### Security — and what this box is about to accumulate

The runner holds a **full-scope claude.ai login for the Operator's account**: anyone with root on
that container can drive Claude as the Operator. That is inherent to Remote Control on a server, not
a defect of this design, and the ordinary mitigations apply — no inbound ports (the connection is
outbound-only), ssh-key access, and a container at the same trust level as the laptop that would
otherwise hold the session.

**But the login is not what makes this host interesting to an attacker.** Follow the sequence
through and the runner ends up holding, on one box:

| Credential | Arrives with | Reaches |
| --- | --- | --- |
| claude.ai full-scope login | this rung | the Operator's Claude account |
| `quince-bot` token | this rung | the product repo and the devlog |
| devct API token | first `devct` use from the runner | the hypervisor's container plane, pool-scoped |
| ssh key for `quince-dev-*` | same | every dev container |
| root path for template builds | pr.6, if built carelessly | `pct` on the hypervisor |

So **compromise of the runner is compromise of the Operator's Claude account *and* the container
plane** — a concentration this rung creates and should name rather than discover later.

**The intended endgame, so pr.6 inherits a constraint rather than a free hand:** the root-capable
path must reach the runner **only** as pr.6's forced-command wrapper — the two allowed command
shapes (`pct set -features …`, `pct exec …`), pool-verified, and nothing else — never a general root
key. That is the zfs-helper pattern for the third time in this project, and pr.6 already owes it.
Everything above the wrapper stays scoped: the devct token is pool-bound and its refusals are proven
([#15](https://github.com/novkostya/quince/pull/15)), and the bot token is repo-scoped with no
`workflow` scope.

This rung does not build the wrapper. It states the constraint so that building it is not optional.

**What the runner buys in exchange**: implementer identity becomes structural. The bot token lives
there and the Operator's own credentials do not, so *approver ≠ author* stops depending on a session
remembering which identity to use ([devlog#1](https://github.com/novkostya/quince-devlog/issues/1)
item 5).

### Forward note — not this rung

The lookup surfaced two native mechanisms the rung-loop spec did not consider: **Channels** (external
events — a chat app or your own server — waking a local session) and **scheduled tasks** (CLI,
Desktop, or cloud). devlog#4 specced a systemd timer plus a dispatcher; one of these may be the
better substrate. **Recorded, not acted on** — rung-loop's design was reviewed and ruled, and
quietly re-architecting it here would be exactly the improvisation its own point 2 forbids.

## Stories

1. `preflight` refuses to start the unit when `ANTHROPIC_API_KEY` is set, naming the variable.
2. `preflight` refuses when `ANTHROPIC_BASE_URL` points elsewhere, naming it.
3. `preflight` refuses when the bot token is missing or world-readable.
4. With a clean environment the unit starts and the session appears in the session list.
5. The Operator connects from a phone and sends a message that reaches the session.
6. Killing the process causes systemd to restart it, and a new session registers.
7. The container reboots and the unit comes back without intervention.
8. `devct destroy` refuses the runner without `--force`, naming why.
9. A session **on the runner** completes an implementer loop end to end: fresh clone → `devct
   create` → gates → PR opened as the bot.

## Gates

- **G1 (no runner needed)** — `preflight` against a table of environments: each rejection names the
  offending variable; the clean case exits 0 (stories 1–3).
- **G2** — the unit starts, `systemctl status` shows it active, the session appears at
  claude.ai/code (story 4). **Operator leg:** connect from the phone and send one message (story 5).
- **G3** — `kill` the process; systemd restarts it; a new session registers. The report states the
  session id changed rather than implying continuity, **and counts the `quince-runner` entries in
  the picker before and after**, which settles whether superseded sessions linger (story 6). A
  number, not an impression.
- **G4** — `reboot` the container; the unit returns (story 7).
- **G5** — `devct destroy quince-runner` refuses; `--force` destroys (story 8) — exercised against a
  throwaway container named like the runner, **never against the runner itself**, which is the same
  guard-proving discipline #15 used for the template.
- **G6 (the point of the rung)** — from a session on the runner: fresh clone, `devct create`, `make
  gates`, PR opened as `quince-bot` (story 9). This also demonstrates that the Operator's personal
  credentials are absent there.
- `make gates-sh` covers the new scripts.

## Fixtures

None beyond `preflight`'s environment table, which is inline in its test.

## Rule check

- **Secrets** — the bot token is placed by provisioning at mode 600 and never printed; the claude.ai
  login is interactive and never scripted; `preflight` reads variable *names* and reports names, not
  values.
- **State honesty** — every refusal names what it observed; a restart reports a new session rather
  than implying the old one continued.
- **No silent fallbacks** — a mis-set environment stops the unit; it never starts degraded, and it
  never quietly bills against an API key.
- **Privacy** — the committed unit and scripts use convention names only; no address, no hostname.
- **Credential concentration** — named as a consequence of this rung, with the endgame constraint
  written down: the root-capable path reaches the runner only as pr.6's forced-command wrapper,
  never a general root key. A rung that creates a concentration and does not say so is the same
  failure as a message that claims a condition nobody checked.
- **Boundary** — no product code, no contracts, no workflow files; `devct` gains one guard. The
  wrapper itself is pr.6's, stated here as a constraint rather than built here as scope creep.
- **Approver ≠ author** — the runner hosts implementer sessions and holds the bot identity only.
  Nothing here approves or merges.
- **Interface facts looked up live** — the three corrections above were found by reading the current
  docs rather than trusting R6, and the base-URL trap in particular would have shipped a runner that
  could not do the one thing it exists for.
- **Resurrection test** — a stranger with their own Proxmox box and their own claude.ai login runs
  the same provisioning; only the login is theirs.

## PR sequence

1. **this spec** — the design and the three corrected facts, reviewed before code.
2. **`preflight` + the unit + provisioning** — G1 offline, then G2–G5 on the box.
3. **the ceremony + G6** — the Operator's five minutes, then an implementer loop run entirely from
   the runner. That last gate is what closes the rung: the session that opens its PR is running on
   the machine this rung built.
