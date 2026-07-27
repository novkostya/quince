# runner — a session host that outlives the lid

> Status: **SPEC — code landed for PRs 1–3; gates owed pending the ceremony. Extended for devlog#7 (a second box for the architect), which raises a credential question routed to pr.0b.** Tracked as `pr.5` in the devlog's
> revamp sequence (R6); feature-named. Ruled next on
> [#20](https://github.com/novkostya/quince/pull/20)'s review: it is the only remaining item that
> needs the Operator, so it blocks scheduling while nothing blocks it.

## Goal

A persistent container hosts Claude Code sessions that the Operator can reach from a phone or a
browser, so implementer work survives a closed laptop — and the bot identity stops being a
discipline the session must remember and becomes a property of the machine it runs on.

## Boundary

**In scope:** `deploy/runner/` (provisioning script, OpenRC service, the environment preflight),
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

**Found while building, and load-bearing for the install:**

4. **Alpine is supported (3.19+) and Claude Code publishes a signed apk repository** — so the
   runner needs no distro change and no `curl | bash`. Provisioning adds the repo after verifying
   the signing key against its published checksum, and refuses on a mismatch rather than trusting
   what it downloaded. The `stable` channel is chosen deliberately: apk installs do not auto-update,
   which suits a project that pins toolchains and dislikes silent drift.
5. **On musl, the bundled ripgrep is not used** — `libgcc`, `libstdc++` and the system `ripgrep`
   are runtime dependencies and `USE_BUILTIN_RIPGREP=0` must be set in `settings.json`. Missed, this
   surfaces later as search silently failing inside sessions.

## Design

### The runner is persistent, and `devct` must know that

It is built from the same template as the dev containers (so it arrives with git, make, a container
runtime and `gh`), but it is **not disposable**: it is named `quince-runner` rather than
`quince-dev-N`, and `devct destroy` refuses it unless `--force` is passed. One guard, no new
lifecycle — and the refusal names why, per canon.

### The supervised service, and the guard that runs before it

**Corrected during the build: OpenRC, not systemd.** The spec's first draft said "systemd unit".
The template is Alpine — the Operator's pr.2 ruling kept Alpine everywhere for dev/staging parity —
and **Alpine ships OpenRC with no systemd at all** (verified on a live container: `rc-service`
present, `systemctl` absent, `supervise-daemon` at `/sbin/supervise-daemon`). Writing a systemd unit
would have produced a file nothing on that host could run.

```
/etc/init.d/quince-runner          # openrc-run, supervisor=supervise-daemon
  start_pre  → deploy/runner/preflight     # refuses on a mis-set environment
  command    → claude remote-control --spawn same-dir --name quince-runner
  respawn    → --respawn-delay 10 --respawn-max 0   (unlimited: outages are the normal case)
```

`supervise-daemon` supplies the restart-always semantics, and it needs them for the measured reason
in fact 3 rather than for tidiness.

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
Desktop, or cloud). devlog#4 specced a supervised timer plus a dispatcher; one of these may be the
better substrate. **Recorded, not acted on** — rung-loop's design was reviewed and ruled, and
quietly re-architecting it here would be exactly the improvisation its own point 2 forbids.

## Stories

1. `preflight` refuses to start the unit when `ANTHROPIC_API_KEY` is set, naming the variable.
2. `preflight` refuses when `ANTHROPIC_BASE_URL` points elsewhere, naming it.
3. `preflight` refuses when the bot token is missing or world-readable.
4. With a clean environment the unit starts and the session appears in the session list.
5. The Operator connects from a phone and sends a message that reaches the session.
6. Killing the process causes `supervise-daemon` to restart it, and a new session registers.
7. The container reboots and the unit comes back without intervention.
8. `devct destroy` refuses the runner without `--force`, naming why.
9. A session **on the runner** completes an implementer loop end to end: fresh clone → `devct
   create` → gates → PR opened as the bot.

## Gates

- **G1 (no runner needed)** — `preflight` against a table of environments: each rejection names the
  offending variable; the clean case exits 0 (stories 1–3). **Run by `make preflight-test`, which
  `make gates-sh` invokes**, so the table is exercised on every PR rather than by whoever remembers
  (quince#32). Thirteen cases, all synthetic — a stub `claude`, fake token files at 600 and 644, no
  private layer and no runner — asserting the **refusals**, since preflight's failure paths are its
  whole product. Each asserts the message as well as the exit code: preflight returns 1 for every
  refusal, so the code alone cannot tell *you set an API key* from *this box holds the wrong
  identity*, and conflating those is how someone fixes the wrong thing.
- **G2** — the service starts, `rc-service quince-runner status` reports a live session, and it appears at
  claude.ai/code (story 4). **Operator leg:** connect from the phone and send one message (story 5).
- **G3** — `kill` the process; `supervise-daemon` restarts it; a new session registers. The report states the
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

## Two boxes, not one — the architect gets its own runner

Taken from [devlog#7](https://github.com/novkostya/quince-devlog/issues/7). The finding is **not a
symmetry argument, it is a boundary**: this rung's stated win is that the *implementer* identity
becomes structural, and it achieves that by putting the bot token on a machine. An architect session
needs a credential that can **approve and merge**. Put both on one box and that box can author *and*
approve — `approver ≠ author` stops being structural and becomes a convention about which token a
session happens to pick up, which is the property R1's whole identity ruling exists to protect.

| box | holds | runs | must NOT hold |
| --- | --- | --- | --- |
| `quince-runner` | bot token (0600), devct token, dev-CT ssh key | implementer sessions | any approve-capable credential |
| `quince-arch` | the architect's GitHub credential; optionally the devct token (pool-scoped, guards proven in [#15](https://github.com/novkostya/quince/pull/15)) for verification runs | review, approve, land, and the review loop | the bot token |

A compromised implementer box then cannot approve its own work; a compromised architect box cannot
author as the bot. This is the credential-concentration finding above, one level up, and it is the
same fix: separate the things whose combination is the risk.

**Costs, stated:** one more devct-cloned container (cheap) and a **second ceremony** — Claude auth is
per-machine, so `claude auth login` twice, ~10 minutes rather than 5. Both boxes register their own
Remote Control session and both appear in the picker.

**The Mac becomes purely a client.** Both roles are reachable from laptop or phone; neither depends
on a lid staying open, which was pr.5's point in the first place.

### The credential question this raises, and does not answer

`quince-arch` needs something that can approve. Today that is the **Operator's personal GitHub
account** — so provisioning the box with a personal PAT means a compromise there acts as him on
GitHub *generally*, not merely on this project. That is a materially worse blast radius than the
bot's repo-scoped token, and it is exactly the argument **pr.0b** (architect as a GitHub App) was
deferred on when the cost seemed theoretical. It is not theoretical once the box exists.

**This spec does not decide it.** Per the gap protocol, the shape of an authority credential is
architectural:

> `PROPOSED (gap):` build `quince-arch` now with its Claude login and (optionally) the pool-scoped
> devct token, and **defer the GitHub credential until pr.0b is ruled**. The box is useful without
> it — it can run the review protocol, the gates, and verification runs — and every approval keeps
> happening from wherever the architect is until the ruling lands. If pr.0b is accepted, the box
> receives an App token and never holds a personal one; if it is declined, the personal PAT goes on
> with its blast radius written down.

Building the container while the provisioning path is warm is cheap; putting a personal credential
on it before that ruling is not reversible in the way the container is.

### Evidence check, since this issue is about identity separation

Before writing this I checked what the record actually shows for every PR in pr.5 and its journals:
all five were authored by `quince-bot` and approved by `novkostya`. **The separation held in
practice** — which is the point worth making about *why* this rung matters: it held because a human
was doing the approving from his own machine, not because anything structural prevented the
alternative. Two boxes is what turns that from a habit into a property.

## PR sequence

1. **this spec** — the design and the three corrected facts, reviewed before code.
2. **`preflight` + the unit + provisioning** — G1 offline, then G2–G5 on the box.
3. **the ceremony + G6** — the Operator's five minutes, then an implementer loop run entirely from
   the runner. That last gate is what closes the rung: the session that opens its PR is running on
   the machine this rung built. ✔ code landed; **gates owed** — the ceremony is postponed (the
   sign-in step asked for a recent authentication), so no session has existed yet.
4. **`quince-arch`** — the second box, per devlog#7: provisioned the same way, holding the Claude
   login and no bot token, with a `preflight` variant that asserts **the absence** of
   `~/.config/quince/quince-bot.token` rather than its presence. That inversion is the whole point:
   each box refuses to hold the other's identity, so the separation is enforced by the thing that
   starts the service rather than by whoever provisioned it. **The GitHub credential waits on the
   pr.0b ruling** (above).

### Gates for PR 4

- **G7** — `quince-arch`'s preflight **refuses to start when a bot token is present**, naming it.
  Exercised by planting one, since a guard proven only in the absence of the thing it guards against
  is not proven.
- **G8** — both boxes register distinct Remote Control sessions and both appear in the picker with
  their own names (needs both ceremonies).
- **G9** — from `quince-arch`, the review protocol runs end to end on a real PR: gates executed,
  verdict submitted. Approval identity is whatever pr.0b rules; the gate is that the *work* happens
  there, not on a laptop.
