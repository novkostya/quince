import { readFileSync } from "node:fs";
import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { Input } from "./input";
import { Select } from "./select";
import { fieldBase } from "./field";

// WHAT THIS PINS, AND WHAT IT CANNOT: quince#616.
//
// iOS Safari zooms the page in whenever a focused control computes below 16px. quince's fields were
// 14px, so tapping the login field — the first control a phone user ever touches — jumped the
// layout and left it scrolled. `text-base sm:text-sm` is 16px on phones and 14px from `sm` up, so
// the zoom stops and desktop density is untouched.
//
// THESE ASSERTIONS CANNOT PROVE SAFARI DOES NOT ZOOM. Only a device can, and that check is owed to
// the Operator rather than claimed here. What a class assertion catches is the failure that is
// actually likely: someone tidying the string back to a plain `text-sm` months from now, with no
// iPhone in the room and every gate green. That is worth pinning precisely because the suite is
// blind to the real symptom — the same shape as quince#512, found by using the product.
// WHAT CHANGED, AND WHY THIS FILE NOW ASSERTS THE THRESHOLD INSTEAD OF THE TRICK — quince#1155.
//
// The three assertions that stood here pinned `text-base sm:text-sm`, which was the MECHANISM for
// clearing 16px while desktop stayed at 14px. The measured type scale puts `text-sm` at 16px on
// every breakpoint, so the mechanism is no longer needed and the property it protected is met by
// the base scale. Pinning the mechanism would now FAIL the correct code and pass a hypothetical
// wrong one, which is the wrong way round for a guard.
//
// So this reads the token instead. A future edit that tunes the scale back under 16px fails here,
// naming the reason, rather than shipping the zoom-on-focus bug to a phone nobody has in the room.
describe("full-width form controls compute at least 16px, however the scale is tuned", () => {
  // `--type-sm` is what `text-sm` resolves to (`index.css` maps it into Tailwind's `--text-sm`).
  // Read from the stylesheet rather than from a rendered box, because jsdom computes no cascade —
  // this asserts the token a real browser would use.
  // Path from the vitest root (`ui/`), not from `import.meta.url` — Vite serves test modules over a
  // non-file scheme, so `new URL(…, import.meta.url)` throws `The URL must be of scheme file` here.
  const tokens = readFileSync("src/styles/tokens.css", "utf8");

  it("the scale's `text-sm` step is at least 1rem, which is iOS Safari's zoom threshold", () => {
    const m = /--type-sm:\s*([\d.]+)rem/.exec(tokens);
    expect(m, "tokens.css must declare --type-sm in rem").not.toBeNull();
    expect(Number(m![1])).toBeGreaterThanOrEqual(1);
  });

  it("both controls carry text-sm, with no smaller responsive step below it", () => {
    const { container } = render(
      <div>
        <Input />
        <Select>
          <option value="auto">auto</option>
        </Select>
      </div>,
    );
    for (const el of container.querySelectorAll("input, select")) {
      const cls = el.className.split(/\s+/);
      expect(cls).toContain("text-sm");
      // A responsive step DOWN is the regression now: it would put a breakpoint back under the
      // threshold that the unconditional class currently clears everywhere.
      expect(cls).not.toContain("sm:text-xs");
      expect(cls).not.toContain("sm:text-2xs");
    }
  });

  // THE HEIGHT HALF (quince#619). Same idiom, same file, same regression shape: a plain `h-9` is
  // what this string carried, it reads as normal, and it is 36px unconditionally — which is what
  // `button.tsx` deliberately stopped doing at the qn.6a mobile pass. 16px of type in a 36px box
  // leaves about 10px of vertical padding in total, so the font change quince#616 shipped made this
  // close to required and then landed without it.
  it("both controls step 40px -> 36px at the sm breakpoint", () => {
    const { container } = render(
      <div>
        <Input />
        <Select>
          <option value="auto">auto</option>
        </Select>
      </div>,
    );
    for (const el of container.querySelectorAll("input, select")) {
      const tokens = el.className.split(/\s+/);
      expect(tokens).toContain("h-10");
      expect(tokens).toContain("sm:h-9");
      // The unconditional form is the regression, exactly as `text-sm` is above.
      expect(tokens).not.toContain("h-9");
    }
  });

  // THE POINT OF `fieldBase` IS THAT THERE IS ONE STRING, not that there is one component. The two
  // controls were a character-for-character duplicate before this; extracting a component while
  // leaving two copies of the responsive string would have rebuilt the same defect one size larger.
  // If a future edit inlines either class list, this fails.
  it("both controls render the shared string rather than a copy of it", () => {
    const { container } = render(
      <div>
        <Input />
        <Select>
          <option value="auto">auto</option>
        </Select>
      </div>,
    );
    for (const el of container.querySelectorAll("input, select")) {
      for (const token of fieldBase.split(/\s+/)) {
        expect(el.className.split(/\s+/)).toContain(token);
      }
    }
  });
});
