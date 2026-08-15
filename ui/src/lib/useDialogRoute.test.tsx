import { describe, it, expect } from "vitest";
import { render, screen, act } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { useDialogRoute } from "./useDialogRoute";

// The e2e proves the behaviour that matters — open, scroll, close, land back where you were — in a
// real browser with a real history. What it cannot reach is the branch where there is NOTHING of
// ours to go back to: a link somebody sent, or a reload with the dialog already open. That branch
// must not call `navigate(-1)`, because doing so leaves the app, and a test is the only thing
// standing between it and a user who arrives by URL.
function Harness() {
  const { open, onOpenChange } = useDialogRoute("encryption");
  const location = useLocation();
  return (
    <div>
      <span data-testid="url">{location.pathname + location.search}</span>
      <span data-testid="open">{String(open)}</span>
      <button onClick={() => onOpenChange(true)}>open</button>
      <button onClick={() => onOpenChange(false)}>close</button>
    </div>
  );
}

function at(entries: string[]) {
  render(
    <MemoryRouter initialEntries={entries}>
      <Harness />
    </MemoryRouter>,
  );
  return {
    url: () => screen.getByTestId("url").textContent,
    open: () => screen.getByTestId("open").textContent,
    click: (name: string) => act(() => screen.getByRole("button", { name }).click()),
  };
}

describe("useDialogRoute", () => {
  it("is closed on a plain URL and open on one that names the dialog", () => {
    const a = at(["/devices/x"]);
    expect(a.open()).toBe("false");
  });

  it("opens by adding the param and closes by returning to the entry before it", () => {
    const a = at(["/devices/x"]);
    a.click("open");
    expect(a.open()).toBe("true");
    expect(a.url()).toBe("/devices/x?dialog=encryption");

    // A POP, not a push of the closed URL — which is what lets the browser put the scroll offset
    // back on a real history (quince#931). Here it shows up as landing on the identical entry.
    a.click("close");
    expect(a.open()).toBe("false");
    expect(a.url()).toBe("/devices/x");
  });

  it("keeps other query state when it opens and when it closes", () => {
    const a = at(["/devices/x?tab=history"]);
    a.click("open");
    expect(a.url()).toBe("/devices/x?tab=history&dialog=encryption");
    a.click("close");
    expect(a.url()).toBe("/devices/x?tab=history");
  });

  it("DROPS THE PARAM IN PLACE when the dialog was not opened from here", () => {
    // Arriving straight at the dialog: a shared link, or a reload. There is no entry of ours behind
    // this one, so going back would leave quince altogether.
    const a = at(["/devices/x?dialog=encryption"]);
    expect(a.open()).toBe("true");
    a.click("close");
    expect(a.open()).toBe("false");
    expect(a.url()).toBe("/devices/x");
  });

  it("ignores a request to move to the state it is already in", () => {
    const a = at(["/devices/x"]);
    a.click("close"); // Radix fires this on some paths; it must not push, pop or replace anything
    expect(a.url()).toBe("/devices/x");
    expect(a.open()).toBe("false");
  });

  it("does not answer to another dialog's name", () => {
    const a = at(["/devices/x?dialog=pair"]);
    expect(a.open()).toBe("false");
  });
});
