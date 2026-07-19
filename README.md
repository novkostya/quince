# quince

> Self-hosted backup manager and browser for iPhone and iPad (any device speaking
> Apple's standard pairing + MobileBackup2 stack; Vision Pro support unknown/untested —
> visionOS may be iCloud-only). Pairs with your device over USB or Wi-Fi,
> runs Apple-native encrypted backups (`idevicebackup2`) into versioned storage (ZFS
> snapshots, or reflink/hardlink versions on plain filesystems), and serves a modern
> web UI to run, monitor, and browse those backups — Messages, Photos, files — right where
> the data lives.

**Status: pre-implementation.** The protocol layer is proven by hand (full encrypted USB
backup + Wi-Fi incremental into a ZFS dataset, verified openable by iMazing); the app does
not exist yet. All decisions and the build plan live in [`docs/`](docs/).

## Why

- Finder/iTunes keeps one overwritable copy; iMazing is desktop-bound and struggles over
  SMB (143k-file backups make remote viewing miserable).
- A server-side app sitting next to the storage can decrypt and serve conversations and
  file trees straight to any browser — including the iPhone itself. Modern iOS requires
  the passcode on-device for every backup, so the flow is *assisted*: put the phone on
  the charger → a Shortcut pings quince → if a backup is due, one push → unlock,
  confirm, done (PWA + Web Push).
- Runs like Plex: copy-paste a compose file, `compose up`, configure everything in the
  web UI — while all settings live in one tidy, hand-editable config file
  (PVE/OpenWrt-style transparency, no opaque GUI state).
- Committed versions are immutable and only exist after verification — on ZFS as native
  snapshots of a per-device dataset (browsed via `.zfs`, carried intact by
  sanoid/syncoid), elsewhere as iMazing-style hardlink version dirs with a journaled
  atomic commit. The live tree always presents a consistent `latest/` per device, so
  one rclone job over the whole storage tree (→ B2) never uploads a half-written
  backup. Apple's fragile MobileBackup2 protocol becomes a verified transaction either
  way.

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

## Docs map

| Doc | What it holds |
| --- | --- |
| [`docs/quince.stack.md`](docs/quince.stack.md) | Tech decisions + why, alternatives considered |
| [`docs/quince.design.md`](docs/quince.design.md) | Architecture canon: components, job state machine, storage, security model |
| [`docs/contracts.md`](docs/contracts.md) | Cross-track contracts: REST/WS API, vault RPC, cache rules |
| [`docs/ui.design.md`](docs/ui.design.md) | Visual direction and frontend conventions |
| [`docs/quince.roadmap.md`](docs/quince.roadmap.md) | Milestones and rungs (`qn.N`), parallelization map |
| [`docs/quince.progress.md`](docs/quince.progress.md) | Live dashboard: what is built, what is the frontier |
| [`docs/program/quince.program.md`](docs/program/quince.program.md) | The build loop for implementing agents |
| [`docs/specs/`](docs/specs/) | Per-rung specs |
| `local/chatgpt-original-idea-chat.md` | Lab notes: the original hands-on feasibility research (Russian) |

## License

MIT.
