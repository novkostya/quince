# The public demo — how it is built and shipped

`quince serve --public-demo` on fly.io, at `quince-demo`. This file plus `fly.toml` at the
repository root are the whole deployment; quince#494 is the reasoning, and this is meant to stay
readable after that issue is closed and forgotten.

**This is not how anyone runs quince for real.** That is `compose.nas.yml` / `compose.lab.yml` and
`dev.md`. The demo serves fixture data, keeps nothing, and is deliberately disposable.

## What it is

| | |
| --- | --- |
| app | `quince-demo` (fly.io) |
| region | `fra` |
| size | `shared-cpu-1x`, 256 MB — the floor, chosen deliberately |
| mode | `--public-demo`: password preset, `Secure` cookies **not** forced off |
| password | `demo`, published on the login screen (rung ruling; `test` remains the *fixture* password and is unrelated) |
| state | none. No volume, no persistence — the rootfs is the state and it is thrown away |
| address | `quince-demo.fly.dev` today. A custom domain is a later, independent step — see below |

## Why there is no reset timer

**The platform is the reset.** `removeDemoState` runs at process **start** as well as on exit
(`core/cmd/quince/main.go`), so a fresh process is a fresh demo regardless of how the previous one
ended — including a `SIGKILL`, which a container stop is entitled to be. `TestPublicDemoRestartResetsEverything` gates exactly that, and runs the killed case on purpose.

So quince runs **no timer** and branches on **no interval** (Operator ruling, quince#470; design
D4). Something outside restarts it, and under scale-to-zero that something is fly putting the
machine to sleep when nobody is looking and cold-booting it on the next request.

**This is why `auto_stop_machines` must be `"stop"` and never `"suspend"`.** Suspend restores a
memory snapshot without restarting the process, so the wipe never runs and a vandalised demo stays
vandalised — silently, because nothing inside quince can observe the difference. The reasoning is
in `fly.toml` beside the setting, where somebody changing it will actually read it.

**`QUINCE_DEMO_RESET_MINUTES` is deliberately unset.** Under this shape there is no interval to
advertise, and fly's own scheduling primitive is hourly at its finest. Unset already produces the
honest UI: the login screen says the demo resets and states no schedule.

## Deploying it

### The credential

A **deploy token scoped to this app**, not an account token:

```sh
fly tokens create deploy -a quince-demo -x <expiry>
gh secret set FLY_API_TOKEN --repo novkostya/quince      # reads the value from STDIN
```

Two things that are cheap now and awkward later:

- **`-a quince-demo`, never `fly auth token`.** An account-wide token in a repository secret is a
  far larger blast radius than a demo warrants. Note fly's own caveat: some org-wide operations
  are reachable from a deploy token, so it is *narrow*, not *inert*.
- **Set `-x` deliberately.** fly's default expiry is 20 years and their CI guide demonstrates
  `-x 999999h`, which their own security page contradicts. Whatever is chosen is a rotation date,
  and an expired deploy token fails the workflow rather than the demo — the demo keeps running the
  last image, so the failure is quiet unless somebody reads the run.

### The workflow — BUILT, NOT WIRED

**No agent seat can create a file under `.github/workflows/`.** Measured for the implementer PAT
(quince#113), the architect (same 403), and again on 2026-08-03 for the coder App, which gets
`403 Resource not accessible by integration` on that path while an ordinary contents write to the
same branch in the same call succeeds — so the refusal is path-scoped, not a broken credential.

**The Operator installs it**, by copying the block below to `.github/workflows/demo-deploy.yml`
and pushing over SSH, which consults no OAuth scope. It lives here rather than in a PR comment so
there is one copy rather than two that drift.

```yaml
name: demo-deploy

# NEVER `pull_request`. GitHub withholds secrets from FORK pull requests but hands them to
# same-repo ones, so a deploy triggered by `pull_request` gives the token to any branch anybody
# pushes. There are no outside contributors here, which makes this a habit rather than a live
# exposure — and it is the standard way this goes wrong.
on:
  push:
    branches: [main]
  workflow_dispatch:

# Least privilege: this job reads the tree and talks to fly. It needs nothing from GitHub.
permissions:
  contents: read

# One deploy at a time. Without this, two merges in quick succession race and the LATER image can
# lose to the earlier one.
concurrency:
  group: demo-deploy
  cancel-in-progress: false

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v7
      - uses: superfly/flyctl-actions/setup-flyctl@master
      - run: flyctl deploy --remote-only
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}
```

Three things about that block, stated rather than hidden:

- **`actions/checkout@v7`, and this said `@v4` until the first run said otherwise.** v4 runs on
  Node 20, which GitHub deprecated, and the runner warned about it. The wrong number came from
  copying fly's own documentation example rather than reading the action's releases — which is the
  exact failure mode *"interface facts and version pins are looked up live, never remembered"*
  exists to prevent, reached by trusting somebody else's stale memory instead of my own. **Check
  this against the live releases when installing rather than trusting this line**, since it is a
  version number in a document and will go stale the same way.
- **`@master` is not a pin**, and this project's canon says to pin. fly publishes no maintained
  release tag for the `setup-flyctl` subdirectory action, and the tags that exist predate the
  repository's split, so pinning to one is not obviously safer than tracking `master`. The
  hardened form is to pin the action to a commit SHA **and** set the action's `version:` input to
  pin flyctl itself. Worth doing; it needs a SHA read at install time, so it is left to whoever
  installs the file rather than guessed here.
- **`--remote-only` builds on fly's builder**, from `deploy/Dockerfile`, which is a heavy
  multi-stage build (Go + Node + Rust + uv). It is the right call *today* only because there is no
  published image to consume: `ci.yml`'s `image` job builds with **no push**. When M5's release
  pipeline publishes to ghcr, this should become `flyctl deploy --image ghcr.io/...` and the build
  disappears. Until then the demo builds a second time what CI already built once.

### By hand, without the workflow

```sh
fly deploy --remote-only          # from the repository root, where fly.toml lives
fly logs -a quince-demo
fly status -a quince-demo
```

## A custom domain, when it is wanted

Not required, and **not a blocker for anything**: `quince-demo.fly.dev` gets an automatic Let's
Encrypt certificate, and fly terminates TLS in front. That means `X-Forwarded-Proto: https`
reaches quince, `secureCookie` returns true, and the demo issues **real `Secure` cookies over real
HTTPS with no code and no flag** — which was quince#444's sharpest finding, answered by the
deployment shape rather than by a change.

It also means the demo runs the same arrangement `qn.6f` recommends to users, so it demonstrates
the recommended deployment rather than a special case.

When a domain is attached:

```sh
fly certs add <domain> -a quince-demo
fly certs check <domain> -a quince-demo
```

DNS wants **A + AAAA** records pointing at the app's IPs (`fly ips list -a quince-demo`). fly warns
off a **CNAME at the apex** unless the registrar supports CNAME flattening, so an apex domain needs
the A/AAAA form. Certificates are issued automatically and cost nothing.

**The domain itself is not named in this file.** It is the Operator's and is recorded in the
private layer; a public repository is not where an address gets announced before its owner
announces it.

## Before it is reachable from the internet

`docs/specs/public-demo/public-demo.md` carries the dependency table, and it is the gate rather
than a checklist: **the instance must not be reachable until those land.** As of this file,
quince#463, quince#464 and quince#466 are on `main`; quince#465's single-flight half is in review.

Deploying is not the same as announcing. Bringing the app up on `*.fly.dev` to prove the pipeline
is fine — nobody knows the address. Publishing it is the step the table gates.

## What is NOT proven here

- **Nothing in this file has been measured against a deployed instance.** Every fly fact came from
  fly's live documentation while writing it; the first deploy is what turns this into a record.
- **The workflow above has never run.** It cannot be installed by the seat that wrote it.
- **Cold-start behaviour under scale-to-zero is unmeasured.** The demo reaches "quince serving" in
  ~64 ms once the process starts, but how long fly's proxy holds a request while it boots a stopped
  machine is not documented by fly and has not been observed here.
- **The 256 MB floor is a choice, not a measurement.** Nobody has watched this image's RSS under
  load on a 256 MB machine.
