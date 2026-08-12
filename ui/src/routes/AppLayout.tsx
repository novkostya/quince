import { useEffect } from "react";
import { Outlet } from "react-router-dom";
import { ReconcilingNotice } from "@/components/ReconcilingNotice";
import { Sidebar } from "@/components/Sidebar";
import { close, connect } from "@/ws/client";
import { useScrollReset } from "./useScrollReset";
import { useTrackNavigation } from "@/components/BackLink";

// The authed shell. The WebSocket bridge lives only here — it connects on mount (once
// authenticated) and closes on unmount (logout / auth loss).
export function AppLayout() {
  useEffect(() => {
    connect();
    return () => close();
  }, []);

  // A new screen opens at the top; Back is left to the browser. See the hook — the `POP` case is
  // deliberately empty.
  useScrollReset();

  // Records where each navigation came FROM, so an in-page back link can tell whether going back
  // would land on the destination it names. Once, here, because there is one history per document.
  useTrackNavigation();

  // ONE SCROLL MODEL, EVERY BREAKPOINT: THE DOCUMENT SCROLLS. Operator direction 2026-08-11
  // (quince#838) — no internal scrollable container; Safari scrolls the page natively, as it does on
  // an ordinary website. The desktop half of this was already true; the phone half is the change,
  // and it is a deliberate REVERSAL of the `qn.6a` soak fix rather than an oversight. The full
  // argument, both bugs it closes and why `svh` rather than `dvh`, is in `index.css`.
  //
  // WHAT IT BUYS, AND IT IS NOT A PREFERENCE: a history entry records `window.scrollY` and nothing
  // else, so only a scrolling DOCUMENT can be restored on a Back traversal — no component can supply
  // that for an element scroller. Safari's toolbars also hide on scroll again, which is both what the
  // Operator asked for and a free acceptance signal: if they still don't, the shell is still the
  // scroller.
  //
  // THE TOP BAR IS `position: fixed` ON A PHONE, so it is OUT OF FLOW and covers what is beneath it
  // — which is why the shell reserves `--bar-h` as padding below. That pair is the whole contract
  // and it is one variable rather than two constants, because a bar height and a content offset that
  // disagree is a permanent overlap nobody notices in review. The e2e asserts `<main>` never begins
  // above the bar's bottom edge, at four widths on six routes, so it cannot fail quietly.
  //
  // It went in-flow → `sticky` → `fixed` across three Operator device tests, and `Sidebar` carries
  // that sequence with what each one fixed and what it did not.
  //
  // `min-h-svh` STILL DOES NOT OVERSHOOT, which is worth stating because adding padding to a
  // viewport-height box usually does: `box-sizing: border-box` is global here, so the minimum
  // INCLUDES the padding and a short page is exactly one screen rather than one screen plus a bar.
  // Get this wrong and every page in the product gains a bar's worth of pointless scroll.
  //
  // THE SHELL NO LONGER HOLDS THE TOP INSET AT ALL, and that is quince#838's last Operator request.
  // It used to carry `sm:pt-[var(--safe-top)]` so that both children cleared the status bar at once
  // — correct, and it left the sidebar's background starting BELOW the status bar with a strip of
  // page behind it. In a home-screen PWA that reads as a gap, which is what was reported.
  //
  // So each child clears the inset ITSELF: the aside as its own padding (its background therefore
  // reaches the screen edge while its contents stay put), and `<main>` folded into the padding
  // below. **They have to move together** — leaving the inset here as well would double it for the
  // aside, and removing it without giving `<main>` its own would put a heading under the status bar
  // on an iPad in portrait, which is ≥ sm and has one.
  //
  // min-w-0 lets wide children (logs, tables) scroll inside themselves, not the page.
  return (
    // Safe-area insets apply at EVERY breakpoint (env() is 0 on real desktops): a LANDSCAPE phone is
    // ≥ sm, so it uses the row layout yet still has a side notch / home indicator — zeroing the insets
    // on sm would put content under the notch (qn.6a soak fix). Sides on the shell at every
    // breakpoint, TOP at sm only (see above), and the bottom on <main> and the sidebar.
    //
    // `min-h-svh` replaces `h-full` + `sm:min-h-screen`: a MINIMUM cannot clip (quince#649) where an
    // exact height could, and `svh` cannot oscillate as the toolbars move (see `index.css`).
    <div className="flex min-h-svh flex-col bg-bg pl-[env(safe-area-inset-left)] pr-[env(safe-area-inset-right)] pt-[var(--bar-h)] text-fg sm:flex-row sm:pt-0">
      <Sidebar />
      {/* `sm:pt-[calc(2rem+…)]` RATHER THAN `sm:p-8`, and the split is required rather than tidy: two
          utilities setting `padding-top` leave which one wins to stylesheet order, so the sides and
          the foot are set separately and the top is set once. The sum is what `sm:p-8` gave plus the
          inset the shell used to hold — the same y as before, moved one element down. */}
      <main className="min-w-0 flex-1 p-4 pb-[max(1rem,env(safe-area-inset-bottom))] sm:px-8 sm:pb-8 sm:pt-[calc(2rem+var(--safe-top))]">
        {/*
          IN THE SHELL RATHER THAN PER PAGE (qn.6i). The state is daemon-wide and affects every
          surface that counts or lists versions — Devices, a device's versions, the storage cards —
          so one notice above the outlet covers them all and cannot drift page by page. It renders
          nothing when false, which is almost always.
        */}
        <ReconcilingNotice />
        <Outlet />
      </main>
    </div>
  );
}
