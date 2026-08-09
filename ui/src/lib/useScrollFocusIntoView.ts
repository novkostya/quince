import { useEffect, type RefObject } from "react";
import { raisesKeyboard } from "./keyboard";

// THE BROWSER'S OWN SCROLL-INTO-VIEW IS `nearest`, AND `nearest` MEANS FLUSH AGAINST THE EDGE.
//
// A dialog with a bounded height is a scroll container, so focusing a field below the fold makes the
// browser scroll it in — by the minimum possible amount, which leaves the field's edge touching the
// container's. On a phone that reads as broken rather than tight: the field the keyboard is about to
// cover sits on the boundary with its label already gone, and the next field down lands off the
// region entirely. Operator-reported on the zfs branch of the add-storage form, quince#762 — `Parent
// dataset` half-cut, `Helper command` fully out of sight.
//
// It is worse than the ordinary version of this problem because the container SHRINKS at the same
// moment: the keyboard opens, `--vv-height` drops, `max-h` follows it, and whatever the browser
// scrolled to a moment ago against the taller box is now wrong. Hence `resize` as well as `focusin`
// — the second pass is the one that counts, and it re-reads the geometry rather than remembering it.
//
// DO NOTHING WHEN NOTHING IS WRONG, WHICH IS MOST OF THE TIME AND IS THE WHOLE OF THE SECOND FIX.
// The first version corrected on every focus, and centring is a movement even when the field was
// already perfectly visible — so stepping between two adjacent fields scrolled the card a full step
// each way. Operator-reported as "unpleasant jumping when I switch between the fields" (quince#762,
// screen recording): `Parent dataset` and `Helper command` are both on screen at once in most
// frames of it, and the card moved anyway.
//
// So a field with `MARGIN` of clearance at both ends is left exactly where it is. Only a field that
// is off the region, or crowding an edge, is moved.
//
// AND THEN IT IS MOVED AS LITTLE AS POSSIBLE — to `MARGIN` of clearance and no further. The first
// version centred, on the reasoning that a nudge would land the field back on the boundary as soon
// as the keyboard resized the container. That reasoning was wrong once the tolerance above existed:
// a field parked at exactly `MARGIN` SATISFIES the test, so it is not touched again, and centring
// was simply the largest movement available where the smallest would do. Operator-reported after the
// tolerance landed — "in default position it seems ok but if you scroll a bit it's still jumping" —
// because scrolling puts a field near an edge, which is precisely when the correction fires.
//
// SYNCHRONOUSLY ON `focusin`, NOT ON THE NEXT FRAME. The browser runs its own scroll-into-view after
// the focus events and paints the result; correcting a frame later means the wrong position is drawn
// first and then snaps, which is the jump the Operator saw at the card's top edge. Doing it inside
// the event puts the content where it belongs before anything is painted, and often leaves the
// element already visible enough that the browser declines to scroll at all. The `resize` pass stays
// deferred because the container's new height is not known until the layout after it.
//
// ONLY THIS CONTAINER MOVES. `Element.scrollIntoView()` walks every scrollable ancestor and would
// let a dialog reach out and scroll the page behind its own overlay; setting `scrollTop` by hand
// cannot. On iOS that matters twice over, because the page behind is what Safari is itself scrolling
// to clear the keyboard, and two parties moving it is how a field ends up somewhere neither intended.
const MARGIN = 24; // matches the card's own padding — one comfortable gutter, not a magic number

// ONLY THINGS THAT RAISE A KEYBOARD, AND THIS IS A CORRECTNESS BOUND RATHER THAN A TIDY-UP.
//
// Focus lands on buttons too, and a button is usually focused BY A TAP. Scrolling the card inside
// that tap moves the button out from under the finger between `mousedown` and `mouseup`, the two
// land on different elements, and the browser therefore fires no `click` at all — the press is
// silently swallowed. Caught by `gates-ui-e2e`, deterministically, on both the attempt and the
// retry: `Test helper` was pressed and its result never appeared, because its handler never ran.
//
// The correction exists to keep the field the KEYBOARD IS ABOUT TO COVER in view. Nothing that
// cannot raise a keyboard needs it, so nothing else is moved, and the swallowed-tap class is closed
// rather than tuned around. `raisesKeyboard` is shared with the shell's scroll reset, which has to
// make the same judgement — see `keyboard.ts`.
export function useScrollFocusIntoView(ref: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    const container = ref.current;
    if (!container) return;

    let frame = 0;
    const centreFocused = (): void => {
      const el = document.activeElement;
      if (!(el instanceof HTMLElement) || el === container || !container.contains(el)) return;
      if (!raisesKeyboard(el)) return;

      const box = container.getBoundingClientRect();
      const field = el.getBoundingClientRect();

      // Already comfortable: leave it alone. A field too tall to hold a gutter at both ends can
      // never satisfy this, which is correct — it always needs aligning.
      const above = field.top - box.top;
      const below = box.top + box.height - (field.top + field.height);
      if (above >= MARGIN && below >= MARGIN) return;

      // A field that cannot fit between the two gutters can only be aligned, not fitted — show its
      // top, which is where the label and the caret are. Otherwise move by the shortfall alone.
      const delta =
        field.height + 2 * MARGIN >= box.height
          ? above - MARGIN
          : above < MARGIN
            ? above - MARGIN // too high: scroll back up, so the delta is negative
            : MARGIN - below; // too low: scroll down by what is missing
      // The browser clamps this to the scrollable range, so no bounds arithmetic is needed here.
      container.scrollTop += delta;
    };

    // One deferred pass per frame at most: a `resize` burst is one user action, and running per event
    // would scroll against geometry that is still changing.
    const schedule = (): void => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(centreFocused);
    };

    container.addEventListener("focusin", centreFocused);
    const vv = window.visualViewport;
    vv?.addEventListener("resize", schedule);
    return () => {
      cancelAnimationFrame(frame);
      container.removeEventListener("focusin", centreFocused);
      vv?.removeEventListener("resize", schedule);
    };
  }, [ref]);
}
