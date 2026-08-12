import { describe, it, expect } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { Sidebar } from "./Sidebar";

// EXACTLY ONE NAV ITEM IS LIT, ON EVERY ROUTE IN THE SHELL — quince#838.
//
// This reads like polish and is not. `Home` used to carry `end`, so on `/devices/:udid`,
// `/storage/:name` and `/storage/new` NOTHING was lit: the selected pill vanished on every push into
// a detail screen and came back on every pop. The Operator reported it as the header flickering, and
// a screen recording is what identified it — two builds of positioning and compositing work went past
// it first, because a bar that CHANGES looks exactly like a bar that fails to paint.
//
// The general form, from `springback`'s `docs/ios-spa-notes.md` §4: "serialise the whole bar … and
// assert it is byte-identical across a traversal. If it is not, that is your flicker, and no amount
// of positioning work will touch it." The e2e does the traversal; this states the property route by
// route, which is where a new route will break it — adding one to the router is a change nobody
// would think to make here.
//
// `aria-current` IS THE PROBE, and it is the same fact the pill draws rather than a parallel one:
// both come from `owns` in the component. Asserting the class string instead would pass on a build
// that lit the pill and told a screen reader nothing.
function renderAt(pathname: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[pathname]}>
        <Sidebar />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function currentLabel(): string {
  const marked = screen.getAllByRole("link").filter((el) => el.getAttribute("aria-current") === "page");
  // ONE, never two: `/settings` and `/` both matching would light the whole bar, which is the
  // opposite failure and just as wrong.
  expect(marked).toHaveLength(1);
  return marked[0].textContent ?? "";
}

describe("Sidebar selection", () => {
  it.each([
    ["/", "Home"],
    ["/devices/00008110-000A1B2C3D4E5F60", "Home"],
    ["/devices", "Home"],
    ["/storage/internal", "Home"],
    ["/storage/new", "Home"],
    ["/settings", "Settings"],
    ["/settings/auth", "Settings"],
  ])("%s lights %s", (pathname, expected) => {
    renderAt(pathname);
    expect(currentLabel()).toBe(expected);
  });

  // THE PREFIX TRAP, stated because `startsWith` is the obvious way to write `owns` and this is what
  // it gets wrong. No such route exists today; the point is that the matcher is a segment test, so
  // one can be added without silently lighting the wrong item.
  it("does not treat a longer name as a section it owns", () => {
    renderAt("/settingsomething");
    const marked = screen.getAllByRole("link").filter((el) => el.getAttribute("aria-current") === "page");
    expect(marked).toHaveLength(0);
  });
});
