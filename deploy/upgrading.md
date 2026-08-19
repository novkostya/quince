# Upgrading quince

Breaking deployment changes, newest first. Each entry says what to do **before** upgrading.

---

## `storage.zfs.hook_cmd` is retired — convert it, AND trust the host key, before you upgrade

**This one does not refuse to start.** Every other entry below stops the daemon and prints what to
write. This one starts, discards the storage the invalid line belongs to, and shows you
**"Add your first storage"** — on an install that has one and has been backing up to it for months.
Backups stop, and the only account of why is a line in the startup log.

So read this before you pull the image, not after.

### Do this first

`hook_cmd` was one free-text ssh command line. It is now the fields that command line contained:

```yaml
# before
storage:
  - name: nas
    path: /backups
    backend: zfs
    zfs:
      parent_dataset: tank/quince
      hook_cmd: ssh -i /data/keys/zfs -p 22 quince-hook@nas.example

# after
storage:
  - name: nas
    path: /backups
    backend: zfs
    zfs:
      parent_dataset: tank/quince
      ssh_user: quince-hook
      ssh_host: nas.example
      ssh_port: 22             # optional, 22 if you leave it out
      ssh_key: /data/keys/zfs  # optional, this is the default
```

`ssh_user` and `ssh_host` are the two with no default; the other two can be omitted entirely if
they match the values above.

### Then trust the host key — converting the keys is NOT enough

Your old `hook_cmd` supplied its own ssh options. The composed transport supplies its own instead,
and they are stricter: `BatchMode=yes`, `StrictHostKeyChecking=yes`, and
`UserKnownHostsFile=/data/keys/known_hosts`.

**That file is empty on an install upgrading from `hook_cmd`**, so ssh refuses every connection to
your helper host until it has an entry — correctly, because a container has no terminal to answer a
host-key prompt on and trusting whoever answers first is not a safe default.

Two ways to fill it, and the first is the one to use:

1. **The add-storage form does the whole ceremony.** It shows you the host's fingerprint, you check
   it against the host itself, and quince writes the entry. On the host, the command to compare
   against is:

   ```sh
   ssh-keyscan localhost | ssh-keygen -lf -
   ```

   That asks the running sshd what it actually presents, so a key sitting in a file that sshd does
   not serve cannot produce a mismatch you would spend an hour on.

2. Or write `/data/keys/known_hosts` yourself, in the ordinary `known_hosts` format, one host.

### If you upgrade first

Nothing is lost and nothing is written — the config is **discarded**, not rewritten, and your
`config.yml` still says exactly what you wrote. The daemon serves setup only.

The cause is in two places: the startup log (`config warning path=storage[0].zfs.hook_cmd`) and
`GET /api/config`, which returns the same warning plus `discarded: true` and your file verbatim.
It is **not** in the screen you land on. Fix the config, restart, and the storage comes back with
its versions — startup reconciliation re-registers them from their `quince-version.json` markers,
as it always has.

### Why a free-text command line had to go

`hook_cmd` was split on whitespace to build an argv, so a key path containing a space produced an
argv that was silently wrong, with no escaping an operator could have written. Fields compose an
array instead, and the question stops existing.

---

## `storage:` is now the list itself

**If you have already written `storage.storages:` per the entry below, that shape no longer parses.
`storage:` IS the list.** This is the second config break in a row, and both landed before the
first release — so no published version of quince ever read the old shape.

### Do this first

Lift the entries up one level and delete the wrapper, the globals, and `storages:`:

```yaml
# before
storage:
  storages:
    - name: local
      path: /backups
      default: true
  backend: auto
  zfs: {parent_dataset: rpool/quince, mode: hook, hook_cmd: "…"}
  retention: {keep_recent: 10, keep_daily: 30, keep_weekly: 12}

# after
storage:
  - path: /backups
    backend: auto
    zfs: {parent_dataset: rpool/quince, mode: hook, hook_cmd: "…"}
    retention: {keep_recent: 10, keep_daily: 30, keep_weekly: 12}
```

**On a single storage that is the whole of it** — `name` defaults to the path and `default: true` is
implied, so `- path: /backups` is a complete declaration. Write both only when you declare a second
storage, where exactly one must carry `default: true` and declaring none is an error.

### What moved, and why the globals could not stay

`backend`, `zfs` and `retention` were global keys every entry inherited. **A list has nowhere to put
a global**, and the inheritance was itself the bug: a second storage on a USB disk got a zfs backend
whose parent dataset pointed at another pool (quince#458). Every entry now carries its own, so
nothing can bleed from a global onto a storage it was never written for.

**`retention` is now per-storage.** An absent `retention:` uses the same code defaults as before, so
if you never set it, nothing changes. If you did set it, copy it into each storage you want it on:
`keep_recent: 10` now means ten versions **on that disk**.

**`zfs: {}` as an opt-out is gone**, and does not need replacing. It existed only to refuse a global
that no longer exists; a storage that is not zfs simply does not declare `zfs`.

**`backend: auto` is still valid and you should keep using it.** The direction that only concrete
backends may appear in this file is deferred (quince#502) — `auto` is what probes the path and
checks the result against the medium, so writing a concrete backend by hand is currently a guess
nothing verifies.

### If you upgrade first

Your old `storage:` block becomes an unknown-key warning and the list reads as absent, so quince
**refuses to start** and prints the short form to write. Nothing is opened, probed or written before
that refusal, and nothing is lost.

---

## Declare your storage before you upgrade

**`QUINCE_BACKUPS` is retired. quince will refuse to start until `storage.storages` is declared in
`config.yml`.**

### Do this first

Add to `/data/config.yml`:

```yaml
storage:
  storages:
    - name: local
      path: /backups        # whatever QUINCE_BACKUPS was set to, or /backups if it was unset
      default: true
```

Use **the path you were already backing up to.** quince adopts what it finds there — existing
versions are re-registered from their `quince-version.json` markers by startup reconciliation, as
they always have been. Nothing is copied and nothing is re-transferred.

Then upgrade. You can drop `QUINCE_BACKUPS` from your compose file or `docker run` line; if you
leave it set, quince logs it as an unknown variable and ignores it.

### If you upgrade first

quince refuses to start and prints exactly what to add, including the path from your still-set
`QUINCE_BACKUPS` if it can see one. Add it and start again. **Nothing is lost** — the refusal
happens before anything is opened, probed or written.

### Why it refuses instead of guessing

`QUINCE_BACKUPS` carried a built-in `/backups` default, so every deployment had a working storage
while declaring nothing. Storage is plural now, and *"where do the backups go"* stops having
a sane default the moment there can be more than one place. The honest form of "no default" is a
refusal that names the key and prints the remedy.

The alternative — synthesize a storage from the old variable — was recommended by both agent seats
and overruled: it is a permanent implicit path bought to protect a population of deployments that
does not exist. A quince that comes up with nowhere to put backups looks healthy and silently
protects nothing, which is worse than one that did not start.

### What did NOT change

- **Your backups.** The on-disk layout is untouched: `latest/`, `working/`, `versions/`, snapshots
  and markers are all exactly as they were.
- **`QUINCE_DATA`, `QUINCE_CACHE`, `QUINCE_LISTEN`** — still environment, still deployment topology.
- **`storage.backend`, `storage.zfs`, `storage.retention`** — still global, and every declared
  storage inherits them.

### Notes

- A change to `storage.storages` needs a **restart**. The backend is probed once per storage at
  startup, and the subsystem holds it for the process's life.
- `--demo` is unaffected — it serves fixture data and touches no storage.
- Declaring more than one storage is accepted by config, but **choosing between them at backup time
  is not built yet.** Until it lands, backups go to the `default` storage.
