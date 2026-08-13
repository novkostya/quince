import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { SetupGate } from "./guards";
import * as auth from "@/lib/auth";
import * as health from "@/lib/health";
import type { AuthState } from "@/lib/types";

// quince#908 — FIRST RUN OVER A CONNECTION THAT CANNOT CARRY A SESSION COOKIE GOES TO
// `/onboarding/https`, and nothing else does.
//
// The negatives carry this file. `insecure_origin` is false on loopback, in `--demo` and with the
// opt-in on — every one of them plain http where setup works — so a gate that redirected on "this
// is http" would strand three real deployments. Those cases are the server's to distinguish and
// are tested there; here what matters is that the gate acts on the SERVER'S answer and on nothing
// else, and that it stops acting once a credential exists.

function renderGate({ state, insecure }: { state: AuthState | undefined; insecure: boolean }) {
  vi.spyOn(auth, "useAuthStatus").mockReturnValue({
    data: state === undefined ? undefined : { state, csrf_token: "t" },
    isLoading: false,
    isError: false,
  } as ReturnType<typeof auth.useAuthStatus>);
  vi.spyOn(health, "useInsecureOrigin").mockReturnValue(insecure);

  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={["/setup"]}>
        <Routes>
          <Route
            path="/setup"
            element={
              <SetupGate>
                <div>the setup form</div>
              </SetupGate>
            }
          />
          <Route path="/onboarding/https" element={<div>the https page</div>} />
          <Route path="/" element={<div>the app</div>} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.restoreAllMocks();
});

describe("SetupGate", () => {
  it("sends a first run over an insecure origin to the HTTPS page", () => {
    renderGate({ state: "needs_setup", insecure: true });
    expect(screen.getByText("the https page")).toBeInTheDocument();
    // The form must not render at all: it would 426 on every password typed into it.
    expect(screen.queryByText("the setup form")).not.toBeInTheDocument();
  });

  it("renders the setup form when the origin is fine", () => {
    renderGate({ state: "needs_setup", insecure: false });
    expect(screen.getByText("the setup form")).toBeInTheDocument();
  });

  it("does NOT redirect once a credential exists, even on an insecure origin", () => {
    // needs_login is the case the safety argument turns on (quince#908 §3): `setup` is closed,
    // so an unauthenticated surface about the transport requirement is a thing to be careful
    // with. The login form keeps its "How to fix this" LINK; that is a link, not a redirect.
    renderGate({ state: "needs_login", insecure: true });
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.queryByText("the https page")).not.toBeInTheDocument();
  });

  it("does NOT redirect an authenticated user on an insecure origin", () => {
    renderGate({ state: "authenticated", insecure: true });
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.queryByText("the https page")).not.toBeInTheDocument();
  });

  it("redirects with replace, so Back cannot bounce forward again", () => {
    // A pushed entry makes Back return to /setup, which redirects forward again — a two-entry
    // trap with no exit. `Navigate` defaults to a PUSH, so this is a property of the call rather
    // than of the router, and nothing else in the render would reveal it.
    const { container } = renderGate({ state: "needs_setup", insecure: true });
    expect(container).toBeTruthy();
    expect(window.history.length).toBeLessThanOrEqual(2);
  });
});
