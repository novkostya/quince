import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor, fireEvent } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { SignOutButton } from "./SignOutButton";
import { api, APIError } from "@/lib/api";
import { authStatusKey } from "@/lib/auth";

// qn.6m slice 2. The button itself is four lines; everything asserted here is about the CACHE it
// leaves behind, because that is where signing out goes wrong in a way nobody sees in review.

const navigate = vi.fn();
vi.mock("react-router-dom", async () => {
  const actual = await vi.importActual<typeof import("react-router-dom")>("react-router-dom");
  return { ...actual, useNavigate: () => navigate };
});

function renderButton() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Pre-populate the shape a signed-in shell actually holds: the auth status the guards read, plus
  // a protected payload standing in for devices/config/storages.
  qc.setQueryData(authStatusKey, { state: "authenticated", csrf_token: "tok" });
  qc.setQueryData(["devices"], [{ udid: "u1", name: "a phone" }]);
  const view = render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <SignOutButton />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return { qc, view };
}

beforeEach(() => {
  vi.restoreAllMocks();
  navigate.mockClear();
});

describe("signing out", () => {
  it("posts to the logout endpoint and lands on /login", async () => {
    const post = vi.spyOn(api, "post").mockResolvedValue(undefined);
    renderButton();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/login", { replace: true }));
    expect(post).toHaveBeenCalledWith("/api/auth/logout");
  });

  // THE BOUNCE THIS PREVENTS, AND IT IS THE WHOLE REASON THE SEED EXISTS. `LoginGate` sends an
  // `authenticated` status straight back to `/`. Navigate while the cache still says authenticated
  // and the user lands on Home again — a sign-out button that visibly does nothing.
  it("seeds needs_login BEFORE navigating, so LoginGate cannot bounce back to Home", async () => {
    vi.spyOn(api, "post").mockResolvedValue(undefined);
    const { qc } = renderButton();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(qc.getQueryData(authStatusKey)).toEqual({ state: "needs_login", csrf_token: "" });
  });

  // The predicate, asserted in BOTH directions. Dropping everything (`qc.clear()`) would take the
  // seed with it and flash `LoginGate`'s "Loading…" over the form; dropping nothing would leave the
  // previous session's data resident behind the login screen.
  it("drops protected payloads and keeps the auth seed", async () => {
    vi.spyOn(api, "post").mockResolvedValue(undefined);
    const { qc } = renderButton();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

    await waitFor(() => expect(navigate).toHaveBeenCalled());
    expect(qc.getQueryData(["devices"])).toBeUndefined();
    expect(qc.getQueryData(authStatusKey)).not.toBeUndefined();
  });
});

describe("what a failure means", () => {
  // A 401 SAYS THE SESSION IS ALREADY GONE, which is precisely what the button was asked to achieve.
  // Reporting it would tell somebody who IS signed out that they are not.
  it("treats a 401 as the goal state and signs out anyway", async () => {
    vi.spyOn(api, "post").mockRejectedValue(new APIError(401, "unauthorized", "authentication required"));
    const { qc } = renderButton();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith("/login", { replace: true }));
    expect(qc.getQueryData(authStatusKey)).toEqual({ state: "needs_login", csrf_token: "" });
    expect(screen.queryByText(/could not sign out/i)).not.toBeInTheDocument();
  });

  // EVERY OTHER FAILURE LEAVES THE SESSION LIVE, so clearing local state would be a lie told about
  // the one action whose entire value is that it took effect.
  it("does NOT navigate or clear the cache when the server refuses", async () => {
    vi.spyOn(api, "post").mockRejectedValue(new APIError(500, "internal", "could not log out"));
    const { qc } = renderButton();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

    expect(await screen.findByText(/could not sign out/i)).toBeInTheDocument();
    expect(navigate).not.toHaveBeenCalled();
    expect(qc.getQueryData(authStatusKey)).toEqual({ state: "authenticated", csrf_token: "tok" });
    expect(qc.getQueryData(["devices"])).not.toBeUndefined();
  });

  // The failure message is NOT `sm:`-only. On a phone the label is hidden and the icon is the whole
  // button, so a hidden error would make a failed sign-out completely silent there.
  it("shows the failure at every breakpoint", async () => {
    vi.spyOn(api, "post").mockRejectedValue(new APIError(500, "internal", "could not log out"));
    renderButton();

    fireEvent.click(screen.getByRole("button", { name: "Sign out" }));

    const msg = await screen.findByText(/could not sign out/i);
    expect(msg.className).not.toMatch(/hidden/);
  });
});

// The button is an ICON ALONE on a phone — the text span is `sm:`-only — so without an explicit name
// it is "button" to a screen reader and unfindable by role+name.
it("has an accessible name even with the label hidden", () => {
  renderButton();
  expect(screen.getByRole("button", { name: "Sign out" })).toHaveAttribute("aria-label", "Sign out");
});
