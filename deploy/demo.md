# The public demo — how it is built and shipped

`quince serve --public-demo` on fly.io, at `quince-demo`. This file plus `fly.toml` at the
repository root are the whole deployment; quince#494 is the reasoning, and this is meant to stay
readable after that issue is closed and forgotten.

**This is not how anyone runs quince for real.** That is `compose.yml` and
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
| proxy trust | `QUINCE_TRUSTED_PROXIES = "172.16.0.0/16"` — **required**, see below |

## The proxy trust list is not optional

**Without it the demo locks itself out**, and it did — quince#464, live on the deployed instance
until 2026-08-04. `ClientIP` returns the peer unless a trust list is configured, and on fly the peer
is fly-proxy, identical for every visitor. So the login limiter bucketed every visitor together:
**ten wrong passwords from any one visitor denied login to all of them** — on an instance that
prints its own password on the login screen.

Measured before and after, against the image, ten wrong attempts from one forwarded client then a
correct password from another:

```
no trust list   victim, CORRECT password, different X-Forwarded-For  ->  429
trust list set  victim, CORRECT password, different X-Forwarded-For  ->  200
trust list set  attacker, correct password, SAME X-Forwarded-For     ->  429   (still limited)
```

The third line matters as much as the second: the limiter still works, it just bills the right
party. `fly.toml` carries why the value is a `/16` rather than a `/12` or the observed `/32`.

**The `Secure` cookie working is not evidence this is configured**, which is what made it easy to
miss. `X-Forwarded-Proto` believes anyone when the list is unset; `X-Forwarded-For` believes nobody.
Opposite defaults, each preserving its own history.

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

### The workflow — INSTALLED

**No agent seat can create a file under `.github/workflows/`.** Measured for the implementer PAT
(quince#113), the architect (same 403), and again on 2026-08-03 for the coder App, which gets
`403 Resource not accessible by integration` on that path while an ordinary contents write to the
same branch in the same call succeeds — so the refusal is path-scoped, not a broken credential.

**The Operator installed it** as `.github/workflows/demo-deploy.yml` (quince#618), pushed over SSH,
which consults no OAuth scope. **Do not copy the block below into a new file** — it is already
there, and this is the reference copy.

It lives here rather than in a PR comment so there is one copy rather than two that drift. That
arrangement earned itself immediately: quince#618 first installed a `cron:` this document had
already tried and moved away from, and one `diff` against this block found it. **Two copies still
means two copies** — an edit to either belongs in the same change as an edit to the other.

**`make demo-block-check` enforces that, in `gates-sh`** (quince#622). It compares the block below
against the installed workflow and refuses when they differ, naming both paths and picking no
winner — quince#618 is why it must not pick: the document was the correct side and the running file
was the drifted one. Until it existed, the rule above was carried by whoever remembered to read.

**The gate requires this file to hold EXACTLY ONE ` ```yaml ` fence, and that one is the mirror.**
Adding a second yaml example anywhere in this document makes the check exit `2` — DID NOT RUN —
rather than silently comparing the wrong block. If a second one is ever genuinely wanted, teach
`bin/demo-block-check` which fence is the mirror in the same change.

```yaml
name: demo-deploy

# NEVER `pull_request`. GitHub withholds secrets from FORK pull requests but hands them to
# same-repo ones, so a deploy triggered by `pull_request` gives the token to any branch anybody
# pushes. There are no outside contributors here, which makes this a habit rather than a live
# exposure — and it is the standard way this goes wrong.
# SCHEDULED + MANUAL, deliberately not on push (Operator, 2026-08-03). A demo does not need to
# track every merge, and a deploy on every push to `main` spends a full remote image build each
# time to move a fixture-data instance nobody is looking at.
#
# NO BRANCH FAN-OUT, and this is the reflex worth spending a line on because other CI systems
# get it wrong: some run a schedule once per branch that carries the file. GitHub does not.
# "Scheduled workflows will only run on the default branch" — one run per tick, from `main`'s
# copy. The same file on twenty branches registers no cron entries at all. Verified against
# GitHub's docs rather than remembered.
#
# :23 PAST THE HOUR, NOT :00. GitHub's own guidance: "The `schedule` event can be delayed during
# periods of high loads... High load times include the start of every hour. If the load is
# sufficiently high enough, SOME QUEUED JOBS MAY BE DROPPED." Dropped, not merely late. This read
# `0 22 * * *` first, which put a nightly deploy squarely in the window GitHub names.
#
# Two more, neither fatal for a demo but both surprising later:
#   - `workflow_dispatch` needs this file ON THE DEFAULT BRANCH before the Run workflow button
#     exists. Once it is there, a run can be aimed at any branch or tag.
#   - In a PUBLIC repository, "scheduled workflows are automatically disabled when no repository
#     activity has occurred in 60 days". quince is public. A quiet stretch stops the nightly, and
#     nothing announces that it stopped.
on:
  schedule:
    - cron: "23 22 * * *"
  workflow_dispatch:

# Least privilege: this job reads the tree and talks to fly. It needs nothing from GitHub.
permissions:
  contents: read

# One deploy at a time. Without this, a manual trigger and the nightly run can overlap and the
# LATER image can lose to the earlier one.
concurrency:
  group: demo-deploy
  cancel-in-progress: false

jobs:
  deploy:
    runs-on: ubuntu-latest
    steps:
      # `fetch-depth: 0` IS REQUIRED, and `fetch-tags` is NOT a lighter substitute for it.
      # deploy/version derives this build's version with `git describe --tags`, and the default
      # depth-1 checkout has no tags at all. Measured 2026-08-20 against a real `--depth 1` clone
      # of this repository: with the tags then fetched explicitly, `git describe --tags --always`
      # STILL returned a bare sha — the tagged commits are outside the shallow history, so nothing
      # can compute a distance to them. Only full history relates HEAD to a tag.
      # ci.yml already passes fetch-depth: 0 on every job for its own reason; this is the workflow
      # that builds the one artifact a stranger looks at, and it did not (quince#615).
      - uses: actions/checkout@v7
        with:
          fetch-depth: 0
      - uses: superfly/flyctl-actions/setup-flyctl@master
      # NOT a bare `flyctl deploy`. deploy/fly-deploy supplies the build-args the Dockerfile
      # needs from versions.env — without them four ARGs arrive empty and the build dies
      # somewhere that looks unrelated. The script says which four and why.
      - run: ./deploy/fly-deploy
        env:
          FLY_API_TOKEN: ${{ secrets.FLY_API_TOKEN }}
```

Four things about that block, stated rather than hidden:

- **A failed nightly may notify nobody who is watching.** GitHub sends scheduled-workflow
  notifications to *"the user who last modified the cron syntax in the workflow file"* — which, if
  an agent seat authored it, is an agent identity rather than a person. This project has already
  lost one agent account to suspension, so that is not a hypothetical mailbox. **If the nightly
  deploy is meant to be noticed when it breaks, the Operator should be the last committer of the
  `cron:` line.** Nothing here can arrange that: no agent seat can write under
  `.github/workflows/`, so whoever installs the file is the last modifier by construction — which
  happens to make the right thing the default, as long as it stays that way.
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
  multi-stage build (Go + Node + Rust). It is the right call *today* only because there is no
  published image to consume: `ci.yml`'s `image` job builds with **no push**. When M5's release
  pipeline publishes to ghcr, this should become `flyctl deploy --image ghcr.io/...` and the build
  disappears. Until then the demo builds a second time what CI already built once.

### By hand, without the workflow

```sh
./deploy/fly-deploy               # NOT `fly deploy` — see below
fly logs -a quince-demo
fly status -a quince-demo
```

**`fly deploy --remote-only` on its own does not work, and fails in a way that names the wrong
thing.** flyctl knows nothing about `versions.env`, so every ARG `deploy/Dockerfile` declares
without a default arrives empty. The first real deploy died here:

```
RUN git clone --depth 1 --branch ${NETMUXD_REF} https://github.com/jkcoxson/netmuxd /src/netmuxd
fatal: repository '/src/netmuxd' does not exist
```

With `NETMUXD_REF` empty, `--branch` swallows the URL and git reads the *destination* as the
repository — so an unset build-arg presents as *"that repository does not exist"*, pointing at the
one path on the line that is fine. Four ARGs were empty; the image-ref ones have Dockerfile
defaults and worked silently, which is why the build looked healthy until it did not.

`deploy/fly-deploy` supplies them from `versions.env`, **deriving the list from the Dockerfile's own
`ARG` lines** rather than repeating it — so a newly added ARG is either found or reported, never
quietly empty. It runs where flyctl is installed and authenticated (a CI runner, or the Operator's
machine) and refuses with an explanation anywhere else.

**`VERSION` comes from a SECOND source, and that split is deliberate.** `versions.env` holds
toolchain pins; a version is not one, so `build-args` correctly has nothing to look up for it and
announces the gap — `using the Dockerfile default for: VERSION`. For months that default is what
the demo shipped, reporting `version: "0.0.0-dev"` to every visitor while a hand-built image
reported a real version, because the Makefile had an override point the deploy did not
(quince#615). `deploy/version` is now that second source, shared by the Makefile and this script so
the two cannot drift.

**The deploy needs full history, not just tags.** `deploy/version` runs `git describe --tags`, and
`actions/checkout` defaults to a depth-1 clone that has none — so `demo-deploy.yml` passes
`fetch-depth: 0`. Fetching the tags alone is **not** a lighter substitute: measured against a real
`--depth 1` clone of this repository, `git describe --tags --always` still returned a bare sha with
the tags present, because the tagged commits lie outside the shallow history and nothing can
compute a distance to them. When the derivation cannot run it says which of the three reasons
applies and stamps `0.0.0-dev` rather than guessing.

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
