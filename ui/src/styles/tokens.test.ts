import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

// WHAT THIS PINS, AND WHAT IT CANNOT: quince#645.
//
// `color-scheme` is the only part of the theme our tokens cannot paint. It tells the browser which
// system palette to draw the internals IT owns from — the `<select>` chevron, number spinners,
// checkboxes, scrollbars. Nothing declared it, so the UA assumed a light page and drew a dark
// chevron onto a dark control, and the indicator was near-invisible in dark mode.
//
// THIS CANNOT PROVE THE CHEVRON IS VISIBLE. jsdom does not draw, and the control's indicator is
// painted by the browser from the system palette — there is nothing in any DOM to inspect. Only a
// device can confirm it, and that check is owed. What this catches is the regression that is
// actually likely: a tidy-up of the token file taking one or both declarations with it, because
// they look unlike every other line in there and are the only two that are not custom properties.
//
// Asserted against the SOURCE rather than a rendered style: Vite injects this stylesheet at build
// time and vitest does not process it into any document, so reading the file is the honest way to
// assert it is declared. The compiled-CSS check lives in the PR evidence, where a real build ran.
// Resolved from the project root, not from `import.meta.url`: under vitest's jsdom environment
// `import.meta.url` is not a `file:` URL, so `fileURLToPath` throws. Measured, not guessed.
const tokens = readFileSync(resolve(process.cwd(), "src/styles/tokens.css"), "utf8");

describe("the theme declares color-scheme for both modes", () => {
  it("declares the light scheme on :root", () => {
    const root = /:root\s*\{([\s\S]*?)\n\}/.exec(tokens)?.[1] ?? "";
    expect(root).toMatch(/color-scheme:\s*light/);
  });

  it("declares the dark scheme on :root.dark", () => {
    const dark = /:root\.dark\s*\{([\s\S]*?)\n\}/.exec(tokens)?.[1] ?? "";
    expect(dark).toMatch(/color-scheme:\s*dark/);
  });

  // BOTH, not just the dark one. Declaring only `dark` leaves the light theme inheriting whatever
  // the OS is set to, so a user in OS-dark with quince set to light would get dark form internals
  // on a white page — the same bug with the palettes swapped.
  it("declares both, so neither mode inherits the OS setting by accident", () => {
    expect(tokens.match(/color-scheme:/g) ?? []).toHaveLength(2);
  });
});
