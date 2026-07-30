# Developing quince

quince builds and tests the same way everywhere — locally, in CI, and in the release
image — because **nothing is installed on the host but `make` and a container runtime.**
Every gate runs inside a pinned toolchain container that is literally a build stage of the
production `deploy/Dockerfile`, so your machine, CI, and the shipped binary all compile
with byte-identical toolchains. All versions are pinned in one place: `versions.env`.

## What you need

- `make`
- a container runtime with BuildKit: **nerdctl** (containerd) or **Docker**. The Makefile
  autodetects which one you have.
- that's it — no Go, Node, Python, pnpm, or uv on your host.

> Why: a daemon whose whole job is "never lie about state" should be reproducible to the
> byte. Host toolchain drift is exactly the class of "works on my machine" bug we refuse
> to ship. See the program doc, *Where work runs*.

**Don't have a box to run gates on?** [`devct/`](devct/README.md) provisions one on any Proxmox
host: it builds a template carrying a container runtime and a pre-warmed toolchain cache, then
clones a persistent box from it — `devct create --name <host> --role <implementer|arch>` —
arriving at a green gate ladder in about three minutes. It runs on a scoped API token; the only
root it needs is one announced block when the template itself is (re)built.

The numbered `quince-dev-N` series it can also create is **retired**: the Operator ruled on
2026-07-28 that there are no per-unit-of-work dev containers and the session host *is* the work
host, so gates run in a clone on that box rather than over ssh. The verbs still exist and the
directory is load-bearing — see [`devct`](devct/devct)'s own header for which half is which.

## What a session box actually is — Alpine, BusyBox, and no Python

Stated as a fact rather than left to be discovered, because every session that reaches for a tool
that is not there loses a cycle to an exit `127`. quince#246 records three such instances in one
afternoon, on two different seats — and once the *defensive* form (`python3 - <<'PY' … || { sed … }`)
failed differently and cost more than the original.

| | |
| --- | --- |
| distro | **Alpine** |
| shell | **BusyBox `ash`** — *not* bash |
| Python | **absent.** `command -v python3` and `command -v python` are both empty on all three boxes |
| present | `jq`, `awk`, `sed`, `grep`, `git`, `gh`, `openssl`, `make`, a container runtime |

**Prefer `jq` for JSON, and do not assume GNU flags.** BusyBox bites well beyond Python. Measured on
a box, Alpine 3.24.1 — each of these works in CI and fails here:

```
${PIPESTATUS[0]}        sh: syntax error: bad substitution   (BusyBox sh is not bash)
ls --time-style=…       rejected
find -newermt           rejected                             (BusyBox has -mmin)
```

**`base64 -w0` is NOT one of them, and quince#246 says it is.** Measured: `/bin/base64` is
`/bin/busybox`, `coreutils` is not installed, and BusyBox's `base64` accepts **and honours** `-w0`
— single-line output. Recorded here because the issue lists it as a GNU-only trap and copying that
list unchecked would have put a false claim into canon, which is the defect class this file exists
to avoid. (`bin/gh-coder` uses `openssl base64 -A` anyway, which is portable and fine — just not
*because* `-w0` is unavailable.)

**The lesson generalises past the one wrong row: verify the trap on the box before writing it down.**
Three of four held; the fourth would have taught the next reader to work around a problem that does
not exist.

**Python is deliberately absent, not missing by accident.** Nothing in the tree needs it and `jq`
covers the use, so the asymmetry settles it: the cost of absence is a loud, self-correcting `127`,
while the cost of presence is a host package that exists on the session boxes but **not** in the
pinned toolchain containers or the release image — which invites committed tooling that depends on
it and then fails where it matters. Do not install it to make an error go away.

**And do not "fix" a box towards GNU.** `deploy/Dockerfile`'s runtime stage is `FROM
${ALPINE_IMAGE}`, so **BusyBox is the production truth** and the boxes match the shipped image.
CI's `ubuntu-latest` is the outlier, not the reference. Making a box more GNU-like moves it away
from production, and would let a GNU-only construct pass both CI and local use while still being
wrong in the image.

> **This section is orientation, NOT a control**, and the distinction is the whole of quince#246's
> own correction to itself. A rule with nothing checking it is a habit, and `gates-sh` cannot close
> this one: `shellcheck -s sh` checks the shell *language*, not coreutils *flags*, so every line in
> the block above passes it cleanly. The control quince#246 asks for is running the shell suites in
> a BusyBox container in CI — where they currently run on whatever the host happens to be, which on
> `ubuntu-latest` is bash and GNU coreutils. **That is not done and is tracked there.**

## The gate ladder

```sh
make gates        # the whole ladder (below), each step in its toolchain container
make gates-go     # gofmt + go vet + golangci-lint + go test -race     (core/)
make gates-vault  # ruff + ruff format --check + mypy --strict + pytest (vault/)
make gates-ui     # tsc + eslint + vitest + vite build                  (ui/)
make image        # build the production container (proves go:embed of the built UI)
make push REGISTRY=host[:port]/repo   # push (registry + creds via env only)
```

First run builds the toolchain images (a few minutes); afterwards, named cache volumes
(`quince-go-build`, `quince-go-mod`, `quince-pnpm-store`, `quince-uv-cache`) keep runs
fast. `make clean` drops those volumes and the local images.

## Repo layout

| Path | What |
| --- | --- |
| `core/` | Go daemon (`quince`) — device tracking, jobs, storage, HTTP/WS API, UI host |
| `vault/` | Python sidecar (`quince-vault`) — session-scoped encrypted-backup reader |
| `ui/` | React + Vite + TS web app, embedded into the Go binary at build time |
| `deploy/` | `Dockerfile`, compose examples, this guide, and `devct/` (dev-container provisioning) |
| `docs/` | canon: stack decisions, architecture, frozen contracts, rung specs |
| `versions.env` | the single source of truth for toolchain + image pins |

## Running it

```sh
make image
docker run --rm -p 8080:8080 quince:local     # or: nerdctl run ...
# → http://localhost:8080  (the UI shell; GET /api/health returns {"status":"ok",...})
```

For UI work with hot reload you can run Vite's dev server *inside* the node toolchain
container against a running `quince serve` (it proxies `/api` to `:8080`); most day-to-day
work just uses `make gates-ui`.

## Adding a dependency / bumping a toolchain

Bump the pin in `versions.env` (only there), rebuild the toolchain image
(`make toolchains`), and re-run the gates. The Dockerfile and CI pick up the same value
via build-args. Language deps live in each track's manifest (`core/go.mod`,
`vault/pyproject.toml`, `ui/package.json`) with a committed lockfile.
