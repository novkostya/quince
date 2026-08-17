# Credits and third-party licences

quince does not talk to an iPhone by itself. It drives Apple's own backup protocol through other
people's tools, and it stores the result. This file names what it stands on and records the licence
position, because the container image ships several of those projects — some of them patched.

quince's own code is [MIT](LICENSE).

Every licence below was **read**, not remembered: the Go set from the module cache after
`go list -deps ./cmd/quince`, the npm set from each installed package, the Alpine set from
`/lib/apk/db/installed` inside the built image, and libimobiledevice's from upstream at the exact
tag `versions.env` pins. The commands are named in each section so you can re-run them.

---

## The Apple-protocol tools the image carries

| Project | What it does here | Licence | Pinned at |
|---|---|---|---|
| [libimobiledevice](https://github.com/libimobiledevice/libimobiledevice) | Everything device-facing: `idevicebackup2` runs the backup, `idevicepair` pairs, `ideviceinfo` reads and writes lockdown values | LGPL-2.1-or-later | `versions.env` → `LIBIMOBILEDEVICE_REF` |
| [libplist](https://github.com/libimobiledevice/libplist) | Reads Apple's property lists | LGPL-2.1-or-later | Alpine package |
| [libimobiledevice-glue](https://github.com/libimobiledevice/libimobiledevice-glue), [libusbmuxd](https://github.com/libimobiledevice/libusbmuxd), [libtatsu](https://github.com/libimobiledevice/libtatsu) | Support libraries the above links against | LGPL-2.1 | Alpine packages |
| [OpenSSL](https://www.openssl.org/) (`libssl3`, `libcrypto3`) | TLS for the device connection | Apache-2.0 | Alpine package |
| [OpenSSH](https://www.openssh.com/) (client) | Transport for the ZFS `hook` mode — quince runs a constrained helper on the ZFS host over ssh | SSH-OpenSSH (BSD-style) | Alpine package |
| [Alpine Linux](https://alpinelinux.org/) | Base image — see *The base image* below | mixed, incl. GPL-2.0 | `versions.env` → `ALPINE_VERSION` |

**quince patches libimobiledevice, and that matters more than the rest of this file.** The image
does not install Alpine's `libimobiledevice-progs` package. It builds the project from source at the
pinned tag with four patches from [`deploy/patches/libimobiledevice/`](deploy/patches/libimobiledevice/),
and ships the results: about twenty `idevice*` binaries plus the patched
`libimobiledevice-1.0.so.6`. Those are LGPL-2.1 works that quince has modified and distributes, so
the corresponding source has to be available. It is, and this is how:

- the upstream tag is in [`versions.env`](versions.env) (`LIBIMOBILEDEVICE_REF`), and the upstream
  source is at <https://github.com/libimobiledevice/libimobiledevice>;
- every change quince makes is a patch file in `deploy/patches/libimobiledevice/`, in this
  repository, under the same LGPL-2.1 as the code it modifies;
- [`deploy/Dockerfile`](deploy/Dockerfile) is the exact build, and it applies those patches and
  nothing else.

The LGPL also asks that you be able to relink against your own version of the library. You can: the
patched shared object is an ordinary file at `/usr/lib/libimobiledevice-1.0.so.6` in the image, the
binaries link it dynamically, and replacing it replaces it for all of them.

*Read with `apk info -L libplist` and the package database inside the image; the tools' licence
comes from the header of every file in `tools/` at tag `1.4.0`, where upstream's own README also
states it. Note that libimobiledevice's `COPYING` holds the GPL-2.0 text and `COPYING.LESSER` the
LGPL-2.1 — the licence that actually applies is the LGPL one named in the source headers.*

## The base image

The runtime is `alpine:3.24`, and it brings 36 packages. Most are permissive — musl and the Alpine
tooling metadata are MIT, zlib is Zlib, tzdata is public domain — but **several are GPL-2.0-only**:
`busybox`, `busybox-binsh`, `apk-tools`, `libapk`, `alpine-baselayout`, `alpine-baselayout-data`,
`scanelf` and `ssl_client`.

quince modifies none of them. They arrive as unmodified Alpine binary packages, which makes this
mere aggregation — shipping a GPL program alongside quince does not make quince's own code GPL. The
source-availability obligation is still real, and it is met the way every Alpine image meets it:
each package's exact version is in the image (`apk info` prints it), and Alpine publishes the
corresponding source for that version at <https://gitlab.alpinelinux.org/alpine/aports>.

Two packages are dual-licensed and quince relies on the permissive half: `libidn2` and
`libunistring` are *GPL-2.0-or-later OR LGPL-3.0-or-later*, and `zstd-libs` is
*BSD-3-Clause OR GPL-2.0-or-later*.

*Read with `awk -F: '/^P:/{p=$2} /^L:/{print p, $2}' /lib/apk/db/installed` inside the built image —
Alpine's own metadata, not a guess about what a package name usually means.*

## Go libraries compiled into the binary

`go list -deps ./cmd/quince` returns 23 third-party modules. Every one is permissive; nothing
copyleft is linked into quince.

| Module | What it does here | Licence |
|---|---|---|
| [github.com/coder/websocket](https://github.com/coder/websocket) | The one WebSocket — device, job and progress events pushed to the browser | ISC |
| [github.com/creack/pty](https://github.com/creack/pty) | Gives `idevicebackup2` a terminal, so the backup password goes over a pty instead of argv | MIT |
| [github.com/go-webauthn/webauthn](https://github.com/go-webauthn/webauthn) | Passkeys — registration, login and re-authentication | BSD-3-Clause |
| [github.com/oklog/ulid](https://github.com/oklog/ulid) | Sortable ids for jobs and devices | Apache-2.0 |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | argon2id for the admin password; `ssh` and `knownhosts` for the ZFS hook transport | BSD-3-Clause |
| [golang.org/x/sys](https://pkg.go.dev/golang.org/x/sys) | The syscalls versioned storage is built on — `renameat2(RENAME_EXCHANGE)`, `FICLONE`, `statfs` | BSD-3-Clause |
| [gopkg.in/yaml.v3](https://github.com/go-yaml/yaml) | Reads and rewrites `config.yml` without destroying what the user wrote | MIT (with Apache-2.0 portions) |
| [howett.net/plist](https://github.com/DHowett/go-plist) | Apple property lists — the muxer protocol, and the backup's own `Info.plist` / `Manifest.plist` | BSD-2-Clause (with BSD-3-Clause portions) |
| [modernc.org/sqlite](https://gitlab.com/cznic/sqlite) | The app database, and read-only opens of a backup's `Manifest.db` — SQLite transpiled to Go, so the binary stays cgo-free | BSD-3-Clause |

Pulled in by those, and linked too:

| Module | Licence |
|---|---|
| [github.com/fxamacker/cbor/v2](https://github.com/fxamacker/cbor), [github.com/x448/float16](https://github.com/x448/float16), [github.com/golang-jwt/jwt/v5](https://github.com/golang-jwt/jwt), [github.com/go-viper/mapstructure/v2](https://github.com/go-viper/mapstructure), [github.com/dustin/go-humanize](https://github.com/dustin/go-humanize), [github.com/tinylib/msgp](https://github.com/tinylib/msgp), [github.com/philhofer/fwd](https://github.com/philhofer/fwd) | MIT |
| [github.com/go-webauthn/x](https://github.com/go-webauthn/x), [github.com/google/uuid](https://github.com/google/uuid), [github.com/remyoudompheng/bigfft](https://github.com/remyoudompheng/bigfft), [modernc.org/libc](https://gitlab.com/cznic/libc), [modernc.org/mathutil](https://gitlab.com/cznic/mathutil), [modernc.org/memory](https://gitlab.com/cznic/memory) | BSD-3-Clause |
| [github.com/google/go-tpm](https://github.com/google/go-tpm) | Apache-2.0 |

That is the whole list. No web framework, no ORM, no logging library.

## The web UI, bundled into the same binary

The UI is built with Vite and embedded into the Go binary with `go:embed`, so its dependencies ship
inside the one executable. `pnpm ls --prod --depth 20` resolves to 43 packages: React and
React DOM, React Router, TanStack Query, Zustand, the Radix UI primitives behind the dialog, label
and slot components, `clsx`, `tailwind-merge`, and their transitive dependencies.

**All 43 are MIT, except three:** [`class-variance-authority`](https://github.com/joe-bell/cva)
(Apache-2.0), [`lucide-react`](https://github.com/lucide-icons/lucide) — the icon set — (ISC), and
[`tslib`](https://github.com/microsoft/tslib) (0BSD).

Tailwind CSS, TypeScript, Vite, Vitest, Playwright and ESLint are build and test tools: they produce
the bundle and are not part of it.

The components under `ui/src/components/ui/` are written in the idiom
[shadcn/ui](https://github.com/shadcn-ui/ui) established — Radix primitives styled with
`class-variance-authority`, copied into the tree rather than installed as a dependency. That is
shadcn/ui's own distribution model, and it is MIT.

## The muxer, which quince needs and does not ship

Every device call goes through a *muxer* — the daemon that owns the USB bus and the Wi-Fi
discovery. **The image contains none, deliberately:** only one may own the bus, so quince dials
whichever one the box already runs.

| Project | What it does here | Licence |
|---|---|---|
| [usbmuxd](https://github.com/libimobiledevice/usbmuxd) | The standard muxer. Devices on the cable | **GPL-2.0** |
| [netmuxd](https://github.com/jkcoxson/netmuxd) | The alternative for Wi-Fi devices, found over mDNS | LGPL-2.1 |

quince speaks to whichever is running over its socket, as any other client would, and distributes
neither — so these licences place no obligation on this repository. They are here because the
project does not work without one of them.

---

## The licence position, stated plainly

**quince's own code is MIT and links nothing copyleft.** The daemon is built `CGO_ENABLED=0` into a
static binary; there is no `import "C"` anywhere in `core/`, and no C source in this repository at
all. `idevicebackup2`, `idevicepair` and `ideviceinfo` are separate executables that quince builds
an argv for and runs, reading their output — so the LGPL library they use is never linked into
quince. That claim had been asserted in `docs/quince.stack.md` for a long time before anyone checked
it; it is now checked, and the way to re-check it is the `go list -deps` command at the top of this
file.

**The image is where the obligations are, and there are two.** It distributes patched LGPL-2.1
libimobiledevice binaries — met by the pinned tag, the in-tree patches, and the Dockerfile, as set
out above. And it distributes unmodified GPL-2.0 Alpine packages — met by Alpine's own published
sources at the versions the image records. Neither reaches quince's own code, and neither is
affected by the MIT licence on it.

If you think any of the above is wrong, please open an issue. Getting it right matters more than
being able to say it was checked.

---

## Not affiliated with Apple

quince is not endorsed by, sponsored by, or connected with Apple Inc. "iPhone", "iPad", "iOS" and
"Apple" are trademarks of Apple Inc., used here only to describe what the software interoperates
with.
