import { useEffect, type RefObject } from "react";

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
// is off the region, or crowding an edge, is moved — and then it is CENTRED rather than nudged,
// because a nudge lands it back on the boundary the moment the keyboard resizes the container. Two
// different questions, deliberately: *should this move* has a tolerance, *where should it go* does not.
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

export function useScrollFocusIntoView(ref: RefObject<HTMLElement | null>): void {
  useEffect(() => {
    const container = ref.current;
    if (!container) return;

    let frame = 0;
    const centreFocused = (): void => {
      const el = document.activeElement;
      if (!(el instanceof HTMLElement) || el === container || !container.contains(el)) return;

      const box = container.getBoundingClientRect();
      const field = el.getBoundingClientRect();

      // Already comfortable: leave it alone. A field taller than the region can never satisfy this,
      // which is correct — it always needs aligning.
      const above = field.top - box.top;
      const below = box.top + box.height - (field.top + field.height);
      if (above >= MARGIN && below >= MARGIN) return;

      // A field taller than the region can only be aligned, not centred — show its top, which is
      // where the label and the caret are.
      const delta =
        field.height >= box.height
          ? above
          : field.top + field.height / 2 - (box.top + box.height / 2);
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
