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
- that's it — no Go, Node or pnpm on your host.

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
| present | `jq`, `awk`, `sed`, `grep` — **which is `ugrep`, see below** — `git`, `gh`, `openssl`, `make`, a container runtime |

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

**`grep` IS `ugrep`, WHICH IS NEITHER ENVIRONMENT THIS PROJECT'S GATES RUN IN.** The row above lists
`grep` as present; it does not say which one, and it is not the BusyBox this section otherwise warns
about. `grep --version` on a session box reads `ugrep 7.5.0 x86_64-pc-linux-musl`. That makes this
box a **worse** oracle than either target for the one class of check this project runs constantly —
a pattern proved portable here has been proved against a third implementation with its own
extensions.

The divergence that has already reached committed prose is a **mid-pattern `^`**: ugrep treats it as
a zero-width assertion that succeeds anywhere, where BusyBox and GNU both anchor it. So an
alternation like `(^|[^A-Za-z0-9_-])` silently matches the empty string here and does not there.
Measured three ways, `printf 'abc\n' | grep -cE 'a(^|x)bc'`, with `^abc` as the control proving a
leading anchor behaves identically in all three:

```
ugrep 7.5.0 (this box)           1     control 1
BusyBox 1.37.0 (alpine:3.24)     0     control 1
GNU grep 3.11 (debian:trixie)    0     control 1
```

**POSITION DECIDES IT, AND THAT IS WHAT MAKES THE CLASS SWEEPABLE.** The trap needs the `^` to be
genuinely mid-pattern. In LEADING position — `grep -oE '(^|[^A-Za-z0-9_/-])#[0-9]+'` — all three
implementations anchor it and agree, because there is nothing before it for a zero-width assertion
to succeed against. Measured on `quince#123`, which every one of the three correctly declines to
match. So `bin/pr-title-refs`, `bin/suite-coverage` and the `curl -k` ban in the Makefile all carry
`(^|…)` and are all fine.

**Grep for `(^|` and then read where it sits**; only the ones with pattern before them can bite.
The sweep found two in the tree: `bin/closing-refs-check`, which was already correct and whose
comment records the table, and `bin/forge-watch-exits-test`, which was not — under ugrep a doc
naming `exit 16` satisfied the check for exit `6`, so an assertion about documentation coverage was
weaker when run by hand on a box than in the gate that runs it. Fixed in quince#1338's item 3.

**The failure direction is the expensive one.** A guard written with the alternation goes quiet when
you test it by hand, which reads as "the guard is too broad, I have fixed it" — and it is the guard
that has stopped working, in the environment where you can see it and not in the two where it runs.
`bin/closing-refs-check` carries one live instance and its comment records the same table.

**So prove a pattern where it will run**, which for a shell gate means `gates-sh` (BusyBox, in
`alpine:3.24`) or CI (GNU). A one-off container is enough:
`nerdctl run --rm alpine:3.24 sh -c '…'`.

**Python is absent from the box AND from the image, and nothing in quince is written in it.** The
reason not to install it on a session box is **host surface**, not scarcity: a session box is a
driver and a container host, the toolchain-container design exists precisely so language runtimes
live in containers rather than on hosts, and nothing a *session* does needs Python — `jq` covers the
JSON use, and the cost of absence is a loud, self-correcting `127`. Do not install it to make an
error go away; write the `jq` form.

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
make gates-ui     # tsc + eslint + vitest + vite build                  (ui/)
make image        # build the production container (proves go:embed of the built UI)
make push REGISTRY=host[:port]/repo   # push (registry + creds via env only)
```

First run builds the toolchain images (a few minutes); afterwards, named cache volumes
(`quince-go-build`, `quince-go-mod`, `quince-pnpm-store`) keep runs
fast. `make clean` drops those volumes and the local images.

### Targeting one Go test

`gates-go` takes `GO_TEST_ARGS`, handed to `go test` verbatim. Everything else about the gate —
`gofmt`, `go vet`, `golangci-lint` — still runs, so this narrows the test leg only:

```sh
make gates-go GO_TEST_ARGS="-count=1 -run TestSomething ./internal/deviceops/..."
```

`-count=1` is what busts the test cache; without it a re-run prints `(cached)` and cannot re-provoke
an intermittent failure.

**Your shell quoting is honoured, so `-run` alternation works** — `-run 'TestA|TestB'` is the
documented Go idiom for "these tests" and is the ordinary reason to reach for this at all:

```sh
make gates-go GO_TEST_ARGS="-run 'TestSeeding|TestPasscode' -count=150 -cpu=1 ./internal/backup/"
```

**A literal `$` still needs doubling** — `-run 'TestExact$$'` for an anchored regex — because `$` is
make's own escape and is consumed before this recipe ever sees the value.

**Both of those were broken until quince#1134, and the shape of the failure is why they are written
down rather than left obvious.** The value was pasted *inside* the recipe's quoted shell script, so a
caller's own quotes closed it early and the rest was parsed as shell source. It surfaced two ways:
`Error 127` naming a *test* as a command not found, and — hit independently, same day — **no output
at all with `make` exiting 0**. The second is the one to know about, because a filter like
`grep -c FAIL` renders it as "0 failures", and a suite that never ran reaches PR evidence as green.
`bin/go-test-args-test` is the gate; it drives the real recipe against a stub runtime and asserts the
argv `go test` would have received.

**A targeted run announces itself and is not a gate.** It prints a `PARTIAL RUN` banner naming what
it actually ran, because the expensive failure here is not the wasted time — it is writing *"I ran
just this test"* into PR evidence when something else happened (quince#368).

**Only `gates-go` honours it; every other target REFUSES rather than ignoring it.** Both
`make gates GO_TEST_ARGS=…` **and** `GO_TEST_ARGS=… make gates` are parse-time errors: the ladder's
Go leg would run a filtered suite while the ladder reported itself green, which is a stronger claim
than anything that ran. Same reasoning as `privacy-check` refusing rather than exiting 0 having swept
nothing (quince#41).

**Both invocation forms, deliberately** — `make target VAR=x` and `VAR=x make target` are different
to `make` (`$(origin)` reports `command line` versus `environment`) and the first version of this
guard caught only the first, which let the ladder run filtered with no banner and no refusal. **The
trade:** an exported `GO_TEST_ARGS` in a shell profile or CI environment makes `make gates` an error
rather than a silently filtered ladder. That is the intended direction, not an oversight.

**`make` accepts any variable you invent**, declared or not — which is what kept this silent. A
misspelling (`GO_TESTARGS=…`) is still accepted and still does nothing, and no guard here can catch
that; the refusal above only covers the correctly-spelled name on a target that cannot honour it.

### Reproducing a flake that only fails under the whole-tree run

**Reach for `-cpu=1` before paying for a full ladder per sample.** A Go test in this repository that
passes on its own and fails only when the whole tree runs is usually **a test racing a goroutine it
never waited on** — a bus subscriber, a supervise path, an engine still tearing down:

```sh
make gates-go GO_TEST_ARGS="-run TestX -count=150 -cpu=1 -race ./internal/pkg/"
```

**Measured on `internal/backup` (quince#1135):** `TestStoryJobsReadAndEvents` at `-count=150 -cpu=1`
produced **54 failures in 150, in about 22 seconds**. The same flake class had previously absorbed
**63 targeted iterations with zero failures** (quince#644), whose conclusion was that *"targeting the
test is the wrong way to chase this — what discriminated was running the whole tree at once"*, at a
full ladder per sample.

**THAT EXACT COMMAND NOW PASSES, AND THE REASON IS THE POINT.** Re-run on 2026-08-18 against `main`:
**0 failures in 150, 23.4s, exit 0.** The defect it reproduced — quince#1115, an assertion racing the
collector goroutine — was fixed by `37cdf0a`, *"an assertion about the collector waits for the
collector"*. So the number above is a record of what the lever found, not something you can reproduce
today, and a reader who runs it expecting red would draw the wrong conclusion about the flag.
**A clean `-cpu=1 -count=150` run is what a repaired happens-before looks like**, which makes the
command useful in both directions: as a reproducer while a flake is live, and as the check that a fix
actually closed it.

**It is the mechanism rather than a trick**, which is why it generalises within the class. `-cpu=1`
sets `GOMAXPROCS`, so a goroutine runs only when the main goroutine yields and the assertion almost
always wins a race it should not be in. **A contended CI runner is a weaker version of the same
thing** — which is exactly why this class presents as whole-tree-only: locally, the whole-tree run is
the only configuration that supplies the contention.

**Two bounds, so it is not oversold:**

- **It reproduces one class, and is not a general flake finder.** Missing happens-before, yes. A
  fixture race, a filesystem-timing seam (quince#786's mtime) or anything in the browser, no.
- **A `-cpu=1` failure is not automatically a defect.** It proves an ordering is unguaranteed, which
  is the thing worth fixing — but a test can also be legitimately single-processor-hostile, and that
  is a judgement rather than a verdict the flag hands you.

## Repo layout

| Path | What |
| --- | --- |
| `core/` | Go daemon (`quince`) — device tracking, jobs, storage, HTTP/WS API, UI host |
| `ui/` | React + Vite + TS web app, embedded into the Go binary at build time |
| `deploy/` | `Dockerfile`, compose examples, this guide, and `devct/` (dev-container provisioning) |
| `docs/` | canon: stack decisions, architecture, frozen contracts, rung specs |
| `versions.env` | the single source of truth for toolchain + image pins |

## Running it

```sh
make image
docker run --rm -p 8968:8968 quince:local     # or: nerdctl run ...
# → http://localhost:8968  (the UI shell; GET /api/health returns {"status":"ok",...})
```

For UI work with hot reload you can run Vite's dev server *inside* the node toolchain
container against a running `quince serve` (it proxies `/api` to `:8968`); most day-to-day
work just uses `make gates-ui`.

## Adding a dependency / bumping a toolchain

Bump the pin in `versions.env` (only there), rebuild the toolchain image
(`make toolchains`), and re-run the gates. The Dockerfile and CI pick up the same value
via build-args. Language deps live in each track's manifest (`core/go.mod`,
`ui/package.json`) with a committed lockfile.
