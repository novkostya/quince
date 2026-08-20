# Where backups go

Point quince at a disk and it works out the rest. For most people that is the whole story —
the rest of this page is for ZFS, and for copying backups offsite.

## What you get

quince looks at the filesystem behind `/backups` and picks how to keep old versions:

| your filesystem | how versions are kept | cost |
| --- | --- | --- |
| Btrfs, XFS, Synology, OpenZFS 2.2+ | copy-on-write clones | only what changed |
| ZFS, set up as below | snapshots | only what changed |
| ext4 | hardlinked trees | only what changed, files shared |
| anything else | full copies | **2× space per version** |

The last row is a fallback, not a choice — quince says so loudly at startup and on screen
rather than letting you find out from a full disk.

It tells you which one it picked and why, when you first set it up and in the log.

## ZFS

### Two settings on the parent dataset

Both are one command, both are silent to get wrong, and both cost you something real.

**1. Keep other snapshot tools off quince's datasets.**

```sh
zfs set com.sun:auto-snapshot=false <parent-dataset>
```

That property is `zfs-auto-snapshot`'s — sanoid, zrepl and `pve-zsync` each need their own
exclusion. Set it on the parent and every per-device child inherits it, now and later.

**This is about space, not tidiness.** Another tool's snapshot pins the same blocks as
quince's. Delete a quince version and nothing is freed: it reports the version removed and
reclaims nothing. It also breaks *reset* — `zfs rollback` refuses while a newer snapshot
exists, so a snapshotter firing every few minutes blocks it. quince names this setting when
that happens.

**2. Put the quota on the parent, not on each device.**

A per-device quota **will cost you a backup**. The backup tool asks how much room is free
before it starts, and it asks about the *parent* — so a quota on the child is invisible to
it. Measured on a real pool: with `quota=10G` on a child, the tool was told **1620 GiB** was
free. It then started a backup that could not fit and failed **part-way through** instead of
refusing up front, which over Wi-Fi costs hours.

### Reaching the pool

quince runs in a container; ZFS lives on the host. It connects over SSH to a small script
that only does the handful of things quince needs, so the container never holds ZFS
privileges. You give it four settings per storage — `ssh_user`, `ssh_host`, and optionally
`ssh_port` (default 22) and `ssh_key` (default `/data/keys/zfs`).

**Setting up that script is [`zfs-helper.md`](zfs-helper.md).**

### On Proxmox

A dataset created after the container starts shows up empty inside a plain bind mount. The
`zfs create` has to propagate through both hops — into the container
(`lxc.mount.entry: /pool-mount mnt/x none rbind,rslave,create=dir 0 0`) and onto the bind
(`propagation: rslave`, or `-v src:dst:rslave`). quince checks whether it worked and prints
the `pct set -mpN` command to fix it if not.

## Copying backups offsite

Run rclone over the whole storage directory and exclude two paths:

```
--filter "- /iphone-backup/*/working/**"
--filter "- /iphone-backup/*/versions/**"
```

Change `iphone-backup` to whatever quince's directory is called under your transfer root.

⚠ **The leading `/` matters.** Without it, `**/working/**` would also drop any folder called
`working` *inside* your actual backup content — silently corrupting the copy. quince generates
exactly these rules itself, and they are tested.

`versions/` is excluded because rclone cannot see that versions share data, so it would upload
every one at full size. Keep history locally; get remote history from bucket versioning or
`--backup-dir`.

**It is safe to run this while a backup is in progress.** A version only appears once it has
been verified, and it appears all at once, so a sync can never pick up half of one.

⚠ **Except on ZFS, where it is not safe and the filters above match nothing.** There, the
backup is written in place, so during a backup the directory holds a half-transferred tree —
and rclone would upload that as though it were a finished version, with nothing to tell you.
**Exclude a ZFS storage from a whole-disk rclone job** until quince can sync from snapshots
instead: [quince#735](https://github.com/novkostya/quince/issues/735).
