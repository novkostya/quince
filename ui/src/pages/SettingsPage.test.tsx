import { describe, it, expect, vi, beforeEach } from "vitest";
// `waitFor` from testing-library, NOT `vi.waitFor` — the former wraps its polling in `act`, so the
// query resolving mid-wait does not produce an "update was not wrapped in act(...)" warning.
import { render, waitFor } from "@testing-library/react";
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
      <SettingsPage />
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
        sessions: { ttl_minutes: 60, allow_insecure_transport: false },
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
