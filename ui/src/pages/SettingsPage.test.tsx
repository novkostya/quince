import { describe, it, expect, vi, beforeEach } from "vitest";
// `waitFor` from testing-library, NOT `vi.waitFor` — the former wraps its polling in `act`, so the
// query resolving mid-wait does not produce an "update was not wrapped in act(...)" warning.
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { SettingsPage } from "./SettingsPage";
import { api } from "@/lib/api";

// THE STRUCTURAL HALF OF quince#631, and the half that would silently rot.
//
// `AppLayout` puts `min-w-0` on `<main>` so a wide child scrolls inside itself rather than moving
// the page, and says so in a comment. That guard is only as strong as the shortest link below it,
// and this page broke the chain: a grid item defaults to `min-width: auto`, so the column would not
// shrink below the intrinsic width of the config dump inside it.
//
// A `min-w-0` with no test is exactly the kind of class a later tidy-up removes as meaningless —
// it has no visible effect until the day it does. jsdom cannot prove the page stops scrolling
// (no layout, no widths), so this asserts the containment is DECLARED, which is what can be lost.
function renderPage() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      {/* A ROUTER IS REQUIRED SINCE qn.6m SLICE 6 — this page carries a `<Link>` to
          `/settings/auth`, and `<Link>` outside a Router throws on a null context. */}
      <MemoryRouter>
        <SettingsPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("SettingsPage grid containment", () => {
  it("gives both grid columns min-w-0, so one long config line cannot widen the page", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      config: {
        backup: { preferred_transport: "usb", require_encryption: true },
        storage: null,
        sessions: { allow_insecure_transport: false },
        reconcile: { interval_minutes: 360 },
        ui: { theme: "system" },
      },
      warnings: [],
      source: { path: "/data/config.yml", mtime: null },
    });

    const { container } = renderPage();

    const grid = await waitFor(() => {
      const g = container.querySelector("div.grid");
      if (!g) throw new Error("grid not rendered yet");
      return g;
    });

    const columns = [...grid.children];
    expect(columns).toHaveLength(2);
    // BOTH, not just the one that overflowed. The editor renders config values too.
    for (const col of columns) {
      expect(col.className.split(/\s+/)).toContain("min-w-0");
    }
  });
});

// THE LAYOUT COMPLAINT, PINNED — Operator-reported 2026-08-12. The Sign-in row used to sit ABOVE
// this grid, which pushed BOTH columns down and moved "Current configuration" off the top of the
// page where it had always been.
//
// Asserted as DOM POSITION rather than as CSS, because that is what the fix actually is: the row
// lives in the first column, so it displaces nothing in the second; and on a phone the columns
// stack in source order, so second-in-source is last-on-screen. jsdom computes no layout, so a
// class assertion would prove nothing here — containment is the real property.
describe("where the Sign-in row sits", () => {
  it("is inside the FIRST column, so it cannot push the second one down", async () => {
    vi.spyOn(api, "get").mockResolvedValue({
      config: {
        backup: { preferred_transport: "usb", require_encryption: true },
        storage: null,
        sessions: { ttl_minutes: 60, allow_insecure_transport: false },
        reconcile: { interval_minutes: 360 },
        ui: { theme: "system" },
      },
      warnings: [],
      source: { path: "/data/config.yml", mtime: null },
    });

    const { container } = renderPage();
    // WAIT FOR THE CONTENT, NOT THE GRID. The grid is UNCONDITIONAL since the layout fix, so
    // waiting for it resolves immediately — before the query settles and while both columns are
    // still empty. That is exactly how the first version of this test failed.
    await screen.findByRole("heading", { name: "Current configuration" });
    const grid = container.querySelector("div.grid") as HTMLElement;
    const [left, right] = [...grid.children] as HTMLElement[];

    const link = screen.getByRole("link", { name: /sign-in/i });
    expect(left).toContainElement(link);
    expect(right).not.toContainElement(link);

    // And the config dump is the SECOND column — last once the columns stack on a phone.
    expect(right).toContainElement(screen.getByRole("heading", { name: "Current configuration" }));
  });

  // The columns are unconditional now; only their CONTENTS are guarded. A grid that appeared only
  // with `data` would take the Sign-in row with it, which is quince#853's defect returning.
  it("renders the grid — and the row — even when the config cannot be loaded", async () => {
    vi.spyOn(api, "get").mockRejectedValue(new Error("config is unreadable"));

    const { container } = renderPage();
    await screen.findByText(/could not load configuration/i);

    const grid = container.querySelector("div.grid");
    expect(grid).not.toBeNull();
    expect(grid).toContainElement(screen.getByRole("link", { name: /sign-in/i }));
  });
});

// THE ROW ENDS WHERE THE FIELDS DO — Operator-reported 2026-08-12, desktop only. The left column is
// wider than `ConfigEditor`'s form, so a full-width row overhung everything beneath it.
//
// ASSERTED AS "THE SAME TOKEN", NOT AS "max-w-md". Hardcoding the value twice is two places to
// change and one of them gets missed; comparing them fails the moment they stop agreeing, which is
// the property that actually matters. jsdom computes no layout, so alignment cannot be measured —
// sharing the constraint is the closest true statement available here.
it("the Sign-in row is constrained to the same width as the config form", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    config: {
      backup: { preferred_transport: "usb", require_encryption: true },
      storage: null,
      sessions: { ttl_minutes: 60, allow_insecure_transport: false },
      reconcile: { interval_minutes: 360 },
      ui: { theme: "system" },
    },
    warnings: [],
    source: { path: "/data/config.yml", mtime: null },
  });

  const { container } = renderPage();
  await screen.findByRole("heading", { name: "Current configuration" });

  const maxWidth = (el: Element) =>
    el.className.split(/\s+/).find((c) => c.startsWith("max-w-"));

  const row = screen.getByRole("link", { name: /sign-in/i });
  const form = container.querySelector("form");
  expect(form).not.toBeNull();

  expect(maxWidth(row)).toBeDefined();
  expect(maxWidth(row)).toBe(maxWidth(form as Element));
});

// THE ROUTE EXISTED AND NOTHING LINKED TO IT (Operator-reported 2026-08-18). qn.12 registered
// `/settings/notifications` in the router and never gave Settings an entry, so the only way to the
// whole notifications feature was typing the URL. A screen nobody can reach is a screen that does
// not exist, and no existing test could fail: the router had the route, the page rendered, and its
// own suite mounted it directly.
it("offers a way to reach Notifications, which is the only route to the feature", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    config: {
      backup: { preferred_transport: "usb", require_encryption: true },
      storage: null,
      sessions: { ttl_minutes: 60, allow_insecure_transport: false },
      reconcile: { interval_minutes: 360 },
      ui: { theme: "system" },
    },
    warnings: [],
    source: { path: "/data/config.yml", mtime: null },
  });

  renderPage();

  const row = await screen.findByRole("link", { name: /notifications/i });
  expect(row).toHaveAttribute("href", "/settings/notifications");
});

// AND IT SHARES Sign-in'S CONSTRAINT. Two rows of the same kind, one narrower than the other, is the
// drift the Sign-in width test exists to catch — asserted between the rows rather than against a
// literal, for the reason that test already gives.
it("the Notifications row is constrained like the Sign-in row", async () => {
  vi.spyOn(api, "get").mockResolvedValue({
    config: {
      backup: { preferred_transport: "usb", require_encryption: true },
      storage: null,
      sessions: { ttl_minutes: 60, allow_insecure_transport: false },
      reconcile: { interval_minutes: 360 },
      ui: { theme: "system" },
    },
    warnings: [],
    source: { path: "/data/config.yml", mtime: null },
  });

  renderPage();
  await screen.findByRole("heading", { name: "Current configuration" });

  const maxWidth = (el: Element) =>
    el.className.split(/\s+/).find((c) => c.startsWith("max-w-"));

  const signIn = screen.getByRole("link", { name: /sign-in/i });
  const notifications = screen.getByRole("link", { name: /notifications/i });

  expect(maxWidth(notifications)).toBeDefined();
  expect(maxWidth(notifications)).toBe(maxWidth(signIn));
});
