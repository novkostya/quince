# quince — single multi-stage image. Also the source of the dev/CI toolchain images:
# `make` builds the `toolchain-*` stages and runs every gate inside them, so dev, CI, and
# this release image compile with byte-identical toolchains. All pins come from
# versions.env via --build-arg (defaults below mirror it). Stack canon: D2/D10/D13.

# ---- pinned base images (mirror versions.env) — ONE Alpine line (3.24) everywhere ----
ARG GO_IMAGE=golang:1.26.5-alpine3.24
ARG NODE_IMAGE=node:22.23.1-alpine3.24
ARG RUST_IMAGE=rust:1.97.1-alpine3.24
ARG ALPINE_IMAGE=alpine:3.24
# uv publishes no -alpine3.24 tag; toolchain-uv is built FROM alpine and copies this in.
ARG UV_IMAGE=ghcr.io/astral-sh/uv:0.5.11-python3.12-alpine

# =============================================================================
# Toolchain stages — reused verbatim by the Makefile gate targets.
# =============================================================================
FROM ${GO_IMAGE} AS toolchain-go
ARG GOLANGCI_LINT_VERSION
RUN apk add --no-cache git make bash gcc musl-dev
# golangci-lint built WITH this exact Go, so no analyzer/toolchain skew. v2 module path
# carries the /v2 suffix.
RUN go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v${GOLANGCI_LINT_VERSION}
WORKDIR /src

FROM ${NODE_IMAGE} AS toolchain-node
ARG PNPM_VERSION
RUN apk add --no-cache git
RUN corepack enable && corepack prepare pnpm@${PNPM_VERSION} --activate
WORKDIR /src

# Alias the uv image as a stage so COPY --from can reference it (buildkit forbids
# variable expansion directly in --from).
FROM ${UV_IMAGE} AS uv-bin

# toolchain-uv is built on the same Alpine line as everything else; uv (a static binary)
# is copied in from astral's image, and the python that runs the vault is Alpine's own.
FROM ${ALPINE_IMAGE} AS toolchain-uv
RUN apk add --no-cache git make python3
COPY --from=uv-bin /usr/local/bin/uv /usr/local/bin/uv
ENV UV_CACHE_DIR=/uv-cache UV_PYTHON_DOWNLOADS=never
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

# --- libimobiledevice: pinned tag, built from source with quince's in-tree patches (qn.6b) ---
# Built from SOURCE (not the Alpine `-progs` package) so three in-tree patches ship in the image:
#   0001  raises the default receive timeout 30s -> 15min (upstream #1413) — a slow passcode,
#         large-device transfer, or transient Wi-Fi flap no longer trips a premature receive error.
#   0002  adds `idevicebackup2 --gate <path>` — pause after the Backup request, before the message
#         loop, so quince can seed working/ in parallel (candidate C).
#   0003  adds `ideviceinfo --set-bool` — WRITE a lockdown boolean at an arbitrary domain/key.
#         Upstream calls lockdownd_set_value from exactly two tools (idevicename, idevicedate) and
#         BOTH hardcode domain=NULL plus the key, so no shipped binary can set
#         com.apple.mobile.wireless_lockdown/EnableWifiConnections (qn.7). Go cannot call the
#         library directly either: the core is built CGO_ENABLED=0 on purpose, for the static
#         musl binary that has to run on a low-end NAS.
# Only THIS repo is built: Alpine 3.24 ships every -dev dependency at a satisfying version, incl.
# libtatsu (verified live, qn.6b spike). NOTE (amendment A): 0001 raises the default for EVERY
# libimobiledevice binary, so quince bounds its non-backup tool ops with a Go-side deadline
# (core/internal/deviceops) — the 15-min patience is intentional only on the backup receive path.
FROM ${ALPINE_IMAGE} AS libimobiledevice-build
ARG LIBIMOBILEDEVICE_REF
RUN apk add --no-cache git build-base autoconf automake libtool pkgconf \
      openssl-dev libplist-dev libimobiledevice-glue-dev libusbmuxd-dev libtatsu-dev
WORKDIR /src/libimobiledevice
RUN git clone --depth 1 --branch ${LIBIMOBILEDEVICE_REF} \
      https://github.com/libimobiledevice/libimobiledevice .
COPY deploy/patches/libimobiledevice/ /patches/
RUN set -eux; for p in /patches/*.patch; do echo "applying $p"; git apply --verbose "$p"; done
# Drop the unconditional -Werror on configure.ac's CFLAGS line (only that line) so a benign
# warning on Alpine's GCC can't fail this vendored build; the patched binaries are unchanged.
# --without-cython: no Python bindings. Install to a DESTDIR the runtime stage copies from.
# autogen.sh runs ./configure "$@" only when NOCONFIGURE is UNSET (empty) — do not set it.
RUN sed -i '/CFLAGS+=/ s/ -Werror//' configure.ac \
    && ./autogen.sh --prefix=/usr --without-cython \
    && make -j"$(nproc)" \
    && make DESTDIR=/stage install

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
#     python. venvs aren't relocatable and hardcode their interpreter — toolchain-uv is
#     the same Alpine (3.24) base the runtime uses (identical /usr/bin/python3), so
#     building into /opt/quince/vault here means no path move and no interpreter skew. ---
FROM toolchain-uv AS vault-build
ENV UV_PROJECT_ENVIRONMENT=/opt/quince/vault/.venv
WORKDIR /opt/quince/vault
COPY vault/ ./
RUN uv sync --no-dev --frozen --python /usr/bin/python3

# =============================================================================
# Runtime — Alpine + Apple protocol tooling + python venv + the daemon.
# =============================================================================
FROM ${ALPINE_IMAGE} AS runtime
# Apple-protocol userland (stack D2). Runtime base is alpine:3.24 because the usbmuxd
# DAEMON lands in Alpine community only at 3.24+ (1.1.1_git20250201; genuinely absent in
# 3.21–3.23 — the qn.0 "no package" finding was correct for the old pinned base).
#   usbmuxd                = USB muxer daemon, the reliability anchor. netmuxd's young USB
#                            path has a recorded bug (D2), so usbmuxd is the USB default;
#                            netmuxd serves Wi-Fi. `simple` profile runs it in-container.
#   netmuxd (source, above)= the Wi-Fi muxer
#   libimobiledevice       = the CLIs (idevicebackup2/idevicepair/ideviceinfo) — built FROM SOURCE
#                            in the libimobiledevice-build stage (qn.6b patches), NOT apk'd; only
#                            its runtime link deps are installed here.
#   libplist / -glue / libtatsu = runtime link deps of the built libimobiledevice (Alpine 3.24);
#   libusbmuxd             = the client library the CLIs speak (also a build dep, runtime here).
#                            libssl3/libcrypto3 come transitively via usbmuxd.
#   openssh-client         = transport for the zfs `hook` mode — quince runs the constrained
#                            quince-zfs-helper on the ZFS host over ssh (deploy/storage.md).
#                            Required whenever storage.zfs.mode: hook; harmless otherwise.
#                            (qn.4a gate-15 finding #2: hook mode was proven on a hand-`apk
#                            add`ed container; codified here so it survives a compose recreate.)
# python3 runs the vault venv.
RUN apk add --no-cache \
      ca-certificates tzdata \
      usbmuxd \
      libplist \
      libimobiledevice-glue \
      libusbmuxd \
      libtatsu \
      openssh-client \
      python3

COPY --from=vault-build /opt/quince/vault /opt/quince/vault
COPY --from=netmuxd-build /netmuxd /usr/local/bin/netmuxd
# The patched libimobiledevice: the CLIs + the shared lib (shared object glob covers .so + soname).
COPY --from=libimobiledevice-build /stage/usr/bin/ /usr/bin/
COPY --from=libimobiledevice-build /stage/usr/lib/libimobiledevice-1.0.so* /usr/lib/
COPY --from=core-build /quince /usr/local/bin/quince

# The vault venv's console script (quince-vault) on PATH.
ENV PATH="/opt/quince/vault/.venv/bin:${PATH}"
ENV QUINCE_DATA=/data QUINCE_CACHE=/cache QUINCE_BACKUPS=/backups QUINCE_LISTEN=:8080
EXPOSE 8080
ENTRYPOINT ["quince"]
CMD ["serve"]
