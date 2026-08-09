import { describe, it, expect, afterEach, vi } from "vitest";
import { initKeyboardScrollReset } from "./keyboardScrollReset";

// quince#649 — the reproduction the issue never had, stated as numbers.
type FakeViewport = { height: number; offsetTop: number };

function install(v: FakeViewport): (next: Partial<FakeViewport>) => void {
  const listeners = new Set<() => void>();
  const vv = {
    ...v,
    addEventListener: (_: string, cb: () => void) => listeners.add(cb),
    removeEventListener: (_: string, cb: () => void) => listeners.delete(cb),
  };
  Object.defineProperty(window, "visualViewport", { value: vv, configurable: true });
  return (next) => {
    Object.assign(vv, next);
    for (const cb of listeners) cb();
  };
}

function focusField(): HTMLInputElement {
  const field = document.createElement("input");
  document.body.appendChild(field);
  field.focus();
  return field;
}

afterEach(() => {
  Reflect.deleteProperty(window, "visualViewport");
  document.body.replaceChildren();
  vi.restoreAllMocks();
});

describe("initKeyboardScrollReset puts the viewport back when the keyboard goes", () => {
  it("resets the offset iOS left behind", () => {
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const update = install({ height: 874, offsetTop: 0 });
    initKeyboardScrollReset();

    const field = focusField();
    update({ height: 430, offsetTop: 200 }); // keyboard opens
    expect(scrollTo).not.toHaveBeenCalled();

    // Keyboard closes and the field blurs, but the offset is left behind — the shell is now shifted
    // up the layout viewport, which is the gap at the bottom of quince#649.
    field.blur();
    update({ height: 874, offsetTop: 200 });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it("does NOT reset while focus is moving between two fields", () => {
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const update = install({ height: 874, offsetTop: 0 });
    initKeyboardScrollReset();

    focusField();
    update({ height: 430, offsetTop: 200 });

    // The transient: iOS reports full height for a frame mid-switch, while a field is still focused.
    // Resetting here would yank the page out from under someone who is still typing.
    update({ height: 874, offsetTop: 200 });
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("does nothing when the keyboard closed cleanly", () => {
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const update = install({ height: 874, offsetTop: 0 });
    initKeyboardScrollReset();

    const field = focusField();
    update({ height: 430, offsetTop: 200 });
    field.blur();
    update({ height: 874, offsetTop: 0 }); // iOS put it back itself
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("resets after a tap on a button dismisses the keyboard", () => {
    const scrollTo = vi.spyOn(window, "scrollTo").mockImplementation(() => {});
    const update = install({ height: 874, offsetTop: 0 });
    initKeyboardScrollReset();

    focusField();
    update({ height: 430, offsetTop: 200 });

    const button = document.createElement("button");
    document.body.appendChild(button);
    button.focus(); // focus moved to something that raises no keyboard
    update({ height: 874, offsetTop: 200 });
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it("is inert with no visualViewport, and its disposer is safe to call", () => {
    Reflect.deleteProperty(window, "visualViewport");
    const dispose = initKeyboardScrollReset();
    expect(() => dispose()).not.toThrow();
  });
});
