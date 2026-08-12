import { describe, it, expect, vi } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { Button } from "./button";

// THE DANGEROUS DEFAULT WAS THE ONE YOU GOT BY WRITING NOTHING (quince#828).
//
// HTML makes a `<button>` inside a `<form>` a submit button unless it says otherwise, so every
// `<Button>` in this product was correct only because each author remembered. quince#820 paid for
// that once — five explicit `type="button"` to wrap one dialog in a form — and quince#824 is the
// second instance.
//
// The behavioural assertions here go through a real `<form>` and a real click rather than reading
// the attribute, because the attribute is not the claim: the claim is that a button does not submit.
describe("Button type", () => {
  it("defaults to type=button, so a Button in a form does not submit", () => {
    const onSubmit = vi.fn((e: React.FormEvent) => e.preventDefault());
    render(
      <form onSubmit={onSubmit}>
        <Button>Cancel</Button>
      </form>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onSubmit).not.toHaveBeenCalled();
  });

  // THE CONTROL, without which the assertion above passes for a component that submits nothing ever.
  it("still submits when type=submit is asked for explicitly", () => {
    const onSubmit = vi.fn((e: React.FormEvent) => e.preventDefault());
    render(
      <form onSubmit={onSubmit}>
        <Button type="submit">Save</Button>
      </form>,
    );
    fireEvent.click(screen.getByRole("button", { name: "Save" }));
    expect(onSubmit).toHaveBeenCalledTimes(1);
  });

  it("renders the attribute, so the default is visible in the DOM and not only in behaviour", () => {
    render(<Button>Plain</Button>);
    expect(screen.getByRole("button", { name: "Plain" }).getAttribute("type")).toBe("button");
  });

  it("forwards an explicit type=reset rather than overriding it with the default", () => {
    render(<Button type="reset">Reset</Button>);
    expect(screen.getByRole("button", { name: "Reset" }).getAttribute("type")).toBe("reset");
  });

  // `asChild` renders whatever the caller passed. `type` is meaningless on an anchor and invalid
  // HTML there, so the default must not leak onto it — this is the case a naive `type ?? "button"`
  // on every path gets wrong.
  it("does NOT put a type on an asChild element, which is often an anchor", () => {
    render(
      <Button asChild>
        <a href="/devices">Devices</a>
      </Button>,
    );
    const el = screen.getByRole("link", { name: "Devices" });
    expect(el.tagName).toBe("A");
    expect(el.hasAttribute("type")).toBe(false);
  });

  // …but an explicit one is still the caller's decision about their own element.
  it("forwards an explicit type through asChild", () => {
    render(
      <Button asChild>
        <button type="submit">Go</button>
      </Button>,
    );
    expect(screen.getByRole("button", { name: "Go" }).getAttribute("type")).toBe("submit");
  });
});
