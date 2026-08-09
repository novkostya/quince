import { describe, it, expect, afterEach } from "vitest";
import { renderHook } from "@testing-library/react";
import { useVisualViewport } from "./useVisualViewport";

// THE CASE THE BROWSER TESTS CANNOT REACH.
//
// The e2e suite drives a real visible-area shrink, which is a faithful stand-in for the keyboard's
// effect on `height` — and no stand-in at all for its effect on `offsetTop`, which only appears when
// the browser SCROLLS the page under the keyboard. Headless never does, so the offset path is
// exercised there at exactly one value, 0. These tests are the only place the arithmetic between a
// non-zero offset and a shrunken height is checked at all.
//
// A fake `visualViewport` is the only way to state those numbers. It is not a simulation of iOS — it
// is the readings iOS produces, written down.
//
// THIS HEADER USED TO CLAIM THESE TESTS CAUGHT A SHIPPED BUG. They did not, and the retraction
// belongs here rather than only in the source file (quince#791 review). The claim was that a clamp
// against `window.innerHeight` "discarded every non-zero offset" on the device — but `innerHeight`
// does not shrink there, so that bound was ~444 and an offset of 200 passed it untouched. The old
// formula would have behaved identically on hardware. The 3-of-5 failure once cited as proof was an
// artefact of the mock below forcing `innerHeight` to a value iOS never produces.
type FakeViewport = { height: number; offsetTop: number };

function install(v: FakeViewport): (next: Partial<FakeViewport>) => void {
  const listeners = new Set<() => void>();
  const vv = {
    ...v,
    addEventListener: (_: string, cb: () => void) => listeners.add(cb),
    removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
  };
  Object.defineProperty(window, "visualViewport", { value: vv, configurable: true });
  return (next) => {
    Object.assign(vv, next);
    for (const cb of listeners) cb();
  };
}

function published(): { top: string; height: string } {
  const s = document.documentElement.style;
  return { top: s.getPropertyValue("--vv-top"), height: s.getPropertyValue("--vv-height") };
}

afterEach(() => {
  Reflect.deleteProperty(window, "visualViewport");
  document.documentElement.removeAttribute("style");
});

describe("useVisualViewport publishes the visible area", () => {
  it("passes the offset through while the keyboard hides part of the viewport", () => {
    // iPhone-class: 874 tall with no keyboard, 430 with one, and the browser has scrolled 200px so
    // the focused field clears it.
    const update = install({ height: 874, offsetTop: 0 });
    renderHook(() => useVisualViewport());
    expect(published()).toEqual({ top: "0px", height: "874px" });

    // `window.innerHeight` IS SET TO A VALUE iOS NEVER PRODUCES, DELIBERATELY, AND THAT IS THE
    // ASSERTION. Measured with `?vvdebug` on the device: `inner` stays 714 in Safari and 812 in the
    // standalone PWA while `vv.height` is 377 and 471 — it does NOT track the keyboard. Forcing it to
    // the shrunken height here is therefore a lie the hook must not be able to hear: it reads
    // `widest`, observed from `visualViewport` itself, and nothing else. Reintroduce an
    // `innerHeight`-based bound and this value collapses it to 0, the offset is clamped away, and
    // this test fails.
    //
    // It said the opposite until quince#791 review — "that is what iOS reports" — in the same diff
    // whose source file spends thirty lines retracting exactly that belief. Two files in one change
    // disagreeing about a measured fact, with the false one in the file a reader consults to learn
    // what iOS does.
    window.innerHeight = 430;
    update({ height: 430, offsetTop: 200 });
    expect(published()).toEqual({ top: "200px", height: "430px" });
  });

  it("discards a stale offset once the keyboard is gone — iOS 26, forums/thread/800154", () => {
    const update = install({ height: 874, offsetTop: 0 });
    renderHook(() => useVisualViewport());

    update({ height: 430, offsetTop: 200 });
    expect(published().top).toBe("200px");

    // The keyboard closes: full height back, offset left behind. Nothing is hidden, so no offset can
    // be legitimate and the dialog belongs at the top of the visible area. Applied at once — a
    // deferral lived here briefly and was visible as a quarter-second of clipped dialog.
    update({ height: 874, offsetTop: 200 });
    expect(published()).toEqual({ top: "0px", height: "874px" });
  });

  it("never publishes an offset larger than the hidden strip", () => {
    const update = install({ height: 874, offsetTop: 0 });
    renderHook(() => useVisualViewport());

    // 444px hidden, and an offset claiming more than that. Clamped to what is actually hidden.
    update({ height: 430, offsetTop: 600 });
    expect(published().top).toBe("444px");
  });

  it("clears the properties on unmount so the stylesheet fallbacks take over", () => {
    install({ height: 874, offsetTop: 0 });
    const { unmount } = renderHook(() => useVisualViewport());
    expect(published().height).toBe("874px");

    unmount();
    expect(published()).toEqual({ top: "", height: "" });
  });

  it("publishes nothing when there is no visualViewport", () => {
    Reflect.deleteProperty(window, "visualViewport");
    renderHook(() => useVisualViewport());
    expect(published()).toEqual({ top: "", height: "" });
  });
});

// THE HOME INDICATOR IS BEHIND THE KEYBOARD, so reserving it while the keyboard is up costs a strip
// of the space above it. Measured on the Operator's recording as a dead gap between the card's foot
// and the keyboard's accessory bar, with the next field clipped by the card's edge (quince#762).
describe("useVisualViewport reserves the bottom inset only when it is on screen", () => {
  it("drops the inset while the keyboard is up", () => {
    const update = install({ height: 874, offsetTop: 0 });
    renderHook(() => useVisualViewport());
    expect(document.documentElement.style.getPropertyValue("--vv-pad-bottom")).toBe(
      "max(1rem,var(--safe-bottom))",
    );

    update({ height: 430, offsetTop: 200 });
    expect(document.documentElement.style.getPropertyValue("--vv-pad-bottom")).toBe("1rem");
  });

  it("puts it back when the keyboard goes", () => {
    const update = install({ height: 874, offsetTop: 0 });
    renderHook(() => useVisualViewport());
    update({ height: 430, offsetTop: 200 });
    update({ height: 874, offsetTop: 0 });
    expect(document.documentElement.style.getPropertyValue("--vv-pad-bottom")).toBe(
      "max(1rem,var(--safe-bottom))",
    );
  });
});
