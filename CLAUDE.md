# quince — agent entry point

Self-hosted iPhone backup manager/browser. Go core daemon + swappable vault sidecar
(Python today, reusing OSS encrypted-backup decryption; lazy session-scoped reads, no
persistent index) + React/TS UI (Tailwind v4 tokens, vendored shadcn-style components,
Zustand; device-centric IA — Devices + Settings only), REST + one WebSocket, SQLite app
DB, never-mutate-committed versioned storage (zfs backend: snapshot-native, one child
dataset per device, in-place `working/` + reflink-built verified `latest/` mirror for
whole-tree rclone offsite sync, versions = quince's own `zfs snapshot`s browsed via
`.zfs`; reflink (FICLONE — the auto-default on Btrfs/XFS/hookless-ZFS) / hardlink / copy
backends: `versions/` + `latest` + `work/` with journaled commit; startup reconciliation
is first-class). Wi-Fi backup is the PRIMARY use case (first-class transport, hardened
before v0.1) under the ASSISTED model — iOS requires on-device passcode entry per
backup, so there is no unattended mode and no auto-retry: opportunity signal → push →
one unlock+confirm; failures become `user action required`. Core value: Plex-grade setup, OpenWrt/PVE-grade config — one tidy
hand-editable `config.yml`, UI edits the file, no secrets in it (stack D12). Photos
viewer is parked, lowest priority.

**Don't improvise architecture — and don't silently patch holes either.** The docs are
canon-so-far, not scripture: gaps WILL surface during implementation. When you hit one,
follow the **gap protocol** in the program doc — rung-local details get decided inside
the rung's spec and logged; anything touching contracts, storage semantics, security,
or user-visible behavior gets a written `PROPOSED (gap)` block in the right canon doc
plus an open question, and waits for an Operator ruling. What's forbidden is building
on an assumption you didn't write down, and re-litigating what's already ruled.

1. Start at [`docs/quince.progress.md`](docs/quince.progress.md) — current frontier rung
   and open questions.
2. The build loop, gate ladder, and hard rules: [`docs/program/quince.program.md`](docs/program/quince.program.md).
3. Canon: [`docs/quince.stack.md`](docs/quince.stack.md) (decisions),
   [`docs/quince.design.md`](docs/quince.design.md) (architecture),
   [`docs/contracts.md`](docs/contracts.md) (frozen interfaces),
   [`docs/ui.design.md`](docs/ui.design.md) (frontend taste).
4. One rung per session (`docs/specs/qn.N/`). Spec first if missing. Prove with gates.
   Update the dashboard. Stop — commit/push only when the Operator asks.

`local/chatgpt-original-idea-chat.md` is the original hands-on feasibility lab log (Russian):
the source of real `idevicebackup2` transcripts, Wi-Fi failure modes, and ZFS facts.
Treat it as evidence, not as decisions — decisions live in `docs/`. It is **local-only
and gitignored** (personal data), present on the Operator's machine but absent from
clones/CI — the durable extract is the committed transcript fixtures
(`core/internal/backup/testdata/transcripts/`).
