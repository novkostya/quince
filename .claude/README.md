# `.claude/` — agent configuration

| Path | What | Committed? |
| --- | --- | --- |
| `skills/<name>/SKILL.md` | the workflow as commands: `/onboard`, `/kickoff`, `/report`, `/review-pr`, `/land`, `/qa` | yes |
| `settings.json` | the shared permission allowlist — generic entries **plus** the documented reference environment | yes |
| `settings.local.json` | per-machine bindings: your real host aliases, your registry | **no** (gitignored) |
| `settings.local.json.example` | what a binding file looks like | yes |

Permission rules **merge** across settings files rather than overriding, and precedence for
conflicts is deny → ask → allow, so the committed file can stay generic while your machine
adds what only it knows.

## The two layers

**Committed (`settings.json`) — complete and optional.** It allowlists the project's own
entrypoints: the `make` gate targets, ordinary `git` and `gh` verbs, the container runtimes
that every gate runs inside, and the documentation domains that the
*version-pins-and-interface-facts-are-looked-up-live* rule sends you to. It also carries the
**reference environment** — a Proxmox host and disposable dev containers — under convention
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

| Convention | Means | Bound by |
| --- | --- | --- |
| `quince-pve` | the hypervisor that hosts the dev containers (reference env: Proxmox VE) | an ssh `Host quince-pve` stanza in your `~/.ssh/config`, or your own alias in `settings.local.json` |
| `quince-dev-N` | a disposable dev container: container runtime, no language toolchains, one per unit of work | `Host quince-dev-1`, `quince-dev-2`, … in `~/.ssh/config`; the allowlist matches `quince-dev-*` |

An ssh alias is the whole binding — `ssh quince-dev-1 'make -C /root/quince gates'` works
and matches the allowlist, while the real address stays on your machine. **The tooling that
creates these containers now exists**: `deploy/devct/` ([its README](../deploy/devct/README.md))
builds the template and creates, lists and destroys the containers, and it *generates* the
`quince-dev-N` aliases into `~/.ssh/quince-devct.conf` — so the convention this file describes is
maintained by a program rather than by hand. One line in your own `~/.ssh/config` connects them:

```
Include ~/.ssh/quince-devct.conf
```

`devct onboard` prints that line; it does not edit your ssh config.

## Notes on individual entries

- **`make` targets are listed one by one**, plus `make gates *` / `make image *` for the
  combined and variable forms (`make image push REGISTRY=…`, `make image IMAGE_TAG=…`). Pass
  variables *after* the target: an allow rule doesn't match past a leading `VAR=value`
  assignment, so `IMAGE_TAG=x make image` prompts while `make image IMAGE_TAG=x` doesn't.
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
- **`bin/gh-bot` is allowlisted, and `GH_TOKEN=… gh …` is not — deliberately.** An allow rule
  never matches past a leading `VAR=value` assignment (same reason `make image IMAGE_TAG=x` is
  written in that order above), so `Bash(gh pr *)` grants nothing to a session acting as the
  bot: every PR, issue and body edit would prompt. The wrapper reads the token from its file,
  clears any inherited `GITHUB_TOKEN`, and `exec`s `gh` — token in the process environment,
  never in argv. Missing token file = a loud refusal, never a silent fall back to whoever is
  signed in, because that would author implementer output under the wrong identity. Put it on
  your `PATH` (`ln -s "$PWD/bin/gh-bot" ~/.local/bin/gh-bot`) so `gh-bot pr create …` works from
  any directory; the two path-relative forms are allowlisted for sessions run from a repo root.
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
