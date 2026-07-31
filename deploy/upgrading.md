# Upgrading quince

Breaking deployment changes, newest first. Each entry says what to do **before** upgrading.

---

## `qn.6c` — declare your storage before you upgrade

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
while declaring nothing. `qn.6c` makes storage plural, and *"where do the backups go"* stops having
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
  is not built yet** (`qn.6c` stories 5–9). Until it lands, backups go to the `default` storage.
