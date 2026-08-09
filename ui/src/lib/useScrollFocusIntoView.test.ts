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

describe("useScrollFocusIntoView moves a field only when it needs moving, and only as far as it must", () => {
  it("leaves a field that already has clearance exactly where it is", async () => {
    // 200..240 inside 100..400: 100px above, 160px below. This is the case that made the card jump a
    // full step every time focus moved between two fields that were both already on screen.
    const { container, field } = stand(200);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(0);
  });

  it("moves a field the browser left flush against the edge, by the shortfall alone", async () => {
    // 370..400 — visible, and touching the bottom boundary, which is exactly where `nearest` leaves
    // it. Zero clearance below, so it moves by the 24 it is missing and not one pixel more. Centring
    // would have moved it 135, which is the difference the Operator felt as a jump.
    const { container, field } = stand(370, 30);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(24);
  });

  it("scrolls a field below the fold just far enough to clear the edge", async () => {
    // 380..420 against a region ending at 400: 20 past it, plus the 24 gutter.
    const { container, field } = stand(380);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(44);
  });

  it("scrolls a field above the fold back down by the same rule", async () => {
    // 40..80 against a region starting at 100: 60 short, plus the gutter, in the other direction.
    const { container, field } = stand(40);
    container.scrollTop = 500;
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(416);
  });

  it("aligns the top of a field too tall to hold a gutter at both ends", async () => {
    // 320 tall in a 300 region: there is no position that satisfies both gutters, so show its top,
    // which is where the label and the caret are.
    const { container, field } = stand(380, 320);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(256);
  });

  it("corrects inside the focus event, before anything is painted", async () => {
    // The first version deferred to the next animation frame, so the browser drew its own
    // scroll-into-view first and the card visibly snapped afterwards. Asserting synchronously is how
    // that is pinned: this test fails if the correction goes back behind a rAF.
    const { container, field } = stand(380);
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(44); // no await — the work is done by the time focus() returns
  });

  it("does not move for a button, because that swallows the tap that focused it", async () => {
    // A button is focused BY A TAP. Scrolling inside that tap moves it out from under the finger
    // between mousedown and mouseup, they land on different elements, and no `click` is fired at
    // all. `gates-ui-e2e` caught this deterministically: `Test helper` was pressed and its handler
    // never ran, so the result it produces never appeared.
    const container = document.createElement("div");
    const button = document.createElement("button");
    container.appendChild(button);
    document.body.appendChild(container);
    container.getBoundingClientRect = () => ({ top: 100, height: 300 }) as DOMRect;
    button.getBoundingClientRect = () => ({ top: 380, height: 40 }) as DOMRect;
    container.scrollTop = 0;
    renderHook(() => useScrollFocusIntoView({ current: container }));

    button.focus();
    expect(container.scrollTop).toBe(0);
  });

  it("does not move for a checkbox either — nothing that cannot raise a keyboard", async () => {
    const { container, field } = stand(380);
    field.type = "checkbox";
    renderHook(() => useScrollFocusIntoView({ current: container }));

    field.focus();
    expect(container.scrollTop).toBe(0);
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
