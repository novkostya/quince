import { describe, it, expect, afterEach } from "vitest";
import { renderHook } from "@testing-library/react";
import { useScrollFocusIntoView } from "./useScrollFocusIntoView";

// WHY THIS IS A UNIT TEST AND NOT A BROWSER ONE, STATED BECAUSE THE BROWSER ONE WAS TRIED FIRST.
//
// The e2e version of this assertion PASSES WITH THE HOOK REMOVED — measured, not assumed. Chromium's
// own scroll-into-view already leaves a comfortable gap at the viewport and content sizes the suite
// runs at, so the assertion is satisfied by the browser doing something else. A test that cannot
// fail is not coverage, and the honest response is to test the arithmetic where the numbers can be
// stated rather than to keep a green assertion that means nothing.
//
// What the browser test is still worth is the shape of the thing: it holds the whole path together
// (a real dialog, a real focus, a real scroll container). It is kept for that and not counted as
// proof of this behaviour.
function stand(fieldTop: number, fieldHeight = 40) {
  const container = document.createElement("div");
  const field = document.createElement("input");
  container.appendChild(field);
  document.body.appendChild(container);

  // Layout, stated: the scroll region runs 100..400 on screen, so its centre is 250.
  container.getBoundingClientRect = () => ({ top: 100, height: 300 }) as DOMRect;
  field.getBoundingClientRect = () => ({ top: fieldTop, height: fieldHeight }) as DOMRect;
  container.scrollTop = 0;
  return { container, field };
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("useScrollFocusIntoView moves a field only when it needs moving", () => {
  it("leaves a field that already has clearance exactly where it is", async () => {
    // 200..240 inside 100..400: 100px above, 160px below. This is the case that made the card jump a
    // full step every time focus moved between two fields that were both already on screen.
    const { container, field } = stand(200);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(0);
  });

  it("moves a field the browser left flush against the edge", async () => {
    // 370..400 — visible, and touching the bottom boundary, which is exactly where `nearest` leaves
    // it. Zero clearance below, so it is corrected: centre 385 against the region's 250.
    const { container, field } = stand(370, 30);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(135);
  });

  it("scrolls a field below the fold to the middle rather than to the edge", async () => {
    const { container, field } = stand(380);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(150);
  });

  it("scrolls a field above the fold back down by the same rule", async () => {
    const { container, field } = stand(40);
    container.scrollTop = 500;
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(310);
  });

  it("aligns the top of a field taller than the region, since it cannot be centred", async () => {
    // 320 tall in a 300 region: centring would hide the label and the caret, which are at the top.
    // It can never satisfy the clearance test either, which is correct — it always needs aligning.
    const { container, field } = stand(380, 320);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(280);
  });

  it("corrects inside the focus event, before anything is painted", async () => {
    // The first version deferred to the next animation frame, so the browser drew its own
    // scroll-into-view first and the card visibly snapped afterwards. Asserting synchronously is how
    // that is pinned: this test fails if the correction goes back behind a rAF.
    const { container, field } = stand(380);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(150); // no await — the work is done by the time focus() returns
  });

  it("leaves the region alone when the focus is outside it", async () => {
    const { container } = stand(380);
    const outside = document.createElement("input");
    document.body.appendChild(outside);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    outside.focus();
    expect(container.scrollTop).toBe(0);
  });

  it("does nothing at all when there is no container", async () => {
    renderHook(() => useScrollFocusIntoView({ current: null }));
    // Reaching here without throwing IS the assertion: a dialog that is closed has no card, and the
    // hook must not care.
    expect(true).toBe(true);
  });
});
