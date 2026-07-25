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
and matches the allowlist, while the real address stays on your machine. The provisioning
tooling that creates these containers, and the fuller definition of the convention, is the
`deploy/devct/` work (pr.2); this file is the contract it binds to.

## Notes on individual entries

- **`make` targets are listed one by one**, plus `make gates *` / `make image *` for the
  combined and variable forms (`make image push REGISTRY=…`, `make image IMAGE_TAG=…`). Pass
  variables *after* the target: an allow rule doesn't match past a leading `VAR=value`
  assignment, so `IMAGE_TAG=x make image` prompts while `make image IMAGE_TAG=x` doesn't.
- **`Bash(nerdctl *)` / `Bash(docker *)` are broad on purpose** — every gate runs inside a
  pinned toolchain container, so a narrower rule would prompt on every gate. Be aware that
  `docker run` and `docker exec` can run arbitrary code: this grant assumes the box is a
  disposable dev container, which is exactly why dev containers are disposable.
- **`Bash(ssh quince-pve pct *)`** is narrow deliberately. Host-side container lifecycle is
  moving to a scoped API token (pr.2), and root on the hypervisor is meant to be rare and
  supervised, not a standing convenience.
- **`Read(~/.config/quince/**)` is denied.** That directory holds the bot token and other
  credentials; denying the Read tool keeps their contents out of a transcript. Piping a
  token into an environment variable (`GH_TOKEN=$(cat …)`) or into git's credential helper
  still works — that is how `/kickoff` and `/report` authenticate.
- **Direct pushes to `main` are denied**, mirroring branch protection so the rule is visible
  locally instead of only as a server-side refusal. Force-pushing a *PR branch* is not denied
  — branches are session-local and unshared, so amending and re-pushing one (with
  `--force-with-lease`) is routine; `main` is what must never be rewritten.
- The curl entry for the Proxmox API constrains an argument, which is fragile by nature
  (a reordered flag won't match). When `deploy/devct/` ships a wrapper script, the honest
  fix is to allowlist the script instead.
