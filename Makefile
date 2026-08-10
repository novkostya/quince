# quince — the one entrypoint. CI calls only these targets (no logic in YAML).
#
# The dev host is a PURE CONTAINER HOST: no Go/Node/Python toolchains are installed on
# it. Every gate runs inside a pinned toolchain container built from the production
# Dockerfile's own build stages, so dev / CI / release compile with identical toolchains.
# All version + image pins live in versions.env (the single source of truth).
#
# Requirements on the box: `make` + a container runtime (nerdctl or docker) with buildkit.
# Program canon: `program/quince.program.md` in novkostya/quince-devlog — "Where work runs" +
# "Gate ladder". The repo is named because this citation read `docs/program/quince.program.md` for
# long enough that a session went looking for it here; there is no `docs/program/` in this
# repository and there never was. A cross-repo citation written as a local path is the same defect
# the whole devlog#45 move inventory is about, sitting in the header of the file that opens the repo.

include versions.env

ROOT        := $(abspath .)
RUNTIME     ?= $(shell command -v nerdctl 2>/dev/null || command -v docker 2>/dev/null)
IMAGE_NAME  ?= quince
IMAGE_TAG   ?= local

# PER-RUNNER NAMESPACE (quince#45). Every identifier below that names a *running thing* is suffixed
# with this runner's name, because the gate targets use FIXED names and two sessions on one box
# destroy each other: B's opening `rm -f quince-e2e-app` kills A's app container mid-test, B's
# closing `network rm` tears down the network A is still using, and both write the same
# node_modules volume and the same log. Every symptom presents as a flake, which is the bug class
# these gates exist to eliminate.
#
# The name comes from quince#111's runner declaration — one fact deciding branch ownership, state
# directory and now container names, so the three cannot drift. Empty when nothing is declared,
# which is every CI run and every un-migrated box, and then every name below is exactly what it
# was. `|| true` because `runner get` refuses rather than defaulting, and a missing name here is
# not an error — it is the one-runner case.
RUNNER      := $(shell bin/forge-watch runner get 2>/dev/null || true)
NS          := $(if $(RUNNER),-$(RUNNER),)

# Named cache volumes — persistent across runs, safe to lose (live on the disposable
# runtime storage). They are what keep containerized gates fast.
#
# NOT NAMESPACED PER RUNNER (quince#45), and the reason is not "they are caches" — E2E_MODULES is
# cache-shaped too and IS namespaced. It is that all four are safe for CONCURRENT WRITERS by
# construction: Go's build and module caches lock, and the pnpm and uv stores are content-addressed,
# so two runners writing the same entry write the same bytes. A materialised `node_modules` tree is
# none of those things — it is one checkout's dependency state, and a second writer corrupts it.
#
# THAT PAIR IS THE POINT: `PNPM_VOL` is shared and `E2E_MODULES` is not, and both are node
# dependency storage. The asymmetry is deliberate and it is exactly what a later maintainer would
# "make consistent" in whichever direction they guessed. An undocumented decision is
# indistinguishable from an accident.
#
# Stated as a design property rather than a measurement: it rests on what those tools document about
# their own caches, not on two runners having been observed sharing one. This line used to promise
# that quince#175 exercised it, and quince#175's suite does NOT — that suite runs two live watchers
# over a stub forge and proves the WATCH layer composes (state files, registry, wake filter, orphan).
# It starts no container and writes none of these volumes. Two concurrent `make gates` runs is a
# different measurement, minutes long and needing a runtime, and it is still owed. Corrected rather
# than left standing, because a pointer to a proof that does not cover the claim is worse than no
# pointer: the next reader stops looking.
GO_BUILD_VOL := quince-go-build
GO_MOD_VOL   := quince-go-mod
PNPM_VOL     := quince-pnpm-store
UV_VOL       := quince-uv-cache

# Locally-built toolchain images (== Dockerfile build stages).
# NOT namespaced, deliberately, and this is the distinction quince#45's inventory does not draw:
# IMAGE_TAG does double duty. These three are CACHES — built once from the Dockerfile stages and
# reused by every gate — and sharing them between runners is the whole reason gates are fast.
# Suffixing them would make each runner rebuild its own toolchain for no isolation: nothing writes
# them during a gate, so there is nothing to collide.
TC_GO   := quince-toolchain-go:$(IMAGE_TAG)
TC_NODE := quince-toolchain-node:$(IMAGE_TAG)
TC_UV   := quince-toolchain-uv:$(IMAGE_TAG)

# e2e (Playwright) plumbing: a demo app container + a runner container on a shared network.
E2E_NET     := quince-e2e-net$(NS)
E2E_APP     := quince-e2e-app$(NS)
E2E_MODULES := quince-e2e-node-modules$(NS)
E2E_LOG     := /tmp/quince-e2e-app$(NS).log

# The PRODUCT image, separate from IMAGE_TAG for the reason above: `make image` rebuilds one fixed
# tag, so a second session retags the image the first is testing. This one is per-runner; the
# toolchain tag stays shared. `make push IMAGE_TAG=v1.2.3` is unaffected — push names a release
# tag explicitly and never a local one.
APP_TAG     ?= $(IMAGE_TAG)$(NS)

# The demo the DoD asks every PR for. Named per runner like everything else that runs, and its port
# is ALLOCATED rather than fixed, because two runners serving demos on one box is the ordinary case
# once parallel rungs exist (quince#45, quince#111).
DEMO_APP    := quince-demo$(NS)
DEMO_PORT   ?= 0
# The CONVENTION NAME a PR is allowed to carry. `CLAUDE.md` forbids an address in PR text and
# requires a conventional name instead, and the first version of `demo` printed `$(shell hostname)`
# — which is the box's identity, not a convention. On this runner that happens to look conventional
# and the gate passed; on a differently-named host the tool would have emitted exactly the string
# the skill forbids, into a session instructed to paste it. Naming it here closes that by
# construction rather than by discipline. Override when serving from somewhere else.
DEMO_HOST   ?= quince-runner

VERSION ?= 0.0.0-dev

# ---------------------------------------------------------------------------
# SCOPE — an OPTIONAL git range that lets a gate decline work its change cannot affect.
#
# Measured 2026-07-29: `gates` 5m12, `image` 5m40, `e2e` 6m39, run in PARALLEL so a PR costs 6m39 of
# wall clock — while product code has not changed in 91 commits. `gates-sh` alone is 61s.
#
# THE CONTRACT, and it is the whole of the Operator's constraint: **absent SCOPE, nothing changes.**
# `make gates`, `make image`, `make gates-ui-e2e` run exactly what they always ran — offline, with no
# remote, on any forge or none. Passing SCOPE is a caller explicitly asking to scope, the same
# opt-in shape `make privacy-check REF=origin/main...HEAD` already has. A git range is the only
# input; no CI variable and no forge concept reaches this file.
#
# The deciding lives in bin/gate-scope, not in a recipe, for the reason privacy-check exists as a
# script: a recipe of continuations cannot be shellchecked and cannot be unit-tested, and for THIS
# decision the untested path is the whole hazard — a ladder that quietly shrinks.
SCOPE ?=

# Parse-time, once. With no SCOPE, gate-scope returns every gate and exit 0, so all three lines below
# are the same values the Makefile has always had.
SCOPED_GATES  := $(shell bin/gate-scope --list "$(SCOPE)")
IMAGE_NEEDED  := $(shell bin/gate-scope --needed image "$(SCOPE)" >/dev/null 2>&1; echo $$?)
E2E_NEEDED    := $(shell bin/gate-scope --needed e2e   "$(SCOPE)" >/dev/null 2>&1; echo $$?)

# An empty ladder is the vacuous pass this whole mechanism could produce, so it is a HARD FAILURE
# rather than a quiet success. gate-scope never prints an empty list; if this fires, the script is
# missing or unrunnable, and `gates` would otherwise depend on nothing and report clean.
ifeq ($(strip $(SCOPED_GATES)),)
$(error gate-scope returned no gates — refusing to run an empty ladder. Is bin/gate-scope present and executable?)
endif

# GO_TEST_ARGS — what `gates-go` hands `go test`. The default is the whole tree, which is what the
# gate MEANS; an override is a targeted debugging run and says so in the output.
#
# It was accepted and IGNORED until quince#368: the recipe hardcoded `./...`, so
# `make gates-go GO_TEST_ARGS="-run TestX ./internal/foo"` ran every test in every package, and
# `-count=1` could not bust the test cache. Both failures are silent, because `make` accepts a
# variable nothing reads. The wasted time was the smaller cost — the larger one is that "I ran just
# this test" reached PR evidence and was not true, and nothing in the output contradicted it.
GO_TEST_ARGS ?= ./...
# `origin` is make's own answer to "did the caller set this, or is it my default?" — which is exactly
# the question, and it needs no sentinel value that a legitimate argument could collide with.
#
# ALL THREE SETTING FORMS, and the missing one was worse than the bug this fixes. `make gates FOO=x`
# reports `command line`, but `FOO=x make gates` reports `environment` — and `?=` honours an
# environment value. So the first version of this guard let the environment form through: the ladder
# ran a one-test Go leg, refused nothing, printed no PARTIAL RUN banner because the same variable
# gates it, and reported green. Caught in review on quince#434. `VAR=x make target` is the ordinary
# shell idiom and survives in an exported profile where an argument cannot, so it is the likelier
# form, not the exotic one.
#
# `filter` splits on whitespace, so these four words cover `command line`, `environment`, and
# `environment override` (the `make -e` form) while leaving `file` — what `?=` sets here — and
# `default` unmatched.
#
# THE TRADE, ON THE RECORD because somebody will hit it: an exported GO_TEST_ARGS in a shell profile
# or a CI environment now makes `make gates` a parse-time ERROR rather than a silently filtered
# ladder. That is the right direction — refuse rather than quietly do less (quince#41) — and it is a
# decision rather than an accident.
GO_TEST_ARGS_OVERRIDDEN := $(filter command line environment override,$(origin GO_TEST_ARGS))

# ONLY `gates-go` can honour it, so every other goal REFUSES rather than accepting and ignoring — the
# quince#41 precedent, where `privacy-check` was made to refuse instead of exiting 0 having swept
# nothing, on exactly this reasoning: a control that silently does less than it was asked is worse
# than one that fails.
#
# The dangerous goal is `gates`. Its Go leg would run a FILTERED suite while the ladder reported
# itself green — a stronger claim than anything that actually ran, and the same "ladder that quietly
# shrinks" hazard the SCOPED_GATES guard above exists for. Parse-time, so it fires before any
# container starts rather than after the Go leg has already passed on a subset.
ifneq ($(GO_TEST_ARGS_OVERRIDDEN),)
ifneq ($(filter-out gates-go,$(MAKECMDGOALS)),)
$(error GO_TEST_ARGS is honoured only by `gates-go`, and cannot be honoured by: $(filter-out gates-go,$(MAKECMDGOALS)). Refusing rather than ignoring it (quince#368). For a targeted run: make gates-go GO_TEST_ARGS="-run TestX ./internal/foo/...")
endif
endif

# Build-args threaded into every image build so the Dockerfile and the gates agree.
BUILD_ARGS := \
	--build-arg GO_IMAGE=$(GO_IMAGE) \
	--build-arg NODE_IMAGE=$(NODE_IMAGE) \
	--build-arg UV_IMAGE=$(UV_IMAGE) \
	--build-arg RUST_IMAGE=$(RUST_IMAGE) \
	--build-arg ALPINE_IMAGE=$(ALPINE_IMAGE) \
	--build-arg GOLANGCI_LINT_VERSION=$(GOLANGCI_LINT_VERSION) \
	--build-arg PNPM_VERSION=$(PNPM_VERSION) \
	--build-arg NETMUXD_REF=$(NETMUXD_REF) \
	--build-arg LIBIMOBILEDEVICE_REF=$(LIBIMOBILEDEVICE_REF) \
	--build-arg VERSION=$(VERSION)

# `run-in <image> <workdir> <extra-args>` — repo bind-mounted at /src.
RUN := $(RUNTIME) run --rm -v $(ROOT):/src

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@echo "quince gate ladder (all run in pinned toolchain containers via $(RUNTIME)):"
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | sort | \
	  awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'
	@echo "Runtime detected: $(RUNTIME)"

.PHONY: preflight
preflight:
	@test -n "$(RUNTIME)" || { echo "ERROR: no container runtime (nerdctl/docker) found. This box must be a container host — see deploy/dev.md."; exit 1; }

# The logic lives in deploy/privacy/privacy-check, not here. A recipe of backslash
# continuations cannot be shellchecked and cannot be unit-tested — and for THIS gate the
# untested path was the whole defect (quince#41): it exited 0 when it could not run, so a
# checklist could tick itself on every box that was unable to sweep.
#
#   make privacy-check                          the staged diff (the commit-time gate)
#   make privacy-check REF=origin/main...HEAD   the whole branch: diff AND commit messages
#   make privacy-check REF=... TEXT=body.md     …and the PR text (TEXT=/dev/stdin to pipe)
#
# exit 0 clean · 1 violation · 2 DID NOT RUN. The third code is the point of the rewrite.
.PHONY: privacy-check
privacy-check: ## Sweep for Operator-private strings (REF=<range> whole-branch, TEXT=<file>); FAILS when it cannot run
	@deploy/privacy/privacy-check $(if $(REF),--ref $(REF)) $(if $(TEXT),--text $(TEXT))

.PHONY: closing-refs-check
closing-refs-check: ## Find bare closing keywords that auto-close an issue (REF=<range>, TEXT=<file>); 0 none · 1 found · 2 DID NOT LOOK
	@bin/closing-refs-check $(if $(REF),--ref $(REF)) $(if $(TEXT),--text $(TEXT))

.PHONY: gap-heading-check
gap-heading-check: ## Find a `PROPOSED (gap)` block whose own body says RULED; 0 none · 1 found · 2 DID NOT RUN
	@bin/gap-heading-check

.PHONY: privacy-check-test
privacy-check-test: ## The privacy gate's own failure-path suite (synthetic — needs no private layer)
	@deploy/privacy/privacy-check-test

# ---------------------------------------------------------------------------
# Toolchain images — built once from the Dockerfile stages, reused by gates.
# ---------------------------------------------------------------------------
.PHONY: toolchains
toolchains: preflight ## Build the go/node/uv toolchain images from the Dockerfile
	$(RUNTIME) build $(BUILD_ARGS) --target toolchain-go   -t $(TC_GO)   -f deploy/Dockerfile .
	$(RUNTIME) build $(BUILD_ARGS) --target toolchain-node -t $(TC_NODE) -f deploy/Dockerfile .
	$(RUNTIME) build $(BUILD_ARGS) --target toolchain-uv   -t $(TC_UV)   -f deploy/Dockerfile .

.PHONY: tc-go
tc-go: preflight
	$(RUNTIME) build $(BUILD_ARGS) --target toolchain-go   -t $(TC_GO)   -f deploy/Dockerfile .
.PHONY: tc-node
tc-node: preflight
	$(RUNTIME) build $(BUILD_ARGS) --target toolchain-node -t $(TC_NODE) -f deploy/Dockerfile .
.PHONY: tc-uv
tc-uv: preflight
	$(RUNTIME) build $(BUILD_ARGS) --target toolchain-uv   -t $(TC_UV)   -f deploy/Dockerfile .

# ---------------------------------------------------------------------------
# Gate ladder.
# ---------------------------------------------------------------------------
.PHONY: gates
# Prerequisites come from gate-scope so the SKIPPING is visible as a dependency list rather than
# hidden inside a recipe: `make -n gates SCOPE=…` shows exactly what will run.
gates: $(SCOPED_GATES) ## Run the whole gate ladder (SCOPE=<git-range> runs only what the range affects)

# The lists are explicit and grow as scripts land — a glob would silently start linting (or
# silently stop linting) files nobody decided on.
#
# EXPLICIT IS NOT THE SAME AS COMPLETE, and for a year it was being read as though it were
# (quince#200). Nothing related these lists to what was on disk, so a file missing from them was
# not *skipped with a warning* — it was INVISIBLE, and `gates` reported success having never opened
# it. `bin/sh-lint-coverage` is the assertion that closes that: every shell file in the tree is in
# SH_ENTRYPOINTS, in SH_LIBS, or in that script's written-down exclusion list. The lists stay
# hand-maintained, which is the deliberate half; what changes is that forgetting one is now a
# failure rather than a silence.
#
# Two lists, because shellcheck lints one file at a time: linting a sourced library standalone
# reports every variable it assigns for its caller as unused (SC2034). So the LINT list holds
# entry points only and `-x` follows their `source` statements, which is also the analysis that
# can actually see cross-file usage. `-P SCRIPTDIR` resolves the `source=` directive next to the
# script, since the real path is computed at runtime. gh-bot sources nothing, but it is an entry
# point too, so it belongs on the same list.
#
# SH_LIBS is that second list, named at last. It was implicit in DEVCT_SCRIPTS — which exists for
# the `curl -k` grep and happens to contain the one library — so "the two-list split" described a
# structure only one of whose lists had a name. The coverage gate needs to distinguish "sourced,
# linted through its entry points" from "nobody lints this", and it cannot do that against a list
# that means something else.
DEVCT_SCRIPTS   := deploy/devct/devct deploy/devct/devct-template deploy/devct/lib.sh
SH_LIBS         := deploy/devct/lib.sh

# ---------------------------------------------------------------------------
# THE SHELL SUITES RUN IN BUSYBOX, BECAUSE BUSYBOX IS WHAT SHIPS (quince#246).
#
# `deploy/Dockerfile`'s runtime stage is `FROM $(ALPINE_IMAGE)`, so the product's shell is BusyBox
# `ash` and its coreutils are BusyBox. CI is `ubuntu-latest` — bash and GNU coreutils — so until now
# these suites were gated against an environment the product never runs in. That is not "add a
# harsher gate"; it is stop gating against the wrong one.
#
# `shellcheck -s sh` cannot substitute: it checks the shell LANGUAGE, not coreutils FLAGS.
# `ls --time-style=…`, `find -newermt` and `${PIPESTATUS[0]}` all pass shellcheck cleanly and all
# fail on BusyBox — measured on a box, in `deploy/dev.md`.
#
# BARE ALPINE PLUS ONLY WHAT THE SUITES NEED, ruled 2026-07-30, and deliberately NOT the shipped
# runtime image. That image also carries `python3`, `openssh-client`, `ca-certificates`, `tzdata` and
# the libimobiledevice runtime deps — none of which a shell suite touches, and pinning to it would
# make the gate weaker than bare Alpine for no gain. `jq`, `git` and `make` are the same
# implementations on Alpine as anywhere, so adding them reintroduces nothing GNU; the same three are
# what `toolchain-uv` installs.
#
# 18 OF 18 PASS, measured. Bare Alpine alone is 5 of 18 — the rest need `jq` or `git`. The last
# holdout was `home-resolution-test`, which needed `gh` on PATH and is fixed in quince#162, which is
# why there is no exclusion list here at all.
SH_SUITES       := suite-coverage-test privacy-check-test forge-watch-test preflight-test provision-guard-test \
                   forge-watch-exits-test forge-watch-stop-test forge-watch-fixtures-doc-test \
                   quince-runner-status-test pr-title-refs-test forge-watch-roundtrip-test \
                   forge-watch-ownership-test forge-watch-composition-test scratch-reap-test \
                   home-resolution-test wrapper-boundary-test gate-scope-test wrapper-body-test \
                   sh-lint-coverage-test allowlist-coverage-test forge-watch-seats-test \
                   gates-sh-exit-test forge-watch-stderr-test forge-watch-counters-test \
                   closing-refs-check-test forge-watch-role-test forge-watch-selfcaused-test \
                   forge-watch-actor-test forge-watch-postmerge-test pre-push-shim-test loop-drift-test \
                   forge-watch-owed-scope-test forge-watch-gh-auth-test gap-heading-check-test
# THE ONE EXCLUSION, NAMED — because "no exclusion list at all" was false (quince#246 review).
#
# `bin/forge-fetch-equivalence-test` needs a LIVE FORGE and a CREDENTIAL: it compares the `gh pr list`
# and GraphQL fetch paths against the real API, and its wrapper loop tries `gh-coder` then
# `gh-review`. None of that is hermetic in bare Alpine, and it never will be. It stays a
# host-run suite, deliberately outside this ladder — its own header says so — and it remains in
# SH_ENTRYPOINTS, so shellcheck still reads it.
#
# HOW IT WENT MISSING IS THE INSTRUCTIVE PART: the totals matched by COINCIDENCE. `SH_SUITES` has 18
# entries and there are 18 `*-test` scripts on disk, but they are not the same 18 — `forge-watch-test`
# is a make target with no script of that name (it runs `forge-watch replay`), and
# `forge-fetch-equivalence-test` is a script with no entry. One omission on each side, cancelling out,
# so "18 of 18" looked like totality and was a subset. That is the failure this whole change exists to
# prevent, and the excluded file's own comment already names the class: "a skip that looks like
# coverage" — written about a seat skipping it, now true of the gate.
SH_SUITES_EXCL  := forge-fetch-equivalence-test
SH_SUITE_IMAGE  := $(ALPINE_IMAGE)
# `bash` is here for ONE reason and it is not to run the suites under it — `/bin/sh` stays BusyBox
# `ash`, which is the whole point. `pr-title-refs-test` has two assertions that deliberately invoke
# `bash` to pin an exit code, because quince#224's defect was a usage error exiting 1 under bash — an
# ALLOCATED code in that contract, so it impersonated a real finding. Without `bash` installed those
# two cases skip, and the suite says so in its own output: "no bash here, so this run cannot see
# quince#224". Measured: 35 passed in bare Alpine, 37 on a host.
#
# Containerising without it would have quietly dropped two assertions from the gate — the "do not
# silently run a subset" failure this whole change exists to avoid, arriving inside the fix for it.
# Caught by reading the count rather than the colour: 35 where the host says 37.
SH_SUITE_PKGS   := jq git make bash

# Deliberately UNDOCUMENTED (no `##`), so it stays out of `make help` and out of the permission
# allowlist that `bin/allowlist-coverage` keys on documented targets. It exists for
# `bin/gates-sh-exit-test`, which must know the suite image to stub a runtime that fails only for it
# — restating `alpine:…` in the suite would stop matching the day the pin moves, and the stub would
# then succeed on the suite run and turn that test green for the wrong reason (quince#274).
.PHONY: print-sh-suite-image
print-sh-suite-image:
	@echo "$(SH_SUITE_IMAGE)"

# THE FAST PATH SURVIVES, which is a constraint rather than a nicety: a session iterating on a fix
# must be able to run one suite without paying container startup. `make forge-watch-test` still runs
# directly on the host, and `QUINCE_SH_RUN_HERE=1 make gates-sh` runs the whole set that way. The
# container is the GATE; the host run is the loop you work in.
#
# The same variable is what the container sets for itself, so the recipe cannot recurse into another
# container: inside, the suites are already in the right environment and run directly.
SH_ENTRYPOINTS  := deploy/devct/devct deploy/devct/devct-template bin/gh-bot \
                   deploy/runner/preflight-test deploy/runner/provision-guard-test \
                   deploy/runner/pre-push-shim deploy/runner/pre-push-shim-test \
                   deploy/runner/preflight deploy/runner/provision bin/forge-watch \
                   deploy/privacy/privacy-check deploy/privacy/privacy-check-test \
                   bin/forge-watch-exits-test bin/forge-watch-stop-test bin/forge-watch-fixtures-doc-test \
                   deploy/runner/quince-runner-status-test \
                   bin/gh-review bin/home-resolution-test bin/forge-watch-roundtrip-test \
                   bin/forge-watch-ownership-test bin/forge-watch-composition-test \
                   bin/forge-watch-seats-test \
                   bin/scratch-reap bin/scratch-reap-test \
                   bin/pr-title-refs bin/pr-title-refs-test bin/wrapper-boundary-test bin/wrapper-body-test \
                   bin/gate-scope bin/gate-scope-test bin/forge-fetch-equivalence-test bin/gh-coder bin/git-coder \
                   bin/gh-analyst \
                   bin/sh-lint-coverage bin/sh-lint-coverage-test deploy/e2e-run.sh \
                   deploy/storageless-smoke \
                   deploy/fly-deploy \
                   bin/allowlist-coverage bin/allowlist-coverage-test \
                   bin/suite-coverage bin/suite-coverage-test bin/gates-sh-exit-test \
                   bin/forge-watch-counters-test bin/closing-refs-check bin/closing-refs-check-test \
                   bin/forge-watch-role-test bin/forge-ledger bin/forge-watch-selfcaused-test \
                   bin/forge-watch-actor-test bin/forge-watch-postmerge-test \
                   bin/loop-drift bin/loop-drift-test \
                   bin/forge-watch-stderr-test bin/forge-watch-owed-scope-test bin/forge-watch-gh-auth-test \
                   bin/gap-heading-check bin/gap-heading-check-test

.PHONY: gates-sh
gates-sh: preflight ## Shell: shellcheck (POSIX sh) + list-completeness + the `curl -k` ban
	@# FIRST, because it decides whether the shellcheck below is a statement about the repository
	@# or only about a list somebody remembered to update (quince#200). Host-side: it reads
	@# `git ls-files` and needs no container.
	@bin/sh-lint-coverage $(SH_ENTRYPOINTS) $(SH_LIBS)
	@# The same totality argument one layer over, for the PERMISSION allowlist rather than the lint
	@# list (quince#256). Host-side and needs no container: it reads the Makefile and the settings
	@# file. It runs here because the gap it catches is invisible in normal operation — the
	@# permission classifier waves an unlisted command through, so nothing surfaces the omission
	@# until the classifier is gone, which is when you can least act on it.
	@bin/allowlist-coverage
	@# The same totality argument aimed at the SUITES themselves (quince#246 review): every *-test
	@# script is in SH_SUITES or named in SH_SUITES_EXCL. Host-side; reads the Makefile and git.
	@bin/suite-coverage
	@# A `PROPOSED (gap)` block whose own body says the question is RULED (quince#408). Host-side:
	@# it reads `git ls-files '*.md'`. It runs in gates-sh rather than as a separate gate because
	@# the marker means "stop that thread", so a stale one sends the NEXT session away from finished
	@# work — a cost nobody pays at the moment it is created, which is what gates are for.
	@bin/gap-heading-check
	$(RUNTIME) run --rm -v $(ROOT):/src -w /src $(SHELLCHECK_IMAGE) \
	  -x -P SCRIPTDIR -s sh $(SH_ENTRYPOINTS)
	@# TLS is pinned, never disabled (docs/specs/devct/devct.md). The rule needs teeth, and a
	@# grep over the scripts themselves is the cheapest possible tooth.
	@if grep -nE -- '(^|[[:space:]])(-k|--insecure)([[:space:]]|$$)' $(DEVCT_SCRIPTS); then \
	  echo "gates-sh: 'curl -k/--insecure' is banned in deploy/devct — pin the cert instead"; exit 1; \
	fi
	@# A PR title must never reach a recipe as a MAKE variable. make expands a command-line value
	@# whether or not the recipe references it — command-line variables are exported to the recipe
	@# environment, and exporting forces expansion — so `make … TITLE='$$(shell cmd)'` executes cmd
	@# no matter how carefully the recipe is written. Un-interpolating the recipe satisfies the
	@# letter and leaves the vector; the value has to arrive in the ENVIRONMENT instead, which is
	@# why pr-title-check takes TITLE_ENV=<NAME>. Same tooth as the curl ban above, for the same
	@# reason: the rule was already written in a comment once and a comment is not a control.
	@if grep -n 'title-env "$$(TITLE)"\|--title "$$(TITLE)"' Makefile; then \
	  echo "gates-sh: a PR title must not be interpolated into a recipe — pass TITLE_ENV=<NAME> (quince#94)"; exit 1; \
	fi
	@# The privacy gate's failure paths. Gated rather than hand-run, because "the gate passes
	@# when it cannot run" is precisely the bug this suite exists to hold shut — leaving its
	@# proof to whoever remembers would reproduce the defect one level up (quince#41, #64).
	@# Synthetic fixtures only: no private layer needed, so it runs here, on CI, and anywhere.
	@# THE SUITES, in BusyBox (quince#246). Reported rather than implied, because a gate that
	@# containerises some of its work and says `clean` cannot be told from one that containerised all
	@# of it — quince#41's shape, and the reason the count and the image are both printed.
	@#
	@# `safe.directory /src` IS NOT BOILERPLATE — it is the first thing the fixed exit code caught
	@# (quince#274). `actions/checkout` marks the repo safe at its HOST path; inside the container the
	@# same tree is `/src`, owned by a uid the container's root is not, so every `git` call in a suite
	@# dies with `detected dubious ownership`. On CI that silently took THREE `gate-scope-test`
	@# assertions down — an exported SCOPE, MAKEFLAGS propagation, and `image` skipping an empty range
	@# — from the moment quince#246 containerised the suites. It never reproduced on a box, where the
	@# tree and the container's root share a uid. Two defects from one change, and the false-green is
	@# what kept the second invisible.
	@#
	@# EVERY ARM ENDS IN THE STATUS THAT DECIDES, and the `if`s below are load-bearing rather than
	@# stylistic (quince#274). A shell `if` block exits with the status of the LAST command it ran, so
	@# `$(RUNTIME) run …; echo …` reports the ECHO — and this recipe printed `gates-sh: clean` and
	@# exited 0 over a suite with 15 failing assertions. That is the exact defect the comment three
	@# lines up cites #41 about, introduced by the change that wrote the comment: the accounting line
	@# added to make the gate honest is what swallowed the failure. Keep the status last.
	@if [ -n "$(QUINCE_SH_RUN_HERE)" ]; then \
	  echo "gates-sh: running $(words $(SH_SUITES)) shell suite(s) ON THIS HOST (QUINCE_SH_RUN_HERE set) — NOT the BusyBox gate"; \
	  $(MAKE) --no-print-directory $(SH_SUITES); \
	else \
	  echo "gates-sh: running $(words $(SH_SUITES)) shell suite(s) in $(SH_SUITE_IMAGE) + $(SH_SUITE_PKGS) — BusyBox ash, as the image ships"; \
	  echo "gates-sh: EXCLUDED from the container, $(words $(SH_SUITES_EXCL)): $(SH_SUITES_EXCL) — needs a live forge and a credential; run it on a host"; \
	  if $(RUNTIME) run --rm -e QUINCE_SH_RUN_HERE=1 -v $(ROOT):/src -w /src $(SH_SUITE_IMAGE) \
	    sh -c 'apk add --no-cache -q $(SH_SUITE_PKGS) >/dev/null && git config --global --add safe.directory /src && exec make --no-print-directory $(SH_SUITES)'; then \
	    echo "gates-sh: $(words $(SH_SUITES)) containerised in $(SH_SUITE_IMAGE), $(words $(SH_SUITES_EXCL)) excluded by name above — no suite ran host-side unannounced"; \
	  else \
	    echo "gates-sh: SUITE(S) FAILED in $(SH_SUITE_IMAGE) — see the suite output above"; exit 1; \
	  fi; \
	fi
	@echo "gates-sh: clean"

# The rung-loop spec's G1, which until now was run by nothing (quince#64). Every round of
# forge-watch work proved it by hand and pasted the output into the PR — honest while somebody
# does it, and the exact shape this repository keeps filing issues about: a gate whose positive
# answer depends on the author remembering. Three of the loop fixtures spend real seconds in
# sleeps, which is the kind of cost that quietly stops being paid.
#
# HOST-SIDE, beside the containerised shellcheck rather than inside it: the `"kind": "loop"`
# fixtures drive the real `watch` verb against a stub `gh`, so they need a subprocess and a
# clock. That was the open question on quince#64 and this is the answer — the same placement
# privacy-check-test already uses, and no network is involved either way.
.PHONY: forge-watch-test
forge-watch-test: ## forge-watch's fixture suite — the rung-loop spec's G1 (~23s, host-side)
	@# jq is asserted, not assumed. The fixtures ARE json; without jq `replay` would fail somewhere
	@# further in with a confusing message, so this refuses up front and names the cause.
	@#
	@# NOT a quince#41-style exit-code distinction, and worth saying so where the code is rather
	@# than only in a PR body: `make` returns its own generic recipe-failure code for ANY failed
	@# target, so "could not run" and "ran and found a failure" both surface as the same exit here.
	@# The distinction lives in the MESSAGE, not in the status. A caller that needs to tell them
	@# apart has to invoke bin/forge-watch directly.
	@command -v jq >/dev/null 2>&1 || { \
	  echo "forge-watch-test: DID NOT RUN — jq is absent and the fixtures are JSON. Refusing rather than skipping."; \
	  exit 1; \
	}
	@bin/forge-watch replay bin/testdata/forge/*.json

# quince#75: the skills enumerated forge-watch's exits as 0, 6 and 7 and said "every exit is a
# re-arm" — false on the refusal (exit 1), where obeying it loops forever with no watch running.
# This asserts every exit DERIVED from the tool is MEASURED and is NAMED in every document that
# enumerates them, so the next omission fails here rather than in a session.
.PHONY: forge-watch-exits-test
forge-watch-exits-test: ## Every exit forge-watch can return is named in the skills (quince#75)
	@bin/forge-watch-exits-test

.PHONY: forge-watch-stop-test
forge-watch-stop-test: ## `stop` / `stop --all` against live pids — what replay cannot cover (quince#118)
	@bin/forge-watch-stop-test

.PHONY: forge-watch-fixtures-doc-test
forge-watch-fixtures-doc-test: ## The fixtures' README indexes every fixture, both directions (quince#107)
	@bin/forge-watch-fixtures-doc-test

.PHONY: quince-runner-status-test
quince-runner-status-test: ## rc-service status classifies on the newest session-log state (quince#101)
	@deploy/runner/quince-runner-status-test

.PHONY: forge-watch-roundtrip-test
forge-watch-roundtrip-test: ## What one writer records must survive the OTHER writers (quince#168)
	@bin/forge-watch-roundtrip-test

.PHONY: forge-watch-ownership-test
forge-watch-ownership-test: ## Another runner's event must not wake mine (quince#111 face 3)
	@bin/forge-watch-ownership-test

.PHONY: forge-watch-seats-test
forge-watch-seats-test: ## A seat declared on the OTHER box is attributable here (quince#265)
	@bin/forge-watch-seats-test

.PHONY: forge-watch-stderr-test
forge-watch-stderr-test: ## The liveness probe must not leak raw shell errors (quince#279)
	@bin/forge-watch-stderr-test

.PHONY: forge-watch-owed-scope-test
forge-watch-owed-scope-test: ## owed is scoped by branch, and an unattributable branch is OWED (quince#227)
	@bin/forge-watch-owed-scope-test

.PHONY: forge-watch-gh-auth-test
forge-watch-gh-auth-test: ## require_gh must check gh can AUTHENTICATE, not just exist (quince#429)
	@bin/forge-watch-gh-auth-test

.PHONY: forge-watch-role-test
forge-watch-role-test: ## Branch-ownership suppression is role-dependent (quince#292)
	@bin/forge-watch-role-test

.PHONY: closing-refs-check-test
closing-refs-check-test: ## The closing-keyword gate's own refusals, all three exit codes (quince#293)
	@bin/closing-refs-check-test

.PHONY: gap-heading-check-test
gap-heading-check-test: ## The gap-marker gate's refusals + quince#408's three instances as fixtures
	@bin/gap-heading-check-test

.PHONY: forge-watch-counters-test
forge-watch-counters-test: ## The loop counts its own cycles, and the count survives (quince#282)
	@bin/forge-watch-counters-test

# quince#111's four faces, TOGETHER, which no other suite does — every one of them drives a single
# runner against state describing another. Gated rather than hand-run for the reason quince#64 gives
# about the loop fixtures: a proof whose positive answer depends on the author remembering is the
# defect class this repository keeps filing issues about, and this one is the acceptance test for a
# prerequisite. ~10s, host-side, and it needs procfs for its last phase — on a host without /proc
# the orphan assertions SKIP and say so rather than passing silently.
.PHONY: forge-watch-composition-test
forge-watch-composition-test: ## Two runners, two live watchers, at once — quince#111's acceptance (quince#175)
	@bin/forge-watch-composition-test

.PHONY: scratch-reap-test
scratch-reap-test: ## The reaper's REFUSALS — the half that loses work if it is wrong (quince#45)
	@bin/scratch-reap-test

# `home-resolution-test` HAD NO `.PHONY` OF ITS OWN until now, and that is a defect quince#158
# introduced: it inserted this target between the quince#94 comment block below and the
# `.PHONY: pr-title-refs-test` line that belonged to it, orphaning both. It worked only because no
# file named `home-resolution-test` exists in the repo root — the day one did, make would call the
# target up to date and silently skip the gate. Two renames later nobody had looked, which is the
# whole argument for fixing it on sight rather than filing it.
.PHONY: home-resolution-test
home-resolution-test: ## Entrypoints deriving paths from $$HOME must not require it SET — the service path
	@bin/home-resolution-test

# quince#157. Ordering is the property, and ordering is invisible to any check that reads the two
# refusals separately — both are present and both are correct in isolation, which is exactly why the
# wrong one shipped as the first to speak. The only way to see it is to make both live and observe
# which one wins.
# The map from paths to gates is a hand-written fact, and a stale one produces a gate that should
# have run and did not — silently, green. Gated for that reason rather than for the script's logic.
.PHONY: gate-scope-test
gate-scope-test: ## The gate map is total and the skipping is never silent (quince#46 follow-on)
	@bin/gate-scope-test

.PHONY: wrapper-boundary-test
wrapper-boundary-test: ## A boundary refusal must outrank an environment one in the gh wrappers (quince#157)
	@bin/wrapper-boundary-test

.PHONY: wrapper-body-test
wrapper-body-test: ## every gh wrapper must REFUSE inline --body — backticks in it EXECUTE (quince#518)
	@bin/wrapper-body-test

# quince#256 item 3's other half, and the same argument as sh-lint-coverage-test below: a totality
# check with no tests would report `clean` about a comparison it had failed to make. The cases that
# earn their keep are the ASYMMETRY ones — direction 1 keys on documented targets, direction 2 on
# real ones, and a fixture encodes each legitimate mismatch (real-but-undocumented-and-allowlisted,
# which is `preflight`; real-but-undocumented-and-not, which is `tc-go`). Synthetic throughout, via
# ALLOWLIST_COVERAGE_ROOT, so it never reads the real tree and cannot go green because somebody
# fixed the allowlist.
# quince#246 review's other half. `suite-coverage` is the gate; this proves the gate. Its most
# valuable cases are the two ASYMMETRIES — an SH_SUITES entry with no script of its own
# (forge-watch-test's shape) must be accepted, and an excluded suite must not be reported missing —
# because a check that got either wrong would fail forever on this repository. Synthetic throughout
# via SUITE_COVERAGE_ROOT: every case builds a throwaway git repo, so it never reads the real tree.
.PHONY: suite-coverage-test
suite-coverage-test: ## The suite-totality gate's own refusals, all three directions (quince#246)
	@bin/suite-coverage-test

.PHONY: gates-sh-exit-test
gates-sh-exit-test: ## gates-sh must not say `clean` over a failed suite run (quince#274)
	@bin/gates-sh-exit-test

.PHONY: allowlist-coverage-test
allowlist-coverage-test: ## The allowlist totality gate's own refusals, both directions (quince#256)
	@bin/allowlist-coverage-test

# quince#200's other half. `sh-lint-coverage` is the gate; this proves the gate. A coverage check
# with no tests would be the same defect it exists to close, one level up — it would report `clean`
# about a repository it had failed to scan, and nothing would tell that apart from a repository
# that is genuinely covered. Synthetic: every case builds a throwaway git repo in a temp dir, so it
# never reads the real tree and cannot pass or fail for reasons unrelated to the code under test.
.PHONY: sh-lint-coverage-test
sh-lint-coverage-test: ## The lint-coverage gate's own refusals — a shell file in no list (quince#200)
	@bin/sh-lint-coverage-test

# quince#94's lint half. `forge-watch` derives its watch set from PR TITLES, so a bare `#N` in a
# title is claimed by the repo the PR is in — and a devlog title reading `(#102, #104)` made two
# quince PRs into derived issues of the devlog, costing two failing `gh` calls PER TICK on the
# reviewer's box until somebody noticed. Synthetic (stub `gh`, no network), so it runs in gates-sh
# beside the other failure-path suites and needs neither a token nor the private layer.
.PHONY: pr-title-refs-test
pr-title-refs-test: ## The title check's failure paths, incl. the ruled DID-NOT-RUN (quince#94)
	@bin/pr-title-refs-test

# The check itself, for CI and for a hand-run. It stays a make target because ci.yml is
# deliberately logic-free — "CI calls only `make` targets (no logic here)" — so the workflow is
# three lines and the behaviour it invokes is reviewable, runnable and testable right here.
#
#   make pr-title-check REPO=owner/name TITLE_ENV=PR_TITLE   <- CI USES THIS
#   make pr-title-check REPO=owner/name PR=42
#   PR_TITLE='the title' make pr-title-check REPO=owner/name TITLE_ENV=PR_TITLE   <- hand-run
#
# THERE IS DELIBERATELY NO `TITLE=`. It existed, marked "local only, injectable", and the review
# asked that the title never be interpolated into the recipe text. Measuring how to do that
# found something worse: **`make` expands a command-line value whether the recipe references it
# or not**, because command-line variables are exported to the recipe's environment, and
# exporting forces expansion. Both probes, on this box:
#
#   make -f p probe TITLE='$(shell touch /tmp/x)'     recipe references TITLE   -> /tmp/x EXISTS
#   make -f p probe TITLE='$(shell touch /tmp/x)'     recipe NEVER mentions it  -> /tmp/x EXISTS
#   PR_TITLE='$(shell touch /tmp/x)' make -f p probe                            -> it does NOT
#
# So un-interpolating the recipe would have satisfied the letter of the ruling and left the
# vector open one level up — a fix that reads as safe and is not. A title arriving as a make
# variable cannot be made safe at any recipe; a title arriving in the ENVIRONMENT never becomes
# a make variable and is never expanded. That is the whole reason TITLE_ENV takes a NAME.
#
# A PR title is attacker-controlled on a public repository, so this matters for CI and for
# anything anyone later wires this into — which was the review's actual argument for deleting
# it, and it is stronger than either of us realised. `gates-sh` greps for a reintroduced
# `$(TITLE)` so the rule has teeth rather than living in this comment.
.PHONY: pr-title-check
pr-title-check: ## Bare #N in a PR title must resolve there (REPO=owner/name + TITLE_ENV=<NAME> or PR=<n>); 0 clean · 1 match · 2 DID NOT RUN
	@bin/pr-title-refs --repo "$(REPO)" $(if $(PR),--pr "$(PR)",$(if $(TITLE_ENV),--title-env "$(TITLE_ENV)",))

# The runner spec's G1 — "`preflight` against a table of environments" — likewise proven by hand and
# pasted into a PR until now (quince#32). preflight's refusals ARE its product: it exists to stop a
# runner coming up unable to do Remote Control, billing against an API key, or holding the identity
# of the box it is meant to be separate from. Synthetic throughout — a stub `claude`, fake token
# files, no private layer and no runner — so it runs on CI and anywhere.
.PHONY: preflight-test
preflight-test: ## preflight's refusals — the runner spec's G1 (synthetic; no runner needed)
	@deploy/runner/preflight-test

.PHONY: pre-push-shim-test
pre-push-shim-test: ## the journal pre-push shim in every push state (quince#308; hermetic, no git)
	@deploy/runner/pre-push-shim-test

.PHONY: provision-guard-test
provision-guard-test: ## provision's identity guard in every credential state (quince#234; synthetic, dry-run)
	@deploy/runner/provision-guard-test

# THE `lab` TAG IS COMPILED HERE AND DELIBERATELY NOT RUN (quince#789).
#
# `-tags lab` guards the hardware harnesses — gate 12's zfs run, the filesystem matrix. They are
# excluded from every gate by design, and the cost of that was invisible until somebody tried to add
# one: a file behind an excluded tag is not *skipped with a warning*, it is INVISIBLE, and `gates`
# reports success having never opened it. `labgate_test.go` stopped compiling when `NewManager` lost
# its RetentionPolicy parameter (quince#473) and nothing said so for the whole interval, so a
# deferred gate and a gate nobody can run looked identical from `main`.
#
# That is quince#200's finding one level up — there it was a hand-maintained list, here it is the
# build system — and the remedy is the same shape: make forgetting it a failure rather than a
# silence.
#
# COMPILE, DO NOT RUN. `go vet` type-checks the files; `go test -tags lab` would try to execute
# harnesses that need a real pool, a real device or a real filesystem tier. They skip without their
# env, so running them would mostly be a slow no-op — but "mostly" is the wrong guarantee for CI,
# and the defect this catches is a compile error, which vet catches at full strength.
.PHONY: gates-go
gates-go: tc-go ## Go: gofmt + vet (incl. -tags lab) + golangci-lint + go test -race (GO_TEST_ARGS="-run X ./pkg/..." to target)
	@[ -z "$(GO_TEST_ARGS_OVERRIDDEN)" ] || printf 'gates-go: PARTIAL RUN — go test %s, NOT the whole tree.\ngates-go: this is a targeted debugging run and is NOT a full Go gate; do not report it as one (quince#368).\n' '$(GO_TEST_ARGS)'
	$(RUN) -w /src/core \
	  -v $(GO_BUILD_VOL):/root/.cache/go-build -v $(GO_MOD_VOL):/go/pkg/mod \
	  -e CGO_ENABLED=1 $(TC_GO) sh -euc '\
	    unformatted=$$(gofmt -l .); \
	    if [ -n "$$unformatted" ]; then echo "gofmt needs to run on:"; echo "$$unformatted"; exit 1; fi; \
	    go vet ./...; \
	    go vet -tags lab ./...; \
	    golangci-lint run; \
	    go test -race -cover $(GO_TEST_ARGS)'

.PHONY: fmt
fmt: tc-go ## Go: gofmt -w (auto-format) + go mod tidy (run after editing core)
	$(RUN) -w /src/core \
	  -v $(GO_BUILD_VOL):/root/.cache/go-build -v $(GO_MOD_VOL):/go/pkg/mod \
	  -e CGO_ENABLED=1 $(TC_GO) sh -euc 'gofmt -w . && go mod tidy'

.PHONY: gen-golden
gen-golden: tc-go ## Regenerate httpapi golden fixtures (UPDATE_GOLDEN=1)
	$(RUN) -w /src/core \
	  -v $(GO_BUILD_VOL):/root/.cache/go-build -v $(GO_MOD_VOL):/go/pkg/mod \
	  -e CGO_ENABLED=1 -e UPDATE_GOLDEN=1 $(TC_GO) sh -euc 'go test ./internal/httpapi/ -run TestReadEndpointsMatchGolden'

.PHONY: gates-vault
gates-vault: tc-uv ## Vault: ruff check + ruff format --check + mypy --strict + pytest
	$(RUN) -w /src/vault \
	  -v $(UV_VOL):/uv-cache \
	  -e UV_CACHE_DIR=/uv-cache $(TC_UV) sh -euc '\
	    uv sync --frozen --all-extras; \
	    uv run ruff check .; \
	    uv run ruff format --check .; \
	    uv run mypy --strict src tests; \
	    uv run pytest -q'

.PHONY: gates-ui
gates-ui: tc-node ## UI: typecheck + lint + vitest + build
	$(RUN) -w /src/ui \
	  -v $(PNPM_VOL):/pnpm-store \
	  $(TC_NODE) sh -euc '\
	    pnpm install --frozen-lockfile --store-dir /pnpm-store; \
	    pnpm run typecheck; \
	    pnpm run lint; \
	    pnpm run test; \
	    pnpm run build'

# ---------------------------------------------------------------------------
# Production image + registry push.
# ---------------------------------------------------------------------------
.PHONY: image
image: preflight ## Build the production container (proves go:embed of the built UI)
ifeq ($(IMAGE_NEEDED),3)
	@echo "image: SKIPPED — nothing it builds from changed in $(SCOPE). gate-scope decided; pass no SCOPE to force it."
else
	$(RUNTIME) build $(BUILD_ARGS) --target runtime -t $(IMAGE_NAME):$(APP_TAG) -f deploy/Dockerfile .
endif

.PHONY: gates-ui-e2e
gates-ui-e2e: image ## Playwright stories 1-2 against `quince serve --demo` (two containers)
ifeq ($(E2E_NEEDED),3)
	@echo "gates-ui-e2e: SKIPPED — nothing the image is built from changed in $(SCOPE). Same coverage as image, so the two skip together and e2e never runs against an image that was not built."
else
	@set -e; \
	$(RUNTIME) rm -f $(E2E_APP) >/dev/null 2>&1 || true; \
	$(RUNTIME) network create $(E2E_NET) >/dev/null 2>&1 || true; \
	$(RUNTIME) run -d --name $(E2E_APP) --network $(E2E_NET) \
	  -e QUINCE_LISTEN=:8968 -e QUINCE_DATA=/tmp -e QUINCE_CACHE=/tmp \
	  $(IMAGE_NAME):$(APP_TAG) serve --demo >/dev/null; \
	status=0; \
	$(RUN) --network $(E2E_NET) -w /src/ui \
	  -v quince-pnpm-store:/pnpm-store -v $(E2E_MODULES):/src/ui/node_modules \
	  -e BASE_URL=http://$(E2E_APP):8968 -e CI=1 -e PNPM_VERSION=$(PNPM_VERSION) \
	  $(PLAYWRIGHT_IMAGE) sh /src/deploy/e2e-run.sh || status=$$?; \
	$(RUNTIME) logs $(E2E_APP) > $(E2E_LOG) 2>&1 || true; \
	$(RUNTIME) rm -f $(E2E_APP) >/dev/null 2>&1 || true; \
	$(RUNTIME) network rm $(E2E_NET) >/dev/null 2>&1 || true; \
	exit $$status
endif

# ---------------------------------------------------------------------------
# demo — build this branch and serve it, on THIS box.
#
# WHY IT EXISTS. `/qa` and `/report` told a session to run `deploy/devct/devct deploy`, and on the
# runner that exits 1: devct needs devct.conf, a Proxmox API token and a pinned CA under
# ~/.config/quince, and `provision`/`preflight` never place any of them. So the DoD's
# deploy-by-default leg silently required the Operator's workstation, discovered at report time —
# the most expensive moment — and contradicted the unfreeze bar of a session that runs on a naked
# /kickoff.
#
# quince-devlog#45 removed the reason for the round trip: the runner IS the work host, so there is
# no container to select and no hypervisor to ask. Building and serving here needs no Proxmox
# credential at all, which makes pr.6's credential-concentration boundary smaller rather than
# larger.
#
# THE PORT IS TRIED, NOT PROBED. nerdctl 2.2.1 does not support `-p 0:` — measured: it exits 1
# rather than allocating — so something has to choose. Probing for a free port and then binding it
# is a race with whoever takes it in between; letting the RUNTIME fail to bind and trying the next
# port makes the allocator and the binder the same actor, so there is no window. It reports the port
# it got, because a demo URL nobody can derive is the same as no demo.
.PHONY: demo
demo: image ## Build this branch and serve it in --demo mode on this box; prints a fetched URL
	@set -e; \
	$(RUNTIME) rm -f $(DEMO_APP) >/dev/null 2>&1 || true; \
	port=$${DEMO_PORT}; [ "$$port" -ne 0 ] 2>/dev/null || port=8968; \
	started=no; \
	for try in 1 2 3 4 5 6 7 8 9 10; do \
	  if $(RUNTIME) run -d --name $(DEMO_APP) -p $$port:8968 \
	       -e QUINCE_LISTEN=:8968 -e QUINCE_DATA=/tmp -e QUINCE_CACHE=/tmp \
	       $(IMAGE_NAME):$(APP_TAG) serve --demo >/dev/null 2>&1; then started=yes; break; fi; \
	  $(RUNTIME) rm -f $(DEMO_APP) >/dev/null 2>&1 || true; \
	  port=$$((port + 1)); \
	done; \
	if [ "$$started" != yes ]; then \
	  echo "demo: could not bind a port in 10 tries from $${DEMO_PORT:-8968} — say so as 'deploy: unavailable', never as silence"; \
	  exit 1; \
	fi; \
	ok=no; \
	for i in $$(seq 1 60); do \
	  if curl -fsS "http://127.0.0.1:$$port/api/health" 2>/dev/null | grep -q '"status"'; then ok=yes; break; fi; \
	  sleep 1; \
	done; \
	if [ "$$ok" != yes ]; then \
	  echo "demo: the container started but /api/health never answered on $$port — logs:"; \
	  $(RUNTIME) logs $(DEMO_APP) 2>&1 | tail -20; \
	  exit 1; \
	fi; \
	echo ""; \
	echo "demo: answering on 127.0.0.1:$$port — FETCHED, not composed."; \
	echo ""; \
	echo "  paste this into the PR:  http://$(DEMO_HOST):$$port/"; \
	echo ""; \
	echo "  Two lines, not one, because they are different claims. The first is what this box"; \
	echo "  verified: the service answered /api/health on the loopback port. The second is a"; \
	echo "  CONVENTION NAME for a reader on the LAN — this tool cannot verify that it resolves for"; \
	echo "  anybody else, and saying so is cheaper than a URL that was 'verified' and does not open."; \
	echo ""; \
	echo "  stop it with: $(RUNTIME) rm -f $(DEMO_APP)"

.PHONY: demo-stop
demo-stop: ## Remove this runner's demo container
	@$(RUNTIME) rm -f $(DEMO_APP) >/dev/null 2>&1 || true; echo "demo: $(DEMO_APP) removed"

.PHONY: push
push: preflight ## Push to $(REGISTRY) (creds via env only; never committed)
	@test -n "$(REGISTRY)" || { echo "ERROR: set REGISTRY=host[:port]/repo (env only)"; exit 1; }
	@# SOURCE is the per-runner APP_TAG, DESTINATION is the release IMAGE_TAG. `image` builds
	@# locally under this runner's tag, so pushing IMAGE_TAG->IMAGE_TAG would look for an image
	@# that does not exist on a box where a runner is declared. Retagging across the two is what
	@# keeps "build locally, push a release name" working in both cases: with no runner declared
	@# the two are identical and this is exactly what it always was.
	$(RUNTIME) tag  $(IMAGE_NAME):$(APP_TAG) $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	$(RUNTIME) push $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

# ---------------------------------------------------------------------------
# Housekeeping.
# ---------------------------------------------------------------------------
.PHONY: clean
clean: ## Drop cache volumes and locally-built images
	-$(RUNTIME) volume rm $(GO_BUILD_VOL) $(GO_MOD_VOL) $(PNPM_VOL) $(UV_VOL) $(E2E_MODULES)
	-$(RUNTIME) rmi $(TC_GO) $(TC_NODE) $(TC_UV) $(IMAGE_NAME):$(IMAGE_TAG)

.PHONY: forge-watch-selfcaused-test
forge-watch-selfcaused-test: ## An act this runner performed must not wake it (quince#242)
	@bin/forge-watch-selfcaused-test

.PHONY: forge-watch-actor-test
forge-watch-actor-test: ## An act attributed to this seat must not wake it (quince#242 step 3)
	@bin/forge-watch-actor-test

.PHONY: forge-watch-postmerge-test
forge-watch-postmerge-test: ## Post-merge housekeeping does not wake; a post-merge comment does (quince#83)
	@bin/forge-watch-postmerge-test

# The SUITE proves the gate; the GATE is what runs against this repository's own `.claude/`. Both,
# because either alone is half a check: a suite over synthetic trees never looks at the real files,
# and the gate alone would pass for two indistinguishable reasons — the copies agree, or the
# extractor stopped finding them.
.PHONY: loop-drift-test
loop-drift-test: ## The loop commands inlined in the skills still match loop-protocol.md (quince#54)
	@bin/loop-drift-test
	@bin/loop-drift

.PHONY: storageless-smoke
storageless-smoke: image ## A REAL container from a fresh install to a working storage (qn.6e)
# IT SKIPS WITH THE IMAGE, exactly as gates-ui-e2e does, and for the same reason: `image` declines to
# build under a scope it does not match, and a recipe that ran anyway would drive a container from an
# image that does not exist. Without this the target is BROKEN for every scoped invocation — measured
# on quince#718, where `image: SKIPPED` was followed by `pull access denied for quince` and a red
# `e2e` (quince#713).
#
# SKIPPING COSTS NO COVERAGE. `e2e`'s scope is `core/ ui/ vault/ deploy/` plus the shared pins — the
# whole product tree — and this smoke's subject lives inside it: the startup path is `core/`, the
# script is `deploy/`. A change that could break a fresh install therefore TRIGGERS this rather than
# skipping it. What skips is docs, `.claude/` and `.github/`-only diffs, which cannot.
ifeq ($(E2E_NEEDED),3)
	@echo "storageless-smoke: SKIPPED — nothing the image is built from changed in $(SCOPE). Same coverage as image, so the two skip together and this never runs against an image that was not built."
else
	@IMAGE=$(IMAGE_NAME):$(APP_TAG) RUNTIME=$(RUNTIME) deploy/storageless-smoke
endif
