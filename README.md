# quince

> Self-hosted backup server for iPhone & iPad with a web UI.

Apple-native encrypted backups (`idevicebackup2`) over USB or Wi-Fi into versioned
storage — ZFS snapshots, or reflink/hardlink versions on plain filesystems — with live
progress, honest failure states, and a layout that is safe to sync offsite while
backups run.

**Status: working, pre-release.** Full encrypted backup cycles over both transports are
hardware-proven and the app runs under real daily use. Browsing backup contents
(Messages, files) is the next major arc. Development is agent-driven; the journal —
progress, decisions, roadmap — lives in
[**quince-devlog**](https://github.com/novkostya/quince-devlog).

## Why

- Finder/iTunes keeps one overwritable copy; iMazing is desktop-bound and struggles
  over SMB with 143k-file backups.
- A server sitting next to the storage can back up, verify, version, and (eventually)
  decrypt and serve your data to any browser — including the iPhone itself.
- Runs like Plex: one container, `compose up`, configure in the web UI — while all
  settings live in one hand-editable config file.
- Committed versions are immutable and exist only after verification — ZFS-native
  snapshots per device, or journaled atomic version dirs elsewhere. One rclone job over
  the storage tree never uploads a half-written backup.

## Shape

```
Go core daemon ─── REST + WebSocket ───► React web UI (embedded)
  ├─ device tracking      (usbmuxd / netmuxd socket protocol, live events)
  ├─ backup jobs          (idevicebackup2 supervisor, state machine)
  ├─ storage backends     (zfs snapshot-native | reflink | hardlink | copy)
  └─ vault                (swappable sidecar, Python today: reuses open-source backup
                           decryption; lazy session-scoped reads, no persistent index)
```

One container, multi-arch (amd64/arm64), designed to also run on modest NAS hardware.

## Docs

| Doc | What it holds |
| --- | --- |
| [`docs/quince.stack.md`](docs/quince.stack.md) | Tech decisions + why, alternatives considered |
| [`docs/quince.design.md`](docs/quince.design.md) | Architecture: components, job state machine, storage, security model |
| [`docs/contracts.md`](docs/contracts.md) | REST/WS API, vault RPC, cache rules |
| [`docs/ui.design.md`](docs/ui.design.md) | Visual direction and frontend conventions |
| [`docs/specs/`](docs/specs/) | Per-rung specs |
| [quince-devlog](https://github.com/novkostya/quince-devlog) | Progress dashboard, decisions log, roadmap |

Historical references in the docs (`qn.N` rungs, lettered decisions) resolve in the
[devlog](https://github.com/novkostya/quince-devlog) — kept as citations rather than
scrubbed, so the record stays traceable.
