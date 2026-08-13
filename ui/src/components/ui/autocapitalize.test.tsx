import { describe, it, expect } from "vitest";
import { render } from "@testing-library/react";
import { Input } from "./input";

// quince#818 follow-up — Operator-reported from a phone, 2026-08-13.
//
// WHAT THIS PINS, AND WHAT IT CANNOT. iOS capitalises the first letter of a text field by default
// and opens the keyboard shifted. Every free-text field quince has is a case-sensitive technical
// identifier — a path, a ZFS dataset, a hostname, a remote user — so the platform default produces
// `Pool/backups`, `Nas.local`, `Zfsuser`. Each fails somewhere else later, and **the failure never
// names the capital letter**: a dataset that does not exist, a host that does not resolve, a user
// with no `authorized_keys` entry.
//
// THESE ASSERTIONS CANNOT PROVE IOS OBEYS THEM. Only a device can, and that is owed to the Operator
// rather than claimed here — the same split `field.test.tsx` draws for the 16px rule. What they
// catch is the failure that is actually likely: a new field, or someone tidying the attributes away,
// with no phone in the room and every gate green.
describe("text inputs do not fight case-sensitive identifiers", () => {
  it("Input defaults to no autocapitalisation, autocorrect or spellcheck", () => {
    const { container } = render(<Input aria-label="x" />);
    const el = container.querySelector("input");

    expect(el?.getAttribute("autocapitalize")).toBe("none");
    expect(el?.getAttribute("autocorrect")).toBe("off");
    expect(el?.getAttribute("spellcheck")).toBe("false");
  });

  // THE DEFAULT MUST BE OVERRIDABLE, which is what lets the one human-language field in the product
  // — a passkey's nickname — opt back in rather than needing a second component. `{...props}`
  // spreading last is the mechanism; this is the assertion that keeps it spreading last.
  it("a caller can opt back in, for the one field that takes human language", () => {
    const { container } = render(
      <Input aria-label="x" autoCapitalize="sentences" autoCorrect="on" spellCheck />,
    );
    const el = container.querySelector("input");

    expect(el?.getAttribute("autocapitalize")).toBe("sentences");
    expect(el?.getAttribute("autocorrect")).toBe("on");
    expect(el?.getAttribute("spellcheck")).toBe("true");
  });
});
