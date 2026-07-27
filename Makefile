# quince — the one entrypoint. CI calls only these targets (no logic in YAML).
#
# The dev host is a PURE CONTAINER HOST: no Go/Node/Python toolchains are installed on
# it. Every gate runs inside a pinned toolchain container built from the production
# Dockerfile's own build stages, so dev / CI / release compile with identical toolchains.
# All version + image pins live in versions.env (the single source of truth).
#
# Requirements on the box: `make` + a container runtime (nerdctl or docker) with buildkit.
# Program canon: docs/program/quince.program.md "Where work runs" + "Gate ladder".

include versions.env

ROOT        := $(abspath .)
RUNTIME     ?= $(shell command -v nerdctl 2>/dev/null || command -v docker 2>/dev/null)
IMAGE_NAME  ?= quince
IMAGE_TAG   ?= local

# Named cache volumes — persistent across runs, safe to lose (live on the disposable
# runtime storage). They are what keep containerized gates fast.
GO_BUILD_VOL := quince-go-build
GO_MOD_VOL   := quince-go-mod
PNPM_VOL     := quince-pnpm-store
UV_VOL       := quince-uv-cache

# Locally-built toolchain images (== Dockerfile build stages).
TC_GO   := quince-toolchain-go:$(IMAGE_TAG)
TC_NODE := quince-toolchain-node:$(IMAGE_TAG)
TC_UV   := quince-toolchain-uv:$(IMAGE_TAG)

# e2e (Playwright) plumbing: a demo app container + a runner container on a shared network.
E2E_NET     := quince-e2e-net
E2E_APP     := quince-e2e-app
E2E_MODULES := quince-e2e-node-modules

VERSION ?= 0.0.0-dev

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
gates: gates-go gates-vault gates-ui gates-sh ## Run the whole gate ladder

# The lists are explicit and grow as scripts land — a glob would silently start linting (or
# silently stop linting) files nobody decided on.
#
# Two lists, because shellcheck lints one file at a time: linting a sourced library standalone
# reports every variable it assigns for its caller as unused (SC2034). So the LINT list holds
# entry points only and `-x` follows their `source` statements, which is also the analysis that
# can actually see cross-file usage. `-P SCRIPTDIR` resolves the `source=` directive next to the
# script, since the real path is computed at runtime. gh-bot sources nothing, but it is an entry
# point too, so it belongs on the same list.
DEVCT_SCRIPTS   := deploy/devct/devct deploy/devct/devct-template deploy/devct/lib.sh
SH_ENTRYPOINTS  := deploy/devct/devct deploy/devct/devct-template bin/gh-bot bin/gh-arch \
                   deploy/runner/preflight deploy/runner/provision bin/forge-watch \
                   deploy/privacy/privacy-check deploy/privacy/privacy-check-test

.PHONY: gates-sh
gates-sh: preflight ## Shell: shellcheck (POSIX sh) + the `curl -k` ban
	$(RUNTIME) run --rm -v $(ROOT):/src -w /src $(SHELLCHECK_IMAGE) \
	  -x -P SCRIPTDIR -s sh $(SH_ENTRYPOINTS)
	@# TLS is pinned, never disabled (docs/specs/devct/devct.md). The rule needs teeth, and a
	@# grep over the scripts themselves is the cheapest possible tooth.
	@if grep -nE -- '(^|[[:space:]])(-k|--insecure)([[:space:]]|$$)' $(DEVCT_SCRIPTS); then \
	  echo "gates-sh: 'curl -k/--insecure' is banned in deploy/devct — pin the cert instead"; exit 1; \
	fi
	@# The privacy gate's failure paths. Gated rather than hand-run, because "the gate passes
	@# when it cannot run" is precisely the bug this suite exists to hold shut — leaving its
	@# proof to whoever remembers would reproduce the defect one level up (quince#41, #64).
	@# Synthetic fixtures only: no private layer needed, so it runs here, on CI, and anywhere.
	@$(MAKE) --no-print-directory privacy-check-test
	@$(MAKE) --no-print-directory forge-watch-test
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
	@# jq is asserted, not assumed. The fixtures ARE json; without jq `replay` would fail
	@# somewhere further in, and a suite that cannot run must say so rather than produce a
	@# confusing failure — the lesson of quince#41 applied to the gate that quince#64 adds.
	@command -v jq >/dev/null 2>&1 || { \
	  echo "forge-watch-test: jq is absent, and the fixtures are JSON — REFUSING rather than skipping."; \
	  exit 1; \
	}
	@bin/forge-watch replay bin/testdata/forge/*.json

.PHONY: gates-go
gates-go: tc-go ## Go: gofmt + vet + golangci-lint + go test -race
	$(RUN) -w /src/core \
	  -v $(GO_BUILD_VOL):/root/.cache/go-build -v $(GO_MOD_VOL):/go/pkg/mod \
	  -e CGO_ENABLED=1 $(TC_GO) sh -euc '\
	    unformatted=$$(gofmt -l .); \
	    if [ -n "$$unformatted" ]; then echo "gofmt needs to run on:"; echo "$$unformatted"; exit 1; fi; \
	    go vet ./...; \
	    golangci-lint run; \
	    go test -race -cover ./...'

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
	$(RUNTIME) build $(BUILD_ARGS) --target runtime -t $(IMAGE_NAME):$(IMAGE_TAG) -f deploy/Dockerfile .

.PHONY: gates-ui-e2e
gates-ui-e2e: image ## Playwright stories 1-2 against `quince serve --demo` (two containers)
	@set -e; \
	$(RUNTIME) rm -f $(E2E_APP) >/dev/null 2>&1 || true; \
	$(RUNTIME) network create $(E2E_NET) >/dev/null 2>&1 || true; \
	$(RUNTIME) run -d --name $(E2E_APP) --network $(E2E_NET) \
	  -e QUINCE_LISTEN=:8080 -e QUINCE_DATA=/tmp -e QUINCE_CACHE=/tmp -e QUINCE_BACKUPS=/tmp \
	  $(IMAGE_NAME):$(IMAGE_TAG) serve --demo >/dev/null; \
	status=0; \
	$(RUN) --network $(E2E_NET) -w /src/ui \
	  -v quince-pnpm-store:/pnpm-store -v $(E2E_MODULES):/src/ui/node_modules \
	  -e BASE_URL=http://$(E2E_APP):8080 -e CI=1 -e PNPM_VERSION=$(PNPM_VERSION) \
	  $(PLAYWRIGHT_IMAGE) sh /src/deploy/e2e-run.sh || status=$$?; \
	$(RUNTIME) logs $(E2E_APP) > /tmp/quince-e2e-app.log 2>&1 || true; \
	$(RUNTIME) rm -f $(E2E_APP) >/dev/null 2>&1 || true; \
	$(RUNTIME) network rm $(E2E_NET) >/dev/null 2>&1 || true; \
	exit $$status

.PHONY: push
push: preflight ## Push to $(REGISTRY) (creds via env only; never committed)
	@test -n "$(REGISTRY)" || { echo "ERROR: set REGISTRY=host[:port]/repo (env only)"; exit 1; }
	$(RUNTIME) tag  $(IMAGE_NAME):$(IMAGE_TAG) $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)
	$(RUNTIME) push $(REGISTRY)/$(IMAGE_NAME):$(IMAGE_TAG)

# ---------------------------------------------------------------------------
# Housekeeping.
# ---------------------------------------------------------------------------
.PHONY: clean
clean: ## Drop cache volumes and locally-built images
	-$(RUNTIME) volume rm $(GO_BUILD_VOL) $(GO_MOD_VOL) $(PNPM_VOL) $(UV_VOL) $(E2E_MODULES)
	-$(RUNTIME) rmi $(TC_GO) $(TC_NODE) $(TC_UV) $(IMAGE_NAME):$(IMAGE_TAG)
