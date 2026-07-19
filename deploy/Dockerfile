# quince — single multi-stage image. Also the source of the dev/CI toolchain images:
# `make` builds the `toolchain-*` stages and runs every gate inside them, so dev, CI, and
# this release image compile with byte-identical toolchains. All pins come from
# versions.env via --build-arg (defaults below mirror it). Stack canon: D2/D10/D13.

# ---- pinned base images (mirror versions.env) ----
ARG GO_IMAGE=golang:1.24.5-alpine3.21
ARG NODE_IMAGE=node:22.12.0-alpine3.21
ARG UV_IMAGE=ghcr.io/astral-sh/uv:0.5.11-python3.12-alpine
ARG RUST_IMAGE=rust:1.88-alpine3.21
ARG ALPINE_IMAGE=alpine:3.21

# =============================================================================
# Toolchain stages — reused verbatim by the Makefile gate targets.
# =============================================================================
FROM ${GO_IMAGE} AS toolchain-go
ARG GOLANGCI_LINT_VERSION
RUN apk add --no-cache git make bash gcc musl-dev
# golangci-lint built WITH this exact Go, so no analyzer/toolchain skew.
RUN go install github.com/golangci/golangci-lint/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}
WORKDIR /src

FROM ${NODE_IMAGE} AS toolchain-node
ARG PNPM_VERSION
RUN apk add --no-cache git
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate
WORKDIR /src

FROM ${UV_IMAGE} AS toolchain-uv
RUN apk add --no-cache git make
ENV UV_CACHE_DIR=/uv-cache
WORKDIR /src

# =============================================================================
# Build stages.
# =============================================================================
# --- UI: pnpm build → /src/ui/dist ---
FROM toolchain-node AS ui-build
COPY ui/package.json ui/pnpm-lock.yaml ./ui/
WORKDIR /src/ui
RUN pnpm install --frozen-lockfile
WORKDIR /src
COPY ui/ ./ui/
WORKDIR /src/ui
RUN pnpm run build

# --- netmuxd: pinned, built from source (Wi-Fi is a primary transport, D13) ---
FROM ${RUST_IMAGE} AS netmuxd-build
ARG NETMUXD_REF
RUN apk add --no-cache git musl-dev pkgconf openssl-dev openssl-libs-static
RUN git clone --depth 1 --branch ${NETMUXD_REF} https://github.com/jkcoxson/netmuxd /src/netmuxd
WORKDIR /src/netmuxd
ENV OPENSSL_STATIC=1
RUN cargo build --release && install -m0755 target/release/netmuxd /netmuxd

# --- core: embed the built UI, cgo-free static binary ---
FROM toolchain-go AS core-build
ARG VERSION=0.0.0-dev
WORKDIR /src/core
COPY core/go.mod ./
COPY core/go.sum* ./
RUN go mod download
COPY core/ ./
COPY --from=ui-build /src/ui/dist ./internal/webui/dist
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags "-s -w -X github.com/novkostya/quince/core/internal/version.Version=${VERSION}" \
      -o /quince ./cmd/quince

# --- vault: locked venv, built at its FINAL path against the runtime's own system
#     python. venvs aren't relocatable and hardcode their interpreter, so we build on the
#     same Alpine base the runtime uses (identical /usr/bin/python3) at /opt/quince/vault
#     — no path move, no interpreter skew. uv is copied in as a static binary. ---
FROM ${ALPINE_IMAGE} AS vault-build
RUN apk add --no-cache python3
COPY --from=toolchain-uv /usr/local/bin/uv /usr/local/bin/uv
ENV UV_PYTHON_DOWNLOADS=never UV_PROJECT_ENVIRONMENT=/opt/quince/vault/.venv
WORKDIR /opt/quince/vault
COPY vault/ ./
RUN uv sync --no-dev --frozen --python /usr/bin/python3

# =============================================================================
# Runtime — Alpine + Apple protocol tooling + python venv + the daemon.
# =============================================================================
FROM ${ALPINE_IMAGE} AS runtime
# Apple-protocol userland. Package names verified on Alpine 3.21 (main+community):
#   libimobiledevice-progs = the CLIs (idevicebackup2/idevicepair/ideviceinfo) — D2
#   libusbmuxd             = the usbmuxd *client* library the CLIs speak
# netmuxd (from source, above) is the Wi-Fi muxer. python3 runs the vault venv.
#
# GAP (stack D2 says "the container ships usbmuxd"): Alpine has NO usbmuxd *daemon*
# package (only libusbmuxd). How the USB daemon is provided — source-built in a stage
# vs the host's usbmuxd socket bind-mounted in (as compose.lab.yml already does) — is an
# open architectural question, ruled before qn.2 (first USB device access). qn.0 ships
# the client libs + CLIs; its gate (version/health/selftest) needs no daemon.
RUN apk add --no-cache \
      ca-certificates tzdata \
      libimobiledevice \
      libimobiledevice-progs \
      libusbmuxd \
      python3

COPY --from=vault-build /opt/quince/vault /opt/quince/vault
COPY --from=netmuxd-build /netmuxd /usr/local/bin/netmuxd
COPY --from=core-build /quince /usr/local/bin/quince

# The vault venv's console script (quince-vault) on PATH.
ENV PATH="/opt/quince/vault/.venv/bin:${PATH}"
ENV QUINCE_DATA=/data QUINCE_CACHE=/cache QUINCE_BACKUPS=/backups QUINCE_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["quince"]
CMD ["serve"]
