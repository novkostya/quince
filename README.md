# quince

> Self-hosted backup server for iPhone & iPad with a web UI.

Apple-native encrypted backups (`idevicebackup2`) over USB or Wi-Fi into versioned
storage — ZFS snapshots, or reflink/hardlink versions on plain filesystems — with live
progress, honest failure states, and a layout that is safe to sync offsite while
backups run.

**Status: working, pre-release.** Encrypted backups run start to finish over both USB and
Wi-Fi, on real phones rather than only in tests, and quince is in daily use. There is no
tagged release yet, so setup still means reading the docs. Browsing backup contents
(Messages, files) is the next major arc. Development is agent-driven; the journal —
progress, decisions, roadmap — lives in
[**quince-devlog**](https://github.com/novkostya/quince-devlog).

## Why

- **One copy is not a backup.** Finder and iTunes keep a single copy and overwrite it on
  every sync: a file you deleted last month is already gone, and one corrupted backup
  takes the only good one with it. quince keeps a history of versions instead, each
  immutable once written.
- **A desktop app needs a desktop.** iMazing and its peers back up only while your Mac or
  PC is awake and running them, and they can crawl when the backup library lives on
  network storage rather than local disk — a full iPhone backup can run to six figures of
  files.
- **The server is already next to the storage.** It can back up, verify, version, and
  (eventually) decrypt and serve your data to any browser — including the phone that made
  the backup.
- **Set up like Plex, configured like a router.** One container, `compose up`, everything
  editable in the web UI — and every setting lives in one hand-editable config file, with
  no secrets in it.
- **Safe to sync offsite while a backup is running.** A version exists only after it has
  been verified, and never changes afterwards — ZFS snapshots per device, or journaled
  atomic version dirs elsewhere. One rclone job over the storage tree can never upload a
  half-written backup.

## Shape

```
Go core daemon ─── REST + WebSocket ───► React web UI (embedded)
  ├─ device tracking      (usbmuxd / netmuxd socket protocol, live events)
  ├─ backup jobs          (idevicebackup2 supervisor, state machine)
  ├─ storage backends     (zfs snapshot-native | reflink | hardlink | copy)
  └─ vault                (swappable seam over quince's own Go decryption/parsing
                           libraries; lazy session-scoped reads, no persistent index)
```

One container, multi-arch (amd64/arm64), designed to also run on modest NAS hardware —
one static Go binary with the UI embedded in it, and no language runtime alongside.

## Docs

| Doc | What it holds |
| --- | --- |
| [`docs/quince.stack.md`](docs/quince.stack.md) | Tech decisions + why, alternatives considered |
| [`docs/quince.design.md`](docs/quince.design.md) | Architecture: components, job state machine, storage, security model |
| [`docs/contracts.md`](docs/contracts.md) | REST/WS API, vault RPC, cache rules |
| [`docs/ui.design.md`](docs/ui.design.md) | Visual direction and frontend conventions |
| [`docs/specs/`](docs/specs/) | Per-rung specs |
| [`CREDITS.md`](CREDITS.md) | The projects quince stands on, and the licence position |
| [quince-devlog](https://github.com/novkostya/quince-devlog) | Progress dashboard, decisions log, roadmap |

Historical references in the docs (`qn.N` rungs, lettered decisions) resolve in the
[devlog](https://github.com/novkostya/quince-devlog) — kept as citations rather than
scrubbed, so the record stays traceable.

## Security

quince holds a phone's entire contents, and `docs/quince.design.md` §6 is the security model.
Found something? [`SECURITY.md`](SECURITY.md) — report it privately rather than as a public issue,
and read its *already known and accepted* list first: several sharp edges are deliberate and it
says why.

## Licence

[MIT](LICENSE). The container image also ships LGPL-2.1 tooling that quince patches, and
unmodified GPL-2.0 Alpine packages; [`CREDITS.md`](CREDITS.md) enumerates every dependency,
records where each licence was read from, and states how the source-availability obligations
are met. Not affiliated with Apple Inc.
