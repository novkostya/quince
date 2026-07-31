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

## The private layer is a start condition

`preflight` **refuses to start** a box that cannot reach `/root/quince-local/privacy-patterns.txt`
(quince#44, Operator ruling 2026-07-27). The privacy gate runs on every push from a session host,
and a box that comes up unable to run it is the failure that issue was filed about: sessions had
learned to trust a file that a rebuild silently removes, with nothing announcing the regression.

Until `provision` places the layer, a rebuilt box needs it hand-placed:

```sh
# from a machine that has it — piped, never in argv, never printed
ssh <box> 'mkdir -p /root/quince-local && chmod 700 /root/quince-local &&
           cat > /root/quince-local/privacy-patterns.txt && chmod 600 /root/quince-local/privacy-patterns.txt' \
  < privacy-patterns.txt
```

The refusal **does not say why** the layer is unreachable, deliberately: a private repository
returns 404 for both "does not exist" and "you were not granted it", so a message that guessed would
send the next reader off to recreate a repo when the real fix is a one-click collaborator grant.

An **empty** list is refused as well, and it is the worse case: it matches nothing and reports every
sweep clean, so it looks like the gate ran. A world-readable list is reported but not enforced —
`chmod 600` it, but a permission bit will not stop the box booting.

## The journal pre-push hook is a property of the box, not of a clone

`quince-devlog`'s `journal` branch is appended to **directly — no pull request, so no reviewer**. Its
`bin/pre-push-journal` refuses a push that has not come back clean from the privacy gate, and git does
not distribute hooks: it was in force only where somebody remembered to run `--install` (quince#308).

`provision` closes that by writing a **git template**, because a template acts at **clone** time and a
scratch clone made an hour later is where journal entries are actually written:

| what | where |
| --- | --- |
| the delegate — the real hook | `/root/quince-devlog`, reset to `origin/main` on every `provision` run |
| the template hook — a shim | `/root/.config/git/template/hooks/pre-push` |
| wiring | `git config --global init.templateDir` + `quince.privacy-check` |

**The template carries a shim, not a copy.** A copy is taken once and then goes stale, and this hook's
own reach was already corrected once in its first week (quince-devlog#159 narrowed what
quince-devlog#155 claimed). A stale privacy control that still reports clean is the shape quince#121
named. So every clone runs whatever `provision` last pulled, and updating it is a `git pull`.

**It cannot brick the box.** The shim narrows to `refs/heads/journal` *before* it requires the
delegate, so an ordinary push succeeds even with no devlog clone present at all — and a journal push
refuses. Both directions are pinned by `make pre-push-shim-test`.

**Not `core.hooksPath`.** That would need no template and could never go stale, and it replaces
`.git/hooks` for every repository on the box — so any repo-local hook installed afterwards is
silently inert. The template costs a copy per clone; `hooksPath` costs the ability to notice.

Two limits, stated because neither is visible from the box:

- **Clones made before the run are not retrofitted.** A template acts at clone time and cannot reach
  backwards. Scratch clones are short-lived (`bin/scratch-reap`), so the window closes on its own.
- **`preflight` reports this and does not refuse.** Whether a box that can author journal entries
  with no gate should be allowed to *start* is quince#308 step 4 — the same refuse-or-degrade
  question the private layer got, ruled there and unruled here.
