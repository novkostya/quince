import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, act } from "@testing-library/react";
import { MemoryRouter, useNavigate } from "react-router-dom";
import { useScrollReset } from "./useScrollReset";

// THE `POP` CASE IS THE ONE WORTH A TEST, and it asserts that we do NOTHING.
//
// That is an odd-looking gate, so it is written out: on a traversal the browser restores the
// document itself (`history.scrollRestoration` is `"auto"`, and `story12` gates that it stays that
// way). Any write here fights it, and the shape of the mistake is not a crash — it is a Back that
// lands at the top, which is the exact bug quince#838 was filed for. A regression would therefore
// look like a fix.
//
// jsdom HAS NO `scrollTo`, so the spy is also the implementation. That is fine for this claim: what
// is being tested is which navigations call it, not what a scroll does.

let navigate: ReturnType<typeof useNavigate>;

function Harness() {
  useScrollReset();
  navigate = useNavigate();
  return null;
}

let scrollTo: ReturnType<typeof vi.fn>;

beforeEach(() => {
  scrollTo = vi.fn();
  vi.stubGlobal("scrollTo", scrollTo);
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("useScrollReset", () => {
  it("does not scroll on the first render — a cold load is the browser's to restore", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Harness />
      </MemoryRouter>,
    );
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("scrolls a pushed screen to the top", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Harness />
      </MemoryRouter>,
    );

    act(() => navigate("/devices/UDID-1"));
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });

  it("leaves a Back traversal entirely alone", () => {
    render(
      <MemoryRouter initialEntries={["/"]}>
        <Harness />
      </MemoryRouter>,
    );

    act(() => navigate("/devices/UDID-1"));
    scrollTo.mockClear();

    act(() => navigate(-1));
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("ignores a replace that keeps the same path", () => {
    render(
      <MemoryRouter initialEntries={["/login"]}>
        <Harness />
      </MemoryRouter>,
    );

    // `?next=` on the login redirect is the live instance: same screen, new search. Scrolling it to
    // the top would move the page under someone who navigated nowhere.
    act(() => navigate("/login?next=%2Fsettings", { replace: true }));
    expect(scrollTo).not.toHaveBeenCalled();
  });

  it("scrolls a replace that changes the path", () => {
    render(
      <MemoryRouter initialEntries={["/devices"]}>
        <Harness />
      </MemoryRouter>,
    );

    // The `/devices` → `/` redirect is a replace to a genuinely different screen.
    act(() => navigate("/", { replace: true }));
    expect(scrollTo).toHaveBeenCalledWith(0, 0);
  });
});
