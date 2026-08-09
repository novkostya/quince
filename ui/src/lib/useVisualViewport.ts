import { useEffect } from "react";

// THE VISIBLE AREA IS NOT THE LAYOUT VIEWPORT, AND ONLY JAVASCRIPT CAN SEE THE DIFFERENCE.
//
// iOS does not shrink the LAYOUT viewport when the on-screen keyboard appears; it shrinks the VISUAL
// one and scrolls the page under it. `position: fixed` resolves against the layout viewport, so a
// fixed surface centred with CSS alone centres against a height that still counts the strip now
// hidden behind the keyboard — quince#762, and Operator-reported twice before that.
//
// There is no CSS that reads the visual viewport on iOS today, so this hook publishes it as two
// custom properties on <html> and lets the stylesheet do the arithmetic:
//
//   --vv-top      offset of the visible area from the top of the layout viewport
//   --vv-height   height of the visible area
//
// Both are cleared on unmount, so the declared fallbacks in `index.css` take over again. The hook is
// mounted BY THE DIALOG rather than by the app shell: the properties matter only while a portalled
// surface is on screen, and this way no listener runs in the common case where none is.
//
// WHY THERE IS A CLAMP AT ALL: on iOS 26 `offsetTop` does not return to 0 when the keyboard is
// dismissed — the visual viewport is restored to full height and the offset is left behind, which
// strands every fixed element away from where it belongs. Reported against physical devices; Apple's
// own forum thread is the record.
//
//   https://developer.apple.com/forums/thread/800154
//
// WHAT IT IS MEASURED AGAINST, AND WHY IT IS NOT `window.innerHeight`. The bound wants "how much of
// the visible area is hidden right now", and the first version of this file computed it as
// `innerHeight - height`. THAT IS ZERO ON THE DEVICE THIS WAS WRITTEN FOR. iOS shrinks
// `window.innerHeight` along with the visual viewport when the keyboard opens, so the difference
// collapses, every legitimate offset was clamped away, and the dialog stayed pinned to the top of
// the layout viewport while Safari scrolled the page down under the keyboard. Operator-measured on
// an iPhone (quince#762): right on first focus, because nothing had scrolled yet and the clamp bound
// nothing; visibly high on re-focus; most of the way off the top of the screen once the tall zfs
// form pushed the focused field far down. One wrong reference, three symptoms of increasing size.
//
// So the reference is the WIDEST visible height this dialog has seen, which is observed rather than
// asked for: `apply()` runs once at mount, before any keyboard, so the first reading IS the
// no-keyboard height. With the keyboard up, `widest - height` is the keyboard and the offset passes
// through; with it gone the two are equal, the bound is 0, and iOS 26's stale offset is discarded —
// which is the case the clamp exists for. No second height source, so nothing can disagree with
// `visualViewport` about what the viewport is.
//
// The one case this does not cover: a dialog MOUNTED while the keyboard is already up takes the
// shrunken height as its widest and will not offset until the keyboard closes once. Every dialog
// here opens from a button tap, which blurs any field first, so it is unreachable today — recorded
// because a Sheet or a nested dialog could reach it.
export function useVisualViewport(): void {
  useEffect(() => {
    const vv = window.visualViewport;
    // No API (jsdom, and browsers older than the Aug-2021 baseline): leave the properties unset and
    // let `index.css`'s fallbacks stand. That degrades to centring in `100dvh` minus the safe area —
    // no keyboard tracking, but never wrong about the notch.
    if (!vv) return;

    const root = document.documentElement;
    let widest = 0;
    let applied = -1;
    let settling: ReturnType<typeof setTimeout> | undefined;

    const write = (): void => {
      widest = Math.max(widest, vv.height);
      const hidden = Math.max(0, widest - vv.height);
      const top = Math.min(Math.max(vv.offsetTop, 0), hidden);
      root.style.setProperty("--vv-top", `${top}px`);
      root.style.setProperty("--vv-height", `${vv.height}px`);
      applied = vv.height;
    };

    // GROWING IS DEFERRED, SHRINKING IS NOT, AND THE ASYMMETRY IS THE WHOLE POINT.
    //
    // Moving focus from one field to another with the keyboard already up makes iOS report, for a
    // frame or two, a visible area that is back to FULL HEIGHT with no offset — as though the
    // keyboard had closed — before it reports the real one again. Taken at face value that
    // momentarily stretches the dialog to the whole screen and slides it up under the status bar,
    // then snaps it back. Operator-reported as jumping when switching fields, and visible in the
    // screen recording on quince#762 as a one-frame taller card with a scrollbar down its side.
    //
    // A shrink is always real and always urgent: it is the keyboard arriving, and a late response
    // means the dialog sits under it. A growth is either the keyboard leaving — in which case
    // `SETTLE_MS` of delay is invisible, since the keyboard is still animating out over roughly that
    // long — or this transient, in which case waiting is exactly right. So the delay costs nothing in
    // the honest case and removes the dishonest one.
    const SETTLE_MS = 250;
    const apply = (): void => {
      clearTimeout(settling);
      // `applied < 0` is the first reading, which must land immediately or the dialog opens against
      // the stylesheet fallbacks and corrects itself a quarter-second later.
      if (applied >= 0 && vv.height > applied) {
        settling = setTimeout(write, SETTLE_MS);
        return;
      }
      write();
    };

    apply();
    // `resize` is the keyboard opening or closing; `scroll` is the page being pushed under it, which
    // moves the visible area without changing its size. Both change where the dialog belongs.
    vv.addEventListener("resize", apply);
    vv.addEventListener("scroll", apply);
    return () => {
      clearTimeout(settling);
      vv.removeEventListener("resize", apply);
      vv.removeEventListener("scroll", apply);
      root.style.removeProperty("--vv-top");
      root.style.removeProperty("--vv-height");
    };
  }, []);
}
