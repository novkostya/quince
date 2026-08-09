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

function nextFrame(): Promise<void> {
  return new Promise((resolve) => requestAnimationFrame(() => resolve()));
}

afterEach(() => {
  document.body.replaceChildren();
});

describe("useScrollFocusIntoView centres the focused field in the scroll region", () => {
  it("scrolls a field below the fold to the middle rather than to the edge", async () => {
    // Field at 380..420, below the region's bottom edge at 400. Its centre is 400, the region's is
    // 250, so 150px of scroll centres it. The browser's own `nearest` would move 20px — just enough
    // to touch the boundary, which is the behaviour the Operator photographed.
    const { container, field } = stand(380);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    await nextFrame();
    expect(container.scrollTop).toBe(150);
  });

  it("scrolls a field above the fold back down by the same rule", async () => {
    // Field at 40..80, above the region. Centre 60 against 250 — a negative delta.
    const { container, field } = stand(40);
    container.scrollTop = 500;
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    await nextFrame();
    expect(container.scrollTop).toBe(310);
  });

  it("aligns the top of a field taller than the region, since it cannot be centred", async () => {
    // 320 tall in a 300 region: centring would hide the label and the caret, which are at the top.
    const { container, field } = stand(380, 320);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    await nextFrame();
    expect(container.scrollTop).toBe(280);
  });

  it("leaves the region alone when the focus is outside it", async () => {
    const { container } = stand(380);
    const outside = document.createElement("input");
    document.body.appendChild(outside);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    outside.focus();
    await nextFrame();
    expect(container.scrollTop).toBe(0);
  });

  it("does nothing at all when there is no container", async () => {
    renderHook(() => useScrollFocusIntoView({ current: null }));
    await nextFrame();
    // Reaching here without throwing IS the assertion: a dialog that is closed has no card, and the
    // hook must not care.
    expect(true).toBe(true);
  });
});
