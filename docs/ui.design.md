# quince — UI direction

> Visual and interaction canon for the web app. The frontend stack decision is
> [`quince.stack.md` D7](quince.stack.md); this doc is taste + conventions.

## Taste references (Operator-supplied)

- **sing-box dashboard (1.14 alpha)** — the primary reference. Light neutral background;
  left sidebar (product name + version up top, then flat nav items with small line icons,
  active item as a soft filled pill); content = grid of white rounded-corner cards; each
  card: muted icon+label header, one large monospace metric, muted secondary stat, minimal
  monochrome sparkline; instance/uptime pinned at sidebar bottom. Quiet, airy, almost
  monochrome.
- **AdGuard Home** — dense-but-calm tables and stat blocks, functional minimalism.
- **GL.iNet router UI** — friendly cards, clear affordances for non-expert users.
- **Anti-reference: iMazing** — capable but 2010s-macOS-skeuomorphic-ish; avoid the vibe.
- **mercury** — a private token-driven design system the Operator likes (family
  project, not public). Borrow the architecture: semantic CSS tokens, light/dark one
  variable deep, presentational components + external state (Effector).

## Principles

1. **Calm by default, loud only for state that matters.** A running backup gets a live
   card with progress and a sparkline of throughput; everything else stays quiet.
   Failures are explicit and plain-language, never toast-and-forget for jobs (toasts for
   acknowledgements, inline persistent state for anything a user must act on).
2. **Real-time is table stakes.** Device appears → it's on screen within a second, no
   refresh. Progress, logs, snapshot list — all WS-driven. No spinners longer than 300 ms
   without a label saying what's happening (the lab showed Apple's protocol goes silent
   for minutes — the UI must narrate that honestly: "device is preparing… this can take
   several minutes").
3. **Data-dense views are virtualized and paginated.** Messages/photos/files never load
   unbounded lists; first page fast, rest streams in.
4. **Device-centric IA — one primary area** (Operator ruling; the old Devices/Backups
   split mirrored the engineering epics, not how anyone thinks). Navigation is
   `Devices` + `Settings`, and backups live *inside* their device:
   - **Home = the Devices dashboard**: one card per device (identity, presence,
     encryption state, last-backup status) with a `Back up now` button and inline
     mini-progress when a job is running, plus the N most recent backups across devices
     — a couple of family phones don't generate much data, so the dashboard is composed
     to look alive rather than empty.
   - **Device details**: everything about one device — status, actions, job history
     (grouped by intent), and its full version list with unlock/browse entry points.
   - *Parked for qn.12*: a phone-first entry point — when the PWA is opened from a
     backed-up device itself, land directly on that device's details with a
     "See all devices" escape hatch.
   Sidebar layout per the sing-box reference; product name + version top-left;
   connection status (WS state, backend probe) bottom-left.
5. **Numbers are monospace** (tabular figures), units spaced (`7.5 KB/s`, `3.6 GB`),
   sizes humanized consistently (one shared formatter).
6. **Light + dark from day one** via tokens; system-follow default, manual override.
7. **PWA-ready shell** (manifest, viewport, touch targets) even before push lands: the
   iPhone itself is a first-class client.
8. **Everything configurable in-app; the file stays visible.** Settings pages are an
   editor over `config.yml` (stack D12) — no UI-only state, no setting that can't be
   found in the file. A read-only "current config" view (PVE-style) in Settings shows the
   exact file contents, with a banner when a hand-edit was rejected (invalid) or a
   hand-edit is live. Onboarding is guided checks with plain-language explanations, not
   a wall of fields.

## Conventions (stack per D7: Tailwind v4 + vendored shadcn-style components + Zustand)

- Tokens live as CSS variables in the Tailwind v4 theme (`ui/src/styles/tokens.css`):
  full semantic palette (`--bg`, `--bg-card`, `--fg`, `--fg-muted`, `--accent`, states)
  — components consume tokens only, never raw colors. This is the mercury idiom carried
  over; mercury itself is a taste reference, not a dependency.
- Components are vendored shadcn/ui-style on Radix primitives — styled copies in our
  repo, ours to edit; no component-library dependency.
- State: Zustand stores per feature (`devices`, `jobs`, `versions`, `session`); a
  single WebSocket bridge multiplexes the event stream into the stores; TanStack Query
  for REST reads; TanStack Virtual for long lists; components stay presentational.
- Icons: one line-icon set (lucide), 16/20 px, muted by default.
- Screenshots for README/releases come from `quince serve --demo` with fixture data —
  keep fixture data presentable (real-looking device names, plausible sizes).
