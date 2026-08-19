# quince — single multi-stage image. Also the source of the dev/CI toolchain images:
# `make` builds the `toolchain-*` stages and runs every gate inside them, so dev, CI, and
# this release image compile with byte-identical toolchains. All pins come from
# versions.env via --build-arg (defaults below mirror it). Stack canon: D2/D10/D13.

# ---- pinned base images (mirror versions.env) — ONE Alpine line (3.24) everywhere ----
ARG GO_IMAGE=golang:1.26.5-alpine3.24
ARG NODE_IMAGE=node:22.23.1-alpine3.24
ARG ALPINE_IMAGE=alpine:3.24

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

# --- libimobiledevice: pinned tag, built from source with quince's in-tree patches (qn.6b) ---
# Built from SOURCE (not the Alpine `-progs` package) so five in-tree patches ship in the image:
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
#   0004  makes `mb2_copy_file_by_path` UNLINK its destination before creating it. Every other path
#         in that file already does; this one truncated the existing inode in place, which reaches
#         THROUGH the hardlink a seed shares with the committed backup and corrupts a version
#         (quince#518). The destination is named by the DEVICE at runtime and the handler is not
#         command-gated, so it is reachable during a backup and no list on quince's side could
#         exclude it. A no-op when the destination is not aliased.
#   0005  treats a network device's NetworkAddress as opaque and opens its service connections
#         through the muxer by DeviceID + port. This removes the cross-kernel sockaddr ABI leak
#         (notably Apple's Darwin usbmuxd mounted into this Linux container) and leaves link-local
#         routing and interface scope with the muxer that discovered the device.
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

# =============================================================================
# Runtime — Alpine + Apple protocol tooling + the daemon. No language runtime (D10).
# =============================================================================
FROM ${ALPINE_IMAGE} AS runtime
# Apple-protocol userland (stack D2).
#
# THIS IMAGE SHIPS NO MUXER DAEMON (qn.6p D1, Operator 2026-08-16). The operator runs one — a host
# usbmuxd, a sidecar container, or another tool's — and quince dials it. `devices.manage_muxer:
# true` is refused at startup, so a daemon here would be one nothing could start; it goes with the
# profile rather than being left as a binary a reader would reasonably assume was reachable.
# The in-container profile is DESCOPED, not abandoned: bringing it back restores the `usbmuxd`
# package and the netmuxd source stage, which built the pinned v0.4.3 that muxsup's parked argv was
# verified against.
#
# WHEN IT COMES BACK, `apk add usbmuxd` MUST STAY IN THIS RUN — ABOVE THE COPY BELOW. Measured
# 2026-08-16: usbmuxd depends on `libimobiledevice`, and installing it REPLACES quince's patched
# /usr/lib/libimobiledevice-1.0.so.6 — a 1.1 MB regular file — with a SYMLINK into the stock
# package. Patch 0001 (receive timeout 30s → 15min) disappears with it, and the symptom is a
# premature receive error on a slow passcode or a large transfer: exactly the bug that patch
# exists to fix, arriving with nothing to connect it to a Dockerfile line. Adding the package in a
# later layer, or after the COPY, loses the patch silently.
#
#   libimobiledevice       = the CLIs (idevicebackup2/idevicepair/ideviceinfo) — built FROM SOURCE
#                            in the libimobiledevice-build stage (qn.6b patches), NOT apk'd; only
#                            its runtime link deps are installed here.
#   libplist / -glue / libtatsu = runtime link deps of the built libimobiledevice (Alpine 3.24);
#   libusbmuxd             = the client library the CLIs speak (also a build dep, runtime here).
#   libssl3 / libcrypto3   = DECLARED, NOT INHERITED. This comment used to say they "come
#                            transitively via usbmuxd", which was measured false: `apk info
#                            --rdepends libssl3` names libapk, libcurl, ssl_client and
#                            libimobiledevice — never usbmuxd. quince's own source-built
#                            libimobiledevice links them, so they are named here rather than left
#                            to whatever the base happens to ship (qn.6p D1).
#   openssh-client         = transport for the zfs `hook` mode — quince runs the constrained
#                            quince-zfs-helper on the ZFS host over ssh (deploy/storage.md).
#                            Required whenever storage.zfs.mode: hook; harmless otherwise.
#                            (qn.4a gate-15 finding #2: hook mode was proven on a hand-`apk
#                            add`ed container; codified here so it survives a compose recreate.)
#
# The runtime base stays alpine:3.24 — ONE Alpine line across every stage (versions.env). The
# reason recorded here was that the usbmuxd DAEMON is packaged only at 3.24+; that daemon is gone
# and the single-line rule stands on its own.
RUN apk add --no-cache \
      ca-certificates tzdata \
      libplist \
      libimobiledevice-glue \
      libusbmuxd \
      libtatsu \
      libssl3 \
      libcrypto3 \
      openssh-client

# The patched libimobiledevice: the CLIs + the shared lib (shared object glob covers .so + soname).
COPY --from=libimobiledevice-build /stage/usr/bin/ /usr/bin/
COPY --from=libimobiledevice-build /stage/usr/lib/libimobiledevice-1.0.so* /usr/lib/
COPY --from=core-build /quince /usr/local/bin/quince

ENV QUINCE_DATA=/data QUINCE_CACHE=/cache QUINCE_LISTEN=:8968

# THE IMAGE MUST CONTAIN THE DIRECTORIES IT DECLARES. Without this, `docker run quince` cannot
# start: the two paths above do not exist, so quince WARNs once per path and then dies on
# `open db /cache/demo.db: unable to open database file (14)` — a sqlite errno that names neither
# the missing directory nor the variable that pointed at it.
#
# It went unnoticed because NOTHING HAD EVER RUN THIS IMAGE AS-IS. `make demo` overrides both
# (`-e QUINCE_DATA=/tmp -e QUINCE_CACHE=/tmp`, and /tmp exists); compose.nas.yml and
# compose.lab.yml BIND-MOUNT over both, and a bind mount creates its target. So every existing
# path either replaced the values or covered the paths, and these defaults were dead config that
# read as live. The public demo on fly.io (quince#494) is the first deployment to run the image
# with no mounts and no overrides, and it crash-looped to its restart limit.
#
# A VOLUME IS NOT THE FIX and would re-hide it: the demo needs these EPHEMERAL. A fresh rootfs on
# every start is exactly what makes the reset work (docs/specs/public-demo/public-demo.md, D4).
RUN mkdir -p /data /cache

EXPOSE 8968
ENTRYPOINT ["quince"]
CMD ["serve"]
