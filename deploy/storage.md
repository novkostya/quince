# quince storage — backends, the zfs hook, and offsite sync

Storage semantics are canon in [`../docs/quince.stack.md`](../docs/quince.stack.md) (D5/D5a) and
[`../docs/quince.design.md`](../docs/quince.design.md) (§5). This file is the operator-facing
deploy reference: the backend probe, the constrained ZFS hook, and the exact rclone filter block.

## Backends (auto-selected)

`storage.backend: auto` (the default) resolves at startup:

- **zfs** — chosen when `storage.zfs.parent_dataset` (or a `hook_cmd`) is set, or `backend: zfs`
  is explicit. Snapshot-native: one child dataset per device, versions are `@quince-*` snapshots,
  `latest/` is a materialized mirror rebuilt from the snapshot's `.zfs` path.
- **reflink** — the smart default where `/backups` supports FICLONE (Btrfs/Synology, XFS,
  hookless OpenZFS 2.2+). CoW clones, fully independent files, no host coupling.
- **hardlink** — for filesystems with neither reflink nor snapshots (ext4 NAS).
- **copy** — last resort (full copies, transient 2× space). A **degraded** mode: quince logs it
  loudly and surfaces it — never a silent fallback.

The chosen backend and *why* are logged at startup and shown in onboarding (qn.6).

## ZFS: `exec` vs `hook`

- `storage.zfs.mode: exec` — quince runs `zfs …` directly. Requires the container to hold ZFS
  delegation (`zfs allow`) or run privileged. Simplest where the daemon can reach `zfs`.
- `storage.zfs.mode: hook` — quince runs `storage.zfs.hook_cmd` (an argv, never a shell string),
  typically an SSH forced-command to a constrained helper on the ZFS host. This keeps the
  HTTP-facing container free of ZFS privileges (the hardened posture). The transport binary the
  `hook_cmd` names (usually `ssh`) must exist **where quince runs** — the container image needs an
  ssh client (e.g. `openssh-client`); the stock image ships without one.

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
#   and `mirror` (rebuild the DERIVED latest/ host-side — never a snapshot).
# Dataset destroy is intentionally NOT reachable.
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
  list)     case "$target" in "$PARENT"|"$PARENT"/*) exec zfs list -t snapshot -H -o name -r "$target" ;; esac ;;
  mirror)   # rebuild latest/ from working/ HOST-side (where FICLONE works even when the
            # container's unprivileged userns forbids it — gate-12 finding). Touches ONLY the
            # derived latest/ (rebuildable), NEVER a snapshot: bounded blast radius. Reports
            # SHARED/COPIED so quince can make an honest space claim (stack D5 (bi)/(bk)).
            case "$target" in "$PARENT"/*)
              mp=$(zfs get -H -o value mountpoint "$target") || exit 1
              [ -d "$mp/working" ] || { echo "no working/" >&2; exit 1; }
              rm -rf "$mp/latest.new"
              a0=$(zfs get -Hp -o value available "$target")
              cp -a --reflink=always "$mp/working" "$mp/latest.new"   # reflink under the job lock
              zpool sync "${PARENT%%/*}" 2>/dev/null || sync          # settle txg accounting
              a1=$(zfs get -Hp -o value available "$target")
              rm -rf "$mp/latest.old"; [ -d "$mp/latest" ] && mv "$mp/latest" "$mp/latest.old"
              mv "$mp/latest.new" "$mp/latest"; rm -rf "$mp/latest.old"   # atomic swap
              sz=$(du -sb "$mp/latest" | cut -f1); drop=$((a0 - a1))
              [ "$drop" -lt $((sz / 2)) ] && echo SHARED || echo COPIED   # pool-level sharing verdict
              exit 0 ;; esac ;;
esac
echo "quince-zfs-helper: refused: $SSH_ORIGINAL_COMMAND" >&2
exit 1
```

The `mirror` verb is the stack D5 ladder's tier (i): with a hook configured, quince delegates the
`latest/` rebuild to the host, where block cloning is not blocked by the unprivileged
user-namespace (gate-12 finding: in-container FICLONE returns `EPERM`). The verb touches only the
derived, rebuildable `latest/` — never a snapshot — so even a buggy verb cannot damage a canonical
version. It emits `SHARED`/`COPIED` so quince reports an honest space claim rather than assuming
zero-space. Hookless deployments fall through the in-container ladder (reflink → hardlink-under-
matrix → copy), reporting sharing UNVERIFIED where no measurement channel is available.

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
--filter "- /iphone-backup/*/work/**"
--filter "- /iphone-backup/*/versions/**"
```

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

The only non-atomic instant is the `latest/` swap itself (two renames); a walk crossing it can
briefly mix two *individually valid* versions — self-healed by the next run, revertible remotely
via bucket versioning.
