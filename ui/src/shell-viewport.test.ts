import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// WHAT THIS PINS, AND WHAT IT CANNOT: quince#649.
//
// On a phone the shell is `h-full` off this height chain and `overflow-hidden`, and the document
// never scrolls — so <main> is the only scroll region. That makes the unit the height is expressed
// in load-bearing: `dvh` tracks the mobile toolbars, and any window in which the computed height
// exceeds the visible height puts the bottom of the page where NO scroller reaches it. `svh` is the
// small viewport and, crucially, is STATIC — there is no window in which it can go stale.
//
// THIS CANNOT PROVE THE CLIP IS GONE. jsdom has no layout and no viewport units; headless Chromium
// has no mobile toolbar to expand, so `100svh`, `100dvh` and `100vh` are all the same number there.
// No test in this repository can observe the trigger. Confirmation on a real iPhone is OWED to the
// Operator — scroll to the bottom, force a toolbar expand, and see whether the clip appears on the
// transition. What this catches is the regression that is actually likely: a later tidy-up
// "unifying" the two rules onto one unit, which is exactly the shape that made `dvh` the phone rule
// in the first place.
//
// Asserted against the SOURCE rather than a rendered style, for the same reason `styles/tokens.test.ts`
// is: Vite injects this stylesheet at build time and vitest never processes it into a document, so
// reading the file is the honest way to assert a declaration exists. Resolved from the project root
// rather than `import.meta.url`, which is not a `file:` URL under vitest's jsdom environment.
const css = readFileSync(resolve(process.cwd(), "src/index.css"), "utf8");

// The base (phone) rule is the first `html, body, #root` block; the desktop one is inside the
// `min-width: 640px` media query. Split on the media query so the two cannot be confused — the
// whole point of this file is that they carry DIFFERENT units on purpose.
const [phoneHalf, desktopHalf] = css.split("@media (min-width: 640px)");

describe("the phone shell is sized so it cannot outgrow the visible viewport", () => {
  it("declares the height chain in svh", () => {
    const rule = /html,\s*body,\s*#root\s*\{([\s\S]*?)\n\}/.exec(phoneHalf)?.[1] ?? "";
    expect(rule).toMatch(/height:\s*100svh/);
  });

  it("does not use dvh for the phone height, which is the defect", () => {
    const rule = /html,\s*body,\s*#root\s*\{([\s\S]*?)\n\}/.exec(phoneHalf)?.[1] ?? "";
    // Scoped to the declaration, not the block: the comment above it explains dvh at length and
    // must stay readable. A test that forbade the word would forbid documenting the decision.
    expect(rule).not.toMatch(/height:\s*100dvh/);
  });

  it("keeps a unitless fallback ahead of it, for a browser that knows neither", () => {
    const rule = /html,\s*body,\s*#root\s*\{([\s\S]*?)\n\}/.exec(phoneHalf)?.[1] ?? "";
    expect(rule.indexOf("height: 100%")).toBeGreaterThanOrEqual(0);
    expect(rule.indexOf("height: 100%")).toBeLessThan(rule.indexOf("height: 100svh"));
  });
});

describe("the desktop rule deliberately keeps dvh", () => {
  // NOT symmetry for its own sake. On desktop the DOCUMENT scrolls, so a box taller than the
  // viewport is scrolled to rather than clipped — `dvh` has no failure mode there, and `svh` would
  // only shorten the page. Asserted so that "unify the units" reads as a deliberate reversal
  // rather than as tidying.
  it("still declares min-height in dvh above the breakpoint", () => {
    expect(desktopHalf ?? "").toMatch(/min-height:\s*100dvh/);
  });
});
