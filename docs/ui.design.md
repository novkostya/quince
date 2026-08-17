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

   **A DEGRADED MODE IS THE LOUD CASE, AND IT IS NON-DISMISSIBLE** (Operator ruling
   quince#446): no close button, no `localStorage` "don't show again", no timeout. It goes
   away when the state does and not before — a degraded mode that can be hidden stops being
   surfaced, which is the hard rule *no silent caps or fallbacks* read as a UI instruction.
   It names **what** is unprotected or incomplete rather than merely that something is: a
   status is a word about the daemon, a consequence is something a user can weigh.

   **THERE ARE THREE SHELLS, NOT ONE, AND A WARNING THAT MUST BE UNIVERSAL GOES IN ALL
   THREE** — `AppLayout` (authed), `AuthPage` (login and setup), and `OnboardingHTTPSPage`,
   which sits outside every guard. Above the shell is wrong: the top bar is `position: fixed`
   on a phone and would cover it. `InsecureTransportBanner` and `ReconcilingNotice` are the
   two instances, and `insecureTransportSurfaces.test.tsx` is what keeps the universal one
   from drifting off a surface nobody was looking at.
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

## Type, contrast and rhythm — the numbers, and where they came from

**CANON WAS SILENT HERE, NOT VIOLATED, AND THAT WAS THE FINDING** (quince#1155). The section above
asks for "generous spacing" and warns against dense surfaces; it named no size, no line-height and
no contrast floor, so every page picked its own and they drifted. `docs/ui.type-survey.md` is the
evidence: 34 rendered surfaces — mainstream web apps and self-hosted admin UIs — walked in Chromium
at desktop and phone widths and tallied by rendered character, plus iOS's own Dynamic Type table read
live from Apple's HIG.

**Sizes live in `ui/src/styles/tokens.css` and nowhere else.** The scale OVERRIDES Tailwind's own
`--text-*`, so `text-sm` and `text-xs` resolve to quince's steps rather than Tailwind's defaults;
that is deliberate, because a parallel set of semantic classes would have left every existing call
site on the old values. Tune the tokens, never the call sites.

| role | step | where |
| --- | --- | --- |
| page title | 24px | one `<h1>` per screen |
| **section heading** | **20px**, `SectionHeading` | labels a region of a page — one component, not a class string |
| card title | 18px | `CardTitle` |
| **body and field labels** | **16px** | every sentence a user reads |
| dense chrome | 14px | table cells, metadata, badges — **the floor** |

- **14px IS THE FLOOR AND 12px IS GONE**, deliberately: no token produces it. Two thirds of Home's
  characters were 12px, against a comparison set whose own floor is 14px.
- **Line-height is 1.5 at body size**, tightening to 1.25 at display size. quince measured 1.38x
  against a set at 1.51x, and that ratio — not the padding — is what "clamped together" describes.
  Container padding and block gaps were already inside both measured distributions and did not move.
- **`--type-sm` MUST STAY AT OR ABOVE 1rem.** iOS Safari zooms the page in when a focused control
  computes under 16px (quince#616), and `fieldBase` no longer carries a responsive step because the
  base scale clears the threshold on its own. Tune it lower and the field has to become responsive
  again in the same change; `field.test.tsx` asserts the token rather than the trick.

**Contrast floors, per role, checked against the WORST surface that role is painted on** — never the
most flattering one. AA is the floor and not the goal: text can clear 4.5:1 and still be exactly the
grey-on-grey that was reported.

| token | floor | measured comparison |
| --- | --- | --- |
| `--fg` | >= 12:1 | both populations' primary text sits at 12.2-14.9:1 |
| `--fg-muted` | >= 7:1 | the measured secondary medians, 9.63:1 and 7.58:1 |
| `--fg-subtle` | >= 5.5:1 | the only role allowed near the floor |
| state colours (`--ok`, `--warn`, `--danger`, `--accent`) | >= 5.5:1 | all four failed AA on the light theme before this |

**Under 2% of rendered characters below 4.5:1** is the whole-page target, which is what both measured
populations achieve. quince was at 20.85%, and 41.9% on the light theme.

**BORDERS ARE EXEMPT AND THAT IS RULED, NOT OVERLOOKED.** WCAG 1.4.11 asks 3:1 for a control's
boundary and `--border` meets neither that nor half of it. Raising it was tried twice on quince#1155
— a separate 3:1 token for controls, then one shared edge at 2:1 — and the Operator rejected both on
the deployed build. Nothing in the comparison set complies either (Forgejo 2.08:1, Grafana 1.63:1,
Immich draws no edge at all and tints the field instead). If a control ever genuinely needs to be
easier to find, give it a distinct FILL; do not raise `--border`.

**THE SCALE IS NOT RESPONSIVE, AND THAT WAS MEASURED RATHER THAN ASSUMED.** 11 of 13 apps that ship
both a desktop and a real mobile experience use the SAME body size at both widths, and the two that
differ go down on the phone, not up. quince follows them.

**WHERE quince DELIBERATELY SITS.** Its 16px body is at the top of the web set's band (median 14px)
and one point BELOW iOS's own Body style, which is 17pt at the default Dynamic Type size. The web and
native references disagree; quince sits between them, closer to native, because the iPhone is a
first-class client here. Operator-confirmed on hardware against a native app, 2026-08-17.

**WHAT IS STILL UNMEASURED, NAMED SO IT IS NOT MISTAKEN FOR SETTLED.** Touch-target size: the apps in
the set with genuine mobile-first pedigree cluster at 48px, and quince runs 36px stepping to 40px on
phones. quince#619 recorded this as unmeasured and it is now measured — but nothing has been decided,
and `field.ts`'s note that **quince meets no 44px bar** still stands. Dynamic Type is the other one:
a native app follows the reader's system text-size setting and quince follows nothing.

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
- **THE DOCUMENT IS THE SCROLLER. THE SHELL IS NOT, AT ANY BREAKPOINT** — Operator direction
  2026-08-11 (quince#838): *"do not use an internal scrollable container; let Safari scroll the
  document natively."* This REVERSES the `qn.6a` phone model, which pinned the shell to the viewport
  and made `<main>` the only scroll region, and which the soak confirmed as "perfect". Two filed bugs
  share that one root and neither is fixable from inside a component: a history entry records
  `window.scrollY` and has no way to record an element's `scrollTop`, so **Back could never restore a
  position** (quince#838); and a shell pinned to an exact viewport height with `overflow-hidden`
  **clips** anything past its box, with no scroller able to reach it, whenever the box and the
  visible area disagree mid-toolbar-transition (quince#649). Inner scrollers stay legitimate where
  the thing that scrolls is genuinely bounded — a dialog's card, the job-log tail — and those carry
  `overscroll-contain` themselves, which is where scroll chaining is worth stopping.
  **The acceptance signal costs nothing and cannot be faked: if the document really scrolls, Safari's
  own toolbars start hiding on scroll.** They do not when a container owns it.
- **Scroll restoration is ZERO LINES, and that is a rule rather than an accident.** Leave
  `history.scrollRestoration` at `"auto"` and let the browser restore a traversal. React Router's
  `<ScrollRestoration>` is the tempting wrong turn twice over — measured against 7.18.1: it is
  window-only (`window.scrollY` to save, `window.scrollTo` to restore, so it could never have served
  the old element scroller), and its first effect sets `"manual"`, taking restoration off the browser
  and re-implementing it over `sessionStorage`. What an SPA *does* owe is the other half: a **pushed**
  screen must be put at the top, because no new document is ever loaded to start it there
  (`useScrollReset`). **A restored offset is only as good as the height it is restored into**, so a
  section that renders nothing while its fetch is in flight shortens the page and the browser clamps
  the restore — seed from last-known-good on a remount, and keep reporting a failure as a failure.
- **`svh` for the document's own minimum height; `dvh` for a box that must track the visible area.**
  Not a contradiction of quince#659, which ruled `dvh` over `vh` for the second case. A *minimum*
  that grows when the toolbars collapse can flip a just-under-viewport page between scrollable and
  not, which re-expands the toolbars — an oscillation. `svh` is the toolbars-shown height and never
  moves. The same trap catches Tailwind's `h-screen`, which is `100vh`, the *large* viewport: **a
  landscape phone is ≥ `sm`**, so a sticky sidebar sized that way stands one toolbar taller than what
  is visible, with no scroller of its own to reach its foot.
- **A PORTALLED SURFACE MUST POSITION ITSELF AGAINST THE VISIBLE AREA, AND `AppLayout` CANNOT DO IT
  FOR IT** (quince#762). `AppLayout` pads by `env(safe-area-inset-*)`, so everything rendered inside
  the shell clears the notch and the home indicator for free. A Radix portal renders into `<body>`
  and is not a descendant of it, so it inherits none of that — which is how every dialog in the
  product came to sit under the Dynamic Island while no other surface did. It is a property of
  portalling, not of dialogs: a Select, Popover, Tooltip, Toast or Sheet added later inherits the
  same gap. Use `--safe-top` / `--safe-bottom` and `--vv-top` / `--vv-height` from `index.css`, which
  exist for exactly this, and centre within that box rather than within the layout viewport.
- **The layout viewport is not what the user can see, and on iOS only JavaScript knows the
  difference.** `viewport-fit=cover` extends the layout viewport under the notch, and iOS does not
  shrink it when the keyboard opens — it shrinks the *visual* viewport. So `position: fixed` plus
  `top: 50%` centres against a box that is partly off-screen and partly behind a keyboard. There is
  no CSS-only fix today; `useVisualViewport` publishes the real box as custom properties and the
  stylesheet does the arithmetic. Heights stay in `dvh`, never `vh` (quince#659) — for a box that
  must equal the visible area. The document's own minimum is the other case, two bullets up.
- Screenshots for README/releases come from `quince serve --demo` with fixture data —
  keep fixture data presentable (real-looking device names, plausible sizes).
