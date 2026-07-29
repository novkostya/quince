# `.claude/` — agent configuration

| Path | What | Committed? |
| --- | --- | --- |
| `skills/<name>/SKILL.md` | the workflow as commands: `/onboard`, `/architect`, `/kickoff`, `/report`, `/review-pr`, `/land`, `/qa` | yes |
| `settings.json` | the shared permission allowlist — generic entries **plus** the documented reference environment — and the one `Stop` hook (below) | yes |
| `settings.local.json` | per-machine bindings: your real host aliases, your registry | **no** (gitignored) |
| `settings.local.json.example` | what a binding file looks like | yes |
| `loop-protocol.md` | the coroutine loop, both halves — normative for `/architect` and `/kickoff` | yes |
| `forge-set` | the repositories the loop watches, one `owner/name` per line | yes |

Permission rules **merge** across settings files rather than overriding, and precedence for
conflicts is deny → ask → allow, so the committed file can stay generic while your machine
adds what only it knows.

## The one hook, and what it does to your session

`settings.json` registers a single `Stop` hook: `bin/forge-watch owed --hook`. When a turn ends it asks
whether this box has **open PRs with no live watch** — the ones you authored, or the declared set if
this box holds the architect credential — and if so it **blocks the stop once**, naming the exact
command to arm one. End the turn again and it stops blocking and warns *you* instead. It exists because
a session with that instruction in prose armed nothing at all and stopped anyway (quince#62); the
reasoning is in [`loop-protocol.md`](loop-protocol.md) and the design in
[`../docs/specs/rung-loop/rung-loop.md`](../docs/specs/rung-loop/rung-loop.md) §4f.

Three things worth knowing before it surprises you:

- **It runs on clone, without the trust dialog.** Hooks are not gated by workspace trust — permission
  entries are, and are ignored in an untrusted workspace while hooks still run (observed). That is the
  property we want, since a gate a session can skip by declining to trust the workspace is not a gate;
  it also means checking this repository out means running this command at the end of every turn. It is
  one local script, it makes one `gh` call, and it writes nothing.
- **It puts a forge round-trip on the end of every turn.** One search call on the implementer side
  (~1.1 s measured); on the architect side **one `pr list` per repository in `.claude/forge-set`**, so
  the cost grows with the declared set. A 30 s timeout bounds it, so a hanging forge cannot hold a
  session open — but this is now on the latency and rate-limit path of every turn, which is worth
  knowing before you add the fourth repository.
- **If it cannot answer it says so and lets you go.** No credential, or a forge that will not respond,
  produces a warning that the question was *not checked* — never a block, and never a quiet pass.

To turn it off on your machine, put `"disableAllHooks": true` in your gitignored
`settings.local.json` — checked, not assumed, and the obvious guess is wrong: **hooks merge across
settings files rather than overriding**, so `"hooks": {"Stop": []}` locally does *not* remove the
project's (verified — the hook still ran). Note what the working switch costs: it is all-or-nothing,
so a machine that opts out of this gate opts out of every hook this repo ever adds. That is a local
decision, and it is visible in a file you own; what is not available is turning it off by forgetting
it exists.

## The two layers

**Committed (`settings.json`) — complete and optional.** It allowlists the project's own
entrypoints: the `make` gate targets, ordinary `git` and `gh` verbs, the container runtimes
that every gate runs inside, and the documentation domains that the
*version-pins-and-interface-facts-are-looked-up-live* rule sends you to. It also carries the
**reference environment** — a Proxmox host and the containers it provisions — under convention
names only. Those entries are inert on a machine where the names don't resolve, so nothing
about this repo requires Proxmox: it is the setup that is documented and known to work, not
a requirement.

**Local (`settings.local.json`) — bindings only.** Real hostnames, IPs, registry names, and
anything else site-specific live here, or in your `~/.ssh/config`. This file is gitignored;
the public repo stays site-neutral. Copy the `.example` and edit:

```sh
cp .claude/settings.local.json.example .claude/settings.local.json
```

## Convention names

The committed allowlist refers to hosts by convention, never by address:

| Convention | Means | Bound by | State |
| --- | --- | --- | --- |
| `quince-pve` | the hypervisor that hosts the containers (reference env: Proxmox VE) | an ssh `Host quince-pve` stanza in your `~/.ssh/config`, or your own alias in `settings.local.json` | live |
| `quince-dev-N` | a disposable dev container, one per unit of work | `Host quince-dev-1`, `quince-dev-2`, … in `~/.ssh/config`; the allowlist matches `quince-dev-*` | **retired** — below |

An ssh alias is the whole binding — `ssh quince-pve pct list` works and matches the allowlist
while the real address stays on your machine. That is the property the table exists for, and it
still holds for `quince-pve`. It no longer describes how work reaches a gate host.

**The boxes that run sessions are not in that table, because nothing sshes to them.**
`quince-runner` (implementer) and `quince-arch` (architect) are persistent hosts; a session runs
*on* one, clones into `$HOME/scratch/<runner>/`, and runs `make gates` there. No alias, no
allowlist entry, no remote invocation. `deploy/devct/` ([its README](../deploy/devct/README.md))
is the toolkit for building them — `devct-template`, `onboard`, `doctor`, `list`, and
`create --name <host> --role <implementer|arch>` are that path.

**How the boxes you are on were actually built is not settled, and this paragraph is not going to
pretend otherwise.** `devct`'s header offered `/etc/quince-devct-stamp` as the proof; that file is
**absent on both boxes**, measured 2026-07-29 (the header now says so, with what was and was not
established). quince#205 records that the current fleet was provisioned from an Operator-local
template factory that is not in this repo, and owns resolving it.

**`quince-dev-N` is the retired half of that same toolkit.** The Operator ruled on 2026-07-28
that there are no dev containers and the runner is the work host, which retires `devct create <n>`'s
numbered aliases, `destroy` as a routine step, and `deploy`. **Cite the ruling, not the issue:** it
is a [comment on quince-devlog#45](https://github.com/novkostya/quince-devlog/issues/45#issuecomment-5101807117),
and that issue's *body* is the spike that preceded it — which still lists "the implementer creates
its own dev CT … one runner, one CT" under **Settled**. Anyone who checks the bare number lands on
text that says the opposite of the decision.

Two things follow, and they point in opposite directions on purpose:

- **The verbs still exist, and `deploy/devct/` must not be deleted.** The ruling retired a
  *workflow*, not the toolkit, and quince#181 records a session coming within one command of
  deleting the directory on the strength of three stale sources that all predated the same change.
  It is this repo's only committed provisioning path; what supersedes it is quince#205's to decide,
  not a cleanup's.
- **The two `quince-dev-*` allowlist entries are left standing, inert rather than wrong.** Removing
  them is part of re-pointing every `devct` reference in this repo, which is quince#205's second
  half; doing it here would be a config change smuggled into a documentation fix.

`devct create <n>` still generates `quince-dev-N` aliases into `~/.ssh/quince-devct.conf`, and
`devct onboard` still prints the one line that includes them (it does not edit your ssh config):

```
Include ~/.ssh/quince-devct.conf
```

Nothing in the current workflow needs either.

## Notes on individual entries

- **`make` targets are listed one by one**, plus `make gates *` / `make image *` /
  `make privacy-check *` for the combined and variable forms (`make image push REGISTRY=…`,
  `make privacy-check REF=origin/main...HEAD TEXT="$BODY"`). Pass variables *after* the
  target: an allow rule doesn't match past a leading `VAR=value` assignment, so
  `IMAGE_TAG=x make image` prompts while `make image IMAGE_TAG=x` doesn't. **A bare entry does
  not cover the variable form** — `Bash(make privacy-check)` matched only the argument-less
  invocation while every form canon instructs carries `REF=`, which is quince#214 and is the
  reason the `*` twin is not optional for any target a skill parameterises.
- **`Bash(nerdctl *)` / `Bash(docker *)` are broad on purpose** — every gate runs inside a
  pinned toolchain container, so a narrower rule would prompt on every gate. Be aware that
  `docker run` and `docker exec` can run arbitrary code: this grant assumes the box is a
  disposable dev container, which is exactly why dev containers are disposable. **If you run
  sessions on a workstation that has a container runtime installed** — against the project's
  own rule, but it happens — that assumption doesn't hold there: move the two runtime lines
  into your `settings.local.json` (or narrow them to the exact commands you run) so the
  committed file keeps granting them only where they are cheap to lose.
- **`Bash(ssh quince-pve pct *)`** is narrow deliberately. Host-side container lifecycle is
  moving to a scoped API token (pr.2), and root on the hypervisor is meant to be rare and
  supervised, not a standing convenience.
- **The forge wrappers are allowlisted, and `GH_TOKEN=… gh …` is not — deliberately.** An allow
  rule never matches past a leading `VAR=value` assignment (same reason `make image IMAGE_TAG=x` is
  written in that order above), so `Bash(gh pr *)` grants nothing to a session acting as an
  identity other than the logged-in one: every PR, issue and body edit would prompt. Each wrapper
  instead resolves its own credential and `exec`s the real tool with the secret in the process
  environment, never in argv. A missing credential is a loud refusal, never a silent fall back to
  whoever is signed in, because that would author output under the wrong identity. Put the one your
  seat uses on your `PATH` (`ln -s "$PWD/bin/gh-coder" ~/.local/bin/gh-coder`) so
  `gh-coder pr create …` works from any directory; the path-relative forms are allowlisted too, for
  sessions run from a repo root.
  - `bin/gh-coder` + `bin/git-coder` — the **implementer**, a GitHub App. `git-coder` exists
    because git has no idea what an App is: it is both a credential *helper* (what `/kickoff` §3
    wires into a clone) and a `git` wrapper you can call directly, and either way the installation
    token reaches git on a pipe rather than in a push URL.
  - `bin/gh-arch` — the **architect**. `bin/gh-review` — the **reviewer** App, which is also how
    canon is authored (`/architect` §1).
  - `bin/gh-bot` — the **retired** implementer machine account, suspended 2026-07-28. Its entries
    are inert: the token cannot authenticate. They stay because `quince-devlog` `decisions/0014`
    condition 1 is that nothing is tidied away — the intact record is what makes the good-faith
    claim checkable — and removing an allowlist line is close enough to tidying to be worth not
    doing by reflex.
- **`Bash(bin/forge-watch *)` is allowlisted because the loop cannot ask.** The watcher runs
  detached and ticks on an interval; a permission prompt per tick would stall the whole coroutine on
  a human tap, which is the one thing this loop exists to remove. It is also the cheapest grant in
  the file: `forge-watch` reads the forge through whichever `gh` wrapper the caller names, writes one
  JSON state file per repo under `$XDG_STATE_HOME`, and holds no credential of its own.
  `bin/scratch-reap` is allowlisted for the mirror-image reason: canon tells every session to make
  a fresh clone per unit of work and nothing removed them, so 33 accumulated in two days
  (quince#45, quince#111). A reaper a session has to ask permission to run is a reaper that does
  not run. It reaps `$HOME/scratch/<runner>` and only that root.
- **`Read(~/.config/quince/**)` is denied.** That directory holds the bot token and other
  credentials; denying the Read tool keeps their contents out of a transcript. Piping a
  token into an environment variable (`GH_TOKEN=$(cat …)`) or into git's credential helper
  still works — that is how `/kickoff` and `/report` authenticate.
- **Direct pushes to `main` are denied**, mirroring branch protection so the rule is visible
  locally instead of only as a server-side refusal. Force-pushing a *PR branch* is not denied
  — branches are session-local and unshared, so amending and re-pushing one (with
  `--force-with-lease`) is routine; `main` is what must never be rewritten.
- **The Proxmox API is reached through `deploy/devct/`, not through a raw `curl` entry.** The
  old entry constrained an argument, which is fragile by nature (a reordered flag stops
  matching), and it has been replaced by the scripts themselves — the allowlist-the-script
  move this file proposed before those scripts existed. The scripts are the narrower grant in
  practice: they talk to one API with one token, they refuse any container outside the pool,
  and they never disable TLS verification (`make gates-sh` fails the build if `curl -k`
  appears in that tree).
