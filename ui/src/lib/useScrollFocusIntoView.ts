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
// CENTRING RATHER THAN A MARGIN, because a margin is a guess about how much of the field's context
// matters and centring is not. It also costs nothing when it is not needed: with content that fits,
// `scrollTop` is pinned at 0 and every line below is a no-op.
//
// ONLY THIS CONTAINER MOVES. `Element.scrollIntoView()` walks every scrollable ancestor and would
// let a dialog reach out and scroll the page behind its own overlay; setting `scrollTop` by hand
// cannot. On iOS that matters twice over, because the page behind is what Safari is itself scrolling
// to clear the keyboard, and two parties moving it is how a field ends up somewhere neither intended.
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
      // A field taller than the region can only be aligned, not centred — show its top, which is
      // where the label and the caret are.
      const delta =
        field.height >= box.height
          ? field.top - box.top
          : field.top + field.height / 2 - (box.top + box.height / 2);
      // The browser clamps this to the scrollable range, so no bounds arithmetic is needed here.
      container.scrollTop += delta;
    };

    // One pass per frame at most: `focusin` and the viewport `resize` that follows it are the same
    // user action, and running twice would scroll against geometry mid-change.
    const schedule = (): void => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(centreFocused);
    };

    container.addEventListener("focusin", schedule);
    const vv = window.visualViewport;
    vv?.addEventListener("resize", schedule);
    return () => {
      cancelAnimationFrame(frame);
      container.removeEventListener("focusin", schedule);
      vv?.removeEventListener("resize", schedule);
    };
  }, [ref]);
}
