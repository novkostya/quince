import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { RequireAdmin, ScopedHome } from "./guards";
import { Sidebar } from "@/components/Sidebar";
import { api } from "@/lib/api";
import { authStatusKey } from "@/lib/auth";
import type { AuthStatus } from "@/lib/types";

// qn.13 slice 8d / D8 — THE SHELL FOLLOWS THE PRINCIPAL (ruled on quince#1443).
//
// Two defects this closes, both filed because a screen was asserting something untrue rather than
// merely missing a feature: Home told a scoped holder *"No devices connected"* about their own
// device, and Settings was enterable into pages whose every read then errored at them.
//
// NONE OF THIS IS THE ENFORCEMENT. D8: *unreachable is a server property, not a routing one.* Every
// route behind these guards refuses a scoped principal at the API regardless. These tests are about
// what the shell OFFERS.
//
// SYNTHETIC UDIDS. A real one is Operator-private and never enters a fixture.

const UDID = "udid-fixture-0001";

function stageStatus(over: Partial<AuthStatus>) {
  vi.spyOn(api, "get").mockResolvedValue({ state: "authenticated", csrf_token: "t", ...over });
}

/**
 * renderWithStatus mounts `ui` and WAITS FOR THE AUTH STATUS TO LAND before returning.
 *
 * THE WAIT IS THE POINT, and it is the lesson from quince#1452 and quince#1465 applied before it
 * costs anything. Every assertion below is about what is or is NOT on the screen, and a guard that
 * renders `Loading` until the query resolves is empty at first paint — so an assertion made before
 * the data arrives passes for a component that has not decided anything yet, and would pass equally
 * for a guard that was deleted.
 *
 * Waiting on the CACHE rather than on a rendered element is what makes it usable for the cases where
 * the correct answer is that nothing renders.
 */
async function renderWithStatus(ui: React.ReactElement, at = "/") {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const out = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[at]}>{ui}</MemoryRouter>
    </QueryClientProvider>,
  );
  await waitFor(() => expect(qc.getQueryData(authStatusKey)).toBeDefined());
  return out;
}

beforeEach(() => vi.restoreAllMocks());
afterEach(() => vi.restoreAllMocks());

function routes() {
  return (
    <Routes>
      <Route path="/" element={<ScopedHome><div>the devices list</div></ScopedHome>} />
      <Route path="/devices/:udid" element={<div>a device page</div>} />
      <Route path="/settings" element={<RequireAdmin><div>settings</div></RequireAdmin>} />
    </Routes>
  );
}

describe("Home follows the principal", () => {
  it("sends a scoped holder to their own device page", async () => {
    stageStatus({ scope: { udid: UDID } });

    await renderWithStatus(routes(), "/");

    expect(await screen.findByText("a device page")).toBeInTheDocument();
    // THE FALSE STATEMENT IS GONE: the devices list is not what they land on.
    expect(screen.queryByText("the devices list")).not.toBeInTheDocument();
  });

  it("leaves the ADMIN on the devices list — the control", async () => {
    // Without this, the test above passes for a guard that redirects everybody, which would take
    // the admin's Home away from them.
    stageStatus({ scope: null });

    await renderWithStatus(routes(), "/");

    expect(await screen.findByText("the devices list")).toBeInTheDocument();
    expect(screen.queryByText("a device page")).not.toBeInTheDocument();
  });
});

describe("Settings does not resolve for a scoped holder", () => {
  it("redirects a scoped holder away from a TYPED settings URL", async () => {
    // D8's *hidden, not merely unlinked*. Removing the nav item leaves the URL working, and a
    // bookmark is exactly how a household member arrives at a screen that errors at them.
    stageStatus({ scope: { udid: UDID } });

    await renderWithStatus(routes(), "/settings");

    expect(await screen.findByText("a device page")).toBeInTheDocument();
    expect(screen.queryByText("settings")).not.toBeInTheDocument();
  });

  it("admits the ADMIN to settings — the control", async () => {
    stageStatus({ scope: null });

    await renderWithStatus(routes(), "/settings");

    expect(await screen.findByText("settings")).toBeInTheDocument();
  });
});

describe("the nav", () => {
  it("has no Settings item for a scoped holder", async () => {
    stageStatus({ scope: { udid: UDID } });

    await renderWithStatus(<Sidebar />, "/");

    expect(screen.queryByRole("link", { name: /settings/i })).not.toBeInTheDocument();
    // HOME STAYS, and that is not incidental: a scoped holder HAS a Home — their device page — so
    // an empty nav would be a different wrong answer.
    expect(screen.getByRole("link", { name: /home/i })).toBeInTheDocument();
  });

  it("keeps Settings for the ADMIN — the control", async () => {
    stageStatus({ scope: null });

    await renderWithStatus(<Sidebar />, "/");

    expect(await screen.findByRole("link", { name: /settings/i })).toBeInTheDocument();
  });
});
