import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import { MemoryRouter, Routes, Route } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";

import { LoginGate, SetupGate } from "./guards";
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

  it("does NOT send a returning visitor to setup, even on an insecure origin", () => {
    // THIS GATE'S JOB IS FIRST RUN, and `needs_login` is not it — `SetupGate` sends such a visitor
    // to `/`, which is what this asserts.
    //
    // ITS COMMENT SAID THE LOGIN FORM KEEPS A LINK RATHER THAN A REDIRECT, AND THAT IS NO LONGER THE
    // RULING (Operator, 2026-08-16, quince#1069). `LoginGate` now redirects a `needs_login` visitor
    // on an insecure origin to the same page — see its own tests below. quince#908 §3 is untouched:
    // the control on that page is `firstRun`-only, so the returning reader arrives at instructions
    // and no affordance. Corrected rather than deleted, because a stale rule in a test comment is
    // how the next change to this file gets argued from the wrong premise.
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

// quince#1069 — THE RETURNING-USER HALF OF quince#923's REDIRECT, ruled by the Operator from the rig
// on 2026-08-16 and left open by qn.6f's spec as *"deliberately NOT settled"* until somebody could
// see it working.
//
// THE REASON IS WHERE THE PASSWORD GOES, NOT THAT THE FORM WOULD FAIL. `refuseInsecureOrigin`
// answers 426 BEFORE the credential is examined — but the browser has already put it on the wire in
// clear. The form invited somebody to hand their admin password to the network in order to be told
// they could not sign in here.
describe("LoginGate", () => {
  function renderLogin({ state, insecure }: { state: AuthState | undefined; insecure: boolean }) {
    vi.spyOn(auth, "useAuthStatus").mockReturnValue({
      data: state === undefined ? undefined : { state, csrf_token: "t" },
      isLoading: false,
      isError: false,
    } as ReturnType<typeof auth.useAuthStatus>);
    vi.spyOn(health, "useInsecureOrigin").mockReturnValue(insecure);

    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    return render(
      <QueryClientProvider client={qc}>
        <MemoryRouter initialEntries={["/login"]}>
          <Routes>
            <Route
              path="/login"
              element={
                <LoginGate>
                  <div>the login form</div>
                </LoginGate>
              }
            />
            <Route path="/onboarding/https" element={<div>the https page</div>} />
            <Route path="/setup" element={<div>the setup form</div>} />
            <Route path="/" element={<div>the app</div>} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );
  }

  it("sends a returning visitor on an insecure origin to the HTTPS page", () => {
    renderLogin({ state: "needs_login", insecure: true });
    expect(screen.getByText("the https page")).toBeInTheDocument();
    // The form must not render: every password typed into it crosses the network in clear and is
    // then refused.
    expect(screen.queryByText("the login form")).not.toBeInTheDocument();
  });

  // LOOPBACK KEEPS THE FORM, and this is the assertion that stops the redirect becoming a lockout:
  // `insecure_origin` is false at the machine itself, so the admin standing at the console — the one
  // recovery path when plain HTTP is refused everywhere else — is never sent away from it.
  it("renders the form when the origin is fine", () => {
    renderLogin({ state: "needs_login", insecure: false });
    expect(screen.getByText("the login form")).toBeInTheDocument();
  });

  // THE STATE CHECKS COME FIRST, deliberately. A signed-in reader on plain http keeps working
  // (quince#1080 is whether the server should stop them, and it is not this gate's question);
  // bouncing them into an instructional page would be a second wrong answer rather than a fix.
  it("leaves an authenticated reader alone, even on an insecure origin", () => {
    renderLogin({ state: "authenticated", insecure: true });
    expect(screen.getByText("the app")).toBeInTheDocument();
    expect(screen.queryByText("the https page")).not.toBeInTheDocument();
  });

  it("still sends first run to setup, which the transport check must not preempt", () => {
    renderLogin({ state: "needs_setup", insecure: true });
    expect(screen.getByText("the setup form")).toBeInTheDocument();
  });
});
