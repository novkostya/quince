import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { Card } from "./card";

// SOURCE-PRESENT IS NOT DOM-PRESENT, and until this file nothing said so mechanically.
//
// `Card` forwards `data-*` one by one. TypeScript does not check hyphenated JSX attributes against
// a component's props type, so `<Card data-storage-name={x}>` typechecks whether or not Card does
// anything with it — and for the whole life of `StorageCard` it did not. The attribute was in the
// source, absent from the browser, and `gates-ui` was green throughout. It took a Playwright
// selector that could not match to find it (quince#598).
//
// These tests are the cheap half of that lesson: every attribute the component claims to forward
// is asserted to REACH THE DOM. They cannot catch a caller's typo — nothing can, at this layer —
// but they do catch the failure that actually happened, which is a prop declared and then dropped
// on the floor during a refactor.
describe("Card data-* forwarding", () => {
  it("forwards every declared data-* attribute to the rendered element", () => {
    const { container } = render(
      <Card data-testid="t" data-storage-name="shuttle" data-udid="UDID-A">
        x
      </Card>,
    );
    const el = container.firstElementChild;
    expect(el?.getAttribute("data-testid")).toBe("t");
    expect(el?.getAttribute("data-storage-name")).toBe("shuttle");
    expect(el?.getAttribute("data-udid")).toBe("UDID-A");
  });

  // An omitted attribute must not render as the string "undefined", which is what a naive
  // forward produces and which then matches `[data-udid]` selectors on cards that have no udid.
  it("omits an attribute that was not passed, rather than rendering undefined", () => {
    const { container } = render(<Card data-testid="t">x</Card>);
    const el = container.firstElementChild;
    expect(el?.hasAttribute("data-storage-name")).toBe(false);
    expect(el?.hasAttribute("data-udid")).toBe(false);
  });
});
