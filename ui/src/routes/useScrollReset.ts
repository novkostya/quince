import { useLayoutEffect, useRef } from "react";
import { useLocation, useNavigationType } from "react-router-dom";

// A NEW SCREEN STARTS AT THE TOP; A SCREEN YOU CAME BACK TO DOES NOT. That is the whole of this
// hook, and the second half is the part that is achieved by NOT writing code.
//
// WHY IT IS NEEDED AT ALL, once the document scrolls (quince#838). A multi-page site gets this for
// free: a new document begins at offset 0. A single-page app never gets a new document, so a `push`
// leaves `window.scrollY` exactly where the previous screen left it — which is the second symptom
// the architect reported on quince#838 and the one the issue's own stated mechanism could not
// explain: *"a details page sometimes opens already scrolled down."* It survives the move to
// document scrolling untouched, because it was never about which element scrolled.
//
// AND WHY THE `POP` BRANCH DOES NOTHING. `history.scrollRestoration` defaults to `"auto"`, and on a
// traversal the browser puts the document back where that entry left it — Safari's own machinery,
// which is the thing the Operator asked for. Every line we could add here is a line that fights it.
//
// SO NOT `<ScrollRestoration>`, AND THIS WAS MEASURED RATHER THAN ASSUMED (react-router 7.18.1,
// `dist/development/chunk-KS7C4IRE.mjs`). It answers both of the issue's open questions at once:
//
//   1. It is WINDOW-ONLY — `() => window.scrollY` to save and `window.scrollTo(0, …)` to restore.
//      It has no concept of an element scroller, so it could never have restored the old `<main>`.
//      Moving to the document is a prerequisite, not an alternative.
//   2. Its very first effect is `window.history.scrollRestoration = "manual"`, taking restoration
//      away from the browser and re-implementing it over `sessionStorage`. That is precisely the
//      trap named in the field notes on quince#838 — *"you'll nod, then write `manual` because you
//      want control … if you're typing `manual`, stop."* Correct restoration here is zero lines.
//
// A `REPLACE` THAT KEEPS THE PATH IS NOT A NEW SCREEN. `?next=` on the login redirect and any future
// query-only state change land as a replace on the same pathname; scrolling those to the top would
// move the page under a user who navigated nowhere. The pathname is what decides.
//
// THE FIRST RENDER IS ALSO NOT A NEW SCREEN — `previous` starts at the current pathname, so a cold
// load and a reload both fall through to the browser, which is what restores a reloaded page.
//
// SCOPE: this runs in `AppLayout`, so it covers every route inside the authed shell — every page
// long enough to scroll. The pre-shell routes (login, setup, the two onboarding steps) are
// single-screen forms reached one at a time and are deliberately not covered; if one of them ever
// grows a fold, it needs a shared root route rather than a second copy of this.
export function useScrollReset(): void {
  const { pathname } = useLocation();
  const navigationType = useNavigationType();
  const previous = useRef(pathname);

  // BEFORE PAINT, not after. A `useEffect` would let the browser draw the new screen at the old
  // offset for one frame and then jump, which is the flicker this exists to avoid.
  useLayoutEffect(() => {
    const changed = previous.current !== pathname;
    previous.current = pathname;
    if (navigationType === "POP") return;
    if (!changed) return;
    window.scrollTo(0, 0);
  }, [pathname, navigationType]);
}
