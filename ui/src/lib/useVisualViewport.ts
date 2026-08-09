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
// WHY THE CLAMP, WHICH IS NOT DEFENSIVE PROGRAMMING BUT A NAMED BUG. On iOS 26 `offsetTop` does not
// return to 0 when the keyboard is dismissed — the visual viewport is restored to full height and
// the offset is left behind, which strands every fixed element too high up the screen. Reported
// against physical devices; Apple's own forum thread is the record.
//
//   https://developer.apple.com/forums/thread/800154
//
// `innerHeight - height` is how much of the layout viewport is currently hidden, so it is the
// largest offset that can be legitimate. With the keyboard up it is the keyboard's height and the
// clamp does nothing; with the keyboard gone it is 0, which is precisely the value iOS 26 fails to
// restore. The clamp is therefore correct on every version rather than a workaround for one.
export function useVisualViewport(): void {
  useEffect(() => {
    const vv = window.visualViewport;
    // No API (jsdom, and browsers older than the Aug-2021 baseline): leave the properties unset and
    // let `index.css`'s fallbacks stand. That degrades to centring in `100dvh` minus the safe area —
    // no keyboard tracking, but never wrong about the notch.
    if (!vv) return;

    const root = document.documentElement;
    const apply = (): void => {
      const hidden = Math.max(0, window.innerHeight - vv.height);
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
