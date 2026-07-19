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
| `deploy/` | `Dockerfile`, compose examples, this guide |
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
