import { raisesKeyboard } from "./keyboard";

// THE BOTTOM OF THE PAGE IS CLIPPED AND A GAP APPEARS BELOW IT — quince#649, and this is the first
// reproduction anyone has had of it.
//
// WHAT HAPPENS. iOS scrolls the visual viewport to clear the keyboard, and on iOS 26 it does not
// always put it back when the keyboard closes (developer.apple.com/forums/thread/800154 — the same
// leftover offset `useVisualViewport` discards for a dialog's own frame). The app shell is
// `position`-independent but height-pinned: `html, body, #root` are `100dvh` and the shell is
// `overflow-hidden` with `<main>` as the only scroll region. So the shell keeps occupying the top of
// the LAYOUT viewport while the visible window has moved down it.
//
// That produces both halves of the report at once, which is why it is worth stating precisely: the
// shell's bottom edge is now ABOVE the bottom of the screen by exactly the leftover offset, so a gap
// of that height appears below it — and the content that has scrolled off the top of the shell is
// unreachable, because the document does not scroll and `<main>` is not what moved. quince#649 was
// filed against `dvh` and toolbar transitions, which is a real hazard and a different one: a
// `dvh`/toolbar mismatch makes the shell TALLER THAN THE SCREEN and clips with no gap. A gap is the
// signature of a shift.
//
// THE FIX IS TO PUT IT BACK — AND THIS MAY NOT DO IT. `window.scrollTo(0, 0)` was chosen on the
// belief that on iOS the window scroll offset IS the visual viewport's, so it would work on a
// document with nothing to scroll.
//
// MEASURED IN SAFARI WITH `?vvdebug`, AND IT CONTRADICTS THAT: `scrollY=0` throughout, while
// `visualViewport.offsetTop` ran 153 → 43 → 6 → 0. The two are plainly different quantities there,
// so `scrollTo(0, 0)` has nothing to undo and this reset is probably INERT.
//
// It is kept, and labelled, rather than deleted or quietly left claiming to work. The gap was seen
// in the home-screen PWA and the measurement was taken in Safari; whether `scrollY` tracks the
// offset in standalone is unmeasured, and one run of `?vvdebug` from the PWA settles it. Until then
// this is an UNVERIFIED fix for quince#649 and must not be reported as one. Its unit tests prove the
// decision logic — when it fires and when it declines — and prove nothing about whether the call
// moves anything.
//
// WHY IT WAITS FOR THE FOCUS RATHER THAN A TIMER. Moving between two fields with the keyboard up
// makes iOS report full height for a frame, as though the keyboard had closed. Resetting the scroll
// there would yank the page while the user is still typing. The test is therefore not "is the
// viewport full height" alone but "and is nothing that raises a keyboard focused" — during a switch
// something always is, and when the keyboard truly goes, nothing is. That reads the actual condition
// instead of guessing at a duration.
//
// Imperative and global, in the shape `initTheme` already set: it is a property of the document, it
// must cover the login and onboarding screens as well as the shell, and it holds no React state.
export function initKeyboardScrollReset(): () => void {
  const vv = window.visualViewport;
  if (!vv) return () => {};

  // The tallest visible area seen — the no-keyboard height, observed rather than assumed, exactly as
  // `useVisualViewport` does it.
  let widest = vv.height;

  const onResize = (): void => {
    widest = Math.max(widest, vv.height);
    if (vv.height < widest) return; // the keyboard is still up

    const focused = document.activeElement;
    if (focused instanceof HTMLElement && raisesKeyboard(focused)) return; // a switch, not a close

    if (vv.offsetTop === 0 && window.scrollY === 0) return; // nothing was left behind
    window.scrollTo(0, 0);
  };

  vv.addEventListener("resize", onResize);
  return () => vv.removeEventListener("resize", onResize);
}
