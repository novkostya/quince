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

    // A DEFERRAL USED TO SIT HERE AND IT WAS BUILT ON A MISREADING. Growth was held back 250ms, to
    // discard a supposed one-frame "keyboard closed" report while focus moves between fields. The
    // evidence for that transient was a taller card with a scrollbar in a screen recording; the
    // Operator has since said the scrollbar appears only when THEY scroll, so those frames were a
    // manual scroll and not the artefact I read into them.
    //
    // The delay was not merely unnecessary, it was VISIBLE: tapping `Check` dismisses the keyboard
    // and grows the form in one gesture, so for a quarter of a second the dialog stayed squeezed
    // into the old keyboard-sized box with its text clipped mid-sentence, and then expanded.
    // Measured in the next recording, two frames at 12fps. Removed rather than tuned — a guess that
    // costs something visible and fixes nothing is worse than no guess.
    const apply = (): void => {
      widest = Math.max(widest, vv.height);
      const hidden = Math.max(0, widest - vv.height);
      const top = Math.min(Math.max(vv.offsetTop, 0), hidden);
      root.style.setProperty("--vv-top", `${top}px`);
      root.style.setProperty("--vv-height", `${vv.height}px`);
    };

    apply();
    // `resize` is the keyboard opening or closing; `scroll` is the page being pushed under it, which
    // moves the visible area without changing its size. Both change where the dialog belongs.
    vv.addEventListener("resize", apply);
    vv.addEventListener("scroll", apply);
    return () => {
      vv.removeEventListener("resize", apply);
      vv.removeEventListener("scroll", apply);
      root.style.removeProperty("--vv-top");
      root.style.removeProperty("--vv-height");
    };
  }, []);
}
