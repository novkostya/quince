# `deploy/runner/` — the session host

A persistent container that runs Claude Code sessions, so work survives a closed laptop and the
bot identity belongs to a machine rather than to a session's memory.

Design and gates: [`docs/specs/runner/runner.md`](../../docs/specs/runner/runner.md).

## Standing one up

```sh
devct create --name quince-runner                       # a container, from the same template
ssh quince-runner 'sh -s' < deploy/runner/provision     # packages, claude, service, launchpad
```

`provision` is idempotent and announces each step. It installs Claude Code from the **signed apk
repository**, verifying the signing key against its published checksum and refusing on a mismatch —
no `curl | bash`. On musl the bundled ripgrep is unused, so the system one is installed and
`USE_BUILTIN_RIPGREP=0` is set; without that, search silently returns nothing inside sessions.

Then the part no script can do:

```sh
claude auth login          # full-scope claude.ai login
cd /root/quince && claude  # once, to accept workspace trust
/config                    # "Push when Claude decides" + "Push when actions required"
rc-service quince-runner start
```

**The login is interactive by necessity, not by omission.** A `setup-token` or
`CLAUDE_CODE_OAUTH_TOKEN` credential can only make model requests — Remote Control rejects it with
*"requires a full-scope login token"* — so there is no headless path, and a script that appeared to
offer one would be lying.

## The service comes from the launchpad, which tracks `main`

`provision` installs `/etc/init.d/quince-runner` **from the launchpad clone**, so the runner runs
whatever version of that file is on `main` — not whatever is on the branch you are reading. A change
to the service reaches the box only after it lands **and** the launchpad is pulled:

```sh
ssh quince-runner 'git -C /root/quince pull && sh /root/quince/deploy/runner/provision'
```

This is not a footnote. It is how a runner ended up reporting a false `started` *after* the commit
fixing that had already been written: the fix was on a branch, the box was on `main`. `provision`
now prints the ref it installed from — `service installed from <sha> (<branch>)` — and warns when
the installed file predates the `status()` override, so a service older than its own fix is visible
rather than puzzling.

## What `status` means here

`rc-service` reports on the *supervisor*, which says `started` while the supervised process
crash-loops. The service overrides `status` to answer the question a reader is actually asking:

```
quince-runner: supervisor is up, but NO SESSION — not logged in.   (exit 1)
quince-runner: stopped                                             (exit 3)
```

## Updates

apk installs **never auto-update**, which is deliberate: on a box nobody is watching, an unattended
version bump is a worse failure than a stale version, and it matches how this project pins
everything else. The trade is that nothing would otherwise tell you the runner is behind — so
`preflight` reports (never enforces) when a newer `claude-code` is in the repository:

```sh
apk update && apk upgrade claude-code
```

## Destroying it

`devct destroy` refuses the runner without `--force`: it holds the claude.ai login and the bot
identity, and rebuilding costs a login ceremony. Disposable containers are unaffected.

## What lives here, and what that means

The runner accumulates the claude.ai login, the bot token, the devct API token and an ssh key to
every dev container. Compromise of this box is compromise of the Claude account **and** the
container plane — stated in the spec's security section, along with the constraint that the
root-capable path must only ever reach it as pr.6's forced-command wrapper.
