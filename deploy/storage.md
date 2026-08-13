# quince storage — backends, the zfs hook, and offsite sync

Storage semantics are canon in [`../docs/quince.stack.md`](../docs/quince.stack.md) (D5/D5a) and
[`../docs/quince.design.md`](../docs/quince.design.md) (§5). This file is the operator-facing
deploy reference: the backend probe, the constrained ZFS hook, and the exact rclone filter block.

## Backends (auto-selected)

`storage.backend: auto` (the default) resolves at startup:

- **zfs** — chosen when `storage.zfs.parent_dataset` (or an `ssh_user`/`ssh_host` pair) is set, or `backend: zfs`
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

## ZFS: the hook is the only transport

`storage.zfs.mode: hook` is the only value and the default — quince reaches an SSH forced-command to
a constrained helper on the ZFS host. This keeps the HTTP-facing container free of ZFS privileges.

**QUINCE COMPOSES THE SSH COMMAND ITSELF** since quince#818 — Operator ruling, relayed at
<https://github.com/novkostya/quince/issues/818#issuecomment-5245496176> — from four
per-storage keys: `ssh_user`, `ssh_host`, and optionally `ssh_port` (default 22) and `ssh_key`
(default `/data/keys/zfs`). It builds an argv array, never a shell string.

**SSH IS THE SHAPE, not one transport among several**, and the reason is the guarantee the design
rests on: `command="/usr/local/sbin/quince-zfs-helper"` in `authorized_keys` pins the helper
regardless of what the client asks for. Under any other transport that constraint would live only
inside the script, and a caller could reach the host without going through it.

**`hook_cmd` IS RETIRED and a file carrying it is REFUSED**, by path, naming its four successors —
the same shape `mode: exec` uses and for the same reason: an unknown-key warning would say
*"ignored"* about the one key every existing zfs install has set.

The transport binary (`ssh`) must exist **where quince runs**: the
runtime image ships `openssh-client` for exactly this (qn.4a gate-15 finding #2) — without it every
hook call dies with `exec: "ssh": executable file not found` and no backup can seed.

**`mode: exec` is REMOVED** — Operator ruling 2026-08-10 (quince#697). It ran `zfs …` in the
container against delegated privileges, and **the shipped image has no `zfs` binary**, so the mode
you got by default could not work in what we ship. Shipping the userland was rejected rather than
merely declined: the `zfs` CLI talks to the host kernel module over a versioned ioctl interface, so
the image would have to track a host version that is not ours to control.

**If your `config.yml` still says `mode: exec`, quince refuses it and names the key** —
`storage[N].zfs.mode: invalid value "exec"; must be one of [hook]`. Set up the helper below and
change the line to `hook`, or just delete the line: an absent key already means `hook`.

### The constrained `quince-zfs-helper` (forced-command reference)

On the ZFS host, add a **dedicated** SSH key whose `authorized_keys` entry forces this helper —
quince can then only snapshot/destroy/list `@quince-*` and create child datasets under the one
configured parent. **Dataset destroy is deliberately impossible via this key** — quince prints
the exact `zfs destroy <dataset>` command for a human instead.

`authorized_keys` (one line):

```
command="/usr/local/sbin/quince-zfs-helper",no-port-forwarding,no-agent-forwarding,no-pty,no-X11-forwarding ssh-ed25519 AAAA... quince
```

`/usr/local/sbin/quince-zfs-helper` — the parent dataset is baked into the script rather than taken
from the client, which is what stops the client escaping it.

**THE SCRIPT IS A FILE, NOT A FENCE IN THIS DOCUMENT** — `core/internal/storage/zfshelper/quince-zfs-helper`.

It moved there (quince#818 piece C) because it is becoming a **product artifact** rather than an
excerpt: the point of that piece is that quince serves the script back with your `parent_dataset`
already substituted, so the *Add storage* screen hands you a finished file instead of asking you to
edit one. That needs `go:embed`, which cannot reach outside its module, and the module root is
`core/` — so the path is decided by the mechanism rather than by taste.

**The serving is NOT BUILT YET; this move is what makes it possible.** Today the file is the same
script it always was, in a place quince can embed it from. Until that lands, set the `PARENT=` line
by hand as before.

**Two things followed from the move, and both are the point rather than side effects.** The Go gate
that runs the real helper against a stubbed `zfs` now reads the file instead of parsing this
document for a fenced block, so a prose edit can no longer break a gate. And `shellcheck` opened the
script **for the first time** — it had never been linted while it lived in a fence — which is what
produced the `SC2086` question the next section answers.

**THE FILE IS DELIBERATELY SPARE, AND THIS SECTION IS WHY IT CAN BE** — Operator ruling on
quince#887. The script is displayed verbatim in the UI and then installed on somebody's storage host,
so it is read there as an *artifact*, not as our notebook: a reader deciding whether to trust a file
they are about to run as root should not have to page through this project's reasoning to find the
code. It was **90 lines, 65 of them comment**; it is now 49, with the code byte-identical. The
comments that survive are the ones an operator needs *at that moment* — what the script allows, and
why three lines that look wrong are not.

**What moved here, so nothing is lost:**

- **`set -- $SSH_ORIGINAL_COMMAND` is unquoted on purpose.** A forced command receives the client's
  request as one string, never as argv, so splitting it on whitespace is how the script gets its
  arguments at all. Quoting it — what `SC2086` asks for — makes the whole request one word, no arm
  matches, and every verb falls through to the refusal. **`set -f` would be a genuine narrowing** and
  is deliberately not applied: no legal dataset or snapshot name contains a glob character, so
  nothing valid would break, but it is a behaviour change to a security boundary that **no agent seat
  can test** (quince#730 — the zfs branch has no live host outside the Operator's). What holds
  without it is that every arm guards `$target` against `"$PARENT"`/`"$PARENT"/*` before reaching
  `zfs`.
- **The `create` arm checks the parent exists BEFORE creating** — measured on a real pool, 2026-08-12
  (quince#818). `zfs create -p` creates missing *parents* too, so a typo in the `PARENT=` line did
  not fail: it silently built a whole new dataset tree and put backups in it. That tree has neither
  of the two settings this document opens by requiring — no `com.sun:auto-snapshot=false`, no quota —
  so the failure surfaces much later as retention reclaiming nothing, or as ENOSPC mid-backup.
  Checking first turns that into a refusal naming the dataset, at a cost of one `zfs get` per create,
  which is once per device rather than once per backup.
- **The chown uid is inherited, not configured** (quince#818). There used to be a `CTUID=` constant an
  operator had to know: `0` for privileged/native, the userns base (e.g. `100000`) for an
  unprivileged LXC — a number that is invisible from inside the container, so quince could not fill
  it in and a wrong one made the chown a silent no-op. The parent's mountpoint must already be
  writable by quince, so its owner **is** the mapped root.
- **`rollback` takes no `-r`, and none can reach it** — the parse drops every flag. Without `-r`,
  `zfs rollback` refuses any snapshot but the most recent, and `-r`/`-R` are what destroy *newer*
  snapshots, i.e. committed versions. So the verb structurally cannot lose one.
- **`capacity` takes no caller argument at all**, which makes it *tighter* than the pattern-guarded
  arms — it is why the helper check fires it first: a failure there is unambiguously about
  reachability.
- **Verbs that changed, for an operator upgrading an existing helper:** `seed)` was **deleted**
  (qn.5b's clone of `latest/` → `working/<udid>`; quince no longer seeds on this backend) and
  `rollback)` was **added** (what Reset uses). Backups keep working across that gap — only reset
  needs the new verb.

**⚠️ MIGRATION — operators upgrading MUST add the `capacity)` case from that file**, the same way this
section records `qn.6h`'s changed verbs (`seed)` out, `rollback)` in). Without it every zfs storage card reads *"free
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

**The `rollback` verb (qn.6h) is what Reset uses**, and it is the only verb that changes a device's
live data. It returns the device dataset to its newest `@quince-*` snapshot, discarding whatever a
failed job left in the head. **It structurally cannot lose a committed version, and that is a
property of the parse rather than of quince's restraint:** the helper discards every flag, so
`rollback -r <snap>` reaches `zfs` as a plain `zfs rollback <snap>` — and a plain rollback refuses
any snapshot but the most recent, while `-r`/`-R` are exactly what destroy newer ones. Measured
through this script on real ZFS, 2026-08-08: `-r` was discarded and the newer snapshot survived; a
non-`@quince-*` target was refused; `destroy <dataset>` with no `@` was refused.

**It cannot be probed, which is a stated cost.** `Test helper` fires `capacity` and `list` because
both are harmless; there is no harmless way to test a rollback, since testing it means performing
one. So a helper that never got the new case surfaces at the **first reset a user asks for**, as this
script's own `quince-zfs-helper: refused: rollback …` on stderr. quince's job is to make that legible
rather than to pretend it can be caught earlier.

**`zfs rollback` also refuses whenever ANY newer snapshot exists — including a foreign one.** That is
why the host snapshotter must be excluded (see REQUIRED SETUP above): an automatic snapshotter firing
every few minutes means a `@quince-*` snapshot stops being the most recent within minutes of being
taken, and from then on reset is refused. quince surfaces that refusal with `zfs`'s own words and
tells the operator the dirty head is still resumable — it does not, and must not, destroy somebody
else's snapshots to clear the path.

**The old `seed` verb is GONE.** quince has no job-start clone on this backend any more: the tool
writes straight into the dataset root, so there is nothing to seed from and nothing to seed into.
Delete the case from your script — nothing calls it, and leaving it is a `cp -a --reflink` and an
`rm -rf` reachable by a key that no longer needs them. Hookless deployments are likewise unaffected:
the in-container seed ladder is not reached on zfs either.

Then the transport. **You give quince the user and the host; it composes the rest** — the key, the
port and all three host-key options — and appends the operation + target as argv:

```yaml
zfs:
  parent_dataset: rpool/quince
  ssh_user: zfsuser
  ssh_host: zfshost
  # ssh_port: 22               # both OPTIONAL — these are the defaults, so leave them out
  # ssh_key: /data/keys/zfs    # unless you already keep the key somewhere else
```

which quince runs as:

```
ssh -i /data/keys/zfs -p 22 -o BatchMode=yes -o UserKnownHostsFile=/data/keys/known_hosts -o StrictHostKeyChecking=yes zfsuser@zfshost
```

**`StrictHostKeyChecking=yes`, not `accept-new`.** `accept-new` trusts whatever answers first, which
on a first connect is exactly the moment a machine-in-the-middle would want. Seeding `known_hosts`
below is what makes `yes` workable, and it is the property standing between this and somebody else's
host receiving your backups.

**THE HOST KEY IS NOT OPTIONAL, AND OMITTING IT FAILS EVERY HOOK CALL FROM THE FIRST ONE.**
`BatchMode=yes` disables the interactive *"are you sure you want to continue connecting?"* prompt —
that is what makes it safe to run unattended — so ssh cannot accept an unknown host key and refuses
instead. A container's `known_hosts` is empty on a first install, which is exactly when an operator
sets this up. Measured on a lab rig, 2026-08-10, with the command shape this document carried until
now (`-i <key> -o BatchMode=yes user@host`), against a real forced-command helper:

```
Test helper → outcome: unreachable
              detail:  Host key verification failed.
```

The same command with the two options above answers `ok`. **Nothing about the key, the forced
command or the pool was wrong** — and `unreachable`'s remedy text points at all three, so the one
cause it cannot be is the one it is.

**Seed `known_hosts` on the host, before the first backup.** This is the recommended form: it pins
the key you verified, once, and `StrictHostKeyChecking=yes` refuses anything else forever after.

```sh
ssh-keyscan -t ed25519 zfshost >> /path/to/quince/data/keys/known_hosts   # then EYEBALL it
```

**`StrictHostKeyChecking=accept-new` IS NO LONGER SELECTABLE, and this paragraph used to offer it.**
It was *"the documented alternative"* — record the key on first contact, refuse a *change*
thereafter; weaker than seeding, because it trusts whatever answers the first time. That was a real
choice while the operator wrote the whole command. **Since quince#818 quince composes it**, so the
only reachable value is `yes` and there is no spelling of `config.yml` that yields `accept-new`.

**Corrected rather than deleted, because it was live guidance the day before** — an operator who
chose `accept-new` deliberately needs to know it is gone, rather than wonder why their setting
stopped having an effect. Seeding is therefore no longer *recommended over* the alternative; it is
the only path, which is why the step above is required.

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
