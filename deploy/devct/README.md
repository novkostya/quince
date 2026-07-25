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
| `devct-template build` | specified, not built |
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

The token **secret** is not in this file. It lives at `~/.config/quince/proxmox-devct.token`
(mode 600) and is read at point of use.

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
