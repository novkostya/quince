import { Link, useLocation } from "react-router-dom";
import { House, Settings as SettingsIcon, type LucideIcon } from "lucide-react";
import { useConnectionStore } from "@/stores/connection";
import { ConnBadge } from "./ConnBadge";
import { SignOutButton } from "./SignOutButton";

// `Home`, not `Devices` (Operator ruling, quince#443). `Devices` had stopped describing its own
// page once storage moved onto it, and the replacement names the POSITION rather than the contents —
// a label naming what is on the page goes stale the next time the page grows, and this one will.

// `/settings` must not be matched by `/settingsomething`, which `startsWith` alone would do.
const under = (pathname: string, base: string): boolean =>
  pathname === base || pathname.startsWith(`${base}/`);

// A NAV ITEM OWNS A SECTION, NOT A PATH — AND THIS IS THE FLICKER, NOT A RENDERER FAULT.
//
// These were `NavLink`s, and `Home` carried `end` so that it matched `/` exactly. The consequence
// nobody looked at: on `/devices/:udid`, `/storage/:name` and `/storage/new`, `Home` is not active
// and `Settings` is not active either, so **no item is lit at all**. The selected pill therefore
// VANISHED on every push into a detail screen and came back on every pop — a bar that genuinely
// changes between two screens, which is seen as the bar flickering.
//
// Found by reading `springback`'s `docs/ios-spa-notes.md` §4 after the Operator sent a screen
// recording of it, where it is called out as "very likely present in any tabbed app". It is the
// second thing that file says about this symptom; the first is §3, directly above in this file.
// Neither is about compositing, and this one is not fixable by any amount of positioning work.
//
// The fix is also simply more correct: a detail screen you opened from Home is still Home.
const NAV: { to: string; label: string; icon: LucideIcon; owns: (pathname: string) => boolean }[] = [
  {
    to: "/",
    label: "Home",
    icon: House,
    // Devices and storages are reached FROM Home and have no nav item of their own, so Home keeps
    // the light while you are in one. `/devices` also still resolves as a redirect (quince#443).
    owns: (p) => p === "/" || under(p, "/devices") || under(p, "/storage"),
  },
  { to: "/settings", label: "Settings", icon: SettingsIcon, owns: (p) => under(p, "/settings") },
];

// Responsive nav: a horizontal top bar on phones, the vertical left sidebar on desktop (qn.6a mobile
// pass). Same links, one component — Tailwind flips flex-row → flex-col at the sm breakpoint.
//
// `fixed` ON A PHONE, NOT `sticky` — AND THE THIRD ANSWER TO ONE SYMPTOM, SO THE SEQUENCE IS KEPT
// (quince#838). The Operator reported the bar "disappearing for milliseconds" on push/pop and sent
// two screen recordings. Stepping through them frame by frame, which is the only instrument that
// worked here:
//
//   1. `transform-gpu` — I ADDED it as the standard repaint-flicker advice, and the first recording
//      shows the bar absent for ~2 frames. Removed; never reapply it (the block below says why).
//   2. the nav pill going out on detail routes — a bar that CHANGES, not one that fails to paint.
//      Fixed by `owns` above, and the second recording confirms it: `Home` stays lit throughout.
//   3. STILL ONE FRAME MISSING, with both of those fixed. That is this change.
//
// WHY `sticky` COULD NOT BE MADE TO WORK: a sticky element is bounded by its containing block. A
// route change swaps the outlet, so for a frame the shell can be SHORTER than the offset the
// document still holds, and the bar is pushed out of view until the browser clamps. Nothing inside
// the bar can prevent that, because the bound is a property of the ancestor. A `fixed` element has
// no such bound — it is not in the scroll at all.
//
// AND IT IS WHAT THE SIBLING PROJECT ACTUALLY DOES. Its notes say fixed and sticky are "the same
// risk class", which is true of the visual-viewport hazard and is why this was not the first move;
// its stylesheet nonetheless says `position: fixed` with `body { padding-top: var(--header-h) }`.
// The notes describe the risk, the code records what survived it. Read both.
//
// IT IS ALSO WHY THE BOUNCE COULD COME BACK. Out of the scroll means an overscroll has no bound to
// drag it past, so `index.css` no longer suppresses the rubber-band — which the Operator asked for
// in as many words. One change, both problems, and the sibling's stylesheet has no
// `overscroll-behavior` rule anywhere either.
//
// THE HEIGHT IS `--bar-h`, DECLARED IN `index.css` AND USED TWICE: here, and as the shell's
// `padding-top`, because a fixed element covers what is under it. One variable rather than two
// constants, so they cannot drift; the e2e asserts `<main>` never begins above this bar's bottom
// edge, at every width, so a drift is a failure rather than an overlap somebody notices later.
//
// `--safe-top` IS PADDING, NOT MARGIN: in standalone the bar draws UNDER the status bar and its own
// background is what clears it. `var()` rather than `env()` so a test can stand the inset up at all
// — headless reports every inset as 0, so a gate written against `env()` proves only the fallback.
// At `sm:` this resets and the shell carries the inset, because there the aside is a column BESIDE
// `<main>` rather than above it, and `<main>` needs the clearance too.
//
// NEVER PROMOTE THIS BAR TO ITS OWN COMPOSITOR LAYER. `transform: translateZ(0)` (Tailwind's
// `transform-gpu`) and `will-change: transform` are the standard advice for repaint flicker on a
// sticky header, I applied it here, and it is WRONG for this element — measured on a device by the
// sibling project and written up in its `docs/ios-spa-notes.md` §3:
//
//   "it made the header disappear outright for a frame during a back-swipe — a sticky element on
//    its own compositor layer can be composited with the page's new scroll offset applied but its
//    sticky adjustment not yet recomputed, so it is drawn at its static position hundreds of pixels
//    above the viewport. A bar that shifts 2px is a nit. A bar that vanishes is a bug."
//
// So the promotion is removed rather than tuned, and this comment is the guard: the next person to
// see this flicker will reach for exactly that utility, because every article about sticky headers
// recommends it.
//
// IT WAS ALSO NOT THE WHOLE CAUSE. The nav ownership above (§4) was a second, independent fault, and
// `position: fixed` at the top of this block is the third. Three answers to one reported symptom,
// none of which superseded the others — which is the reason all three are still written down.
//
// `justify-between` IS THE SPACING, and it is asked to do one specific thing (Operator, quince#838:
// "equal spacings between logo and tabs, tabs and right elements"). The nav used to be `flex-1`, so
// it swallowed every spare pixel and the bar read as logo|tabs...............|status. Free space now
// divides equally between the two gaps. It is visually equal as well as geometrically, because both
// ends of the nav are a pill with the same `px-3`, so the same 12px is added inside each gap.
//
// AT `sm:` THE COLUMN SPANS THE WHOLE VISIBLE VIEWPORT AND CARRIES THE INSETS ITSELF — Operator-
// asked, quince#838, from an iPad in both Safari and the home-screen PWA. Two requests, one shape:
//
//   "left panel height is fixed when scrolled … make it higher, so that bottom elements are almost
//    at the bottom" — Safari, where the toolbars collapse and the visible area GROWS.
//   "in PWA there should be no visible gaps and left panel should occupate whole height including
//    safe area (elements should stay where they are though)" — standalone, where the panel's
//    background stopped short of the status bar at the top and the home indicator at the bottom.
//
// `sm:h-dvh`, AND THIS CORRECTS MY OWN `svh` FROM THE COMMIT BEFORE. `dvh` equals the SMALL viewport
// while the toolbars are shown and the LARGE one once they retract, so it tracks what is visible in
// both states — which is exactly what a pinned column must do, and is what `AuthPage` already argues
// at length for the same reason. `svh` is right for the DOCUMENT's minimum height (it must not
// oscillate) and wrong for this box: sized to `svh`, the column stayed at the toolbars-shown height
// after they collapsed, leaving the gap the Operator photographed. `ui.design.md` draws exactly this
// distinction and I mis-applied it here.
//
// It also retires the `lvh` hazard rather than trading against it: Tailwind's `h-screen` is `100vh`
// = the LARGE viewport, which on a LANDSCAPE PHONE (≥ sm) stands one toolbar taller than what is
// visible and puts Sign out where no scroller can reach it — quince#649's shape. `dvh` is never
// taller than visible, so it is strictly better than both.
//
// `sm:top-0` + PADDING RATHER THAN A SHORTER BOX. The previous commit sized this column to
// `calc(100svh - var(--safe-top))` and offset it, because the SHELL reserved the top inset. That
// fixed the scroll-by-the-status-bar-height defect and left the panel's background starting below
// the status bar — correct arithmetic, visible seam. The inset now sits on this element as padding,
// so the background reaches the screen edge while the content sits exactly where it did:
// `top: 0` + `padding-top: inset` puts the first control at the same y as `top: inset` did.
// `sm:pb-[var(--safe-bottom)]` does the same at the foot for the home indicator.
//
// THE SHELL THEREFORE NO LONGER PADS THE TOP AT `sm:`, and `<main>` picks that up itself — see
// `AppLayout`. Both halves have to move together or one of them loses its notch clearance.
//
// IT STILL CANNOT AFFECT A PHONE OR A MAC, which is the Operator's standing condition and is
// provable rather than hopeful. A phone is below `sm`: none of these utilities apply, and the base
// `pt-[var(--safe-top)]` it does use is unchanged. A Mac reports every inset as 0, so the padding is
// `0px` on both edges and `dvh` = `svh` = `vh` where there are no dynamic toolbars — the same box,
// in the same place. Only a surface with a non-zero inset or a collapsing toolbar can see any
// difference, which is the iPad this came from.
//
// WHAT CI CANNOT PROVE, STATED RATHER THAN IMPLIED: headless has no dynamic toolbars, so `dvh`,
// `svh` and `lvh` are all the viewport height there and no gate can distinguish them. The e2e pins
// the half that IS testable — the column spans the viewport exactly, and its contents clear the
// insets — and the toolbar-collapse half is owed to the device.
//
// NO `flex-wrap` ANY MORE, AND THE BACKSTOP IT PROVIDED MOVED RATHER THAN VANISHED. This bar's
// contents were ~390px of min-content, so below 390 it pushed the DOCUMENT sideways — 15px at 375,
// 70px at 320, identically on every route, which is how you could tell it was the shell and not a
// page. It had been invisible because the old shell was `overflow-hidden` and simply CLIPPED it.
//
// `flex-wrap` was the structural answer then: anything that would not fit took a second LINE, so the
// property held whatever the labels grew into. It is incompatible with a FIXED HEIGHT — a second row
// would be clipped rather than shown, and would slide under content the shell's padding no longer
// accounts for. So the bar is one row by construction now, and two things hold it: the wordmark
// yields below 360px (see below), and the e2e sweeps four real widths on six routes for document
// overflow AND asserts `<main>` never starts above this bar's bottom edge.
//
// `overflow-x: hidden` is still NOT available: it breaks scroll restoration (blank viewport on a
// traversal until you touch the screen), which is the one thing this whole change exists to deliver.
export function Sidebar() {
  const version = useConnectionStore((s) => s.serverVersion);
  const { pathname } = useLocation();
  return (
    <aside
      className="fixed inset-x-0 top-0 z-30 flex h-[var(--bar-h)] shrink-0 flex-row items-center justify-between gap-1 border-b border-line bg-card px-3 pt-[var(--safe-top)] sm:sticky sm:inset-x-auto sm:top-0 sm:z-auto sm:h-dvh sm:w-[var(--sidebar-w)] sm:flex-col sm:items-stretch sm:justify-normal sm:gap-0 sm:self-start sm:border-b-0 sm:border-r sm:px-0 sm:pb-[var(--safe-bottom)]"
      aria-label="Primary"
    >
      {/* THE WORDMARK IS ON THE PHONE BAR — Operator ruling, quince#838, on the plain ground that
          they like it there. It had been dropped as the cheapest ~63px when the bar was measured too
          wide; taste about the product's own name is the Operator's call, and the width was found
          elsewhere. What pays for it here: `px-1` is gone at this breakpoint, since the aside's
          `px-3` and its `gap-1` already separate this from the nav.
          `shrink-0` because it has no fold to give — squeezing it would clip the name rather than
          reflow it, so anything that must give ground gives it in the nav instead.

          AND IT YIELDS BELOW 360px, which is the one place the bar cannot hold everything on one
          row: contents are ~326px of min-content against 296px of usable width at 320. The bar has a
          FIXED height now (the shell reserves exactly that much), so a second row would be clipped
          rather than shown — something has to go, and this is the only element here that is not a
          control. 360 is below every viewport of a phone running a current iOS; the narrowest is
          375, where the wordmark stays. */}
      <div className="hidden shrink-0 min-[360px]:block sm:px-5 sm:pb-5 sm:pt-6">
        <div className="text-base font-semibold tracking-tight sm:text-lg">quince</div>
        <div className="hidden font-mono text-xs text-subtle sm:block" data-testid="version">
          {version ? `v${version}` : "—"}
        </div>
      </div>

      {/* `min-w-0` so this can actually give ground: a flex item defaults to `min-width: auto`, which
          is min-content, and an item that refuses to shrink below its min-content is exactly how a
          bar pushes the page sideways instead of wrapping. */}
      <nav className="flex min-w-0 flex-row gap-1 sm:flex-col sm:px-3">
        {NAV.map(({ to, label, icon: Icon, owns }) => {
          const current = owns(pathname);
          return (
            // `Link` RATHER THAN `NavLink`, because `NavLink`'s whole contribution is the `isActive`
            // it computes and that is exactly what is being replaced. Keeping it would leave two
            // notions of "active" in one element — its `aria-current` from a path match, and the
            // pill from `owns` — which is how a screen reader and a sighted user end up being told
            // different things about the same nav.
            <Link
              key={to}
              to={to}
              aria-current={current ? "page" : undefined}
              className={
                "flex min-h-[40px] items-center gap-2.5 rounded-lg px-3 py-2 text-sm transition-colors " +
                (current
                  ? "bg-accent-soft font-medium text-accent"
                  : "text-muted hover:bg-elevated hover:text-fg")
              }
            >
              <Icon size={18} strokeWidth={1.75} />
              {label}
            </Link>
          );
        })}
      </nav>

      {/* Status first, then the action — and sign out is LAST on purpose, at the far end of the bar
          on a phone and the bottom of the column on desktop. It is the one control here whose
          misfire costs the user something, so it does not sit adjacent to Home and Settings. */}
      {/* No `px-1` at this breakpoint any more: it made the right edge 16px from the screen where
          the wordmark's left edge is 12px, which is half of what "unbalanced" was. The aside's own
          `px-3` is the gutter on both sides. */}
      <div className="flex items-center gap-1 sm:mt-auto sm:flex-col sm:items-stretch sm:gap-2 sm:px-3 sm:py-4">
        <div className="sm:px-2">
          <ConnBadge />
        </div>
        <SignOutButton />
      </div>
    </aside>
  );
}
