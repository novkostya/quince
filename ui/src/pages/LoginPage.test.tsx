import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider, QueryCache } from "@tanstack/react-query";
import { LoginPage } from "./LoginPage";
import type { Health } from "@/lib/types";

const get = vi.fn();
vi.mock("@/lib/api", () => ({
  api: { get: (p: string) => get(p), post: () => Promise.resolve({}) },
}));

beforeEach(() => get.mockReset());

function renderLogin() {
  // A rejecting query is EXPECTED in one test; swallow it at the cache so vitest does not report
  // the deliberate failure as an unhandled rejection.
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
    queryCache: new QueryCache({ onError: () => {} }),
  });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter>
        <LoginPage />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

function health(mode: Health["mode"], demoResetMinutes?: number): Health {
  return { status: "ok", version: "0.0.0-dev", mode, demo_reset_minutes: demoResetMinutes };
}

describe("LoginPage demo copy", () => {
  // Story 5: a visitor to the public demo has no way to learn the password unless the login screen
  // says it. Asserted on the rendered password rather than on the mode, because the mode being
  // right is not the story — the visitor being able to sign in is.
  it("prints the password when the server reports public_demo", async () => {
    get.mockResolvedValue(health("public_demo"));
    renderLogin();
    await waitFor(() => expect(screen.getByText("demo")).toBeInTheDocument());
    expect(screen.getByText(/public demo/i)).toBeInTheDocument();
  });

  // The half that protects the SHIPPING product. Demo copy leaking into a real deployment would
  // print a password that is not the operator's, on the screen where they are trying to sign in.
  it.each(["normal", "demo"] as const)("prints nothing extra in %s mode", async (mode) => {
    get.mockResolvedValue(health(mode));
    renderLogin();
    await waitFor(() => expect(screen.getByText("Enter your admin password.")).toBeInTheDocument());
    expect(screen.queryByText("demo")).not.toBeInTheDocument();
    expect(screen.queryByText(/public demo/i)).not.toBeInTheDocument();
  });

  // SILENCE ON ANYTHING THAT IS NOT A POSITIVE public_demo. The risk is one-directional:
  // demo copy in a real deployment prints a password that is not the operator's, on the
  // screen where they are trying to sign in. An unrecognised mode is the general case of
  // that, and it also covers a server newer than this UI.
  it("prints nothing for an unrecognised mode", async () => {
    get.mockResolvedValue({ status: "ok", version: "0.0.0-dev", mode: "something-new" });
    renderLogin();
    await waitFor(() => expect(screen.getByText("Enter your admin password.")).toBeInTheDocument());
    expect(screen.queryByText("demo")).not.toBeInTheDocument();
  });
});

// Story 6. The reset is DESTRUCTIVE — a visitor mid-click is signed out and their edits are gone —
// so the warning is the deliverable and the schedule is the detail. Every test here asserts the
// rendered sentence rather than the hook, because "the mode is right" was never the story.
describe("LoginPage reset notice", () => {
  it("states the interval the server reported", async () => {
    get.mockResolvedValue(health("public_demo", 30));
    renderLogin();
    await waitFor(() =>
      expect(screen.getByText(/resets every 30 minutes/i)).toBeInTheDocument(),
    );
    expect(screen.getByText(/will be wiped/i)).toBeInTheDocument();
  });

  // THE DEGRADE, and the one that decides the shape of this feature. An interval nobody configured
  // costs the SCHEDULE, never the warning: the option the spec's Rule check argues hardest against
  // is saying nothing at all, because the instance with no declared interval is exactly the one
  // where a visitor is most likely to be surprised.
  it.each([undefined, 0])("still warns when the interval is %s", async (minutes) => {
    get.mockResolvedValue(health("public_demo", minutes));
    renderLogin();
    await waitFor(() => expect(screen.getByText(/resets periodically/i)).toBeInTheDocument());
    expect(screen.getByText(/will be wiped/i)).toBeInTheDocument();
    expect(screen.queryByText(/every/i)).not.toBeInTheDocument();
  });

  // The shipping product's login screen must never carry it. `--demo` is included because it looks
  // like the case that should: it runs fixtures and throws its state away at exit — but nothing
  // restarts it on a schedule, so "resets every 30 minutes" there is simply false. The server
  // already refuses to report an interval outside public_demo; this asserts the UI would not print
  // one even if a server did.
  it.each(["normal", "demo"] as const)("prints no reset notice in %s mode", async (mode) => {
    get.mockResolvedValue(health(mode, 30));
    renderLogin();
    await waitFor(() => expect(screen.getByText("Enter your admin password.")).toBeInTheDocument());
    expect(screen.queryByText(/resets/i)).not.toBeInTheDocument();
    expect(screen.queryByText(/will be wiped/i)).not.toBeInTheDocument();
  });

  // An unrecognised mode stands in for the two cases this file cannot drive directly: a server
  // newer than this UI, and a health probe that failed (which resolves to `normal`). Both must be
  // silent, never a false claim.
  //
  // NOT asserted by rejecting the mock, deliberately. A rejecting query here is reported by vitest
  // as an unhandled rejection at the mock's own line and survives `QueryCache.onError`; quince#532
  // chased that and settled on this substitute rather than leaving a flaky test in the suite. The
  // `catch` inside `useHealth`'s queryFn is what makes the substitution sound — a failed probe and
  // an unknown mode reach this component through the same `mode !== "public_demo"` branch.
  it("prints no reset notice for an unrecognised mode", async () => {
    get.mockResolvedValue({ status: "ok", version: "0.0.0-dev", mode: "something-new", demo_reset_minutes: 30 });
    renderLogin();
    await waitFor(() => expect(screen.getByText("Enter your admin password.")).toBeInTheDocument());
    expect(screen.queryByText(/resets/i)).not.toBeInTheDocument();
  });
});
