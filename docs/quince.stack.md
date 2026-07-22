# quince — stack decisions

> Every load-bearing tech choice, with the reasoning and the alternatives that lost.
> Implementing agents follow these as settled law; reopening one requires the Operator
> saying so in chat, and the change lands here first.
>
> Grounding: the hands-on feasibility lab (`../local/chatgpt-original-idea-chat.md`) proved the
> protocol path in the target environment — Alpine LXC on Proxmox, libimobiledevice 1.4.0,
> full encrypted USB backup (143k files) + Wi-Fi incremental via netmuxd (3.6 GB, ~9 min,
> `Backup Successful`), iMazing opens the result. Decisions below assume those facts.

---

## D1. Core daemon: Go

**Decision.** The long-running service — device tracking, backup jobs, storage backends,
HTTP/WebSocket API, UI hosting — is a single Go binary (`quince`).

**Why.**
- *Deployment reality:* open-source target includes weak NAS boxes (Synology). A static
  musl-friendly binary idles at ~30 MB RSS, cross-compiles to amd64/arm64 trivially, and
  produces a tiny container. Python service stacks cost more RAM at idle and drag a
  runtime into every image layer.
- *Robustness bias:* explicit error handling, static types, `go test -race`, and a flat
  concurrency model (goroutines supervising subprocesses, fanning out WS events) fit a
  daemon whose whole job is "never wedge, never lie about state". This is also the
  Operator's brother's argument, and he'll be a reviewer — house familiarity counts.
- *LLM authorship:* the project will be written by Claude (Opus). Go's small surface and
  enforced explicitness produce reviewable diffs; the race detector and `vet` catch what
  reviews miss.
- The riskiest integration — MobileBackup2 — is subprocess-shaped regardless of language
  (see D3), so Go sacrifices nothing there.

**Alternatives.** *All-Python (FastAPI)*: fastest start, native access to decryption
libs, Operator has some Python — but weakest on idle footprint, packaging, and
"discourages shitty code" for a 24/7 daemon. Rejected as the core, retained as the vault
(D4). *All-Go*: would mean re-implementing backup keybag decryption, violating the hard
requirement to reuse existing open-source decryption. Rejected.

## D2. Device layer: speak the muxd protocol natively; drive Apple ops via CLI subprocesses

**Decision.** **`netmuxd` (v0.4+) is the single muxer daemon for BOTH transports** —
the Operator identified, and its README confirms, that netmuxd outgrew its name: it
now talks to USB devices directly via `nusb` with *"no dependency on a separate
usbmuxd daemon"*, alongside its original mDNS Wi-Fi discovery. The core's muxd client
connects to its socket in `Listen` mode → attach/detach events push to the UI in real
time, with per-transport presence read from the muxer protocol's `ConnectionType`.
The client is written against *N* muxer sockets (config: `devices.*`), so alternate
topologies (classic `usbmuxd` for USB alongside netmuxd, or the hardened profile's
external socket) are configuration, not code changes. Pairing, device info, and
backups execute the proven libimobiledevice CLIs (`idevicepair`, `ideviceinfo`,
`idevicebackup2`) as argv subprocesses (never shell strings), pointed at the muxer via
`USBMUXD_SOCKET_ADDRESS`.

**Why.** Listen mode is what makes "iPhone appears → UI updates instantly" real — no
polling. One daemon for both transports collapses the merge complexity. The protocol is
small, documented, and has a reference Go implementation to crib from (go-ios, MIT).
The CLIs are exactly what succeeded in the lab; wrapping them keeps us on the paved
road and keeps pairing records (`/var/lib/lockdown`) compatible with the whole
libimobiledevice toolchain. go-ios itself is automation-focused (no MobileBackup2), so
it is a protocol reference, not a dependency.

**Honest risk + fallback (ruled 2026-07-19).** netmuxd's USB path is *young* (added in
the v0.4 line; v0.4.3 released 2026-07-14) versus usbmuxd's decades of hardening — and
USB is our reliability anchor. This is not hypothetical: **a backup-breaking failure in netmuxd's USB path is on
record from the feasibility lab (2026-07-13)**, exact line preserved in the lab log:

```
[2026-07-13T18:30:10Z WARN  netmuxd::usb::mux] dev=0 CONTROL ERROR: asyncReadComplete,
message was too large (65536 bytes, max = 65535)
```

— exactly one byte over a **u16** boundary in the USB mux read path: the signature of
a `0xFFFF` length-field/buffer guard meeting a `0x10000`-byte message, plausibly a
one-line fix. Any real backup trips it immediately (backup messages exceed 64 KiB at
once). As of 2026-07-19 nothing matching it is in netmuxd's issue tracker. Notably,
**v0.4.3 shipped the day after the observation** with the note "Fixes iTunes on the
Apple mux" — possibly this very bug, unconfirmed. Consequences:
- **Until netmuxd-USB is proven clean on real hardware, the configured default for USB
  is the two-daemon topology**: `usbmuxd` serves USB, netmuxd serves Wi-Fi. The daemon
  is `apk add usbmuxd` on **Alpine ≥ 3.24 community ONLY** (verified per-branch against
  the APKINDEX files, 2026-07-19: absent in 3.21–3.23) — hence the runtime base is
  Alpine 3.24 (`versions.env`). The qn.0 "no package on 3.21" finding was CORRECT; an
  intermediate architect claim of all-branch availability was the faulty one (apk's
  `--repository` flag *appends* to configured repos — it answered from the host's own
  3.24). Lesson: package-existence claims are verified against the branch's APKINDEX or
  in a clean container of that branch, never with `apk search --repository` on a
  configured host. Single-muxer netmuxd is the goal state, one config flip away.
- The netmuxd-USB audition on the pinned v0.4.3 **with real backup traffic** (which
  crosses the 64 KiB message boundary immediately — the same workload that failed on
  2026-07-13) now runs in **qn.7** (originally qn.2's lab gate, re-homed via qn.2b —
  decisions log (aw): it pairs with qn.7's netmuxd co-supervision, and `idevicepair
  unpair` destroys the lab pairing record, so it belongs in the dedicated hardening
  session; procedure preserved verbatim in the qn.2b spec, gate 8): clean → flip the
  default to single-muxer and credit the v0.4.3 mux fix; reproduces → file the upstream
  issue with the exact log line, and optionally carry a patch in our pinned source build
  (the same pattern as the qn.7 libimobiledevice timeout patch). Until then the
  two-daemon default stands — proven on hardware by qn.2b's supervised-usbmuxd gate.
- qn.4/qn.5 re-prove sustained full USB backup through whichever topology is default
  before it carries real data. Note the protocol floor
either way: initial pairing and enabling Wi-Fi sync require a USB *connection* — a
fresh device can never be adopted over Wi-Fi — so USB must work by the qn.6 gate
("fresh user pairs via UI"); netmuxd-first/Wi-Fi-first sequencing inside qn.2/qn.3 is
encouraged (the Operator's device is pre-paired; records in the lab CT).

**Verified netmuxd invocation (qn.4c, run against the shipped pinned v0.4.3 — not remembered).**
quince supervises netmuxd as
`netmuxd --host <h> --port <p> --socket-path <private> --disable-usb`:

- `--host/--port` from `devices.netmuxd_addr`, making the configured address authoritative (the
  same discipline as usbmuxd's `-S`).
- **`--socket-path` is a safety flag, not a preference.** netmuxd deletes and rebinds whatever
  unix socket that names, and its default is `/var/run/usbmuxd`. Reproduced in the built image:
  with usbmuxd running and serving, starting netmuxd on the default path logged `Deleting old
  Unix socket` and took it over — usbmuxd stayed alive with its socket inode gone, i.e. a
  **silent USB blackout**. quince gives netmuxd a private path and refuses to supervise it at all
  if that path would equal `devices.usbmuxd_socket`. (The feasibility lab hit the same class of
  accident by running `netmuxd --version`, which is not a flag, so it started normally.)
- `--disable-unix` was the alternative way to avoid the collision and was **rejected**: it puts
  netmuxd in "host mode", where it depends on another unix-mode daemon being alive — coupling
  Wi-Fi health to USB health, backwards for two transports that should fail independently.
- `--disable-usb` keeps this decision's two-daemon split real: without it both daemons claim the
  same USB device. It is the single flag the single-muxer flip removes after qn.7's audition.
- No `--plist-storage`: netmuxd reads `/var/lib/lockdown/<UDID>.plist`, the same pairing records
  quince already persists and restores (qn.3 amendment 1). `RUST_LOG=info` is injected when
  unset, since netmuxd is silent below `error`.
- **Wi-Fi discovery is mDNS-only**, so a supervised netmuxd is necessary but not sufficient: the
  container must be able to receive multicast from the LAN (`deploy/compose.nas.yml`).

**Consequence.** The container ships `netmuxd` (pinned, source-built) + `usbmuxd`
(apk, fallback) + `libimobiledevice-progs` from qn.0; the Wi-Fi hardening rung replaces
the stock libimobiledevice with a source-built one raising the 30 s service timeouts
(`src/service.c` — upstream issue #1413, reproduced in the lab) **before** the first
public release, since Wi-Fi is a primary transport (D13).

## D3. Backup engine: never-mutate-committed state machine around `idevicebackup2`

**Decision.** A backup job is a state machine
(`queued → waiting_for_device → preflight → backing_up → verifying → committing →
succeeded`, with `failed / cancelled / connection_lost` terminals) built on one
invariant: **a committed version is immutable — `idevicebackup2` only ever writes into
the backend's working area.** Verify is two-layered: *structural verification*
(automatic, passwordless — exit code AND `Backup Successful` in output AND
`Status.plist` sane AND `Manifest.db` opens read-only with required tables AND sampled
Manifest records point at existing blobs) runs on every job; *content verification*
(decrypt a canary file) runs on the user's next unlock and is recorded as
`content_verified_at`. The UI shows the levels honestly ("backup completed · structure
verified · decryption verified 3 days ago"). Commit follows a **journaled phase model**
(`prepared → version_promoted → latest_swapped → registry_committed`) with on-disk
markers, and **startup reconciliation is a first-class subsystem**: after any crash the
disk is the source of truth and every half-state has a defined repair (design §5).

**Why.** Verified-commit-or-discard turns a fragile protocol into a transaction, and
admitting that commit itself is multiple filesystem operations (rename + pointer swap +
registry write) is what makes crash recovery designable instead of accidental.

**Lab grounding.** MobileBackup2 over Wi-Fi drops sessions, cannot resume within a
file, and reports progress erratically (multi-minute silence is normal) — hence the
activity-sampler liveness with staged stall states (design §4), and never "resuming" a
torn session: recovery is always a fresh job, user-initiated per the assisted model
(D13 — no automatic retries exist). Never run two jobs for one UDID.

## D4. Vault: Python sidecar behind a language-neutral seam, reusing existing decryption

**Decision.** All encrypted-backup reading lives in `quince-vault serve` — a
session-scoped child process built on the open-source `iphone_backup_decrypt` family
(proven against our real backup in the lab). It receives the backup password on stdin,
unlocks the keybag, and answers JSON-RPC over stdio (list, stat, decrypt-file, domain
queries); it is killed on session lock/timeout, so keys live only in that process's
memory. There is no batch indexer: reading is lazy and session-scoped (D8).

**The seam is designed for replacement.** The core never knows the vault is Python: it
talks to a Go `vault.Vault` interface whose only current implementation is
process-over-stdio. The RPC protocol (`contracts.md` §4) is language-neutral and
versioned, and the vault's conformance test suite (golden requests/responses against
fixture backups) is the contract's executable form. A future all-Go vault — porting the
keybag/decryption logic as its own side project — drops in as a second implementation of
the same interface and must pass the same conformance suite. Nothing else changes.

**Why.** Reusing existing decryption is a hard requirement today, and Python is where
that ecosystem lives. Sidecar-as-child-process keeps the polyglot cost near zero and the
exit door open: the Operator explicitly wants the option to go all-Go later.

**Planned successor (ruled 2026-07-19).** The all-Go option is now an active parallel
project: an **independent Go library** (own public MIT repo) ports the decryption —
the reference implementation is small and frozen (last release 2024; the backup
encryption format has been stable since iOS 10.2), and every primitive has a mature Go
counterpart. When it's ready, `quince-vault` becomes a thin **Go binary on the same
stdio RPC** — subprocess boundary and key isolation deliberately kept (Operator
ruling), contracts §4 untouched. **Python remains the shipped implementation until the
Go vault passes the full conformance suite byte-for-byte**, including a differential
gate against the Python reference on the Operator's real backup. Bonus: the library's
test-only encrypt/builder side doubles as the synthetic-fixture generator qn.8 wants.
The suite's goldens are generated by the Python reference regardless of which
implementation ships.

## D5. Storage: two genuinely different version models — ZFS snapshot-native, or namespace-versioned

**Decision.** A `VersionBackend` implements `Seed / Commit / Discard / List / Delete /
Prune / Verify`, chosen by capability probe (`storage.backend: auto` overridable) — but
the backends do NOT share one layout, because ZFS and plain filesystems version
differently (Operator ruling: no hardlink games under ZFS):

1. **`zfs` — snapshot-native, one dataset per device** (Operator ruling: snapshot
   streams must be independent — versioning device A must never snapshot device B — and
   the per-device snapshot list is the version list). Layout inside each child dataset
   `<parent>/<udid>`:
   - `working/` — the in-place MobileBackup2 working copy (Apple-designed, most-tested
     write path, zero cleverness); honestly dirty mid-job; **never** a sync source.
     (Named `working/`, not `current/`, by Operator ruling: the name must scream
     "possibly dirty" to someone with zero context — `working`/`latest` reads
     correctly, `current`/`latest` does not.)
   - `latest/` — a consistent mirror of the last *verified* backup, rebuilt at commit
     and swapped atomically. **The snapshot is the canonical version; `latest/` is its
     materialized view**: built from the just-created snapshot's `.zfs` path (an
     immutable source even during a long clone/copy fallback), by the first mirror
     strategy that actually works — reflink → hardlink (safety-matrix-gated) → copy.
     **RESOLVED 2026-07-20 after a three-round investigation (decisions log
     (bf)→(bg)→(bh)→(bi)) — the zfs mirror uses a strategy ladder, because three
     distinct facts were established:** (1) *the pool clones fine* (host-side:
     `bclonesaved` +≈file-size, ALLOC flat); (2) *dataset-level accounting lies* —
     ZFS bills BRT clones like dedup (full size per reference in `used`/`du`), so
     sharing is verified ONLY at pool level (`zpool get bcloneused,bclonesaved`, or
     the `avail` delta reachable via the hook's `list` verb); (3) *the unprivileged
     user-namespace blocks FICLONE outright (`EPERM`)* — measured in the exact
     production shape (rpool child rbind'd into an unprivileged CT, and the OCI
     container inside it), so in-container reflink is unavailable in the
     recommended secure topology. **The mirror ladder (ruled (bi)):**
     (i) hook configured → a new constrained **`mirror` verb** runs the rebuild
     host-side, where FICLONE works (`cp -a --reflink=always` from `working/` under
     the job lock into a temp dir + atomic swap; constrained to children of the
     configured parent; touches only the DERIVED `latest/` — never snapshots — so
     even a buggy verb cannot damage canonical versions); (ii) no hook →
     in-container reflink attempt (covers privileged/bare-metal topologies; fails
     fast with `EPERM` in unprivileged ones, self-selecting down the ladder);
     (iii) hardlink-under-safety-matrix (gate 12c); (iv) copy — always correct,
     cost SURFACED (backend-selection string, commit log, health;
     ~full-backup-size write amplification per commit stated plainly), never
     silent. EXDEV-from-snapshot holds at every layer (kernel: separate
     superblock), so ALL sharing strategies clone from `working/` under the job
     lock, never from the `.zfs` mount. **Probe semantics (refined 2026-07-20, (bj), corrected (bk) — both
     Operator-challenged): the sharing measurement governs REPORTING plus exactly
     ONE selection edge.** Reflink outranks hardlink *when both share* because
     clones are independent while hardlinks alias (in-place mutation of `working/`
     would silently corrupt a hardlinked `latest/` — the matrix-gated risk), so
     the ladder orders by RISK dominance, not space. The one edge: a
     **measured-not-sharing** reflink falls through to hardlink-under-matrix
     (downgrade-for-space is allowed; upgrading into aliasing risk blindly is
     not). Absent any measurement channel, reflink wins on the risk asymmetry —
     its worst case is copy COST (reported "unverified"), hardlink's worst case
     is silent `latest/` corruption. The measurement otherwise decides what
     quince honestly *claims* ("zero-space verified" / "sharing unverifiable in
     this topology — budget full-copy cost" / "copy"). Measurement channels, best available: hook `list` avail-delta →
     delegated `zfs list -o avail` (exec mode) → syscall-only `statfs(2)`
     `f_bavail` delta around an incompressible test clone (works in any container;
     sync-and-settle for txg accounting lag) → none usable ⇒ report UNVERIFIED,
     never claim zero-space. This mirror exists for file-level offsite sync (D5a)
     — which is unchanged: pointing rclone at `.zfs` paths instead was considered
     and rejected (with `snapdir=hidden` rclone never sees them; with
     `snapdir=visible` it would walk EVERY snapshot at full size).
   A version IS a `zfs snapshot <parent>/<udid>@quince-<job>-<ts>`, taken **only after
   structural verification passes**, browsed read-only via that dataset's
   `.zfs/snapshot/`. Seed and Discard are no-ops; retention = destroying our own
   snapshots. **Only quince-created snapshots count** — host auto-snapshot tooling is
   never relied on, created, or classified. Host-side ops go through delegated exec or a
   constrained hook (forced-command SSH key allowing only: `snapshot`/`destroy`/`list`
   scoped to `@quince-*` snapshots, plus `create` of child datasets under the one
   configured parent; **dataset destroy is never in the key** — quince prints the exact
   host command for a human). Container-visibility caveat: a child dataset created after
   an LXC starts appears as an empty stub inside a plain bind mount (mount propagation).
   **Recommended PVE setup**: a raw `lxc.mount.entry: … none rbind,rslave,…` instead of
   a plain `mpX` — with rslave propagation, datasets created on the host appear in the
   running container live, no restart (the same file already carries the USB entries;
   the nested-OCI hop needs `bind: {propagation: rslave}` in compose — included in the
   examples). Provisioning still probes visibility empirically and, when propagation
   isn't available, prints the exact `pct set -mpN` + restart instructions. If child
   datasets are impossible in a setup, a documented single-dataset fallback mode exists
   (dataset-wide snapshots namespaced per device, with the space-accounting entanglement
   stated honestly).
2. **`reflink` — namespace-versioned via CoW file clones** (FICLONE): same layout as
   `hardlink` below, but clones are **fully independent files**, so the in-place-
   mutation hazard (and its destructive test matrix) does not exist here at all.
   Supported by Btrfs (Synology), XFS, bcachefs — and by OpenZFS 2.2+ itself, which
   makes `reflink` the graceful mode for ZFS **without** a host hook: full versioning
   inside the dataset, zero host coupling. **The smart default wherever the probe
   passes.**
3. **`hardlink` — namespace-versioned** for filesystems with neither reflink nor
   snapshots (ext4 NAS): guarded by the destructive safety matrix below.
4. **`copy`** — like `hardlink` but seeds by full copy (transient 2× space, retention
   defaults to latest-only) for filesystems without hardlinks.

For all three namespace backends (`reflink`/`hardlink`/`copy`), **`latest/` is a real
directory, never a symlink** (external-review point, accepted — symlink behavior under
rclone depends on flags and would make the offsite contract fragile): the newest
verified backup *lives* at `latest/`; commit rotates by rename pair — `latest/` →
`versions/<prev-ts>/`, then `work/<job>/` → `latest/` — journaled, same filesystem,
crash-repairable. `work/<job>/` is seeded from `latest/` (reflink or hardlink clone, or
copy).

**Auto-selection** (`storage.backend: auto`): explicit zfs intent in config
(`storage.zfs.parent_dataset`/hook set) → `zfs`; otherwise probe the actual `/backups`
filesystem at runtime — FICLONE a test file and verify independence → `reflink`; else
`link()` + inode identity → `hardlink`; else `copy`. Deterministic, logged, explained in
plain language during onboarding. All cloning happens in-process via the FICLONE ioctl
(`golang.org/x/sys/unix`), never by shelling out to `cp --reflink` — busybox userlands
are irrelevant, and the ioctl passes through container bind mounts to the real
filesystem, which is the only layer that must support it (host OpenZFS needs block
cloning enabled — probed, with a plain-language onboarding message when absent).

**D5a. The offsite-sync contract (the Operator's motivating case).** The offsite model
is **file-level sync of the whole storage tree** (rclone → B2 class tools, one cron job
over e.g. `/rpool/userdata` covering quince and everything else), which walks live
mounted filesystems and uploads whatever is there. The rule:

> **The live namespace always presents a consistent last-verified backup per device;
> working areas are excluded by one static filter rule.**

- `zfs`: rclone includes `<udid>/latest/` (the verified mirror — and with reflink
  builds, a backup running concurrently in `working/` cannot perturb it) and excludes
  the mutable/local trees with **anchored** filter rules, e.g. syncing `/rpool/userdata`:

  ```
  --filter "- /iphone-backup/*/working/**"
  --filter "- /iphone-backup/*/work/**"
  --filter "- /iphone-backup/*/versions/**"
  ```

  ⚠ Filters MUST be anchored (leading `/` = transfer root). An unanchored
  `--exclude "**/working/**"` would also silently drop any same-named directory *inside
  the backup content* under `latest/` — a corrupted offsite copy with no error. The
  deploy docs ship the exact filter block; `versions/` is excluded because rclone has
  no reflink/hardlink awareness and would upload every version at full size — local
  history stays local, remote history comes from B2 bucket versioning or
  `--backup-dir`. The operator's flow is literally
  `zfs snapshot -r … && rclone sync /rpool/userdata b2:…` — the snapshot for local
  restore points, `latest/` guaranteeing the upload is never torn.

  > **`PROPOSED (gap)` — the `latest` swap is NOT atomic, and this passage understated
  > it (Operator-found 2026-07-22; scoped to `qn.5b`).** This text called the swap "the
  > only nonatomic instant… could briefly mix two *individually valid* versions." That
  > is **too mild**. Both implementations do `mv latest → latest.old; mv latest.new →
  > latest` — the in-container Go path (`storage/zfs.go`) and the host-side hook's
  > `mirror` verb (`deploy/storage.md`) — each commented "atomic swap," neither atomic.
  > Between the two renames **`latest/` does not exist at all**. The real failure modes
  > are therefore: (1) `rclone sync` crossing the window sees `latest/` missing and
  > **DELETES the remote copy at B2** (sync mirrors deletions — not a "mix," a wipe
  > followed by a full re-upload); (2) a `zfs snapshot` landing there captures a version
  > with **no `latest/`**. B2 versioning makes it recoverable, not harmless. The window
  > is two renames wide, but the Operator's requirement is explicitly "safe at ANY
  > instant," and a cron running for months will eventually land in it. The fix this
  > passage already gestures at — **exchange-rename** (`renameat2(RENAME_EXCHANGE)`,
  > which never leaves the name unoccupied) — was never implemented. Note the privilege
  > split favours us: only FICLONE needs the host, so the hook keeps doing the reflink
  > into `latest.new` while **quince does the exchange in-container** (rename needs no
  > privilege). `RENAME_EXCHANGE` support on ZFS is an **interface fact to verify live**,
  > not assume; the symlink workaround stays forbidden (D5a: `latest/` is a real
  > directory). Full scope + the `working/`-lifecycle redesign it belongs with: qn.5b,
  > decisions log (cg).

  Push-style alternative: the post-commit hook (parked) runs rclone
  right after each verified commit.
- `reflink`/`hardlink`/`copy`: `latest/` is a real immutable-between-commits directory —
  same include rule, same anchored filter block (minus `working/`).
- Snapshot-stream replication (syncoid) of zfs datasets is also safe at any instant: a
  mid-backup pass ships a dirty `working/` *plus* every `quince-*` restore point and a
  consistent `latest/`.

Restore/browse never read `working/`. A torn `working/` normally needs no repair — the
lab showed MobileBackup2 continues from torn state, and every result re-passes full
structural verification — with `quince device repair-working-copy <udid>` (rebuild
`working/` from the last good snapshot) as the explicit escape hatch if dirty-state
incrementals repeatedly fail; the UI reports "working copy dirty, last good version =
<ts>" meanwhile. The `quince versions path --latest <udid>` CLI prints the mirror path
(or a specific version's path) for scripts that want a single-device source.

**Why.** The Operator's ruling makes the zfs backend *simpler and more robust* than the
previous hardlink+snapshot hybrid: the write path is exactly what Apple ships, versioning
rides on CoW instead of 143k directory entries per job, and the hardlink-safety
hypothesis (below) stops applying to ZFS at all. The crosscheck's alternative —
per-version/clone child datasets — is rejected: dynamically created datasets don't
propagate into an LXC/container bind mount (mount namespaces are private), and it turns
one host-side operation (snapshot) into fragile clone/promote/rename chains over a
constrained channel.

**Verified assumption (early destructive gate — wherever hardlinks are actually used:
the `hardlink` backend, and hardlink *fallbacks* of the reflink/mirror machinery).** The
hardlink scheme assumes MobileBackup2 replaces files rather than mutating them in place;
every reflink-built tree is exempt (independent files). qn.5
proves this with a **destructive lab matrix**, not one file: byte- and metadata-identity
of the previous version across full→incremental, big-file change, SQLite `-wal`/`-shm`
companions, deletions, renames, interrupted backup + the incremental after it, iOS
upgrade, and encryption-settings change (truncate/chmod/xattr traps included). Any
in-place-mutating file class is copied instead of linked. The matrix re-runs manually
after every libimobiledevice upgrade (release checklist).

App state (job history DB, caches, pairing records, logs) lives **outside** the backup
dataset in every model.

## D6. API: REST for commands, one WebSocket for events

**Decision.** JSON REST (`/api/...`) for CRUD and commands; a single `/api/ws` WebSocket
pushing typed events (device attach/detach, job state + progress, log chunks, snapshot and
index updates). No gRPC.

**Why.** The responsiveness requirement is server→browser push; WS covers it with zero
proxy friction and trivial browser support. gRPC-web needs a proxy layer and buys nothing
for a single-user LAN app. Protocol shapes are pinned in `contracts.md` so UI and core
tracks can build in parallel.

## D7. Frontend: maximally mainstream — React + Vite + TS, Tailwind, vendored shadcn-style components, Zustand

**Decision** (revised on Operator ruling: mainstream, highly maintainable, lightweight,
strong LLM fluency — no niche dependencies):
- React 19 + Vite + TypeScript.
- **Tailwind CSS v4** with the design tokens as CSS variables in the theme — the
  mercury *idiom* (semantic tokens, light/dark one variable deep) carried over without
  the dependency; mercury remains a taste reference only, `@mercury-fx/ui` is not
  consumed.
- **Components vendored, shadcn/ui-style**: accessible primitives from Radix UI, styled
  copies living in our repo — we own the code, no component-library version churn, and
  it is the pattern current LLMs author most fluently. lucide icons.
- State: **Zustand** stores fed by one WebSocket bridge; **TanStack Query** for REST
  fetching/caching; TanStack Virtual for unbounded lists. Effector is dropped — good
  library, but niche enough to be an onboarding barrier for open-source contributors
  and a weaker LLM path than the boring mainstream trio.
- Built assets embedded in the Go binary (`go:embed`) — one artifact serves everything.

**Why.** Every piece is the most-trodden path in its slot (huge ecosystems, hiring/
contributor familiarity, deep LLM training coverage), the bundle stays light, and
nothing here can be abandoned upstream in a way that strands us — the components are
ours, and Tailwind/Radix/Zustand/TanStack are as durable as frontend deps get.

## D8. Persistence: SQLite app DB; backup reading is lazy and session-scoped

**Decision.** One app database (devices, jobs, versions registry, settings, sessions) via
`modernc.org/sqlite` (no cgo), WAL mode — this records what *quince did*, never mirrors
backup content. Backup content is read **lazily inside an unlocked session**: the vault
decrypts `Manifest.db` (and domain DBs like `sms.db` on first use) into session scratch,
queries run against those live copies, and lock/timeout wipes it all. No persistent index
of backup content — the backup dataset is external storage the user may prune, replicate,
or hand-edit, and a stored index *will* diverge from it (Operator-raised concern; agreed).

**The one exception: derived caches, fingerprint-validated.** Some artifacts are too
expensive to rebuild per session on NAS hardware (photo thumbnails above all; possibly
FTS shards for huge message stores). These may be cached in `/cache` under strict rules:
keyed by immutable version identity + `Manifest.db` hash; validated before every use;
silently dropped and rebuilt (or absent) on mismatch or missing source; wipeable at any
time with zero correctness impact. A cache is a lie-proof derivation or it doesn't exist.
Message search default: FTS built in session scratch on first search.

**Why.** Single-user scale; zero-ops; cgo-free keeps cross-compilation clean. Lazy-first
also removes the only feature that wanted a stored backup password (post-backup batch
indexing) — v1 keeps no secrets at rest, full stop.

## D9. Quality bar: comprehensive tests, race-clean, fixtures from real transcripts

**Decision.**
- Go: unit tests + `go test -race` everywhere; integration tests run the real state
  machine against a **fake `idevicebackup2`** (scripted binary replaying real stdout
  transcripts captured in the lab, including the pathological ones: 30 s stalls, `-4`
  disconnects, silent minutes) and a **fake muxd socket** (record/replay of the plist
  protocol).
- Python: pytest against fixture backups; a fixture *generator* (tiny synthetic encrypted
  backup with known password) is its own rung — until it lands, decryption integration
  tests run against a real backup on the Operator's lab box (a non-CI gate).
- Frontend: vitest for logic, Playwright against `quince serve --demo` (fixture data, no
  device) — the demo mode doubles as the public screenshot/demo story.
- Live E2E (real iPhone, real LXC): a documented manual checklist per release, not CI.

**Why.** The core's whole value is reliability; the lab transcripts are a free corpus of
real failure modes. Demo mode keeps UI development unblocked by hardware.

## D10. Delivery: GitHub Actions → multi-arch images + releases

**Decision.** CI on every PR: lint (golangci-lint, ruff+mypy, eslint/tsc) + all test
suites. On tag: goreleaser builds binaries, buildx builds `linux/amd64 + linux/arm64`
images pushed to `ghcr.io` (Docker Hub mirror optional later), GitHub Release with
changelog. During pre-public development the same image target pushes to the Operator's
LAN registry via `make image push REGISTRY=...` (registry/creds via env, never committed).
Base image: Alpine; ships usbmuxd, netmuxd (pinned, built from source in a CI stage),
libimobiledevice-progs (later: patched-timeout build), python3 + locked venv (uv).

**Why.** Standard, boring, reproducible; multi-arch is what makes the Synology story real.

## D11. Language/toolchain versions & conventions

- Go: latest stable (1.24.x at writing), `golangci-lint` pinned config, no cgo.
- Python: 3.12+, `uv` for env+lock, `ruff` + `mypy --strict`, `pytest`.
- Node: 22 LTS, `pnpm`, workspace mirroring mercury conventions where sensible.
- Monorepo layout: `core/` (Go), `vault/` (Python), `ui/` (React), `deploy/`
  (Dockerfile, compose examples), `docs/`.
- Licenses: MIT for quince; all Apple-protocol heavy lifting stays in subprocesses
  (libimobiledevice is LGPL — invoked, not linked). A license audit is part of the first
  public release rung.

## D12. Operations UX: Plex-grade setup, OpenWrt-grade config

**Decision.** Getting started is copy-paste-compose → `compose up` → open the web UI —
first-run onboarding (set admin password, guided checks: backups dir writable, backend
probe explained, usbmuxd reachable) handles the rest in-app. Only deployment topology
lives in env (`QUINCE_DATA/CACHE/BACKUPS`, `QUINCE_LISTEN`); **every other setting
lives in one tidy, hand-editable file** (`/data/config.yml`) that the UI *edits* rather
than replaces as the source of truth:

- canonical key order, every key annotated with a generated doc-comment;
- atomic validated writes; manual edits picked up by file watch — an invalid edit never
  crashes the app (keep running on last-good, show a UI banner naming the bad key);
- deterministic regeneration on UI saves (user's own comments aren't preserved — the
  OpenWrt/PVE precedent, stated honestly);
- **no secrets in the config file, ever** (admin password hash lives in the app DB) — the
  file is safely diffable, shareable, and versionable;
- `quince config validate` for pre-flight in scripts/CI.

**Why.** The Operator's litmus test: PVE and OpenWrt earn love because their config is
transparent files you can read, edit, and diff; OPNsense got deleted within an hour for
burying state in an opaque GUI. Plex is the setup bar: nothing to learn before the UI is
up. Both properties at once — GUI-first onboarding, file-first truth.

**Staged delivery** (external-review point, accepted): the full subsystem is the
destination, not the qn.1 payload. qn.1 ships the load-bearing core — typed config,
YAML as source of truth, atomic canonical writes, `config validate`, a small Settings
page for safe keys, restart-required for the rest. File-watch live reload, generated
doc-comments, and the full transparent-editor UX land with qn.6 (the release gated on
onboarding anyway). The contract (file-first, no secrets, no UI-only state) binds from
day one.

## D13. Wi-Fi is a first-class transport — and the product model is ASSISTED backup

**Decision.** Wi-Fi backup is not an experimental extra; it is **the product's primary
use case** (Operator ruling, overriding external review advice to defer it). But there
is no such thing as an unattended backup on modern iOS: **starting an encrypted backup
requires the passcode to be entered on the device** (Operator-established; the lab
transcripts show `*** Waiting for passcode ***` on every run). So the automation model
is *assisted*, not *scheduled*:

```
phone goes on the charger → Shortcut sends an opportunity signal to quince
quince decides server-side: device visible on Wi-Fi? no job running? last good
backup stale? not nagged recently?  → if warranted, one push notification
user unlocks the phone, confirms, enters the passcode → backup runs over Wi-Fi
```

Consequences:
- **No auto-retry.** The old 1 → 5 → 20 min ladder is deleted — a retry would hang at
  the passcode prompt. A failed/torn job becomes an honest `user action required` state
  with a push explaining why; the retry is one tap. A `retry_of` link ties the new job
  to the failed one in history.
- The supervisor detects the passcode-wait phase from `idevicebackup2` output and
  surfaces it (`waiting_for_passcode`) — the liveness clock pauses there (the user may
  take minutes).
- USB remains required for initial pairing and preferred automatically when plugged in
  (faster); default transport policy is `auto`. No experimental flag.
- Flakiness is still absorbed by engineering, not flagged away: the timeout-patched
  libimobiledevice build lands before v0.1 (D2); the lab's torn-session transcripts are
  permanent replay fixtures; liveness is activity-based with staged states (design §4).
- v0.1 gate: a week of *real* Wi-Fi backups driven from the UI (phone in hand, zero
  cable, zero tmux), with failures producing honest actionable states. The full
  assisted-flow acceptance list (opportunity → push → one-tap start; no spam; correct
  no-op on fresh backups) is the qn.12 gate, once push exists.

**Why.** The value proposition is not "set and forget" (Apple forbids it) — it is
**"quince notices the right moment, reminds you, and shrinks the ritual to an unlock
and one confirmation."** That's still a decisive win over cable + desktop app, and it's
exactly why Wi-Fi must be in the core path: without it the assisted flow doesn't exist.

## Settled non-goals (v1)

- No multi-user/multi-tenant; single admin password, cookie session, LAN/reverse-proxy
  deployment assumed.
- No backup *restore* orchestration in v1 (export a snapshot in Finder/iMazing-compatible
  form instead; restore is a later epic).
- No iCloud anything.
- No attempt to resume torn MobileBackup2 sessions.
