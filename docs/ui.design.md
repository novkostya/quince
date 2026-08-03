# quince — UI direction

> Visual and interaction canon for the web app. The frontend stack decision is
> [`quince.stack.md` D7](quince.stack.md); this doc is taste + conventions.

## Visual target

Quiet, airy, nearly monochrome: light neutral surfaces, with colour spent only on state
that matters. Concretely —

- **Sidebar-first layout.** A left sidebar holds the product name and version at the top,
  then flat nav items with small line icons; the active item reads as a soft filled pill.
  A persistent status readout is pinned at the bottom.
- **Content is a grid of cards.** White, rounded corners, generous spacing. A status card
  reads: muted icon-and-label header, one large monospace metric, a muted secondary stat,
  and at most a minimal monochrome sparkline.
- **Dense but calm.** Tables and stat blocks carry many rows without shouting — functional
  minimalism, no chrome that isn't load-bearing, no decoration competing with the data.
- **Legible without expertise.** Cards and actions explain themselves; someone who has
  never heard of `idevicebackup2` can still tell what is happening and what to press.
- **What to avoid:** the skeuomorphic, heavily-chromed desktop-utility look — faux
  materials, gradients, dense toolbars of tiny tinted icons. Capable software can still
  feel a decade out of date, and that is the failure mode here.
- **Token-driven, one variable deep.** Semantic CSS tokens are the entire theming layer:
  light and dark differ by token values, never by a branch inside a component. Components
  stay presentational and read state from outside themselves.

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
   `Home` + `Settings`, and backups live *inside* their device:
   - **Home** is `/`, and `/devices` still resolves so existing links keep working. It was
     labelled `Devices` until `qn.6d` (Operator ruling, quince#443): **storage joined the
     page and the label stopped describing it.** `Home` names the POSITION rather than the
     contents, and the position does not change — a label naming what is on a page goes
     stale the next time the page grows, and this one will. **There is no third nav item**:
     the sidebar is one `NAV` array rendered as a phone top bar *and* a desktop sidebar, so
     a third entry is nearly free on desktop and expensive on the phone, with no way to have
     it in one and not the other. `HardDrive` was Home's icon and is handed to storage,
     which is what it actually depicts.
   - **Home holds devices and storage as PEERS**: one card per device (identity, presence,
     encryption state, last-backup status) with a `Back up now` button and inline
     mini-progress when a job is running; one card per declared storage (free-of-total, a
     usage bar, backup and device counts); plus the N most recent backups across devices
     — a household with two or three devices doesn't generate much data, so the dashboard
     is composed to look alive rather than empty.
   - **Storage details**: everything about one storage — its marker rendered as identity,
     space, the devices backed up there, and the versions it holds.
   - **Device details**: everything about one device — status, actions, job history
     (grouped by intent), and its full version list with unlock/browse entry points.
   - *Parked for qn.12*: a phone-first entry point — when the PWA is opened from a
     backed-up device itself, land directly on that device's details with a
     "See all devices" escape hatch.
   Sidebar layout per **Visual target** above; product name + version top-left;
   connection status (WS state, backend probe) bottom-left — that section's pinned status
   readout is this.
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
  — components consume tokens only, never raw colors, and light/dark is a change of token
  values rather than a branch in a component (stack D7 records where the idiom came from
  and why no design-system dependency came with it).
- Components are vendored shadcn/ui-style on Radix primitives — styled copies in our
  repo, ours to edit; no component-library dependency.
- State: Zustand stores per feature (`devices`, `jobs`, `versions`, `session`); a
  single WebSocket bridge multiplexes the event stream into the stores; TanStack Query
  for REST reads; TanStack Virtual for long lists; components stay presentational.
- Icons: one line-icon set (lucide), 16/20 px, muted by default.
- Screenshots for README/releases come from `quince serve --demo` with fixture data —
  keep fixture data presentable (real-looking device names, plausible sizes).
