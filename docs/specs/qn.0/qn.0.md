# qn.0 — the floor: scaffold, gates, CI, image

**Goal.** A cloneable repo where `make gates` and `make image` are green over a minimal
vertical slice of all three languages, CI runs the same ladder, and the image lands in the
Operator's LAN registry — so every later rung starts from a working loop, not from setup.

## Boundary

In scope: repo root layout (**the repo root IS `~/iphone-backup-app` itself** — docs and
CLAUDE.md are already in place), `core/` `vault/` `ui/` `deploy/` skeletons, `Makefile`,
`.github/workflows/`, lint configs, `.gitignore`, git init (first commit when the Operator
asks). `.gitignore` MUST exclude the local-only material: `/chatgpt-*.md` (the
Operator's private lab logs and review transcripts — they contain personal details like
device UDIDs and never go public; the durable extract is the transcript fixtures, story
below), `/quince-planning-pack.md` (a generated sharing bundle), and **`/local/`** (the
Operator's private environment layer — hosts, addresses, provisioning). Out of scope: any
real feature logic — the slice is hello-world thin by design; no public registry pushes;
no release workflow (that's qn.6).

## Design

- Layout per stack.md D11: `core/` Go module (`github.com/novkostya/quince/core`), `vault/` uv-managed Python package
  (`quince_vault`), `ui/` pnpm + Vite + React + TS app, `deploy/` Dockerfile + compose
  examples, `docs/` (exists).
- Single multi-stage Dockerfile: ui build → go build (embedding `ui/dist` via
  `go:embed`) → alpine runtime with usbmuxd, libimobiledevice-progs, **netmuxd (pinned,
  built from source in a CI stage — Wi-Fi is primary, stack D13)**, python3 + venv from
  `uv.lock`. Entrypoint runs `quince serve`.
- Makefile is the one entrypoint; CI calls only make targets (no logic in YAML).
- **Gates are containerized** (program doc "Where work runs"): every `gates-*` target
  runs inside a pinned toolchain container via a `nerdctl`/`docker` autodetect wrapper,
  using the same base images as the production Dockerfile stages; named cache volumes
  for Go build cache / pnpm store / uv cache; Playwright (from qn.1) runs in the
  official Playwright image. No toolchain ever installs on a host. CI runs the same
  containerized `make gates` — identical environments everywhere.
- Toolchain versions pinned in one place: `versions.env` pins **container image
  references** (Go, Node/pnpm, Python/uv, golangci-lint, Playwright) consumed by both
  the Makefile wrapper and the Dockerfile.

## Stories

0. **Dev environment first** (program doc "Where work runs"): provision `quince-dev`
   per the specifics in `local/environment.md` (Operator-local, gitignored — read it,
   never quote it into committed files); record the exact provisioning commands back
   into that same file so the box is disposable/rebuildable, and write the *generic*
   contributor dev-env guide (any Linux box: toolchains per `versions.env`, nerdctl,
   git) as public `deploy/dev.md`. All subsequent stories execute inside the dev
   container (the workstation gets nothing installed — hard rule).
1. `core/`: `quince` binary with `serve` (serves embedded UI + `GET /api/health` →
   `{status:"ok", version}`) and `version` subcommands; one real unit test; slog wired.
2. `vault/`: `quince-vault --version` and a `selftest` subcommand importing
   `iphone_backup_decrypt` (proves the dependency installs on musl/alpine); one pytest.
3. `ui/`: Vite app rendering the sidebar shell (name, version from `/api/health`, nav
   stubs per ui.design.md); one vitest; `pnpm build` output embedded by the Go build.
4. `make gates` runs the full ladder green locally; `make image` builds the container;
   `make push REGISTRY=...` pushes (LAN registry creds via env only).
5. CI (`.github/workflows/ci.yml`): the same `make gates` on push/PR, Linux, with Go/
   Node/Python setup pinned to the same versions; image build as a CI job (no push).
6. `deploy/compose.lab.yml` (PVE LXC shape: /backups bind, /var/run/usbmuxd or in-container
   usbmuxd + USB device) and `deploy/compose.nas.yml` (generic NAS shape) — both start the
   container; correctness of device access is proven later (qn.2 manual gate).

## Gates

- `make gates && make image` green from a fresh clone **inside `quince-dev`** (CI
  proves the fresh-clone property independently).
- `docker run … quince version` prints version; `curl :8080/api/health` returns ok;
  browser shows the shell.
- `docker run … quince-vault selftest` exits 0 (decryption dep functional on alpine).

## Fixtures

None yet — but create `core/internal/backup/testdata/transcripts/README.md` documenting
the extraction task from the lab log (consumed by qn.4).

## Rung-ruled decisions (qn.0, *rung-ruled* — canon within this rung's boundary)

Settled during the build; a later rung changes them only via the gap protocol.

- **Containerized-gate Makefile.** Realizing the "dev host is a container host, no
  toolchains" ruling: the Makefile autodetects `nerdctl`/`docker` and runs every gate
  inside pinned toolchain images that are literally build stages of `deploy/Dockerfile`
  (`toolchain-go` / `-node` / `-uv`). golangci-lint is `go install`ed into `toolchain-go`
  (built with the same Go — no analyzer skew). Named cache volumes
  (`quince-go-build/-mod`, `quince-pnpm-store`, `quince-uv-cache`) keep runs fast.
  `versions.env` pins **image refs** (not just versions) so the Dockerfile `--build-arg`
  defaults and the gate images are one source of truth.
- **Toolchain pins validated in `quince-dev`** (bootstrapping facts, all in `versions.env`):
  uv image tag suffix is `-alpine` (not `-alpine3.21`); Tailwind pinned **4.1.18** (the
  first stable `4.0.0` crashes in `@tailwindcss/vite` build); Rust pinned **1.88** (netmuxd
  `v0.4.3` needs Cargo edition 2024 ≥ 1.85); netmuxd ref bumped `v0.1.4 → v0.4.3`. Two
  in-repo fixes: a pnpm `overrides.vite` (collapses a dual-`vite` type skew between
  `vite@6` and the copy `vitest` pulls) and an mypy override ignoring the stub-less
  `iphone_backup_decrypt` under `--strict`. Lockfiles (`go` needs none — stdlib only;
  `ui/pnpm-lock.yaml`; `vault/uv.lock`) are committed; gates run `--frozen`.
- **Vault venv is built at its final path against the runtime's own system python.** venvs
  aren't relocatable and hardcode their interpreter, so `vault-build` is an Alpine stage
  (same base as `runtime`, identical `/usr/bin/python3`) that `uv sync`s into
  `/opt/quince/vault/.venv` — no path move, no interpreter skew in the shipped image.
- **Alpine Apple-protocol packages** are `libimobiledevice` + `libimobiledevice-progs`
  (the CLIs) + `libusbmuxd` (client lib). The missing `usbmuxd` *daemon* is NOT a
  rung-local call — it's escalated as an architectural gap (stack D2 `PROPOSED`, open
  question 2); qn.0 ships only the unambiguous pieces and builds on neither option.
