# `deploy/devct/` — disposable dev containers on Proxmox

quince's gates run on a container host, never on a workstation (`deploy/dev.md`). `devct`
provisions those hosts: a template built once per `versions.env` change, and disposable
containers cloned from it per unit of work.

Everything here is **site-neutral**. Hosts, pools, storages, bridges and registries are
parameters read from `~/.config/quince/devct.conf`, which is never committed. A stranger with
their own Proxmox box and their own credentials runs the same scripts.

Design and acceptance gates: [`docs/specs/devct/devct.md`](../../docs/specs/devct/devct.md).

## What exists today

| Command | State |
| --- | --- |
| `devct doctor` | **built** — reports readiness and, crucially, *measures* what the API token can actually do |
| `devct-template build` | **built** — the six-step ladder, token-first; a step the API refuses stops the build and names the grant |
| `devct create` / `list` / `destroy` | specified, not built |
| `devct onboard` | specified, not built — write `devct.conf` by hand meanwhile (below) |

Unbuilt verbs say so when you call them. Nothing here stubs a success.

## `doctor` measures; it does not trust

The privileges a Proxmox token needs to run this workflow are written down in the spec, and
that list is a **hypothesis**. `doctor` asks the API's own permissions endpoint what the token
holds and prints the delta, because a recorded access scope is an interface fact and interface
facts decay — this project has already been bitten by one that went stale inside a day.

Each missing privilege comes back as a named grant, not a mystery 403 halfway through a
provision.

## Configuration

`~/.config/quince/devct.conf`, mode 600, plain `key=value`. It is read as data — never sourced,
so it cannot execute anything:

```
api_host         = pve.example.invalid
api_addr         = 203.0.113.10
api_port         = 8006
node             = node-name
pool             = quince-dev
storage          = local-zfs
template_storage = local
bridge           = vmbr0
sdn_zone         = localnetwork
template_name    = quince-dev-template
token_id         = quince-dev@pve!devct
ssh_key          = ~/.ssh/id_ed25519.pub
registry         = registry.example.invalid/quince
ca_pin           = ~/.config/quince/devct-api.pem
```

`storage` and `template_storage` are **two different storages and both are required** — a zfspool
holds container rootfs (`rootdir`/`images`) and cannot hold appliance images (`vztmpl`) at all, so
the grant for downloading a template belongs on the storage that can actually hold one. devct
refuses to guess one from the other: defaulting `template_storage` to `storage` reported a
privilege as satisfied on a storage that could never hold the thing it authorises.

The token **secret** is not in this file. It lives at `~/.config/quince/proxmox-devct.token`
(mode 600) and is read at point of use.

## The one root block, and why it exists

Root touches the box **exactly once, at template build, and never again**. That is a deliberate
invariant: everything the API cannot do is collected into one announced block rather than sprinkled
wherever a root command would be convenient.

Two things need it, both measured rather than assumed:

- **`keyctl`** — PVE allows a non-root user to set the `nesting` flag and nothing else (*"changing
  feature flags (except nesting) is only allowed for root@pam"*), so no ACL grant can ever cover it.
- **the first shell** — provisioning runs over ssh, and the Alpine appliance ships no sshd. ssh is
  the one tool that cannot bootstrap itself, so `pct exec` installs and starts it.

```
pct set  <vmid> -features nesting=1,keyctl=1
pct exec <vmid> -- apk add --no-cache openssh
pct exec <vmid> -- rc-update add sshd default
pct exec <vmid> -- service sshd start
```

Each command is printed before it runs, and the block refuses to touch a vmid that is not verified
in the pool. Set `root_ssh` to let the build run them; leave it unset and the build stops with the
commands and a `--vmid` to resume with. Clones inherit their template's features, so this is once
per template rebuild (`versions.env` cadence) and **`devct create` never needs root at all**.

`--skip-keyctl` omits the flag — an open measurement, since if the toolchain turns out not to need
`keyctl`, half the block goes away.

**A verified no-root alternative exists and is not used:** `POST /nodes/<node>/lxc/<vmid>/termproxy`
answers 200 on the scoped token (VM.Console rides along with the pool's container privileges), so a
console channel could bootstrap the box without root at all. It costs driving a websocket console
from shell, which is why the ruling chose `pct exec` — recorded here so the option does not
evaporate if the root class is ever narrowed.

## Resuming a failed build

A ladder that only runs on a clean box turns any mid-run failure into an orphaned container and a
manual cleanup. So `--vmid N` on an existing in-pool container **resumes from the first unfinished
step**: completed work is detected and announced rather than redone, and the root block carries only
what that box still needs — if the flag is set and ssh already answers, root is not entered at all.
An id that is already a template reports as built and stops. `--recreate` destroys the container
first (token-only, pool-guarded) and takes the clean path.

Expect two harmless noises during provisioning: cgroup2 mount and `RC_ULIMIT` warnings when
containerd starts in an unprivileged container. The services come up regardless; they are not a
failure and nothing needs chasing.

## Container ids

`devct-template` takes the cluster's next free id, or `--vmid N`. There is no reserved range: pool
membership is the guard that matters, since the token can only touch what is in its pool and
`devct destroy` refuses anything outside it. A reserved range would be an optional config key if a
site wants tidier numbering — it is not implemented, and nothing depends on it.

## Two rules the code enforces on itself

**No secret reaches argv.** curl is driven with `-K -`, reading its options — including the
`Authorization` header — from stdin, so nothing sensitive is visible in `ps`. Proxmox's own API
documentation makes the same point about auth headers on command lines. This mirrors the
project's existing stdin-only handling of backup passwords, and the git credential helper that
keeps the bot token out of the environment.

**TLS is pinned, never disabled.** The API presents a self-signed certificate; the pin is
captured once and every call validates against it with `--cacert`. Disabling verification is
banned in this tree and `make gates-sh` fails the build if the flag appears.

`api_host` must therefore be **a name the certificate actually carries** — curl verifies the
URL's host against the certificate, and PVE's self-signed certificate names the node, not its
address. Putting an address there fails with *"no alternative certificate subject name matches
target ipv4 address"*. Where that name doesn't resolve for you, set `api_addr` and devct binds
it with curl's `--resolve` instead of asking DNS — the connection goes to the address you
named, and verification stays on. The alternative (turning verification off) is the thing this
whole arrangement exists to avoid.

## Running the gate

```sh
make gates-sh
```

Runs shellcheck (POSIX `sh` dialect — dev containers use busybox) in a pinned container, then
the ban check. It is part of `make gates`, so CI runs it with no workflow change.
