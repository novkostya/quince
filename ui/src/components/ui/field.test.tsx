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
describe("full-width form controls are 16px on mobile", () => {
  it("Input steps 16px -> 14px at the sm breakpoint", () => {
    const { container } = render(<Input />);
    const cls = container.querySelector("input")?.className ?? "";
    expect(cls).toContain("text-base");
    expect(cls).toContain("sm:text-sm");
  });

  it("Select steps 16px -> 14px at the sm breakpoint", () => {
    const { container } = render(
      <Select>
        <option value="auto">auto</option>
      </Select>,
    );
    const cls = container.querySelector("select")?.className ?? "";
    expect(cls).toContain("text-base");
    expect(cls).toContain("sm:text-sm");
  });

  // A bare `text-sm` is the specific regression: it is what both controls carried, it is what a
  // reviewer's eye reads as normal, and it is what Tailwind renders as 14px unconditionally.
  it("neither control carries an unconditional text-sm", () => {
    const { container } = render(
      <div>
        <Input />
        <Select>
          <option value="auto">auto</option>
        </Select>
      </div>,
    );
    for (const el of container.querySelectorAll("input, select")) {
      expect(el.className.split(/\s+/)).not.toContain("text-sm");
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
