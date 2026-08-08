# quince storage — backends, the zfs hook, and offsite sync

Storage semantics are canon in [`../docs/quince.stack.md`](../docs/quince.stack.md) (D5/D5a) and
[`../docs/quince.design.md`](../docs/quince.design.md) (§5). This file is the operator-facing
deploy reference: the backend probe, the constrained ZFS hook, and the exact rclone filter block.

## Backends (auto-selected)

`storage.backend: auto` (the default) resolves at startup:

- **zfs** — chosen when `storage.zfs.parent_dataset` (or a `hook_cmd`) is set, or `backend: zfs`
  is explicit. Snapshot-native: one child dataset per device, versions are `@quince-*` snapshots.
  qn.6h: **the backup tree IS the child dataset root.** `idevicebackup2`'s target is the parent
  dataset and it appends the device's UDID itself, so a backup lands at `<parent>/<udid>/Info.plist`
  and friends — no `latest/`, no working copy, and no clone before the transfer can start. Commit is
  verify → `zfs snapshot`, and the snapshot IS the version. Between backups the dataset holds only
  the backup and quince's one marker file.
- **reflink** — the smart default where `/backups` supports FICLONE (Btrfs/Synology, XFS,
  hookless OpenZFS 2.2+). CoW clones, fully independent files, no host coupling.
- **hardlink** — for filesystems with neither reflink nor snapshots (ext4 NAS).
- **copy** — last resort (full copies, transient 2× space). A **degraded** mode: quince logs it
  loudly and surfaces it — never a silent fallback.

The chosen backend and *why* are logged at startup and shown in onboarding (qn.6).

## ZFS: REQUIRED SETUP — two settings on the parent dataset

Both are one command, both are on the **parent**, and both are silent to get wrong.

**1. Exclude quince's datasets from whatever snapshotter this host runs.**

```sh
zfs set com.sun:auto-snapshot=false <parent-dataset>
```

That property is `zfs-auto-snapshot`'s. **sanoid, zrepl and `pve-zsync` each need their own
exclusion** — the instruction is *exclude quince's datasets from whatever snapshotter you run*, and
the command above is the worked example. Setting it on the parent covers every per-device child, now
and in future, because ZFS user properties inherit.

**The reason is space, not tidiness.** A snapshot taken by another tool alongside a `@quince-*` one
**pins the same blocks**. Destroy the quince snapshot and nothing is freed: quince's retention runs,
reports versions removed, and reclaims no space. There is a second effect worth knowing — `zfs
rollback` refuses while any newer snapshot exists, so an automatic snapshotter firing every few
minutes makes *reset* refuse too. Reset says so and names this setting when it happens.

**2. Set the quota on the parent dataset, NOT per device.**

**A per-device quota is UNSUPPORTED and will cost you a backup.** `idevicebackup2` asks the
filesystem how much space is free before it starts, and since qn.6h its target is the **parent**
dataset — so a quota on the child is invisible to that question. Measured on a real pool,
2026-08-08: with `quota=10G` on a child, the tool is told **1620 GiB** is available. It then starts a
backup that cannot fit, and the failure arrives as **ENOSPC part-way through** rather than as a clean
up-front refusal — which on Wi-Fi costs hours.

## ZFS: `exec` vs `hook`

- `storage.zfs.mode: exec` — quince runs `zfs …` directly. Requires the container to hold ZFS
  delegation (`zfs allow`) or run privileged. Simplest where the daemon can reach `zfs`.
- `storage.zfs.mode: hook` — quince runs `storage.zfs.hook_cmd` (an argv, never a shell string),
  typically an SSH forced-command to a constrained helper on the ZFS host. This keeps the
  HTTP-facing container free of ZFS privileges (the hardened posture). The transport binary the
  `hook_cmd` names (usually `ssh`) must exist **where quince runs**: the runtime image ships
  `openssh-client` for exactly this (qn.4a gate-15 finding #2) — without it every hook call dies
  with `exec: "ssh": executable file not found` and no backup can seed. An `exec`-mode hook that
  shells out to some other transport must ensure that binary is present too.

### The constrained `quince-zfs-helper` (forced-command reference)

On the ZFS host, add a **dedicated** SSH key whose `authorized_keys` entry forces this helper —
quince can then only snapshot/destroy/list `@quince-*` and create child datasets under the one
configured parent. **Dataset destroy is deliberately impossible via this key** — quince prints
the exact `zfs destroy <dataset>` command for a human instead.

`authorized_keys` (one line):

```
command="/usr/local/sbin/quince-zfs-helper",no-port-forwarding,no-agent-forwarding,no-pty,no-X11-forwarding ssh-ed25519 AAAA... quince
```

`/usr/local/sbin/quince-zfs-helper` (the parent dataset is baked in here, not taken from the
client — the client cannot escape it):

```sh
#!/bin/sh
# Constrained ZFS helper for quince. Allows ONLY:
#   snapshot|destroy|list on @quince-* snapshots under $PARENT, create of children of $PARENT,
#   and `seed` (clone latest/ → working/<udid> host-side — the mutable work area, never a snapshot).
# Dataset destroy is intentionally NOT reachable.
# qn.5b MIGRATION: the old `mirror` verb (rebuild latest/ from working/) is REPLACED by `seed`
#   (clone latest/ → working/<udid>) — the reflink moved from commit-time to job-start. The atomic
#   latest/ swap is now an in-container renameat2(RENAME_EXCHANGE) done by quince (no privilege).
#   Operators upgrading MUST replace the mirror) case below with the seed) case.
set -eu
PARENT="pool/path/to/iphone-backup"   # <-- set to your storage.zfs.parent_dataset
CTUID=0   # container's mapped root uid: 0 for privileged/native; the userns base (e.g. 100000)
          # when quince runs in an UNPRIVILEGED LXC — else the create chown below is a no-op fix.
set -- $SSH_ORIGINAL_COMMAND
op="${1:-}"
# The dataset/snapshot is the LAST arg, not $2: quince sends flags BEFORE it — `create -p <ds>`,
# `list -t snapshot -H -o name -r <ds>` — so $2 is a flag and $2-based matching REFUSES those verbs.
target=""; for a in "$@"; do target="$a"; done
case "$op" in
  create)   case "$target" in "$PARENT"/*)
              zfs create -p "$target" || exit 1
              # host root owns the new dataset; when quince runs in an unprivileged-userns container
              # its mapped root can't write the root-owned mountpoint — chown so working/ is writable.
              chown "$CTUID:$CTUID" "$(zfs get -H -o value mountpoint "$target")"
              exit 0 ;; esac ;;
  snapshot) case "$target" in "$PARENT"/*@quince-*) exec zfs snapshot "$target" ;; esac ;;
  destroy)  case "$target" in "$PARENT"/*@quince-*) exec zfs destroy "$target" ;; esac ;;  # snapshot only (has '@')
  rollback) # qn.6h ABANDON: return the device dataset to its newest @quince-* snapshot. NO -r, and
            # none can reach here — the parse above discards every flag. Without -r, `zfs rollback`
            # refuses any snapshot but the most recent, and -r/-R are what destroy NEWER snapshots,
            # i.e. committed versions. So this verb structurally cannot lose one.
            case "$target" in "$PARENT"/*@quince-*) exec zfs rollback "$target" ;; esac ;;
  list)     case "$target" in "$PARENT"|"$PARENT"/*) exec zfs list -t snapshot -H -o name -r "$target" ;; esac ;;
  seed)     # qn.5b: clone latest/ → working/<udid> HOST-side (where FICLONE works even when the
            # container's unprivileged userns forbids it — gate-12 finding), then chown it so the
            # in-container idevicebackup2 can WRITE it and quince can EXCHANGE it. Touches ONLY the
            # mutable working area, NEVER a snapshot or the committed latest/: bounded blast radius.
            # Reports SHARED/COPIED so quince makes an honest space claim (stack D5 (bi)/(bk)).
            case "$target" in "$PARENT"/*)
              mp=$(zfs get -H -o value mountpoint "$target") || exit 1
              [ -d "$mp/latest" ] || { echo "no latest/ to seed from" >&2; exit 1; }
              udid=${target##*/}
              rm -rf "$mp/working/$udid"; mkdir -p "$mp/working"
              # Chown the PARENT only: mkdir makes it root-owned, but `cp -a` below PRESERVES
              # latest/'s ownership (already the container uid), so a recursive chown is redundant —
              # and it is NOT free: it re-walks every file (measured 4.7 s on a 133k-file tree, and
              # it grows with file count). Hardware finding (cs).
              chown "$CTUID:$CTUID" "$mp/working"
              a0=$(zfs get -Hp -o value available "$target")
              cp -a --reflink=always "$mp/latest" "$mp/working/$udid"   # reflink seed under the job lock
              zpool sync "${PARENT%%/*}" 2>/dev/null || sync            # settle txg accounting
              a1=$(zfs get -Hp -o value available "$target")
              sz=$(du -sb "$mp/working/$udid" | cut -f1); drop=$((a0 - a1))
              [ "$drop" -lt $((sz / 2)) ] && echo SHARED || echo COPIED # pool-level sharing verdict
              exit 0 ;; esac ;;
  capacity) # qn.6d: the storage card's free-of-total. NO caller argument reaches zfs — the verb
            # takes none, and $PARENT is the helper's own. That makes it TIGHTER than the arms
            # above, which accept a pattern-guarded $target.
            exec zfs list -H -p -o used,available "$PARENT" ;;
esac
echo "quince-zfs-helper: refused: $SSH_ORIGINAL_COMMAND" >&2
exit 1
```

**⚠️ MIGRATION — operators upgrading MUST add the `capacity)` case above**, the same way the header
records the `qn.5b` `mirror)` → `seed)` replacement. Without it every zfs storage card reads *"free
space unavailable"* and the daemon logs `capacity unavailable on a reachable storage — omitted`.
Nothing else breaks: backups, commits, snapshots and retention are untouched, and quince omits the
number rather than showing a wrong one — which is why this is a migration note rather than a
release blocker.

**`Test helper` in the UI now TELLS YOU whether you did this** (`qn.6e`). Adding a storage on the
zfs backend fires two of the arms above — `capacity` (no argument) then `list <your parent dataset>`
— and distinguishes four states rather than working/not-working:

| what happens | what it means |
| --- | --- |
| both answer | the key, the forced command and the parent all line up |
| `capacity` refused, `list` answers | **you have not applied the migration above** |
| `capacity` answers, `list` refused | the dataset you typed is not the `PARENT` set in this script |
| neither answers | the key, the forced command in `authorized_keys`, or the host |

**An empty answer from `list` is SUCCESS, not a failure.** It returns the `@quince-*` snapshots
under the parent, and a storage with no backups yet has none — so a correct, freshly-installed
helper answers with nothing at all.

Both verbs are read-only and path-guarded, which is why quince is willing to fire them from a form.
Nothing quince sends can create, destroy or write, and that is a property of the `case` arms above
rather than of quince's restraint — which is the point of a forced command.

**Why a new verb rather than letting `list` take flags** — Operator ruling 2026-08-03 (quince#600).
quince first shipped this read as `list -H -p -o used,available "$PARENT"`, which assumes the
helper forwards argv to `zfs`. **It does not, and that is the entire point of a forced command.**
The `list` arm runs a fixed `zfs list -t snapshot`, so the call returned the *snapshot list* at
**exit 0** — a succeeded command with wrong-shaped output, which is why it survived a release.
Teaching `list` to forward flags was the tempting fix and was refused: the same key would then take
arbitrary `zfs list` arguments, and *"dataset destroy is intentionally NOT reachable"* would stop
being checkable by reading these five case arms.

**The `seed` verb is DEAD CODE ON THE HOST as of qn.6h and quince never calls it.** There is no
job-start clone on this backend any more — the tool writes into the dataset root — so the paragraph
below describes a verb that still sits in your script and is never invoked. Removing it from the
helper (and this text with it) is a one-line follow-up, deliberately kept separate from the code
change so the host edit is not required on the same day.

The `seed` verb (qn.5b, replacing `mirror`): with a hook configured, quince delegates the job-start
clone of `latest/` → `working/<udid>` to the host, where block cloning is not blocked by the
unprivileged user-namespace (gate-12 finding: in-container FICLONE returns `EPERM`). The verb
touches only the mutable, rebuildable `working/` area — never a snapshot, never the committed
`latest/` — so even a buggy verb cannot damage a canonical version. It emits `SHARED`/`COPIED` so
quince reports an honest space claim rather than assuming zero-space. **Sizing (hardware-measured,
(cs)):** the seed is **O(file count + blocks cloned)**, never O(1) — reflink makes it *space*-free,
not *time*-free, because every file still costs `open`+`create`+`FICLONE`+metadata. A 133k-file /
34 GB device clones in ~17.5 s warm (~7.6k files/s) and takes longer cold or when a previous
`working/` must be removed first. quince therefore bounds the seed with its own generous
`zfsSeedTimeout`, **not** the 60 s metadata-op timeout — reusing that one SIGKILLed a real 34 GB
seed mid-clone. Budget minutes for large devices. **The atomic `latest/` swap
itself is NOT in the hook** — at commit quince does an in-container `renameat2(RENAME_EXCHANGE)`
(working/<udid> ⇄ latest/, no privilege, no window) and then the hook `snapshot`. Hookless
deployments fall through the in-container seed ladder (reflink → copy; **never hardlink** — a
hardlink seed would alias the committed `latest/`, gate 12c), reporting sharing UNVERIFIED where no
measurement channel is available.

Then `storage.zfs.hook_cmd: "ssh -i /data/keys/zfs -o BatchMode=yes zfsuser@zfshost"` (the helper
runs regardless of the command text; quince appends the operation + target as argv).

Child-dataset visibility: a dataset created after the container starts appears as an empty stub
inside a plain bind mount. The host `zfs create` must propagate through **both** hops — into the
LXC (`lxc.mount.entry: /pool-mount mnt/x none rbind,rslave,create=dir 0 0`, which becomes
slave+shared when the host mount is `shared`) **and** onto the OCI bind (`propagation: rslave` /
`-v src:dst:rslave`) — so the new child mounts live at `/backups/<udid>` in the container
(design §5). quince probes visibility and prints `pct set -mpN` fallback instructions when
propagation is absent.

## Offsite sync (D5a) — the anchored filter block

The offsite model is a **whole-tree** rclone job over the storage parent that walks live mounts.
The live namespace always presents a consistent last-verified `latest/` per device; the mutable
and local-only areas are excluded by **anchored** filter rules. Ship this block verbatim (adjust
`iphone-backup` to quince's directory name under your transfer root):

```
--filter "- /iphone-backup/*/working/**"
--filter "- /iphone-backup/*/versions/**"
```

(qn.5b dropped the old per-job `work/<job>/` dir — the mutable in-progress tree is now
`working/<udid>/`, still covered by the anchored `working/**` rule.)

⚠ **The leading `/` (anchor) is load-bearing.** An unanchored `--exclude "**/working/**"` would
also drop any directory named `working` *inside* backup content under `latest/`, silently
corrupting the offsite copy. quince's `storage.AnchoredFilterRules` emits exactly these rules and
`storage.PathExcluded` proves their semantics in CI; the real `rclone` binary is exercised in the
qn.5 lab gate.

`versions/` is excluded because rclone has no reflink/hardlink awareness and would upload every
version at full size — local history stays local; remote history comes from B2 bucket versioning
or `--backup-dir`. The operator's flow is:

```
zfs snapshot -r pool/path/to/iphone-backup@offsite-$(date +%s)   # local restore point (zfs backend)
rclone sync /pool/path b2:bucket/quince <the three --filter lines above>
```

**There is no non-atomic instant (qn.5b) — ON THE reflink / hardlink / copy BACKENDS.** There
`latest/` changes only by a single `renameat2(RENAME_EXCHANGE)`, so it is never unoccupied and a walk
crossing a commit always sees a complete `latest/`, never a missing one. This replaced the old
two-rename swap, whose window an `rclone sync` could cross and mirror as a **deletion** of the remote
copy (the stack-D5 `PROPOSED (gap)`, decisions (cg)). Between backups the device dir holds only
`latest/` (the per-job `working/` exists only during/after a backup, and is rclone-excluded).

⚠ **ON ZFS SINCE qn.6h THAT GUARANTEE IS GONE, AND THE FILTER RULES ABOVE MATCH NOTHING.** The backup
tree is the dataset root, so there is no `working/`, no `versions/` and no `latest/` — the whole
device dataset is in scope for a whole-tree walk, and **during a backup it is a half-transferred
tree**. An rclone job crossing it uploads that as though it were a verified version, and it fails
silently from the operator's side.

**So a zfs storage must be EXCLUDED from a whole-host rclone job until the snapshot-sourced offsite
path exists** — that is [quince#735](https://github.com/novkostya/quince/issues/735), which reads
`.zfs/snapshot/<snap>/` instead of the live tree. The cost was accepted knowingly when the in-place
shape was ruled; it is stated here rather than left to be discovered.
